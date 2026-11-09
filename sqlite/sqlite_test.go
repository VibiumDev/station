package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

func open(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// The store satisfies the interfaces.
var (
	_ station.EventStore  = (*DB)(nil)
	_ station.DeviceStore = (*DB)(nil)
	_ station.HoldStore   = (*DB)(nil)
	_ station.AuditSink   = (*DB)(nil)
)

func signed(t *testing.T, sk nostr.SecretKey, kind nostr.Kind, at int64, tags nostr.Tags, content string) nostr.Event {
	t.Helper()
	ev := nostr.Event{Kind: kind, CreatedAt: nostr.Timestamp(at), Tags: tags, Content: content}
	if err := ev.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return ev
}

func collect(seq func(func(nostr.Event) bool)) []nostr.Event {
	var out []nostr.Event
	seq(func(ev nostr.Event) bool { out = append(out, ev); return true })
	return out
}

func TestEvents(t *testing.T) {
	d := open(t)
	sk, other := nostr.Generate(), nostr.Generate()
	rig := nostr.Generate().Public()
	a := signed(t, sk, token.KindGrant, 100, nostr.Tags{{"p", rig.Hex()}, {"hold", "hld_1"}}, "")
	b := signed(t, sk, token.KindGrant, 200, nostr.Tags{{"p", rig.Hex()}, {"hold", "hld_2"}}, "")
	c := signed(t, other, token.KindRevocation, 300, nostr.Tags{{"p", rig.Hex()}}, "")
	for _, ev := range []nostr.Event{a, b, c} {
		if err := d.SaveEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SaveEvent(a); err == nil {
		t.Error("saved the same event twice")
	}
	// Verbatim.
	got := collect(d.QueryEvents(nostr.Filter{IDs: []nostr.ID{a.ID}}, 0))
	if len(got) != 1 || got[0].ID != a.ID || got[0].Sig != a.Sig || !got[0].VerifySignature() {
		t.Fatalf("event not kept verbatim: %+v", got)
	}
	cases := []struct {
		name string
		f    nostr.Filter
		want []nostr.ID
	}{
		{"all newest first", nostr.Filter{}, []nostr.ID{c.ID, b.ID, a.ID}},
		{"by kind", nostr.Filter{Kinds: []nostr.Kind{token.KindGrant}}, []nostr.ID{b.ID, a.ID}},
		{"by author", nostr.Filter{Authors: []nostr.PubKey{other.Public()}}, []nostr.ID{c.ID}},
		{"by p tag", nostr.Filter{Tags: nostr.TagMap{"p": {rig.Hex()}}}, []nostr.ID{c.ID, b.ID, a.ID}},
		{"by hold tag", nostr.Filter{Tags: nostr.TagMap{"hold": {"hld_2"}}}, nil}, // multi-letter tags are not indexed
		{"since", nostr.Filter{Since: 200}, []nostr.ID{c.ID, b.ID}},
		{"until", nostr.Filter{Until: 200}, []nostr.ID{b.ID, a.ID}},
		{"limit", nostr.Filter{Limit: 1}, []nostr.ID{c.ID}},
		{"limit zero", nostr.Filter{LimitZero: true}, nil},
	}
	for _, c := range cases {
		got := collect(d.QueryEvents(c.f, 0))
		if len(got) != len(c.want) {
			t.Errorf("%s: %d events, want %d", c.name, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i].ID != c.want[i] {
				t.Errorf("%s: event %d is %s, want %s", c.name, i, got[i].ID, c.want[i])
			}
		}
	}
	if n, _ := d.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindGrant}}); n != 2 {
		t.Errorf("count %d, want 2", n)
	}
	got = collect(d.QueryEvents(nostr.Filter{}, 2))
	if len(got) != 2 {
		t.Errorf("maxLimit: %d events, want 2", len(got))
	}
	if err := d.DeleteEvent(a.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.CountEvents(nostr.Filter{}); n != 2 {
		t.Errorf("after delete %d, want 2", n)
	}
}

func TestReplaceEvents(t *testing.T) {
	d := open(t)
	sk := nostr.Generate()
	st := nostr.Generate().Public()
	old := signed(t, sk, token.KindRigAnnounce, 100, nostr.Tags{{"p", st.Hex()}}, "old")
	newer := signed(t, sk, token.KindRigAnnounce, 200, nostr.Tags{{"p", st.Hex()}}, "new")
	if _, err := d.ReplaceEvent(old); err != nil {
		t.Fatal(err)
	}
	deleted, err := d.ReplaceEvent(newer)
	if err != nil || len(deleted) != 1 || deleted[0].ID != old.ID {
		t.Fatalf("replace: deleted %v, err %v", deleted, err)
	}
	if deleted, err := d.ReplaceEvent(old); err != nil || deleted != nil {
		t.Errorf("older event replaced the newer one: %v %v", deleted, err)
	}
	got := collect(d.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindRigAnnounce}}, 0))
	if len(got) != 1 || got[0].Content != "new" {
		t.Errorf("kept %v", got)
	}
	// Addressable: one per d tag.
	phone := signed(t, sk, token.KindDeviceAnnounce, 100, nostr.Tags{{"d", "rig_1/phone"}}, "")
	tablet := signed(t, sk, token.KindDeviceAnnounce, 100, nostr.Tags{{"d", "rig_1/tablet"}}, "")
	phone2 := signed(t, sk, token.KindDeviceAnnounce, 150, nostr.Tags{{"d", "rig_1/phone"}}, "again")
	for _, ev := range []nostr.Event{phone, tablet, phone2} {
		if _, err := d.ReplaceEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	got = collect(d.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindDeviceAnnounce}}, 0))
	if len(got) != 2 {
		t.Errorf("addressable: kept %d, want 2", len(got))
	}
	if n, _ := d.CountEvents(nostr.Filter{Tags: nostr.TagMap{"d": {"rig_1/phone"}}}); n != 1 {
		t.Errorf("phone announcements %d, want 1", n)
	}
}

func TestRigsAndDevices(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	pk := nostr.Generate().Public()
	rig := station.Rig{ID: "rig_1", Name: "pi", PubKey: pk, EnrolledAt: time.Now(),
		Devices: []station.Device{{ID: "rig_1/phone", Kind: "phone", Caps: []string{"video", "hid"}, Shortcuts: []station.Shortcut{{Label: "Home", Keys: "GUI+h"}}}}}
	if err := d.PutRig(ctx, rig); err != nil {
		t.Fatal(err)
	}
	got, err := d.RigByKey(ctx, pk)
	if err != nil || got.ID != "rig_1" || len(got.Devices) != 1 || got.Devices[0].Shortcuts[0].Keys != "GUI+h" {
		t.Fatalf("by key: %+v %v", got, err)
	}
	if err := d.SetDevices(ctx, "rig_1", []station.Device{{ID: "rig_1/phone", Kind: "phone", Caps: []string{"video"}}, {ID: "rig_1/tablet", Kind: "phone", Caps: nil, Input: "touch", Model: "iPad Air", OSVersion: "26.6", Platform: "ios"}}); err != nil {
		t.Fatal(err)
	}
	dev, err := d.Device(ctx, "rig_1/tablet")
	if err != nil || dev.Kind != "phone" || dev.Input != "touch" || dev.Model != "iPad Air" || dev.OSVersion != "26.6" || dev.Title() != "iPad Air" {
		t.Fatalf("device: %+v %v", dev, err)
	}
	if plain, _ := d.Device(ctx, "rig_1/phone"); plain.Title() != "phone" {
		t.Errorf("an unlabelled device without a model is titled by its name: %q", plain.Title())
	}
	rigs, _ := d.Rigs(ctx)
	if len(rigs) != 1 || len(rigs[0].Devices) != 2 {
		t.Errorf("rigs: %+v", rigs)
	}
	if err := d.SetDevices(ctx, "rig_9", nil); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("devices of an unknown rig: %v", err)
	}
	if err := d.RemoveRig(ctx, "rig_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Device(ctx, "rig_1/phone"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("device survived its rig: %v", err)
	}
	if err := d.RemoveRig(ctx, "rig_1"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("second remove: %v", err)
	}
}

func TestHolds(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	now := time.Now()
	mk := func(id string) station.Hold {
		return station.Hold{ID: id, DeviceID: "rig_1/phone", RigID: "rig_1", Subject: "owner", Scopes: []station.Scope{station.ScopeView, station.ScopeInput},
			CreatedAt: now, ActiveAt: now, ExpiresAt: now.Add(8 * time.Hour)}
	}
	if _, err := d.Current(ctx, "rig_1/phone"); !errors.Is(err, station.ErrNoHold) {
		t.Errorf("empty: %v", err)
	}
	l1, err := d.Create(ctx, mk("hld_1"))
	if err != nil || l1.Seq != 1 {
		t.Fatalf("first: %+v %v", l1, err)
	}
	l2, err := d.Create(ctx, mk("hld_2"))
	if err != nil || l2.Seq != 2 {
		t.Fatalf("second: %+v %v", l2, err)
	}
	cur, err := d.Current(ctx, "rig_1/phone")
	if err != nil || cur.ID != "hld_2" {
		t.Fatalf("current: %+v %v", cur, err)
	}
	if err := d.Release(ctx, "hld_2", now); err != nil {
		t.Fatal(err)
	}
	if err := d.Release(ctx, "hld_2", now); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("released twice: %v", err)
	}
	cur, err = d.Current(ctx, "rig_1/phone")
	if err != nil || cur.ID != "hld_1" {
		t.Fatalf("after release: %+v %v", cur, err)
	}
	if err := d.Touch(ctx, "hld_1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Hold(ctx, "hld_1")
	if !got.ActiveAt.Equal(now.Add(time.Minute)) {
		t.Errorf("touch not kept: %v", got.ActiveAt)
	}
	held, _ := d.Held(ctx)
	if len(held) != 1 || held[0].ID != "hld_1" {
		t.Errorf("held: %+v", held)
	}
	// An expired hold is not current.
	short := mk("hld_3")
	short.ExpiresAt = now.Add(-time.Second)
	if _, err := d.Create(ctx, short); err != nil {
		t.Fatal(err)
	}
	cur, _ = d.Current(ctx, "rig_1/phone")
	if cur.ID != "hld_1" {
		t.Errorf("expired hold is current: %+v", cur)
	}
	// The counter keeps going after a device is re-announced.
	l4, _ := d.Create(ctx, mk("hld_4"))
	if l4.Seq != 4 {
		t.Errorf("seq %d, want 4", l4.Seq)
	}
}

func TestTokensAndCodes(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	secret, err := d.CreateToken(ctx, "ci", true, "")
	if err != nil || secret == "" {
		t.Fatal(err)
	}
	if _, err := d.CreateToken(ctx, "ci", false, ""); !errors.Is(err, ErrTokenExists) {
		t.Errorf("duplicate name: %v", err)
	}
	tok, err := d.LookupToken(ctx, secret)
	if err != nil || tok.Name != "ci" || !tok.ReadOnly {
		t.Fatalf("lookup: %+v %v", tok, err)
	}
	if _, err := d.LookupToken(ctx, "stk_nope"); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("unknown secret: %v", err)
	}
	if err := d.RevokeToken(ctx, "ci"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.LookupToken(ctx, secret); !errors.Is(err, station.ErrNotFound) {
		t.Errorf("revoked token still works: %v", err)
	}
	now := time.Now()
	if err := d.SaveCode(ctx, "ABCD-1234-7F3A", now.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := d.UseCode(ctx, "ABCD-1234-7F3A", now); err != nil {
		t.Fatal(err)
	}
	if err := d.UseCode(ctx, "ABCD-1234-7F3A", now); !errors.Is(err, ErrCode) {
		t.Errorf("reused: %v", err)
	}
	d.SaveCode(ctx, "WXYZ-0000-7F3A", now.Add(15*time.Minute))
	if err := d.UseCode(ctx, "WXYZ-0000-7F3A", now.Add(16*time.Minute)); !errors.Is(err, ErrCode) {
		t.Errorf("expired: %v", err)
	}
	if err := d.UseCode(ctx, "NOPE-0000-7F3A", now); !errors.Is(err, ErrCode) {
		t.Errorf("unknown: %v", err)
	}
	d.Audit(ctx, station.AuditEntry{Subject: "owner", Action: "hold", DeviceID: "rig_1/phone", HoldID: "hld_1"})
	entries, _ := d.AuditEntries(ctx, 10)
	if len(entries) != 1 || entries[0].Action != "hold" {
		t.Errorf("audit: %+v", entries)
	}
}
