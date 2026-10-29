package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/lease"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/signal"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
	"github.com/VibiumDev/station/tunnel"
	"github.com/VibiumDev/station/turn"
	"github.com/VibiumDev/station/viewer"
)

// server is the single-player station's HTTP side.
type server struct {
	cfg       *Config
	dir       string
	db        *sqlite.DB
	key       nostr.SecretKey
	auth      *auth
	leases    *lease.Manager
	tunnel    *tunnel.Server
	enroll    *enroll.Handler
	signal    *signal.Handler
	relay     *relay.Relay
	turn      *turn.Server // nil when off
	turnInfo  string
	publicURL string
	mode      string
	mdns      bool
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	// Rigs and the public bits.
	mux.HandleFunc("GET /v1/station", s.enroll.ServeProfile)
	mux.HandleFunc("POST /v1/enroll", s.enroll.ServeEnroll)
	mux.Handle("/v1/tunnel", s.tunnel)
	mux.Handle("GET /viewer/", http.StripPrefix("/viewer/", viewer.Assets()))
	mux.HandleFunc("GET /static/station.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(stationCSS)
	})
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logout)
	// Pages.
	mux.Handle("GET /{$}", s.page(station.ActionView, s.indexPage))
	mux.Handle("GET /devices/{rig}/{name}/view", s.page(station.ActionView, s.viewPage))
	mux.Handle("GET /settings", s.page(station.ActionManage, s.settingsPage))
	mux.Handle("POST /settings/password", s.page(station.ActionManage, s.setPassword))
	mux.Handle("POST /settings/tokens", s.page(station.ActionManage, s.createToken))
	mux.Handle("POST /settings/tokens/revoke", s.page(station.ActionManage, s.revokeToken))
	mux.Handle("POST /settings/codes", s.page(station.ActionManage, s.createCode))
	mux.Handle("POST /settings/rigs/remove", s.page(station.ActionManage, s.removeRig))
	// The API.
	mux.Handle("GET /v1/devices", s.api(station.ActionView, s.listDevices))
	mux.Handle("POST /v1/devices/{rig}/{name}/lease", s.api(station.ActionView, s.takeLease))
	mux.Handle("DELETE /v1/devices/{rig}/{name}/lease", s.api(station.ActionView, s.releaseLease))
	mux.Handle("POST /v1/devices/{rig}/{name}/lease/release", s.api(station.ActionView, s.releaseLease))
	mux.Handle("GET /v1/devices/{rig}/{name}/signal", s.api(station.ActionView, func(w http.ResponseWriter, r *http.Request, _ station.Principal) {
		s.signal.ServeDevice(w, r, deviceID(r))
	}))
	// Relayed to the rig's own endpoints, with a grant the station signs
	// for the caller: the MJPEG fallback and its input, and screenshots.
	mux.Handle("GET /v1/devices/{rig}/{name}/stream", s.api(station.ActionView, s.relayed("/stream", station.ScopeView)))
	mux.Handle("GET /v1/devices/{rig}/{name}/screenshot", s.api(station.ActionView, s.relayed("/snapshot", station.ScopeView)))
	mux.Handle("GET /v1/devices/{rig}/{name}/hid", s.api(station.ActionControl, s.relayed("/api/hid/ws", station.ScopeInput)))
	mux.Handle("GET /v1/devices/{rig}/{name}/audio", s.api(station.ActionView, s.relayed("/audio", station.ScopeView)))
	mux.Handle("GET /v1/devices/{rig}/{name}/mic", s.api(station.ActionControl, s.relayed("/api/mic", station.ScopeInput)))
	return secure(mux)
}

func deviceID(r *http.Request) string { return r.PathValue("rig") + "/" + r.PathValue("name") }

// secure adds the headers every response carries. The pages have no
// inline script or style, so the policy is strict.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss: ws:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type handlerWithPrincipal func(w http.ResponseWriter, r *http.Request, p station.Principal)

// page gates a browser page: signed in, allowed, and with a CSRF token on
// anything but GET.
func (s *server) page(action station.Action, next handlerWithPrincipal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.auth.Authenticate(r)
		if err != nil {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && !s.auth.csrfOK(r) {
			http.Error(w, "the form has expired; go back and try again", http.StatusForbidden)
			return
		}
		if err := s.auth.Authorize(r.Context(), p, action, ""); err != nil {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		next(w, r, p)
	})
}

func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func apiJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// sameOrigin says whether a browser's Origin header names this station.
// Requests without one (API clients) pass.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// api gates an API call: a bearer token, the session (with CSRF on
// writes), or a tailnet identity. A WebSocket from a browser must come
// from this station's own pages: the cookie would otherwise let any site
// the owner visits open one.
func (s *server) api(action station.Action, next handlerWithPrincipal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay.IsWebSocket(r) && !sameOrigin(r) {
			apiError(w, http.StatusForbidden, "cross-origin WebSocket")
			return
		}
		p, err := s.auth.Authenticate(r)
		if err != nil {
			apiError(w, http.StatusUnauthorized, "sign in, or send Authorization: Bearer <token>")
			return
		}
		if r.Method != http.MethodGet && !s.csrfOKAPI(r) {
			apiError(w, http.StatusForbidden, "missing or stale CSRF token")
			return
		}
		if err := s.auth.Authorize(r.Context(), p, action, ""); err != nil {
			apiError(w, http.StatusForbidden, "not allowed")
			return
		}
		next(w, r, p)
	})
}

// csrfOKAPI is csrfOK plus the beacon case: a JSON body {"csrf": ...}
// when a page unloads and cannot set headers.
func (s *server) csrfOKAPI(r *http.Request) bool {
	if s.auth.sessionOf(r) == nil || r.Header.Get("X-CSRF-Token") != "" {
		return s.auth.csrfOK(r)
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		var body struct {
			CSRF string `json:"csrf"`
		}
		json.Unmarshal(data, &body)
		return body.CSRF != "" && body.CSRF == s.auth.csrfToken(r)
	}
	return s.auth.csrfOK(r)
}

// --- Login ---

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.auth.Authenticate(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login", map[string]any{"Next": safeNext(r.FormValue("next")), "NoPassword": s.cfg.PasswordHash == ""})
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func (s *server) loginPost(w http.ResponseWriter, r *http.Request) {
	if !s.auth.login(w, r, r.FormValue("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login", map[string]any{"Next": safeNext(r.FormValue("next")), "Error": "That is not the password.", "NoPassword": s.cfg.PasswordHash == ""})
		return
	}
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.auth.csrfOK(r) {
		http.Error(w, "stale form", http.StatusForbidden)
		return
	}
	s.auth.logout(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- Devices ---

// deviceView is a device as the pages and the API show it.
type deviceView struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Kind      string             `json:"kind"`
	Caps      []string           `json:"caps"`
	Shortcuts []station.Shortcut `json:"shortcuts,omitempty"`
	Input     string             `json:"input,omitempty"`
	Rig       rigView            `json:"rig"`
	Online    bool               `json:"online"`
	Lease     *leaseView         `json:"lease"`
}

type rigView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Npub string `json:"npub"`
}

type leaseView struct {
	ID      string    `json:"id"`
	Seq     uint64    `json:"seq"`
	Subject string    `json:"subject"`
	Scopes  []string  `json:"scopes"`
	Since   time.Time `json:"since"`
	Expires time.Time `json:"expires"`
}

func (s *server) devices(ctx context.Context) ([]deviceView, error) {
	rigs, err := s.db.Rigs(ctx)
	if err != nil {
		return nil, err
	}
	var out []deviceView
	for _, rg := range rigs {
		online := s.tunnel.IsOnline(rg.ID)
		for _, d := range rg.Devices {
			v := deviceView{ID: d.ID, Name: d.Name(), Kind: d.Kind, Caps: d.Caps, Shortcuts: d.Shortcuts, Input: d.Input,
				Rig: rigView{ID: rg.ID, Name: rg.Name, Npub: npub(rg.PubKey)}, Online: online}
			if l, err := s.db.Current(ctx, d.ID); err == nil {
				v.Lease = &leaseView{ID: l.ID, Seq: l.Seq, Subject: l.Subject, Scopes: station.ScopeStrings(l.Scopes), Since: l.CreatedAt, Expires: l.ExpiresAt}
			}
			out = append(out, v)
		}
		if len(rg.Devices) == 0 {
			out = append(out, deviceView{Rig: rigView{ID: rg.ID, Name: rg.Name, Npub: npub(rg.PubKey)}, Online: online})
		}
	}
	return out, nil
}

func (s *server) listDevices(w http.ResponseWriter, r *http.Request, _ station.Principal) {
	devs, err := s.devices(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if devs == nil {
		devs = []deviceView{}
	}
	apiJSON(w, devs)
}

// takeLease is POST /v1/devices/{rig}/{name}/lease: {"scopes": [...]}
// gives a grant on a lease, the caller's own or, watching, the holder's.
func (s *server) takeLease(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		Scopes []string `json:"scopes"`
	}
	if r.Body != nil {
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	}
	scopes, err := station.ParseScopes(body.Scopes)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := station.ActionView
	for _, sc := range scopes {
		switch sc {
		case station.ScopeInput:
			action = station.ActionControl
		case station.ScopeBidi:
			action = station.ActionBidi
		case station.ScopeAdmin:
			action = station.ActionAdmin
		}
	}
	id := deviceID(r)
	if err := s.auth.Authorize(r.Context(), p, action, id); err != nil {
		apiError(w, http.StatusForbidden, "this token may only view")
		return
	}
	res, err := s.leases.Take(r.Context(), lease.Request{DeviceID: id, Subject: p.Subject, Scopes: scopes})
	if errors.Is(err, station.ErrNotFound) {
		apiError(w, http.StatusNotFound, "no such device")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ice := res.Grant.ICE
	if ice == nil {
		ice = []token.ICEServer{}
	}
	apiJSON(w, map[string]any{
		"grant":    res.Event,
		"ice":      ice,
		"observer": res.Observer,
		"lease": leaseView{ID: res.Lease.ID, Seq: res.Lease.Seq, Subject: res.Lease.Subject, Scopes: station.ScopeStrings(res.Grant.Scopes),
			Since: res.Lease.CreatedAt, Expires: res.Grant.ExpiresAt},
		"signal": "/v1/devices/" + id + "/signal",
		"online": s.tunnel.IsOnline(res.Lease.RigID),
	})
}

// relayed forwards a request to one of the rig's own endpoints with a
// grant the station signs for the caller: a seat on the device's lease
// with the scope the endpoint needs. Viewing gets the caller's own lease
// or a seat watching the holder's; input needs the caller to hold the
// lease already, taken through the lease endpoint. The rig checks the
// grant like any other, and the stream it serves is capped on its side.
func (s *server) relayed(rigPath string, scope station.Scope) handlerWithPrincipal {
	return func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		id := deviceID(r)
		scopes := []station.Scope{station.ScopeView}
		if scope != station.ScopeView {
			cur, err := s.db.Current(r.Context(), id)
			if err != nil || cur.Subject != p.Subject {
				apiError(w, http.StatusConflict, "take the device first")
				return
			}
			scopes = append(scopes, scope)
		}
		res, err := s.leases.Take(r.Context(), lease.Request{DeviceID: id, Subject: p.Subject, Scopes: scopes})
		if errors.Is(err, station.ErrNotFound) {
			apiError(w, http.StatusNotFound, "no such device")
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		r.Header.Set("Authorization", token.Header(res.Event))
		rigID, _, _ := station.SplitDeviceID(id)
		s.relay.Serve(w, r, rigID, rigPath)
	}
}

func (s *server) releaseLease(w http.ResponseWriter, r *http.Request, p station.Principal) {
	id := deviceID(r)
	cur, err := s.db.Current(r.Context(), id)
	if errors.Is(err, station.ErrNoLease) {
		apiJSON(w, map[string]any{"released": false})
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.leases.Release(r.Context(), cur.ID, p.Subject); err != nil {
		if errors.Is(err, lease.ErrNotHolder) {
			apiError(w, http.StatusForbidden, "someone else holds this device")
			return
		}
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiJSON(w, map[string]any{"released": true})
}

// --- Pages ---

func (s *server) indexPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	devs, err := s.devices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "index", map[string]any{"Devices": devs, "Principal": p, "CSRF": s.auth.csrfToken(r), "Name": s.cfg.Name})
}

func (s *server) viewPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	id := deviceID(r)
	dev, err := s.db.Device(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rg, _ := s.db.Rig(r.Context(), r.PathValue("rig"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	viewer.Render(w, viewer.Page{Title: rg.Name + " / " + dev.Name() + " · " + s.cfg.Name, DeviceID: dev.ID, DeviceName: rg.Name + " / " + dev.Name(),
		Shortcuts: dev.Shortcuts, Input: dev.Input, CSRF: s.auth.csrfToken(r)})
}

func (s *server) settingsData(r *http.Request, p station.Principal, extra map[string]any) map[string]any {
	ctx := r.Context()
	tokens, _ := s.db.Tokens(ctx)
	rigs, _ := s.db.Rigs(ctx)
	codes, _ := s.db.OpenCodes(ctx, time.Now())
	data := map[string]any{
		"Principal": p, "CSRF": s.auth.csrfToken(r), "Name": s.cfg.Name, "Tokens": tokens, "Rigs": rigs, "OpenCodes": codes,
		"Mode": s.mode, "PublicURL": s.publicURL, "TunnelURL": s.enroll.TunnelURL, "MDNS": s.mdns,
		"Npub": npub(s.key.Public()), "Fingerprint": enroll.Fingerprint(s.key.Public()), "Allow": s.cfg.Allow,
		"NoPassword": s.cfg.PasswordHash == "", "TURN": s.turnInfo,
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (s *server) settingsPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.render(w, "settings", s.settingsData(r, p, nil))
}

func (s *server) setPassword(w http.ResponseWriter, r *http.Request, p station.Principal) {
	pw, again := r.FormValue("password"), r.FormValue("again")
	msg := "Password changed."
	switch {
	case len(pw) < 8:
		msg = "A password needs at least 8 characters."
	case pw != again:
		msg = "The two passwords differ."
	default:
		h, err := hashPassword(pw)
		if err == nil {
			s.cfg.PasswordHash = h
			err = saveConfig(s.dir, s.cfg)
		}
		if err != nil {
			msg = "Could not save: " + err.Error()
		}
	}
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": msg}))
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "A token needs a name."}))
		return
	}
	secret, err := s.db.CreateToken(r.Context(), name, r.FormValue("read_only") == "on")
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "token", Detail: "created " + name})
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"NewToken": secret, "NewTokenName": name}))
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := r.FormValue("name")
	if err := s.db.RevokeToken(r.Context(), name); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "token", Detail: "revoked " + name})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *server) createCode(w http.ResponseWriter, r *http.Request, p station.Principal) {
	code, expires, err := s.newCode(r.Context())
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"NewCode": code, "CodeExpires": expires.Format(time.Kitchen)}))
}

func (s *server) newCode(ctx context.Context) (string, time.Time, error) {
	code := enroll.NewCode(s.key.Public())
	expires := time.Now().Add(enroll.CodeLifetime)
	return code, expires, s.db.SaveCode(ctx, code, expires)
}

func (s *server) removeRig(w http.ResponseWriter, r *http.Request, p station.Principal) {
	id := r.FormValue("rig")
	if err := s.db.RemoveRig(r.Context(), id); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "rig", Detail: "removed " + id})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

var _ = fmt.Sprintf
