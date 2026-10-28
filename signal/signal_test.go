package signal

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/relay"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/tunnel"
)

// A WebSocket from a browser reaches the rig's handler through the station
// and the tunnel, and messages flow both ways.
func TestSignalRelay(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	stationKey, rigKey := nostr.Generate(), nostr.Generate()
	db.PutRig(ctx, station.Rig{ID: "rig_1", Name: "pi", PubKey: rigKey.Public(), EnrolledAt: time.Now(),
		Devices: []station.Device{{ID: "rig_1/phone", Kind: "phone"}}})
	online := make(chan bool, 4)
	ts := &tunnel.Server{Key: stationKey.Public(), Devices: db, Logf: t.Logf, OnOnline: func(_ string, up bool) { online <- up }}
	touched := make(chan string, 4)
	h := &Handler{Relay: &relay.Relay{Tunnel: ts, Logf: t.Logf}, Devices: db, Touch: func(_ context.Context, id string) { touched <- id }}
	mux := http.NewServeMux()
	mux.Handle("/v1/tunnel", ts)
	// A device id has a slash in it, so the pattern takes both parts.
	mux.HandleFunc("/v1/devices/{rig}/{name}/signal", func(w http.ResponseWriter, r *http.Request) {
		h.ServeDevice(w, r, r.PathValue("rig")+"/"+r.PathValue("name"))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	// The rig's signaling handler: echoes each message with a prefix.
	rigMux := http.NewServeMux()
	rigMux.HandleFunc(RigPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie reached the rig")
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			c.Write(r.Context(), websocket.MessageText, append([]byte("rig: "), data...))
		}
	})
	rigMux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "jpeg") })
	client := &tunnel.Client{URL: "wss" + strings.TrimPrefix(srv.URL, "https") + "/v1/tunnel", Key: rigKey, Station: stationKey.Public(),
		TLS: &tls.Config{InsecureSkipVerify: true}, Handler: rigMux, Logf: t.Logf}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go client.Run(cctx)
	<-online

	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	ws, _, err := websocket.Dial(wctx, "wss"+strings.TrimPrefix(srv.URL, "https")+"/v1/devices/rig_1/phone/signal",
		&websocket.DialOptions{HTTPClient: hc, HTTPHeader: http.Header{"Cookie": {"station_session=x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(wctx, websocket.MessageText, []byte(`{"type":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := ws.Read(wctx)
	if err != nil || string(data) != `rig: {"type":"hello"}` {
		t.Fatalf("got %q %v", data, err)
	}
	ws.Close(websocket.StatusNormalClosure, "")
	for i := 0; i < 2; i++ {
		select {
		case id := <-touched:
			if id != "rig_1/phone" {
				t.Errorf("touched %q", id)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("touch missing")
		}
	}

	// A plain request relays too, and an unknown device is refused.
	resp, err := hc.Get(srv.URL + "/v1/devices/rig_1/tablet/signal")
	if err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown device: %v %v", resp, err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	h.Relay.Serve(rr, req, "rig_1", "/snapshot")
	if rr.Code != 200 || rr.Body.String() != "jpeg" {
		t.Errorf("relayed GET: %d %q", rr.Code, rr.Body.String())
	}
	cancel()
	<-online
	rr = httptest.NewRecorder()
	h.Relay.Serve(rr, req, "rig_1", "/snapshot")
	if rr.Code != http.StatusBadGateway {
		t.Errorf("offline rig: %d", rr.Code)
	}
}
