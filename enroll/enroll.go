// Package enroll is the station's side of a rig joining it: one-time codes
// that carry the station key's fingerprint, the /v1/station profile the
// rig checks that fingerprint against, the /v1/enroll endpoint, and mDNS
// so a rig on the LAN finds the station without a URL.
package enroll

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/hashicorp/mdns"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

const (
	// Service is the mDNS service a station advertises.
	Service = "_vibium-station._tcp"
	// CodeLifetime is how long an enrollment code stays usable.
	CodeLifetime = 15 * time.Minute
)

// Fingerprint is the start of the station's key, the last group of every
// enrollment code: the rig refuses a station whose key does not match it.
func Fingerprint(pk nostr.PubKey) string {
	return strings.ToUpper(pk.Hex()[:4])
}

// The code alphabet leaves out letters and digits that read alike.
const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewCode makes a code such as ABCD-1234-7F3A: two random groups and the
// station key's fingerprint.
func NewCode(pk nostr.PubKey) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	s := make([]byte, 8)
	for i := range s {
		s[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(s[:4]) + "-" + string(s[4:]) + "-" + Fingerprint(pk)
}

// ParseCode normalizes a typed code and returns it with its fingerprint.
func ParseCode(code string) (normalized, fingerprint string, err error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	parts := strings.FieldsFunc(code, func(r rune) bool { return r == '-' || r == ' ' })
	if len(parts) != 3 || len(parts[0]) != 4 || len(parts[1]) != 4 || len(parts[2]) != 4 {
		return "", "", errors.New("a code looks like ABCD-1234-7F3A")
	}
	normalized = strings.Join(parts, "-")
	return normalized, parts[2], nil
}

// Codes burns codes: the sqlite store does.
type Codes interface {
	UseCode(ctx context.Context, code string, now time.Time) error
}

// Profile is what GET /v1/station returns: the station's name, key and
// tunnel URL, signed as its kind 0 event so the rig can check the key.
type Profile struct {
	Name      string      `json:"name"`
	PubKey    string      `json:"pubkey"`
	TunnelURL string      `json:"tunnel_url"`
	Event     nostr.Event `json:"event"`
}

// Response is what a successful enrollment returns.
type Response struct {
	RigID     string `json:"rig"`
	Station   string `json:"station"` // the station's key, hex
	Name      string `json:"name"`    // the station's name
	TunnelURL string `json:"tunnel_url"`
}

// Handler serves the profile and enrollment endpoints.
type Handler struct {
	Key       nostr.SecretKey
	Name      string
	TunnelURL string
	Devices   station.DeviceStore
	Events    station.EventStore
	Codes     Codes
	Audit     station.AuditSink // optional
	Logf      func(format string, args ...any)
}

func (h *Handler) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Profile signs the station's profile.
func (h *Handler) Profile() (Profile, error) {
	content, _ := json.Marshal(map[string]string{"name": h.Name, "tunnel_url": h.TunnelURL})
	ev := nostr.Event{Kind: token.KindProfile, CreatedAt: nostr.Now(), Content: string(content)}
	if err := ev.Sign(h.Key); err != nil {
		return Profile{}, err
	}
	return Profile{Name: h.Name, PubKey: h.Key.Public().Hex(), TunnelURL: h.TunnelURL, Event: ev}, nil
}

// ServeProfile is GET /v1/station.
func (h *Handler) ServeProfile(w http.ResponseWriter, r *http.Request) {
	p, err := h.Profile()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// request is the body of POST /v1/enroll.
type request struct {
	Event nostr.Event `json:"event"`
}

// Offer is a device as a rig describes it before it has an id: the name
// the station will put after its rig id.
type Offer struct {
	Name      string             `json:"name"`
	Kind      string             `json:"kind"`
	Caps      []string           `json:"caps"`
	Key       string             `json:"key,omitempty"`
	Shortcuts []station.Shortcut `json:"shortcuts,omitempty"`
	Input     string             `json:"input,omitempty"`      // "touch" or "mouse"
	Extras    string             `json:"extras,omitempty"`     // the rig's viewer extras script path
	Label     string             `json:"label,omitempty"`      // the device's name for people
	Platform  string             `json:"platform,omitempty"`   // "ios", "android", ...
	Model     string             `json:"model,omitempty"`      // "iPhone 15 Pro Max"
	OSVersion string             `json:"os_version,omitempty"` // "26.6"
}

// Device is the offer as the station's device under the rig's id.
func (o Offer) Device(rigID string) station.Device {
	return station.Device{ID: station.DeviceID(rigID, o.Name), Kind: o.Kind, Caps: o.Caps, Key: o.Key, Shortcuts: o.Shortcuts,
		Input: o.Input, Extras: o.Extras, Label: o.Label, Platform: o.Platform, Model: o.Model, OSVersion: o.OSVersion}
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ServeEnroll is POST /v1/enroll: a kind 7702 event signed by the rig,
// naming this station, with a live code and the rig's devices.
func (h *Handler) ServeEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST an enrollment request")
		return
	}
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	ev := req.Event
	if ev.Kind != token.KindEnrollment {
		fail(w, http.StatusBadRequest, "not an enrollment request")
		return
	}
	if !ev.CheckID() || !ev.VerifySignature() {
		fail(w, http.StatusBadRequest, "bad signature")
		return
	}
	if ev.Tags.FindWithValue("p", h.Key.Public().Hex()) == nil {
		fail(w, http.StatusBadRequest, "the request names another station")
		return
	}
	codeTag, nameTag := ev.Tags.Find("code"), ev.Tags.Find("name")
	if len(codeTag) < 2 || len(nameTag) < 2 || strings.TrimSpace(nameTag[1]) == "" {
		fail(w, http.StatusBadRequest, "code and name tags are required")
		return
	}
	code, _, err := ParseCode(codeTag[1])
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var offers []Offer
	if err := json.Unmarshal([]byte(ev.Content), &offers); err != nil {
		fail(w, http.StatusBadRequest, "devices: "+err.Error())
		return
	}
	for _, d := range offers {
		if d.Name == "" || strings.Contains(d.Name, "/") || d.Kind == "" {
			fail(w, http.StatusBadRequest, "every device needs a name without a slash and a kind")
			return
		}
	}
	ctx := r.Context()
	if err := h.Codes.UseCode(ctx, code, time.Now()); err != nil {
		h.logf("enroll: %s: %v", r.RemoteAddr, err)
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	// A key already enrolled keeps its id: the rig is enrolling again.
	rigID := station.NewID("rig")
	if old, err := h.Devices.RigByKey(ctx, ev.PubKey); err == nil {
		rigID = old.ID
	}
	rig := station.Rig{ID: rigID, Name: strings.TrimSpace(nameTag[1]), PubKey: ev.PubKey, EnrolledAt: time.Now()}
	for _, d := range offers {
		rig.Devices = append(rig.Devices, d.Device(rigID))
	}
	if err := h.Devices.PutRig(ctx, rig); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Events != nil {
		h.Events.SaveEvent(ev)
	}
	if h.Audit != nil {
		h.Audit.Audit(ctx, station.AuditEntry{Subject: "rig:" + rigID, Action: "enroll", Detail: fmt.Sprintf("%s, %d devices, from %s", rig.Name, len(rig.Devices), r.RemoteAddr)})
	}
	h.logf("enroll: %s (%s) enrolled from %s with %d devices", rigID, rig.Name, r.RemoteAddr, len(rig.Devices))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{RigID: rigID, Station: h.Key.Public().Hex(), Name: h.Name, TunnelURL: h.TunnelURL})
}

// Advertise announces the station on the LAN over mDNS. The returned
// function stops it.
func Advertise(name string, port int) (func(), error) {
	host := strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name))
	if host == "" {
		host = "station"
	}
	service, err := mdns.NewMDNSService(name, Service, "", host+".local.", port, localIPs(), []string{"version=1"})
	if err != nil {
		return nil, err
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return nil, err
	}
	return func() { server.Shutdown() }, nil
}

// Discover looks for a station on the LAN and returns its URL.
func Discover(ctx context.Context, timeout time.Duration) (string, error) {
	entries := make(chan *mdns.ServiceEntry, 8)
	params := mdns.DefaultParams(Service)
	params.Entries = entries
	params.Timeout = timeout
	params.DisableIPv6 = true
	params.Logger = log.New(nullWriter{}, "", 0)
	done := make(chan error, 1)
	go func() { done <- mdns.Query(params); close(entries) }()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case e, ok := <-entries:
			if !ok {
				return "", errors.New("no station found on this network")
			}
			if e == nil || e.Port == 0 {
				continue
			}
			ip := e.AddrV4
			if ip == nil {
				ip = e.Addr
			}
			if ip == nil {
				continue
			}
			return fmt.Sprintf("https://%s", net.JoinHostPort(ip.String(), fmt.Sprint(e.Port))), nil
		}
	}
}

type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

func localIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, addr := range addrs {
		if n, ok := addr.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			ips = append(ips, n.IP)
		}
	}
	return ips
}
