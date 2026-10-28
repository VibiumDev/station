package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/VibiumDev/station"
)

const leaseCols = `id, device_id, rig_id, seq, subject, scopes, created_at, active_at, expires_at, released_at`

func scanLease(s scanner) (station.Lease, error) {
	var l station.Lease
	var scopes string
	var created, active, expires, released int64
	if err := s.Scan(&l.ID, &l.DeviceID, &l.RigID, &l.Seq, &l.Subject, &scopes, &created, &active, &expires, &released); err != nil {
		return l, err
	}
	l.Scopes, _ = station.ParseScopes(strings.Fields(scopes))
	l.CreatedAt, l.ActiveAt, l.ExpiresAt = time.Unix(0, created), time.Unix(0, active), time.Unix(0, expires)
	if released != 0 {
		l.ReleasedAt = time.Unix(0, released)
	}
	return l, nil
}

// Current is the lease held on a device: not released and not past its
// end, the highest sequence number if several qualify.
func (d *DB) Current(ctx context.Context, deviceID string) (station.Lease, error) {
	l, err := scanLease(d.db.QueryRowContext(ctx, `SELECT `+leaseCols+` FROM leases
		WHERE device_id = ? AND released_at = 0 AND expires_at > ? ORDER BY seq DESC LIMIT 1`, deviceID, time.Now().UnixNano()))
	if errors.Is(err, sql.ErrNoRows) {
		return l, station.ErrNoLease
	}
	return l, err
}

// Create stores a lease with the device's next sequence number.
func (d *DB) Create(ctx context.Context, l station.Lease) (station.Lease, error) {
	err := d.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO lease_seq (device_id, seq) VALUES (?, 1)
			ON CONFLICT(device_id) DO UPDATE SET seq = seq + 1 RETURNING seq`, l.DeviceID).Scan(&l.Seq); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO leases (`+leaseCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
			l.ID, l.DeviceID, l.RigID, l.Seq, l.Subject, strings.Join(station.ScopeStrings(l.Scopes), " "),
			l.CreatedAt.UnixNano(), l.ActiveAt.UnixNano(), l.ExpiresAt.UnixNano())
		return err
	})
	return l, err
}

// Lease finds a lease by id.
func (d *DB) Lease(ctx context.Context, id string) (station.Lease, error) {
	l, err := scanLease(d.db.QueryRowContext(ctx, `SELECT `+leaseCols+` FROM leases WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, station.ErrNotFound
	}
	return l, err
}

// Touch records activity on a lease.
func (d *DB) Touch(ctx context.Context, id string, at time.Time) error {
	return d.update(ctx, `UPDATE leases SET active_at = ? WHERE id = ? AND released_at = 0`, at.UnixNano(), id)
}

// Release ends a lease.
func (d *DB) Release(ctx context.Context, id string, at time.Time) error {
	return d.update(ctx, `UPDATE leases SET released_at = ? WHERE id = ? AND released_at = 0`, at.UnixNano(), id)
}

func (d *DB) update(ctx context.Context, q string, args ...any) error {
	res, err := d.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return station.ErrNotFound
	}
	return nil
}

// Held lists every lease not released and not past its end.
func (d *DB) Held(ctx context.Context) ([]station.Lease, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+leaseCols+` FROM leases WHERE released_at = 0 AND expires_at > ? ORDER BY device_id, seq`, time.Now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}
