package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/jam"
)

// The AI section on Settings: the models the owner set up, bring your
// own keys, and the Jams switch.

// modelsData adds the AI section's data to the Settings page's.
func (s *server) modelsData(data map[string]any) {
	models, _ := s.jams.models.List()
	data["Models"], data["Providers"], data["Roles"], data["Efforts"] = models, jam.Providers, jam.Roles, jam.Efforts
	data["JamsOn"], data["JamsPossible"], data["Vibium"], data["VibiumMissing"] = s.jams.on(), s.jams.possible(), s.jams.vibium, s.jams.missing
}

// addModel is POST /settings/models: a new model, or the same name again
// with changed settings, keeping its key when none is typed. An agent
// model is then asked for one word, so a wrong key or an unknown model
// is seen here and not in a failed jam.
func (s *server) addModel(w http.ResponseWriter, r *http.Request, p station.Principal) {
	c := jam.ModelConfig{Name: strings.TrimSpace(r.FormValue("name")), Role: r.FormValue("role"), Provider: r.FormValue("provider"),
		Model: strings.TrimSpace(r.FormValue("model")), BaseURL: strings.TrimSpace(r.FormValue("base_url")), ReasoningEffort: r.FormValue("effort"),
		APIKey: strings.TrimSpace(r.FormValue("key")), Default: r.FormValue("default") == "on"}
	if c.APIKey == "" {
		if old, err := s.jams.models.Get(c.Name); err == nil && !old.Host {
			c.APIKey = old.APIKey
		}
	}
	if err := s.jams.models.Save(c); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: "saved " + c.Name + " (" + c.Role + ", " + c.Provider + " " + c.Model + ")"})
	msg := "Saved " + c.Name + "."
	if c.Role == jam.RoleAgent {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if answer, err := c.Client().Ping(ctx); err != nil {
			msg += " It did not answer: " + err.Error()
		} else {
			if len(answer) > 80 {
				answer = answer[:80] + "…"
			}
			msg += " It answered: " + answer
		}
	}
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": msg}))
}

// removeModel is POST /settings/models/remove.
func (s *server) removeModel(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := r.FormValue("name")
	if err := s.jams.models.Remove(name); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: "removed " + name})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// defaultModel is POST /settings/models/default.
func (s *server) defaultModel(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := r.FormValue("name")
	if err := s.jams.models.SetDefault(name); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: name + " made default"})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// modelKey is POST /settings/models/key: a new key for a model.
func (s *server) modelKey(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name, key := r.FormValue("name"), strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "Type the new key."}))
		return
	}
	if err := s.jams.models.SetKey(name, key); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: name + " key replaced"})
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "The key for " + name + " is replaced."}))
}

// setJams is POST /settings/jams: the switch.
func (s *server) setJams(w http.ResponseWriter, r *http.Request, p station.Principal) {
	on := r.FormValue("on") == "1"
	s.nameMu.Lock()
	s.cfg.JamsOff = !on
	err := saveConfig(s.dir, s.cfg)
	s.nameMu.Unlock()
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	state := "off"
	if on {
		state = "on"
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "jams", Detail: "turned " + state})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
