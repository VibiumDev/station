package sqlite

import (
	"context"
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
	j.Status, j.Took, j.Verdict, j.Summary, j.HoldID, j.Actions, j.TokensIn, j.EndedAt = "done", "check", "passed", "The home screen shows.", "hold_9", 3, 1200, time.Now()
	if err := d.UpdateJam(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := d.Jam(ctx, "jam_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.Verdict != "passed" || got.HoldID != "hold_9" || got.Actions != 3 || got.TokensIn != 1200 || got.EndedAt.IsZero() || got.Open() {
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
