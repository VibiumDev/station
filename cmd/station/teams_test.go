package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/sqlite"
)

func teamsServer(t *testing.T) (*server, station.Principal) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	ctx := context.Background()
	if _, err := db.SeedOwner(ctx, "hash"); err != nil {
		t.Fatal(err)
	}
	owner, _ := db.UserByLogin(ctx, station.OwnerLogin)
	s := &server{db: db, auth: newAuth(&Config{}, db, nil, false, false)}
	return s, principalOf(owner, "")
}

// The operations behind the Teams section: a user with a first
// password, roles, disabling, removal, teams, members and access, and
// the guard against an admin locking themselves out.
func TestTeamsOperations(t *testing.T) {
	s, owner := teamsServer(t)
	ctx := context.Background()
	if _, _, err := s.addUser(ctx, owner, "Ann Example", "Ann", false); err == nil {
		t.Error("a login with a space passed")
	}
	ann, link, err := s.addUser(ctx, owner, "Ann@Example.com", "Ann", false)
	if err != nil || ann.Login != "ann@example.com" || ann.Admin || !strings.HasPrefix(link, "/setup/") {
		t.Fatalf("add: %+v %q %v", ann, link, err)
	}
	if _, _, err := s.addUser(ctx, owner, "ann@example.com", "", false); !errors.Is(err, sqlite.ErrLoginTaken) {
		t.Errorf("duplicate: %v", err)
	}
	if u, _ := s.db.User(ctx, ann.ID); u.HasPassword || u.TwoFactor {
		t.Error("a new user has no password until their setup link is used")
	}
	if open, _ := s.db.OpenInvites(ctx, time.Now()); open[ann.ID].IsZero() {
		t.Error("the setup link is not open")
	}
	if err := s.setRole(ctx, owner, owner.User, false); !errors.Is(err, errLockout) {
		t.Errorf("demoting oneself: %v", err)
	}
	if err := s.setLocked(ctx, owner, owner.User, true); !errors.Is(err, errLockout) {
		t.Errorf("locking oneself: %v", err)
	}
	if err := s.removeUser(ctx, owner, owner.User); !errors.Is(err, errLockout) {
		t.Errorf("removing oneself: %v", err)
	}
	if err := s.setRole(ctx, owner, ann.ID, true); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.db.User(ctx, ann.ID); !u.Admin {
		t.Error("ann should be an admin now")
	}
	s.setRole(ctx, owner, ann.ID, false)
	team, err := s.addTeam(ctx, owner, " QA ")
	if err != nil || team.Name != "QA" {
		t.Fatalf("team: %+v %v", team, err)
	}
	if err := s.setMember(ctx, owner, team.ID, ann.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.setAccess(ctx, owner, "rig_a/phone", team.ID, "boss"); err == nil {
		t.Error("a made-up level passed")
	}
	if err := s.setAccess(ctx, owner, "rig_a/phone", team.ID, "drive"); err != nil {
		t.Fatal(err)
	}
	annP, _ := s.db.User(ctx, ann.ID)
	if l := s.auth.level(ctx, principalOf(annP, ""), "rig_a/phone"); l != station.LevelDrive {
		t.Errorf("ann's level: %q", l)
	}
	// A reset forgets the password and the authenticator and makes a new link.
	s.db.SetPassword(ctx, ann.ID, "hash")
	s.db.SetSecondFactor(ctx, ann.ID, newTOTPSecret(), time.Now())
	s.db.SetBackupCodes(ctx, ann.ID, []string{"h1", "h2"})
	link2, err := s.resetSignIn(ctx, owner, ann.ID)
	if err != nil || !strings.HasPrefix(link2, "/setup/") || link2 == link {
		t.Fatalf("reset: %q %v", link2, err)
	}
	if u, _ := s.db.User(ctx, ann.ID); u.HasPassword || u.TwoFactor {
		t.Error("the reset left the password or the authenticator")
	}
	if left, _ := s.db.BackupCodesLeft(ctx, ann.ID); left != 0 {
		t.Error("the reset left backup codes")
	}
	if _, err := s.db.Invite(ctx, hashToken(strings.TrimPrefix(link, "/setup/")), time.Now()); err == nil {
		t.Error("the older link still opens")
	}
	if err := s.setLocked(ctx, owner, ann.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.removeTeam(ctx, owner, team.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.removeUser(ctx, owner, ann.ID); err != nil {
		t.Fatal(err)
	}
	if users, _ := s.db.Users(ctx); len(users) != 1 {
		t.Errorf("users left: %+v", users)
	}
}

// The Settings page renders its Teams section: users with their guards,
// teams with members and a way in, and the access grid.
func TestSettingsTeamsRender(t *testing.T) {
	s, owner := teamsServer(t)
	ctx := context.Background()
	ann, _, _ := s.addUser(ctx, owner, "ann@example.com", "Ann", false)
	bob, _, _ := s.addUser(ctx, owner, "bob@example.com", "Bob", false)
	team, _ := s.addTeam(ctx, owner, "QA")
	s.setMember(ctx, owner, team.ID, ann.ID, true)
	_ = bob
	data := map[string]any{"Page": "settings", "Principal": owner, "CSRF": "c", "Name": "Station", "Named": true}
	s.teamsData(ctx, owner, data)
	var out strings.Builder
	if err := pages.ExecuteTemplate(&out, "settings", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`<b>owner</b></td>`, `admin <span class="muted">(you)</span>`, `<b>ann@example.com</b>`, "Make admin", "Hold to remove", ">Lock<", "Reset sign-in", "setup link until",
		`<option value="` + bob.ID + `">bob@example.com</option>`, // bob can still join QA
		`<span class="member">ann@example.com`, "No devices yet.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("settings lacks %q", want)
		}
	}
	if strings.Contains(html, `name="user" value="`+owner.User+`"><input type="hidden" name="admin"`) {
		t.Error("the admin looking at the page should have no role button of their own")
	}
	// A device known to the station gets a row with a select per team.
	devs := []deviceView{{ID: "rig_a/iphone_1", Title: "iPhone", Name: "iphone_1"}}
	data["AccessRows"] = []accessRow{{Device: devs[0], Cells: []accessCell{{Team: team, Level: station.LevelWatch}}}}
	out.Reset()
	if err := pages.ExecuteTemplate(&out, "settings", data); err != nil {
		t.Fatal(err)
	}
	if html := out.String(); !strings.Contains(html, `<option value="watch" selected>watch</option>`) || !strings.Contains(html, `name="device" value="rig_a/iphone_1"`) {
		t.Error("the access grid lacks the device's select")
	}
}

// Settings lists every open enrollment code with its command. A new one
// hides none of the others, Cancel ends one at once, and a code this run
// did not make is listed without its text.
func TestEnrollmentCodesStayListedAndCancel(t *testing.T) {
	f := newFullStation(t)
	ck, csrf := f.session(f.users["admin"].ID, stageSignedIn)
	admin := caller{name: "admin", decor: withCookie(ck), csrf: csrf, signed: true, admin: true, person: true}
	codeIn := regexp.MustCompile(`roadie enroll \S* ?--code ([0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4})`)
	idIn := regexp.MustCompile(`name="id" value="(\d+)"`)
	create := func() {
		t.Helper()
		if w := f.do(admin, "POST", "/settings/codes", "", true); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings/rigs#enroll" {
			t.Fatalf("a new code: %d %q", w.Code, w.Header().Get("Location"))
		}
	}
	listed := func() (codes, ids []string, page string) {
		page = f.do(admin, "GET", "/settings/rigs", "", true).Body.String()
		for _, m := range codeIn.FindAllStringSubmatch(page, -1) {
			codes = append(codes, m[1])
		}
		for _, m := range idIn.FindAllStringSubmatch(page, -1) {
			ids = append(ids, m[1])
		}
		return
	}
	use := func(code string) error {
		normalized, _, err := enroll.ParseCode(code)
		if err != nil {
			t.Fatal(err)
		}
		return f.db.UseCode(f.ctx, normalized, time.Now())
	}
	if codes, ids, _ := listed(); len(codes)+len(ids) != 0 {
		t.Fatalf("codes before any was made: %v %v", codes, ids)
	}
	create()
	first, _, _ := listed()
	if len(first) != 1 {
		t.Fatalf("after one: %v", first)
	}
	time.Sleep(2 * time.Millisecond) // the list is by when each was made
	create()
	both, ids, page := listed()
	// Cancel is held like the other buttons that take something away.
	if strings.Count(page, `<button class="small hold" type="button" data-hold title="Hold to cancel">Cancel</button>`) != 2 {
		t.Error("Cancel is not a held button on each code")
	}
	if len(both) != 2 || len(ids) != 2 || both[1] != first[0] || both[0] == first[0] {
		t.Fatalf("after two, newest first: %v (ids %v), the first was %v", both, ids, first)
	}
	// Cancel the newest: it is gone from the page and no rig can use it.
	if w := f.do(admin, "POST", "/settings/codes/cancel", "id="+ids[0], true); w.Code != http.StatusSeeOther {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if left, _, _ := listed(); len(left) != 1 || left[0] != first[0] {
		t.Fatalf("after the cancel: %v", left)
	}
	if err := use(both[0]); !errors.Is(err, sqlite.ErrCode) {
		t.Errorf("the cancelled code: %v", err)
	}
	// Cancelling it again, or something that was never a code, changes nothing.
	for _, id := range []string{ids[0], "12", "x"} {
		if w := f.do(admin, "POST", "/settings/codes/cancel", "id="+id, true); w.Code != http.StatusSeeOther {
			t.Errorf("cancel %q: %d", id, w.Code)
		}
	}
	if err := use(first[0]); err != nil {
		t.Errorf("the other code after the cancel: %v", err)
	}
	if left, ids, _ := listed(); len(left)+len(ids) != 0 {
		t.Errorf("a used code is still listed: %v", left)
	}
	// A code made elsewhere, as `station enroll` makes one: listed, with
	// Cancel, without its text.
	if err := f.db.SaveCode(f.ctx, "WXYZ-2345-ABCD", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	codes, ids, page := listed()
	if len(codes) != 0 || len(ids) != 1 || !strings.Contains(page, "A code made at") || strings.Contains(page, "WXYZ-2345") {
		t.Errorf("a code this run did not make: codes %v ids %v", codes, ids)
	}
	// A code that has run out is not listed.
	f.db.SaveCode(f.ctx, "WXYZ-9999-ABCD", time.Now().Add(-time.Second))
	if _, ids, _ := listed(); len(ids) != 1 {
		t.Errorf("an expired code is listed: %v", ids)
	}
}
