package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/jam"
)

// Jam notes are standing guidance the model is given with every jam.
// The station has one set, a file in the data directory that an admin
// edits on Settings or by hand. Each person has their own, on Account,
// for the jams they start.

// jamNotesFile is the station's notes, in the data directory.
const jamNotesFile = "jam-notes.md"

// cleanNotes makes typed notes ready to keep: one kind of line ending,
// no space around them, and no longer than a jam may carry.
func cleanNotes(text string) (string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if n := len([]rune(text)); n > jam.MaxNotes {
		return "", fmt.Errorf("the notes are %d characters, and the most a jam carries is %d", n, jam.MaxNotes)
	}
	return text, nil
}

// stationNotes reads the station's notes as they are now, so a change
// to the file applies to the next jam.
func (s *server) stationNotes() string {
	data, err := os.ReadFile(filepath.Join(s.dir, jamNotesFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

func (s *server) saveStationNotes(text string) error {
	path := filepath.Join(s.dir, jamNotesFile)
	if text == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// jamNotes is the notes a jam started by p is given: the station's,
// and those of the person behind p, who may be a token's owner.
func (s *server) jamNotes(ctx context.Context, p station.Principal) (stationNotes, personNotes string) {
	stationNotes = s.stationNotes()
	if p.User != "" {
		personNotes, _ = s.db.JamNotes(ctx, p.User)
	}
	return stationNotes, personNotes
}

// setJamNotes is POST /settings/jam-notes: the station's notes.
func (s *server) setJamNotes(w http.ResponseWriter, r *http.Request, p station.Principal) {
	text, err := cleanNotes(r.FormValue("notes"))
	if err == nil {
		err = s.saveStationNotes(text)
	}
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error(), "JamNotes": r.FormValue("notes")}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "jams", Detail: "changed the station's jam notes"})
	http.Redirect(w, r, "/settings#jam-notes", http.StatusSeeOther)
}

// setMyJamNotes is POST /account/jam-notes: a person's own notes.
func (s *server) setMyJamNotes(w http.ResponseWriter, r *http.Request, p station.Principal) {
	text, err := cleanNotes(r.FormValue("notes"))
	if err == nil {
		err = s.db.SetJamNotes(r.Context(), p.User, text)
	}
	if err != nil {
		s.render(w, "account", s.accountData(r, p, map[string]any{"Message": err.Error(), "JamNotes": r.FormValue("notes")}))
		return
	}
	http.Redirect(w, r, "/account#jam-notes", http.StatusSeeOther)
}
