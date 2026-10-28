package rig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/enroll"
	"github.com/VibiumDev/station/token"
)

// EnrollOptions is what enrolling takes.
type EnrollOptions struct {
	URL     string         // the station; empty finds one on the LAN over mDNS
	Code    string         // the one-time code from the station
	Name    string         // this rig's name
	Devices []enroll.Offer // what this rig offers
	Key     nostr.SecretKey
	Admin   bool          // accept admin grants from this station
	Timeout time.Duration // zero means 30 seconds
}

// ErrFingerprint says the station's key does not match the code, so this
// is not the station the code came from.
var ErrFingerprint = errors.New("the station's key does not match the code")

// Enroll joins a station: checks its key against the code's fingerprint,
// sends the signed request, and returns the policy entry to save.
func Enroll(ctx context.Context, o EnrollOptions) (*StationEntry, error) {
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	code, fp, err := enroll.ParseCode(o.Code)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(o.Name) == "" {
		return nil, errors.New("the rig needs a name")
	}
	base := strings.TrimRight(o.URL, "/")
	if base == "" {
		if base, err = enroll.Discover(ctx, 3*time.Second); err != nil {
			return nil, err
		}
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("the station URL must be https://host[:port], got %q", base)
	}
	// Trust on first use for the certificate: the code's fingerprint, not
	// the certificate, says this is the right station.
	t := &tofu{host: u.Hostname()}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: t.config(), Proxy: http.ProxyFromEnvironment}}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/station", nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("station: %w", err)
	}
	var profile enroll.Profile
	err = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&profile)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("station profile: %w", err)
	}
	pk, err := nostr.PubKeyFromHex(profile.PubKey)
	if err != nil || profile.Event.PubKey != pk || profile.Event.Kind != token.KindProfile || !profile.Event.CheckID() || !profile.Event.VerifySignature() {
		return nil, errors.New("station profile: not signed by the key it names")
	}
	if enroll.Fingerprint(pk) != fp {
		return nil, ErrFingerprint
	}

	devices, _ := json.Marshal(o.Devices)
	ev := nostr.Event{Kind: token.KindEnrollment, CreatedAt: nostr.Now(),
		Tags: nostr.Tags{{"p", pk.Hex()}, {"code", code}, {"name", strings.TrimSpace(o.Name)}}, Content: string(devices)}
	if err := ev.Sign(o.Key); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"event": ev})
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/enroll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, fmt.Errorf("enroll: %s", e.Error)
	}
	var out enroll.Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}
	if out.Station != pk.Hex() || out.RigID == "" || out.TunnelURL == "" {
		return nil, errors.New("enroll: the station's answer is incomplete")
	}
	entry := &StationEntry{
		Name: out.Name, Npub: Npub(pk), URL: base, TunnelURL: out.TunnelURL, RigID: out.RigID,
		Scopes: station.ScopeStrings([]station.Scope{station.ScopeView, station.ScopeInput, station.ScopeBidi}),
		Admin:  o.Admin, MaxSession: "8h",
	}
	if !t.verified() {
		entry.CertSHA256 = t.leaf()
	}
	return entry, nil
}

// tofu records the certificate a server presented and whether the system
// roots vouch for it.
type tofu struct {
	host string
	mu   sync.Mutex
	hash string
	ok   bool
}

func (t *tofu) config() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // the check below does the work
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			t.mu.Lock()
			defer t.mu.Unlock()
			t.hash = certHash(raw[0])
			t.ok = verifyChain(raw, t.host) == nil
			return nil
		},
	}
}

func (t *tofu) leaf() string   { t.mu.Lock(); defer t.mu.Unlock(); return t.hash }
func (t *tofu) verified() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.ok }

func certHash(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func verifyChain(raw [][]byte, host string) error {
	certs := make([]*x509.Certificate, 0, len(raw))
	for _, der := range raw {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return err
		}
		certs = append(certs, c)
	}
	opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(opts)
	return err
}

// TLSConfig is how the rig connects to an enrolled station: the system
// roots, or the certificate pinned at enrollment.
func TLSConfig(e *StationEntry) *tls.Config {
	if e.CertSHA256 == "" {
		return nil
	}
	u, _ := url.Parse(e.URL)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	pin := e.CertSHA256
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			if certHash(raw[0]) == pin {
				return nil
			}
			if err := verifyChain(raw, host); err == nil {
				return nil
			}
			return fmt.Errorf("the station's certificate changed and no root signs it: enroll again if that is expected")
		},
	}
}
