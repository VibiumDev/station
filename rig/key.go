// Package rig is what a machine needs to be a rig: its key, the policy
// file naming the stations it accepts, enrollment, the tunnel to each
// station, and the gate every relayed request passes: the grant check with
// the hold state, and the endpoint allowlist. Roadie imports it; any other
// device provider can.
package rig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// LoadOrCreateKey reads the rig key from path, making one on the first
// run. The file holds the secret key in hex (an nsec is accepted too) and
// is readable by its owner only.
func LoadOrCreateKey(path string) (sk nostr.SecretKey, created bool, err error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		s := strings.TrimSpace(string(data))
		if strings.HasPrefix(s, "nsec1") {
			prefix, v, err := nip19.Decode(s)
			if err != nil || prefix != "nsec" {
				return sk, false, fmt.Errorf("%s: not an nsec", path)
			}
			return v.(nostr.SecretKey), false, nil
		}
		sk, err = nostr.SecretKeyFromHex(s)
		if err != nil {
			return sk, false, fmt.Errorf("%s: %w", path, err)
		}
		return sk, false, nil
	case errors.Is(err, os.ErrNotExist):
		sk = nostr.Generate()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return sk, false, err
		}
		if err := os.WriteFile(path, []byte(sk.Hex()+"\n"), 0o600); err != nil {
			return sk, false, err
		}
		return sk, true, nil
	default:
		return sk, false, err
	}
}

// Npub is a public key as people see it.
func Npub(pk nostr.PubKey) string { return nip19.EncodeNpub(pk) }

// ParseNpub reads an npub (or a hex key).
func ParseNpub(s string) (nostr.PubKey, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "npub1") {
		prefix, v, err := nip19.Decode(s)
		if err != nil || prefix != "npub" {
			return nostr.ZeroPK, fmt.Errorf("%q is not an npub", s)
		}
		return v.(nostr.PubKey), nil
	}
	return nostr.PubKeyFromHex(s)
}
