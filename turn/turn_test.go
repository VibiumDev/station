package turn

import (
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	pion "github.com/pion/turn/v4"
)

func TestAllowed(t *testing.T) {
	s := &Server{Deny: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}}
	for ip, want := range map[string]bool{
		"8.8.8.8":              true,
		"203.0.113.5":          false, // configured
		"198.51.100.7":         true,
		"10.0.0.1":             false,
		"172.16.5.5":           false,
		"192.168.1.1":          false,
		"127.0.0.1":            false,
		"169.254.169.254":      false,
		"100.100.1.1":          false, // shared address space, tailnets
		"224.0.0.1":            false,
		"0.0.0.0":              false,
		"255.255.255.255":      false,
		"240.0.0.1":            false,
		"198.18.0.1":           false,
		"2001:4860:4860::8888": true,
		"fdaa:0:1::2":          false, // unique local, Fly's 6PN
		"fe80::1":              false,
		"::1":                  false,
		"::":                   false,
		"64:ff9b::a00:1":       false,
		"::ffff:10.1.1.1":      false,
		"::ffff:8.8.4.4":       true,
		"ff02::1":              false,
	} {
		if got := s.Allowed(net.ParseIP(ip)); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", ip, got, want)
		}
	}
	if s.Allowed(nil) {
		t.Error("nil allowed")
	}
}

func TestCredentials(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	u := Username("hld_1", exp)
	hold, got, err := ParseUsername(u)
	if err != nil || hold != "hld_1" || !got.Equal(exp) {
		t.Fatalf("ParseUsername(%q) = %q %v %v", u, hold, got, err)
	}
	if _, _, err := ParseUsername("hld_1"); err == nil {
		t.Error("no expiry parsed")
	}
	if _, _, err := ParseUsername("x:hld_1"); err == nil {
		t.Error("bad expiry parsed")
	}
	if Password([]byte("a"), u) == Password([]byte("b"), u) {
		t.Error("the password does not depend on the secret")
	}
}

// start runs a server on the loopback with a small relay range.
func start(t *testing.T, valid func(string) bool, quota int) *Server {
	t.Helper()
	s := &Server{Listen: "127.0.0.1:0", PublicIP: net.IPv4(127, 0, 0, 1), RelayAddress: "127.0.0.1",
		MinPort: 50100, MaxPort: 50120, Secret: []byte("test secret"), Valid: valid, Quota: quota, Logf: t.Logf}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func client(t *testing.T, s *Server, username, password string) *pion.Client {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c, err := pion.NewClient(&pion.ClientConfig{
		STUNServerAddr: s.Addr().String(), TURNServerAddr: s.Addr().String(),
		Username: username, Password: password, Realm: DefaultRealm, Conn: conn, RTO: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); conn.Close() })
	return c
}

func TestServer(t *testing.T) {
	// The server asks whether a hold has ended from its own goroutines,
	// while the test marks one ended: the map is shared under a lock.
	var mu sync.Mutex
	ended := map[string]bool{}
	s := start(t, func(hold string) bool {
		mu.Lock()
		defer mu.Unlock()
		return !ended[hold]
	}, 1)
	creds := s.Credentials("hld_ok", time.Now().Add(time.Hour))
	if len(creds) != 1 || len(creds[0].URLs) != 2 || !strings.HasPrefix(creds[0].URLs[1], "turn:127.0.0.1:") {
		t.Fatalf("credentials: %+v", creds)
	}
	good := client(t, s, creds[0].Username, creds[0].Credential)
	relay, err := good.Allocate()
	if err != nil {
		t.Fatalf("a valid credential was refused: %v", err)
	}
	defer relay.Close()
	if s.Allocations() != 1 {
		t.Errorf("allocations = %d", s.Allocations())
	}
	if ra, ok := relay.LocalAddr().(*net.UDPAddr); !ok || ra.Port < 50100 || ra.Port > 50120 {
		t.Errorf("relay address %v outside the range", relay.LocalAddr())
	}

	// Permissions: a public peer yes, private and loopback no.
	if err := good.CreatePermission(&net.UDPAddr{IP: net.ParseIP("198.51.100.9"), Port: 5000}); err != nil {
		t.Errorf("public peer refused: %v", err)
	}
	for _, ip := range []string{"10.1.2.3", "127.0.0.1", "192.168.0.10", "169.254.1.1", "100.64.0.1"} {
		if err := good.CreatePermission(&net.UDPAddr{IP: net.ParseIP(ip), Port: 5000}); err == nil {
			t.Errorf("private peer %s allowed", ip)
		}
	}

	// A second allocation on the same credential is over the quota of one.
	again := client(t, s, creds[0].Username, creds[0].Credential)
	if r, err := again.Allocate(); err == nil {
		r.Close()
		t.Error("quota not enforced")
	}

	// Expired, wrong password, unknown shape, ended hold.
	old := s.Credentials("hld_old", time.Now().Add(-time.Minute))
	if r, err := client(t, s, old[0].Username, old[0].Credential).Allocate(); err == nil {
		r.Close()
		t.Error("an expired credential was accepted")
	}
	if r, err := client(t, s, creds[0].Username, "wrong").Allocate(); err == nil {
		r.Close()
		t.Error("a wrong password was accepted")
	}
	if r, err := client(t, s, "nobody", "x").Allocate(); err == nil {
		r.Close()
		t.Error("a malformed username was accepted")
	}
	mu.Lock()
	ended["hld_gone"] = true
	mu.Unlock()
	gone := s.Credentials("hld_gone", time.Now().Add(time.Hour))
	if r, err := client(t, s, gone[0].Username, gone[0].Credential).Allocate(); err == nil {
		r.Close()
		t.Error("an ended hold's credential was accepted")
	}
}
