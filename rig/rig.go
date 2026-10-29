package rig

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/token"
	"github.com/VibiumDev/station/tunnel"
)

// Rig runs the tunnels to every station in the policy and serves the rig's
// handlers over them behind the gate.
type Rig struct {
	Key     nostr.SecretKey
	Policy  *Policy
	Devices []enroll.Offer // what this rig offers, by name
	Handler http.Handler   // the rig's own mux
	Allow   Allowlist      // what the station may reach on it
	Gate    *Gate          // made by Run when nil
	// StatePath is where the gate keeps its hold state. Optional.
	StatePath string
	Logf      func(format string, args ...any)

	mu      sync.Mutex
	clients map[string]*tunnel.Client // by station npub
	online  map[string]string         // by station npub: the rig id, while connected
}

func (r *Rig) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// DeviceIDs lists this rig's device ids under every station's rig id.
func (r *Rig) DeviceIDs() []string {
	var ids []string
	for _, e := range r.Policy.Stations {
		for _, d := range r.Devices {
			ids = append(ids, station.DeviceID(e.RigID, d.Name))
		}
	}
	return ids
}

// Run keeps a tunnel to every enrolled station until ctx ends.
func (r *Rig) Run(ctx context.Context) {
	if r.Gate == nil {
		r.Gate = &Gate{Policy: r.Policy, Key: r.Key.Public(), StatePath: r.StatePath, Logf: r.Logf}
	}
	r.Gate.Devices = r.DeviceIDs
	handler := r.Gate.Middleware(r.Allow, r.Handler)
	r.mu.Lock()
	r.clients = map[string]*tunnel.Client{}
	r.online = map[string]string{}
	var wg sync.WaitGroup
	for _, e := range r.Policy.Stations {
		if e.TunnelURL == "" {
			continue
		}
		pk, err := e.PubKey()
		if err != nil {
			r.logf("rig: station %q: %v", e.Name, err)
			continue
		}
		entry := e
		c := &tunnel.Client{
			URL: e.TunnelURL, Key: r.Key, Station: pk, TLS: TLSConfig(e), Handler: handler, Logf: r.Logf,
			Announce: func() ([]nostr.Event, error) { return r.announce(entry, pk) },
			OnRevoke: func(ev nostr.Event) {
				if err := r.Gate.Revoke(ev); err != nil {
					r.logf("rig: revocation from %s: %v", entry.Name, err)
				}
			},
			OnConnect: func(rigID string, up bool) {
				r.mu.Lock()
				if up {
					r.online[entry.Npub] = rigID
				} else {
					delete(r.online, entry.Npub)
				}
				r.mu.Unlock()
			},
		}
		r.clients[e.Npub] = c
		wg.Add(1)
		go func() { defer wg.Done(); c.Run(ctx) }()
	}
	r.mu.Unlock()
	wg.Wait()
}

// Online lists the stations the rig is connected to, by name.
func (r *Rig) Online() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, e := range r.Policy.Stations {
		if _, ok := r.online[e.Npub]; ok {
			names = append(names, e.Name)
		}
	}
	return names
}

// Reannounce tells every station the devices changed.
func (r *Rig) Reannounce() {
	r.mu.Lock()
	clients := make([]*tunnel.Client, 0, len(r.clients))
	for _, c := range r.clients {
		clients = append(clients, c)
	}
	r.mu.Unlock()
	for _, c := range clients {
		c.Reannounce()
	}
}

// announce signs the rig announcement (kind 17700, the devices in its
// content) and one device announcement (kind 37700) per device.
func (r *Rig) announce(e *StationEntry, st nostr.PubKey) ([]nostr.Event, error) {
	var devices []station.Device
	for _, d := range r.Devices {
		devices = append(devices, station.Device{ID: station.DeviceID(e.RigID, d.Name), Kind: d.Kind, Caps: d.Caps, Key: d.Key, Shortcuts: d.Shortcuts, Input: d.Input})
	}
	content, _ := json.Marshal(devices)
	tags := nostr.Tags{{"p", st.Hex()}}
	for _, d := range devices {
		tags = append(tags, nostr.Tag{"dev", d.ID})
	}
	ev := nostr.Event{Kind: token.KindRigAnnounce, CreatedAt: nostr.Now(), Tags: tags, Content: string(content)}
	if err := ev.Sign(r.Key); err != nil {
		return nil, err
	}
	events := []nostr.Event{ev}
	for _, d := range devices {
		tags := nostr.Tags{{"d", d.ID}, {"p", st.Hex()}, {"kind", d.Kind}}
		if d.Key != "" {
			tags = append(tags, nostr.Tag{"p", d.Key})
		}
		for _, c := range d.Caps {
			tags = append(tags, nostr.Tag{"cap", c})
		}
		if d.Input != "" {
			tags = append(tags, nostr.Tag{"input", d.Input})
		}
		dev := nostr.Event{Kind: token.KindDeviceAnnounce, CreatedAt: ev.CreatedAt, Tags: tags}
		if err := dev.Sign(r.Key); err != nil {
			return nil, err
		}
		events = append(events, dev)
	}
	return events, nil
}
