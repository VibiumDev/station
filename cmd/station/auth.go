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

// auth is the single-player Authenticator and Authorizer: the owner's
// password session, API tokens, and Tailscale identities when allowed.
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
	principal station.Principal
	csrf      string
	expires   time.Time
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

// owner is the principal the password gives.
var owner = station.Principal{Subject: "owner", Admin: true}

// Authenticate finds the principal: a bearer token, the session cookie, or
// a tailnet identity.
func (a *auth) Authenticate(r *http.Request) (station.Principal, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		secret := strings.TrimSpace(h[7:])
		tok, err := a.db.LookupToken(r.Context(), secret)
		if err != nil {
			return station.Principal{}, station.ErrUnauthenticated
		}
		return station.Principal{Subject: "token:" + tok.Name, ReadOnly: tok.ReadOnly, Device: tok.Device}, nil
	}
	if s := a.sessionOf(r); s != nil {
		return s.principal, nil
	}
	if a.who != nil && len(a.cfg.Allow) > 0 {
		login, tags, err := a.who(r.Context(), r.RemoteAddr)
		switch {
		case err != nil:
			log.Printf("identity: %s: %v", r.RemoteAddr, err)
		case a.allowed(login, tags):
			return station.Principal{Subject: login, Admin: true}, nil
		default:
			log.Printf("identity: %s is %s %v, not in --allow", r.RemoteAddr, login, tags)
		}
	}
	return station.Principal{}, station.ErrUnauthenticated
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

// Authorize: the owner and allowed identities do everything; tokens
// control devices unless read-only, and a token bound to a device acts
// on that device alone; nobody but admins manages.
func (a *auth) Authorize(_ context.Context, p station.Principal, action station.Action, deviceID string) error {
	if p.Device != "" && deviceID != "" && deviceID != p.Device {
		return station.ErrForbidden
	}
	switch action {
	case station.ActionView:
		return nil
	case station.ActionManage:
		if p.Admin {
			return nil
		}
		return station.ErrForbidden
	default:
		if p.ReadOnly {
			return station.ErrForbidden
		}
		return nil
	}
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

// login checks the password, with a lockout after repeated failures from
// one address, and starts a session.
func (a *auth) login(w http.ResponseWriter, r *http.Request, password string) bool {
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
	if a.cfg.PasswordHash == "" || !checkPassword(a.cfg.PasswordHash, password) {
		a.mu.Lock()
		a.failures[addr] = append(a.failures[addr], now)
		a.mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		return false
	}
	a.mu.Lock()
	delete(a.failures, addr)
	id := randomID()
	a.sessions[id] = &session{principal: owner, csrf: randomID(), expires: now.Add(sessionLife)}
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
