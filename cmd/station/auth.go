package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	maxFailures   = 5
	lockout       = 15 * time.Minute
)

// identity is what tailnet mode asks: who is behind an address.
type identity func(ctx context.Context, remoteAddr string) (login string, tags []string, err error)

// auth is the station's Authenticator and Authorizer: users signing in
// with a login and a password, API tokens acting as their users, and
// Tailscale identities, allowed or known as users. Admins see and do
// everything; a regular user's teams decide what they may do with each
// device.
type auth struct {
	cfg *Config
	db  *sqlite.DB
	who identity // nil outside tailnet mode
	tls bool     // cookies are Secure when served over TLS
	// proxied says a TLS proxy is in front (--http): the client's address
	// is in the headers the proxy sets, not the connection's.
	proxied bool

	mu       sync.Mutex
	sessions map[string]*session
	failures map[string][]time.Time
}

type session struct {
	user    string // the user's id; the principal is read afresh each request
	csrf    string
	expires time.Time
}

func newAuth(cfg *Config, db *sqlite.DB, who identity, tls, proxied bool) *auth {
	return &auth{cfg: cfg, db: db, who: who, tls: tls, proxied: proxied, sessions: map[string]*session{}, failures: map[string][]time.Time{}}
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
// from before tokens had owners. A disabled user's tokens are no good.
func (a *auth) tokenUser(ctx context.Context, tok sqlite.APIToken) (station.User, error) {
	var u station.User
	var err error
	if tok.UserID != "" {
		u, err = a.db.User(ctx, tok.UserID)
	} else {
		u, err = a.db.UserByLogin(ctx, station.OwnerLogin)
	}
	if err != nil || u.Disabled {
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
		} else if !u.Admin && !u.Disabled {
			u.Admin = true // --allow is the operator's word
			a.db.PutUser(ctx, u)
		}
		return u, !u.Disabled
	}
	return u, err == nil && !u.Disabled
}

// Authenticate finds the principal: a bearer token, the session cookie, or
// a tailnet identity.
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
		if err == nil && !u.Disabled {
			return principalOf(u, ""), nil
		}
		a.drop(r) // the user is gone or disabled: so is the session
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

// login checks a login and password, with a lockout after repeated
// failures from one address, and starts a session.
func (a *auth) login(w http.ResponseWriter, r *http.Request, login, password string) bool {
	addr := a.clientIP(r)
	a.mu.Lock()
	now := time.Now()
	var recent []time.Time
	for _, t := range a.failures[addr] {
		if now.Sub(t) < lockout {
			recent = append(recent, t)
		}
	}
	a.failures[addr] = recent
	locked := len(recent) >= maxFailures
	a.mu.Unlock()
	if locked {
		time.Sleep(time.Second)
		return false
	}
	u, hash, err := a.db.Credentials(r.Context(), login)
	if err != nil || u.Disabled || hash == "" || !checkPassword(hash, password) {
		a.mu.Lock()
		a.failures[addr] = append(a.failures[addr], now)
		a.mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		return false
	}
	a.db.TouchUser(r.Context(), u.ID, now)
	a.mu.Lock()
	delete(a.failures, addr)
	id := randomID()
	a.sessions[id] = &session{user: u.ID, csrf: randomID(), expires: now.Add(sessionLife)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: a.tls,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLife / time.Second)})
	return true
}

func (a *auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: a.tls, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
