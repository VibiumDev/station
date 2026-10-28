package lease

import (
	"context"
	"errors"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
)

type fixture struct {
	m       *Manager
	db      *sqlite.DB
	rigKey  nostr.SecretKey
	revoked []string
	clock   time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := &fixture{db: db, rigKey: nostr.Generate(), clock: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	if err := db.PutRig(ctx, station.Rig{ID: "rig_1", Name: "pi", PubKey: f.rigKey.Public(), EnrolledAt: f.clock,
		Devices: []station.Device{{ID: "rig_1/phone", Kind: "phone", Caps: []string{"video", "hid"}}}}); err != nil {
		t.Fatal(err)
	}
	f.m = &Manager{Leases: db, Devices: db, Events: db, Audit: db, Key: nostr.Generate(),
		Now: func() time.Time { return f.clock },
		OnRevoke: func(_ context.Context, rigID string, ev nostr.Event) {
			r, err := token.ParseRevocation(ev)
			if err != nil || rigID != "rig_1" {
				t.Errorf("revocation: %v %s", err, rigID)
			}
			f.revoked = append(f.revoked, r.LeaseID)
		}}
	return f
}

func (f *fixture) verifier() token.Verifier {
	return token.Verifier{Stations: []nostr.PubKey{f.m.Key.Public()}, Rig: f.rigKey.Public(), RigID: "rig_1", Devices: []string{"rig_1/phone"}, Now: f.clock}
}

func TestTakeRefreshAndRelease(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	res, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Observer || res.Lease.Seq != 1 || res.Lease.Subject != "owner" || !station.HasScope(res.Lease.Scopes, station.ScopeView) {
		t.Fatalf("first lease: %+v", res)
	}
	g, err := token.Verify(res.Event, f.verifier())
	if err != nil {
		t.Fatal(err)
	}
	if g.LeaseID != res.Lease.ID || g.Seq != 1 || !g.HasScope(station.ScopeInput) || !g.HasScope(station.ScopeView) {
		t.Errorf("grant: %+v", g)
	}
	// The grant is in the event store.
	if n, _ := f.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindGrant}}); n != 1 {
		t.Errorf("stored grants: %d", n)
	}
	// The same subject again: the same lease, refreshed.
	f.clock = f.clock.Add(3 * time.Minute)
	again, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	if err != nil {
		t.Fatal(err)
	}
	if again.Lease.ID != res.Lease.ID || again.Lease.Seq != 1 || !again.Lease.ActiveAt.Equal(f.clock) || again.Event.ID == res.Event.ID {
		t.Errorf("refresh: %+v", again)
	}
	// Only the holder releases.
	if err := f.m.Release(ctx, res.Lease.ID, "token:ci"); !errors.Is(err, ErrNotHolder) {
		t.Errorf("stranger released: %v", err)
	}
	if err := f.m.Release(ctx, res.Lease.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != res.Lease.ID {
		t.Errorf("revocations: %v", f.revoked)
	}
	if _, err := f.db.Current(ctx, "rig_1/phone"); !errors.Is(err, station.ErrNoLease) {
		t.Errorf("still held: %v", err)
	}
	if n, _ := f.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindRevocation}}); n != 1 {
		t.Errorf("stored revocations: %d", n)
	}
	// A second release is quiet.
	if err := f.m.Release(ctx, res.Lease.ID, "owner"); err != nil || len(f.revoked) != 1 {
		t.Errorf("second release: %v %v", err, f.revoked)
	}
}

func TestObserverAndTakeover(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	holder, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	// View only from someone else: an observer on the holder's lease.
	obs, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci"})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Observer || obs.Lease.ID != holder.Lease.ID || obs.Grant.Seq != 1 || obs.Grant.HasScope(station.ScopeInput) || !obs.Grant.HasScope(station.ScopeView) {
		t.Errorf("observer: %+v", obs)
	}
	if obs.Grant.Subject != "owner" {
		t.Errorf("observer grant subject %q: the lease's holder is the subject", obs.Grant.Subject)
	}
	if len(f.revoked) != 0 {
		t.Errorf("observer ended the lease: %v", f.revoked)
	}
	// Input from someone else: takeover with a new lease.
	take, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci", Scopes: []station.Scope{station.ScopeBidi}})
	if err != nil {
		t.Fatal(err)
	}
	if take.Observer || take.Lease.ID == holder.Lease.ID || take.Lease.Seq != 2 || take.Lease.Subject != "token:ci" {
		t.Errorf("takeover: %+v", take)
	}
	if len(f.revoked) != 1 || f.revoked[0] != holder.Lease.ID {
		t.Errorf("old lease not revoked: %v", f.revoked)
	}
	cur, _ := f.db.Current(ctx, "rig_1/phone")
	if cur.ID != take.Lease.ID {
		t.Errorf("current %s, want %s", cur.ID, take.Lease.ID)
	}
	// The holder asking for more scopes gets a new lease too.
	more, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci", Scopes: []station.Scope{station.ScopeBidi, station.ScopeInput}})
	if more.Lease.Seq != 3 || len(f.revoked) != 2 {
		t.Errorf("more scopes: seq %d, revoked %v", more.Lease.Seq, f.revoked)
	}
}

func TestExpire(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	res, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	f.clock = f.clock.Add(4 * time.Minute)
	if ended, _ := f.m.Expire(ctx); len(ended) != 0 {
		t.Errorf("ended early: %v", ended)
	}
	f.m.Touch(ctx, res.Lease.ID)
	f.clock = f.clock.Add(4 * time.Minute)
	if ended, _ := f.m.Expire(ctx); len(ended) != 0 {
		t.Errorf("ended after a touch: %v", ended)
	}
	f.clock = f.clock.Add(IdleTimeout)
	ended, err := f.m.Expire(ctx)
	if err != nil || len(ended) != 1 || ended[0].ID != res.Lease.ID || len(f.revoked) != 1 {
		t.Errorf("idle: %v %v %v", ended, err, f.revoked)
	}
	// Maximum time, with the lease kept active.
	res, _ = f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	for i := 0; i < 8*60/4; i++ {
		f.clock = f.clock.Add(4 * time.Minute)
		f.m.Touch(ctx, res.Lease.ID)
	}
	ended, _ = f.m.Expire(ctx)
	if len(ended) != 1 || ended[0].ID != res.Lease.ID {
		t.Errorf("maximum time: %v", ended)
	}
	// A grant near the end is cut to the lease's end.
	res, _ = f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner"})
	f.clock = f.clock.Add(MaxDuration - time.Minute)
	near, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner"})
	if !near.Grant.ExpiresAt.Equal(res.Lease.ExpiresAt) {
		t.Errorf("grant expiry %v, lease end %v", near.Grant.ExpiresAt, res.Lease.ExpiresAt)
	}
}

func TestUnknownDevice(t *testing.T) {
	f := setup(t)
	if _, err := f.m.Take(context.Background(), Request{DeviceID: "rig_1/tablet", Subject: "owner"}); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}
