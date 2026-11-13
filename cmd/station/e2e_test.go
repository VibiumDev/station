package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/jam"
	"github.com/VibiumDev/station/records"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/signal"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
	"github.com/VibiumDev/station/tunnel"
)

// A whole station in memory, driven through the handler the binary
// serves: two rigs with a device each, users at every level on device A
// and none on device B, a jam the admin started on A, a private
// recording of A, and tokens of every level. The security tests live on
// top of it.

const (
	devA, devB = "rig_a/phone", "rig_b/phone"
	jamTest    = "jam_test"
)

type fullStation struct {
	t       *testing.T
	s       *server
	h       http.Handler
	db      *sqlite.DB
	ctx     context.Context
	rigKey  nostr.SecretKey
	users   map[string]station.User // admin, noteam, watch, drive, jam, locked
	teams   map[string]string       // by level
	sha     string                  // the private recording on A, uploaded by the admin
	zip     []byte
	admin   station.Principal
	uploads int
	// spare is a user and a team the management rows change and remove,
	// made again whenever they are missing.
	spareUser, spareTeam string
}

// ensureSpare makes the spare user and team when a row removed them.
func (f *fullStation) ensureSpare() {
	f.t.Helper()
	if _, err := f.db.UserByLogin(f.ctx, "spare@example.com"); err != nil {
		u, _, err := f.s.addUser(f.ctx, f.admin, "spare@example.com", "Spare", false)
		if err != nil {
			f.t.Fatal(err)
		}
		f.spareUser = u.ID
	}
	teams, _ := f.db.Teams(f.ctx)
	found := false
	for _, t := range teams {
		if t.Name == "spare team" {
			f.spareTeam, found = t.ID, true
		}
	}
	if !found {
		t, err := f.s.addTeam(f.ctx, f.admin, "spare team")
		if err != nil {
			f.t.Fatal(err)
		}
		f.spareTeam = t.ID
	}
}

func newFullStation(t *testing.T) *fullStation {
	t.Helper()
	dir := t.TempDir()
	key, err := loadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	ctx := context.Background()
	cfg := &Config{Name: "Test station", Allow: []string{"root@example.com"}}
	who := func(ctx context.Context, remoteAddr string) (string, string, []string, error) {
		switch ip(remoteAddr) {
		case "100.64.0.1":
			return "root@example.com", "Root", nil, nil
		case "100.64.0.2":
			return "stranger@example.com", "Stranger", nil, nil
		}
		return "", "", nil, nil
	}
	a := newAuth(cfg, db, who, false, false).withDeviceKey(key[:])
	a.delay = 0
	mgr := &hold.Manager{Holds: db, Devices: db, Events: db, Audit: db, Key: key}
	ts := &tunnel.Server{Key: key.Public(), Devices: db, Events: db}
	rl := &relay.Relay{Tunnel: ts}
	mgr.OnEnd = func(h station.Hold) { rl.End(h.ID) }
	mgr.InUse = func(holdID string) bool { return rl.Open(holdID) > 0 }
	s := &server{cfg: cfg, dir: dir, db: db, key: key, auth: a, holds: mgr, tunnel: ts, relay: rl, mode: "test", publicURL: "https://st.example",
		enroll: &enroll.Handler{Key: key, Name: cfg.Name, TunnelURL: "wss://st.example/v1/tunnel", Devices: db, Events: db, Codes: db, Audit: db},
		signal: &signal.Handler{Relay: rl, Devices: db}}
	blobDir := filepath.Join(dir, "blobs")
	s.records = &records.Store{Blobs: blob.Disk{Dir: blobDir}, Index: db, Events: db, Holds: db, Devices: db, Signer: records.Key(key),
		HoldsOf: db.HoldsOf, BaseURL: s.publicURL, UnpackDir: filepath.Join(blobDir, "unpacked"), MaxFile: 1 << 20, MaxTotal: 8 << 20, MaxAge: 24 * time.Hour, Audit: db}
	s.recordsDir = blobDir
	s.records.Access = func(ctx context.Context, p station.Principal, deviceID string) bool {
		return s.auth.level(ctx, p, deviceID).AtLeast(station.LevelWatch)
	}
	s.jams = &jams{s: s, models: &jam.Store{Dir: filepath.Join(dir, "ai"), Getenv: func(string) string { return "" }}, dir: filepath.Join(dir, "jams"), live: map[string]*liveJam{}}
	if err := s.jams.models.Save(jam.ModelConfig{Name: "test", Role: jam.RoleAgent, Provider: "local", Model: "test-model", Default: true}); err != nil {
		t.Fatal(err)
	}
	f := &fullStation{t: t, s: s, h: s.handler(), db: db, ctx: ctx, rigKey: nostr.Generate(), users: map[string]station.User{}, teams: map[string]string{}}
	now := time.Now()
	for _, rg := range []struct{ id, name string }{{"rig_a", "pi-a"}, {"rig_b", "pi-b"}} {
		k := f.rigKey
		if rg.id == "rig_b" {
			k = nostr.Generate()
		}
		if err := db.PutRig(ctx, station.Rig{ID: rg.id, Name: rg.name, PubKey: k.Public(), EnrolledAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := db.SetDevices(ctx, rg.id, []station.Device{{ID: rg.id + "/phone", Kind: "phone", Caps: []string{station.CapVideo, station.CapHID, station.CapBidi}, Extras: "/viewer/extras.js"}}); err != nil {
			t.Fatal(err)
		}
	}
	// The admin, and a regular user at each level on device A.
	hash, _ := hashPassword("owner-secret-123")
	if _, err := db.SeedOwner(ctx, hash); err != nil {
		t.Fatal(err)
	}
	owner, _ := db.UserByLogin(ctx, station.OwnerLogin)
	f.users["admin"] = owner
	f.admin = principalOf(owner, "")
	for _, name := range []string{"noteam", "watch", "drive", "jam", "locked"} {
		u, _, err := s.addUser(ctx, f.admin, name+"@example.com", strings.ToUpper(name[:1])+name[1:], false)
		if err != nil {
			t.Fatal(err)
		}
		f.users[name] = u
	}
	for _, level := range []string{"watch", "drive", "jam"} {
		team, err := s.addTeam(ctx, f.admin, level+" team")
		if err != nil {
			t.Fatal(err)
		}
		f.teams[level] = team.ID
		s.setMember(ctx, f.admin, team.ID, f.users[level].ID, true)
		if err := s.setAccess(ctx, f.admin, devA, team.ID, level); err != nil {
			t.Fatal(err)
		}
	}
	for name := range f.users {
		f.users[name], _ = db.User(ctx, f.users[name].ID) // with teams
		if name != "admin" {
			db.SetPassword(ctx, f.users[name].ID, hash)
		}
	}
	f.ensureSpare()
	// A jam the admin started on A, a hold of theirs there, and a
	// recording of it.
	if err := db.CreateJam(ctx, station.Jam{ID: jamTest, Subject: "owner", DeviceID: devA, Model: "test", Prompt: "check the clock", Status: "running", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, station.Hold{ID: "hld_a", DeviceID: devA, RigID: "rig_a", Subject: "owner", Scopes: []station.Scope{station.ScopeView, station.ScopeInput},
		CreatedAt: now.Add(-time.Hour), ActiveAt: now.Add(-time.Hour), ExpiresAt: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	fw, _ := zw.Create("x.txt")
	fw.Write([]byte("hello"))
	zw.Close()
	f.zip = zbuf.Bytes()
	sum := sha256.Sum256(f.zip)
	f.sha = hex.EncodeToString(sum[:])
	f.ensureBlob()
	return f
}

// ensureBlob uploads the recording when it is not there, as the admin's
// own: a row may have deleted it.
func (f *fullStation) ensureBlob() {
	f.t.Helper()
	if _, err := f.db.Blob(f.ctx, f.sha); err == nil {
		return
	}
	f.uploads++
	name := "rec-" + strconv.Itoa(f.uploads) + ".zip" // each upload is its own file event
	ev, err := f.s.records.UploadAuth(f.ctx, f.admin, records.UploadRequest{SHA256: f.sha, Size: int64(len(f.zip)), Type: "application/zip", Name: name, HoldID: "hld_a"})
	if err != nil {
		f.t.Fatalf("upload auth: %v", err)
	}
	r := httptest.NewRequest("PUT", "/upload", bytes.NewReader(f.zip))
	r.ContentLength = int64(len(f.zip))
	r.Header.Set("Authorization", records.Header(ev))
	r.Header.Set("Content-Type", "application/zip")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 200 {
		f.t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	// Public again is private: a row may have made it public.
	f.db.SetBlobPublic(f.ctx, f.sha, false)
}

// caller is one kind of request-maker and what the specs promise it.
type caller struct {
	name   string
	decor  func(r *http.Request)
	csrf   string
	signed bool // reaches handlers at all
	admin  bool
	person bool
	token  station.TokenLevel // "" for a person
	levelA station.Level      // on device A; admins have everything everywhere
	bound  string             // the one device a token may act on
}

func (f *fullStation) session(userID string, st stage) (*http.Cookie, string) {
	w := httptest.NewRecorder()
	sess := f.s.auth.begin(w, userID, st, "/")
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c, sess.csrf
		}
	}
	f.t.Fatal("no session cookie")
	return nil, ""
}

func withCookie(c *http.Cookie) func(*http.Request) { return func(r *http.Request) { r.AddCookie(c) } }
func withBearer(secret string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+secret) }
}
func fromAddr(addr string) func(*http.Request) { return func(r *http.Request) { r.RemoteAddr = addr } }

// callers makes every kind of caller against the station.
func (f *fullStation) callers() []caller {
	u := f.users
	tok := func(name string, level station.TokenLevel, userID string) string {
		secret, err := f.db.CreateToken(f.ctx, name, level, userID)
		if err != nil {
			f.t.Fatal(err)
		}
		return secret
	}
	sess := func(name string, who string, st stage, c *caller) caller {
		ck, csrf := f.session(u[who].ID, st)
		c.name, c.decor, c.csrf = name, withCookie(ck), csrf
		return *c
	}
	lockedCookie, lockedCSRF := f.session(u["locked"].ID, stageSignedIn)
	if err := f.s.setLocked(f.ctx, f.admin, u["locked"].ID, true); err != nil {
		f.t.Fatal(err)
	}
	bound, err := f.db.CreateScopedToken(f.ctx, "jam:"+jamTest, station.TokenDrive, devA, time.Now().Add(time.Hour), u["admin"].ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return []caller{
		{name: "nobody", decor: func(*http.Request) {}},
		sess("code stage", "admin", stageCode, &caller{}),
		sess("setup stage", "admin", stageSetup, &caller{}),
		{name: "locked", decor: withCookie(lockedCookie), csrf: lockedCSRF},
		sess("no team", "noteam", stageSignedIn, &caller{signed: true, person: true}),
		sess("watch user", "watch", stageSignedIn, &caller{signed: true, person: true, levelA: station.LevelWatch}),
		sess("drive user", "drive", stageSignedIn, &caller{signed: true, person: true, levelA: station.LevelDrive}),
		sess("jam user", "jam", stageSignedIn, &caller{signed: true, person: true, levelA: station.LevelJam}),
		sess("admin", "admin", stageSignedIn, &caller{signed: true, person: true, admin: true, levelA: station.LevelJam}),
		{name: "drive user's watch token", decor: withBearer(tok("dw", station.TokenWatch, u["drive"].ID)), signed: true, token: station.TokenWatch, levelA: station.LevelDrive},
		{name: "drive user's drive token", decor: withBearer(tok("dd", station.TokenDrive, u["drive"].ID)), signed: true, token: station.TokenDrive, levelA: station.LevelDrive},
		{name: "drive user's admin token", decor: withBearer(tok("da", station.TokenAdmin, u["drive"].ID)), signed: true, token: station.TokenAdmin, levelA: station.LevelDrive},
		{name: "admin's watch token", decor: withBearer(tok("aw", station.TokenWatch, u["admin"].ID)), signed: true, admin: true, token: station.TokenWatch, levelA: station.LevelJam},
		{name: "admin's drive token", decor: withBearer(tok("ad", station.TokenDrive, u["admin"].ID)), signed: true, admin: true, token: station.TokenDrive, levelA: station.LevelJam},
		{name: "admin's admin token", decor: withBearer(tok("aa", station.TokenAdmin, u["admin"].ID)), signed: true, admin: true, token: station.TokenAdmin, levelA: station.LevelJam},
		{name: "jam's device token", decor: withBearer(bound), signed: true, admin: true, token: station.TokenDrive, levelA: station.LevelJam, bound: devA},
		{name: "allowed identity", decor: fromAddr("100.64.0.1:1"), signed: true, person: true, admin: true, levelA: station.LevelJam},
		{name: "unknown identity", decor: fromAddr("100.64.0.2:1")},
	}
}

// The rules: who a route is for.
type rule func(c caller) bool

var (
	rulePublic = func(c caller) bool { return true }
	ruleSigned = func(c caller) bool { return c.signed }
	rulePerson = func(c caller) bool { return c.signed && c.person }
	// On device A, by level; a token adds its own cap, and a bound token
	// acts on its device alone.
	ruleReadA = func(c caller) bool {
		return c.signed && (c.admin || c.levelA.AtLeast(station.LevelWatch)) && (c.bound == "" || c.bound == devA)
	}
	ruleControlA = func(c caller) bool {
		return ruleReadA(c) && (c.admin || c.levelA.AtLeast(station.LevelDrive)) && c.token != station.TokenWatch
	}
	ruleBidiA = func(c caller) bool {
		return ruleReadA(c) && (c.admin || c.levelA.AtLeast(station.LevelJam)) && c.token != station.TokenWatch
	}
	// Device B was shared with nobody: admins alone, and no token bound to A.
	ruleReadB    = func(c caller) bool { return c.signed && c.admin && c.bound == "" }
	ruleControlB = func(c caller) bool { return ruleReadB(c) && c.token != station.TokenWatch }
	// A change about no device in particular: anyone signed in but a watch token.
	ruleChange = func(c caller) bool { return c.signed && c.token != station.TokenWatch }
	// The station's management: admins in person or by admin token.
	ruleManage = func(c caller) bool {
		return c.signed && c.admin && (c.person || c.token == station.TokenAdmin)
	}
	ruleAdminRead = func(c caller) bool { return c.signed && c.admin && c.bound == "" }
	// The admin's jam on A: its starter, admins and jam-level people act
	// on it, never a watch token.
	ruleJamAct = func(c caller) bool { return ruleBidiA(c) }
	// The admin's recording: changes are for admins with admin power and
	// for the uploader, in person or through a drive token of theirs; a
	// watch token changes nothing, and other users' tokens are not the
	// uploader's.
	ruleOwnRecord = func(c caller) bool { return c.signed && c.admin && c.token != station.TokenWatch }
)

type row struct {
	method, pattern, path string
	body                  string // JSON for /v1, form fields otherwise
	rule                  rule
	// soft says the answer for an allowed caller depends on state the
	// matrix does not arrange (a hold someone else has, a screenshot
	// never taken): they must not be turned away as nobody, but a 403 or
	// 404 from the handler itself is no verdict. The disallowed are
	// still refused.
	soft bool
	// spare says the path and body name the spare user and team, filled
	// in at request time since a row may have removed and remade them.
	spare bool
}

// fill puts the spare ids into a row's path and body.
func (f *fullStation) fill(s string) string {
	s = strings.ReplaceAll(s, "{spareUser}", f.spareUser)
	return strings.ReplaceAll(s, "{spareTeam}", f.spareTeam)
}

func (f *fullStation) rows() []row {
	A, B := devA, devB
	return []row{
		{"GET", "/{$}", "/", "", ruleSigned, false, false},
		{"GET", "/devices", "/devices", "", ruleSigned, false, false},
		{"GET", "/jams", "/jams", "", ruleSigned, false, false},
		{"POST", "/jams", "/jams", "prompt=hi&device=" + A, ruleChange, false, false},
		{"GET", "/jams/{id}", "/jams/" + jamTest, "", ruleReadA, false, false},
		{"POST", "/jams/{id}/turns", "/jams/" + jamTest + "/turns", "prompt=more", ruleJamAct, false, false},
		{"POST", "/jams/{id}/stop", "/jams/" + jamTest + "/stop", "", ruleJamAct, false, false},
		{"POST", "/jams/{id}/keep", "/jams/" + jamTest + "/keep", "", ruleJamAct, false, false},
		{"GET", "/jams/{id}/shots/{seq}", "/jams/" + jamTest + "/shots/1", "", ruleReadA, true, false},
		{"GET", "/devices/{rig}/{name}/drive", "/devices/" + A + "/drive", "", ruleReadA, false, false},
		{"GET", "/devices/{rig}/{name}/view", "/devices/" + A + "/view", "", ruleReadA, false, false},
		{"GET", "/devices/{rig}/{name}/drive", "/devices/" + B + "/drive", "", ruleReadB, false, false},
		{"GET", "/devices/{rig}/{name}/view", "/devices/" + B + "/view", "", ruleReadB, false, false},
		{"GET", "/records", "/records", "", ruleSigned, false, false},
		{"GET", "/records/{sha}/play", "/records/" + f.sha + "/play", "", ruleReadA, false, false},
		{"GET", "/records/{sha}/thumb.jpg", "/records/" + f.sha + "/thumb.jpg", "", ruleReadA, true, false},
		{"GET", "/records/{sha}/frames/{frame}", "/records/" + f.sha + "/frames/0.jpg", "", ruleReadA, true, false},
		{"GET", "/records/{sha}/files/{path...}", "/records/" + f.sha + "/files/x.txt", "", ruleReadA, false, false},
		{"POST", "/records/public", "/records/public", "sha256=" + f.sha + "&public=1", ruleOwnRecord, false, false},
		{"POST", "/records/delete", "/records/delete", "sha256=" + f.sha, ruleOwnRecord, false, false},
		{"POST", "/records/golden", "/records/golden", "sha256=" + f.sha + "&make=golden", ruleOwnRecord, false, false},
		{"POST", "/records/tags", "/records/tags", "sha256=" + f.sha + "&tags=a", ruleOwnRecord, false, false},
		{"GET", "/account", "/account", "", ruleSigned, false, false},
		{"POST", "/account/password", "/account/password", "current=x&password=y&again=y", rulePerson, false, false},
		{"POST", "/account/codes", "/account/codes", "current=x", rulePerson, false, false},
		{"POST", "/account/authenticator", "/account/authenticator", "current=x", rulePerson, false, false},
		{"POST", "/account/tokens", "/account/tokens", "name=t&level=watch", rulePerson, false, false},
		{"POST", "/account/jam-notes", "/account/jam-notes", "notes=x", rulePerson, false, false},
		{"POST", "/account/tokens/revoke", "/account/tokens/revoke", "name=none", rulePerson, false, false},
		{"GET", "/settings", "/settings", "", ruleManage, false, false},
		{"GET", "/settings/ai", "/settings/ai", "", ruleManage, false, false},
		{"GET", "/settings/rigs", "/settings/rigs", "", ruleManage, false, false},
		{"GET", "/settings/access", "/settings/access", "", ruleManage, false, false},
		{"POST", "/settings/name", "/settings/name", "name=x", ruleManage, false, false},
		{"POST", "/settings/tokens", "/settings/tokens", "name=t&level=watch", ruleManage, false, false},
		{"POST", "/settings/tokens/revoke", "/settings/tokens/revoke", "name=none", ruleManage, false, false},
		{"POST", "/settings/codes", "/settings/codes", "", ruleManage, false, false},
		{"POST", "/settings/codes/cancel", "/settings/codes/cancel", "id=1", ruleManage, false, false},
		{"POST", "/settings/piper/voice", "/settings/piper/voice", "voice=en_US-x", ruleManage, false, false},
		{"POST", "/settings/say/preview", "/settings/say/preview", "name=none&text=", ruleManage, false, false},
		{"POST", "/settings/rigs/remove", "/settings/rigs/remove", "rig=rig_none", ruleManage, false, false},
		{"POST", "/settings/models", "/settings/models", "", ruleManage, false, false},
		{"POST", "/settings/models/remove", "/settings/models/remove", "name=none", ruleManage, false, false},
		{"POST", "/settings/models/default", "/settings/models/default", "name=none", ruleManage, false, false},
		{"POST", "/settings/models/key", "/settings/models/key", "name=none&key=k", ruleManage, false, false},
		{"POST", "/settings/jams", "/settings/jams", "on=1", ruleManage, false, false},
		{"POST", "/settings/jam-video", "/settings/jam-video", "on=1", ruleManage, false, false},
		{"POST", "/settings/jam-notes", "/settings/jam-notes", "notes=x", ruleManage, false, false},
		{"POST", "/settings/users", "/settings/users", "login=new@example.com", ruleManage, false, false},
		{"POST", "/settings/users/role", "/settings/users/role", "user=none&admin=1", ruleManage, false, false},
		{"POST", "/settings/users/lock", "/settings/users/lock", "user=none&locked=1", ruleManage, false, false},
		{"POST", "/settings/users/expires", "/settings/users/expires", "user=none&clear=1", ruleManage, false, false},
		{"POST", "/settings/users/reset", "/settings/users/reset", "user=none", ruleManage, false, false},
		{"POST", "/settings/users/remove", "/settings/users/remove", "user=none", ruleManage, false, false},
		{"POST", "/settings/teams", "/settings/teams", "name=t", ruleManage, false, false},
		{"POST", "/settings/teams/remove", "/settings/teams/remove", "team=none", ruleManage, false, false},
		{"POST", "/settings/teams/members", "/settings/teams/members", "team=none&user=none", ruleManage, false, false},
		{"POST", "/settings/access", "/settings/access", "device=" + A + "&team=none&level=watch", ruleManage, false, false},
		{"GET", "/v1/users", "/v1/users", "", ruleManage, false, false},
		{"POST", "/v1/users", "/v1/users", `{"login":"x@example.com"}`, ruleManage, false, false},
		{"PUT", "/v1/users/{id}", "/v1/users/{spareUser}", `{"name":"x"}`, ruleManage, false, true},
		{"POST", "/v1/users/{id}/reset", "/v1/users/{spareUser}/reset", "", ruleManage, false, true},
		{"DELETE", "/v1/users/{id}", "/v1/users/{spareUser}", "", ruleManage, false, true},
		{"GET", "/v1/teams", "/v1/teams", "", ruleManage, false, false},
		{"POST", "/v1/teams", "/v1/teams", `{"name":"x"}`, ruleManage, false, false},
		{"DELETE", "/v1/teams/{id}", "/v1/teams/{spareTeam}", "", ruleManage, false, true},
		{"PUT", "/v1/teams/{id}/members/{user}", "/v1/teams/{spareTeam}/members/{spareUser}", "", ruleManage, false, true},
		{"DELETE", "/v1/teams/{id}/members/{user}", "/v1/teams/{spareTeam}/members/{spareUser}", "", ruleManage, false, true},
		{"GET", "/v1/access", "/v1/access", "", ruleManage, false, false},
		{"PUT", "/v1/access", "/v1/access", `{"device":"` + A + `","team":"{spareTeam}","level":"watch"}`, ruleManage, false, true},
		{"GET", "/v1/devices", "/v1/devices", "", ruleSigned, false, false},
		{"GET", "/v1/models", "/v1/models", "", ruleSigned, false, false},
		{"POST", "/v1/jams", "/v1/jams", `{"prompt":"hi","device":"` + A + `"}`, ruleBidiA, false, false},
		{"GET", "/v1/jams", "/v1/jams", "", ruleSigned, false, false},
		{"GET", "/v1/jams/{id}", "/v1/jams/" + jamTest, "", ruleReadA, false, false},
		{"POST", "/v1/jams/{id}/turns", "/v1/jams/" + jamTest + "/turns", `{"prompt":"more"}`, ruleJamAct, false, false},
		{"GET", "/v1/jams/{id}/events", "/v1/jams/" + jamTest + "/events", "", ruleReadA, false, false},
		{"POST", "/v1/jams/{id}/stop", "/v1/jams/" + jamTest + "/stop", "", ruleJamAct, false, false},
		{"POST", "/v1/jams/{id}/keep", "/v1/jams/" + jamTest + "/keep", "", ruleJamAct, false, false},
		{"POST", "/v1/jams/{id}/title", "/v1/jams/" + jamTest + "/title", `{"title":"named"}`, ruleJamAct, false, false},
		{"GET", "/v1/devices/{rig}/{name}/recordings", "/v1/devices/" + A + "/recordings", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/recordings", "/v1/devices/" + B + "/recordings", "", ruleReadB, false, false},
		{"POST", "/v1/devices/{rig}/{name}/hold", "/v1/devices/" + A + "/hold", "", ruleReadA, false, false},
		{"POST", "/v1/devices/{rig}/{name}/hold", "/v1/devices/" + A + "/hold", `{"scopes":["view","input"]}`, ruleControlA, false, false},
		{"POST", "/v1/devices/{rig}/{name}/hold", "/v1/devices/" + B + "/hold", `{"scopes":["view","input"]}`, ruleControlB, false, false},
		{"DELETE", "/v1/devices/{rig}/{name}/hold", "/v1/devices/" + A + "/hold", "", ruleReadA, true, false},
		{"POST", "/v1/devices/{rig}/{name}/hold/release", "/v1/devices/" + A + "/hold/release", "", ruleReadA, true, false},
		{"POST", "/v1/devices/{rig}/{name}/reservation", "/v1/devices/" + A + "/reservation", "", ruleControlA, true, false},
		{"DELETE", "/v1/devices/{rig}/{name}/reservation", "/v1/devices/" + A + "/reservation", "", ruleControlA, true, false},
		{"GET", "/v1/devices/{rig}/{name}/signal", "/v1/devices/" + A + "/signal", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/stream", "/v1/devices/" + A + "/stream", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/stream", "/v1/devices/" + B + "/stream", "", ruleReadB, false, false},
		{"GET", "/v1/devices/{rig}/{name}/screenshot", "/v1/devices/" + A + "/screenshot", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/hid", "/v1/devices/" + A + "/hid", "", ruleControlA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/audio", "/v1/devices/" + A + "/audio", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/mic", "/v1/devices/" + A + "/mic", "", ruleControlA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/bidi", "/v1/devices/" + A + "/bidi", "", ruleBidiA, false, false},
		{"GET", "/session", "/session", "", ruleChange, true, false},
		{"GET", "/v1/devices/{rig}/{name}/health", "/v1/devices/" + A + "/health", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/extras.js", "/v1/devices/" + A + "/extras.js", "", ruleReadA, false, false},
		{"GET", "/v1/devices/{rig}/{name}/api/{path...}", "/v1/devices/" + A + "/api/hid/status", "", ruleReadA, false, false},
		{"GET", "/v1/records", "/v1/records", "", ruleSigned, false, false},
		{"POST", "/v1/records/upload-auth", "/v1/records/upload-auth", `{"sha256":"` + f.sha + `","size":1}`, ruleChange, false, false},
		{"POST", "/v1/records/{sha}/download-auth", "/v1/records/" + f.sha + "/download-auth", "", ruleReadA, false, false},
		{"POST", "/v1/records/{sha}/golden", "/v1/records/" + f.sha + "/golden", `{"golden":true}`, ruleOwnRecord, false, false},
		{"PUT", "/v1/records/{sha}/tags", "/v1/records/" + f.sha + "/tags", `{"tags":["a"]}`, ruleOwnRecord, false, false},
		{"GET", "/{sha}", "/" + f.sha, "", ruleReadA, false, false},
		{"DELETE", "/{sha}", "/" + f.sha, "", ruleOwnRecord, false, false},
		{"GET", "/list/{pubkey}", "/list/" + f.rigKey.Public().Hex(), "", ruleAdminRead, false, false},
	}
}

// skipped are the patterns the matrix leaves to other tests: the public
// ones, the rig's, and the sign-in flow's.
var skipped = map[string]bool{
	"GET /v1/station": true, "POST /v1/enroll": true, "/v1/tunnel": true, "GET /viewer/": true, "GET /favicon.png": true, "GET /apple-touch-icon.png": true,
	"GET /login": true, "POST /login": true, "GET /login/code": true, "POST /login/code": true, "POST /logout": true,
	"GET /setup/{token}": true, "POST /setup/{token}": true, "GET /account/setup": true, "POST /account/setup": true, "POST /account/setup/cancel": true,
	"PUT /upload": true,
}

func (f *fullStation) do(c caller, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	f.t.Helper()
	var r *http.Request
	switch {
	case body == "":
		r = httptest.NewRequest(method, path, nil)
	case strings.HasPrefix(body, "{"):
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	default:
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.decor(r)
	if csrf && c.csrf != "" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

// denied says the station turned the caller away before doing anything:
// not signed in, not allowed, or hidden (404 for what they may not see).
func denied(w *httptest.ResponseRecorder) bool {
	switch w.Code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect:
		loc := w.Header().Get("Location")
		return strings.HasPrefix(loc, "/login") || strings.HasPrefix(loc, "/account/setup")
	}
	return false
}

func refused(w *httptest.ResponseRecorder) bool { return denied(w) && w.Code != http.StatusNotFound }

// Every route against every caller: the answer is the rule's.
func TestAuthorizationMatrix(t *testing.T) {
	f := newFullStation(t)
	callers := f.callers()
	for _, row := range f.rows() {
		for _, c := range callers {
			f.ensureBlob()
			f.ensureSpare()
			path, body := row.path, row.body
			if row.spare {
				path, body = f.fill(path), f.fill(body)
			}
			w := f.do(c, row.method, path, body, true)
			want := row.rule(c)
			got := !denied(w)
			if row.soft && want && (w.Code == http.StatusForbidden || w.Code == http.StatusNotFound) {
				continue // the handler itself said no for reasons of state; the caller got in
			}
			if want != got {
				t.Errorf("%s %s as %s: %d %s (want allowed=%v)", row.method, path, c.name, w.Code, strings.TrimSpace(firstLine(w.Body.String())), want)
			}
			if !want && !refused(w) && w.Code != http.StatusNotFound {
				t.Errorf("%s %s as %s: let through with %d", row.method, path, c.name, w.Code)
			}
		}
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

// Every pattern the mux knows is in the matrix or skipped on purpose.
func TestMatrixIsComplete(t *testing.T) {
	src, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatal(err)
	}
	f := newFullStation(t)
	known := map[string]bool{}
	for _, r := range f.rows() {
		known[r.method+" "+r.pattern] = true
		known[r.pattern] = true
	}
	re := regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+ )?([^"]+)"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		key := strings.TrimSpace(m[1]) + " " + m[2]
		key = strings.TrimSpace(key)
		if !known[key] && !skipped[key] && !known[m[2]] {
			t.Errorf("route %q is in neither the matrix nor the skipped list", key)
		}
	}
}

// A state-changing request from a session without its CSRF token is
// refused, whoever sends it; with the token it is not refused for that.
func TestCSRFOnEveryChange(t *testing.T) {
	f := newFullStation(t)
	ck, csrf := f.session(f.users["admin"].ID, stageSignedIn)
	admin := caller{name: "admin", decor: withCookie(ck), csrf: csrf, signed: true, admin: true, person: true}
	for _, row := range f.rows() {
		if row.method == "GET" {
			continue
		}
		f.ensureBlob()
		f.ensureSpare()
		path, body := f.fill(row.path), f.fill(row.body)
		if w := f.do(admin, row.method, path, body, false); w.Code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF: %d", row.method, path, w.Code)
		}
		if w := f.do(admin, row.method, path, body, true); w.Code == http.StatusForbidden && strings.Contains(w.Body.String(), "CSRF") {
			t.Errorf("%s %s with CSRF: still refused for it", row.method, path)
		}
	}
	// The Blossom delete from a session needs it too; a bearer never does.
	r := httptest.NewRequest("DELETE", "/"+f.sha, nil)
	r.AddCookie(ck)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("Blossom delete without CSRF: %d", w.Code)
	}
}

// A tailnet identity has no CSRF token to show, so its changes are taken
// from the station's own pages and from clients that are no browser, and
// from no other site.
func TestIdentityChangesComeFromThisStation(t *testing.T) {
	f := newFullStation(t)
	from := func(origin string) caller {
		return caller{name: "allowed identity from " + origin, decor: func(r *http.Request) {
			r.RemoteAddr = "100.64.0.1:1"
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
		}, signed: true, admin: true, person: true}
	}
	for _, row := range f.rows() {
		if row.method == "GET" {
			continue
		}
		f.ensureBlob()
		f.ensureSpare()
		path, body := f.fill(row.path), f.fill(row.body)
		if w := f.do(from("https://evil.example"), row.method, path, body, false); w.Code != http.StatusForbidden {
			t.Errorf("%s %s from another site: %d", row.method, path, w.Code)
		}
		for _, origin := range []string{"", "http://example.com"} {
			if w := f.do(from(origin), row.method, path, body, false); w.Code == http.StatusForbidden && (strings.Contains(w.Body.String(), "CSRF") || strings.Contains(w.Body.String(), "expired")) {
				t.Errorf("%s %s from origin %q: refused as another site's", row.method, path, origin)
			}
			f.ensureBlob()
			f.ensureSpare()
		}
	}
}

// Cookies carry the flags, and a WebSocket from another origin is refused.
func TestCookieFlagsAndOrigin(t *testing.T) {
	f := newFullStation(t)
	for _, tls := range []bool{false, true} {
		f.s.auth.tls = tls
		w := httptest.NewRecorder()
		f.s.auth.begin(w, f.users["admin"].ID, stageSignedIn, "/")
		for _, c := range w.Result().Cookies() {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != tls {
				t.Errorf("tls=%v cookie %s: HttpOnly=%v SameSite=%v Secure=%v", tls, c.Name, c.HttpOnly, c.SameSite, c.Secure)
			}
		}
	}
	f.s.auth.tls = false
	ck, _ := f.session(f.users["admin"].ID, stageSignedIn)
	r := httptest.NewRequest("GET", "/v1/devices/"+devA+"/signal", nil)
	r.AddCookie(ck)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin WebSocket: %d", w.Code)
	}
}

// Nothing the station keeps about a sign-in leaves it: the users API and
// the Account page carry no hash, secret or code, and a locked user's
// session, token and identity all stop at once.
func TestNoSecretsLeakAndLockBites(t *testing.T) {
	f := newFullStation(t)
	by := map[string]caller{}
	for _, c := range f.callers() {
		by[c.name] = c
	}
	drive := f.users["drive"]
	secret := newTOTPSecret()
	f.db.SetSecondFactor(f.ctx, drive.ID, secret, time.Now())
	codes, _ := f.s.auth.newBackupCodes(f.ctx, drive.ID)
	hash, _ := f.db.PasswordHash(f.ctx, drive.ID)
	w := f.do(by["admin"], "GET", "/v1/users", "", true)
	body := w.Body.String()
	for _, leak := range []string{secret, hash, codes[0], hashCode(codes[0]), "password_hash", "totp", "\"secret\""} {
		if strings.Contains(body, leak) {
			t.Errorf("/v1/users carries %q", leak)
		}
	}
	w = f.do(by["drive user"], "GET", "/account", "", true)
	for _, leak := range []string{secret, hash, codes[0]} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("/account carries %q", leak)
		}
	}
	for _, tab := range settingsTabs {
		w = f.do(by["admin"], "GET", tab.URL, "", true)
		for _, leak := range []string{secret, hash, codes[0]} {
			if strings.Contains(w.Body.String(), leak) {
				t.Errorf("%s carries %q", tab.URL, leak)
			}
		}
	}
	// Lock the drive user: their session, their tokens, the device they
	// hold and the pages all stop.
	if w := f.do(by["drive user"], "POST", "/v1/devices/"+devA+"/hold", `{"scopes":["view","input"]}`, true); w.Code != http.StatusOK {
		t.Fatalf("a hold before the lock: %d %s", w.Code, w.Body)
	}
	if err := f.s.setLocked(f.ctx, f.admin, drive.ID, true); err != nil {
		t.Fatal(err)
	}
	if held, _ := f.db.Held(f.ctx); slices.ContainsFunc(held, func(h station.Hold) bool { return h.Subject == drive.Login }) {
		t.Error("a locked user still holds the device")
	}
	for _, who := range []string{"drive user", "drive user's drive token", "drive user's watch token", "drive user's admin token"} {
		if w := f.do(by[who], "GET", "/v1/devices", "", true); w.Code != http.StatusUnauthorized {
			t.Errorf("%s after the lock: %d", who, w.Code)
		}
	}
	if w := f.do(by["drive user"], "GET", "/devices", "", true); !strings.HasPrefix(w.Header().Get("Location"), "/login") {
		t.Errorf("a locked user's page: %d %s", w.Code, w.Header().Get("Location"))
	}
	// A removed user's session and tokens are gone too.
	jam := f.users["jam"]
	if err := f.s.removeUser(f.ctx, f.admin, jam.ID); err != nil {
		t.Fatal(err)
	}
	if w := f.do(by["jam user"], "GET", "/v1/devices", "", true); w.Code != http.StatusUnauthorized {
		t.Errorf("a removed user's session: %d", w.Code)
	}
}

// What a rig answers through the relay arrives under the station's own
// address. It sets no cookie there, and but for the extras script it is
// sandboxed, so a page the rig sends has no origin and runs nothing. The
// viewer's own traffic is as it was: the stream, a snapshot and JSON
// come back byte for byte, and a WebSocket still opens. The rig in turn
// is never shown the browser's Origin or cookies: its own same-origin
// check, which a Roadie makes on every socket, would refuse the station's.
func TestRigAnswersCannotActAsTheStation(t *testing.T) {
	f := newFullStation(t)
	srv := httptest.NewServer(f.h)
	defer srv.Close()
	stream := "--frame\r\nContent-Type: image/jpeg\r\n\r\nJPEG-ONE\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\nJPEG-TWO\r\n"
	var shown atomic.Value // what of the browser's the rig was shown
	rig := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o, c := r.Header.Get("Origin"), r.Header.Get("Cookie"); o != "" || c != "" {
			shown.Store(r.URL.Path + ": Origin " + o + ", Cookie " + c)
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "chosen-by-the-rig", Path: "/"})
		switch r.URL.Path {
		case "/viewer/extras.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Write([]byte("/* the rig's buttons */"))
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
		case "/snapshot":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("JPEG-BYTES"))
		case "/stream":
			w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
			w.Write([]byte(stream))
		case "/audio":
			// The same-origin check a Roadie makes.
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			if typ, data, err := c.Read(r.Context()); err == nil {
				c.Write(r.Context(), typ, append([]byte("heard "), data...))
			}
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<!doctype html><script src="extras.js"></script>`))
		}
	})
	c := &tunnel.Client{URL: "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/tunnel", Key: f.rigKey, Station: f.s.key.Public(), Handler: rig, Logf: func(string, ...any) {},
		Announce: func() ([]nostr.Event, error) {
			devices, _ := json.Marshal([]station.Device{{ID: devA, Kind: "phone", Caps: []string{station.CapVideo}, Extras: "/viewer/extras.js"}})
			ev := nostr.Event{Kind: token.KindRigAnnounce, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", f.s.key.Public().Hex()}, {"name", "pi-a"}}, Content: string(devices)}
			return []nostr.Event{ev}, ev.Sign(f.rigKey)
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	for i := 0; i < 200 && !f.s.tunnel.IsOnline("rig_a"); i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if !f.s.tunnel.IsOnline("rig_a") {
		t.Fatal("the rig did not come online")
	}
	secret, err := f.db.CreateToken(f.ctx, "agent", station.TokenDrive, f.users["admin"].ID)
	if err != nil {
		t.Fatal(err)
	}
	agent := caller{name: "an admin's token", decor: func(r *http.Request) {
		withBearer(secret)(r)
		r.Header.Set("Origin", "https://another.example")
		r.AddCookie(&http.Cookie{Name: "pet", Value: "cat"})
	}}
	for _, c := range []struct {
		path, mediaType, body string
		sandboxed             bool
	}{
		{"/api/anything", "text/html", `<!doctype html><script src="extras.js"></script>`, true},
		{"/health", "application/json", `{"ok":true}`, true},
		{"/screenshot", "image/jpeg", "JPEG-BYTES", true},
		{"/stream", "multipart/x-mixed-replace; boundary=frame", stream, true},
		{"/extras.js", "text/javascript", "/* the rig's buttons */", false},
	} {
		w := f.do(agent, "GET", "/v1/devices/"+devA+c.path, "", false)
		h := w.Header()
		if w.Code != 200 || h.Get("Content-Type") != c.mediaType || w.Body.String() != c.body {
			t.Errorf("%s: %d %s %q", c.path, w.Code, h.Get("Content-Type"), w.Body)
		}
		if len(h.Values("Set-Cookie")) != 0 {
			t.Errorf("%s: the rig's cookie came through: %q", c.path, h.Values("Set-Cookie"))
		}
		want := pageCSP
		if c.sandboxed {
			want += "; sandbox"
		}
		if got := strings.Join(h.Values("Content-Security-Policy"), " | "); got != want {
			t.Errorf("%s: policy %q, want %q", c.path, got, want)
		}
	}
	// A WebSocket through the relay, as the viewer opens for sound.
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/devices/"+devA+"/audio",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + secret}, "Origin": {srv.URL}, "Cookie": {"pet=cat"}}})
	if err != nil {
		t.Fatalf("the relayed WebSocket: %v", err)
	}
	defer ws.CloseNow()
	if len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Errorf("the rig's cookie came through the upgrade: %q", resp.Header.Values("Set-Cookie"))
	}
	ws.Write(ctx, websocket.MessageText, []byte("this"))
	if _, data, err := ws.Read(ctx); err != nil || string(data) != "heard this" {
		t.Errorf("over the relayed WebSocket: %q, %v", data, err)
	}
	if got := shown.Load(); got != nil {
		t.Errorf("the rig was shown the browser's own headers: %v", got)
	}
}

// An account with an expiry works until then, no hold or grant outlasts
// it, and at that time it stops as if locked: sign-in, sessions, tokens
// and the device it holds. An admin can move the expiry or clear it.
func TestUserExpiry(t *testing.T) {
	f := newFullStation(t)
	by := map[string]caller{}
	for _, c := range f.callers() {
		by[c.name] = c
	}
	drive := f.users["drive"]
	tokens := []string{"drive user's watch token", "drive user's drive token", "drive user's admin token"}

	// Set through the API, a few minutes ahead.
	at := time.Now().Add(3 * time.Minute).Truncate(time.Second).UTC()
	w := f.do(by["admin"], "PUT", "/v1/users/"+drive.ID, `{"expires":"`+at.Format(time.RFC3339)+`"}`, true)
	var put struct{ Expires time.Time }
	if json.Unmarshal(w.Body.Bytes(), &put); w.Code != http.StatusOK || !put.Expires.Equal(at) {
		t.Fatalf("setting it: %d %s", w.Code, w.Body)
	}
	if w := f.do(by["admin"], "PUT", "/v1/users/"+drive.ID, `{"expires":"next tuesday"}`, true); w.Code != http.StatusBadRequest {
		t.Errorf("a bad expiry: %d", w.Code)
	}
	// Until then it works, and a hold it takes ends by the expiry.
	w = f.do(by["drive user"], "POST", "/v1/devices/"+devA+"/hold", `{"scopes":["view","input"]}`, true)
	if w.Code != http.StatusOK {
		t.Fatalf("a hold before the expiry: %d %s", w.Code, w.Body)
	}
	var got struct {
		Hold struct {
			Subject string
			Expires time.Time
		}
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Hold.Expires.Equal(at) {
		t.Errorf("the grant ends %s, want the expiry %s", got.Hold.Expires, at)
	}
	for _, who := range tokens {
		if w := f.do(by[who], "GET", "/v1/devices", "", true); w.Code != http.StatusOK {
			t.Errorf("%s before the expiry: %d", who, w.Code)
		}
	}
	if page := f.do(by["drive user"], "GET", "/account", "", true).Body.String(); !strings.Contains(page, "<th>Expires</th>") {
		t.Error("the Account page does not say when the account expires")
	}
	page := f.do(by["admin"], "GET", "/settings/access", "", true).Body.String()
	if !strings.Contains(page, `data-utc="`+at.Format(time.RFC3339)+`"`) || !strings.Contains(page, "<th>Expires</th>") {
		t.Error("Settings does not show the expiry")
	}

	// Moved into the past from the page: what it has open ends at once.
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if w := f.do(by["admin"], "POST", "/settings/users/expires", "user="+drive.ID+"&expires_utc="+url.QueryEscape(past), true); w.Code != http.StatusSeeOther {
		t.Fatalf("moving it: %d %s", w.Code, w.Body)
	}
	held, _ := f.db.Held(f.ctx)
	for _, h := range held {
		if h.Subject == got.Hold.Subject {
			t.Errorf("the expired user still holds %s", h.DeviceID)
		}
	}
	for _, who := range append([]string{"drive user"}, tokens...) {
		if w := f.do(by[who], "GET", "/v1/devices", "", true); w.Code != http.StatusUnauthorized {
			t.Errorf("%s after the expiry: %d", who, w.Code)
		}
	}
	if w := f.do(by["admin"], "GET", "/settings/access", "", true); !strings.Contains(w.Body.String(), `<td data-l="Sign-in">expired<span class="sub">`) {
		t.Error("Settings does not say the account expired")
	}
	// An admin cannot set one on their own account.
	if w := f.do(by["admin"], "POST", "/settings/users/expires", "user="+f.admin.User+"&expires_utc="+url.QueryEscape(at.Format(time.RFC3339)), true); !strings.Contains(w.Body.String(), errLockout.Error()) {
		t.Errorf("an expiry on one's own account: %d", w.Code)
	}
	// Clear: the account works again, through its tokens at once.
	if w := f.do(by["admin"], "POST", "/settings/users/expires", "user="+drive.ID+"&clear=1", true); w.Code != http.StatusSeeOther {
		t.Fatalf("clearing it: %d %s", w.Code, w.Body)
	}
	if w := f.do(by["drive user's drive token"], "GET", "/v1/devices", "", true); w.Code != http.StatusOK {
		t.Errorf("a token after Clear: %d", w.Code)
	}
	if u, _ := f.db.User(f.ctx, drive.ID); !u.ExpiresAt.IsZero() {
		t.Errorf("still expires %s", u.ExpiresAt)
	}
	// Added with an expiry from the page, in the station's own time when
	// the page's script did not run: a day alone means 12:01 AM.
	day := time.Now().Add(72 * time.Hour).Local()
	w = f.do(by["admin"], "POST", "/settings/users", "login=demo%40example.com&expires_date="+day.Format("2006-01-02")+"&expires_time=", true)
	demo, err := f.db.UserByLogin(f.ctx, "demo@example.com")
	want := time.Date(day.Year(), day.Month(), day.Day(), 0, 1, 0, 0, time.Local)
	if err != nil || !demo.ExpiresAt.Equal(want) {
		t.Errorf("a user added with an expiry: %s, want %s, %v (%d)", demo.ExpiresAt, want, err, w.Code)
	}
	// Without a date, the account does not expire.
	f.do(by["admin"], "POST", "/settings/users", "login=lasting%40example.com&expires_date=&expires_time=00%3A01", true)
	if u, err := f.db.UserByLogin(f.ctx, "lasting@example.com"); err != nil || !u.ExpiresAt.IsZero() {
		t.Errorf("a user added with no date: %s %v", u.ExpiresAt, err)
	}
	page = f.do(by["admin"], "GET", "/settings/access", "", true).Body.String()
	if !strings.Contains(page, `name="expires_time" value="00:01"`) || !strings.Contains(page, `data-tz>`) || !strings.Contains(page, `data-clear-expiry`) {
		t.Error("the add form should offer a day, a time of 12:01 AM, the time zone and Clear")
	}
}

// A jam an expired user started loses its device with them, though its
// hold is in the jam's name.
func TestUserExpiryEndsTheirJamsHold(t *testing.T) {
	f := newFullStation(t)
	drive := f.users["drive"]
	jam := station.Jam{ID: station.NewID("jam"), Subject: drive.Login, DeviceID: devA, Prompt: "look", Status: "running", CreatedAt: time.Now()}
	if err := f.db.CreateJam(f.ctx, jam); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.holds.Take(f.ctx, hold.Request{DeviceID: devA, Subject: jamSubject(jam.ID), Scopes: []station.Scope{station.ScopeBidi}}); err != nil {
		t.Fatal(err)
	}
	other := station.Jam{ID: station.NewID("jam"), Subject: f.users["jam"].Login, DeviceID: devB, Prompt: "look", Status: "running", CreatedAt: time.Now()}
	f.db.CreateJam(f.ctx, other)
	if _, err := f.s.holds.Take(f.ctx, hold.Request{DeviceID: devB, Subject: jamSubject(other.ID), Scopes: []station.Scope{station.ScopeBidi}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.setExpiry(f.ctx, f.admin, drive.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	held, _ := f.db.Held(f.ctx)
	var left []string
	for _, h := range held {
		left = append(left, h.Subject)
	}
	if slices.Contains(left, jamSubject(jam.ID)) || !slices.Contains(left, jamSubject(other.ID)) {
		t.Errorf("holds left: %v; the expired user's jam should have lost its device, the other jam not", left)
	}
}

// Everyone picks their own theme on their Account page; it is kept in
// their browser, so Settings, which only admins open, has none.
func TestAppearanceOnAccount(t *testing.T) {
	f := newFullStation(t)
	for _, c := range f.callers() {
		switch c.name {
		case "watch user", "drive user", "admin":
			if page := f.do(c, "GET", "/account", "", true).Body.String(); !strings.Contains(page, `id="themePick"`) {
				t.Errorf("%s's Account page has no theme picker", c.name)
			}
		}
		if c.name == "admin" {
			if page := f.do(c, "GET", "/settings", "", true).Body.String(); strings.Contains(page, `id="themePick"`) {
				t.Error("Settings still has the theme picker")
			}
		}
	}
}
