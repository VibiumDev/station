package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
)

type harness struct {
	db         *sqlite.DB
	server     *Server
	ts         *httptest.Server
	stationKey nostr.SecretKey
	rigKey     nostr.SecretKey
	online     chan bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	h := &harness{db: db, stationKey: nostr.Generate(), rigKey: nostr.Generate(), online: make(chan bool, 16)}
	if err := db.PutRig(context.Background(), station.Rig{ID: "rig_1", Name: "pi", PubKey: h.rigKey.Public(), EnrolledAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	h.server = &Server{Key: h.stationKey.Public(), Devices: db, Events: db, Logf: t.Logf,
		OnOnline: func(_ string, up bool) { h.online <- up }}
	mux := http.NewServeMux()
	mux.Handle("/v1/tunnel", h.server)
	h.ts = httptest.NewTLSServer(mux)
	t.Cleanup(h.ts.Close)
	return h
}

func (h *harness) url() string { return "wss" + strings.TrimPrefix(h.ts.URL, "https") + "/v1/tunnel" }

func (h *harness) client(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return &Client{URL: h.url(), Key: h.rigKey, Station: h.stationKey.Public(),
		TLS: &tls.Config{InsecureSkipVerify: true}, Handler: handler, Logf: t.Logf,
		Announce: func() ([]nostr.Event, error) {
			devices, _ := json.Marshal([]station.Device{{ID: "rig_1/phone", Kind: "phone", Caps: []string{"video", "hid"}}})
			ev := nostr.Event{Kind: token.KindRigAnnounce, CreatedAt: nostr.Now(),
				Tags: nostr.Tags{{"p", h.stationKey.Public().Hex()}, {"name", "pi"}}, Content: string(devices)}
			if err := ev.Sign(h.rigKey); err != nil {
				return nil, err
			}
			dev := nostr.Event{Kind: token.KindDeviceAnnounce, CreatedAt: nostr.Now(),
				Tags: nostr.Tags{{"d", "rig_1/phone"}, {"p", h.stationKey.Public().Hex()}, {"kind", "phone"}}}
			if err := dev.Sign(h.rigKey); err != nil {
				return nil, err
			}
			return []nostr.Event{ev, dev}, nil
		}}
}

func waitOnline(t *testing.T, h *harness, want bool) {
	t.Helper()
	select {
	case up := <-h.online:
		if up != want {
			t.Fatalf("online = %v, want %v", up, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no online=%v event", want)
	}
}

func TestTunnel(t *testing.T) {
	h := newHarness(t)
	var hits int
	var mu sync.Mutex
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.Path, body)
	})
	c := h.client(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitOnline(t, h, true)
	if !h.server.IsOnline("rig_1") || h.server.Online()[0] != "rig_1" {
		t.Fatal("rig not listed online")
	}

	// The announcement reached the device store and the event store.
	deadline := time.Now().Add(5 * time.Second)
	for {
		dev, err := h.db.Device(ctx, "rig_1/phone")
		if err == nil && dev.Kind == "phone" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("device not announced: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n, _ := h.db.CountEvents(nostr.Filter{Kinds: []nostr.Kind{token.KindRigAnnounce, token.KindDeviceAnnounce}}); n != 2 {
		t.Errorf("stored announcements: %d", n)
	}

	// HTTP over the tunnel, twice, with a body.
	hc := &http.Client{Transport: h.server.Transport("rig_1")}
	for i := 0; i < 2; i++ {
		resp, err := hc.Post("http://rig/api/thing", "text/plain", strings.NewReader("hello"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "POST /api/thing hello" {
			t.Errorf("got %q", body)
		}
	}
	mu.Lock()
	if hits != 2 {
		t.Errorf("hits %d", hits)
	}
	mu.Unlock()

	// A revocation reaches the rig.
	got := make(chan nostr.Event, 1)
	c.OnRevoke = func(ev nostr.Event) { got <- ev }
	rev, _ := token.SignRevocation(h.stationKey, token.Revocation{Rig: h.rigKey.Public(), HoldID: "hld_1"})
	if err := h.server.Revoke("rig_1", rev); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-got:
		if ev.ID != rev.ID {
			t.Errorf("wrong revocation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation not delivered")
	}

	// Reannounce with a changed device list.
	c.Announce = func() ([]nostr.Event, error) {
		devices, _ := json.Marshal([]station.Device{{ID: "rig_1/phone", Kind: "phone", Caps: []string{"video"}}, {ID: "rig_1/tablet", Kind: "phone"}})
		ev := nostr.Event{Kind: token.KindRigAnnounce, CreatedAt: nostr.Now() + 1, Tags: nostr.Tags{{"p", h.stationKey.Public().Hex()}}, Content: string(devices)}
		ev.Sign(h.rigKey)
		return []nostr.Event{ev}, nil
	}
	if err := c.Reannounce(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		rig, _ := h.db.Rig(ctx, "rig_1")
		if len(rig.Devices) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("devices not updated: %+v", rig.Devices)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The station dropping the tunnel: the rig reconnects.
	h.server.mu.Lock()
	h.server.rigs["rig_1"].session.Close()
	h.server.mu.Unlock()
	waitOnline(t, h, false)
	waitOnline(t, h, true)
	resp, err := hc.Get("http://rig/again")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	cancel()
	waitOnline(t, h, false)
	if _, err := h.server.Dial(context.Background(), "rig_1"); !errors.Is(err, ErrOffline) {
		t.Errorf("dial offline rig: %v", err)
	}
}

// rawHello runs the handshake by hand with a given hello and returns the
// station's answer.
func rawHello(t *testing.T, h *harness, hello func(nonce string) nostr.Event) message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	ws, _, err := websocket.Dial(ctx, h.url(), &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var challenge message
	if err := readJSON(ctx, ws, &challenge); err != nil {
		t.Fatal(err)
	}
	ev := hello(challenge.Nonce)
	if err := writeJSON(ctx, ws, message{Type: typeHello, Event: &ev}); err != nil {
		t.Fatal(err)
	}
	var answer message
	if err := readJSON(ctx, ws, &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestHandshakeRefusals(t *testing.T) {
	h := newHarness(t)
	stranger := nostr.Generate()
	cases := []struct {
		name  string
		hello func(nonce string) nostr.Event
		want  string
	}{
		{"unknown rig", func(nonce string) nostr.Event {
			ev, _ := SignHello(stranger, h.stationKey.Public(), nonce)
			return ev
		}, ErrUnknownRig.Error()},
		{"wrong nonce", func(nonce string) nostr.Event {
			ev, _ := SignHello(h.rigKey, h.stationKey.Public(), "not-the-nonce")
			return ev
		}, "wrong challenge"},
		{"for another station", func(nonce string) nostr.Event {
			ev, _ := SignHello(h.rigKey, stranger.Public(), nonce)
			return ev
		}, "not for this station"},
		{"tampered", func(nonce string) nostr.Event {
			ev, _ := SignHello(h.rigKey, h.stationKey.Public(), nonce)
			ev.Content = "x"
			return ev
		}, "signature"},
		{"old", func(nonce string) nostr.Event {
			ev := nostr.Event{Kind: token.KindHello, CreatedAt: nostr.Now() - 3600,
				Tags: nostr.Tags{{"p", h.stationKey.Public().Hex()}, {"challenge", nonce}}}
			ev.Sign(h.rigKey)
			return ev
		}, "clock off"},
		{"wrong kind", func(nonce string) nostr.Event {
			ev := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", h.stationKey.Public().Hex()}, {"challenge", nonce}}}
			ev.Sign(h.rigKey)
			return ev
		}, "kind"},
	}
	for _, c := range cases {
		answer := rawHello(t, h, c.hello)
		if answer.Type != typeError || !strings.Contains(answer.Error, c.want) {
			t.Errorf("%s: got %+v, want an error with %q", c.name, answer, c.want)
		}
	}
	if h.server.IsOnline("rig_1") {
		t.Error("a refused rig is online")
	}
}

func TestClientRefusesWrongStation(t *testing.T) {
	h := newHarness(t)
	c := h.client(t, http.NotFoundHandler())
	c.Station = nostr.Generate().Public()
	err := c.connect(context.Background())
	if !errors.Is(err, ErrNotStation) {
		t.Errorf("got %v", err)
	}
}
