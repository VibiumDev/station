package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/VibiumDev/station"
)

const jamCols = `id, subject, device_id, model, prompt, mode, status, took, result, summary, hold_id, actions, tokens_in, tokens_out, error, created_at, ended_at`

func scanJam(s scanner) (station.Jam, error) {
	var j station.Jam
	var created, ended int64
	if err := s.Scan(&j.ID, &j.Subject, &j.DeviceID, &j.Model, &j.Prompt, &j.Mode, &j.Status, &j.Took, &j.Result, &j.Summary,
		&j.HoldID, &j.Actions, &j.TokensIn, &j.TokensOut, &j.Error, &created, &ended); err != nil {
		return j, err
	}
	j.CreatedAt = time.Unix(0, created)
	if ended != 0 {
		j.EndedAt = time.Unix(0, ended)
	}
	return j, nil
}

// CreateJam stores a new jam as it starts.
func (d *DB) CreateJam(ctx context.Context, j station.Jam) error {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO jams (`+jamCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		j.ID, j.Subject, j.DeviceID, j.Model, j.Prompt, j.Mode, j.Status, j.Took, j.Result, j.Summary,
		j.HoldID, j.Actions, j.TokensIn, j.TokensOut, j.Error, j.CreatedAt.UnixNano())
	return err
}

// UpdateJam writes what changes while a jam runs and when it ends.
func (d *DB) UpdateJam(ctx context.Context, j station.Jam) error {
	var ended int64
	if !j.EndedAt.IsZero() {
		ended = j.EndedAt.UnixNano()
	}
	return d.update(ctx, `UPDATE jams SET status = ?, took = ?, result = ?, summary = ?, hold_id = ?, actions = ?, tokens_in = ?, tokens_out = ?, error = ?, ended_at = ? WHERE id = ?`,
		j.Status, j.Took, j.Result, j.Summary, j.HoldID, j.Actions, j.TokensIn, j.TokensOut, j.Error, ended, j.ID)
}

// Jam finds a jam by id.
func (d *DB) Jam(ctx context.Context, id string) (station.Jam, error) {
	j, err := scanJam(d.db.QueryRowContext(ctx, `SELECT `+jamCols+` FROM jams WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, station.ErrNotFound
	}
	return j, err
}

// Jams lists jams newest first: a subject's own, or everyone's when the
// subject is empty.
func (d *DB) Jams(ctx context.Context, subject string, limit int) ([]station.Jam, error) {
	q := `SELECT ` + jamCols + ` FROM jams`
	var args []any
	if subject != "" {
		q += ` WHERE subject = ?`
		args = append(args, subject)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Jam
	for rows.Next() {
		j, err := scanJam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// OpenJams lists the jams still marked running, to close at startup
// whatever a previous run left open.
func (d *DB) OpenJams(ctx context.Context) ([]station.Jam, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+jamCols+` FROM jams WHERE status = 'running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Jam
	for rows.Next() {
		j, err := scanJam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// AddJamEvent appends one event to a jam's transcript.
func (d *DB) AddJamEvent(ctx context.Context, e station.JamEvent) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO jam_events (jam_id, seq, at, kind, data) VALUES (?, ?, ?, ?, ?)`,
		e.JamID, e.Seq, e.At.UnixNano(), e.Kind, string(data))
	return err
}

// JamEvents is a jam's transcript in order, from the event after seq.
func (d *DB) JamEvents(ctx context.Context, jamID string, after int) ([]station.JamEvent, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT seq, at, kind, data FROM jam_events WHERE jam_id = ? AND seq > ? ORDER BY seq`, jamID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.JamEvent
	for rows.Next() {
		e := station.JamEvent{JamID: jamID}
		var at int64
		var data string
		if err := rows.Scan(&e.Seq, &at, &e.Kind, &data); err != nil {
			return nil, err
		}
		e.At = time.Unix(0, at)
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
