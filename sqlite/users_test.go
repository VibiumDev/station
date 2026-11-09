package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/VibiumDev/station"
)

func TestUsersTeamsAndAccess(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	// The owner is seeded once from the old password hash, and never again.
	if did, err := d.SeedOwner(ctx, "hash1"); err != nil || !did {
		t.Fatalf("seed: %v %v", did, err)
	}
	if did, _ := d.SeedOwner(ctx, "hash2"); did {
		t.Error("seeded twice")
	}
	owner, hash, err := d.Credentials(ctx, "Owner")
	if err != nil || !owner.Admin || hash != "hash1" || owner.Login != "owner" {
		t.Fatalf("owner: %+v %q %v", owner, hash, err)
	}
	// Users: created, found by login whatever the case, logins unique.
	ann := station.User{ID: "usr_ann", Login: "Ann@Example.com", Name: "Ann"}
	if err := d.PutUser(ctx, ann); err != nil {
		t.Fatal(err)
	}
	if err := d.PutUser(ctx, station.User{ID: "usr_dup", Login: "ann@example.com"}); !errors.Is(err, ErrLoginTaken) {
		t.Errorf("duplicate login: %v", err)
	}
	got, err := d.UserByLogin(ctx, "ANN@example.com")
	if err != nil || got.ID != "usr_ann" || got.Admin || got.Name != "Ann" {
		t.Fatalf("by login: %+v %v", got, err)
	}
	if err := d.SetPassword(ctx, "usr_ann", "h"); err != nil {
		t.Fatal(err)
	}
	if h, _ := d.PasswordHash(ctx, "usr_ann"); h != "h" {
		t.Errorf("hash: %q", h)
	}
	// Teams and members; access by level; the highest level wins.
	if err := d.PutTeam(ctx, station.Team{ID: "team_qa", Name: "QA"}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutTeam(ctx, station.Team{ID: "team_dev", Name: "Dev"}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutTeam(ctx, station.Team{ID: "team_x", Name: "QA"}); !errors.Is(err, ErrTeamExists) {
		t.Errorf("duplicate team: %v", err)
	}
	if err := d.SetMember(ctx, "team_qa", "usr_ann", true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetMember(ctx, "team_dev", "usr_ann", true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetMember(ctx, "team_qa", "usr_nobody", true); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("member who is not a user: %v", err)
	}
	if err := d.SetAccess(ctx, station.Access{DeviceID: "rig_1/phone", TeamID: "team_qa", Level: station.LevelWatch}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAccess(ctx, station.Access{DeviceID: "rig_1/phone", TeamID: "team_dev", Level: station.LevelJam}); err != nil {
		t.Fatal(err)
	}
	ann, _ = d.User(ctx, "usr_ann")
	if len(ann.Teams) != 2 {
		t.Errorf("teams: %v", ann.Teams)
	}
	if l, _ := d.LevelFor(ctx, ann.Teams, "rig_1/phone"); l != station.LevelJam {
		t.Errorf("level: %q", l)
	}
	if l, _ := d.LevelFor(ctx, []string{"team_qa"}, "rig_1/phone"); l != station.LevelWatch {
		t.Errorf("qa level: %q", l)
	}
	if l, _ := d.LevelFor(ctx, ann.Teams, "rig_2/phone"); l != station.LevelNone {
		t.Errorf("unshared device: %q", l)
	}
	if l, _ := d.LevelFor(ctx, nil, "rig_1/phone"); l != station.LevelNone {
		t.Errorf("no teams: %q", l)
	}
	// Taking access away, leaving a team, removing a team and a user.
	if err := d.SetAccess(ctx, station.Access{DeviceID: "rig_1/phone", TeamID: "team_dev", Level: station.LevelNone}); err != nil {
		t.Fatal(err)
	}
	if l, _ := d.LevelFor(ctx, ann.Teams, "rig_1/phone"); l != station.LevelWatch {
		t.Errorf("after taking dev's access: %q", l)
	}
	teams, _ := d.Teams(ctx)
	if len(teams) != 2 || teams[0].Name != "Dev" || len(teams[1].Members) != 1 || teams[1].Members[0] != "usr_ann" {
		t.Errorf("teams: %+v", teams)
	}
	if err := d.RemoveTeam(ctx, "team_qa"); err != nil {
		t.Fatal(err)
	}
	if acc, _ := d.Accesses(ctx); len(acc) != 0 {
		t.Errorf("access outlived its team: %+v", acc)
	}
	if _, err := d.CreateToken(ctx, "anns", false, "usr_ann"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveUser(ctx, "usr_ann"); err != nil {
		t.Fatal(err)
	}
	if toks, _ := d.Tokens(ctx); len(toks) != 0 {
		t.Errorf("a removed user's token survived: %+v", toks)
	}
	if _, err := d.User(ctx, "usr_ann"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("removed user: %v", err)
	}
	users, _ := d.Users(ctx)
	if len(users) != 1 || users[0].Login != "owner" {
		t.Errorf("users left: %+v", users)
	}
	if l, err := station.ParseLevel("drive"); err != nil || l != station.LevelDrive || !l.AtLeast(station.LevelWatch) || l.AtLeast(station.LevelJam) {
		t.Errorf("levels: %q %v", l, err)
	}
	if _, err := station.ParseLevel("boss"); err == nil {
		t.Error("a made-up level parsed")
	}
}
