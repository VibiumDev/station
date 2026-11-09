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
	jams       *jams
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
	mux.HandleFunc("GET /favicon.png", serveIcon)
	mux.HandleFunc("GET /apple-touch-icon.png", serveIcon)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("GET /login/code", s.codePage)
	mux.HandleFunc("POST /login/code", s.codePost)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /setup/{token}", s.setupLinkPage)
	mux.HandleFunc("POST /setup/{token}", s.setupLinkPost)
	mux.HandleFunc("GET /account/setup", s.accountSetupPage)
	mux.HandleFunc("POST /account/setup", s.accountSetupPost)
	mux.HandleFunc("POST /account/setup/cancel", s.accountSetupCancel)
	// Pages.
	mux.Handle("GET /{$}", s.page(station.ActionView, s.homePage))
	mux.Handle("GET /devices", s.page(station.ActionView, s.indexPage))
	mux.Handle("GET /jams", s.page(station.ActionView, s.jamsPage))
	mux.Handle("POST /jams", s.page(station.ActionControl, s.startJamForm))
	mux.Handle("GET /jams/{id}", s.page(station.ActionView, s.jamPage))
	mux.Handle("POST /jams/{id}/turns", s.page(station.ActionControl, s.jamTurnForm))
	mux.Handle("POST /jams/{id}/stop", s.page(station.ActionControl, s.jamStopForm))
	mux.Handle("POST /jams/{id}/keep", s.page(station.ActionControl, s.jamKeepForm))
	mux.Handle("GET /jams/{id}/shots/{seq}", s.page(station.ActionView, s.jamShot))
	mux.Handle("GET /devices/{rig}/{name}/drive", s.page(station.ActionView, s.drivePage))
	mux.Handle("GET /devices/{rig}/{name}/view", s.page(station.ActionView, s.viewPage))
	mux.Handle("GET /records", s.page(station.ActionView, s.recordsPage))
	mux.Handle("GET /records/{sha}/play", s.page(station.ActionView, s.playPage))
	mux.Handle("GET /records/{sha}/thumb.jpg", s.page(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		s.records.ServeThumbnail(w, r, r.PathValue("sha"), &p)
	}))
	mux.Handle("GET /records/{sha}/files/{path...}", s.page(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		s.records.ServeFile(w, r, r.PathValue("sha"), r.PathValue("path"), &p)
	}))
	mux.Handle("POST /records/public", s.page(station.ActionControl, s.recordPublic))
	mux.Handle("POST /records/delete", s.page(station.ActionControl, s.recordDelete))
	mux.Handle("POST /records/golden", s.page(station.ActionControl, s.recordGolden))
	mux.Handle("POST /records/tags", s.page(station.ActionControl, s.recordTags))
	mux.Handle("GET /account", s.page(station.ActionView, s.accountPage))
	mux.Handle("POST /account/password", s.personPage(s.changePassword))
	mux.Handle("POST /account/codes", s.personPage(s.accountCodes))
	mux.Handle("POST /account/authenticator", s.personPage(s.accountAuthenticator))
	mux.Handle("POST /account/tokens", s.personPage(s.accountTokens))
	mux.Handle("POST /account/tokens/revoke", s.personPage(s.accountTokenRevoke))
	mux.Handle("GET /settings", s.page(station.ActionManage, s.settingsPage))
	mux.Handle("POST /settings/name", s.page(station.ActionManage, s.setName))
	mux.Handle("POST /settings/tokens", s.page(station.ActionManage, s.createToken))
	mux.Handle("POST /settings/tokens/revoke", s.page(station.ActionManage, s.revokeToken))
	mux.Handle("POST /settings/codes", s.page(station.ActionManage, s.createCode))
	mux.Handle("POST /settings/rigs/remove", s.page(station.ActionManage, s.removeRig))
	mux.Handle("POST /settings/models", s.page(station.ActionManage, s.addModel))
	mux.Handle("POST /settings/models/remove", s.page(station.ActionManage, s.removeModel))
	mux.Handle("POST /settings/models/default", s.page(station.ActionManage, s.defaultModel))
	mux.Handle("POST /settings/models/key", s.page(station.ActionManage, s.modelKey))
	mux.Handle("POST /settings/jams", s.page(station.ActionManage, s.setJams))
	mux.Handle("POST /settings/users", s.page(station.ActionManage, s.addUserForm))
	mux.Handle("POST /settings/users/role", s.page(station.ActionManage, s.userRoleForm))
	mux.Handle("POST /settings/users/lock", s.page(station.ActionManage, s.userLockForm))
	mux.Handle("POST /settings/users/reset", s.page(station.ActionManage, s.userResetForm))
	mux.Handle("POST /settings/users/remove", s.page(station.ActionManage, s.userRemoveForm))
	mux.Handle("POST /settings/teams", s.page(station.ActionManage, s.addTeamForm))
	mux.Handle("POST /settings/teams/remove", s.page(station.ActionManage, s.teamRemoveForm))
	mux.Handle("POST /settings/teams/members", s.page(station.ActionManage, s.memberForm))
	mux.Handle("POST /settings/access", s.page(station.ActionManage, s.accessForm))
	mux.Handle("GET /v1/users", s.api(station.ActionManage, s.listUsers))
	mux.Handle("POST /v1/users", s.api(station.ActionManage, s.createUser))
	mux.Handle("PUT /v1/users/{id}", s.api(station.ActionManage, s.updateUser))
	mux.Handle("POST /v1/users/{id}/reset", s.api(station.ActionManage, s.resetUser))
	mux.Handle("DELETE /v1/users/{id}", s.api(station.ActionManage, s.deleteUser))
	mux.Handle("GET /v1/teams", s.api(station.ActionManage, s.listTeams))
	mux.Handle("POST /v1/teams", s.api(station.ActionManage, s.createTeam))
	mux.Handle("DELETE /v1/teams/{id}", s.api(station.ActionManage, s.deleteTeam))
	mux.Handle("PUT /v1/teams/{id}/members/{user}", s.api(station.ActionManage, s.putMember))
	mux.Handle("DELETE /v1/teams/{id}/members/{user}", s.api(station.ActionManage, s.putMember))
	mux.Handle("GET /v1/access", s.api(station.ActionManage, s.listAccess))
	mux.Handle("PUT /v1/access", s.api(station.ActionManage, s.putAccess))
	// The API.
	mux.Handle("GET /v1/devices", s.api(station.ActionView, s.listDevices))
	// Jams: a prompt on the station, answered by the station's own loop.
	mux.Handle("GET /v1/models", s.api(station.ActionView, s.listModels))
	mux.Handle("POST /v1/jams", s.api(station.ActionControl, s.startJam))
	mux.Handle("GET /v1/jams", s.api(station.ActionView, s.listJams))
	mux.Handle("GET /v1/jams/{id}", s.api(station.ActionView, s.getJam))
	mux.Handle("POST /v1/jams/{id}/turns", s.api(station.ActionControl, s.jamTurn))
	mux.Handle("GET /v1/jams/{id}/events", s.api(station.ActionView, s.jamEvents))
	mux.Handle("POST /v1/jams/{id}/stop", s.api(station.ActionControl, s.stopJam))
	mux.Handle("POST /v1/jams/{id}/keep", s.api(station.ActionControl, s.keepJam))
	// The Record Store: Blossom at the root, the station's own calls under /v1/records.
	mux.HandleFunc("PUT /upload", func(w http.ResponseWriter, r *http.Request) { s.records.ServeUpload(w, r) })
	mux.HandleFunc("GET /list/{pubkey}", s.blossomList)
	mux.HandleFunc("GET /{sha}", s.blossomBlob)
	mux.HandleFunc("DELETE /{sha}", s.blossomBlob)
	mux.Handle("GET /v1/records", s.api(station.ActionView, s.listRecords))
	mux.Handle("POST /v1/records/upload-auth", s.api(station.ActionControl, s.uploadAuth))
	mux.Handle("POST /v1/records/{sha}/download-auth", s.api(station.ActionView, s.downloadAuth))
	mux.Handle("POST /v1/records/{sha}/golden", s.api(station.ActionControl, s.setRecordGolden))
	mux.Handle("PUT /v1/records/{sha}/tags", s.api(station.ActionControl, s.setRecordTags))
	mux.Handle("GET /v1/devices/{rig}/{name}/recordings", s.api(station.ActionView, s.listRecords))
	mux.Handle("POST /v1/devices/{rig}/{name}/hold", s.api(station.ActionView, s.takeHold))
	mux.Handle("DELETE /v1/devices/{rig}/{name}/hold", s.api(station.ActionView, s.releaseHold))
	mux.Handle("POST /v1/devices/{rig}/{name}/hold/release", s.api(station.ActionView, s.releaseHold))
	mux.Handle("POST /v1/devices/{rig}/{name}/reservation", s.api(station.ActionView, s.reserve))
	mux.Handle("DELETE /v1/devices/{rig}/{name}/reservation", s.api(station.ActionControl, s.unreserve))
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

// mayRecall says whether a principal takes a held device at once rather
// than waiting in line: a person does, as people recall agents and each
// other, but an admin's hold is recalled by admins alone, and a token
// never recalls.
func (s *server) mayRecall(ctx context.Context, p station.Principal, deviceID string) bool {
	if !person(p) {
		return false
	}
	if p.Manages() {
		return true
	}
	cur, err := s.db.Current(ctx, deviceID)
	if err != nil {
		return true // nobody to recall from
	}
	return !s.subjectIsAdmin(ctx, cur.Subject)
}

// subjectIsAdmin says whether a hold's subject is an admin: a login of
// one, or a token acting as one.
func (s *server) subjectIsAdmin(ctx context.Context, subject string) bool {
	if name, ok := strings.CutPrefix(subject, "token:"); ok {
		tokens, _ := s.db.Tokens(ctx)
		for _, t := range tokens {
			if t.Name == name {
				u, err := s.auth.tokenUser(ctx, t)
				return err == nil && u.Admin
			}
		}
		return false
	}
	u, err := s.db.UserByLogin(ctx, subject)
	return err == nil && u.Admin
}

// routeDevice is the device a route names, "" for one about no device
// in particular: every device route is authorized against its device
// before its handler runs, whatever the handler checks besides.
func routeDevice(r *http.Request) string {
	if r.PathValue("rig") == "" || r.PathValue("name") == "" {
		return ""
	}
	return deviceID(r)
}

// pageCSP is the policy every page carries: no inline script anywhere;
// inline style attributes are the viewer's layout (Roadie's markup),
// with no way to run code. A page framed by one of the station's own,
// the viewer watching a jam, allows that frame and no other.
const (
	pageCSP   = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss: ws:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"
	framedCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' wss: ws:; frame-ancestors 'self'; base-uri 'none'; form-action 'self'; object-src 'none'"
)

// secure adds the headers every response carries.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", pageCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type handlerWithPrincipal func(w http.ResponseWriter, r *http.Request, p station.Principal)

// page gates a browser page: signed in, allowed, and with a CSRF token on
// anything but GET. A sign-in on its way is sent to the step it is at.
func (s *server) page(action station.Action, next handlerWithPrincipal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.auth.Authenticate(r)
		switch {
		case errors.Is(err, errCodeDue):
			http.Redirect(w, r, "/login/code", http.StatusSeeOther)
			return
		case errors.Is(err, errSetupDue):
			http.Redirect(w, r, "/account/setup", http.StatusSeeOther)
			return
		case err != nil:
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && !s.auth.csrfOK(r) {
			http.Error(w, "the form has expired; go back and try again", http.StatusForbidden)
			return
		}
		dev := routeDevice(r)
		if err := s.auth.Authorize(r.Context(), p, action, dev); err != nil {
			if dev != "" {
				http.NotFound(w, r) // a device not shared with them is not there
				return
			}
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		next(w, r, p)
	})
}

// personPage gates an Account change: a person signed in, never a token,
// since a token must not change the credentials of the user it acts as.
func (s *server) personPage(next handlerWithPrincipal) http.Handler {
	return s.page(station.ActionView, func(w http.ResponseWriter, r *http.Request, p station.Principal) {
		if !person(p) {
			http.Error(w, "not allowed: a token cannot change its user's account", http.StatusForbidden)
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
		if err := s.auth.Authorize(r.Context(), p, action, routeDevice(r)); err != nil {
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
	Model     string             `json:"model,omitempty"`
	OSVersion string             `json:"os_version,omitempty"`
	Title     string             `json:"title"`           // the label, else the model, else the name
	Level     station.Level      `json:"level,omitempty"` // what the caller may do with it: watch, drive or jam
	CanDrive  bool               `json:"-"`
	Rig       rigView            `json:"rig"`
	Online    bool               `json:"online"`
	Recorded  bool               `json:"recorded,omitempty"` // the rig records every hold, by its announcement
	Hold      *holdView          `json:"hold"`
}

type rigView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Npub string `json:"npub"`
}

type holdView struct {
	JamID    string    `json:"jam,omitempty"`      // when a jam's token holds the device
	Watching bool      `json:"watching,omitempty"` // the hold only watches, and gives way to anyone asking for control
	ID       string    `json:"id"`
	Seq      uint64    `json:"seq"`
	Subject  string    `json:"subject"`
	Scopes   []string  `json:"scopes"`
	Since    time.Time `json:"since"`
	Expires  time.Time `json:"expires"`
}

func (s *server) devices(ctx context.Context) ([]deviceView, error) {
	rigs, err := s.db.Rigs(ctx)
	if err != nil {
		return nil, err
	}
	var out []deviceView
	for _, rg := range rigs {
		online := s.tunnel.IsOnline(rg.ID)
		recorded := s.tunnel.RecordsHolds(rg.ID)
		for _, d := range rg.Devices {
			v := deviceView{ID: d.ID, Name: d.Name(), Kind: d.Kind, Caps: d.Caps, Shortcuts: d.Shortcuts, Input: d.Input, Extras: d.Extras,
				Label: d.Label, Platform: d.Platform, Model: d.Model, OSVersion: d.OSVersion, Title: d.Title(), Rig: rigView{ID: rg.ID, Name: rg.Name, Npub: npub(rg.PubKey)}, Online: online, Recorded: recorded}
			if l, err := s.db.Current(ctx, d.ID); err == nil {
				v.Hold = &holdView{ID: l.ID, Seq: l.Seq, Subject: l.Subject, Scopes: station.ScopeStrings(l.Scopes), Since: l.CreatedAt, Expires: l.ExpiresAt, JamID: jamOfSubject(l.Subject), Watching: !hold.Controls(l.Scopes)}
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

// visibleDevices is what a principal may see: every device and every
// rig for an admin, else the devices a team of theirs was given, each
// with the caller's level on it.
func (s *server) visibleDevices(ctx context.Context, p station.Principal) ([]deviceView, error) {
	devs, err := s.devices(ctx)
	if err != nil {
		return nil, err
	}
	var out []deviceView
	for _, d := range devs {
		if d.ID == "" {
			if p.Admin {
				out = append(out, d)
			}
			continue
		}
		l := s.auth.level(ctx, p, d.ID)
		if !l.AtLeast(station.LevelWatch) {
			continue
		}
		d.Level, d.CanDrive = l, l.AtLeast(station.LevelDrive) && !p.ReadOnly
		out = append(out, d)
	}
	return out, nil
}

func (s *server) listDevices(w http.ResponseWriter, r *http.Request, p station.Principal) {
	devs, err := s.visibleDevices(r.Context(), p)
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
// instead, whoever asks, and a free one is taken when the scopes
// include control, or held view only when they do not, a hold anyone
// asking for control takes over at once.
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
	res, err := s.holds.Take(r.Context(), hold.Request{DeviceID: id, Subject: p.Subject, Scopes: scopes, Recall: s.mayRecall(r.Context(), p, id), Watch: body.Watch})
	var queued *hold.Queued
	switch {
	case errors.Is(err, station.ErrNotFound):
		apiError(w, http.StatusNotFound, "no such device")
		return
	case errors.Is(err, hold.ErrWatched):
		apiError(w, http.StatusConflict, "someone is already watching this device")
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
		} else if scope == "" && s.auth.Authorize(r.Context(), p, station.ActionControl, id) == nil {
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

// homePage is /: the Jams page once a model is set and Jams is on, the
// Devices page otherwise, so each page has one address.
// homePage is GET /: the prompt box alone once Jams is on, as the chat
// products open; the devices otherwise.
func (s *server) homePage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if !s.jams.on() {
		http.Redirect(w, r, "/devices", http.StatusSeeOther)
		return
	}
	s.render(w, "home", s.homeData(r, p, nil))
}

func (s *server) indexPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	devs, err := s.visibleDevices(r.Context(), p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "index", map[string]any{"Page": "index", "Devices": devs, "Principal": p, "CSRF": s.auth.csrfToken(r), "JamsOn": s.jams.on()})
}

// drivePage is GET /devices/{rig}/{name}/drive: the viewer that takes
// the device and drives it, or watches with a Hold button when someone
// else has it. viewPage is GET /devices/{rig}/{name}/view: the same
// viewer looking only, which never takes the device and sends no input.
func (s *server) drivePage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.devicePage(w, r, p, false)
}

func (s *server) viewPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.devicePage(w, r, p, true)
}

func (s *server) devicePage(w http.ResponseWriter, r *http.Request, p station.Principal, watch bool) {
	id := deviceID(r)
	dev, err := s.db.Device(r.Context(), id)
	if err != nil || s.auth.Authorize(r.Context(), p, station.ActionView, id) != nil {
		http.NotFound(w, r) // a device not shared with them is not there
		return
	}
	if !watch && s.auth.Authorize(r.Context(), p, station.ActionControl, id) != nil {
		http.Redirect(w, r, "/devices/"+id+"/view", http.StatusSeeOther) // watching is what they may do
		return
	}
	rg, _ := s.db.Rig(r.Context(), r.PathValue("rig"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	q := r.URL.Query()
	if q.Get("minimal") == "1" {
		w.Header().Set("Content-Security-Policy", framedCSP)
	}
	cfg := viewer.Config{Mode: "station", Watch: watch, Device: dev.ID, Name: rg.Name + " / " + dev.Title(), API: viewer.Relayed("", dev.ID),
		InputMode: dev.Input, Shortcuts: dev.Shortcuts, CSRF: s.auth.csrfToken(r), WebRTC: true, Recorded: s.tunnel.RecordsHolds(rg.ID)}
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
	page := viewer.Page{Title: rg.Name + " / " + dev.Title() + " · " + s.title(), Minimal: q.Get("minimal") == "1", Home: "/devices", HomeTitle: "Devices", Config: cfg}
	if watch {
		page.Title = "Watching " + page.Title
		page.Drive = "/devices/" + dev.ID + "/drive"
	} else {
		page.Watch = "/devices/" + dev.ID + "/view"
	}
	viewer.Render(w, page)
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
		"NoPassword": !s.auth.hasUsers(ctx), "TURN": s.turnInfo,
	}
	if n, size, err := s.db.BlobTotal(ctx); err == nil {
		data["RecordsCount"], data["RecordsTotal"] = n, humanSize(size)
	}
	data["RecordsDir"], data["RecordsMaxFile"], data["RecordsMaxTotal"], data["RecordsMaxAge"] =
		s.recordsDir, humanSize(s.records.MaxFile), humanSize(s.records.MaxTotal), humanAge(s.records.MaxAge)
	s.modelsData(data)
	s.teamsData(ctx, p, data)
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (s *server) settingsPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.render(w, "settings", s.settingsData(r, p, nil))
}

// createToken is POST /settings/tokens: an admin makes a token for any
// user, within that user's ceiling.
func (s *server) createToken(w http.ResponseWriter, r *http.Request, p station.Principal) {
	ctx := r.Context()
	userID := r.FormValue("user")
	if userID == "" {
		userID = p.User
	}
	u, err := s.db.User(ctx, userID)
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "No such user."}))
		return
	}
	secret, err := s.makeToken(ctx, p, u, r.FormValue("name"), r.FormValue("level"))
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": capitalize(err.Error()) + "."}))
		return
	}
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"NewToken": secret, "NewTokenName": strings.TrimSpace(r.FormValue("name"))}))
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
	data["JamsOn"] = s.jams.on()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Every page is one person's view of the moment and changes under
	// its own forms, so Back refetches it rather than showing the copy
	// the browser kept.
	w.Header().Set("Cache-Control", "no-store")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

var _ = fmt.Sprintf
