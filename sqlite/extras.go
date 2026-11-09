package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/VibiumDev/station"
)

// Audit appends an entry to the audit log.
func (d *DB) Audit(ctx context.Context, e station.AuditEntry) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	d.db.ExecContext(ctx, `INSERT INTO audit (at, subject, action, device_id, hold_id, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At.UnixNano(), e.Subject, e.Action, e.DeviceID, e.HoldID, e.Detail)
}

// AuditEntries lists the newest entries.
func (d *DB) AuditEntries(ctx context.Context, limit int) ([]station.AuditEntry, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT at, subject, action, device_id, hold_id, detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.AuditEntry
	for rows.Next() {
		var e station.AuditEntry
		var at int64
		if err := rows.Scan(&at, &e.Subject, &e.Action, &e.DeviceID, &e.HoldID, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.Unix(0, at)
		out = append(out, e)
	}
	return out, nil
}

// APIToken is a named token an agent signs in with. The secret is shown
// once, at creation; only its hash is kept. A token minted for a jam is
// bound to one device and expires with the jam.
type APIToken struct {
	Name      string
	Level     station.TokenLevel // watch, drive or admin
	Device    string             // the one device the token may act on, or ""
	ExpiresAt time.Time          // zero for a token that lasts until revoked
	CreatedAt time.Time
	UsedAt    time.Time
	UserID    string // the user the token acts as; empty means the owner
}

// Expired says whether the token has run out at t.
func (t APIToken) Expired(at time.Time) bool { return !t.ExpiresAt.IsZero() && !at.Before(t.ExpiresAt) }

// ErrTokenExists says a token of that name already exists.
var ErrTokenExists = errors.New("a token with that name exists")

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CreateToken makes a token at a level for a user and returns its
// secret; an empty user is the owner's.
func (d *DB) CreateToken(ctx context.Context, name string, level station.TokenLevel, userID string) (string, error) {
	return d.CreateScopedToken(ctx, name, level, "", time.Time{}, userID)
}

// CreateScopedToken makes a token bound to one device and expiring at a
// time, either of which may be left empty, for a user, and returns its
// secret.
func (d *DB) CreateScopedToken(ctx context.Context, name string, level station.TokenLevel, device string, expires time.Time, userID string) (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	secret := "stk_" + base64.RawURLEncoding.EncodeToString(b[:])
	if level == "" {
		level = station.TokenDrive
	}
	ro := 0
	if level == station.TokenWatch {
		ro = 1
	}
	var exp int64
	if !expires.IsZero() {
		exp = expires.UnixNano()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO api_tokens (name, hash, read_only, level, created_at, device, expires_at, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		name, hashSecret(secret), ro, string(level), time.Now().UnixNano(), device, exp, userID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return "", ErrTokenExists
	}
	return secret, err
}

const tokenCols = `name, read_only, level, created_at, used_at, device, expires_at, user_id`

func scanToken(s scanner) (APIToken, error) {
	var t APIToken
	var ro int
	var level string
	var created, used, expires int64
	if err := s.Scan(&t.Name, &ro, &level, &created, &used, &t.Device, &expires, &t.UserID); err != nil {
		return t, err
	}
	t.Level = station.TokenLevel(level)
	if t.Level == "" { // from before levels: read-only or control
		t.Level = station.TokenDrive
		if ro == 1 {
			t.Level = station.TokenWatch
		}
	}
	t.CreatedAt = time.Unix(0, created)
	if used != 0 {
		t.UsedAt = time.Unix(0, used)
	}
	if expires != 0 {
		t.ExpiresAt = time.Unix(0, expires)
	}
	return t, nil
}

// LookupToken finds the token a secret belongs to and notes the use. An
// expired token is not found.
func (d *DB) LookupToken(ctx context.Context, secret string) (APIToken, error) {
	t, err := scanToken(d.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_tokens WHERE hash = ?`, hashSecret(secret)))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && t.Expired(time.Now())) {
		return t, station.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	d.db.ExecContext(ctx, `UPDATE api_tokens SET used_at = ? WHERE name = ?`, time.Now().UnixNano(), t.Name)
	return t, nil
}

// Tokens lists the tokens the owner made; the ones minted for jams, bound
// to a device, are the station's own and are left out.
func (d *DB) Tokens(ctx context.Context) ([]APIToken, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+tokenCols+` FROM api_tokens WHERE device = '' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// RevokeToken removes a token.
func (d *DB) RevokeToken(ctx context.Context, name string) error {
	return d.update(ctx, `DELETE FROM api_tokens WHERE name = ?`, name)
}

// Enrollment codes: single use, short lived, hashed at rest.

// ErrCode says an enrollment code is unknown, used or expired.
var ErrCode = errors.New("enrollment code unknown, used or expired")

// SaveCode stores a code that expires at the given time.
func (d *DB) SaveCode(ctx context.Context, code string, expires time.Time) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO enroll_codes (hash, created_at, expires_at) VALUES (?, ?, ?)`,
		hashSecret(code), time.Now().UnixNano(), expires.UnixNano())
	return err
}

// UseCode burns a code; it must be unused and not expired.
func (d *DB) UseCode(ctx context.Context, code string, now time.Time) error {
	res, err := d.db.ExecContext(ctx, `UPDATE enroll_codes SET used_at = ? WHERE hash = ? AND used_at = 0 AND expires_at > ?`,
		now.UnixNano(), hashSecret(code), now.UnixNano())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCode
	}
	return nil
}

// OpenCodes counts the codes still usable.
func (d *DB) OpenCodes(ctx context.Context, now time.Time) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM enroll_codes WHERE used_at = 0 AND expires_at > ?`, now.UnixNano()).Scan(&n)
	return n, err
}
