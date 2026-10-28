// Package token builds and checks Station's signed events: lease grants,
// revocations and the handshake events, in the Nostr event format with the
// kinds in docs/kinds.md. The station signs; both the station and the rig
// check, because the rig is the final gate.
package token

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
)

// Station's event kinds; see docs/kinds.md.
const (
	KindProfile        nostr.Kind = 0
	KindGrant          nostr.Kind = 7700
	KindRevocation     nostr.Kind = 7701
	KindEnrollment     nostr.Kind = 7702
	KindAttestation    nostr.Kind = 7703
	KindRigAnnounce    nostr.Kind = 17700
	KindHello          nostr.Kind = 27700
	KindDeviceAnnounce nostr.Kind = 37700
)

const (
	// GrantLifetime is how long a grant is valid; the station refreshes
	// held leases well before.
	GrantLifetime = 10 * time.Minute
	// Skew is the clock difference the checks forgive.
	Skew = 30 * time.Second
)

// Errors a check can return. Verify wraps them with detail.
var (
	ErrKind        = errors.New("not a lease grant")
	ErrSignature   = errors.New("bad signature")
	ErrMalformed   = errors.New("malformed grant")
	ErrExpired     = errors.New("grant expired")
	ErrNotYetValid = errors.New("grant from the future")
	ErrStation     = errors.New("not signed by an accepted station")
	ErrRig         = errors.New("grant is for another rig")
	ErrDevice      = errors.New("grant names no device of this rig")
)

// Grant is a lease grant: the session token.
type Grant struct {
	ID        nostr.ID
	Station   nostr.PubKey
	Rig       nostr.PubKey
	Device    nostr.PubKey // zero when the device has no key of its own
	RigID     string
	DeviceID  string
	Subject   string
	LeaseID   string
	Seq       uint64
	Scopes    []station.Scope
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// HasScope says whether the grant allows s.
func (g Grant) HasScope(s station.Scope) bool { return station.HasScope(g.Scopes, s) }

func (g Grant) event() nostr.Event {
	tags := nostr.Tags{{"p", g.Rig.Hex()}}
	if g.Device != nostr.ZeroPK {
		tags = append(tags, nostr.Tag{"p", g.Device.Hex()})
	}
	tags = append(tags,
		nostr.Tag{"rig", g.RigID},
		nostr.Tag{"dev", g.DeviceID},
		nostr.Tag{"sub", g.Subject},
		nostr.Tag{"lease", g.LeaseID},
		nostr.Tag{"seq", strconv.FormatUint(g.Seq, 10)},
	)
	for _, s := range g.Scopes {
		tags = append(tags, nostr.Tag{"scope", string(s)})
	}
	tags = append(tags, nostr.Tag{"expiration", strconv.FormatInt(g.ExpiresAt.Unix(), 10)})
	return nostr.Event{Kind: KindGrant, CreatedAt: nostr.Timestamp(g.IssuedAt.Unix()), Tags: tags}
}

// SignGrant signs g with the station's key. IssuedAt defaults to now and
// ExpiresAt to IssuedAt plus GrantLifetime.
func SignGrant(sk nostr.SecretKey, g Grant) (nostr.Event, error) {
	if g.RigID == "" || g.DeviceID == "" || g.LeaseID == "" || g.Subject == "" || len(g.Scopes) == 0 || g.Rig == nostr.ZeroPK {
		return nostr.Event{}, fmt.Errorf("%w: rig, device, subject, lease and scopes are required", ErrMalformed)
	}
	if g.IssuedAt.IsZero() {
		g.IssuedAt = time.Now()
	}
	if g.ExpiresAt.IsZero() {
		g.ExpiresAt = g.IssuedAt.Add(GrantLifetime)
	}
	ev := g.event()
	if err := ev.Sign(sk); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// ParseGrant reads a grant out of ev, checking its shape, id and
// signature only. Who signed it, for which rig, and whether it is current
// are Verify's job.
func ParseGrant(ev nostr.Event) (Grant, error) {
	if ev.Kind != KindGrant {
		return Grant{}, ErrKind
	}
	if !ev.CheckID() || !ev.VerifySignature() {
		return Grant{}, ErrSignature
	}
	g := Grant{ID: ev.ID, Station: ev.PubKey, IssuedAt: ev.CreatedAt.Time()}
	var parties []nostr.PubKey
	for _, t := range ev.Tags {
		if len(t) < 2 {
			continue
		}
		switch t[0] {
		case "p":
			pk, err := nostr.PubKeyFromHex(t[1])
			if err != nil {
				return Grant{}, fmt.Errorf("%w: p tag: %v", ErrMalformed, err)
			}
			parties = append(parties, pk)
		case "rig":
			g.RigID = t[1]
		case "dev":
			g.DeviceID = t[1]
		case "sub":
			g.Subject = t[1]
		case "lease":
			g.LeaseID = t[1]
		case "seq":
			n, err := strconv.ParseUint(t[1], 10, 64)
			if err != nil {
				return Grant{}, fmt.Errorf("%w: seq %q", ErrMalformed, t[1])
			}
			g.Seq = n
		case "scope":
			s, err := station.ParseScope(t[1])
			if err != nil || g.HasScope(s) {
				return Grant{}, fmt.Errorf("%w: scope %q", ErrMalformed, t[1])
			}
			g.Scopes = append(g.Scopes, s)
		case "expiration":
			n, err := strconv.ParseInt(t[1], 10, 64)
			if err != nil {
				return Grant{}, fmt.Errorf("%w: expiration %q", ErrMalformed, t[1])
			}
			g.ExpiresAt = time.Unix(n, 0)
		}
	}
	if len(parties) == 0 || len(parties) > 2 {
		return Grant{}, fmt.Errorf("%w: a grant names the rig and at most the device", ErrMalformed)
	}
	g.Rig = parties[0]
	if len(parties) == 2 {
		g.Device = parties[1]
	}
	if g.RigID == "" || g.DeviceID == "" || g.LeaseID == "" || len(g.Scopes) == 0 || g.ExpiresAt.IsZero() {
		return Grant{}, fmt.Errorf("%w: rig, dev, lease, scope and expiration tags are required", ErrMalformed)
	}
	if _, _, ok := station.SplitDeviceID(g.DeviceID); !ok {
		return Grant{}, fmt.Errorf("%w: device id %q", ErrMalformed, g.DeviceID)
	}
	return g, nil
}

// Verifier is what the checking side knows about itself.
type Verifier struct {
	Stations []nostr.PubKey // keys whose grants are accepted
	Rig      nostr.PubKey   // this rig's key
	RigID    string         // the id the station gave this rig
	Devices  []string       // this rig's device ids
	Now      time.Time      // zero means time.Now()
}

// Verify parses ev and checks it against v: signed by an accepted station,
// for this rig and one of its devices, inside its lifetime with Skew. The
// lease order and replays are state the rig package keeps.
func Verify(ev nostr.Event, v Verifier) (Grant, error) {
	g, err := ParseGrant(ev)
	if err != nil {
		return Grant{}, err
	}
	if !nostr.ContainsPubKey(v.Stations, g.Station) {
		return Grant{}, ErrStation
	}
	if g.Rig != v.Rig || g.RigID != v.RigID {
		return Grant{}, ErrRig
	}
	found := false
	for _, d := range v.Devices {
		if d == g.DeviceID {
			found = true
		}
	}
	if !found {
		return Grant{}, ErrDevice
	}
	now := v.Now
	if now.IsZero() {
		now = time.Now()
	}
	if now.After(g.ExpiresAt.Add(Skew)) {
		return Grant{}, ErrExpired
	}
	if g.IssuedAt.After(now.Add(Skew)) {
		return Grant{}, ErrNotYetValid
	}
	return g, nil
}

// Revocation ends a lease before its grants expire.
type Revocation struct {
	ID       nostr.ID
	Station  nostr.PubKey
	Rig      nostr.PubKey
	LeaseID  string
	IssuedAt time.Time
}

// SignRevocation signs a revocation of the lease with the station's key.
func SignRevocation(sk nostr.SecretKey, r Revocation) (nostr.Event, error) {
	if r.LeaseID == "" || r.Rig == nostr.ZeroPK {
		return nostr.Event{}, fmt.Errorf("%w: rig and lease are required", ErrMalformed)
	}
	if r.IssuedAt.IsZero() {
		r.IssuedAt = time.Now()
	}
	ev := nostr.Event{Kind: KindRevocation, CreatedAt: nostr.Timestamp(r.IssuedAt.Unix()),
		Tags: nostr.Tags{{"p", r.Rig.Hex()}, {"lease", r.LeaseID}}}
	if err := ev.Sign(sk); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// ParseRevocation checks the shape and signature of a revocation. Whether
// the signer is accepted is the caller's check, as for grants.
func ParseRevocation(ev nostr.Event) (Revocation, error) {
	if ev.Kind != KindRevocation {
		return Revocation{}, ErrKind
	}
	if !ev.CheckID() || !ev.VerifySignature() {
		return Revocation{}, ErrSignature
	}
	r := Revocation{ID: ev.ID, Station: ev.PubKey, IssuedAt: ev.CreatedAt.Time()}
	for _, t := range ev.Tags {
		if len(t) < 2 {
			continue
		}
		switch t[0] {
		case "p":
			pk, err := nostr.PubKeyFromHex(t[1])
			if err != nil {
				return Revocation{}, fmt.Errorf("%w: p tag: %v", ErrMalformed, err)
			}
			r.Rig = pk
		case "lease":
			r.LeaseID = t[1]
		}
	}
	if r.Rig == nostr.ZeroPK || r.LeaseID == "" {
		return Revocation{}, fmt.Errorf("%w: p and lease tags are required", ErrMalformed)
	}
	return r, nil
}

// Scheme is the Authorization scheme a relayed request carries its grant
// with: "Authorization: Lease <base64url event JSON>".
const Scheme = "Lease"

// Header encodes ev for the Authorization header.
func Header(ev nostr.Event) string {
	data, _ := json.Marshal(ev)
	return Scheme + " " + base64.RawURLEncoding.EncodeToString(data)
}

// FromHeader decodes an Authorization header made by Header.
func FromHeader(h string) (nostr.Event, error) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, Scheme) {
		return nostr.Event{}, fmt.Errorf("%w: not a %s header", ErrMalformed, Scheme)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(rest))
	if err != nil {
		return nostr.Event{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	var ev nostr.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return nostr.Event{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return ev, nil
}
