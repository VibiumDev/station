// Package relay forwards HTTP to a rig over its tunnel: the station's
// reverse proxy for signaling now, and for REST, BiDi, MJPEG and the
// fallback WebSockets later. It is dumb on purpose: bytes in, bytes out,
// the hold grant passed along for the rig to check.
package relay

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/VibiumDev/station/tunnel"
)

// Relay proxies to rigs.
type Relay struct {
	Tunnel *tunnel.Server
	Logf   func(format string, args ...any)

	mu   sync.Mutex
	open map[string]map[*tracked]struct{} // relayed connections by hold id
}

// tracked is one relayed connection on a hold: its context, cancelled to
// stop a plain request, and the client's connection once a WebSocket
// upgrade hijacked it, closed to end that.
type tracked struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	conn   net.Conn
}

// hijackRecorder is the response writer the proxy sees: it remembers the
// connection an upgrade hijacks, so the relay can close it later.
type hijackRecorder struct {
	http.ResponseWriter
	t *tracked
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("not a hijacker")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		h.t.mu.Lock()
		h.t.conn = conn
		h.t.mu.Unlock()
	}
	return conn, rw, err
}

func (h *hijackRecorder) Flush() {
	if f, ok := h.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *hijackRecorder) Unwrap() http.ResponseWriter { return h.ResponseWriter }

// ServeHold is Serve for a connection admitted on a hold: End closes it
// when the hold ends.
func (rl *Relay) ServeHold(w http.ResponseWriter, r *http.Request, rigID, path, holdID string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	t := &tracked{cancel: cancel}
	rl.mu.Lock()
	if rl.open == nil {
		rl.open = map[string]map[*tracked]struct{}{}
	}
	if rl.open[holdID] == nil {
		rl.open[holdID] = map[*tracked]struct{}{}
	}
	rl.open[holdID][t] = struct{}{}
	rl.mu.Unlock()
	defer func() {
		rl.mu.Lock()
		delete(rl.open[holdID], t)
		if len(rl.open[holdID]) == 0 {
			delete(rl.open, holdID)
		}
		rl.mu.Unlock()
	}()
	rl.Serve(&hijackRecorder{ResponseWriter: w, t: t}, r.WithContext(ctx), rigID, path)
}

// End closes every relayed connection on a hold.
func (rl *Relay) End(holdID string) {
	rl.mu.Lock()
	conns := rl.open[holdID]
	delete(rl.open, holdID)
	rl.mu.Unlock()
	for t := range conns {
		t.cancel()
		t.mu.Lock()
		if t.conn != nil {
			t.conn.Close()
		}
		t.mu.Unlock()
	}
}

// Open is how many relayed connections a hold has.
func (rl *Relay) Open(holdID string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.open[holdID])
}

// Serve forwards r to the rig at path, with the query kept. The request's
// own path and host are replaced; everything else, including an
// Authorization header and a WebSocket upgrade, goes through.
func (rl *Relay) Serve(w http.ResponseWriter, r *http.Request, rigID, path string) {
	if !rl.Tunnel.IsOnline(rigID) {
		http.Error(w, "the rig is offline", http.StatusBadGateway)
		return
	}
	target := &url.URL{Scheme: "http", Host: "rig", Path: path, RawQuery: r.URL.RawQuery}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = target
			pr.Out.Host = "rig"
			// The rig sees the station as the client; who the browser is
			// travels in the grant, not in headers it could forge. The
			// station checked the browser's Origin; the rig's own
			// same-origin checks would only see the station's name.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
		},
		Transport:     rl.Tunnel.Transport(rigID),
		FlushInterval: -1, // streams (MJPEG, events) flush as they come
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			if rl.Logf != nil {
				rl.Logf("relay: %s %s: %v", rigID, path, err)
			}
			if errors.Is(err, tunnel.ErrOffline) {
				http.Error(w, "the rig is offline", http.StatusBadGateway)
				return
			}
			http.Error(w, "the rig did not answer", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// IsWebSocket says whether a request asks for an upgrade.
func IsWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

var _ = time.Second
