package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

func TestStopAndDriveSitByTheLiveView(t *testing.T) {
	_, owner := teamsServer(t)
	page := func(extra map[string]any) (head, side string) {
		t.Helper()
		data := map[string]any{"Page": "jams", "Principal": owner, "CSRF": "c", "Name": "Station", "Named": true,
			"Jam": station.Jam{ID: "jam_1", DeviceID: "rig_1/phone_1", Prompt: "check the clock", Status: "running"}}
		for k, v := range extra {
			data[k] = v
		}
		var out strings.Builder
		if err := pages.ExecuteTemplate(&out, "jam", data); err != nil {
			t.Fatal(err)
		}
		head, rest, _ := strings.Cut(out.String(), `class="jamgrid"`)
		if _, after, found := strings.Cut(rest, `<aside class="watchside">`); found {
			side, _, _ = strings.Cut(after, "</aside>")
		}
		if strings.Contains(rest, "promptbar") && strings.Contains(strings.SplitN(rest, "promptbar", 2)[1], "/stop") {
			t.Error("the prompt bar has a Stop")
		}
		return head, side
	}
	const stop, drive = `action="/jams/jam_1/stop"`, `href="/devices/rig_1/phone_1/drive"`

	// A running jam with its device in view: the two are on the live
	// view, where they stay on screen, and not in the header.
	head, side := page(map[string]any{"Open": true, "Watch": "/devices/rig_1/phone_1/view?minimal=1"})
	if !strings.Contains(side, stop) || !strings.Contains(side, drive) {
		t.Errorf("the live view's bar: %s", side)
	}
	if !strings.Contains(side, "Stop</button>") || !strings.Contains(side, "Drive</a>") {
		t.Errorf("the buttons have no words: %s", side)
	}
	if strings.Index(side, stop) > strings.Index(side, "<iframe") {
		t.Error("the bar is under the live view, where a short window cuts it off")
	}
	if strings.Contains(head, stop) || strings.Contains(head, drive) {
		t.Error("the header repeats Stop or Drive")
	}

	// Running, but the rig is away and there is no live view: the header.
	head, side = page(map[string]any{"Open": true})
	if side != "" || !strings.Contains(head, stop) || !strings.Contains(head, drive) {
		t.Errorf("without a live view: side %q", side)
	}

	// Ended: nothing to stop. Drive and Play are in the header.
	head, side = page(map[string]any{"Play": "/records/abc/play"})
	if side != "" || strings.Contains(head, stop) || !strings.Contains(head, drive) || !strings.Contains(head, `href="/records/abc/play"`) {
		t.Errorf("an ended jam's header: %s", head)
	}
}

// Jam notes: the station's set is a file an admin edits on Settings,
// each person has their own on Account, and a jam is given the station's
// and its starter's.
func TestJamNotes(t *testing.T) {
	s, owner := teamsServer(t)
	s.dir, s.cfg = t.TempDir(), &Config{Name: "station"}
	s.jams = &jams{s: s}
	ctx := context.Background()
	ann, _, err := s.addUser(ctx, owner, "ann@example.com", "Ann", false)
	if err != nil {
		t.Fatal(err)
	}
	annP := principalOf(ann, "")
	post := func(handler func(http.ResponseWriter, *http.Request, station.Principal), p station.Principal, notes string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"notes": {notes}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler(w, r, p)
		return w
	}

	// With none written, a jam is given none.
	if a, b := s.jamNotes(ctx, owner); a != "" || b != "" {
		t.Fatalf("notes from nowhere: %q %q", a, b)
	}

	if w := post(s.setJamNotes, owner, "  Leave the device on its home screen.\r\nReload a page before using its microphone.\n"); w.Code != http.StatusSeeOther {
		t.Fatalf("station notes: %d %s", w.Code, w.Body)
	}
	const stationText = "Leave the device on its home screen.\nReload a page before using its microphone."
	if file, err := os.ReadFile(filepath.Join(s.dir, "jam-notes.md")); err != nil || string(file) != stationText+"\n" {
		t.Errorf("the file holds %q, %v", file, err)
	}
	if w := post(s.setMyJamNotes, annP, "Say what you tapped."); w.Code != http.StatusSeeOther {
		t.Fatalf("Ann's notes: %d %s", w.Code, w.Body)
	}

	// Ann's jams carry both. The owner's carry the station's only. A
	// token of Ann's is Ann.
	if a, b := s.jamNotes(ctx, annP); a != stationText || b != "Say what you tapped." {
		t.Errorf("Ann's jam: %q %q", a, b)
	}
	if a, b := s.jamNotes(ctx, owner); a != stationText || b != "" {
		t.Errorf("the owner's jam: %q %q", a, b)
	}
	if _, b := s.jamNotes(ctx, principalOf(ann, "token:anns")); b != "Say what you tapped." {
		t.Errorf("a jam from Ann's token: %q", b)
	}

	// A file edited by hand applies to the next jam.
	os.WriteFile(filepath.Join(s.dir, "jam-notes.md"), []byte("Typed in an editor.\n"), 0o600)
	if a, _ := s.jamNotes(ctx, owner); a != "Typed in an editor." {
		t.Errorf("after an edit by hand: %q", a)
	}

	// Notes too long for a jam are refused, counted in characters.
	if _, err := cleanNotes(strings.Repeat("é", jam.MaxNotes+1)); err == nil || !strings.Contains(err.Error(), "the most a jam carries is 4000") {
		t.Errorf("too long: %v", err)
	}
	if w := post(s.setJamNotes, owner, strings.Repeat("é", jam.MaxNotes)); w.Code != http.StatusSeeOther {
		t.Errorf("notes of exactly the limit: %d", w.Code)
	}

	// Empty notes are no notes, and no file.
	post(s.setJamNotes, owner, " \n ")
	post(s.setMyJamNotes, annP, "")
	if a, b := s.jamNotes(ctx, annP); a != "" || b != "" {
		t.Errorf("cleared: %q %q", a, b)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "jam-notes.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file is still there: %v", err)
	}
}

func TestJamNotesOnThePages(t *testing.T) {
	_, owner := teamsServer(t)
	render := func(name string, data map[string]any) string {
		t.Helper()
		base := map[string]any{"Principal": owner, "CSRF": "c", "Name": "Station", "Named": true, "MaxNotes": jam.MaxNotes}
		for k, v := range data {
			base[k] = v
		}
		var out strings.Builder
		if err := pages.ExecuteTemplate(&out, name, base); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// Settings and Account each show their notes in a box that posts
	// to its own place, once jams are possible.
	settings := render("settings", map[string]any{"Page": "settings", "JamsPossible": true, "JamNotes": "Be <brief>."})
	if !strings.Contains(settings, `action="/settings/jam-notes"`) || !strings.Contains(settings, `maxlength="4000"`) || !strings.Contains(settings, "Be &lt;brief&gt;.</textarea>") {
		t.Error("Settings has no notes box holding the notes")
	}
	if strings.Contains(render("settings", map[string]any{"Page": "settings"}), "/settings/jam-notes") {
		t.Error("Settings offers notes with no jams")
	}
	account := render("account", map[string]any{"Page": "account", "User": station.User{Login: "ann@example.com"}, "JamsPossible": true, "JamNotes": "Mine."})
	if !strings.Contains(account, `action="/account/jam-notes"`) || !strings.Contains(account, "Mine.</textarea>") {
		t.Error("Account has no notes box holding the notes")
	}
	if strings.Contains(render("account", map[string]any{"Page": "account", "User": station.User{Login: "ann@example.com"}, "JamsPossible": true, "Token": true}), "/account/jam-notes") {
		t.Error("a token is offered its user's notes to change")
	}
	// The transcript shows what a jam was given, folded away.
	var row strings.Builder
	ev, _ := jam.NotesEvent("Station's.", "Ann's.")
	if err := pages.ExecuteTemplate(&row, "event", map[string]any{"Kind": ev.Kind, "Text": ev.Text, "When": "12:00:00"}); err != nil {
		t.Fatal(err)
	}
	if html := row.String(); !strings.Contains(html, "<summary>Notes given to the model</summary>") || !strings.Contains(html, "Station&#39;s.") || !strings.Contains(html, "Ann&#39;s.") {
		t.Errorf("the transcript row: %s", html)
	}
}
