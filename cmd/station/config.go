package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fiatjaf.com/nostr"
	"golang.org/x/crypto/argon2"

	"github.com/VibiumDev/station/rig"
)

// Config is the station's config file, config.json in the data directory.
type Config struct {
	Name         string `json:"name"`
	PasswordHash string `json:"password_hash,omitempty"`
	// Allow lists the Tailscale logins and tags that may sign in by
	// identity in tailnet mode; empty means password only.
	Allow []string `json:"allow,omitempty"`
	// JamsOff keeps Jams off although an agent model is set: the switch
	// on Settings.
	JamsOff bool `json:"jams_off,omitempty"`
	// JamVideoOff leaves the device's screen out of a jam's record: the
	// other switch on Settings. Video is most of a record's size.
	JamVideoOff bool `json:"jam_video_off,omitempty"`
}

// dataDir is where the station keeps everything: config, key, database,
// certificate, tsnet state.
func dataDir(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if d := os.Getenv("STATION_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "station"), nil
}

func loadConfig(dir string) (*Config, error) {
	c := &Config{Name: "station"}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	if c.Name == "" {
		c.Name = "station"
	}
	return c, nil
}

func saveConfig(dir string, c *Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "config.json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "config.json"))
}

// loadKey reads or makes the station's signing key, station.key.
func loadKey(dir string) (nostr.SecretKey, error) {
	sk, created, err := rig.LoadOrCreateKey(filepath.Join(dir, "station.key"))
	if err != nil {
		return sk, err
	}
	if created {
		fmt.Printf("New station key: %s\n", rig.Npub(sk.Public()))
	}
	return sk, nil
}

// Passwords: argon2id, encoded the way other argon2 tools do.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// hashSlots is how many argon2 runs go at once. Each takes 64 MiB and
// the sign-in page is open to anyone, so the rest wait their turn rather
// than take the machine's memory together.
var hashSlots = make(chan struct{}, 2)

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func checkPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
