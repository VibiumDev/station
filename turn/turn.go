// Package turn is the station's embedded TURN server: pion/turn with one
// job, carrying a session's media when the browser and the rig cannot
// reach each other directly. Credentials are per hold and last the
// hold's maximum time; relaying to private, loopback, link-local,
// shared-address and provider-internal destinations is refused, so the
// server is not a proxy into anyone's network.
package turn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/logging"
	pion "github.com/pion/turn/v4"

	"github.com/VibiumDev/station/token"
)

const (
	// DefaultListen is the usual TURN port, UDP.
	DefaultListen = ":3478"
	// DefaultMinPort and DefaultMaxPort are the relay ports when none are
	// configured: the ephemeral range.
	DefaultMinPort = 49152
	DefaultMaxPort = 65535
	// DefaultQuota is how many allocations one credential may hold at
	// once: a browser and a rig each take one per peer connection.
	DefaultQuota = 8
	// DefaultRealm names the server in credential challenges.
	DefaultRealm = "station"
)

// Server is the TURN server. Set the fields, then Start.
type Server struct {
	// Listen is the UDP address clients reach, DefaultListen by default.
	// On Fly.io it is "fly-global-services:3478".
	Listen string
	// PublicIP is the address clients and peers reach this server at:
	// the listener's and the relays' advertised address. Required.
	PublicIP net.IP
	// RelayAddress is the address relay sockets bind, the listener's host
	// by default (every address when that is empty). On Fly.io it is
	// "fly-global-services".
	RelayAddress string
	// MinPort and MaxPort are the relay port range, inclusive; each
	// allocation takes one port, which must be reachable from outside.
	MinPort, MaxPort uint16
	// Realm is the credential realm, DefaultRealm by default.
	Realm string
	// Secret keys the credentials. Required; derive it from the station's
	// key or keep it in the config.
	Secret []byte
	// Deny lists more destinations to refuse besides the built-in ones,
	// such as a provider's internal range.
	Deny []netip.Prefix
	// MTU is the inbound MTU; pion's default (1600) when zero.
	MTU int
	// Quota is the allocations one credential may hold, DefaultQuota when
	// zero.
	Quota int
	// Valid says whether a hold may still allocate and refresh; nil
	// accepts any credential inside its time. A revoked hold's
	// allocations die at their next refresh, ten minutes at most.
	Valid func(holdID string) bool
	Now   func() time.Time
	Logf  func(format string, args ...any)

	srv  *pion.Server
	conn net.PacketConn
	mu   sync.Mutex
	held map[string]int // allocations by username
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Start binds the listener and serves until Close.
func (s *Server) Start() error {
	if len(s.Secret) == 0 {
		return errors.New("turn: a secret is required")
	}
	if s.PublicIP == nil {
		return errors.New("turn: the public IP is required")
	}
	listen := s.Listen
	if listen == "" {
		listen = DefaultListen
	}
	conn, err := net.ListenPacket("udp4", listen)
	if err != nil {
		return fmt.Errorf("turn: listen %s: %w", listen, err)
	}
	relay := s.RelayAddress
	if relay == "" {
		relay, _, _ = net.SplitHostPort(listen)
	}
	if relay == "" {
		relay = "0.0.0.0"
	}
	minPort, maxPort := s.MinPort, s.MaxPort
	if minPort == 0 {
		minPort = DefaultMinPort
	}
	if maxPort == 0 {
		maxPort = DefaultMaxPort
	}
	if maxPort < minPort {
		conn.Close()
		return fmt.Errorf("turn: relay ports %d-%d", minPort, maxPort)
	}
	realm := s.Realm
	if realm == "" {
		realm = DefaultRealm
	}
	logs := logging.NewDefaultLoggerFactory()
	logs.DefaultLogLevel = logging.LogLevelError
	s.held = map[string]int{}
	srv, err := pion.NewServer(pion.ServerConfig{
		Realm:         realm,
		AuthHandler:   s.auth,
		QuotaHandler:  s.quota,
		LoggerFactory: logs,
		InboundMTU:    s.MTU,
		PacketConnConfigs: []pion.PacketConnConfig{{
			PacketConn: conn,
			RelayAddressGenerator: &pion.RelayAddressGeneratorPortRange{
				RelayAddress: s.PublicIP, MinPort: minPort, MaxPort: maxPort, Address: relay, MaxRetries: 20,
			},
			PermissionHandler: func(_ net.Addr, peer net.IP) bool { return s.Allowed(peer) },
		}},
		EventHandler: pion.EventHandler{
			OnAllocationCreated: func(src, _ net.Addr, _, username, _ string, relayAddr net.Addr, _ int) {
				s.mu.Lock()
				s.held[username]++
				s.mu.Unlock()
				s.logf("turn: allocation for %s from %s at %s", holdOf(username), src, relayAddr)
			},
			OnAllocationDeleted: func(_, _ net.Addr, _, username, _ string) {
				s.mu.Lock()
				if s.held[username]--; s.held[username] <= 0 {
					delete(s.held, username)
				}
				s.mu.Unlock()
			},
		},
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("turn: %w", err)
	}
	s.srv, s.conn = srv, conn
	return nil
}

// Close stops the server and ends every allocation.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// Addr is the listener's address, for tests and logs.
func (s *Server) Addr() net.Addr {
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

// Allocations is how many relays are live.
func (s *Server) Allocations() int {
	if s.srv == nil {
		return 0
	}
	return s.srv.AllocationCount()
}

func holdOf(username string) string {
	_, rest, _ := strings.Cut(username, ":")
	return rest
}

func (s *Server) auth(username, realm string, src net.Addr) ([]byte, bool) {
	holdID, expires, err := ParseUsername(username)
	if err != nil {
		s.logf("turn: %s: refused a malformed credential", src)
		return nil, false
	}
	if !s.now().Before(expires) {
		s.logf("turn: %s: refused an expired credential for %s", src, holdID)
		return nil, false
	}
	if s.Valid != nil && !s.Valid(holdID) {
		s.logf("turn: %s: refused a credential for the ended hold %s", src, holdID)
		return nil, false
	}
	return pion.GenerateAuthKey(username, realm, Password(s.Secret, username)), true
}

func (s *Server) quota(username, _ string, src net.Addr) bool {
	quota := s.Quota
	if quota <= 0 {
		quota = DefaultQuota
	}
	s.mu.Lock()
	n := s.held[username]
	s.mu.Unlock()
	if n >= quota {
		s.logf("turn: %s: %s has %d allocations already", src, holdOf(username), n)
		return false
	}
	return true
}

// denied are destinations no relay may reach besides what net.IP already
// calls private, loopback, link-local, multicast or unspecified: shared
// address space (carrier NAT and tailnets), IETF protocol assignments,
// benchmarking, reserved, and the NAT64 prefix that reaches IPv4 again.
var denied = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// Allowed says whether a relay may send to ip: public unicast only, minus
// the built-in and configured ranges.
func (s *Server) Allowed(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, p := range denied {
		if p.Contains(addr) {
			return false
		}
	}
	for _, p := range s.Deny {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// URLs are the server's STUN and TURN URLs for clients.
func (s *Server) URLs() []string {
	port := "3478"
	if a, ok := s.Addr().(*net.UDPAddr); ok && a != nil {
		port = strconv.Itoa(a.Port)
	} else if _, p, err := net.SplitHostPort(s.Listen); err == nil && p != "" {
		port = p
	}
	hp := net.JoinHostPort(s.PublicIP.String(), port)
	return []string{"stun:" + hp, "turn:" + hp + "?transport=udp"}
}

// Credentials makes the ICE server entry a hold's peers use until
// expires: the same username and password for the browser and the rig,
// since they share the hold.
func (s *Server) Credentials(holdID string, expires time.Time) []token.ICEServer {
	u := Username(holdID, expires)
	return []token.ICEServer{{URLs: s.URLs(), Username: u, Credential: Password(s.Secret, u)}}
}

// Username is "<expiry unix>:<hold id>", the TURN REST convention, so
// the server can read the expiry without state.
func Username(holdID string, expires time.Time) string {
	return strconv.FormatInt(expires.Unix(), 10) + ":" + holdID
}

// ParseUsername reads a Username back.
func ParseUsername(u string) (holdID string, expires time.Time, err error) {
	exp, rest, ok := strings.Cut(u, ":")
	if !ok || rest == "" {
		return "", time.Time{}, errors.New("turn: not an expiry:hold username")
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("turn: expiry %q", exp)
	}
	return rest, time.Unix(n, 0), nil
}

// Password is the credential for a username: an HMAC of it under the
// server's secret, so the server keeps nothing per credential.
func Password(secret []byte, username string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
