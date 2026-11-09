package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/VibiumDev/station"
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
	if _, err := s.addUser(ctx, owner, "Ann Example", "Ann", "long enough", false); err == nil {
		t.Error("a login with a space passed")
	}
	if _, err := s.addUser(ctx, owner, "ann@example.com", "Ann", "short", false); err == nil {
		t.Error("a short password passed")
	}
	ann, err := s.addUser(ctx, owner, "Ann@Example.com", "Ann", "ann-secret", false)
	if err != nil || ann.Login != "ann@example.com" || ann.Admin {
		t.Fatalf("add: %+v %v", ann, err)
	}
	if _, err := s.addUser(ctx, owner, "ann@example.com", "", "another-one", false); !errors.Is(err, sqlite.ErrLoginTaken) {
		t.Errorf("duplicate: %v", err)
	}
	if hash, _ := s.db.PasswordHash(ctx, ann.ID); !checkPassword(hash, "ann-secret") {
		t.Error("the first password was not kept")
	}
	if err := s.setRole(ctx, owner, owner.User, false); !errors.Is(err, errLockout) {
		t.Errorf("demoting oneself: %v", err)
	}
	if err := s.setDisabled(ctx, owner, owner.User, true); !errors.Is(err, errLockout) {
		t.Errorf("disabling oneself: %v", err)
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
	if err := s.adminSetPassword(ctx, owner, ann.ID, "new-secret-1"); err != nil {
		t.Fatal(err)
	}
	if hash, _ := s.db.PasswordHash(ctx, ann.ID); !checkPassword(hash, "new-secret-1") {
		t.Error("the admin's password did not take")
	}
	if err := s.setDisabled(ctx, owner, ann.ID, true); err != nil {
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
	ann, _ := s.addUser(ctx, owner, "ann@example.com", "Ann", "ann-secret", false)
	bob, _ := s.addUser(ctx, owner, "bob@example.com", "Bob", "bob-secret", false)
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
		`<b>owner</b> <span class="muted">you</span>`, `<b>ann@example.com</b>`, "Make admin", "Hold to remove",
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
