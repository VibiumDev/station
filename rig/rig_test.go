package rig

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
	"github.com/VibiumDev/station/tunnel"
)

func TestKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "rig.key")
	sk, created, err := LoadOrCreateKey(path)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	again, created, err := LoadOrCreateKey(path)
	if err != nil || created || again != sk {
		t.Fatalf("reload: %v %v same=%v", created, err, again == sk)
	}
	if pk, err := ParseNpub(Npub(sk.Public())); err != nil || pk != sk.Public() {
		t.Errorf("npub round trip: %v", err)
	}
}

func TestPolicyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig.json")
	p, err := LoadPolicy(path)
	if err != nil || len(p.Stations) != 0 {
		t.Fatalf("missing file: %+v %v", p, err)
	}
	st := nostr.Generate().Public()
	p.Set(&StationEntry{Name: "home", Npub: Npub(st), URL: "https://station.local:8443", RigID: "rig_1", Scopes: []string{"view", "input"}, Hours: "09:00-18:00"})
	p.Set(&StationEntry{Name: "home2", Npub: Npub(st), URL: "https://station.local:8443", RigID: "rig_1", Scopes: []string{"view"}})
	if len(p.Stations) != 1 || p.Stations[0].Name != "home2" {
		t.Errorf("set did not replace: %+v", p.Stations)
	}
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadPolicy(path)
	if err != nil || back.Station(st) == nil || back.Station(st).RigID != "rig_1" {
		t.Fatalf("reload: %+v %v", back, err)
	}
	e := &StationEntry{Scopes: []string{"view"}, Hours: "22:00-06:00", MaxSession: "2h"}
	if !e.Allows(station.ScopeView) || e.Allows(station.ScopeInput) || e.Allows(station.ScopeAdmin) {
		t.Error("scopes")
	}
	e.Admin = true
	if !e.Allows(station.ScopeAdmin) {
		t.Error("admin")
	}
	if e.MaxSessionDuration() != 2*time.Hour {
		t.Error("max session")
	}
	at := func(h int) time.Time { return time.Date(2026, 10, 28, h, 30, 0, 0, time.Local) }
	if !e.InHours(at(23)) || !e.InHours(at(3)) || e.InHours(at(12)) {
		t.Error("hours over midnight")
	}
	e.Hours = "nonsense"
	if e.InHours(at(12)) {
		t.Error("a malformed window should allow nothing")
	}
}

// gateFixture is a rig with one station in its policy.
type gateFixture struct {
	gate       *Gate
	stationKey nostr.SecretKey
	rigKey     nostr.SecretKey
	entry      *StationEntry
	clock      time.Time
	ended      chan string
}

func newGate(t *testing.T, statePath string) *gateFixture {
	t.Helper()
	f := &gateFixture{stationKey: nostr.Generate(), rigKey: nostr.Generate(), clock: time.Date(2026, 10, 28, 12, 0, 0, 0, time.Local), ended: make(chan string, 16)}
	f.entry = &StationEntry{Name: "home", Npub: Npub(f.stationKey.Public()), RigID: "rig_1", Scopes: []string{"view", "input"}, MaxSession: "1h"}
	f.gate = &Gate{Policy: &Policy{Stations: []*StationEntry{f.entry}}, Key: f.rigKey.Public(),
		Devices: func() []string { return []string{"rig_1/phone"} }, StatePath: statePath,
		Now:   func() time.Time { return f.clock },
		OnEnd: func(device, hold string) { f.ended <- device + ":" + hold }, Logf: t.Logf}
	return f
}

func (f *gateFixture) grant(t *testing.T, sk nostr.SecretKey, hold string, seq uint64, scopes ...station.Scope) nostr.Event {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []station.Scope{station.ScopeView, station.ScopeInput}
	}
	ev, err := token.SignGrant(sk, token.Grant{Rig: f.rigKey.Public(), RigID: "rig_1", DeviceID: "rig_1/phone", Subject: "owner",
		HoldID: hold, Seq: seq, Scopes: scopes, IssuedAt: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func (f *gateFixture) wantEnded(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-f.ended:
		if got != want {
			t.Errorf("ended %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("no end for %q", want)
	}
}

func TestGate(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	f := newGate(t, statePath)
	g := f.gate
	ok := f.grant(t, f.stationKey, "hld_1", 1)
	if _, err := g.Check(ok, station.ScopeInput); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if _, err := g.Check(ok, station.ScopeInput); !errors.Is(err, ErrReplay) {
		t.Errorf("replayed: %v", err)
	}
	if hold, held := g.Current("rig_1/phone"); !held || hold != "hld_1" {
		t.Errorf("current: %q %v", hold, held)
	}
	// A refresh of the same hold is a new event.
	f.clock = f.clock.Add(time.Minute)
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_1", 1), station.ScopeView); err != nil {
		t.Errorf("refresh: %v", err)
	}
	cases := []struct {
		name string
		ev   nostr.Event
		need station.Scope
		want error
	}{
		{"missing scope", f.grant(t, f.stationKey, "hld_1", 1, station.ScopeView), station.ScopeInput, ErrScope},
		{"outside the policy", f.grant(t, f.stationKey, "hld_1", 1, station.ScopeView, station.ScopeBidi), station.ScopeView, ErrPolicy},
		{"admin not allowed", f.grant(t, f.stationKey, "hld_1", 1, station.ScopeView, station.ScopeAdmin), station.ScopeView, ErrPolicy},
		{"station not in the policy", f.grant(t, nostr.Generate(), "hld_1", 1), station.ScopeView, token.ErrStation},
		{"older seq", f.grant(t, f.stationKey, "hld_0", 0), station.ScopeView, ErrOlderHold},
		{"expired", func() nostr.Event {
			ev, _ := token.SignGrant(f.stationKey, token.Grant{Rig: f.rigKey.Public(), RigID: "rig_1", DeviceID: "rig_1/phone", Subject: "owner",
				HoldID: "hld_1", Seq: 1, Scopes: []station.Scope{station.ScopeView}, IssuedAt: f.clock.Add(-time.Hour)})
			return ev
		}(), station.ScopeView, token.ErrExpired},
		{"wrong device", func() nostr.Event {
			ev, _ := token.SignGrant(f.stationKey, token.Grant{Rig: f.rigKey.Public(), RigID: "rig_1", DeviceID: "rig_1/tablet", Subject: "owner",
				HoldID: "hld_1", Seq: 1, Scopes: []station.Scope{station.ScopeView}, IssuedAt: f.clock})
			return ev
		}(), station.ScopeView, token.ErrDevice},
	}
	for _, c := range cases {
		if _, err := g.Check(c.ev, c.need); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	// A newer hold ends the old one.
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_2", 2), station.ScopeInput); err != nil {
		t.Fatalf("newer hold: %v", err)
	}
	f.wantEnded(t, "rig_1/phone:hld_1")
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_1", 1), station.ScopeView); !errors.Is(err, ErrOlderHold) {
		t.Errorf("old hold after the new one: %v", err)
	}
	// A restart keeps the state.
	g2 := newGate(t, statePath)
	g2.stationKey, g2.rigKey, g2.clock = f.stationKey, f.rigKey, f.clock
	g2.gate.Policy, g2.gate.Key = g.Policy, g.Key
	if _, err := g2.gate.Check(f.grant(t, f.stationKey, "hld_1", 1), station.ScopeView); !errors.Is(err, ErrOlderHold) {
		t.Errorf("older hold after restart: %v", err)
	}
	// Revocation ends the hold and refuses later grants on it.
	rev, _ := token.SignRevocation(f.stationKey, token.Revocation{Rig: f.rigKey.Public(), HoldID: "hld_2", IssuedAt: f.clock})
	if err := g.Revoke(rev); err != nil {
		t.Fatal(err)
	}
	f.wantEnded(t, "rig_1/phone:hld_2")
	if _, held := g.Current("rig_1/phone"); held {
		t.Error("held after revocation")
	}
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_2", 2), station.ScopeView); !errors.Is(err, ErrRevoked) {
		t.Errorf("grant on a revoked hold: %v", err)
	}
	strangerRev, _ := token.SignRevocation(nostr.Generate(), token.Revocation{Rig: f.rigKey.Public(), HoldID: "hld_3"})
	if err := g.Revoke(strangerRev); !errors.Is(err, token.ErrStation) {
		t.Errorf("stranger's revocation: %v", err)
	}
	// Session length: the policy says one hour.
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_3", 3), station.ScopeView); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(61 * time.Minute)
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_3", 3), station.ScopeView); !errors.Is(err, ErrSessionLength) {
		t.Errorf("long session: %v", err)
	}
	// Hours.
	f.entry.Hours = "00:00-01:00"
	if _, err := g.Check(f.grant(t, f.stationKey, "hld_4", 4), station.ScopeView); !errors.Is(err, ErrHours) {
		t.Errorf("outside hours: %v", err)
	}
	f.entry.Hours = ""
}

func TestGateTwoStations(t *testing.T) {
	f := newGate(t, "")
	other := nostr.Generate()
	f.gate.Policy.Set(&StationEntry{Name: "hosted", Npub: Npub(other.Public()), RigID: "rig_1", Scopes: []string{"view", "input"}})
	if _, err := f.gate.Check(f.grant(t, f.stationKey, "hld_1", 1), station.ScopeInput); err != nil {
		t.Fatal(err)
	}
	// The other station's hold must wait for this one to run out.
	if _, err := f.gate.Check(f.grant(t, other, "hld_x", 1), station.ScopeInput); !errors.Is(err, ErrBusy) {
		t.Errorf("busy: %v", err)
	}
	f.clock = f.clock.Add(token.GrantLifetime + time.Minute)
	if _, err := f.gate.Check(f.grant(t, other, "hld_x", 1), station.ScopeInput); err != nil {
		t.Errorf("after the hold ran out: %v", err)
	}
	if hold, _ := f.gate.Current("rig_1/phone"); hold != "hld_x" {
		t.Errorf("current %q", hold)
	}
}

func TestAllowlistAndMiddleware(t *testing.T) {
	f := newGate(t, "")
	allow := Allowlist{
		{Path: "/health", Scope: station.ScopeView},
		{Method: "POST", Path: "/api/hid/", Scope: station.ScopeInput},
		{Path: "/api/webrtc/signal", Scope: station.ScopeView, FirstMessage: true},
	}
	var seen token.Grant
	var check func(nostr.Event) (token.Grant, error)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = GrantFrom(r.Context())
		check = FirstMessageCheck(r.Context())
		io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(f.gate.Middleware(allow, next))
	defer srv.Close()
	do := func(method, path, auth string) int {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	grant := f.grant(t, f.stationKey, "hld_1", 1)
	if c := do("GET", "/health", ""); c != 401 {
		t.Errorf("no header: %d", c)
	}
	if c := do("GET", "/health", "Bearer x"); c != 401 {
		t.Errorf("bearer: %d", c)
	}
	if c := do("GET", "/api/settings", token.Header(grant)); c != 403 {
		t.Errorf("not in the allowlist: %d", c)
	}
	if c := do("GET", "/api/hid/type", token.Header(grant)); c != 403 {
		t.Errorf("wrong method: %d", c)
	}
	if c := do("GET", "/health", token.Header(grant)); c != 200 || seen.HoldID != "hld_1" {
		t.Errorf("allowed: %d %+v", c, seen)
	}
	if c := do("GET", "/health", token.Header(grant)); c != 403 {
		t.Errorf("replayed header: %d", c)
	}
	viewOnly := f.grant(t, f.stationKey, "hld_1", 1, station.ScopeView)
	if c := do("POST", "/api/hid/type", token.Header(viewOnly)); c != 403 {
		t.Errorf("view-only on input: %d", c)
	}
	if c := do("POST", "/api/hid/type", token.Header(f.grant(t, f.stationKey, "hld_1", 1))); c != 200 {
		t.Errorf("input: %d", c)
	}
	if c := do("GET", "/api/webrtc/signal", ""); c != 200 || check == nil {
		t.Errorf("first-message endpoint: %d, check %v", c, check != nil)
	}
	if _, err := check(f.grant(t, f.stationKey, "hld_1", 1)); err != nil {
		t.Errorf("first-message check: %v", err)
	}
}

// stationFixture is a station for enrollment and tunnel tests.
type stationFixture struct {
	db     *sqlite.DB
	key    nostr.SecretKey
	tunnel *tunnel.Server
	ts     *httptest.Server
	online chan bool
}

func newStation(t *testing.T) *stationFixture {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := &stationFixture{db: db, key: nostr.Generate(), online: make(chan bool, 16)}
	s.tunnel = &tunnel.Server{Key: s.key.Public(), Devices: db, Events: db, Logf: t.Logf, OnOnline: func(_ string, up bool) { s.online <- up }}
	mux := http.NewServeMux()
	s.ts = httptest.NewUnstartedServer(mux)
	s.ts.StartTLS()
	t.Cleanup(s.ts.Close)
	h := &enroll.Handler{Key: s.key, Name: "test station", TunnelURL: "wss" + strings.TrimPrefix(s.ts.URL, "https") + "/v1/tunnel",
		Devices: db, Events: db, Codes: db, Audit: db, Logf: t.Logf}
	mux.HandleFunc("/v1/station", h.ServeProfile)
	mux.HandleFunc("/v1/enroll", h.ServeEnroll)
	mux.Handle("/v1/tunnel", s.tunnel)
	return s
}

func (s *stationFixture) code(t *testing.T, ttl time.Duration) string {
	t.Helper()
	code := enroll.NewCode(s.key.Public())
	if err := s.db.SaveCode(context.Background(), code, time.Now().Add(ttl)); err != nil {
		t.Fatal(err)
	}
	return code
}

func TestEnrollAndRun(t *testing.T) {
	s := newStation(t)
	ctx := context.Background()
	rigKey := nostr.Generate()
	offers := []enroll.Offer{{Name: "phone", Kind: "phone", Caps: []string{"video", "hid"}, Shortcuts: []station.Shortcut{{Label: "Home", Keys: "GUI+h"}}}}

	// Wrong fingerprint: the code is for another station.
	code := s.code(t, enroll.CodeLifetime)
	wrong := code[:10] + "0000"
	if _, err := Enroll(ctx, EnrollOptions{URL: s.ts.URL, Code: wrong, Name: "pi", Devices: offers, Key: rigKey}); !errors.Is(err, ErrFingerprint) {
		t.Errorf("wrong fingerprint: %v", err)
	}
	// The right code, typed loosely.
	entry, err := Enroll(ctx, EnrollOptions{URL: s.ts.URL, Code: strings.ToLower(strings.ReplaceAll(code, "-", " ")), Name: "pi", Devices: offers, Key: rigKey})
	if err != nil {
		t.Fatal(err)
	}
	if entry.RigID == "" || entry.Name != "test station" || entry.CertSHA256 == "" || entry.TunnelURL == "" || entry.Npub != Npub(s.key.Public()) {
		t.Fatalf("entry: %+v", entry)
	}
	rig, err := s.db.RigByKey(ctx, rigKey.Public())
	if err != nil || rig.Name != "pi" || len(rig.Devices) != 1 || rig.Devices[0].ID != entry.RigID+"/phone" {
		t.Fatalf("stored rig: %+v %v", rig, err)
	}
	if n, _ := s.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindEnrollment}}); n != 1 {
		t.Errorf("enrollment events: %d", n)
	}
	// Reused and expired codes.
	if _, err := Enroll(ctx, EnrollOptions{URL: s.ts.URL, Code: code, Name: "pi", Devices: offers, Key: rigKey}); err == nil || !strings.Contains(err.Error(), "code") {
		t.Errorf("reused code: %v", err)
	}
	if _, err := Enroll(ctx, EnrollOptions{URL: s.ts.URL, Code: s.code(t, -time.Second), Name: "pi", Devices: offers, Key: rigKey}); err == nil || !strings.Contains(err.Error(), "code") {
		t.Errorf("expired code: %v", err)
	}
	// The same key enrolling again keeps its id.
	again, err := Enroll(ctx, EnrollOptions{URL: s.ts.URL, Code: s.code(t, enroll.CodeLifetime), Name: "pi two", Devices: offers, Key: rigKey})
	if err != nil || again.RigID != entry.RigID {
		t.Errorf("re-enroll: %+v %v", again, err)
	}

	// The pinned certificate works, and a changed one is refused.
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: TLSConfig(entry)}}
	if resp, err := hc.Get(s.ts.URL + "/v1/station"); err != nil {
		t.Errorf("pinned: %v", err)
	} else {
		resp.Body.Close()
	}
	changed := *entry
	changed.CertSHA256 = strings.Repeat("0", 64)
	hc = &http.Client{Transport: &http.Transport{TLSClientConfig: TLSConfig(&changed)}}
	if resp, err := hc.Get(s.ts.URL + "/v1/station"); err == nil {
		resp.Body.Close()
		t.Error("changed certificate accepted")
	}

	// Run the rig: the tunnel comes up, the devices are announced, a
	// relayed request with a grant passes the gate, a revocation ends it.
	policy := &Policy{}
	policy.Set(entry)
	ended := make(chan string, 4)
	r := &Rig{Key: rigKey, Policy: policy, Devices: offers, Logf: t.Logf,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "pong") }),
		Allow:   Allowlist{{Path: "/health", Scope: station.ScopeView}},
		Gate:    &Gate{OnEnd: func(device, hold string) { ended <- device + ":" + hold }}}
	r.Gate.Policy, r.Gate.Key = policy, rigKey.Public()
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.Run(rctx)
	select {
	case <-s.online:
	case <-time.After(10 * time.Second):
		t.Fatal("tunnel did not come up")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _ := s.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindRigAnnounce, token.KindDeviceAnnounce}})
		if n == 2 && len(r.Online()) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("announced events %d, online %v", n, r.Online())
		}
		time.Sleep(50 * time.Millisecond)
	}
	dev, err := s.db.Device(ctx, entry.RigID+"/phone")
	if err != nil || len(dev.Shortcuts) != 1 || !dev.Has("hid") {
		t.Errorf("announced device: %+v %v", dev, err)
	}
	grant, _ := token.SignGrant(s.key, token.Grant{Rig: rigKey.Public(), RigID: entry.RigID, DeviceID: entry.RigID + "/phone", Subject: "owner",
		HoldID: "hld_1", Seq: 1, Scopes: []station.Scope{station.ScopeView}})
	hc = &http.Client{Transport: s.tunnel.Transport(entry.RigID)}
	req, _ := http.NewRequest("GET", "http://rig/health", nil)
	req.Header.Set("Authorization", token.Header(grant))
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "pong" {
		t.Errorf("relayed: %d %q", resp.StatusCode, body)
	}
	req, _ = http.NewRequest("GET", "http://rig/health", nil)
	if resp, err := hc.Do(req); err != nil || resp.StatusCode != 401 {
		t.Errorf("relayed without a grant: %v %v", resp, err)
	}
	rev, _ := token.SignRevocation(s.key, token.Revocation{Rig: rigKey.Public(), HoldID: "hld_1"})
	if err := s.tunnel.Revoke(entry.RigID, rev); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ended:
		if got != entry.RigID+"/phone:hld_1" {
			t.Errorf("ended %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Error("revocation did not end the hold")
	}
}
