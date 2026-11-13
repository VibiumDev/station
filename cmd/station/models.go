package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/jam"
)

// The AI section on Settings: the models the owner set up, bring your
// own keys, and the Jams switch.

// modelsData adds the AI section's data to the Settings page's.
func (s *server) modelsData(data map[string]any) {
	models, _ := s.jams.models.List()
	data["Models"], data["Efforts"] = models, jam.Efforts
	// The page's three parts, each its own list with the one in use.
	parts := []aiPart{
		{Role: jam.RoleAgent, Title: "Agent", Add: "Add an agent model",
			About: "The AI chat model that runs jam sessions."},
		{Role: jam.RoleSay, Title: "Say", Add: "Add a voice",
			About: "Text to speech, for when a jam session talks to the device."},
		{Role: jam.RoleListen, Title: "Listen", Add: "Add a listen model",
			About: "Speech to text, for when a jam session listens to the device."},
	}
	for i := range parts {
		parts[i].Providers = jam.ProvidersFor(parts[i].Role)
		for _, c := range models {
			if c.Role == parts[i].Role {
				parts[i].Models = append(parts[i].Models, c)
			}
		}
	}
	data["AIParts"], data["AIDir"] = parts, s.jams.models.Dir
	data["JamsOn"], data["JamsPossible"], data["Vibium"], data["VibiumMissing"] = s.jams.on(), s.jams.possible(), s.jams.vibium, s.jams.missing
	data["JamVideo"] = s.jams.video()
	piper := s.piper.status()
	piper.Voices = s.piper.Voices()
	data["Piper"] = piper
	sherpa := s.sherpa.status()
	sherpa.Models = s.sherpa.Models()
	data["Sherpa"] = sherpa
	data["JamNotes"], data["MaxNotes"] = s.stationNotes(), jam.MaxNotes
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

// previewMax is the most a voice preview says, in characters.
const previewMax = 50

// sayPreview is POST /settings/say/preview: name and text. It says the
// text with that voice, through its real server or API, and answers the
// WAV for the page to play. So a voice is heard working before a jam
// needs it.
func (s *server) sayPreview(w http.ResponseWriter, r *http.Request, p station.Principal) {
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" || utf8.RuneCountInString(text) > previewMax {
		http.Error(w, fmt.Sprintf("Type up to %d characters to hear.", previewMax), http.StatusBadRequest)
		return
	}
	c, err := s.jams.models.Get(r.FormValue("name"))
	if err != nil || c.Role != jam.RoleSay {
		http.Error(w, "No such voice.", http.StatusNotFound)
		return
	}
	if c.OwnPiper() {
		if c.BaseURL = s.piper.URL(); c.BaseURL == "" {
			http.Error(w, "Piper is not running on this station.", http.StatusServiceUnavailable)
			return
		}
		// The voice picked in the list, which need not be the one in use.
		if v := r.FormValue("voice"); v != "" {
			if !slices.Contains(s.piper.Voices(), v) {
				http.Error(w, "No such Piper voice.", http.StatusNotFound)
				return
			}
			c.Model = v
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	wav, err := previewSpeech(ctx, c, text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(wav)
}

// previewSpeech asks a say model's server or API for text as a WAV.
// OpenAI is asked for cedar, the voice Vibium speaks with by default.
func previewSpeech(ctx context.Context, c jam.ModelConfig, text string) ([]byte, error) {
	var url string
	var body map[string]any
	header := http.Header{"Content-Type": {"application/json"}}
	switch c.Provider {
	case "piper-server":
		url, body = strings.TrimRight(c.BaseURL, "/")+"/synthesize", map[string]any{"text": text, "voice": c.Model}
	case "openai":
		base := c.BaseURL
		if base == "" {
			p, _ := jam.ProviderByID("openai")
			base = p.BaseURL
		}
		if c.APIKey == "" {
			return nil, errors.New("this voice has no API key")
		}
		url, body = strings.TrimRight(base, "/")+"/audio/speech", map[string]any{"model": c.Model, "voice": "cedar", "input": text, "response_format": "wav"}
		header.Set("Authorization", "Bearer "+c.APIKey)
	default:
		return nil, fmt.Errorf("%s voices have no preview", c.Provider)
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header = header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s did not answer: %v", c.Provider, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("%s answered HTTP %d: %s", c.Provider, resp.StatusCode, msg)
	}
	if !bytes.HasPrefix(data, []byte("RIFF")) {
		return nil, fmt.Errorf("%s sent no WAV", c.Provider)
	}
	return data, nil
}

// aiPart is one part of the AI section: the models of one role.
type aiPart struct {
	Role, Title, About, Add string
	Models                  []jam.ModelConfig
	Providers               []jam.Provider
}

// piperVoice is POST /settings/piper/voice: jam sessions speak with the
// station's own Piper, in this voice. The server loads the voice at once,
// so the first session to speak with it does not wait.
func (s *server) piperVoice(w http.ResponseWriter, r *http.Request, p station.Principal) {
	voice := r.FormValue("voice")
	if !slices.Contains(s.piper.Voices(), voice) {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "No such Piper voice: " + voice}))
		return
	}
	c, err := s.jams.models.Get(piperModel)
	if err != nil || !c.OwnPiper() {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "The station's Piper is not running."}))
		return
	}
	c.Model, c.Builtin, c.Default = voice, false, true
	if err := s.jams.models.Save(c); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: "piper speaks with " + voice})
	go s.piper.warm(voice)
	http.Redirect(w, r, "/settings/ai#ai-say", http.StatusSeeOther)
}

// sherpaUse is POST /settings/sherpa/model: jam sessions listen with the
// station's own sherpa-onnx, with this model. The server starts again
// with the model at once, so the first session to listen does not wait
// for the load.
func (s *server) sherpaUse(w http.ResponseWriter, r *http.Request, p station.Principal) {
	model := r.FormValue("model")
	if !slices.Contains(s.sherpa.Models(), model) {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "No such sherpa-onnx model: " + model}))
		return
	}
	c, err := s.jams.models.Get(sherpaModel)
	if err != nil || !c.OwnSherpa() {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": "The station's sherpa-onnx is not running."}))
		return
	}
	changed := c.Model != model
	c.Model, c.Builtin, c.Default = model, false, true
	if err := s.jams.models.Save(c); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: "sherpa-onnx listens with " + model})
	if changed || s.sherpa.status().Model != model {
		s.sherpa.reload()
	}
	http.Redirect(w, r, "/settings/ai#ai-listen", http.StatusSeeOther)
}

// removeModel is POST /settings/models/remove.
func (s *server) removeModel(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := r.FormValue("name")
	c, _ := s.jams.models.Get(name)
	if err := s.jams.models.Remove(name); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: "removed " + name})
	http.Redirect(w, r, "/settings/ai#ai-"+c.Role, http.StatusSeeOther)
}

// defaultModel is POST /settings/models/default.
func (s *server) defaultModel(w http.ResponseWriter, r *http.Request, p station.Principal) {
	name := r.FormValue("name")
	if err := s.jams.models.SetDefault(name); err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "model", Detail: name + " made default"})
	c, _ := s.jams.models.Get(name)
	http.Redirect(w, r, "/settings/ai#ai-"+c.Role, http.StatusSeeOther)
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
	http.Redirect(w, r, "/settings/ai#jams", http.StatusSeeOther)
}

// setJamVideo is POST /settings/jam-video: whether a jam's record holds
// video of the device. It applies to jams started from then on.
func (s *server) setJamVideo(w http.ResponseWriter, r *http.Request, p station.Principal) {
	on := r.FormValue("on") == "1"
	s.nameMu.Lock()
	s.cfg.JamVideoOff = !on
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
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "jams", Detail: "video turned " + state})
	http.Redirect(w, r, "/settings/ai#jams", http.StatusSeeOther)
}
