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
// once, at creation; only its hash is kept.
type APIToken struct {
	Name      string
	ReadOnly  bool
	CreatedAt time.Time
	UsedAt    time.Time
}

// ErrTokenExists says a token of that name already exists.
var ErrTokenExists = errors.New("a token with that name exists")

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CreateToken makes a token and returns its secret.
func (d *DB) CreateToken(ctx context.Context, name string, readOnly bool) (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	secret := "stk_" + base64.RawURLEncoding.EncodeToString(b[:])
	ro := 0
	if readOnly {
		ro = 1
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO api_tokens (name, hash, read_only, created_at) VALUES (?, ?, ?, ?)`,
		name, hashSecret(secret), ro, time.Now().UnixNano())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return "", ErrTokenExists
	}
	return secret, err
}

// LookupToken finds the token a secret belongs to and notes the use.
func (d *DB) LookupToken(ctx context.Context, secret string) (APIToken, error) {
	var t APIToken
	var ro int
	var created, used int64
	err := d.db.QueryRowContext(ctx, `SELECT name, read_only, created_at, used_at FROM api_tokens WHERE hash = ?`, hashSecret(secret)).
		Scan(&t.Name, &ro, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return t, station.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.ReadOnly = ro == 1
	t.CreatedAt = time.Unix(0, created)
	if used != 0 {
		t.UsedAt = time.Unix(0, used)
	}
	d.db.ExecContext(ctx, `UPDATE api_tokens SET used_at = ? WHERE name = ?`, time.Now().UnixNano(), t.Name)
	return t, nil
}

// Tokens lists the tokens.
func (d *DB) Tokens(ctx context.Context) ([]APIToken, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT name, read_only, created_at, used_at FROM api_tokens ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var ro int
		var created, used int64
		if err := rows.Scan(&t.Name, &ro, &created, &used); err != nil {
			return nil, err
		}
		t.ReadOnly = ro == 1
		t.CreatedAt = time.Unix(0, created)
		if used != 0 {
			t.UsedAt = time.Unix(0, used)
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
