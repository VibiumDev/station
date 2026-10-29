package hold

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

// The hold queue: who waits for a held device, in order. People recall
// a device from an agent; agents take a place in line and get the hold
// when it is released, or when the holder before them does not claim it.
// A reservation is a place in line for a set time, which sorts ahead
// when its time comes. The queue lives in memory: a station restart
// empties it, and a waiting agent simply asks again.

const (
	// PlaceTimeout drops a place in line nobody has asked about.
	PlaceTimeout = 5 * time.Minute
	// ClaimWindow is how long the next in line has to use a hold handed
	// to it before it passes on.
	ClaimWindow = time.Minute
)

// Queued is the error Take returns when another subject holds the device
// and the request waits in line instead of recalling it.
type Queued struct {
	Position int    // one-based place in line
	Holder   string // who holds the device now
}

func (q *Queued) Error() string {
	return fmt.Sprintf("held by %s, place %d in line", q.Holder, q.Position)
}

// ErrWatched says another subject already watches the hold: one observer
// at a time.
var ErrWatched = fmt.Errorf("someone is already watching this device")

type place struct {
	subject string
	scopes  []station.Scope
	since   time.Time
	seen    time.Time // the last time the subject asked
	at      time.Time // a reservation's time; zero for "as soon as it is free"
	ready   chan Result
}

type observer struct {
	subject string
	at      time.Time
}

func (m *Manager) init() {
	if m.queues == nil {
		m.queues = map[string][]*place{}
		m.observers = map[string]observer{}
		m.unclaimed = map[string]time.Time{}
		m.handed = map[string]*place{}
		m.handedHold = map[string]string{}
	}
}

// enqueue puts the subject in line for the device, or refreshes its
// place, and returns the position. m.mu is held.
func (m *Manager) enqueue(deviceID string, req Request, at time.Time, now time.Time) int {
	m.init()
	q := m.queues[deviceID]
	for i, p := range q {
		if p.subject == req.Subject {
			p.seen = now
			if !at.IsZero() {
				p.at = at
			}
			return i + 1
		}
	}
	q = append(q, &place{subject: req.Subject, scopes: req.Scopes, since: now, seen: now, at: at, ready: make(chan Result, 1)})
	m.queues[deviceID] = q
	return len(q)
}

// due is the first place whose turn it is: reservations whose time has
// come first, earliest first, then the line in order. Places still
// waiting for their time are skipped. m.mu is held.
func (m *Manager) due(deviceID string, now time.Time) *place {
	q := m.queues[deviceID]
	var best *place
	for _, p := range q {
		if !p.at.IsZero() {
			if p.at.After(now) {
				continue
			}
			if best == nil || best.at.IsZero() || p.at.Before(best.at) {
				best = p
			}
			continue
		}
		if best == nil {
			best = p
		}
	}
	return best
}

func (m *Manager) remove(deviceID string, p *place) {
	q := m.queues[deviceID]
	for i, x := range q {
		if x == p {
			m.queues[deviceID] = append(q[:i], q[i+1:]...)
			break
		}
	}
	if len(m.queues[deviceID]) == 0 {
		delete(m.queues, deviceID)
	}
}

// pending lists the devices with someone whose turn it is in line.
func (m *Manager) pending(now time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var devs []string
	for dev := range m.queues {
		if m.due(dev, now) != nil {
			devs = append(devs, dev)
		}
	}
	sort.Strings(devs)
	return devs
}

// Waiting is how many are in line for a device.
func (m *Manager) Waiting(deviceID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queues[deviceID])
}

// Position is the subject's place in line for the device, or zero.
func (m *Manager) Position(deviceID, subject string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.queues[deviceID] {
		if p.subject == subject {
			return i + 1
		}
	}
	return 0
}

// Leave takes the subject out of the line for the device.
func (m *Manager) Leave(deviceID, subject string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.queues[deviceID] {
		if p.subject == subject {
			m.remove(deviceID, p)
			return true
		}
	}
	return false
}

// Reserve puts the subject in line for the device for a set time; the
// place sorts ahead of the line once that time comes. The device must
// exist. Returns the position now.
func (m *Manager) Reserve(ctx context.Context, req Request, at time.Time) (int, error) {
	if _, err := m.Devices.Device(ctx, req.DeviceID); err != nil {
		return 0, err
	}
	now := m.now()
	if at.IsZero() {
		at = now
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pos := m.enqueue(req.DeviceID, req, at, now)
	m.audit(ctx, station.AuditEntry{Subject: req.Subject, Action: "reserve", DeviceID: req.DeviceID,
		Detail: fmt.Sprintf("for %s, place %d", at.Format(time.RFC3339), pos)})
	return pos, nil
}

// Wait blocks until the subject's place in line turns into a hold, or
// ctx ends. The result is the hold's first grant; a hold handed over
// before the call is returned at once.
func (m *Manager) Wait(ctx context.Context, deviceID, subject string) (Result, bool) {
	m.mu.Lock()
	m.init()
	p := m.handed[deviceID+"|"+subject]
	if p == nil {
		for _, x := range m.queues[deviceID] {
			if x.subject == subject {
				p = x
			}
		}
	}
	m.mu.Unlock()
	if p == nil {
		return Result{}, false
	}
	select {
	case res := <-p.ready:
		return res, true
	case <-ctx.Done():
		return Result{}, false
	}
}

// next hands a device that was just released to the first in line whose
// turn it is. The new hold must be claimed within ClaimWindow, by a
// renew or any use, or it passes on.
func (m *Manager) next(ctx context.Context, deviceID string) {
	for {
		now := m.now()
		m.mu.Lock()
		p := m.due(deviceID, now)
		if p != nil {
			m.remove(deviceID, p)
		}
		m.mu.Unlock()
		if p == nil {
			return
		}
		res, err := m.Take(ctx, Request{DeviceID: deviceID, Subject: p.subject, Scopes: p.scopes, Recall: true})
		if err != nil {
			continue // the next in line
		}
		m.mu.Lock()
		m.unclaimed[res.Hold.ID] = now.Add(m.claimWindow())
		m.handed[deviceID+"|"+p.subject] = p
		m.handedHold[res.Hold.ID] = deviceID + "|" + p.subject
		m.mu.Unlock()
		p.ready <- res
		return
	}
}

func (m *Manager) claimWindow() time.Duration {
	if m.ClaimWindow > 0 {
		return m.ClaimWindow
	}
	return ClaimWindow
}

// claim marks a hold as used by its holder, so it is no longer waiting
// to be claimed.
func (m *Manager) claim(holdID string) {
	m.mu.Lock()
	delete(m.unclaimed, holdID)
	if key, ok := m.handedHold[holdID]; ok {
		delete(m.handed, key)
		delete(m.handedHold, holdID)
	}
	m.mu.Unlock()
}

// watch admits an observer on a hold: one other subject at a time, for a
// grant's lifetime since it last asked.
func (m *Manager) watch(holdID, subject string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if o, ok := m.observers[holdID]; ok && o.subject != subject && now.Sub(o.at) < token.GrantLifetime {
		return ErrWatched
	}
	m.observers[holdID] = observer{subject: subject, at: now}
	return nil
}

// sweep drops places nobody asked about, forgets observers of ended
// holds, and returns the holds whose claim window passed.
func (m *Manager) sweep(now time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	for dev, q := range m.queues {
		kept := q[:0]
		for _, p := range q {
			if now.Sub(p.seen) < PlaceTimeout || (!p.at.IsZero() && p.at.After(now.Add(-PlaceTimeout))) {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			delete(m.queues, dev)
		} else {
			m.queues[dev] = kept
		}
	}
	for id, o := range m.observers {
		if now.Sub(o.at) > token.GrantLifetime {
			delete(m.observers, id)
		}
	}
	var passed []string
	for id, deadline := range m.unclaimed {
		if now.After(deadline) {
			passed = append(passed, id)
			delete(m.unclaimed, id)
			if key, ok := m.handedHold[id]; ok {
				delete(m.handed, key)
				delete(m.handedHold, id)
			}
		}
	}
	sort.Strings(passed)
	return passed
}
