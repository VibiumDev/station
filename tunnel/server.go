package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

// Server accepts rigs' tunnels and keeps the ones that are up.
type Server struct {
	Key     nostr.PubKey        // the station's public key, named in the challenge and the hello
	Devices station.DeviceStore // rigs are looked up by key; announcements update their devices
	Events  station.EventStore  // announcements are stored verbatim; optional
	Logf    func(format string, args ...any)

	// OnOnline is told when a rig's tunnel comes up or goes down. Optional.
	OnOnline func(rigID string, online bool)

	mu   sync.Mutex
	rigs map[string]*conn
}

// conn is one rig's live tunnel.
type conn struct {
	rigID   string
	key     nostr.PubKey
	session *yamux.Session
	ws      *websocket.Conn

	cmu     sync.Mutex // the control stream's writer
	control net.Conn
	done    chan struct{}

	record bool // the rig records every hold from this station, by its last announcement
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Online lists the rig ids with a tunnel up.
func (s *Server) Online() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.rigs {
		ids = append(ids, id)
	}
	return ids
}

// IsOnline says whether a rig's tunnel is up.
func (s *Server) IsOnline(rigID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.rigs[rigID]
	return ok
}

// RecordsHolds says whether a rig with a tunnel up has announced that it
// records every hold from this station and uploads it to the Record
// Store, which the pages tell people holding its devices.
func (s *Server) RecordsHolds(rigID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.rigs[rigID]
	return ok && c.record
}

// ErrOffline says the rig has no tunnel up.
var ErrOffline = errors.New("rig is offline")

// Dial opens a stream to the rig's HTTP handlers.
func (s *Server) Dial(ctx context.Context, rigID string) (net.Conn, error) {
	s.mu.Lock()
	c := s.rigs[rigID]
	s.mu.Unlock()
	if c == nil {
		return nil, ErrOffline
	}
	return c.session.Open()
}

// Transport is an HTTP transport that reaches the rig over its tunnel,
// for reverse proxies and clients. The request's host is ignored.
func (s *Server) Transport(rigID string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return s.Dial(ctx, rigID) },
		// Streams are cheap and a rig serves few requests: no pooling, so a
		// closed tunnel never leaves stale connections behind.
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// Send writes a control message to the rig.
func (s *Server) send(rigID string, m message) error {
	s.mu.Lock()
	c := s.rigs[rigID]
	s.mu.Unlock()
	if c == nil {
		return ErrOffline
	}
	return c.write(m)
}

func (c *conn) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.cmu.Lock()
	defer c.cmu.Unlock()
	c.control.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.control.Write(append(data, '\n'))
	return err
}

// Revoke pushes a signed revocation to the rig.
func (s *Server) Revoke(rigID string, ev nostr.Event) error {
	return s.send(rigID, message{Type: typeRevoke, Event: &ev})
}

// ServeHTTP is the /v1/tunnel endpoint.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Rigs are not browsers: no Origin to check.
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	ctx := r.Context()

	// Challenge and hello.
	nonce := newNonce()
	if err := writeJSON(ctx, ws, message{Type: typeChallenge, Nonce: nonce, Station: s.Key.Hex()}); err != nil {
		return
	}
	var hello message
	if err := readJSON(ctx, ws, &hello); err != nil {
		return
	}
	if hello.Type != typeHello || hello.Event == nil {
		writeJSON(ctx, ws, message{Type: typeError, Error: "expected a hello"})
		return
	}
	key, err := checkHello(*hello.Event, s.Key, nonce, time.Now())
	if err != nil {
		s.logf("tunnel: %s: %v", r.RemoteAddr, err)
		writeJSON(ctx, ws, message{Type: typeError, Error: err.Error()})
		return
	}
	rig, err := s.Devices.RigByKey(ctx, key)
	if err != nil {
		s.logf("tunnel: %s: %v", r.RemoteAddr, ErrUnknownRig)
		writeJSON(ctx, ws, message{Type: typeError, Error: ErrUnknownRig.Error()})
		return
	}
	if err := writeJSON(ctx, ws, message{Type: typeWelcome, Rig: rig.ID}); err != nil {
		return
	}

	// From here on, yamux.
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 15 * time.Second
	nc := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	session, err := yamux.Server(nc, cfg)
	if err != nil {
		return
	}
	defer session.Close()
	// The rig opens the control stream first, within a few seconds.
	timer := time.AfterFunc(15*time.Second, func() { session.Close() })
	control, err := session.Accept()
	timer.Stop()
	if err != nil {
		return
	}
	c := &conn{rigID: rig.ID, key: key, session: session, ws: ws, control: control, done: make(chan struct{})}

	s.mu.Lock()
	if s.rigs == nil {
		s.rigs = map[string]*conn{}
	}
	if old := s.rigs[rig.ID]; old != nil {
		// A rig reconnecting before its old tunnel timed out: the new one wins.
		old.session.Close()
	}
	s.rigs[rig.ID] = c
	s.mu.Unlock()
	s.logf("tunnel: %s (%s) connected from %s", rig.ID, rig.Name, r.RemoteAddr)
	if s.OnOnline != nil {
		s.OnOnline(rig.ID, true)
	}
	defer func() {
		s.mu.Lock()
		if s.rigs[rig.ID] == c {
			delete(s.rigs, rig.ID)
		}
		s.mu.Unlock()
		close(c.done)
		s.logf("tunnel: %s disconnected", rig.ID)
		if s.OnOnline != nil {
			s.OnOnline(rig.ID, false)
		}
	}()

	// Control messages from the rig until the stream or the session ends.
	sc := bufio.NewScanner(control)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch m.Type {
		case typeAnnounce:
			if err := s.announce(ctx, c, m.Events); err != nil {
				s.logf("tunnel: %s: announce: %v", rig.ID, err)
			}
		case typePing:
			c.write(message{Type: typePong})
		}
	}
}

// announce takes a rig's announcement events: each must be signed by the
// rig, and the rig announcement's content lists the devices.
func (s *Server) announce(ctx context.Context, c *conn, events []nostr.Event) error {
	var devices []station.Device
	found := false
	for _, ev := range events {
		if ev.PubKey != c.key || !ev.CheckID() || !ev.VerifySignature() {
			return fmt.Errorf("event %s not signed by the rig", ev.ID)
		}
		switch ev.Kind {
		case token.KindRigAnnounce:
			if err := json.Unmarshal([]byte(ev.Content), &devices); err != nil {
				return fmt.Errorf("devices: %w", err)
			}
			for i, d := range devices {
				rigID, _, ok := station.SplitDeviceID(d.ID)
				if !ok || rigID != c.rigID {
					return fmt.Errorf("device %d: id %q is not under %s", i, d.ID, c.rigID)
				}
			}
			record := ev.Tags.Find("record") != nil
			s.mu.Lock()
			c.record = record
			s.mu.Unlock()
			found = true
		case token.KindDeviceAnnounce:
		default:
			return fmt.Errorf("event %s: kind %d is not an announcement", ev.ID, ev.Kind)
		}
	}
	if !found {
		return errors.New("no rig announcement")
	}
	if err := s.Devices.SetDevices(ctx, c.rigID, devices); err != nil {
		return err
	}
	if s.Events != nil {
		for _, ev := range events {
			if _, err := s.Events.ReplaceEvent(ev); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeJSON(ctx context.Context, ws *websocket.Conn, m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, data)
}

func readJSON(ctx context.Context, ws *websocket.Conn, m *message) error {
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, data, err := ws.Read(rctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, m)
}
