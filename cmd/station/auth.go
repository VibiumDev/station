package main

import (
	"context"
	"crypto/rand"
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

// identity is what tailnet mode asks: who is behind an address.
type identity func(ctx context.Context, remoteAddr string) (login string, tags []string, err error)

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

	mu       sync.Mutex
	sessions map[string]*session
	// failures are the recent wrong passwords and codes, by address and
	// by login, so a guesser moving between addresses is slowed too.
	failures map[string][]time.Time
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
	return &auth{cfg: cfg, db: db, who: who, tls: tls, proxied: proxied, delay: 500 * time.Millisecond, sessions: map[string]*session{}, failures: map[string][]time.Time{}}
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
// admin added with that login, regular or admin as the record says.
func (a *auth) identityUser(ctx context.Context, login string, tags []string) (station.User, bool) {
	if login == "" {
		return station.User{}, false
	}
	u, err := a.db.UserByLogin(ctx, login)
	if a.allowed(login, tags) {
		if err != nil {
			u = station.User{ID: station.NewID("usr"), Login: strings.ToLower(login), Admin: true}
			if err := a.db.PutUser(ctx, u); err != nil {
				log.Printf("identity: making a user for %s: %v", login, err)
				return station.User{}, false
			}
		} else if !u.Admin && !u.Locked {
			u.Admin = true // --allow is the operator's word
			a.db.PutUser(ctx, u)
		}
		return u, !u.Locked
	}
	return u, err == nil && !u.Locked
}

// Authenticate finds the principal: a bearer token, the session cookie, or
// a tailnet identity. A session still on its way in is nobody yet, with
// an error that says which step it is at.
func (a *auth) Authenticate(r *http.Request) (station.Principal, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		secret := strings.TrimSpace(h[7:])
		tok, err := a.db.LookupToken(ctx, secret)
		if err != nil {
			return station.Principal{}, station.ErrUnauthenticated
		}
		u, err := a.tokenUser(ctx, tok)
		if err != nil {
			return station.Principal{}, err
		}
		p := principalOf(u, "token:"+tok.Name)
		p.ReadOnly, p.Device = tok.ReadOnly, tok.Device
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
		login, tags, err := a.who(ctx, r.RemoteAddr)
		if err != nil {
			log.Printf("identity: %s: %v", r.RemoteAddr, err)
		} else if u, ok := a.identityUser(ctx, login, tags); ok {
			return principalOf(u, ""), nil
		} else if login != "" {
			log.Printf("identity: %s is %s %v, not in --allow and not a user", r.RemoteAddr, login, tags)
		}
	}
	return station.Principal{}, station.ErrUnauthenticated
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
// jam to drive it with an agent. Only people manage, never tokens; a
// read-only token never controls; a token bound to a device acts on that
// device alone. A call about no device in particular passes for anyone
// signed in, and the device's own check comes where the device is known.
func (a *auth) Authorize(ctx context.Context, p station.Principal, action station.Action, deviceID string) error {
	if p.Device != "" && deviceID != "" && deviceID != p.Device {
		return station.ErrForbidden
	}
	var need station.Level
	switch action {
	case station.ActionManage:
		if p.Admin && person(p) {
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
		if p.ReadOnly || !p.Admin {
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

// csrfOK checks a state-changing request from a browser session: the
// token must come in the X-CSRF-Token header or the csrf form field. Token
// and identity requests carry no cookie and need none.
func (a *auth) csrfOK(r *http.Request) bool {
	s := a.sessionOf(r)
	if s == nil {
		return true
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

// throttled says whether any of the keys, an address or a login, has
// failed too often lately; stale failures are forgotten on the way.
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

// keys are what a sign-in attempt counts against: the address and the login.
func (a *auth) keys(r *http.Request, login string) []string {
	return []string{"addr:" + a.clientIP(r), "login:" + strings.ToLower(strings.TrimSpace(login))}
}

// login checks a login and password and starts a session at the stage the
// user is at: a code due when they have an authenticator, setting one up
// when they have none yet. Failures count by address and by login.
func (a *auth) login(w http.ResponseWriter, r *http.Request, login, password, next string) (stage, bool) {
	keys := a.keys(r, login)
	if a.throttled(keys...) {
		time.Sleep(2 * a.delay)
		return 0, false
	}
	u, hash, err := a.db.Credentials(r.Context(), login)
	if err != nil || u.Locked || hash == "" || !checkPassword(hash, password) {
		a.fail(keys...)
		time.Sleep(a.delay)
		return 0, false
	}
	a.clear(keys...)
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
// saw the password through is not the one that is signed in.
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
	keys := a.keys(r, u.Login)
	if a.throttled(keys...) {
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
		a.fail(keys...)
		a.mu.Lock()
		s.tries++
		over := s.tries >= maxFailures
		a.mu.Unlock()
		if over {
			a.drop(r)
		}
		time.Sleep(a.delay)
		return false, backup, 0
	}
	a.clear(keys...)
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
	keys := a.keys(r, u.Login)
	if a.throttled(keys...) {
		time.Sleep(2 * a.delay)
		return nil, false
	}
	now := time.Now()
	step, good := totpCheck(s.secret, code, now)
	if !good {
		a.fail(keys...)
		time.Sleep(a.delay)
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
	a.clear(keys...)
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
