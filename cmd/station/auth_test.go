package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
)

// A token minted for a jam signs in as the jam, bound to its device: it
// may act on that device alone, and not at all once it has expired.
func TestJamTokenIsBoundToItsDevice(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	secret, err := db.CreateScopedToken(ctx, "jam:jam_1", false, "rig_a/phone", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	a := newAuth(&Config{}, db, nil, false, false)
	r := httptest.NewRequest("GET", "/session", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	p, err := a.Authenticate(r)
	if err != nil || p.Subject != "token:jam:jam_1" || p.Device != "rig_a/phone" || p.ReadOnly {
		t.Fatalf("principal: %+v, %v", p, err)
	}
	if err := a.Authorize(ctx, p, station.ActionControl, "rig_a/phone"); err != nil {
		t.Errorf("its own device: %v", err)
	}
	if err := a.Authorize(ctx, p, station.ActionControl, "rig_a/tablet"); err == nil {
		t.Error("another device was allowed")
	}
	if err := a.Authorize(ctx, p, station.ActionView, ""); err != nil {
		t.Errorf("a call about no device in particular: %v", err)
	}
	if err := a.Authorize(ctx, p, station.ActionManage, ""); err == nil {
		t.Error("a jam's token may not manage")
	}
	old, _ := db.CreateScopedToken(ctx, "jam:jam_0", false, "rig_a/phone", time.Now().Add(-time.Second))
	r.Header.Set("Authorization", "Bearer "+old)
	if _, err := a.Authenticate(r); err == nil {
		t.Error("an expired token signed in")
	}
}
