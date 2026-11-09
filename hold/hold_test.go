package hold

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
	f := &fixture{db: db, rigKey: nostr.Generate(), clock: time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	if err := db.PutRig(ctx, station.Rig{ID: "rig_1", Name: "pi", PubKey: f.rigKey.Public(), EnrolledAt: f.clock,
		Devices: []station.Device{{ID: "rig_1/phone", Kind: "phone", Caps: []string{"video", "hid"}}}}); err != nil {
		t.Fatal(err)
	}
	f.m = &Manager{Holds: db, Devices: db, Events: db, Audit: db, Key: nostr.Generate(),
		Now: func() time.Time { return f.clock },
		OnRevoke: func(_ context.Context, rigID string, ev nostr.Event) {
			r, err := token.ParseRevocation(ev)
			if err != nil || rigID != "rig_1" {
				t.Errorf("revocation: %v %s", err, rigID)
			}
			f.revoked = append(f.revoked, r.HoldID)
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
	if res.Observer || res.Hold.Seq != 1 || res.Hold.Subject != "owner" || !station.HasScope(res.Hold.Scopes, station.ScopeView) {
		t.Fatalf("first hold: %+v", res)
	}
	g, err := token.Verify(res.Event, f.verifier())
	if err != nil {
		t.Fatal(err)
	}
	if g.HoldID != res.Hold.ID || g.Seq != 1 || !g.HasScope(station.ScopeInput) || !g.HasScope(station.ScopeView) {
		t.Errorf("grant: %+v", g)
	}
	// The grant is in the event store.
	if n, _ := f.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindGrant}}); n != 1 {
		t.Errorf("stored grants: %d", n)
	}
	// The same subject again: the same hold, refreshed.
	f.clock = f.clock.Add(3 * time.Minute)
	again, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	if err != nil {
		t.Fatal(err)
	}
	if again.Hold.ID != res.Hold.ID || again.Hold.Seq != 1 || !again.Hold.ActiveAt.Equal(f.clock) || again.Event.ID == res.Event.ID {
		t.Errorf("refresh: %+v", again)
	}
	// Only the holder releases.
	if err := f.m.Release(ctx, res.Hold.ID, "token:ci"); !errors.Is(err, ErrNotHolder) {
		t.Errorf("stranger released: %v", err)
	}
	if err := f.m.Release(ctx, res.Hold.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != res.Hold.ID {
		t.Errorf("revocations: %v", f.revoked)
	}
	if _, err := f.db.Current(ctx, "rig_1/phone"); !errors.Is(err, station.ErrNoHold) {
		t.Errorf("still held: %v", err)
	}
	if n, _ := f.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindRevocation}}); n != 1 {
		t.Errorf("stored revocations: %d", n)
	}
	// A second release is quiet.
	if err := f.m.Release(ctx, res.Hold.ID, "owner"); err != nil || len(f.revoked) != 1 {
		t.Errorf("second release: %v %v", err, f.revoked)
	}
}

func TestObserverAndTakeover(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	holder, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	// View only from someone else: an observer on the holder's hold.
	obs, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci"})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Observer || obs.Hold.ID != holder.Hold.ID || obs.Grant.Seq != 1 || obs.Grant.HasScope(station.ScopeInput) || !obs.Grant.HasScope(station.ScopeView) {
		t.Errorf("observer: %+v", obs)
	}
	if obs.Grant.Subject != "owner" {
		t.Errorf("observer grant subject %q: the hold's holder is the subject", obs.Grant.Subject)
	}
	if len(f.revoked) != 0 {
		t.Errorf("observer ended the hold: %v", f.revoked)
	}
	// Input from someone else without Recall: a place in line, not a hold.
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci", Scopes: []station.Scope{station.ScopeBidi}}); err == nil {
		t.Fatal("an agent took a held device without recalling it")
	} else if q, ok := err.(*Queued); !ok || q.Position != 1 || q.Holder != "owner" {
		t.Fatalf("queued: %v", err)
	}
	if f.m.Waiting("rig_1/phone") != 1 || f.m.Position("rig_1/phone", "token:ci") != 1 {
		t.Errorf("waiting %d, position %d", f.m.Waiting("rig_1/phone"), f.m.Position("rig_1/phone", "token:ci"))
	}
	if !f.m.Leave("rig_1/phone", "token:ci") || f.m.Waiting("rig_1/phone") != 0 {
		t.Error("could not leave the line")
	}
	// A recall from someone else: takeover with a new hold.
	take, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci", Scopes: []station.Scope{station.ScopeBidi}, Recall: true})
	if err != nil {
		t.Fatal(err)
	}
	if take.Observer || take.Hold.ID == holder.Hold.ID || take.Hold.Seq != 2 || take.Hold.Subject != "token:ci" {
		t.Errorf("takeover: %+v", take)
	}
	if len(f.revoked) != 1 || f.revoked[0] != holder.Hold.ID {
		t.Errorf("old hold not revoked: %v", f.revoked)
	}
	cur, _ := f.db.Current(ctx, "rig_1/phone")
	if cur.ID != take.Hold.ID {
		t.Errorf("current %s, want %s", cur.ID, take.Hold.ID)
	}
	// The holder asking for more scopes gets a new hold too.
	more, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci", Scopes: []station.Scope{station.ScopeBidi, station.ScopeInput}})
	if more.Hold.Seq != 3 || len(f.revoked) != 2 {
		t.Errorf("more scopes: seq %d, revoked %v", more.Hold.Seq, f.revoked)
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
	f.m.Touch(ctx, res.Hold.ID)
	f.clock = f.clock.Add(4 * time.Minute)
	if ended, _ := f.m.Expire(ctx); len(ended) != 0 {
		t.Errorf("ended after a touch: %v", ended)
	}
	f.clock = f.clock.Add(IdleTimeout)
	ended, err := f.m.Expire(ctx)
	if err != nil || len(ended) != 1 || ended[0].ID != res.Hold.ID || len(f.revoked) != 1 {
		t.Errorf("idle: %v %v %v", ended, err, f.revoked)
	}
	// Maximum time, with the hold kept active.
	res, _ = f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}})
	for i := 0; i < 8*60/4; i++ {
		f.clock = f.clock.Add(4 * time.Minute)
		f.m.Touch(ctx, res.Hold.ID)
	}
	ended, _ = f.m.Expire(ctx)
	if len(ended) != 1 || ended[0].ID != res.Hold.ID {
		t.Errorf("maximum time: %v", ended)
	}
	// A grant near the end is cut to the hold's end.
	res, _ = f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner"})
	f.clock = f.clock.Add(MaxDuration - time.Minute)
	near, _ := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner"})
	if !near.Grant.ExpiresAt.Equal(res.Hold.ExpiresAt) {
		t.Errorf("grant expiry %v, hold end %v", near.Grant.ExpiresAt, res.Hold.ExpiresAt)
	}
}

func TestUnknownDevice(t *testing.T) {
	f := setup(t)
	if _, err := f.m.Take(context.Background(), Request{DeviceID: "rig_1/tablet", Subject: "owner"}); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

func TestQueue(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var ended []string
	f.m.OnEnd = func(h station.Hold) { ended = append(ended, h.ID) }
	holder, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:a", Scopes: []station.Scope{station.ScopeInput}})
	if err != nil {
		t.Fatal(err)
	}
	// Two agents line up; the second asks twice and keeps its place.
	for _, sub := range []string{"token:b", "token:c", "token:c"} {
		if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: sub, Scopes: []station.Scope{station.ScopeBidi}}); err == nil {
			t.Fatalf("%s took a held device", sub)
		}
	}
	if f.m.Waiting("rig_1/phone") != 2 || f.m.Position("rig_1/phone", "token:c") != 2 {
		t.Fatalf("line: %d waiting, c at %d", f.m.Waiting("rig_1/phone"), f.m.Position("rig_1/phone", "token:c"))
	}
	// Released: the first in line gets the hold, and Wait hands it over.
	if err := f.m.Release(ctx, holder.Hold.ID, "token:a"); err != nil {
		t.Fatal(err)
	}
	if len(ended) != 1 || ended[0] != holder.Hold.ID {
		t.Errorf("OnEnd: %v", ended)
	}
	got, ok := f.m.Wait(ctx, "rig_1/phone", "token:b")
	if !ok || got.Hold.Subject != "token:b" || !got.Grant.HasScope(station.ScopeBidi) {
		t.Fatalf("b did not get the hold: %+v %v", got, ok)
	}
	cur, _ := f.db.Current(ctx, "rig_1/phone")
	if cur.Subject != "token:b" || f.m.Waiting("rig_1/phone") != 1 {
		t.Errorf("current %s, %d waiting", cur.Subject, f.m.Waiting("rig_1/phone"))
	}
	// b never shows up: after the claim window the hold passes to c.
	f.clock = f.clock.Add(ClaimWindow + time.Second)
	expired, err := f.m.Expire(ctx)
	if err != nil || len(expired) != 1 || expired[0].ID != got.Hold.ID {
		t.Fatalf("expire: %v %v", expired, err)
	}
	cur, _ = f.db.Current(ctx, "rig_1/phone")
	if cur.Subject != "token:c" || f.m.Waiting("rig_1/phone") != 0 {
		t.Errorf("after the no-show: current %s, %d waiting", cur.Subject, f.m.Waiting("rig_1/phone"))
	}
	// c claims it by asking again, so it stays past the window.
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:c", Scopes: []station.Scope{station.ScopeBidi}}); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(ClaimWindow + time.Second)
	if expired, _ := f.m.Expire(ctx); len(expired) != 0 {
		t.Errorf("a claimed hold expired: %v", expired)
	}
	// A free device with someone's turn pending goes to them, not to a
	// newcomer without Recall; a person recalls regardless.
	if err := f.m.Release(ctx, cur.ID, "token:c"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Reserve(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:d", Scopes: []station.Scope{station.ScopeInput}}, f.clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:e", Scopes: []station.Scope{station.ScopeInput}}); err == nil {
		t.Error("a newcomer jumped a due reservation")
	}
	if res, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:d", Scopes: []station.Scope{station.ScopeInput}}); err != nil || res.Hold.Subject != "token:d" {
		t.Errorf("the reserved subject could not take its turn: %+v %v", res, err)
	}
	if res, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}, Recall: true}); err != nil || res.Hold.Subject != "owner" {
		t.Errorf("the owner could not recall: %+v %v", res, err)
	}
	// A reservation for later waits its turn behind the line until then.
	f.m.Leave("rig_1/phone", "token:e")
	if _, err := f.m.Reserve(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:late", Scopes: []station.Scope{station.ScopeInput}}, f.clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:f", Scopes: []station.Scope{station.ScopeInput}})
	own, _ := f.db.Current(ctx, "rig_1/phone")
	f.m.Release(ctx, own.ID, "owner")
	cur, _ = f.db.Current(ctx, "rig_1/phone")
	if cur.Subject != "token:f" {
		t.Errorf("the line went to %s, not f", cur.Subject)
	}
	f.m.Release(ctx, cur.ID, "token:f")
	if _, err := f.db.Current(ctx, "rig_1/phone"); err == nil {
		t.Error("a reservation for later took the device early")
	}
	// Its time comes while the device sits free: the tick hands it over,
	// and a newcomer lines up behind it.
	f.clock = f.clock.Add(61 * time.Minute)
	f.m.Expire(ctx)
	cur, _ = f.db.Current(ctx, "rig_1/phone")
	if cur.Subject != "token:late" {
		t.Errorf("the due reservation was not handed the free device: current %s", cur.Subject)
	}
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:g", Scopes: []station.Scope{station.ScopeInput}}); err == nil {
		t.Error("a newcomer took the reserved device")
	}
	if got, ok := f.m.Wait(ctx, "rig_1/phone", "token:late"); !ok || got.Hold.Subject != "token:late" {
		t.Errorf("Wait after the hand-over: %+v %v", got, ok)
	}
}

func TestOneObserver(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:w1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:w1"}); err != nil {
		t.Error("the same observer asking again was refused")
	}
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:w2"}); err != ErrWatched {
		t.Errorf("a second observer: %v", err)
	}
	f.clock = f.clock.Add(token.GrantLifetime + time.Minute)
	if _, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:w2"}); err != nil {
		t.Errorf("after the first observer's grant lapsed: %v", err)
	}
}

func TestWatch(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// A watch request on a free device: with control among the scopes it
	// takes the device, as a page opening on a free device does; view
	// only, it holds the device view only, so the picture shows, and
	// anyone asking for control takes it over at once.
	look, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "guest", Watch: true})
	if err != nil || look.Observer || Controls(look.Hold.Scopes) || look.Hold.Subject != "guest" {
		t.Fatalf("watching a free device: %+v %v", look, err)
	}
	drive, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}, Recall: true, Watch: true})
	if err != nil || drive.Observer || !drive.Grant.HasScope(station.ScopeInput) || drive.Hold.ID == look.Hold.ID || drive.Hold.Seq != look.Hold.Seq+1 {
		t.Fatalf("driving a watched device: %+v %v", drive, err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != look.Hold.ID || f.m.Waiting("rig_1/phone") != 0 {
		t.Errorf("the watcher's hold should end and nobody wait: revoked %v, waiting %d", f.revoked, f.m.Waiting("rig_1/phone"))
	}
	taken := len(f.revoked)
	// The same request from someone else while the owner holds it: a
	// seat watching, not a recall and not a place in line, even with
	// Recall set.
	watch, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "guest", Scopes: []station.Scope{station.ScopeInput}, Recall: true, Watch: true})
	if err != nil {
		t.Fatal(err)
	}
	if !watch.Observer || watch.Hold.ID != drive.Hold.ID || watch.Grant.HasScope(station.ScopeInput) || watch.Grant.Subject != "owner" {
		t.Errorf("watch: %+v", watch)
	}
	if len(f.revoked) != taken || f.m.Waiting("rig_1/phone") != 0 {
		t.Errorf("watching changed the hold: revoked %v, waiting %d", f.revoked, f.m.Waiting("rig_1/phone"))
	}
	// The holder's own watch request is a refresh on their hold.
	again, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "owner", Scopes: []station.Scope{station.ScopeInput}, Recall: true, Watch: true})
	if err != nil || again.Observer || again.Hold.ID != drive.Hold.ID || !again.Grant.HasScope(station.ScopeInput) {
		t.Errorf("holder watching: %+v %v", again, err)
	}
	// Without Watch, the guest's request recalls as before.
	take, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "guest", Scopes: []station.Scope{station.ScopeInput}, Recall: true})
	if err != nil || take.Observer || take.Hold.ID == drive.Hold.ID || len(f.revoked) != taken+1 {
		t.Errorf("recall after watching: %+v %v, revoked %v", take, err, f.revoked)
	}
}

// A watcher never stands in an agent's way: an agent with no recall takes
// a device a watcher holds at once, without a place in line, and the
// watcher asking again lands as the agent's observer.
func TestWatcherGivesWay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	look, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "guest", Watch: true})
	if err != nil || Controls(look.Hold.Scopes) {
		t.Fatalf("watching: %+v %v", look, err)
	}
	agent, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "token:ci", Scopes: []station.Scope{station.ScopeInput}})
	if err != nil || agent.Observer || agent.Hold.Subject != "token:ci" {
		t.Fatalf("an agent should take a watched device at once: %+v %v", agent, err)
	}
	again, err := f.m.Take(ctx, Request{DeviceID: "rig_1/phone", Subject: "guest", Watch: true})
	if err != nil || !again.Observer || again.Hold.ID != agent.Hold.ID {
		t.Fatalf("the watcher should observe the agent: %+v %v", again, err)
	}
}
