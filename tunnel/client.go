package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Client is the rig's side: it keeps one tunnel to the station up,
// serving the rig's HTTP handlers over it.
type Client struct {
	URL     string          // wss://station/v1/tunnel
	Key     nostr.SecretKey // the rig key
	Station nostr.PubKey    // the station's key, pinned at enrollment
	TLS     *tls.Config     // optional; see rig for the pinned certificate
	Handler http.Handler    // what the station reaches over the tunnel

	// Announce returns the signed announcement events to send after each
	// connect and on Reannounce.
	Announce func() ([]nostr.Event, error)
	// OnRevoke receives revocation events the station pushes.
	OnRevoke func(ev nostr.Event)
	// OnConnect is told when the tunnel is up (with the rig id the station
	// uses) or down. Optional.
	OnConnect func(rigID string, up bool)
	Logf      func(format string, args ...any)

	mu      sync.Mutex
	control net.Conn
	rigID   string
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Run keeps the tunnel up until ctx ends, reconnecting with backoff and
// jitter.
func (c *Client) Run(ctx context.Context) {
	delay := time.Second
	for {
		start := time.Now()
		err := c.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.logf("tunnel: %v", err)
		}
		if time.Since(start) > time.Minute {
			delay = time.Second // it was up for a while: start over
		}
		wait := delay + time.Duration(rand.Int64N(int64(delay)/2+1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if delay < time.Minute {
			delay *= 2
		}
	}
}

// Connected says whether the tunnel is up, and the rig id if so.
func (c *Client) Connected() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rigID, c.control != nil
}

// ErrNotConnected says the tunnel is down.
var ErrNotConnected = errors.New("tunnel is down")

func (c *Client) send(m message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.control == nil {
		return ErrNotConnected
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.control.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.control.Write(append(data, '\n'))
	return err
}

// Reannounce sends the announcement again, after the devices changed.
func (c *Client) Reannounce() error {
	if c.Announce == nil {
		return nil
	}
	events, err := c.Announce()
	if err != nil {
		return err
	}
	return c.send(message{Type: typeAnnounce, Events: events})
}

// connect runs one tunnel until it ends.
func (c *Client) connect(ctx context.Context) error {
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: c.TLS, Proxy: http.ProxyFromEnvironment}}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, _, err := websocket.Dial(dctx, c.URL, &websocket.DialOptions{HTTPClient: hc, CompressionMode: websocket.CompressionDisabled})
	cancel()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)

	var challenge message
	if err := readJSON(ctx, ws, &challenge); err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	if challenge.Type != typeChallenge || challenge.Nonce == "" {
		return fmt.Errorf("challenge: got %q", challenge.Type)
	}
	if challenge.Station != c.Station.Hex() {
		return ErrNotStation
	}
	hello, err := SignHello(c.Key, c.Station, challenge.Nonce)
	if err != nil {
		return err
	}
	if err := writeJSON(ctx, ws, message{Type: typeHello, Event: &hello}); err != nil {
		return err
	}
	var welcome message
	if err := readJSON(ctx, ws, &welcome); err != nil {
		return fmt.Errorf("welcome: %w", err)
	}
	if welcome.Type != typeWelcome {
		return fmt.Errorf("refused: %s", welcome.Error)
	}

	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 15 * time.Second
	nc := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	session, err := yamux.Client(nc, cfg)
	if err != nil {
		return err
	}
	defer session.Close()
	control, err := session.Open()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.control, c.rigID = control, welcome.Rig
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.control, c.rigID = nil, ""
		c.mu.Unlock()
		if c.OnConnect != nil {
			c.OnConnect(welcome.Rig, false)
		}
	}()
	c.logf("tunnel: connected to %s as %s", c.URL, welcome.Rig)
	if c.OnConnect != nil {
		c.OnConnect(welcome.Rig, true)
	}
	if err := c.Reannounce(); err != nil {
		return err
	}

	// Serve the station's streams, and end when ctx does.
	srv := &http.Server{Handler: c.Handler, ReadHeaderTimeout: 30 * time.Second}
	go srv.Serve(session)
	go func() {
		select {
		case <-ctx.Done():
			session.Close()
		case <-session.CloseChan():
		}
	}()

	sc := bufio.NewScanner(control)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch m.Type {
		case typeRevoke:
			if c.OnRevoke != nil && m.Event != nil {
				c.OnRevoke(*m.Event)
			}
		case typePing:
			c.send(message{Type: typePong})
		}
	}
	srv.Close()
	return errors.New("tunnel closed")
}
