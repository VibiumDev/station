package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"fiatjaf.com/nostr"
)

// SaveEvent stores ev verbatim, with its single-letter tags indexed.
func (d *DB) SaveEvent(ev nostr.Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO events (id, pubkey, created_at, kind, raw) VALUES (?, ?, ?, ?, ?)`,
		ev.ID.Hex(), ev.PubKey.Hex(), int64(ev.CreatedAt), int(ev.Kind), string(raw)); err != nil {
		return fmt.Errorf("save event: %w", err)
	}
	for _, t := range ev.Tags {
		if len(t) >= 2 && len(t[0]) == 1 {
			if _, err := tx.Exec(`INSERT INTO event_tags (event_id, name, value) VALUES (?, ?, ?)`, ev.ID.Hex(), t[0], t[1]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// DeleteEvent removes one event.
func (d *DB) DeleteEvent(id nostr.ID) error {
	_, err := d.db.Exec(`DELETE FROM events WHERE id = ?`, id.Hex())
	return err
}

// ReplaceEvent stores a replaceable or addressable event in place of the
// older one it supersedes. An older event than the one stored is dropped,
// and what was deleted is returned.
func (d *DB) ReplaceEvent(ev nostr.Event) ([]nostr.Event, error) {
	f := nostr.Filter{Kinds: []nostr.Kind{ev.Kind}, Authors: []nostr.PubKey{ev.PubKey}}
	if ev.Kind.IsAddressable() {
		f.Tags = nostr.TagMap{"d": []string{ev.Tags.GetD()}}
	}
	var deleted []nostr.Event
	for old := range d.QueryEvents(f, 0) {
		if nostr.IsOlder(ev, old) {
			// What we have is newer: keep it.
			return nil, nil
		}
		if err := d.DeleteEvent(old.ID); err != nil {
			return deleted, err
		}
		deleted = append(deleted, old)
	}
	return deleted, d.SaveEvent(ev)
}

// CountEvents counts the events a filter matches.
func (d *DB) CountEvents(f nostr.Filter) (uint32, error) {
	where, args := whereFor(f)
	var n uint32
	err := d.db.QueryRow(`SELECT COUNT(*) FROM events e`+where, args...).Scan(&n)
	return n, err
}

// QueryEvents returns the events a filter matches, newest first. The
// filter's limit applies, capped by maxLimit when that is positive.
func (d *DB) QueryEvents(f nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if f.LimitZero {
			return
		}
		where, args := whereFor(f)
		limit := f.Limit
		if maxLimit > 0 && (limit == 0 || limit > maxLimit) {
			limit = maxLimit
		}
		q := `SELECT raw FROM events e` + where + ` ORDER BY created_at DESC, id ASC`
		if limit > 0 {
			q += fmt.Sprintf(" LIMIT %d", limit)
		}
		rows, err := d.db.Query(q, args...)
		if err != nil {
			return
		}
		// Read everything first: the single connection must be free again
		// before the caller does anything else with the store.
		var out []nostr.Event
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				break
			}
			var ev nostr.Event
			if err := json.Unmarshal([]byte(raw), &ev); err == nil {
				out = append(out, ev)
			}
		}
		rows.Close()
		for _, ev := range out {
			if !yield(ev) {
				return
			}
		}
	}
}

// whereFor turns a NIP-01 filter into SQL.
func whereFor(f nostr.Filter) (string, []any) {
	var conds []string
	var args []any
	in := func(col string, n int) string {
		return col + " IN (" + strings.TrimRight(strings.Repeat("?,", n), ",") + ")"
	}
	if len(f.IDs) > 0 {
		conds = append(conds, in("e.id", len(f.IDs)))
		for _, id := range f.IDs {
			args = append(args, id.Hex())
		}
	}
	if len(f.Authors) > 0 {
		conds = append(conds, in("e.pubkey", len(f.Authors)))
		for _, a := range f.Authors {
			args = append(args, a.Hex())
		}
	}
	if len(f.Kinds) > 0 {
		conds = append(conds, in("e.kind", len(f.Kinds)))
		for _, k := range f.Kinds {
			args = append(args, int(k))
		}
	}
	for name, values := range f.Tags {
		if len(values) == 0 {
			continue
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM event_tags t WHERE t.event_id = e.id AND t.name = ? AND "+in("t.value", len(values))+")")
		args = append(args, name)
		for _, v := range values {
			args = append(args, v)
		}
	}
	if f.Since != 0 {
		conds = append(conds, "e.created_at >= ?")
		args = append(args, int64(f.Since))
	}
	if f.Until != 0 {
		conds = append(conds, "e.created_at <= ?")
		args = append(args, int64(f.Until))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

var _ = sql.ErrNoRows
