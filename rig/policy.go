package rig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
)

// DefaultMaxSession is the longest hold a station may hold unless the
// policy says otherwise.
const DefaultMaxSession = 8 * time.Hour

// Policy is the operator's consent: the stations this rig accepts and what
// each may do. It is a file on the rig, written by enrollment and edited
// by hand, and nothing remote can change it.
type Policy struct {
	Stations []*StationEntry `json:"stations"`
}

// StationEntry is one accepted station.
type StationEntry struct {
	Name       string   `json:"name"`
	Npub       string   `json:"npub"`
	URL        string   `json:"url"`
	TunnelURL  string   `json:"tunnel_url"`
	RigID      string   `json:"rig_id"`
	CertSHA256 string   `json:"cert_sha256,omitempty"` // the station's certificate, pinned when no root signs it
	Scopes     []string `json:"scopes"`                // view, input, bidi: what grants may carry
	Admin      bool     `json:"admin"`                 // whether admin grants are accepted
	MaxSession string   `json:"max_session,omitempty"` // a duration such as "8h"
	Hours      string   `json:"hours,omitempty"`       // "09:00-18:00", local time, when holds are allowed
	Record     bool     `json:"record,omitempty"`      // record each hold (screenshots per input) and upload it to the station
}

// PubKey decodes the entry's npub.
func (e *StationEntry) PubKey() (nostr.PubKey, error) { return ParseNpub(e.Npub) }

// Allows says whether the policy lets this station grant s.
func (e *StationEntry) Allows(s station.Scope) bool {
	if s == station.ScopeAdmin {
		return e.Admin
	}
	for _, k := range e.Scopes {
		if k == string(s) {
			return true
		}
	}
	return false
}

// MaxSessionDuration is the entry's limit, or the default.
func (e *StationEntry) MaxSessionDuration() time.Duration {
	if e.MaxSession == "" {
		return DefaultMaxSession
	}
	d, err := time.ParseDuration(e.MaxSession)
	if err != nil || d <= 0 {
		return DefaultMaxSession
	}
	return d
}

// InHours says whether t is inside the entry's allowed hours. No hours
// means always.
func (e *StationEntry) InHours(t time.Time) bool {
	if e.Hours == "" {
		return true
	}
	from, to, ok := strings.Cut(e.Hours, "-")
	start, err1 := minutes(from)
	end, err2 := minutes(to)
	if !ok || err1 != nil || err2 != nil {
		return false // a malformed window allows nothing: fail closed
	}
	now := t.Hour()*60 + t.Minute()
	if start <= end {
		return now >= start && now < end
	}
	return now >= start || now < end // over midnight
}

func minutes(hhmm string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(hhmm), ":")
	if !ok {
		return 0, errors.New("want HH:MM")
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("bad time %q", hhmm)
	}
	return hh*60 + mm, nil
}

// LoadPolicy reads the policy file; a missing file is an empty policy.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Policy{}, nil
	}
	if err != nil {
		return nil, err
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, e := range p.Stations {
		if _, err := e.PubKey(); err != nil {
			return nil, fmt.Errorf("%s: station %q: %w", path, e.Name, err)
		}
	}
	return &p, nil
}

// Save writes the policy file, readable by its owner only.
func (p *Policy) Save(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'), 0o600)
}

// Station finds the entry for a station key.
func (p *Policy) Station(pk nostr.PubKey) *StationEntry {
	for _, e := range p.Stations {
		if k, err := e.PubKey(); err == nil && k == pk {
			return e
		}
	}
	return nil
}

// Set adds an entry or replaces the one with the same key.
func (p *Policy) Set(e *StationEntry) {
	for i, old := range p.Stations {
		if old.Npub == e.Npub {
			p.Stations[i] = e
			return
		}
	}
	p.Stations = append(p.Stations, e)
}

// Keys lists the accepted stations' keys.
func (p *Policy) Keys() []nostr.PubKey {
	var keys []nostr.PubKey
	for _, e := range p.Stations {
		if k, err := e.PubKey(); err == nil {
			keys = append(keys, k)
		}
	}
	return keys
}

// writeFile writes atomically: a temporary file next to the target, then
// a rename.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
