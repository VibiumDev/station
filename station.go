// Package station is the model Vibium Station's packages share: rigs,
// devices, leases, principals and scopes, and the five interfaces an
// embedding application implements. Station's code never names a kind of
// device; what a rig offers is data.
package station

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
)

// EventStore keeps every event the station signs or accepts, verbatim. It
// is khatru's event-store interface, so the same store can back a relay
// later without a migration.
type EventStore = eventstore.Store

// Scope is what a lease grant allows on a device.
type Scope string

const (
	ScopeView  Scope = "view"  // video, speaker audio, screenshots, MJPEG, status
	ScopeInput Scope = "input" // HID input and the microphone
	ScopeBidi  Scope = "bidi"  // WebDriver BiDi
	ScopeAdmin Scope = "admin" // settings, resets, logs; only from a station the rig policy trusts with it
)

// AllScopes lists the scopes in their canonical order.
var AllScopes = []Scope{ScopeView, ScopeInput, ScopeBidi, ScopeAdmin}

// ParseScope checks that s names a scope.
func ParseScope(s string) (Scope, error) {
	for _, k := range AllScopes {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown scope %q", s)
}

// ParseScopes parses a list, refusing unknown and repeated scopes.
func ParseScopes(list []string) ([]Scope, error) {
	var out []Scope
	for _, s := range list {
		k, err := ParseScope(s)
		if err != nil {
			return nil, err
		}
		if HasScope(out, k) {
			return nil, fmt.Errorf("scope %q repeated", s)
		}
		out = append(out, k)
	}
	return out, nil
}

// HasScope says whether scopes contains s.
func HasScope(scopes []Scope, s Scope) bool {
	for _, k := range scopes {
		if k == s {
			return true
		}
	}
	return false
}

// ScopeStrings is the list as strings, for tags and JSON.
func ScopeStrings(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// Capabilities a device can announce.
const (
	CapVideo = "video"
	CapAudio = "audio"
	CapHID   = "hid"
	CapBidi  = "bidi"
	CapMJPEG = "mjpeg"
)

// Shortcut is a toolbar button the device's provider asks the shared
// viewer to show: a label and the key chord to send, in the viewer's key
// syntax ("GUI+h").
type Shortcut struct {
	Label string `json:"label"`
	Keys  string `json:"keys"`
}

// Device is one thing a rig offers.
type Device struct {
	ID        string     `json:"id"`            // "<rig id>/<name>"
	Kind      string     `json:"kind"`          // "phone", "browser", ...
	Caps      []string   `json:"caps"`          // see the Cap constants
	Key       string     `json:"key,omitempty"` // hex public key, when the device has its own
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
	// Input is how the device is best driven, "touch" or "mouse": the
	// viewer's pointer mode until the person picks one. Empty leaves it
	// to the viewer.
	Input string `json:"input,omitempty"`
}

// Name is the device's name inside its rig.
func (d Device) Name() string {
	_, name, _ := SplitDeviceID(d.ID)
	return name
}

// Has says whether the device announced a capability.
func (d Device) Has(cap string) bool {
	for _, c := range d.Caps {
		if c == cap {
			return true
		}
	}
	return false
}

// DeviceID joins a rig id and a device name.
func DeviceID(rigID, name string) string { return rigID + "/" + name }

// SplitDeviceID takes a device id apart.
func SplitDeviceID(id string) (rigID, name string, ok bool) {
	rigID, name, ok = strings.Cut(id, "/")
	if !ok || rigID == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return rigID, name, true
}

// Rig is an enrolled machine: one key, one tunnel, one or more devices.
type Rig struct {
	ID         string
	Name       string
	PubKey     nostr.PubKey
	EnrolledAt time.Time
	Devices    []Device
}

// Principal is who a request comes from, as the Authenticator saw it.
type Principal struct {
	Subject  string // "owner", "token:<name>", or a login such as you@example.com
	ReadOnly bool   // may view, never control
	Admin    bool   // may enroll and remove rigs, make codes and tokens
}

// Lease is control of one device by one subject, numbered per device so
// a newer lease always wins on the rig.
type Lease struct {
	ID         string
	DeviceID   string
	RigID      string
	Seq        uint64
	Subject    string
	Scopes     []Scope
	CreatedAt  time.Time
	ActiveAt   time.Time // the last refresh or input
	ExpiresAt  time.Time // the hard end
	ReleasedAt time.Time // zero while held
}

// Held says whether the lease is still in force at t.
func (l Lease) Held(t time.Time) bool {
	return l.ReleasedAt.IsZero() && t.Before(l.ExpiresAt)
}

// Action is what a principal wants to do, for the Authorizer.
type Action string

const (
	ActionView    Action = "view"    // open the viewer, screenshots, status
	ActionControl Action = "control" // take a lease with input
	ActionBidi    Action = "bidi"    // drive the device with an agent
	ActionAdmin   Action = "admin"   // the device's admin endpoints
	ActionManage  Action = "manage"  // enroll and remove rigs, codes, tokens
)

// Errors the interfaces return.
var (
	ErrUnauthenticated = errors.New("not signed in")
	ErrForbidden       = errors.New("not allowed")
	ErrNotFound        = errors.New("not found")
	ErrNoLease         = errors.New("no lease")
)

// Authenticator says who a request comes from.
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

// Authorizer says whether a principal may do an action to a device
// (deviceID is empty for ActionManage).
type Authorizer interface {
	Authorize(ctx context.Context, p Principal, action Action, deviceID string) error
}

// DeviceStore holds rigs and their devices.
type DeviceStore interface {
	Rigs(ctx context.Context) ([]Rig, error)
	Rig(ctx context.Context, id string) (Rig, error)
	RigByKey(ctx context.Context, pk nostr.PubKey) (Rig, error)
	PutRig(ctx context.Context, rig Rig) error
	RemoveRig(ctx context.Context, id string) error
	// SetDevices replaces a rig's devices with what it last announced.
	SetDevices(ctx context.Context, rigID string, devices []Device) error
	Device(ctx context.Context, id string) (Device, error)
}

// LeaseStore holds leases and each device's sequence counter.
type LeaseStore interface {
	// Current is the lease held on a device, or ErrNoLease.
	Current(ctx context.Context, deviceID string) (Lease, error)
	// Create stores a new lease with the device's next sequence number and
	// returns it with Seq set.
	Create(ctx context.Context, lease Lease) (Lease, error)
	Lease(ctx context.Context, id string) (Lease, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Release(ctx context.Context, id string, at time.Time) error
	// Held lists every lease still held.
	Held(ctx context.Context) ([]Lease, error)
}

// AuditEntry is one thing that happened.
type AuditEntry struct {
	At       time.Time
	Subject  string
	Action   string
	DeviceID string
	LeaseID  string
	Detail   string
}

// AuditSink receives audit entries.
type AuditSink interface {
	Audit(ctx context.Context, e AuditEntry)
}

// NewID makes a random id such as "rig_3f9a1c02".
func NewID(prefix string) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
