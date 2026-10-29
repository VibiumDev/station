// Package relay forwards HTTP to a rig over its tunnel: the station's
// reverse proxy for signaling now, and for REST, BiDi, MJPEG and the
// fallback WebSockets later. It is dumb on purpose: bytes in, bytes out,
// the lease grant passed along for the rig to check.
package relay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/VibiumDev/station/tunnel"
)

// Relay proxies to rigs.
type Relay struct {
	Tunnel *tunnel.Server
	Logf   func(format string, args ...any)
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
