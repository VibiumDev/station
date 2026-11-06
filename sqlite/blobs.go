package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/VibiumDev/station"
)

const blobCols = `sha256, size, type, name, uploader, pubkey, hold_id, device_id, rig_id, event_id, public, created_at`

func scanBlob(s scanner) (station.Blob, error) {
	var b station.Blob
	var public int
	var created int64
	if err := s.Scan(&b.SHA256, &b.Size, &b.Type, &b.Name, &b.Uploader, &b.PubKey, &b.HoldID, &b.DeviceID, &b.RigID, &b.EventID, &public, &created); err != nil {
		return b, err
	}
	b.Public = public != 0
	b.CreatedAt = time.Unix(0, created)
	return b, nil
}

// PutBlob stores or replaces what the index knows about a blob.
func (d *DB) PutBlob(ctx context.Context, b station.Blob) error {
	public := 0
	if b.Public {
		public = 1
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO blobs (`+blobCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sha256) DO UPDATE SET size = excluded.size, type = excluded.type, name = excluded.name,
		uploader = excluded.uploader, pubkey = excluded.pubkey, hold_id = excluded.hold_id, device_id = excluded.device_id,
		rig_id = excluded.rig_id, event_id = excluded.event_id, public = excluded.public, created_at = excluded.created_at`,
		b.SHA256, b.Size, b.Type, b.Name, b.Uploader, b.PubKey, b.HoldID, b.DeviceID, b.RigID, b.EventID, public, b.CreatedAt.UnixNano())
	return err
}

// Blob finds a blob by its hash.
func (d *DB) Blob(ctx context.Context, sha string) (station.Blob, error) {
	b, err := scanBlob(d.db.QueryRowContext(ctx, `SELECT `+blobCols+` FROM blobs WHERE sha256 = ?`, sha))
	if errors.Is(err, sql.ErrNoRows) {
		return b, station.ErrNotFound
	}
	return b, err
}

// Blobs lists blobs, newest first unless the query says otherwise.
func (d *DB) Blobs(ctx context.Context, q station.BlobQuery) ([]station.Blob, error) {
	where, args := "1 = 1", []any{}
	if q.DeviceID != "" {
		where += " AND device_id = ?"
		args = append(args, q.DeviceID)
	}
	if q.HoldID != "" {
		where += " AND hold_id = ?"
		args = append(args, q.HoldID)
	}
	if q.PubKey != "" {
		where += " AND pubkey = ?"
		args = append(args, q.PubKey)
	}
	if !q.Before.IsZero() {
		where += " AND created_at < ?"
		args = append(args, q.Before.UnixNano())
	}
	order := " ORDER BY created_at DESC"
	if q.OldestFirst {
		order = " ORDER BY created_at ASC"
	}
	if q.Limit > 0 {
		order += " LIMIT ?"
		args = append(args, q.Limit)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT `+blobCols+` FROM blobs WHERE `+where+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Blob
	for rows.Next() {
		b, err := scanBlob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBlob forgets a blob; the file event it names stays in the
// event store.
func (d *DB) DeleteBlob(ctx context.Context, sha string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM blobs WHERE sha256 = ?`, sha)
	return err
}

// SetBlobPublic marks a blob readable by anyone, or not.
func (d *DB) SetBlobPublic(ctx context.Context, sha string, public bool) error {
	v := 0
	if public {
		v = 1
	}
	res, err := d.db.ExecContext(ctx, `UPDATE blobs SET public = ? WHERE sha256 = ?`, v, sha)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return station.ErrNotFound
	}
	return nil
}

// BlobTotal is how many blobs the index holds and their size in bytes.
func (d *DB) BlobTotal(ctx context.Context) (int, int64, error) {
	var n int
	var size sql.NullInt64
	err := d.db.QueryRowContext(ctx, `SELECT count(*), sum(size) FROM blobs`).Scan(&n, &size)
	return n, size.Int64, err
}

// HoldsOf lists the holds a subject has had since a time, newest first:
// what an upload without a named hold is matched against.
func (d *DB) HoldsOf(ctx context.Context, subject string, since time.Time) ([]station.Hold, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+holdCols+` FROM holds WHERE subject = ? AND created_at >= ? ORDER BY created_at DESC`, subject, since.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Hold
	for rows.Next() {
		l, err := scanHold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
