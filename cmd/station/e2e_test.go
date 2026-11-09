package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/jam"
	"github.com/VibiumDev/station/records"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/signal"
	"github.com/VibiumDev/station/sqlite"
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
	ruleAdminRead = func(c caller) bool { return c.signed && c.admin }
	// The admin's jam on A: its starter, admins and jam-level people act
	// on it, never a watch token.
	ruleJamAct = func(c caller) bool { return ruleBidiA(c) }
	// The admin's recording: changes are the uploader's and admins' with
	// admin power, so an admin's watch or drive token has no say.
	ruleOwnRecord = ruleManage
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
		{"POST", "/account/tokens/revoke", "/account/tokens/revoke", "name=none", rulePerson, false, false},
		{"GET", "/settings", "/settings", "", ruleManage, false, false},
		{"POST", "/settings/name", "/settings/name", "name=x", ruleManage, false, false},
		{"POST", "/settings/tokens", "/settings/tokens", "name=t&level=watch", ruleManage, false, false},
		{"POST", "/settings/tokens/revoke", "/settings/tokens/revoke", "name=none", ruleManage, false, false},
		{"POST", "/settings/codes", "/settings/codes", "", ruleManage, false, false},
		{"POST", "/settings/rigs/remove", "/settings/rigs/remove", "rig=rig_none", ruleManage, false, false},
		{"POST", "/settings/models", "/settings/models", "", ruleManage, false, false},
		{"POST", "/settings/models/remove", "/settings/models/remove", "name=none", ruleManage, false, false},
		{"POST", "/settings/models/default", "/settings/models/default", "name=none", ruleManage, false, false},
		{"POST", "/settings/models/key", "/settings/models/key", "name=none&key=k", ruleManage, false, false},
		{"POST", "/settings/jams", "/settings/jams", "on=1", ruleManage, false, false},
		{"POST", "/settings/users", "/settings/users", "login=new@example.com", ruleManage, false, false},
		{"POST", "/settings/users/role", "/settings/users/role", "user=none&admin=1", ruleManage, false, false},
		{"POST", "/settings/users/lock", "/settings/users/lock", "user=none&locked=1", ruleManage, false, false},
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
