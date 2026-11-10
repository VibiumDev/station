package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/jam"
)

// A jam's result is its first prompt's. A later result of the same
// kind replaces it; one of the other kind stays in the transcript.
func TestTakeResultKeepsTheJamsKind(t *testing.T) {
	var j station.Jam
	takeResult(&j, nil)
	if j.Took != "" {
		t.Errorf("nil result set %+v", j)
	}
	takeResult(&j, &jam.Verdict{Took: "check", Status: "passed", Summary: "Checkout works."})
	takeResult(&j, &jam.Verdict{Took: "run", Status: "completed", Summary: "Session ended."})
	if j.Took != "check" || j.Result != "passed" || j.Summary != "Checkout works." {
		t.Errorf("an errand after a check took over: %+v", j)
	}
	takeResult(&j, &jam.Verdict{Took: "check", Status: "failed", Summary: "The coupon field is gone."})
	if j.Result != "failed" || j.Summary != "The coupon field is gone." {
		t.Errorf("a second check did not replace the first: %+v", j)
	}
}

// The viewer's key syntax becomes Vibium's: GUI is Meta, Ctrl is Control.
func TestJamShortcutsSpeakVibium(t *testing.T) {
	got := jamShortcuts([]station.Shortcut{{Label: "Home", Keys: "GUI+h"}, {Label: "Back", Keys: "Escape"}, {Label: "Lock", Keys: "Ctrl+GUI+q"}, {Label: "Recents", Keys: "Alt+Tab"}})
	want := []jam.Shortcut{{Label: "Home", Keys: "Meta+h"}, {Label: "Back", Keys: "Escape"}, {Label: "Lock", Keys: "Control+Meta+q"}, {Label: "Recents", Keys: "Alt+Tab"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %v, want %v", i, got[i], want[i])
		}
	}
	if jamShortcuts(nil) != nil {
		t.Error("no shortcuts should stay none")
	}
}

// The instruction's sentence about the device, from what the rig said.
func TestDeviceAbout(t *testing.T) {
	cases := []struct {
		d    deviceView
		want string
	}{
		{deviceView{}, ""},
		{deviceView{Model: "iPhone 15 Pro Max", Platform: "ios", OSVersion: "26.6"}, "an iPhone 15 Pro Max on iOS 26.6"},
		{deviceView{Model: "Pixel 8a", Platform: "android"}, "a Pixel 8a on Android"},
		{deviceView{Platform: "android", OSVersion: "15"}, "running Android 15"},
		{deviceView{Model: "Galaxy S24"}, "a Galaxy S24"},
	}
	for _, c := range cases {
		if got := deviceAbout(c.d); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.d, got, c.want)
		}
	}
}

// The Jam video switch on Settings decides what a jam asks of Vibium's
// recording. Video is on until it is turned off, and the choice is kept.
func TestJamVideoSwitch(t *testing.T) {
	s, owner := teamsServer(t)
	s.dir, s.cfg = t.TempDir(), &Config{Name: "station"}
	s.jams = &jams{s: s}
	set := func(on string) {
		t.Helper()
		r := httptest.NewRequest("POST", "/settings/jam-video", strings.NewReader("on="+on))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.setJamVideo(w, r, owner)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("on=%s: %d %s", on, w.Code, w.Body)
		}
	}
	row := func() string {
		t.Helper()
		data := map[string]any{"Page": "settings", "Principal": owner, "CSRF": "c", "Name": "Station", "Named": true, "JamsPossible": true, "JamVideo": s.jams.video()}
		var out strings.Builder
		if err := pages.ExecuteTemplate(&out, "settings", data); err != nil {
			t.Fatal(err)
		}
		_, rest, _ := strings.Cut(out.String(), `action="/settings/jam-video"`)
		rest, _, _ = strings.Cut(rest, "</form>")
		return rest
	}

	// Left alone, Vibium films the device whenever it can: nothing is said.
	if args := jamRecordArgs("jam_1", "/r/jam_1.zip", s.jams.video()); len(args) != 2 || args["name"] != "jam_1" || args["path"] != "/r/jam_1.zip" {
		t.Errorf("by default a jam asks for %v", args)
	}
	if html := row(); !strings.Contains(html, `name="on" value="0">on <button`) {
		t.Errorf("the row when on: %s", html)
	}

	set("0")
	if args := jamRecordArgs("jam_1", "/r/jam_1.zip", s.jams.video()); args["video"] != false {
		t.Errorf("turned off, a jam asks for %v", args)
	}
	if html := row(); !strings.Contains(html, `name="on" value="1">off <button`) {
		t.Errorf("the row when off: %s", html)
	}
	if saved, err := loadConfig(s.dir); err != nil || !saved.JamVideoOff {
		t.Errorf("the choice was not saved: %+v, %v", saved, err)
	}

	set("1")
	if saved, err := loadConfig(s.dir); err != nil || saved.JamVideoOff || !s.jams.video() {
		t.Errorf("turned back on: %+v, %v", saved, err)
	}
}
