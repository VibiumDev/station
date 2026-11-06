// Package sqlite is Station's storage: one SQLite file holding the event
// store, rigs and devices, holds, the Record Store's index, API tokens,
// enrollment codes and the audit log. Pure Go through modernc.org/sqlite.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// DB is an open store.
type DB struct {
	db *sql.DB
}

// Open opens or creates the database at path. ":memory:" is a private
// in-memory database for tests.
func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)", "synchronous(NORMAL)"},
	}.Encode()
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite serializes writers anyway, and a single
	// connection keeps an in-memory database alive and transactions simple.
	db.SetMaxOpenConns(1)
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the file. No error: the event store interface has none,
// and a close that fails has nothing left to do.
func (d *DB) Close() { d.db.Close() }

// Init satisfies the event store interface; Open already did the work.
func (d *DB) Init() error { return nil }

const schema = `
CREATE TABLE IF NOT EXISTS events (
	id         TEXT PRIMARY KEY,
	pubkey     TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	kind       INTEGER NOT NULL,
	raw        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_kind ON events(kind, created_at);
CREATE INDEX IF NOT EXISTS events_pubkey ON events(pubkey, kind, created_at);
CREATE TABLE IF NOT EXISTS event_tags (
	event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	name     TEXT NOT NULL,
	value    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS event_tags_lookup ON event_tags(name, value, event_id);
CREATE TABLE IF NOT EXISTS rigs (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	pubkey      TEXT NOT NULL UNIQUE,
	enrolled_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
	id        TEXT PRIMARY KEY,
	rig_id    TEXT NOT NULL REFERENCES rigs(id) ON DELETE CASCADE,
	position  INTEGER NOT NULL,
	kind      TEXT NOT NULL,
	caps      TEXT NOT NULL,
	key       TEXT NOT NULL DEFAULT '',
	shortcuts TEXT NOT NULL DEFAULT '[]',
	input     TEXT NOT NULL DEFAULT '',
	extras    TEXT NOT NULL DEFAULT '',
	label     TEXT NOT NULL DEFAULT '',
	platform  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS hold_seq (
	device_id TEXT PRIMARY KEY,
	seq       INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS holds (
	id          TEXT PRIMARY KEY,
	device_id   TEXT NOT NULL,
	rig_id      TEXT NOT NULL,
	seq         INTEGER NOT NULL,
	subject     TEXT NOT NULL,
	scopes      TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	active_at   INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	released_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS holds_device ON holds(device_id, released_at, seq);
CREATE TABLE IF NOT EXISTS api_tokens (
	name       TEXT PRIMARY KEY,
	hash       TEXT NOT NULL UNIQUE,
	read_only  INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	used_at    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS enroll_codes (
	hash       TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	used_at    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS blobs (
	sha256     TEXT PRIMARY KEY,
	size       INTEGER NOT NULL,
	type       TEXT NOT NULL,
	name       TEXT NOT NULL DEFAULT '',
	uploader   TEXT NOT NULL,
	pubkey     TEXT NOT NULL,
	hold_id    TEXT NOT NULL DEFAULT '',
	device_id  TEXT NOT NULL DEFAULT '',
	rig_id     TEXT NOT NULL DEFAULT '',
	event_id   TEXT NOT NULL DEFAULT '',
	public     INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	steps      INTEGER NOT NULL DEFAULT 0,
	frames     INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	thumb      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS blobs_device ON blobs(device_id, created_at);
CREATE INDEX IF NOT EXISTS blobs_hold ON blobs(hold_id);
CREATE INDEX IF NOT EXISTS blobs_age ON blobs(created_at);
CREATE TABLE IF NOT EXISTS audit (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	at        INTEGER NOT NULL,
	subject   TEXT NOT NULL,
	action    TEXT NOT NULL,
	device_id TEXT NOT NULL,
	hold_id  TEXT NOT NULL,
	detail    TEXT NOT NULL
);
`

func (d *DB) migrate() error {
	// Leases became holds in R5: a database from before carries its rows
	// over under the new names.
	for _, r := range [][2]string{{"leases", "holds"}, {"lease_seq", "hold_seq"}} {
		if has, err := d.hasTable(r[0]); err != nil {
			return err
		} else if has {
			if _, err := d.db.Exec("ALTER TABLE " + r[0] + " RENAME TO " + r[1]); err != nil {
				return fmt.Errorf("schema: rename %s: %w", r[0], err)
			}
		}
	}
	if has, err := d.hasColumn("audit", "lease_id"); err == nil && has {
		if _, err := d.db.Exec("ALTER TABLE audit RENAME COLUMN lease_id TO hold_id"); err != nil {
			return fmt.Errorf("schema: rename audit.lease_id: %w", err)
		}
	}
	if _, err := d.db.Exec(schema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	// Columns added since the first release, for databases made before.
	for _, c := range []struct{ table, column, def string }{
		{"devices", "input", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "extras", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "label", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "platform", "TEXT NOT NULL DEFAULT ''"},
		{"blobs", "steps", "INTEGER NOT NULL DEFAULT 0"},
		{"blobs", "frames", "INTEGER NOT NULL DEFAULT 0"},
		{"blobs", "duration_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"blobs", "thumb", "TEXT NOT NULL DEFAULT ''"},
	} {
		has, err := d.hasColumn(c.table, c.column)
		if err != nil {
			return err
		}
		if !has {
			if _, err := d.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.def)); err != nil {
				return fmt.Errorf("schema: add %s.%s: %w", c.table, c.column, err)
			}
		}
	}
	return nil
}

func (d *DB) hasTable(table string) (bool, error) {
	var n int
	err := d.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n)
	return n > 0, err
}

func (d *DB) hasColumn(table, column string) (bool, error) {
	rows, err := d.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// tx runs fn in a transaction.
func (d *DB) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
