package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
)

// Rigs lists every enrolled rig with its devices.
func (d *DB) Rigs(ctx context.Context) ([]station.Rig, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, name, pubkey, enrolled_at FROM rigs ORDER BY enrolled_at, id`)
	if err != nil {
		return nil, err
	}
	var rigs []station.Rig
	for rows.Next() {
		r, err := scanRig(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		rigs = append(rigs, r)
	}
	rows.Close()
	for i := range rigs {
		if rigs[i].Devices, err = d.devices(ctx, rigs[i].ID); err != nil {
			return nil, err
		}
	}
	return rigs, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanRig(s scanner) (station.Rig, error) {
	var r station.Rig
	var pk string
	var at int64
	if err := s.Scan(&r.ID, &r.Name, &pk, &at); err != nil {
		return r, err
	}
	var err error
	if r.PubKey, err = nostr.PubKeyFromHex(pk); err != nil {
		return r, err
	}
	r.EnrolledAt = time.Unix(0, at)
	return r, nil
}

func (d *DB) rig(ctx context.Context, where string, arg any) (station.Rig, error) {
	r, err := scanRig(d.db.QueryRowContext(ctx, `SELECT id, name, pubkey, enrolled_at FROM rigs WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return r, station.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.Devices, err = d.devices(ctx, r.ID)
	return r, err
}

// Rig finds a rig by id.
func (d *DB) Rig(ctx context.Context, id string) (station.Rig, error) {
	return d.rig(ctx, "id = ?", id)
}

// RigByKey finds a rig by its public key.
func (d *DB) RigByKey(ctx context.Context, pk nostr.PubKey) (station.Rig, error) {
	return d.rig(ctx, "pubkey = ?", pk.Hex())
}

// PutRig stores a rig and its devices, replacing what was there.
func (d *DB) PutRig(ctx context.Context, rig station.Rig) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO rigs (id, name, pubkey, enrolled_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, pubkey = excluded.pubkey, enrolled_at = excluded.enrolled_at`,
			rig.ID, rig.Name, rig.PubKey.Hex(), rig.EnrolledAt.UnixNano()); err != nil {
			return err
		}
		return setDevices(ctx, tx, rig.ID, rig.Devices)
	})
}

// RemoveRig forgets a rig and its devices.
func (d *DB) RemoveRig(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM rigs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return station.ErrNotFound
	}
	return nil
}

// SetDevices replaces a rig's devices with what it last announced.
func (d *DB) SetDevices(ctx context.Context, rigID string, devices []station.Device) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rigs WHERE id = ?`, rigID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return station.ErrNotFound
		}
		return setDevices(ctx, tx, rigID, devices)
	})
}

func setDevices(ctx context.Context, tx *sql.Tx, rigID string, devices []station.Device) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE rig_id = ?`, rigID); err != nil {
		return err
	}
	for i, dev := range devices {
		caps, _ := json.Marshal(dev.Caps)
		shortcuts, _ := json.Marshal(dev.Shortcuts)
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices (id, rig_id, position, kind, caps, key, shortcuts, input) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			dev.ID, rigID, i, dev.Kind, string(caps), dev.Key, string(shortcuts), dev.Input); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) devices(ctx context.Context, rigID string) ([]station.Device, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, kind, caps, key, shortcuts, input FROM devices WHERE rig_id = ? ORDER BY position`, rigID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Device
	for rows.Next() {
		dev, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dev)
	}
	return out, nil
}

func scanDevice(s scanner) (station.Device, error) {
	var dev station.Device
	var caps, shortcuts string
	if err := s.Scan(&dev.ID, &dev.Kind, &caps, &dev.Key, &shortcuts, &dev.Input); err != nil {
		return dev, err
	}
	json.Unmarshal([]byte(caps), &dev.Caps)
	json.Unmarshal([]byte(shortcuts), &dev.Shortcuts)
	return dev, nil
}

// Device finds a device by id.
func (d *DB) Device(ctx context.Context, id string) (station.Device, error) {
	dev, err := scanDevice(d.db.QueryRowContext(ctx, `SELECT id, kind, caps, key, shortcuts, input FROM devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return dev, station.ErrNotFound
	}
	return dev, err
}
