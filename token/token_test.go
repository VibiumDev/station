package token

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
)

var (
	stationKey = nostr.Generate()
	rigKey     = nostr.Generate()
	otherKey   = nostr.Generate()
	now        = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
)

func grant() Grant {
	return Grant{
		Rig: rigKey.Public(), RigID: "rig_0007", DeviceID: "rig_0007/phone", Subject: "owner",
		LeaseID: "lse_9f3c", Seq: 42, Scopes: []station.Scope{station.ScopeView, station.ScopeInput},
		IssuedAt: now,
	}
}

func verifier() Verifier {
	return Verifier{Stations: []nostr.PubKey{stationKey.Public()}, Rig: rigKey.Public(), RigID: "rig_0007",
		Devices: []string{"rig_0007/phone"}, Now: now.Add(time.Minute)}
}

func TestGrantRoundTrip(t *testing.T) {
	ev, err := SignGrant(stationKey, grant())
	if err != nil {
		t.Fatal(err)
	}
	// Through JSON, as it travels.
	data, _ := json.Marshal(ev)
	var back nostr.Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	g, err := Verify(back, verifier())
	if err != nil {
		t.Fatal(err)
	}
	if g.Station != stationKey.Public() || g.Rig != rigKey.Public() || g.RigID != "rig_0007" || g.DeviceID != "rig_0007/phone" ||
		g.Subject != "owner" || g.LeaseID != "lse_9f3c" || g.Seq != 42 || !g.HasScope(station.ScopeInput) || g.HasScope(station.ScopeBidi) {
		t.Errorf("fields lost: %+v", g)
	}
	if !g.ExpiresAt.Equal(now.Add(GrantLifetime)) {
		t.Errorf("expiry %v, want %v", g.ExpiresAt, now.Add(GrantLifetime))
	}
	if g.ID != back.ID {
		t.Errorf("id not kept")
	}
}

func TestGrantWithDeviceKey(t *testing.T) {
	g := grant()
	dev := nostr.Generate().Public()
	g.Device = dev
	ev, err := SignGrant(stationKey, g)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(ev, verifier())
	if err != nil {
		t.Fatal(err)
	}
	if got.Device != dev {
		t.Errorf("device key lost")
	}
}

func TestGrantChecks(t *testing.T) {
	ev, _ := SignGrant(stationKey, grant())
	cases := []struct {
		name string
		ev   func() nostr.Event
		v    func() Verifier
		want error
	}{
		{"tampered scope", func() nostr.Event {
			e := ev
			e.Tags = e.Tags.CloneDeep()
			e.Tags = append(e.Tags, nostr.Tag{"scope", "bidi"})
			return e
		}, verifier, ErrSignature},
		{"tampered seq keeps id", func() nostr.Event {
			e := ev
			e.Tags = e.Tags.CloneDeep()
			for _, tg := range e.Tags {
				if tg[0] == "seq" {
					tg[1] = "43"
				}
			}
			return e
		}, verifier, ErrSignature},
		{"wrong signer", func() nostr.Event {
			e, _ := SignGrant(otherKey, grant())
			return e
		}, verifier, ErrStation},
		{"expired", func() nostr.Event { return ev }, func() Verifier {
			v := verifier()
			v.Now = now.Add(GrantLifetime + Skew + time.Second)
			return v
		}, ErrExpired},
		{"inside the skew", func() nostr.Event { return ev }, func() Verifier {
			v := verifier()
			v.Now = now.Add(GrantLifetime + Skew - time.Second)
			return v
		}, nil},
		{"from the future", func() nostr.Event { return ev }, func() Verifier {
			v := verifier()
			v.Now = now.Add(-Skew - time.Second)
			return v
		}, ErrNotYetValid},
		{"wrong rig key", func() nostr.Event { return ev }, func() Verifier {
			v := verifier()
			v.Rig = otherKey.Public()
			return v
		}, ErrRig},
		{"wrong rig id", func() nostr.Event { return ev }, func() Verifier {
			v := verifier()
			v.RigID = "rig_0008"
			return v
		}, ErrRig},
		{"wrong device", func() nostr.Event { return ev }, func() Verifier {
			v := verifier()
			v.Devices = []string{"rig_0007/tablet"}
			return v
		}, ErrDevice},
		{"wrong kind", func() nostr.Event {
			e := nostr.Event{Kind: 1, CreatedAt: nostr.Timestamp(now.Unix()), Content: "hi"}
			e.Sign(stationKey)
			return e
		}, verifier, ErrKind},
	}
	for _, c := range cases {
		_, err := Verify(c.ev(), c.v())
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestGrantNeedsItsFields(t *testing.T) {
	g := grant()
	g.Scopes = nil
	if _, err := SignGrant(stationKey, g); !errors.Is(err, ErrMalformed) {
		t.Errorf("no scopes: %v", err)
	}
	// A signed event missing the lease tag parses as malformed, not as a
	// signature error.
	e := nostr.Event{Kind: KindGrant, CreatedAt: nostr.Timestamp(now.Unix()),
		Tags: nostr.Tags{{"p", rigKey.Public().Hex()}, {"rig", "rig_0007"}, {"dev", "rig_0007/phone"}, {"scope", "view"}, {"expiration", "1"}}}
	e.Sign(stationKey)
	if _, err := ParseGrant(e); !errors.Is(err, ErrMalformed) {
		t.Errorf("missing lease: %v", err)
	}
}

func TestHeader(t *testing.T) {
	ev, _ := SignGrant(stationKey, grant())
	h := Header(ev)
	back, err := FromHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(back, verifier()); err != nil {
		t.Fatal(err)
	}
	if _, err := FromHeader("Bearer abc"); !errors.Is(err, ErrMalformed) {
		t.Errorf("bearer accepted: %v", err)
	}
}

func TestRevocation(t *testing.T) {
	ev, err := SignRevocation(stationKey, Revocation{Rig: rigKey.Public(), LeaseID: "lse_9f3c", IssuedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRevocation(ev)
	if err != nil {
		t.Fatal(err)
	}
	if r.Station != stationKey.Public() || r.Rig != rigKey.Public() || r.LeaseID != "lse_9f3c" {
		t.Errorf("fields lost: %+v", r)
	}
	ev.Tags[1][1] = "lse_0000"
	if _, err := ParseRevocation(ev); !errors.Is(err, ErrSignature) {
		t.Errorf("tampered: %v", err)
	}
}

func TestGrantICE(t *testing.T) {
	sk := nostr.Generate()
	g := Grant{Rig: nostr.Generate().Public(), RigID: "rig_1", DeviceID: "rig_1/phone", Subject: "owner", LeaseID: "lse_1", Seq: 1,
		Scopes: []station.Scope{station.ScopeView},
		ICE:    []ICEServer{{URLs: []string{"stun:203.0.113.1:3478"}}, {URLs: []string{"turn:203.0.113.1:3478?transport=udp"}, Username: "1:lse_1", Credential: "secret"}}}
	ev, err := SignGrant(sk, g)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseGrant(ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ICE) != 2 || got.ICE[0].URLs[0] != "stun:203.0.113.1:3478" || got.ICE[1].Username != "1:lse_1" || got.ICE[1].Credential != "secret" || got.ICE[1].URLs[0] != "turn:203.0.113.1:3478?transport=udp" {
		t.Errorf("ICE servers: %+v", got.ICE)
	}
	bad := ev
	bad.Tags = append(nostr.Tags{{"turn", "u"}}, ev.Tags...)
	bad.Sign(sk)
	if _, err := ParseGrant(bad); err == nil {
		t.Error("a turn tag without a credential and URL parsed")
	}
}
