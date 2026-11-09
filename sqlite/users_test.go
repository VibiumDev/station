package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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
	if _, err := d.CreateToken(ctx, "anns", station.TokenDrive, "usr_ann"); err != nil {
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

// The second factor: the authenticator secret and the steps it was used
// at, backup codes spent once, setup links that open once and expire,
// and the reset that forgets it all.
func TestSecondFactorAndInvites(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	ann := station.User{ID: "usr_ann", Login: "ann@example.com"}
	if err := d.PutUser(ctx, ann); err != nil {
		t.Fatal(err)
	}
	if u, _ := d.User(ctx, ann.ID); u.HasPassword || u.TwoFactor {
		t.Errorf("fresh user: %+v", u)
	}
	now := time.Now()
	if err := d.SetSecondFactor(ctx, ann.ID, "SECRET", now); err != nil {
		t.Fatal(err)
	}
	if secret, step, err := d.SecondFactor(ctx, ann.ID); err != nil || secret != "SECRET" || step != 0 {
		t.Fatalf("factor: %q %d %v", secret, step, err)
	}
	if u, _ := d.User(ctx, ann.ID); !u.TwoFactor || !u.TwoFactorAt.Equal(now) {
		t.Errorf("user: %+v", u)
	}
	for _, c := range []struct {
		step int64
		ok   bool
	}{{100, true}, {100, false}, {99, false}, {101, true}} {
		if ok, _ := d.AcceptStep(ctx, ann.ID, c.step); ok != c.ok {
			t.Errorf("step %d: %v", c.step, ok)
		}
	}
	if _, _, err := d.SecondFactor(ctx, "usr_nobody"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("nobody's factor: %v", err)
	}
	if err := d.SetBackupCodes(ctx, ann.ID, []string{"h1", "h2", "h3"}); err != nil {
		t.Fatal(err)
	}
	if left, ok, _ := d.UseBackupCode(ctx, ann.ID, "h2"); !ok || left != 2 {
		t.Errorf("use: %v %d", ok, left)
	}
	if left, ok, _ := d.UseBackupCode(ctx, ann.ID, "h2"); ok || left != 2 {
		t.Errorf("reuse: %v %d", ok, left)
	}
	if _, ok, _ := d.UseBackupCode(ctx, ann.ID, "nope"); ok {
		t.Error("a made-up code")
	}
	if err := d.SetBackupCodes(ctx, ann.ID, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	if left, _ := d.BackupCodesLeft(ctx, ann.ID); left != 1 {
		t.Errorf("after replacing: %d", left)
	}
	// Setup links: one per user, found while open, spent once.
	inv := station.Invite{UserID: ann.ID, ExpiresAt: now.Add(time.Hour), By: "owner"}
	if err := d.PutInvite(ctx, "link1", inv); err != nil {
		t.Fatal(err)
	}
	if err := d.PutInvite(ctx, "link2", inv); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Invite(ctx, "link1", now); !errors.Is(err, station.ErrNotFound) {
		t.Error("the older link still opens")
	}
	got, err := d.Invite(ctx, "link2", now)
	if err != nil || got.UserID != ann.ID || got.By != "owner" || !got.ExpiresAt.Equal(inv.ExpiresAt) {
		t.Fatalf("invite: %+v %v", got, err)
	}
	if _, err := d.Invite(ctx, "link2", now.Add(2*time.Hour)); !errors.Is(err, station.ErrNotFound) {
		t.Error("an expired link opens")
	}
	if open, _ := d.OpenInvites(ctx, now); !open[ann.ID].Equal(inv.ExpiresAt) || len(open) != 1 {
		t.Errorf("open: %v", open)
	}
	if err := d.PutInvite(ctx, "link3", station.Invite{UserID: "usr_nobody", ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("a link for nobody: %v", err)
	}
	if err := d.UseInvite(ctx, "link2"); err != nil {
		t.Fatal(err)
	}
	if err := d.UseInvite(ctx, "link2"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("spent twice: %v", err)
	}
	// Reset forgets password, factor and codes; removal takes the rest.
	d.SetPassword(ctx, ann.ID, "hash")
	if err := d.ResetSignIn(ctx, ann.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := d.User(ctx, ann.ID); u.HasPassword || u.TwoFactor {
		t.Errorf("after reset: %+v", u)
	}
	if left, _ := d.BackupCodesLeft(ctx, ann.ID); left != 0 {
		t.Errorf("codes after reset: %d", left)
	}
	if err := d.ResetSignIn(ctx, "usr_nobody"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("resetting nobody: %v", err)
	}
	d.PutInvite(ctx, "link4", inv)
	d.SetBackupCodes(ctx, ann.ID, []string{"z"})
	if err := d.RemoveUser(ctx, ann.ID); err != nil {
		t.Fatal(err)
	}
	if open, _ := d.OpenInvites(ctx, now); len(open) != 0 {
		t.Error("a removed user's link stays")
	}
	var n int
	d.db.QueryRow(`SELECT count(*) FROM backup_codes`).Scan(&n)
	if n != 0 {
		t.Error("a removed user's codes stay")
	}
}

// A database from before the second factor had users.disabled; it is
// users.locked now, with the same rows.
func TestDisabledBecomesLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "station.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.PutUser(ctx, station.User{ID: "usr_a", Login: "a@example.com", Locked: true}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE users DROP COLUMN totp_secret`, `ALTER TABLE users DROP COLUMN totp_at`, `ALTER TABLE users DROP COLUMN totp_step`,
		`ALTER TABLE users RENAME COLUMN locked TO disabled`,
	} {
		if _, err := d.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u, err := d.User(ctx, "usr_a")
	if err != nil || !u.Locked {
		t.Fatalf("after the migration: %+v %v", u, err)
	}
	if has, _ := d.hasColumn("users", "disabled"); has {
		t.Error("users.disabled is still there")
	}
}
