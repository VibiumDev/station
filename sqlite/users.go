package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/VibiumDev/station"
)

// Users, teams and device access: who may sign in, and what a regular
// user's teams let them see and do.

const userCols = `id, login, name, password_hash, admin, locked, created_at, last_login, totp_secret, totp_at, expires_at`

func scanUser(s scanner) (station.User, string, error) {
	var u station.User
	var hash, secret string
	var admin, locked int
	var created, last, totpAt, expires int64
	if err := s.Scan(&u.ID, &u.Login, &u.Name, &hash, &admin, &locked, &created, &last, &secret, &totpAt, &expires); err != nil {
		return u, "", err
	}
	u.Admin, u.Locked = admin != 0, locked != 0
	u.HasPassword, u.TwoFactor = hash != "", secret != ""
	u.CreatedAt = time.Unix(0, created)
	if last != 0 {
		u.LastLogin = time.Unix(0, last)
	}
	if totpAt != 0 {
		u.TwoFactorAt = time.Unix(0, totpAt)
	}
	if expires != 0 {
		u.ExpiresAt = time.Unix(0, expires)
	}
	return u, hash, nil
}

// ErrLoginTaken says a user with that login exists.
var ErrLoginTaken = errors.New("a user with that login exists")

// ErrTeamExists says a team with that name exists.
var ErrTeamExists = errors.New("a team with that name exists")

// Users lists every user, by login, with their teams.
func (d *DB) Users(ctx context.Context) ([]station.User, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY login`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.User
	for rows.Next() {
		u, _, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	teams, err := d.db.QueryContext(ctx, `SELECT user_id, team_id FROM team_members ORDER BY team_id`)
	if err != nil {
		return nil, err
	}
	defer teams.Close()
	by := map[string][]string{}
	for teams.Next() {
		var user, team string
		if err := teams.Scan(&user, &team); err != nil {
			return nil, err
		}
		by[user] = append(by[user], team)
	}
	for i := range out {
		out[i].Teams = by[out[i].ID]
	}
	return out, teams.Err()
}

func (d *DB) userWhere(ctx context.Context, where string, arg any) (station.User, string, error) {
	u, hash, err := scanUser(d.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return u, "", station.ErrNotFound
	}
	if err != nil {
		return u, "", err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT team_id FROM team_members WHERE user_id = ? ORDER BY team_id`, u.ID)
	if err != nil {
		return u, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return u, "", err
		}
		u.Teams = append(u.Teams, t)
	}
	return u, hash, rows.Err()
}

// User finds a user by id.
func (d *DB) User(ctx context.Context, id string) (station.User, error) {
	u, _, err := d.userWhere(ctx, "id = ?", id)
	return u, err
}

// UserByLogin finds a user by login, whatever its case.
func (d *DB) UserByLogin(ctx context.Context, login string) (station.User, error) {
	u, _, err := d.userWhere(ctx, "login = ?", strings.ToLower(strings.TrimSpace(login)))
	return u, err
}

// Credentials is a user and their password hash, for signing in.
func (d *DB) Credentials(ctx context.Context, login string) (station.User, string, error) {
	return d.userWhere(ctx, "login = ?", strings.ToLower(strings.TrimSpace(login)))
}

// PutUser creates a user, or updates everything but the password, the
// second factor and the teams of one that exists.
func (d *DB) PutUser(ctx context.Context, u station.User) error {
	admin, locked := 0, 0
	if u.Admin {
		admin = 1
	}
	if u.Locked {
		locked = 1
	}
	created := u.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	var expires int64
	if !u.ExpiresAt.IsZero() {
		expires = u.ExpiresAt.UnixNano()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO users (id, login, name, admin, locked, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET login = excluded.login, name = excluded.name, admin = excluded.admin, locked = excluded.locked, expires_at = excluded.expires_at`,
		u.ID, strings.ToLower(strings.TrimSpace(u.Login)), strings.TrimSpace(u.Name), admin, locked, created.UnixNano(), expires)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrLoginTaken
	}
	return err
}

// RemoveUser removes a user, their memberships and their tokens.
func (d *DB) RemoveUser(ctx context.Context, id string) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id = ?`, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return station.ErrNotFound
		}
		return nil
	})
}

// SetPassword stores a user's password hash.
func (d *DB) SetPassword(ctx context.Context, id, hash string) error {
	return d.update(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
}

// PasswordHash is a user's password hash, empty when none is set.
func (d *DB) PasswordHash(ctx context.Context, id string) (string, error) {
	_, hash, err := d.userWhere(ctx, "id = ?", id)
	return hash, err
}

// JamNotes is a user's standing notes for the jams they start.
func (d *DB) JamNotes(ctx context.Context, id string) (string, error) {
	var notes string
	err := d.db.QueryRowContext(ctx, `SELECT jam_notes FROM users WHERE id = ?`, id).Scan(&notes)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return notes, err
}

// SetJamNotes replaces a user's standing notes.
func (d *DB) SetJamNotes(ctx context.Context, id, notes string) error {
	return d.update(ctx, `UPDATE users SET jam_notes = ? WHERE id = ?`, notes, id)
}

// TouchUser notes a sign-in.
func (d *DB) TouchUser(ctx context.Context, id string, at time.Time) error {
	return d.update(ctx, `UPDATE users SET last_login = ? WHERE id = ?`, at.UnixNano(), id)
}

// SetSecondFactor stores a user's confirmed authenticator secret, or
// forgets it with an empty one; either way no step has been used yet.
func (d *DB) SetSecondFactor(ctx context.Context, id, secret string, at time.Time) error {
	var when int64
	if secret != "" {
		when = at.UnixNano()
	}
	return d.update(ctx, `UPDATE users SET totp_secret = ?, totp_at = ?, totp_step = 0 WHERE id = ?`, secret, when, id)
}

// SecondFactor is a user's authenticator secret, empty when none is
// set, and the last time step a code was accepted for.
func (d *DB) SecondFactor(ctx context.Context, id string) (string, int64, error) {
	var secret string
	var step int64
	err := d.db.QueryRowContext(ctx, `SELECT totp_secret, totp_step FROM users WHERE id = ?`, id).Scan(&secret, &step)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, station.ErrNotFound
	}
	return secret, step, err
}

// AcceptStep remembers a step a code was accepted for; it says no when
// a code from that step or a later one was already used, so a code seen
// on the wire cannot be replayed.
func (d *DB) AcceptStep(ctx context.Context, id string, step int64) (bool, error) {
	res, err := d.db.ExecContext(ctx, `UPDATE users SET totp_step = ? WHERE id = ? AND totp_step < ?`, step, id, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetBackupCodes replaces a user's backup codes with the hashes given.
func (d *DB) SetBackupCodes(ctx context.Context, id string, hashes []string) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM backup_codes WHERE user_id = ?`, id); err != nil {
			return err
		}
		for _, h := range hashes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO backup_codes (user_id, hash) VALUES (?, ?)`, id, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// UseBackupCode spends the backup code with that hash, if the user has
// it unused, and says how many are left.
func (d *DB) UseBackupCode(ctx context.Context, id, hash string) (int, bool, error) {
	res, err := d.db.ExecContext(ctx, `UPDATE backup_codes SET used_at = ? WHERE user_id = ? AND hash = ? AND used_at = 0`, time.Now().UnixNano(), id, hash)
	if err != nil {
		return 0, false, err
	}
	n, _ := res.RowsAffected()
	left, err := d.BackupCodesLeft(ctx, id)
	return left, n > 0, err
}

// BackupCodesLeft counts a user's unused backup codes.
func (d *DB) BackupCodesLeft(ctx context.Context, id string) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM backup_codes WHERE user_id = ? AND used_at = 0`, id).Scan(&n)
	return n, err
}

// ResetSignIn forgets a user's password, authenticator and backup codes,
// for a setup link to make anew.
func (d *DB) ResetSignIn(ctx context.Context, id string) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = '', totp_secret = '', totp_at = 0, totp_step = 0 WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return station.ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM backup_codes WHERE user_id = ?`, id)
		return err
	})
}

// PutInvite stores a setup link's hash for a user, replacing any older
// link of theirs.
func (d *DB) PutInvite(ctx context.Context, hash string, inv station.Invite) error {
	return d.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM invites WHERE user_id = ?`, inv.UserID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO invites (hash, user_id, made_by, expires_at) VALUES (?, ?, ?, ?)`,
			hash, inv.UserID, inv.By, inv.ExpiresAt.UnixNano())
		if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
			return station.ErrNotFound
		}
		return err
	})
}

// Invite finds the open setup link with that hash; one that expired or
// was used is not found.
func (d *DB) Invite(ctx context.Context, hash string, now time.Time) (station.Invite, error) {
	var inv station.Invite
	var expires int64
	err := d.db.QueryRowContext(ctx, `SELECT user_id, made_by, expires_at FROM invites WHERE hash = ? AND expires_at > ?`, hash, now.UnixNano()).Scan(&inv.UserID, &inv.By, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return inv, station.ErrNotFound
	}
	inv.ExpiresAt = time.Unix(0, expires)
	return inv, err
}

// UseInvite spends a setup link.
func (d *DB) UseInvite(ctx context.Context, hash string) error {
	return d.update(ctx, `DELETE FROM invites WHERE hash = ?`, hash)
}

// OpenInvites is when each user's open setup link expires.
func (d *DB) OpenInvites(ctx context.Context, now time.Time) (map[string]time.Time, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT user_id, expires_at FROM invites WHERE expires_at > ?`, now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var user string
		var expires int64
		if err := rows.Scan(&user, &expires); err != nil {
			return nil, err
		}
		out[user] = time.Unix(0, expires)
	}
	return out, rows.Err()
}

// Teams lists every team, by name, with its members.
func (d *DB) Teams(ctx context.Context) ([]station.Team, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, name, created_at FROM teams ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Team
	for rows.Next() {
		var t station.Team
		var created int64
		if err := rows.Scan(&t.ID, &t.Name, &created); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(0, created)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := d.db.QueryContext(ctx, `SELECT m.team_id, m.user_id FROM team_members m JOIN users u ON u.id = m.user_id ORDER BY u.login`)
	if err != nil {
		return nil, err
	}
	defer members.Close()
	by := map[string][]string{}
	for members.Next() {
		var team, user string
		if err := members.Scan(&team, &user); err != nil {
			return nil, err
		}
		by[team] = append(by[team], user)
	}
	for i := range out {
		out[i].Members = by[out[i].ID]
	}
	return out, members.Err()
}

// PutTeam creates a team or renames one.
func (d *DB) PutTeam(ctx context.Context, t station.Team) error {
	created := t.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET name = excluded.name`,
		t.ID, strings.TrimSpace(t.Name), created.UnixNano())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrTeamExists
	}
	return err
}

// RemoveTeam removes a team; its memberships and access go with it.
func (d *DB) RemoveTeam(ctx context.Context, id string) error {
	return d.update(ctx, `DELETE FROM teams WHERE id = ?`, id)
}

// SetMember puts a user in a team, or takes them out.
func (d *DB) SetMember(ctx context.Context, teamID, userID string, in bool) error {
	if !in {
		_, err := d.db.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID)
		return err
	}
	_, err := d.db.ExecContext(ctx, `INSERT OR IGNORE INTO team_members (team_id, user_id) VALUES (?, ?)`, teamID, userID)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return station.ErrNotFound
	}
	return err
}

// Accesses lists every team's level on every device.
func (d *DB) Accesses(ctx context.Context) ([]station.Access, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT device_id, team_id, level FROM device_access ORDER BY device_id, team_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []station.Access
	for rows.Next() {
		var a station.Access
		var level string
		if err := rows.Scan(&a.DeviceID, &a.TeamID, &level); err != nil {
			return nil, err
		}
		a.Level = station.Level(level)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAccess sets a team's level on a device; LevelNone takes it away.
func (d *DB) SetAccess(ctx context.Context, a station.Access) error {
	if a.Level == station.LevelNone {
		_, err := d.db.ExecContext(ctx, `DELETE FROM device_access WHERE device_id = ? AND team_id = ?`, a.DeviceID, a.TeamID)
		return err
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO device_access (device_id, team_id, level) VALUES (?, ?, ?) ON CONFLICT(device_id, team_id) DO UPDATE SET level = excluded.level`,
		a.DeviceID, a.TeamID, string(a.Level))
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return station.ErrNotFound
	}
	return err
}

// LevelFor is the highest level any of the teams has on the device.
func (d *DB) LevelFor(ctx context.Context, teams []string, deviceID string) (station.Level, error) {
	return d.bestLevel(ctx, teams, `device_id = ? AND `, deviceID)
}

// BestLevel is the highest level any of the teams has on any device:
// what a token of theirs may be allowed at most.
func (d *DB) BestLevel(ctx context.Context, teams []string) (station.Level, error) {
	return d.bestLevel(ctx, teams, "")
}

func (d *DB) bestLevel(ctx context.Context, teams []string, where string, args ...any) (station.Level, error) {
	best := station.LevelNone
	if len(teams) == 0 {
		return best, nil
	}
	marks := make([]string, len(teams))
	for i, t := range teams {
		marks[i] = "?"
		args = append(args, t)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT level FROM device_access WHERE `+where+`team_id IN (`+strings.Join(marks, ", ")+`)`, args...)
	if err != nil {
		return best, err
	}
	defer rows.Close()
	for rows.Next() {
		var level string
		if err := rows.Scan(&level); err != nil {
			return best, err
		}
		if l := station.Level(level); l.Rank() > best.Rank() {
			best = l
		}
	}
	return best, rows.Err()
}

// SeedOwner makes the owner user from a password hash when there are no
// users yet, so a station from before users keeps its sign-in. It says
// whether it did.
func (d *DB) SeedOwner(ctx context.Context, hash string) (bool, error) {
	var n int
	if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 || hash == "" {
		return false, nil
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO users (id, login, name, password_hash, admin, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		station.NewID("usr"), station.OwnerLogin, "Owner", hash, time.Now().UnixNano())
	return err == nil, err
}
