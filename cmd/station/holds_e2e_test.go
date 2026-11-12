package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station"
)

// holder is who holds device A now, and with what, as the device list says.
func (f *fullStation) holder(c caller, dev string) (subject string, scopes []string) {
	f.t.Helper()
	w := f.do(c, "GET", "/v1/devices", "", true)
	var devs []struct {
		ID   string `json:"id"`
		Hold *struct {
			Subject string   `json:"subject"`
			Scopes  []string `json:"scopes"`
		} `json:"hold"`
	}
	json.Unmarshal(w.Body.Bytes(), &devs)
	for _, d := range devs {
		if d.ID == dev && d.Hold != nil {
			return d.Hold.Subject, d.Hold.Scopes
		}
	}
	return "", nil
}

// Phase 3: holds by level. A watcher's hold is view-only and gives way;
// regular users recall one another and agents, never an admin; an admin
// recalls anyone; release is one's own; tokens keep to their level and
// their device.
func TestHoldsByLevel(t *testing.T) {
	f := newFullStation(t)
	by := map[string]caller{}
	for _, c := range f.callers() {
		by[c.name] = c
	}
	control := `{"scopes":["view","input"]}`
	take := func(who, dev, body string) int {
		return f.do(by[who], "POST", "/v1/devices/"+dev+"/hold", body, true).Code
	}
	release := func(who, dev string) int {
		return f.do(by[who], "DELETE", "/v1/devices/"+dev+"/hold", "", true).Code
	}
	// A refused taker is left in line for the device; leaving keeps the
	// line from handing it to them later in this story.
	leave := func(who string) { f.do(by[who], "DELETE", "/v1/devices/"+devA+"/hold", "", true) }
	// A watcher asking for nothing in particular gets a view-only hold
	// on a free device, and control is refused them.
	if code := take("watch user", devA, ""); code != 200 {
		t.Fatalf("watcher's view hold: %d", code)
	}
	if subj, scopes := f.holder(by["admin"], devA); subj != "watch@example.com" || len(scopes) != 1 || scopes[0] != "view" {
		t.Fatalf("after the watcher: %s %v", subj, scopes)
	}
	if code := take("watch user", devA, control); code != http.StatusForbidden {
		t.Errorf("a watcher taking control: %d", code)
	}
	// A driver takes the device from the watcher at once.
	if code := take("drive user", devA, control); code != 200 {
		t.Fatalf("driver takes from the watcher: %d", code)
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "drive@example.com" {
		t.Fatalf("after the driver: %s", subj)
	}
	// The device list tells each caller which hold is their own, so a
	// client that came in by /session can find the device it was given.
	mine := func(who string) bool {
		var devs []struct {
			ID   string `json:"id"`
			Hold *struct {
				Mine bool `json:"mine"`
			} `json:"hold"`
		}
		json.Unmarshal(f.do(by[who], "GET", "/v1/devices", "", true).Body.Bytes(), &devs)
		for _, d := range devs {
			if d.ID == devA && d.Hold != nil {
				return d.Hold.Mine
			}
		}
		return false
	}
	if !mine("drive user") || mine("admin") {
		t.Errorf("mine: the driver %v, the admin %v; only the holder's list should say so", mine("drive user"), mine("admin"))
	}
	// Another regular user recalls them; a token waits in line instead.
	if code := take("jam user", devA, control); code != 200 {
		t.Errorf("a regular user recalling another: %d", code)
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "jam@example.com" {
		t.Fatalf("after the recall: %s", subj)
	}
	if code := take("drive user's drive token", devA, control); code == 200 {
		t.Error("a token recalled a person")
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "jam@example.com" {
		t.Errorf("a token's try moved the hold to %s", subj)
	}
	leave("drive user's drive token")
	// The admin recalls anyone; nobody regular recalls the admin.
	if code := take("admin", devA, control); code != 200 {
		t.Fatalf("admin recalls: %d", code)
	}
	if code := take("jam user", devA, control); code == 200 {
		t.Error("a regular user recalled the admin")
	}
	if code := take("drive user", devA, control); code == 200 {
		t.Error("a driver recalled the admin")
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "owner" {
		t.Fatalf("the admin's hold went to %s", subj)
	}
	// Release is one's own: a would-be taker in line leaves the line
	// instead, and the hold stays where it was.
	leave("jam user")
	leave("drive user")
	if code := release("jam user", devA); code != http.StatusForbidden {
		t.Errorf("releasing another's hold: %d", code)
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "owner" {
		t.Fatalf("a release by another moved the hold to %s", subj)
	}
	if code := release("admin", devA); code != 200 {
		t.Errorf("admin releasing their own: %d", code)
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "" {
		t.Fatalf("after release: held by %s", subj)
	}
	// Tokens: a watch token never controls; a drive token takes a free
	// device; a device-bound token stays on its device.
	if code := take("admin's watch token", devA, control); code != http.StatusForbidden {
		t.Errorf("an admin's watch token taking control: %d", code)
	}
	if code := take("admin's watch token", devA, ""); code != 200 {
		t.Errorf("an admin's watch token watching: %d", code)
	}
	if code := take("drive user's drive token", devA, control); code != 200 {
		t.Errorf("a drive token taking from a watcher: %d", code)
	}
	if subj, _ := f.holder(by["admin"], devA); subj != "token:dd" {
		t.Fatalf("after the drive token: %s", subj)
	}
	if code := take("jam's device token", devB, control); code != http.StatusForbidden {
		t.Errorf("a bound token off its device: %d", code)
	}
	if code := take("jam's device token", devA, control); code == 200 {
		t.Error("a bound token recalled a person's hold")
	}
	leave("jam's device token")
	release("drive user's drive token", devA)
	if code := take("jam's device token", devA, control); code != 200 {
		t.Errorf("a bound token on its free device: %d", code)
	}
	// A regular user recalls an agent, which is what recall is for.
	if code := take("drive user", devA, control); code != 200 {
		t.Errorf("a driver recalling an agent: %d", code)
	}
	// An admin's token holds; a regular user may not recall it either.
	release("drive user", devA)
	if code := take("admin's drive token", devA, control); code != 200 {
		t.Fatalf("admin's drive token takes: %d", code)
	}
	if code := take("jam user", devA, control); code == 200 {
		t.Error("a regular user recalled an admin's token")
	}
	leave("jam user")
	// Device B stays the admins': a driver may not even watch it.
	if code := take("drive user", devB, ""); code != http.StatusForbidden {
		t.Errorf("a driver watching B: %d", code)
	}
	if code := take("admin", devB, control); code != 200 {
		t.Errorf("admin on B: %d", code)
	}
	_ = station.LevelWatch
}

// A jam's token is bound to its device everywhere, not on the device
// routes alone. An admin's sees no jam of another device, lists no
// records by key, files nothing under another device's hold, and does
// not change its user's records of another device. On its own device
// it does what it did.
func TestBoundTokenKeepsToItsDevice(t *testing.T) {
	f := newFullStation(t)
	now := time.Now()
	secret, err := f.db.CreateScopedToken(f.ctx, "jam:"+jamTest, station.TokenDrive, devA, now.Add(time.Hour), f.users["admin"].ID)
	if err != nil {
		t.Fatal(err)
	}
	bound := caller{name: "the admin's jam token on A", decor: withBearer(secret)}
	if err := f.db.CreateJam(f.ctx, station.Jam{ID: "jam_onb", Subject: "owner", DeviceID: devB, Model: "test", Prompt: "read what B shows", Status: "done", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Create(f.ctx, station.Hold{ID: "hld_b", DeviceID: devB, RigID: "rig_b", Subject: "owner", Scopes: []station.Scope{station.ScopeView, station.ScopeInput},
		CreatedAt: now.Add(-time.Hour), ActiveAt: now.Add(-time.Hour), ExpiresAt: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	onB := f.uploadAs(f.admin, []byte("PK the admin's own, on device B"), "b.zip", "hld_b")
	other := strings.Repeat("ab", 32)
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		// Jams: its own device's, and nothing of B's.
		{"GET", "/v1/jams/" + jamTest, "", 200},
		{"GET", "/v1/jams/jam_onb", "", 404},
		{"GET", "/v1/jams/jam_onb/events", "", 404},
		{"GET", "/jams/jam_onb", "", 404},
		{"GET", "/jams/jam_onb/shots/1", "", 404},
		// Records: none listed by key, none filed under B's hold, and
		// its user's own record of B neither shared nor deleted.
		{"GET", "/list/" + f.s.key.Public().Hex(), "", 401},
		{"POST", "/v1/records/upload-auth", `{"sha256":"` + other + `","size":1,"hold":"hld_b"}`, 403},
		{"POST", "/v1/records/upload-auth", `{"sha256":"` + other + `","size":1,"hold":"hld_a"}`, 200},
		{"POST", "/records/public", "sha256=" + onB + "&public=1", 403},
		{"POST", "/v1/records/" + onB + "/golden", `{"golden":true}`, 403},
		{"DELETE", "/" + onB, "", 403},
		{"POST", "/records/golden", "sha256=" + f.sha + "&make=golden", 303},
	} {
		if w := f.do(bound, c.method, c.path, c.body, false); w.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, w.Code, c.want)
		}
	}
	w := f.do(bound, "GET", "/v1/jams", "", false)
	if !strings.Contains(w.Body.String(), jamTest) || strings.Contains(w.Body.String(), "jam_onb") {
		t.Errorf("the jams it lists: %s", w.Body)
	}
	if b, err := f.db.Blob(f.ctx, onB); err != nil || b.Public || b.Golden {
		t.Errorf("the record on B: %+v, %v", b, err)
	}
}
