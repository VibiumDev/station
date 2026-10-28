// Package lease hands out control of devices: one lease per device,
// numbered so the rig can always tell the newer one, with takeover by
// input scope, observers on the holder's lease, and release after idle or
// maximum time. Every grant and revocation it signs is stored verbatim.
package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

const (
	// IdleTimeout releases a lease nobody refreshed or used.
	IdleTimeout = 5 * time.Minute
	// MaxDuration is the hard end of a lease.
	MaxDuration = 8 * time.Hour
)

// Manager issues leases and grants.
type Manager struct {
	Leases  station.LeaseStore
	Devices station.DeviceStore
	Events  station.EventStore
	Audit   station.AuditSink // optional
	Key     nostr.SecretKey   // the station's signing key

	// OnRevoke is told about every signed revocation, to push it to the
	// rig over the tunnel. Optional.
	OnRevoke func(ctx context.Context, rigID string, ev nostr.Event)

	Now         func() time.Time // optional, for tests
	IdleTimeout time.Duration    // zero means IdleTimeout
	MaxDuration time.Duration    // zero means MaxDuration
}

// Request asks for a device.
type Request struct {
	DeviceID string
	Subject  string
	Scopes   []station.Scope // empty means view only; view is always included
}

// Result is what a request got: the lease the grant is on, the grant, and
// whether the requester only observes someone else's lease.
type Result struct {
	Lease    station.Lease
	Observer bool
	Grant    token.Grant
	Event    nostr.Event
}

// ErrNotHolder says the subject does not hold the lease.
var ErrNotHolder = errors.New("not the holder of this lease")

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) idle() time.Duration {
	if m.IdleTimeout > 0 {
		return m.IdleTimeout
	}
	return IdleTimeout
}

func (m *Manager) max() time.Duration {
	if m.MaxDuration > 0 {
		return m.MaxDuration
	}
	return MaxDuration
}

func (m *Manager) audit(ctx context.Context, e station.AuditEntry) {
	if m.Audit != nil {
		e.At = m.now()
		m.Audit.Audit(ctx, e)
	}
}

// controls says whether scopes include control of the device.
func controls(scopes []station.Scope) bool {
	return station.HasScope(scopes, station.ScopeInput) || station.HasScope(scopes, station.ScopeBidi) || station.HasScope(scopes, station.ScopeAdmin)
}

func subset(scopes, of []station.Scope) bool {
	for _, s := range scopes {
		if !station.HasScope(of, s) {
			return false
		}
	}
	return true
}

// Take gives the subject the device, or a seat watching whoever has it.
//
// With no lease on the device, a new lease. With the subject's own lease,
// a fresh grant on it (and a new lease when more scopes are asked for).
// With someone else's lease: a request with input, bidi or admin takes
// over with a new lease, which ends the old one on the rig; a view-only
// request gets a grant on the holder's lease as an observer.
func (m *Manager) Take(ctx context.Context, req Request) (Result, error) {
	dev, err := m.Devices.Device(ctx, req.DeviceID)
	if err != nil {
		return Result{}, err
	}
	rigID, _, _ := station.SplitDeviceID(dev.ID)
	rig, err := m.Devices.Rig(ctx, rigID)
	if err != nil {
		return Result{}, err
	}
	scopes := append([]station.Scope(nil), req.Scopes...)
	if !station.HasScope(scopes, station.ScopeView) {
		scopes = append([]station.Scope{station.ScopeView}, scopes...)
	}
	now := m.now()
	cur, err := m.Leases.Current(ctx, dev.ID)
	switch {
	case errors.Is(err, station.ErrNoLease):
		// Free: a new lease.
	case err != nil:
		return Result{}, err
	case cur.Subject == req.Subject && subset(scopes, cur.Scopes):
		if err := m.Leases.Touch(ctx, cur.ID, now); err != nil {
			return Result{}, err
		}
		cur.ActiveAt = now
		return m.issue(ctx, rig, dev, cur, scopes, false)
	case cur.Subject == req.Subject:
		// The holder wants more than the lease has: a new lease.
		if err := m.end(ctx, cur, "replaced by its holder"); err != nil {
			return Result{}, err
		}
	case controls(scopes):
		if err := m.end(ctx, cur, "taken over by "+req.Subject); err != nil {
			return Result{}, err
		}
	default:
		// Someone else holds it and the request only watches.
		return m.issue(ctx, rig, dev, cur, []station.Scope{station.ScopeView}, true)
	}
	lease, err := m.Leases.Create(ctx, station.Lease{
		ID: station.NewID("lse"), DeviceID: dev.ID, RigID: rig.ID, Subject: req.Subject, Scopes: scopes,
		CreatedAt: now, ActiveAt: now, ExpiresAt: now.Add(m.max()),
	})
	if err != nil {
		return Result{}, err
	}
	m.audit(ctx, station.AuditEntry{Subject: req.Subject, Action: "lease", DeviceID: dev.ID, LeaseID: lease.ID,
		Detail: fmt.Sprintf("seq %d, scopes %v", lease.Seq, station.ScopeStrings(scopes))})
	return m.issue(ctx, rig, dev, lease, scopes, false)
}

// issue signs a grant on the lease and stores it.
func (m *Manager) issue(ctx context.Context, rig station.Rig, dev station.Device, lease station.Lease, scopes []station.Scope, observer bool) (Result, error) {
	now := m.now()
	g := token.Grant{
		Rig: rig.PubKey, RigID: rig.ID, DeviceID: dev.ID, Subject: lease.Subject, LeaseID: lease.ID, Seq: lease.Seq,
		Scopes: scopes, IssuedAt: now, ExpiresAt: now.Add(token.GrantLifetime),
	}
	if g.ExpiresAt.After(lease.ExpiresAt) {
		g.ExpiresAt = lease.ExpiresAt
	}
	if dev.Key != "" {
		pk, err := nostr.PubKeyFromHex(dev.Key)
		if err != nil {
			return Result{}, fmt.Errorf("device key: %w", err)
		}
		g.Device = pk
	}
	ev, err := token.SignGrant(m.Key, g)
	if err != nil {
		return Result{}, err
	}
	if err := m.Events.SaveEvent(ev); err != nil {
		return Result{}, err
	}
	g.ID, g.Station = ev.ID, ev.PubKey
	return Result{Lease: lease, Observer: observer, Grant: g, Event: ev}, nil
}

// Touch records activity on a lease, so it is not released as idle.
func (m *Manager) Touch(ctx context.Context, leaseID string) error {
	return m.Leases.Touch(ctx, leaseID, m.now())
}

// Release ends the subject's lease. Only its holder may, so an observer
// cannot end the controller's session.
func (m *Manager) Release(ctx context.Context, leaseID, subject string) error {
	lease, err := m.Leases.Lease(ctx, leaseID)
	if err != nil {
		return err
	}
	if lease.Subject != subject {
		return ErrNotHolder
	}
	if !lease.Held(m.now()) {
		return nil
	}
	return m.end(ctx, lease, "released")
}

// Expire ends every lease past its idle or maximum time and returns them.
func (m *Manager) Expire(ctx context.Context) ([]station.Lease, error) {
	held, err := m.Leases.Held(ctx)
	if err != nil {
		return nil, err
	}
	now := m.now()
	var ended []station.Lease
	for _, l := range held {
		var why string
		switch {
		case !now.Before(l.ExpiresAt):
			why = "maximum time"
		case now.Sub(l.ActiveAt) >= m.idle():
			why = "idle"
		default:
			continue
		}
		if err := m.end(ctx, l, why); err != nil {
			return ended, err
		}
		ended = append(ended, l)
	}
	return ended, nil
}

// end releases a lease and signs, stores and pushes its revocation.
func (m *Manager) end(ctx context.Context, lease station.Lease, why string) error {
	if err := m.Leases.Release(ctx, lease.ID, m.now()); err != nil && !errors.Is(err, station.ErrNotFound) {
		return err
	}
	rig, err := m.Devices.Rig(ctx, lease.RigID)
	if err != nil {
		return err
	}
	ev, err := token.SignRevocation(m.Key, token.Revocation{Rig: rig.PubKey, LeaseID: lease.ID, IssuedAt: m.now()})
	if err != nil {
		return err
	}
	if err := m.Events.SaveEvent(ev); err != nil {
		return err
	}
	m.audit(ctx, station.AuditEntry{Subject: lease.Subject, Action: "release", DeviceID: lease.DeviceID, LeaseID: lease.ID, Detail: why})
	if m.OnRevoke != nil {
		m.OnRevoke(ctx, lease.RigID, ev)
	}
	return nil
}
