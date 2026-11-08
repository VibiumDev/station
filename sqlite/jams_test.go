package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/VibiumDev/station"
)

func TestJams(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	j := station.Jam{ID: "jam_1", Subject: "owner", DeviceID: "rig_a/phone", Model: "gpt", Prompt: "Is it on the home screen?", Status: "running"}
	if err := d.CreateJam(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateJam(ctx, station.Jam{ID: "jam_2", Subject: "token:ci", DeviceID: "rig_a/phone", Model: "gpt", Prompt: "Open Settings", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	open, err := d.OpenJams(ctx)
	if err != nil || len(open) != 2 {
		t.Fatalf("open jams: %v, %v", open, err)
	}
	for i, kind := range []string{"turn", "call", "verdict"} {
		if err := d.AddJamEvent(ctx, station.JamEvent{JamID: "jam_1", Seq: i + 1, Kind: kind, Data: json.RawMessage(`{"n":` + string(rune('0'+i)) + `}`)}); err != nil {
			t.Fatal(err)
		}
	}
	j.Status, j.Took, j.Result, j.Summary, j.HoldID, j.Actions, j.TokensIn, j.EndedAt = "done", "check", "passed", "The home screen shows.", "hold_9", 3, 1200, time.Now()
	if err := d.UpdateJam(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := d.Jam(ctx, "jam_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.Result != "passed" || got.HoldID != "hold_9" || got.Actions != 3 || got.TokensIn != 1200 || got.EndedAt.IsZero() || got.Open() {
		t.Errorf("jam after update: %+v", got)
	}
	events, err := d.JamEvents(ctx, "jam_1", 1)
	if err != nil || len(events) != 2 || events[0].Kind != "call" || events[1].Seq != 3 {
		t.Errorf("events after 1: %+v, %v", events, err)
	}
	mine, _ := d.Jams(ctx, "token:ci", 10)
	all, _ := d.Jams(ctx, "", 10)
	if len(mine) != 1 || mine[0].ID != "jam_2" || len(all) != 2 {
		t.Errorf("listing: mine %v, all %v", mine, all)
	}
	if _, err := d.Jam(ctx, "jam_none"); err != station.ErrNotFound {
		t.Errorf("missing jam: %v", err)
	}
}

func TestScopedToken(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	secret, err := d.CreateScopedToken(ctx, "jam:jam_1", false, "rig_a/phone", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := d.LookupToken(ctx, secret)
	if err != nil || tok.Device != "rig_a/phone" || tok.ExpiresAt.IsZero() || tok.ReadOnly {
		t.Fatalf("scoped token: %+v, %v", tok, err)
	}
	if _, err := d.CreateToken(ctx, "ci", true); err != nil {
		t.Fatal(err)
	}
	list, _ := d.Tokens(ctx)
	if len(list) != 1 || list[0].Name != "ci" {
		t.Errorf("the jam's token is listed with the owner's: %+v", list)
	}
	gone, err := d.CreateScopedToken(ctx, "jam:old", false, "rig_a/phone", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.LookupToken(ctx, gone); err != station.ErrNotFound {
		t.Errorf("expired token found: %v", err)
	}
}

// A database from before the rename carries its verdicts over as results.
func TestJamsVerdictBecomesResult(t *testing.T) {
	path := t.TempDir() + "/old.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := `CREATE TABLE jams (id TEXT PRIMARY KEY, subject TEXT NOT NULL, device_id TEXT NOT NULL, model TEXT NOT NULL, prompt TEXT NOT NULL,
		mode TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, took TEXT NOT NULL DEFAULT '', verdict TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '',
		hold_id TEXT NOT NULL DEFAULT '', actions INTEGER NOT NULL DEFAULT 0, tokens_in INTEGER NOT NULL DEFAULT 0, tokens_out INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, ended_at INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE jam_events (jam_id TEXT NOT NULL, seq INTEGER NOT NULL, at INTEGER NOT NULL, kind TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY (jam_id, seq));
		INSERT INTO jams (id, subject, device_id, model, prompt, status, took, verdict, summary, created_at, ended_at) VALUES ('jam_old', 'owner', 'rig_a/phone', 'gpt', 'home?', 'done', 'check', 'passed', 'Yes.', 1700000000, 1700000060);
		INSERT INTO jams (id, subject, device_id, model, prompt, status, took, verdict, summary, created_at, ended_at) VALUES ('jam_last', 'owner', 'rig_a/phone', 'gpt', 'checkout?', 'done', 'run', 'completed', 'Session ended.', 1700000100, 1700000400);
		INSERT INTO jam_events VALUES ('jam_last', 1, 1700000100, 'turn', '{"kind":"turn","text":"checkout?"}');
		INSERT INTO jam_events VALUES ('jam_last', 2, 1700000200, 'verdict', '{"kind":"verdict","verdict":{"took":"check","status":"inconclusive","summary":"A popup was in the way."}}');
		INSERT INTO jam_events VALUES ('jam_last', 3, 1700000250, 'verdict', '{"kind":"verdict","verdict":{"took":"check","status":"passed","summary":"Checkout works."}}');
		INSERT INTO jam_events VALUES ('jam_last', 4, 1700000300, 'verdict', '{"kind":"verdict","verdict":{"took":"run","status":"completed","summary":"Session ended."}}');
		INSERT INTO jams (id, subject, device_id, model, prompt, status, created_at, ended_at) VALUES ('jam_none', 'owner', 'rig_a/phone', 'gpt', 'home?', 'stopped', 1700000500, 1700000510);`
	if _, err := raw.Exec(old); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Jam(context.Background(), "jam_old")
	if err != nil || got.Took != "check" || got.Result != "passed" || got.Summary != "Yes." {
		t.Errorf("after the rename: %+v, %v", got, err)
	}
	if has, _ := d.hasColumn("jams", "verdict"); has {
		t.Error("the verdict column is still there")
	}
	// A jam whose last result was an errand after a check reads as the
	// check again, with the last check's verdict; a jam with no result
	// stays as it was.
	got, err = d.Jam(context.Background(), "jam_last")
	if err != nil || got.Took != "check" || got.Result != "passed" || got.Summary != "Checkout works." {
		t.Errorf("reread: %+v, %v", got, err)
	}
	if got, err := d.Jam(context.Background(), "jam_none"); err != nil || got.Took != "" || got.Result != "" || got.Status != "stopped" {
		t.Errorf("no result: %+v, %v", got, err)
	}
}
