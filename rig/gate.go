package rig

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

// Gate checks every grant a relayed request presents, with the state that
// makes the rig the final gate: the current hold per device, the highest
// sequence seen per station, grants already used, and holds revoked.
type Gate struct {
	Policy  *Policy
	Key     nostr.PubKey    // the rig's key
	Devices func() []string // this rig's device ids, under every station's rig id
	// StatePath persists the hold state, so a restart cannot be made to
	// accept an older hold. Optional.
	StatePath string
	// OnEnd is told when a device's hold ends: a newer hold arrived or a
	// revocation did. The rig ends that hold's sessions. Optional.
	OnEnd func(deviceID, holdID string)
	Now   func() time.Time
	Logf  func(format string, args ...any)

	mu     sync.Mutex
	loaded bool
	st     state
	seen   map[nostr.ID]time.Time
}

// Errors the gate returns, besides the token package's.
var (
	ErrOlderHold     = errors.New("an older hold than the one this device has")
	ErrReplay        = errors.New("grant already used")
	ErrRevoked       = errors.New("hold revoked")
	ErrScope         = errors.New("grant lacks the scope this needs")
	ErrPolicy        = errors.New("the rig policy does not allow this station that scope")
	ErrBusy          = errors.New("another station's hold holds this device")
	ErrHours         = errors.New("outside the hours the rig policy allows")
	ErrSessionLength = errors.New("the hold is longer than the rig policy allows")
)

type holdState struct {
	Station string    `json:"station"` // hex key of the station that granted it
	Hold    string    `json:"hold"`
	Seq     uint64    `json:"seq"`
	Since   time.Time `json:"since"` // the first grant seen
	Until   time.Time `json:"until"` // the latest grant's end
}

type state struct {
	Devices map[string]*holdState `json:"devices"` // by device id
	Seqs    map[string]uint64     `json:"seqs"`    // by device id + "|" + station hex: the highest seq seen
	Revoked map[string]time.Time  `json:"revoked"` // hold id: when
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gate) logf(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

func (g *Gate) load() {
	if g.loaded {
		return
	}
	g.loaded = true
	g.seen = map[nostr.ID]time.Time{}
	g.st = state{Devices: map[string]*holdState{}, Seqs: map[string]uint64{}, Revoked: map[string]time.Time{}}
	if g.StatePath == "" {
		return
	}
	data, err := os.ReadFile(g.StatePath)
	if err != nil {
		return
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		g.logf("rig: %s: %v (starting over)", g.StatePath, err)
		return
	}
	if st.Devices != nil {
		g.st.Devices = st.Devices
	}
	if st.Seqs != nil {
		g.st.Seqs = st.Seqs
	}
	if st.Revoked != nil {
		g.st.Revoked = st.Revoked
	}
}

func (g *Gate) save() {
	if g.StatePath == "" {
		return
	}
	data, err := json.MarshalIndent(g.st, "", "  ")
	if err != nil {
		return
	}
	if err := writeFile(g.StatePath, data, 0o600); err != nil {
		g.logf("rig: %s: %v", g.StatePath, err)
	}
}

// Check verifies a grant and that it allows need, and takes it into the
// hold state. On success the grant is spent: presenting it again is a
// replay.
func (g *Gate) Check(ev nostr.Event, need station.Scope) (token.Grant, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.load()
	now := g.now()
	parsed, err := token.ParseGrant(ev)
	if err != nil {
		return token.Grant{}, err
	}
	entry := g.Policy.Station(parsed.Station)
	if entry == nil {
		return token.Grant{}, token.ErrStation
	}
	grant, err := token.Verify(ev, token.Verifier{Stations: []nostr.PubKey{parsed.Station}, Rig: g.Key, RigID: entry.RigID, Devices: g.Devices(), Now: now})
	if err != nil {
		return token.Grant{}, err
	}
	for _, s := range grant.Scopes {
		if !entry.Allows(s) {
			return token.Grant{}, fmt.Errorf("%w: %s", ErrPolicy, s)
		}
	}
	if !entry.InHours(now) {
		return token.Grant{}, ErrHours
	}
	if need != "" && !grant.HasScope(need) {
		return token.Grant{}, fmt.Errorf("%w: %s", ErrScope, need)
	}
	if _, ok := g.seen[ev.ID]; ok {
		return token.Grant{}, ErrReplay
	}
	if _, ok := g.st.Revoked[grant.HoldID]; ok {
		return token.Grant{}, ErrRevoked
	}
	key := grant.DeviceID + "|" + grant.Station.Hex()
	if grant.Seq < g.st.Seqs[key] {
		return token.Grant{}, ErrOlderHold
	}
	cur := g.st.Devices[grant.DeviceID]
	ended := ""
	switch {
	case cur == nil:
		cur = &holdState{Station: grant.Station.Hex(), Hold: grant.HoldID, Seq: grant.Seq, Since: now}
	case cur.Hold == grant.HoldID:
		// The hold it has, refreshed.
	case cur.Station != grant.Station.Hex() && now.Before(cur.Until):
		return token.Grant{}, ErrBusy
	case cur.Station == grant.Station.Hex() && grant.Seq <= cur.Seq:
		return token.Grant{}, ErrOlderHold
	default:
		// A newer hold from the same station, or another station's once
		// the old hold ran out.
		if now.Before(cur.Until) {
			ended = cur.Hold
		}
		cur = &holdState{Station: grant.Station.Hex(), Hold: grant.HoldID, Seq: grant.Seq, Since: now}
	}
	if now.Sub(cur.Since) > entry.MaxSessionDuration() {
		return token.Grant{}, ErrSessionLength
	}
	if grant.ExpiresAt.After(cur.Until) {
		cur.Until = grant.ExpiresAt
	}
	g.st.Devices[grant.DeviceID] = cur
	g.st.Seqs[key] = grant.Seq
	g.seen[ev.ID] = grant.ExpiresAt
	for id, until := range g.seen {
		if now.After(until.Add(token.Skew)) {
			delete(g.seen, id)
		}
	}
	for id, at := range g.st.Revoked {
		if now.Sub(at) > 24*time.Hour {
			delete(g.st.Revoked, id)
		}
	}
	g.save()
	if ended != "" && g.OnEnd != nil {
		go g.OnEnd(grant.DeviceID, ended)
	}
	return grant, nil
}

// Revoke takes a revocation from a station in the policy and ends the
// hold it names.
func (g *Gate) Revoke(ev nostr.Event) error {
	r, err := token.ParseRevocation(ev)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.load()
	if g.Policy.Station(r.Station) == nil {
		return token.ErrStation
	}
	g.st.Revoked[r.HoldID] = g.now()
	for device, cur := range g.st.Devices {
		if cur.Hold == r.HoldID && cur.Station == r.Station.Hex() {
			delete(g.st.Devices, device)
			if g.OnEnd != nil {
				go g.OnEnd(device, r.HoldID)
			}
		}
	}
	g.save()
	return nil
}

// Current says which hold holds a device, if one does.
func (g *Gate) Current(deviceID string) (holdID string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.load()
	cur := g.st.Devices[deviceID]
	if cur == nil || !g.now().Before(cur.Until) {
		return "", false
	}
	return cur.Hold, true
}
