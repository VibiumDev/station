package main

import (
	"encoding/json"
	"net/http"
	"testing"

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
