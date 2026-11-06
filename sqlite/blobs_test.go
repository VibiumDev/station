package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VibiumDev/station"
)

func TestBlobs(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	now := time.Now()
	a := station.Blob{SHA256: "a" + repeat("0", 63), Size: 10, Type: "application/zip", Name: "record.zip", Uploader: "token:ci", PubKey: "k1",
		HoldID: "hld_1", DeviceID: "rig_1/phone", RigID: "rig_1", CreatedAt: now.Add(-2 * time.Hour)}
	b := station.Blob{SHA256: "b" + repeat("0", 63), Size: 20, Type: "image/jpeg", Uploader: "rig:rig_1", PubKey: "k2",
		HoldID: "hld_2", DeviceID: "rig_1/phone", RigID: "rig_1", CreatedAt: now.Add(-time.Hour), Public: true}
	c := station.Blob{SHA256: "c" + repeat("0", 63), Size: 30, Type: "text/plain", Uploader: "owner", PubKey: "k1",
		DeviceID: "rig_2/phone", RigID: "rig_2", CreatedAt: now}
	for _, x := range []station.Blob{a, b, c} {
		if err := d.PutBlob(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.Blob(ctx, a.SHA256)
	if err != nil || got.Name != "record.zip" || got.HoldID != "hld_1" || got.Public {
		t.Fatalf("blob a: %+v %v", got, err)
	}
	if _, err := d.Blob(ctx, "d"+repeat("0", 63)); !errors.Is(err, station.ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
	all, _ := d.Blobs(ctx, station.BlobQuery{})
	if len(all) != 3 || all[0].SHA256 != c.SHA256 || all[2].SHA256 != a.SHA256 {
		t.Fatalf("newest first: %v", shas(all))
	}
	oldest, _ := d.Blobs(ctx, station.BlobQuery{OldestFirst: true, Limit: 1})
	if len(oldest) != 1 || oldest[0].SHA256 != a.SHA256 {
		t.Fatalf("oldest first with a limit: %v", shas(oldest))
	}
	dev, _ := d.Blobs(ctx, station.BlobQuery{DeviceID: "rig_1/phone"})
	if len(dev) != 2 {
		t.Fatalf("by device: %v", shas(dev))
	}
	hold, _ := d.Blobs(ctx, station.BlobQuery{HoldID: "hld_2"})
	if len(hold) != 1 || hold[0].SHA256 != b.SHA256 {
		t.Fatalf("by hold: %v", shas(hold))
	}
	key, _ := d.Blobs(ctx, station.BlobQuery{PubKey: "k1"})
	if len(key) != 2 {
		t.Fatalf("by key: %v", shas(key))
	}
	before, _ := d.Blobs(ctx, station.BlobQuery{Before: now.Add(-90 * time.Minute)})
	if len(before) != 1 || before[0].SHA256 != a.SHA256 {
		t.Fatalf("before: %v", shas(before))
	}
	n, size, err := d.BlobTotal(ctx)
	if err != nil || n != 3 || size != 60 {
		t.Fatalf("total: %d %d %v", n, size, err)
	}
	if err := d.SetBlobPublic(ctx, a.SHA256, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Blob(ctx, a.SHA256); !got.Public {
		t.Fatal("public not set")
	}
	if err := d.SetBlobPublic(ctx, "d"+repeat("0", 63), true); !errors.Is(err, station.ErrNotFound) {
		t.Fatalf("public on a missing blob: %v", err)
	}
	a.EventID = "ev1"
	if err := d.PutBlob(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Blob(ctx, a.SHA256); got.EventID != "ev1" || got.Public {
		t.Fatalf("put again replaces the row: %+v", got)
	}
	if err := d.DeleteBlob(ctx, b.SHA256); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := d.BlobTotal(ctx); n != 2 {
		t.Fatalf("after delete: %d", n)
	}
	if n, size, err := open(t).BlobTotal(ctx); err != nil || n != 0 || size != 0 {
		t.Fatalf("empty total: %d %d %v", n, size, err)
	}
}

func TestHoldsOf(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	now := time.Now()
	for i, sub := range []string{"token:ci", "token:ci", "owner"} {
		_, err := d.Create(ctx, station.Hold{ID: "hld_" + string(rune('a'+i)), DeviceID: "rig_1/phone", RigID: "rig_1", Subject: sub,
			Scopes: []station.Scope{station.ScopeView}, CreatedAt: now.Add(time.Duration(i) * time.Minute), ActiveAt: now, ExpiresAt: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.HoldsOf(ctx, "token:ci", now.Add(-time.Hour))
	if err != nil || len(got) != 2 || got[0].ID != "hld_b" {
		t.Fatalf("holds of token:ci: %+v %v", got, err)
	}
	if got, _ := d.HoldsOf(ctx, "token:ci", now.Add(30*time.Second)); len(got) != 1 {
		t.Fatalf("since: %d", len(got))
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func shas(bs []station.Blob) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.SHA256[:1])
	}
	return out
}
