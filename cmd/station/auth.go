package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
)

const (
	sessionCookie = "station_session"
	sessionLife   = 30 * 24 * time.Hour
	// A sign-in on its way is short-lived: a code is due within minutes,
	// and an authenticator is set up within the half hour.
	codeLife    = 5 * time.Minute
	setupLife   = 30 * time.Minute
	maxFailures = 5
	lockout     = 15 * time.Minute
	// A login's wait grows with each miss after the first two, doubling
	// from a second up to the lockout; misses are forgotten after a
	// quiet lockout's worth of time.
	backoffAfter = 2
	// deviceCookie marks a browser that has signed in as a user before,
	// so a stranger guessing at that login cannot hold the user up.
	deviceCookie = "station_device"
	deviceLife   = 365 * 24 * time.Hour
)

// A session's stage: the password passed and a code from the
// authenticator or a backup code is due; the password passed but no
// authenticator is set up yet, so one must be before anything else; or
// signed in.
type stage int

const (
	stageSignedIn stage = iota
	stageCode
	stageSetup
)

// errCodeDue and errSetupDue say a session is on its way in: the pages
// send it to the step it is at, and the API treats it as nobody.
var (
	errCodeDue  = fmt.Errorf("%w: a code is due", station.ErrUnauthenticated)
	errSetupDue = fmt.Errorf("%w: an authenticator must be set up", station.ErrUnauthenticated)
)

// identity is what tailnet mode asks: who is behind an address, with
// the name their tailnet profile gives them.
type identity func(ctx context.Context, remoteAddr string) (login, name string, tags []string, err error)

// auth is the station's Authenticator and Authorizer: users signing in
// with a login, a password and a code from their authenticator, API
// tokens acting as their users, and Tailscale identities, allowed or
// known as users. Admins see and do everything; a regular user's teams
// decide what they may do with each device.
type auth struct {
	cfg *Config
	db  *sqlite.DB
	who identity // nil outside tailnet mode
	tls bool     // cookies are Secure when served over TLS
	// proxied says a TLS proxy is in front (--http): the client's address
	// is in the headers the proxy sets, not the connection's.
	proxied bool
	// delay is how long a wrong password or code waits before answering,
	// twice that when throttled; tests set it to zero.
	delay time.Duration

	// deviceKey signs the device cookies; the station's own key derives
	// it, so they outlive restarts.
	deviceKey []byte

	mu       sync.Mutex
	sessions map[string]*session
	// failures are the recent wrong passwords and codes by address, and
	// by device cookie for a browser that has one: five in the lockout
	// window lock the key hard.
	failures map[string][]time.Time
	// logins are the recent misses by login: each one lengthens the wait
	// before the next try is looked at, so a guesser moving between
	// addresses is slowed all the same, without locking the login shut.
	logins map[string]*misses
}

type misses struct {
	n    int
	last time.Time
}

type session struct {
	user    string // the user's id; the principal is read afresh each request
	csrf    string
	expires time.Time
	stage   stage
	secret  string // the authenticator secret being set up, until a code confirms it
	tries   int    // wrong codes so far
	next    string // where to go once signed in
}

func newAuth(cfg *Config, db *sqlite.DB, who identity, tls, proxied bool) *auth {
	var key [32]byte
	rand.Read(key[:])
	return &auth{cfg: cfg, db: db, who: who, tls: tls, proxied: proxied, delay: 500 * time.Millisecond, deviceKey: key[:],
		sessions: map[string]*session{}, failures: map[string][]time.Time{}, logins: map[string]*misses{}}
}

// clientIP is the address login failures are counted by: behind a proxy,
// what the proxy says (Fly-Client-IP, then the first X-Forwarded-For).
func (a *auth) clientIP(r *http.Request) string {
	if a.proxied {
		if h := r.Header.Get("Fly-Client-IP"); h != "" {
			return h
		}
		if h := r.Header.Get("X-Forwarded-For"); h != "" {
			first, _, _ := strings.Cut(h, ",")
			return strings.TrimSpace(first)
		}
	}
	return ip(r.RemoteAddr)
}

func randomID() string {
	var b [24]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func ip(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// principalOf is the principal a user signs in as, under a subject: the
// user's login as a rule, or a token's name.
func principalOf(u station.User, subject string) station.Principal {
	if subject == "" {
		subject = u.Login
	}
	return station.Principal{Subject: subject, Admin: u.Admin, User: u.ID, Teams: u.Teams}
}

// user finds the user a token acts as: its own, or the owner for a token
// from before tokens had owners. A locked user's tokens are no good.
func (a *auth) tokenUser(ctx context.Context, tok sqlite.APIToken) (station.User, error) {
	var u station.User
	var err error
	if tok.UserID != "" {
		u, err = a.db.User(ctx, tok.UserID)
	} else {
		u, err = a.db.UserByLogin(ctx, station.OwnerLogin)
	}
	if err != nil || u.Locked {
		return station.User{}, station.ErrUnauthenticated
	}
	return u, nil
}

// identityUser is the user behind a tailnet identity: one on --allow is an
// admin, made a user the first time they are seen; otherwise a user an
// admin added with that login, regular or admin as the record says. A
// user with no name yet takes the one their tailnet profile gives.
func (a *auth) identityUser(ctx context.Context, login, name string, tags []string) (station.User, bool) {
	if login == "" {
		return station.User{}, false
	}
	name = strings.TrimSpace(name)
	u, err := a.db.UserByLogin(ctx, login)
	if a.allowed(login, tags) {
		if err != nil {
			u = station.User{ID: station.NewID("usr"), Login: strings.ToLower(login), Name: name, Admin: true}
			if err := a.db.PutUser(ctx, u); err != nil {
				log.Printf("identity: making a user for %s: %v", login, err)
				return station.User{}, false
			}
			return u, true
		}
		if !u.Admin && !u.Locked {
			u.Admin = true // --allow is the operator's word
		}
	} else if err != nil {
		return u, false
	}
	if u.Name == "" && name != "" && !u.Locked {
		u.Name = name
	}
	a.db.PutUser(ctx, u)
	return u, !u.Locked
}

// Authenticate finds the principal: a bearer token, the session cookie, or
// a tailnet identity. A session still on its way in is nobody yet, with
// an error that says which step it is at.
func (a *auth) Authenticate(r *http.Request) (station.Principal, error) {
	ctx := r.Context()
	if hasBearer(r) {
		secret := strings.TrimSpace(r.Header.Get("Authorization")[7:])
		tok, err := a.db.LookupToken(ctx, secret)
		if err != nil {
			return station.Principal{}, station.ErrUnauthenticated
		}
		u, err := a.tokenUser(ctx, tok)
		if err != nil {
			return station.Principal{}, err
		}
		p := principalOf(u, "token:"+tok.Name)
		p.Token, p.ReadOnly, p.Device = tok.Level, tok.Level == station.TokenWatch, tok.Device
		return p, nil
	}
	if s := a.sessionOf(r); s != nil {
		u, err := a.db.User(ctx, s.user)
		switch {
		case err != nil || u.Locked:
			a.drop(r) // the user is gone or locked: so is the session
		case s.stage == stageCode:
			return station.Principal{}, errCodeDue
		case s.stage == stageSetup:
			return station.Principal{}, errSetupDue
		default:
			return principalOf(u, ""), nil
		}
	}
	if a.who != nil {
		login, name, tags, err := a.who(ctx, r.RemoteAddr)
		if err != nil {
			log.Printf("identity: %s: %v", r.RemoteAddr, err)
		} else if u, ok := a.identityUser(ctx, login, name, tags); ok {
			return principalOf(u, ""), nil
		} else if login != "" {
			log.Printf("identity: %s is %s %v, not in --allow and not a user", r.RemoteAddr, login, tags)
		}
	}
	return station.Principal{}, station.ErrUnauthenticated
}

// hasBearer says whether the request names a token.
func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
}

// drop forgets the request's session.
func (a *auth) drop(r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
}

// dropUser ends every session of a user, when they are locked or their
// sign-in is reset.
func (a *auth) dropUser(userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, s := range a.sessions {
		if s.user == userID {
			delete(a.sessions, id)
		}
	}
}

// sessionUser is the request's session, at whatever stage, and its user.
func (a *auth) sessionUser(r *http.Request) (*session, station.User, bool) {
	s := a.sessionOf(r)
	if s == nil {
		return nil, station.User{}, false
	}
	u, err := a.db.User(r.Context(), s.user)
	if err != nil || u.Locked {
		a.drop(r)
		return nil, station.User{}, false
	}
	return s, u, true
}

// hasUsers says whether anyone can sign in yet.
func (a *auth) hasUsers(ctx context.Context) bool {
	users, err := a.db.Users(ctx)
	return err == nil && len(users) > 0
}

// level is what a principal may do with a device: everything for an
// admin, else the highest level any of their teams was given.
func (a *auth) level(ctx context.Context, p station.Principal, deviceID string) station.Level {
	if p.Admin {
		return station.LevelJam
	}
	l, err := a.db.LevelFor(ctx, p.Teams, deviceID)
	if err != nil {
		return station.LevelNone
	}
	return l
}

func (a *auth) allowed(login string, tags []string) bool {
	for _, want := range a.cfg.Allow {
		if login != "" && strings.EqualFold(want, login) {
			return true
		}
		for _, t := range tags {
			if want == t {
				return true
			}
		}
	}
	return false
}

// Authorize: admins do everything, and a regular user does with a device
// what their teams' level allows: watch to view it, drive to control it,
// jam to drive it with an agent. A token acts within its level as well
// as its user's: a watch token never controls, a drive token never
// manages, and only an admin's admin token manages; a token bound to a
// device acts on that device alone. A call about no device in particular
// passes for anyone signed in, and the device's own check comes where
// the device is known.
func (a *auth) Authorize(ctx context.Context, p station.Principal, action station.Action, deviceID string) error {
	if p.Device != "" && deviceID != "" && deviceID != p.Device {
		return station.ErrForbidden
	}
	var need station.Level
	switch action {
	case station.ActionManage:
		if p.Admin && (person(p) || p.Token == station.TokenAdmin) {
			return nil
		}
		return station.ErrForbidden
	case station.ActionView:
		need = station.LevelWatch
	case station.ActionControl:
		need = station.LevelDrive
	case station.ActionBidi:
		need = station.LevelJam
	case station.ActionAdmin:
		if !p.Admin || !person(p) && p.Token != station.TokenAdmin {
			return station.ErrForbidden
		}
		return nil
	default:
		return station.ErrForbidden
	}
	if action != station.ActionView && p.ReadOnly {
		return station.ErrForbidden
	}
	if deviceID == "" || a.level(ctx, p, deviceID).AtLeast(need) {
		return nil
	}
	return station.ErrForbidden
}

func (a *auth) sessionOf(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[c.Value]
	if s == nil {
		return nil
	}
	if time.Now().After(s.expires) {
		delete(a.sessions, c.Value)
		return nil
	}
	return s
}

// csrfOK checks a state-changing request. From a browser session it
// carries the session's token in the X-CSRF-Token header or the csrf form
// field. With a bearer token it needs nothing more: no other site can set
// that header. Anything else stands on a tailnet identity, which a browser
// brings to every request whichever site's page sent it, so it must come
// from this station's own pages.
func (a *auth) csrfOK(r *http.Request) bool {
	s := a.sessionOf(r)
	if s == nil {
		return hasBearer(r) || new(http.CrossOriginProtection).Check(r) == nil
	}
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		got = r.FormValue("csrf")
	}
	return got != "" && got == s.csrf
}

func (a *auth) csrfToken(r *http.Request) string {
	if s := a.sessionOf(r); s != nil {
		return s.csrf
	}
	return ""
}

// withDeviceKey derives the device cookies' key from the station's own
// secret, so the cookies outlive a restart.
func (a *auth) withDeviceKey(secret []byte) *auth {
	sum := sha256.Sum256(append([]byte("station device cookie\x00"), secret...))
	a.deviceKey = sum[:]
	return a
}

// throttled says whether any of the keys has failed too often lately;
// stale failures are forgotten on the way.
func (a *auth) throttled(keys ...string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	locked := false
	for _, k := range keys {
		var recent []time.Time
		for _, t := range a.failures[k] {
			if now.Sub(t) < lockout {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(a.failures, k)
		} else {
			a.failures[k] = recent
		}
		if len(recent) >= maxFailures {
			locked = true
		}
	}
	return locked
}

func (a *auth) fail(keys ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		a.failures[k] = append(a.failures[k], now)
	}
}

func (a *auth) clear(keys ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range keys {
		delete(a.failures, k)
	}
}

// backoff is how long a login waits after its nth miss: nothing for the
// first two, then a second, doubling, up to the lockout.
func backoff(n int) time.Duration {
	if n <= backoffAfter {
		return 0
	}
	d := time.Second << uint(n-backoffAfter-1)
	if d > lockout || d <= 0 {
		return lockout
	}
	return d
}

// loginWait is how much longer a login must wait before its next try is
// looked at; misses older than the lockout are forgotten.
func (a *auth) loginWait(login string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.logins[login]
	if m == nil {
		return 0
	}
	now := time.Now()
	if now.Sub(m.last) > lockout {
		delete(a.logins, login)
		return 0
	}
	if until := m.last.Add(backoff(m.n)); now.Before(until) {
		return until.Sub(now)
	}
	return 0
}

func (a *auth) loginMiss(login string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.logins[login]
	if m == nil {
		m = &misses{}
		a.logins[login] = m
	}
	m.n++
	m.last = time.Now()
}

// gate is what one attempt at a login is measured by: the keys that lock
// hard after five misses, the address and the device cookie when the
// browser carries one for this user, and the login itself, whose wait
// grows with each miss unless the device cookie vouches for the browser.
type gate struct {
	hard    []string
	login   string
	vouched bool
}

func (a *auth) gateFor(r *http.Request, login, userID string) gate {
	g := gate{hard: []string{"addr:" + a.clientIP(r)}, login: strings.ToLower(strings.TrimSpace(login))}
	if nonce, ok := a.deviceOf(r, userID); ok {
		g.hard = append(g.hard, "device:"+nonce)
		g.vouched = true
	}
	return g
}

// blocked says whether the attempt is refused before being looked at.
func (a *auth) blocked(g gate) bool {
	return a.throttled(g.hard...) || !g.vouched && a.loginWait(g.login) > 0
}

func (a *auth) miss(g gate) {
	a.fail(g.hard...)
	if !g.vouched {
		a.loginMiss(g.login)
	}
	time.Sleep(a.delay)
}

func (a *auth) pass(g gate) {
	a.clear(g.hard...)
	a.mu.Lock()
	delete(a.logins, g.login)
	a.mu.Unlock()
}

// deviceToken is a device cookie's value for a user: the user, a nonce
// and a signature over both.
func (a *auth) deviceToken(userID string) string {
	nonce := randomID()[:16]
	return userID + "." + nonce + "." + a.deviceMAC(userID, nonce)
}

func (a *auth) deviceMAC(userID, nonce string) string {
	mac := hmac.New(sha256.New, a.deviceKey)
	mac.Write([]byte(userID + "." + nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// deviceOf is the nonce of the request's device cookie when it is this
// user's and genuine.
func (a *auth) deviceOf(r *http.Request, userID string) (string, bool) {
	c, err := r.Cookie(deviceCookie)
	if err != nil || userID == "" {
		return "", false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 || parts[0] != userID || !hmac.Equal([]byte(parts[2]), []byte(a.deviceMAC(parts[0], parts[1]))) {
		return "", false
	}
	return parts[1], true
}

// login checks a login and password and starts a session at the stage the
// user is at: a code due when they have an authenticator, setting one up
// when they have none yet. Misses count against the address and, for a
// browser without this user's device cookie, against the login. The right
// password forgets none of them. Only the step after it does, or someone
// with the password could guess at the code for ever by typing the
// password again between guesses.
func (a *auth) login(w http.ResponseWriter, r *http.Request, login, password, next string) (stage, bool) {
	u, hash, err := a.db.Credentials(r.Context(), login)
	g := a.gateFor(r, login, u.ID)
	if a.blocked(g) {
		time.Sleep(2 * a.delay)
		return 0, false
	}
	if err != nil || u.Locked || hash == "" || !checkPassword(hash, password) {
		a.miss(g)
		return 0, false
	}
	a.drop(r)
	st := stageCode
	if !u.TwoFactor {
		st = stageSetup
	}
	a.begin(w, u.ID, st, next)
	return st, true
}

// begin starts a session for a user at a stage and sets its cookie.
func (a *auth) begin(w http.ResponseWriter, user string, st stage, next string) *session {
	s := &session{user: user, csrf: randomID(), stage: st, next: next}
	id := randomID()
	a.mu.Lock()
	s.expires = time.Now().Add(stageLife(st))
	a.sessions[id] = s
	a.mu.Unlock()
	a.setCookie(w, id, stageLife(st))
	return s
}

// advance moves a session on to a stage under a fresh id, so the id that
// saw the password through is not the one that is signed in. A browser
// that signs all the way in is marked as this user's device.
func (a *auth) advance(w http.ResponseWriter, r *http.Request, s *session, st stage) {
	id := randomID()
	a.mu.Lock()
	if c, err := r.Cookie(sessionCookie); err == nil {
		delete(a.sessions, c.Value)
	}
	s.stage, s.tries = st, 0
	if st == stageSignedIn {
		s.secret = ""
	}
	s.expires = time.Now().Add(stageLife(st))
	a.sessions[id] = s
	a.mu.Unlock()
	a.setCookie(w, id, stageLife(st))
	if st == stageSignedIn {
		http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: a.deviceToken(s.user), Path: "/", HttpOnly: true, Secure: a.tls,
			SameSite: http.SameSiteLaxMode, MaxAge: int(deviceLife / time.Second)})
	}
}

func stageLife(st stage) time.Duration {
	switch st {
	case stageCode:
		return codeLife
	case stageSetup:
		return setupLife
	}
	return sessionLife
}

func (a *auth) setCookie(w http.ResponseWriter, id string, life time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: a.tls,
		SameSite: http.SameSiteLaxMode, MaxAge: int(life / time.Second)})
}

// code checks the second factor for a session awaiting one: six digits
// from the authenticator, good once per time step, or a backup code,
// spent on use. A wrong code counts like a wrong password, and after
// five the attempt is over and the session gone. It says whether the
// code passed, whether it was a backup code, and how many of those are
// left.
func (a *auth) code(w http.ResponseWriter, r *http.Request, code string) (ok, backup bool, left int) {
	s, u, found := a.sessionUser(r)
	if !found || s.stage != stageCode {
		return false, false, 0
	}
	ctx := r.Context()
	g := a.gateFor(r, u.Login, u.ID)
	if a.blocked(g) {
		time.Sleep(2 * a.delay)
		return false, false, 0
	}
	now := time.Now()
	if isBackupCode(code) {
		backup = true
		left, ok, _ = a.db.UseBackupCode(ctx, u.ID, hashCode(code))
	} else if secret, _, err := a.db.SecondFactor(ctx, u.ID); err == nil && secret != "" {
		if step, good := totpCheck(secret, code, now); good {
			ok, _ = a.db.AcceptStep(ctx, u.ID, step)
		}
	}
	if !ok {
		a.miss(g)
		a.mu.Lock()
		s.tries++
		over := s.tries >= maxFailures
		a.mu.Unlock()
		if over {
			a.drop(r)
		}
		return false, backup, 0
	}
	a.pass(g)
	a.db.TouchUser(ctx, u.ID, now)
	a.advance(w, r, s, stageSignedIn)
	return true, backup, left
}

// setupSecret is the authenticator secret a session is setting up, made
// the first time it is asked for and kept until a code confirms it.
func (a *auth) setupSecret(s *session) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s.secret == "" {
		s.secret = newTOTPSecret()
	}
	return s.secret
}

// startSetup sends a signed-in session back to setting up an
// authenticator, for a user replacing theirs.
func (a *auth) startSetup(w http.ResponseWriter, r *http.Request, s *session) {
	a.mu.Lock()
	s.secret = ""
	a.mu.Unlock()
	a.advance(w, r, s, stageSetup)
}

// cancelSetup leaves the setup where it was: a user who has an
// authenticator keeps it and is signed in again; one who has none has
// nothing to go back to.
func (a *auth) cancelSetup(w http.ResponseWriter, r *http.Request) bool {
	s, u, ok := a.sessionUser(r)
	if !ok || s.stage != stageSetup || !u.TwoFactor {
		return false
	}
	a.advance(w, r, s, stageSignedIn)
	return true
}

// confirmSetup checks a code against the secret being set up; when it
// is right the authenticator is the user's from now on, a fresh set of
// backup codes replaces any older one, and the session is signed in.
// It returns the backup codes, to be shown once.
func (a *auth) confirmSetup(w http.ResponseWriter, r *http.Request, code string) ([]string, bool) {
	s, u, found := a.sessionUser(r)
	if !found || s.stage != stageSetup || s.secret == "" {
		return nil, false
	}
	ctx := r.Context()
	g := a.gateFor(r, u.Login, u.ID)
	if a.blocked(g) {
		time.Sleep(2 * a.delay)
		return nil, false
	}
	now := time.Now()
	step, good := totpCheck(s.secret, code, now)
	if !good {
		a.miss(g)
		return nil, false
	}
	if err := a.db.SetSecondFactor(ctx, u.ID, s.secret, now); err != nil {
		log.Printf("second factor for %s: %v", u.Login, err)
		return nil, false
	}
	a.db.AcceptStep(ctx, u.ID, step)
	codes, err := a.newBackupCodes(ctx, u.ID)
	if err != nil {
		log.Printf("backup codes for %s: %v", u.Login, err)
		return nil, false
	}
	a.pass(g)
	a.db.TouchUser(ctx, u.ID, now)
	a.advance(w, r, s, stageSignedIn)
	return codes, true
}

// newBackupCodes replaces a user's backup codes and returns the new ones.
func (a *auth) newBackupCodes(ctx context.Context, userID string) ([]string, error) {
	codes := newBackupCodes()
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = hashCode(c)
	}
	return codes, a.db.SetBackupCodes(ctx, userID, hashes)
}

func (a *auth) logout(w http.ResponseWriter, r *http.Request) {
	a.drop(r)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: a.tls, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
