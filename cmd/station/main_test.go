package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/VibiumDev/station"
)

// typed makes what follows read from the terminal the lines given, as
// `station passwd` reads a password and its repeat.
func typed(t *testing.T, lines string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(lines); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

// `station passwd` sets a password and leaves the authenticator alone;
// with --reset it forgets the authenticator and the backup codes too, so
// an owner who lost both gets back in.
func TestPasswdReset(t *testing.T) {
	dir, ctx := t.TempDir(), context.Background()
	db, err := openDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ensureOwner(ctx, db, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSecondFactor(ctx, owner.ID, newTOTPSecret(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBackupCodes(ctx, owner.ID, []string{hashCode("abcde-fghjk")}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	state := func() (station.User, string, int) {
		t.Helper()
		db, err := openDB(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		u, err := db.UserByLogin(ctx, station.OwnerLogin)
		if err != nil {
			t.Fatal(err)
		}
		hash, _ := db.PasswordHash(ctx, u.ID)
		left, _ := db.BackupCodesLeft(ctx, u.ID)
		return u, hash, left
	}

	typed(t, "a-first-pass-phrase\na-first-pass-phrase\n")
	if code := runCommand("passwd", []string{"--data-dir", dir}); code != 0 {
		t.Fatalf("passwd: exit %d", code)
	}
	if u, hash, left := state(); !checkPassword(hash, "a-first-pass-phrase") || !u.TwoFactor || left != 1 {
		t.Fatalf("passwd alone: password set %v, authenticator %v, %d backup codes", checkPassword(hash, "a-first-pass-phrase"), u.TwoFactor, left)
	}

	typed(t, "a-second-pass-phrase\na-second-pass-phrase\n")
	if code := runCommand("passwd", []string{"--reset", "owner", "--data-dir", dir}); code != 0 {
		t.Fatalf("passwd --reset: exit %d", code)
	}
	if u, hash, left := state(); !checkPassword(hash, "a-second-pass-phrase") || u.TwoFactor || left != 0 {
		t.Errorf("passwd --reset: password set %v, authenticator %v, %d backup codes", checkPassword(hash, "a-second-pass-phrase"), u.TwoFactor, left)
	}
}
