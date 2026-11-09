package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/VibiumDev/station"
)

const blobCols = `sha256, size, type, name, uploader, user_id, pubkey, hold_id, device_id, rig_id, event_id, public, created_at, steps, frames, duration_ms, thumb, golden`

func scanBlob(s scanner) (station.Blob, error) {
	var b station.Blob
	var public, golden int
	var created, duration int64
	if err := s.Scan(&b.SHA256, &b.Size, &b.Type, &b.Name, &b.Uploader, &b.User, &b.PubKey, &b.HoldID, &b.DeviceID, &b.RigID, &b.EventID, &public, &created, &b.Steps, &b.Frames, &duration, &b.Thumb, &golden); err != nil {
		return b, err
	}
	b.Public = public != 0
	b.Golden = golden != 0
	b.CreatedAt = time.Unix(0, created)
	b.Duration = time.Duration(duration) * time.Millisecond
	return b, nil
}

// PutBlob stores or replaces what the index knows about a blob. A blob
// stored again keeps its golden mark and its tags.
func (d *DB) PutBlob(ctx context.Context, b station.Blob) error {
	public, golden := 0, 0
	if b.Public {
		public = 1
	}
	if b.Golden {
		golden = 1
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO blobs (`+blobCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sha256) DO UPDATE SET size = excluded.size, type = excluded.type, name = excluded.name,
		uploader = excluded.uploader, user_id = excluded.user_id, pubkey = excluded.pubkey, hold_id = excluded.hold_id, device_id = excluded.device_id,
		rig_id = excluded.rig_id, event_id = excluded.event_id, public = excluded.public, created_at = excluded.created_at,
		steps = excluded.steps, frames = excluded.frames, duration_ms = excluded.duration_ms, thumb = excluded.thumb`,
		b.SHA256, b.Size, b.Type, b.Name, b.Uploader, b.User, b.PubKey, b.HoldID, b.DeviceID, b.RigID, b.EventID, public, b.CreatedAt.UnixNano(),
		b.Steps, b.Frames, b.Duration.Milliseconds(), b.Thumb, golden)
	return err
}

// Blob finds a blob by its hash.
func (d *DB) Blob(ctx context.Context, sha string) (station.Blob, error) {
	b, err := scanBlob(d.db.QueryRowContext(ctx, `SELECT `+blobCols+` FROM blobs WHERE sha256 = ?`, sha))
	if errors.Is(err, sql.ErrNoRows) {
		return b, station.ErrNotFound
	}
	if err != nil {
		return b, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT tag FROM blob_tags WHERE sha256 = ? ORDER BY tag`, sha)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return b, err
		}
		b.Tags = append(b.Tags, t)
	}
	return b, rows.Err()
}

// like is a LIKE pattern matching s anywhere, with its wildcards escaped.
func like(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}

const hasTag = `EXISTS (SELECT 1 FROM blob_tags t WHERE t.sha256 = blobs.sha256 AND t.tag`

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
	for _, word := range strings.Fields(q.Text) {
		where += ` AND (name LIKE ? ESCAPE '\' OR type LIKE ? ESCAPE '\' OR uploader LIKE ? ESCAPE '\' OR hold_id LIKE ? ESCAPE '\' OR device_id LIKE ? ESCAPE '\' OR rig_id LIKE ? ESCAPE '\' OR sha256 LIKE ? ESCAPE '\' OR ` + hasTag + ` LIKE ? ESCAPE '\'))`
		p := like(word)
		args = append(args, p, p, p, p, p, p, p, p)
	}
	for _, tag := range q.Tags {
		where += " AND " + hasTag + " = ?)"
		args = append(args, tag)
	}
	if q.Golden {
		where += " AND golden = 1"
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
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	// The tags of every blob at once: a store is small enough that one
	// pass over the tags beats a query per blob.
	tags, err := d.db.QueryContext(ctx, `SELECT sha256, tag FROM blob_tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer tags.Close()
	by := map[string][]string{}
	for tags.Next() {
		var sha, t string
		if err := tags.Scan(&sha, &t); err != nil {
			return nil, err
		}
		by[sha] = append(by[sha], t)
	}
	for i := range out {
		out[i].Tags = by[out[i].SHA256]
	}
	return out, tags.Err()
}

// DeleteBlob forgets a blob and its tags; the file event it names stays
// in the event store.
func (d *DB) DeleteBlob(ctx context.Context, sha string) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM blob_tags WHERE sha256 = ?`, sha); err != nil {
		return err
	}
	_, err := d.db.ExecContext(ctx, `DELETE FROM blobs WHERE sha256 = ?`, sha)
	return err
}

// SetBlobGolden marks a blob golden, or plain again.
func (d *DB) SetBlobGolden(ctx context.Context, sha string, golden bool) error {
	v := 0
	if golden {
		v = 1
	}
	res, err := d.db.ExecContext(ctx, `UPDATE blobs SET golden = ? WHERE sha256 = ?`, v, sha)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return station.ErrNotFound
	}
	return nil
}

// SetBlobTags replaces a blob's tags.
func (d *DB) SetBlobTags(ctx context.Context, sha string, tags []string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM blobs WHERE sha256 = ?`, sha).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return station.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blob_tags WHERE sha256 = ?`, sha); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blob_tags (sha256, tag) VALUES (?, ?)`, sha, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BlobTagCounts lists the tags in use, most used first, then by name.
func (d *DB) BlobTagCounts(ctx context.Context) ([]station.TagCount, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT tag, count(*) FROM blob_tags GROUP BY tag ORDER BY count(*) DESC, tag ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.TagCount
	for rows.Next() {
		var c station.TagCount
		if err := rows.Scan(&c.Tag, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
