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
	"strconv"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/records"
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
	cfg        *Config
	dir        string
	db         *sqlite.DB
	key        nostr.SecretKey
	auth       *auth
	holds      *hold.Manager
	tunnel     *tunnel.Server
	enroll     *enroll.Handler
	signal     *signal.Handler
	relay      *relay.Relay
	turn       *turn.Server // nil when off
	records    *records.Store
	recordsDir string
	turnInfo   string
	publicURL  string
	mode       string
	mdns       bool
	// The name may change from the Settings page while the station runs.
	nameMu   sync.RWMutex
	mdnsStop func() // the LAN advertisement, to redo under a new name
	mdnsPort int
}

// name is the station's name: the config's, "station" until the owner
// sets one.
func (s *server) name() string {
	s.nameMu.RLock()
	defer s.nameMu.RUnlock()
	return s.cfg.Name
}

// named says whether the owner has given the station a name of their
// own; the default is not shown on the pages.
func (s *server) named() bool {
	n := s.name()
	return n != "" && n != "station"
}

// title is the name for a browser tab: the owner's, or the product's.
func (s *server) title() string {
	if s.named() {
		return s.name()
	}
	return "Vibium Station"
}

// host is the station's address without the scheme, for the pages' foot.
func (s *server) host() string {
	u := s.publicURL
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	return strings.TrimSuffix(u, "/")
}

// enrollNow is the enrollment handler under the station's current name.
func (s *server) enrollNow() *enroll.Handler {
	h := *s.enroll
	h.Name = s.name()
	return &h
}

// setName renames the station: the config, the enrollment profile from
// now on, and the LAN advertisement.
func (s *server) setName(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := strings.TrimSpace(r.FormValue("name"))
	msg := "Name saved."
	switch {
	case name == "":
		msg = "The station needs a name."
	case len(name) > 60:
		msg = "A name is at most 60 characters."
	default:
		s.nameMu.Lock()
		old := s.cfg.Name
		s.cfg.Name = name
		err := saveConfig(s.dir, s.cfg)
		if err != nil {
			s.cfg.Name = old
			msg = "Could not save: " + err.Error()
		} else if s.mdnsStop != nil {
			s.mdnsStop()
			if stop, err := enroll.Advertise(name, s.mdnsPort); err == nil {
				s.mdnsStop = stop
			} else {
				s.mdnsStop, s.mdns = nil, false
			}
		}
		s.nameMu.Unlock()
		if err == nil && name != old {
			s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "name", Detail: old + " renamed " + name})
		}
	}
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": msg}))
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	// Rigs and the public bits.
	mux.HandleFunc("GET /v1/station", func(w http.ResponseWriter, r *http.Request) { s.enrollNow().ServeProfile(w, r) })
	mux.HandleFunc("POST /v1/enroll", func(w http.ResponseWriter, r *http.Request) { s.enrollNow().ServeEnroll(w, r) })
	mux.Handle("/v1/tunnel", s.tunnel)
	mux.Handle("GET /viewer/", http.StripPrefix("/viewer/", viewer.Assets()))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logout)
	// Pages.
	mux.Handle("GET /{$}", s.page(station.ActionView, s.indexPage))
	mux.Handle("GET /devices/{rig}/{name}/view", s.page(station.ActionView, s.viewPage))
	mux.Handle("GET /records", s.page(station.ActionView, s.recordsPage))
	mux.Handle("GET /records/{sha}/play", s.page(station.ActionView, s.playPage))
	mux.Handle("GET /records/{sha}/thumb.jpg", s.page(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		s.records.ServeThumbnail(w, r, r.PathValue("sha"), &p)
	}))
	mux.Handle("GET /records/{sha}/files/{path...}", s.page(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		s.records.ServeFile(w, r, r.PathValue("sha"), r.PathValue("path"), &p)
	}))
	mux.Handle("POST /records/public", s.page(station.ActionView, s.recordPublic))
	mux.Handle("POST /records/delete", s.page(station.ActionView, s.recordDelete))
	mux.Handle("GET /settings", s.page(station.ActionManage, s.settingsPage))
	mux.Handle("POST /settings/name", s.page(station.ActionManage, s.setName))
	mux.Handle("POST /settings/password", s.page(station.ActionManage, s.setPassword))
	mux.Handle("POST /settings/tokens", s.page(station.ActionManage, s.createToken))
	mux.Handle("POST /settings/tokens/revoke", s.page(station.ActionManage, s.revokeToken))
	mux.Handle("POST /settings/codes", s.page(station.ActionManage, s.createCode))
	mux.Handle("POST /settings/rigs/remove", s.page(station.ActionManage, s.removeRig))
	// The API.
	mux.Handle("GET /v1/devices", s.api(station.ActionView, s.listDevices))
	// The Record Store: Blossom at the root, the station's own calls under /v1/records.
	mux.HandleFunc("PUT /upload", func(w http.ResponseWriter, r *http.Request) { s.records.ServeUpload(w, r) })
	mux.HandleFunc("GET /list/{pubkey}", s.blossomList)
	mux.HandleFunc("GET /{sha}", s.blossomBlob)
	mux.HandleFunc("DELETE /{sha}", s.blossomBlob)
	mux.Handle("GET /v1/records", s.api(station.ActionView, s.listRecords))
	mux.Handle("POST /v1/records/upload-auth", s.api(station.ActionControl, s.uploadAuth))
	mux.Handle("POST /v1/records/{sha}/download-auth", s.api(station.ActionView, s.downloadAuth))
	mux.Handle("GET /v1/devices/{rig}/{name}/recordings", s.api(station.ActionView, s.listRecords))
	mux.Handle("POST /v1/devices/{rig}/{name}/hold", s.api(station.ActionView, s.takeHold))
	mux.Handle("DELETE /v1/devices/{rig}/{name}/hold", s.api(station.ActionView, s.releaseHold))
	mux.Handle("POST /v1/devices/{rig}/{name}/hold/release", s.api(station.ActionView, s.releaseHold))
	mux.Handle("POST /v1/devices/{rig}/{name}/reservation", s.api(station.ActionView, s.reserve))
	mux.Handle("DELETE /v1/devices/{rig}/{name}/reservation", s.api(station.ActionView, s.unreserve))
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
	// Agents: WebDriver BiDi, byte for byte, and the rig's REST under
	// /api, with the rig's allowlist deciding what each scope reaches.
	mux.Handle("GET /v1/devices/{rig}/{name}/bidi", s.api(station.ActionBidi, s.relayed("/session", station.ScopeBidi)))
	mux.Handle("GET /session", s.api(station.ActionBidi, s.serveSession))
	mux.Handle("GET /v1/devices/{rig}/{name}/health", s.api(station.ActionView, s.relayed("/health", station.ScopeView)))
	// The device's own additions to the viewer, a script the rig serves.
	mux.Handle("GET /v1/devices/{rig}/{name}/extras.js", s.api(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		dev, err := s.db.Device(r.Context(), deviceID(r))
		if err != nil || dev.Extras == "" {
			http.NotFound(w, r)
			return
		}
		s.relayTo(w, r, p, dev.Extras, station.ScopeView)
	}))
	mux.Handle("/v1/devices/{rig}/{name}/api/{path...}", s.api(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		s.relayTo(w, r, p, "/api/"+r.PathValue("path"), "")
	}))
	return secure(mux)
}

func deviceID(r *http.Request) string { return r.PathValue("rig") + "/" + r.PathValue("name") }

// secure adds the headers every response carries. The pages have no
// inline script or style, so the policy is strict.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// No inline script anywhere; inline style attributes are the
		// viewer's layout (Roadie's markup), with no way to run code.
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss: ws:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
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
	Waiting   int                `json:"waiting,omitempty"`
	Name      string             `json:"name"`
	Kind      string             `json:"kind"`
	Caps      []string           `json:"caps"`
	Shortcuts []station.Shortcut `json:"shortcuts,omitempty"`
	Input     string             `json:"input,omitempty"`
	Extras    string             `json:"extras,omitempty"`
	Label     string             `json:"label,omitempty"`
	Platform  string             `json:"platform,omitempty"`
	Rig       rigView            `json:"rig"`
	Online    bool               `json:"online"`
	Hold      *holdView          `json:"hold"`
}

type rigView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Npub string `json:"npub"`
}

type holdView struct {
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
			v := deviceView{ID: d.ID, Name: d.Name(), Kind: d.Kind, Caps: d.Caps, Shortcuts: d.Shortcuts, Input: d.Input, Extras: d.Extras,
				Label: d.Label, Platform: d.Platform, Rig: rigView{ID: rg.ID, Name: rg.Name, Npub: npub(rg.PubKey)}, Online: online}
			if l, err := s.db.Current(ctx, d.ID); err == nil {
				v.Hold = &holdView{ID: l.ID, Seq: l.Seq, Subject: l.Subject, Scopes: station.ScopeStrings(l.Scopes), Since: l.CreatedAt, Expires: l.ExpiresAt}
			}
			v.Waiting = s.holds.Waiting(d.ID)
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

// actionFor is what the authorizer must allow for a set of scopes.
func actionFor(scopes []station.Scope) station.Action {
	action := station.ActionView
	for _, sc := range scopes {
		switch sc {
		case station.ScopeInput:
			if action == station.ActionView {
				action = station.ActionControl
			}
		case station.ScopeBidi:
			if action != station.ActionAdmin {
				action = station.ActionBidi
			}
		case station.ScopeAdmin:
			action = station.ActionAdmin
		}
	}
	return action
}

// person says whether a principal is someone at a page rather than an
// agent with a token: people recall a device, agents wait in line.
func person(p station.Principal) bool { return !strings.HasPrefix(p.Subject, "token:") }

// holdResponse is what a grant on a hold looks like to the caller.
func (s *server) holdResponse(id string, res hold.Result) map[string]any {
	ice := res.Grant.ICE
	if ice == nil {
		ice = []token.ICEServer{}
	}
	return map[string]any{
		"grant":    res.Event,
		"ice":      ice,
		"observer": res.Observer,
		"hold": holdView{ID: res.Hold.ID, Seq: res.Hold.Seq, Subject: res.Hold.Subject, Scopes: station.ScopeStrings(res.Grant.Scopes),
			Since: res.Hold.CreatedAt, Expires: res.Grant.ExpiresAt},
		"signal":  "/v1/devices/" + id + "/signal",
		"bidi":    "/v1/devices/" + id + "/bidi",
		"online":  s.tunnel.IsOnline(res.Hold.RigID),
		"waiting": s.holds.Waiting(id),
	}
}

// takeHold is POST /v1/devices/{rig}/{name}/hold: {"scopes": [...]}
// gives a grant on a hold, the caller's own or, watching, the holder's.
// A person recalls a device someone else holds; an agent gets a place in
// line (202 with its position), and ?wait=SECONDS waits there for the
// hold, up to 30 s a call. With "watch": true a held device is watched
// instead, whoever asks, and a free one is taken only when the scopes
// include control; view only, it is 409 with "free": true.
func (s *server) takeHold(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		Scopes []string `json:"scopes"`
		Watch  bool     `json:"watch"`
	}
	if r.Body != nil {
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	}
	scopes, err := station.ParseScopes(body.Scopes)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := deviceID(r)
	if err := s.auth.Authorize(r.Context(), p, actionFor(scopes), id); err != nil {
		apiError(w, http.StatusForbidden, "this token may only view")
		return
	}
	res, err := s.holds.Take(r.Context(), hold.Request{DeviceID: id, Subject: p.Subject, Scopes: scopes, Recall: person(p), Watch: body.Watch})
	var queued *hold.Queued
	switch {
	case errors.Is(err, station.ErrNotFound):
		apiError(w, http.StatusNotFound, "no such device")
		return
	case errors.Is(err, hold.ErrWatched):
		apiError(w, http.StatusConflict, "someone is already watching this device")
		return
	case errors.Is(err, hold.ErrFree):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": "nobody holds the device", "free": true})
		return
	case errors.As(err, &queued):
		wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		if wait > 0 {
			if wait > 30 {
				wait = 30
			}
			ctx, cancel := context.WithTimeout(r.Context(), time.Duration(wait)*time.Second)
			defer cancel()
			if got, ok := s.holds.Wait(ctx, id, p.Subject); ok {
				apiJSON(w, s.holdResponse(id, got))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"queued": true, "position": s.holds.Position(id, p.Subject), "holder": queued.Holder, "waiting": s.holds.Waiting(id)})
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiJSON(w, s.holdResponse(id, res))
}

// reserve is POST /v1/devices/{rig}/{name}/reservation: {"at": RFC 3339,
// "scopes": [...]} puts the caller in line for the device at that time
// (now, when at is left out); the place sorts ahead once its time comes.
func (s *server) reserve(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		At     string   `json:"at"`
		Scopes []string `json:"scopes"`
	}
	if r.Body != nil {
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	}
	if len(body.Scopes) == 0 {
		body.Scopes = []string{"view", "input"}
	}
	scopes, err := station.ParseScopes(body.Scopes)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	var at time.Time
	if body.At != "" {
		if at, err = time.Parse(time.RFC3339, body.At); err != nil {
			apiError(w, http.StatusBadRequest, "at: want RFC 3339, such as 2026-10-30T15:00:00Z")
			return
		}
	}
	id := deviceID(r)
	if err := s.auth.Authorize(r.Context(), p, actionFor(scopes), id); err != nil {
		apiError(w, http.StatusForbidden, "this token may only view")
		return
	}
	pos, err := s.holds.Reserve(r.Context(), hold.Request{DeviceID: id, Subject: p.Subject, Scopes: scopes}, at)
	if errors.Is(err, station.ErrNotFound) {
		apiError(w, http.StatusNotFound, "no such device")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	out := map[string]any{"reserved": true, "position": pos, "waiting": s.holds.Waiting(id)}
	if !at.IsZero() {
		out["at"] = at.UTC().Format(time.RFC3339)
	}
	json.NewEncoder(w).Encode(out)
}

func (s *server) unreserve(w http.ResponseWriter, r *http.Request, p station.Principal) {
	apiJSON(w, map[string]any{"left": s.holds.Leave(deviceID(r), p.Subject)})
}

// relayed forwards a request to one of the rig's own endpoints with a
// grant the station signs for the caller; scope is what the endpoint
// needs, or empty for the rig's REST, where the rig's allowlist decides.
func (s *server) relayed(rigPath string, scope station.Scope) handlerWithPrincipal {
	return func(w http.ResponseWriter, r *http.Request, p station.Principal) { s.relayTo(w, r, p, rigPath, scope) }
}

// relayTo signs a grant for the caller and relays. The holder gets its
// own scopes (plus the one the endpoint needs, which makes a new hold);
// a free device is taken on the spot, with the needed scope, or with
// input for the REST when the caller may control; someone else's device
// gives a seat watching for view endpoints and 409 for the rest, since
// taking it from them is the hold endpoint's business. The rig checks
// the grant like any other and caps what it serves.
func (s *server) relayTo(w http.ResponseWriter, r *http.Request, p station.Principal, rigPath string, scope station.Scope) {
	id := deviceID(r)
	cur, err := s.db.Current(r.Context(), id)
	free := errors.Is(err, station.ErrNoHold)
	if err != nil && !free {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	scopes := []station.Scope{station.ScopeView}
	switch {
	case !free && cur.Subject == p.Subject:
		scopes = append([]station.Scope(nil), cur.Scopes...)
		if scope != "" && !station.HasScope(scopes, scope) {
			scopes = append(scopes, scope)
		}
	case free && person(p):
		// A person takes a device through the hold endpoint, on purpose;
		// a relayed request never does it for them, so a page left
		// watching a free device cannot take it by polling.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": "nobody holds the device: take it first", "free": true})
		return
	case free:
		if scope != "" && scope != station.ScopeView {
			scopes = append(scopes, scope)
		} else if scope == "" && !p.ReadOnly {
			scopes = append(scopes, station.ScopeInput)
		}
	default:
		if scope != "" && scope != station.ScopeView {
			apiError(w, http.StatusConflict, "held by "+cur.Subject+": take the device first, or wait in line")
			return
		}
	}
	if err := s.auth.Authorize(r.Context(), p, actionFor(scopes), id); err != nil {
		apiError(w, http.StatusForbidden, "this token may only view")
		return
	}
	res, err := s.holds.Take(r.Context(), hold.Request{DeviceID: id, Subject: p.Subject, Scopes: scopes})
	var queued *hold.Queued
	switch {
	case errors.Is(err, station.ErrNotFound):
		apiError(w, http.StatusNotFound, "no such device")
		return
	case errors.Is(err, hold.ErrWatched):
		apiError(w, http.StatusConflict, "someone is already watching this device")
		return
	case errors.As(err, &queued):
		apiError(w, http.StatusConflict, queued.Error()+": take the device first, or wait in line")
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.Header.Set("Authorization", token.Header(res.Event))
	// Which hold the request rides on, so a page notices when the hold
	// it knows is over and someone else's has begun.
	w.Header().Set("X-Hold", res.Hold.ID)
	if res.Observer {
		w.Header().Set("X-Hold-Observer", "1")
	}
	rigID, _, _ := station.SplitDeviceID(id)
	s.relay.ServeHold(w, r, rigID, rigPath, res.Hold.ID)
	// A BiDi socket that took the device gives it back when it closes,
	// as /session does. A REST request keeps the hold for the next one,
	// and a device the subject already held is theirs to release.
	if scope == station.ScopeBidi && relay.IsWebSocket(r) && cur.Subject != p.Subject {
		s.releaseAfter(id, res.Hold.ID, p.Subject)
	}
}

func (s *server) releaseHold(w http.ResponseWriter, r *http.Request, p station.Principal) {
	id := deviceID(r)
	if s.holds.Leave(id, p.Subject) {
		apiJSON(w, map[string]any{"released": false, "left": true})
		return
	}
	cur, err := s.db.Current(r.Context(), id)
	if errors.Is(err, station.ErrNoHold) {
		apiJSON(w, map[string]any{"released": false})
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.holds.Release(r.Context(), cur.ID, p.Subject); err != nil {
		if errors.Is(err, hold.ErrNotHolder) {
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
	s.render(w, "index", map[string]any{"Page": "index", "Devices": devs, "Principal": p, "CSRF": s.auth.csrfToken(r)})
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
	q := r.URL.Query()
	label := dev.Name()
	if dev.Label != "" {
		label = dev.Label
	}
	cfg := viewer.Config{Mode: "station", Device: dev.ID, Name: rg.Name + " / " + label, API: viewer.Relayed("", dev.ID),
		InputMode: dev.Input, Shortcuts: dev.Shortcuts, CSRF: s.auth.csrfToken(r), WebRTC: true}
	for _, c := range dev.Caps {
		if c == station.CapVideo {
			cfg.WebRTC = true
		}
	}
	if m := q.Get("input"); m == "touch" || m == "mouse" {
		cfg.InputMode = m
	}
	if t := q.Get("transport"); t == "webrtc" || t == "mjpeg" {
		cfg.Transport = t
	}
	if dev.Extras != "" {
		cfg.Extras = "/v1/devices/" + dev.ID + "/extras.js"
	}
	viewer.Render(w, viewer.Page{Title: rg.Name + " / " + label + " · " + s.title(), Minimal: q.Get("minimal") == "1", Home: "/", HomeTitle: "Devices", Config: cfg})
}

func (s *server) settingsData(r *http.Request, p station.Principal, extra map[string]any) map[string]any {
	ctx := r.Context()
	tokens, _ := s.db.Tokens(ctx)
	rigs, _ := s.db.Rigs(ctx)
	codes, _ := s.db.OpenCodes(ctx, time.Now())
	data := map[string]any{
		"Page": "settings", "Principal": p, "CSRF": s.auth.csrfToken(r), "Tokens": tokens, "Rigs": rigs, "OpenCodes": codes,
		"Mode": s.mode, "PublicURL": s.publicURL, "TunnelURL": s.enroll.TunnelURL, "MDNS": s.mdns,
		"Npub": npub(s.key.Public()), "Fingerprint": enroll.Fingerprint(s.key.Public()), "Allow": s.cfg.Allow,
		"NoPassword": s.cfg.PasswordHash == "", "TURN": s.turnInfo,
	}
	if n, size, err := s.db.BlobTotal(ctx); err == nil {
		data["RecordsCount"], data["RecordsTotal"] = n, humanSize(size)
	}
	data["RecordsDir"], data["RecordsMaxFile"], data["RecordsMaxTotal"], data["RecordsMaxAge"] =
		s.recordsDir, humanSize(s.records.MaxFile), humanSize(s.records.MaxTotal), humanAge(s.records.MaxAge)
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

// render writes a page; every page knows the station's name, whether the
// owner set it, and the station's address.
func (s *server) render(w http.ResponseWriter, name string, data map[string]any) {
	data["Name"], data["Named"], data["Host"] = s.name(), s.named(), s.host()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

var _ = fmt.Sprintf
