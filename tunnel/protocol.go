// Package tunnel is the rig's one outbound connection to its station: a
// WebSocket at /v1/tunnel, opened by the rig, authenticated by signing the
// station's challenge, then multiplexed with yamux. The station opens
// streams to reach the rig's HTTP handlers; the rig opens one control
// stream for announcements, revocations and pings. The station never
// dials a rig.
package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station/token"
)

// Messages on the WebSocket before yamux starts, and on the control stream
// after, are JSON objects with a "type".
type message struct {
	Type    string        `json:"type"`
	Nonce   string        `json:"nonce,omitempty"`   // challenge
	Station string        `json:"station,omitempty"` // challenge: the station's public key, hex
	Rig     string        `json:"rig,omitempty"`     // welcome: the rig id
	Event   *nostr.Event  `json:"event,omitempty"`   // hello, revoke
	Events  []nostr.Event `json:"events,omitempty"`  // announce
	Error   string        `json:"error,omitempty"`
}

const (
	typeChallenge = "challenge"
	typeHello     = "hello"
	typeWelcome   = "welcome"
	typeError     = "error"
	typeAnnounce  = "announce"
	typeRevoke    = "revoke"
	typePing      = "ping"
	typePong      = "pong"
)

// helloSkew is how far the hello's timestamp may be from the station's
// clock; rigs set their clocks by NTP but may have just booted.
const helloSkew = 5 * time.Minute

// Errors of the handshake.
var (
	ErrUnknownRig = errors.New("unknown rig")
	ErrBadHello   = errors.New("bad hello")
	ErrNotStation = errors.New("the station's key is not the one enrolled")
)

func newNonce() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// SignHello answers a challenge with the rig's key: a kind 27700 event
// naming the station and the nonce.
func SignHello(sk nostr.SecretKey, station nostr.PubKey, nonce string) (nostr.Event, error) {
	ev := nostr.Event{Kind: token.KindHello, CreatedAt: nostr.Now(),
		Tags: nostr.Tags{{"p", station.Hex()}, {"challenge", nonce}}}
	if err := ev.Sign(sk); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// checkHello verifies a hello against the challenge and returns the rig's
// key.
func checkHello(ev nostr.Event, station nostr.PubKey, nonce string, now time.Time) (nostr.PubKey, error) {
	if ev.Kind != token.KindHello {
		return nostr.ZeroPK, fmt.Errorf("%w: kind %d", ErrBadHello, ev.Kind)
	}
	if !ev.CheckID() || !ev.VerifySignature() {
		return nostr.ZeroPK, fmt.Errorf("%w: signature", ErrBadHello)
	}
	if t := ev.Tags.FindWithValue("challenge", nonce); t == nil {
		return nostr.ZeroPK, fmt.Errorf("%w: wrong challenge", ErrBadHello)
	}
	if t := ev.Tags.FindWithValue("p", station.Hex()); t == nil {
		return nostr.ZeroPK, fmt.Errorf("%w: not for this station", ErrBadHello)
	}
	if d := now.Sub(ev.CreatedAt.Time()); d > helloSkew || d < -helloSkew {
		return nostr.ZeroPK, fmt.Errorf("%w: clock off by %v", ErrBadHello, d)
	}
	return ev.PubKey, nil
}
