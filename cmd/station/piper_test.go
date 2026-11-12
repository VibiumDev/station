package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station/jam"
)

// fakePiperServer is a stand-in for python -m piper.http_server: /info
// names the voice it was started with.
const fakePiperServer = `import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
args = sys.argv[1:]
port = int(args[args.index("--port") + 1])
voice = os.path.basename(args[args.index("-m", 2) + 1])[:-len(".onnx")]
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        body = json.dumps({"voice": {"name": voice}}).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def do_POST(self):
        self.send_response(200); self.send_header("Content-Length", "0"); self.end_headers()
HTTPServer(("127.0.0.1", port), H).serve_forever()
`

func TestPiperPython(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o755)
		return p
	}
	python := write("python3.13", "")
	if got, err := piperPython(python); err != nil || got != python {
		t.Errorf("a Python: %q %v", got, err)
	}
	script := write("piper", "#!"+python+"\nimport piper\n")
	if got, err := piperPython(script); err != nil || got != python {
		t.Errorf("the piper program: %q %v", got, err)
	}
	if real, err := exec.LookPath("python3"); err == nil {
		viaEnv := write("piper-env", "#!/usr/bin/env python3\n")
		if got, err := piperPython(viaEnv); err != nil || got != real {
			t.Errorf("through env: %q %v", got, err)
		}
	}
	for name, path := range map[string]string{
		"a shell script": write("other", "#!/bin/sh\n"),
		"no #! line":     write("plain", "import piper\n"),
		"a directory":    dir,
		"nothing there":  filepath.Join(dir, "gone"),
	} {
		if got, err := piperPython(path); err == nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	// A path the owner named that is wrong is said so, not passed over.
	if _, err := findPiper(filepath.Join(dir, "gone"), dir); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("a wrong --piper: %v", err)
	}
}

func TestPiperRunner(t *testing.T) {
	real, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	dir := t.TempDir()
	voices := filepath.Join(dir, "piper")
	os.MkdirAll(voices, 0o700)
	for _, v := range []string{"en_US-amy-low", "en_US-danny-low", "en_US-half"} {
		os.WriteFile(filepath.Join(voices, v+".onnx"), []byte("x"), 0o600)
		if v != "en_US-half" { // a voice without its .json is not one
			os.WriteFile(filepath.Join(voices, v+".onnx.json"), []byte("{}"), 0o600)
		}
	}
	server := filepath.Join(dir, "server.py")
	os.WriteFile(server, []byte(fakePiperServer), 0o600)
	python := filepath.Join(dir, "python3")
	os.WriteFile(python, []byte("#!/bin/sh\nexec "+real+" "+server+" \"$@\"\n"), 0o755)

	p := &piperRunner{python: python, voicesDir: voices, logPath: filepath.Join(voices, "server.log"), ready: 20 * time.Second,
		logf: t.Logf, preferred: func() string { return "en_US-danny-low" }, done: make(chan struct{})}
	if got := strings.Join(p.Voices(), " "); got != "en_US-amy-low en_US-danny-low" {
		t.Fatalf("voices: %s", got)
	}
	if p.builtin() != nil {
		t.Error("listed before it answers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go p.run(ctx)
	for deadline := time.Now().Add(20 * time.Second); p.URL() == "" && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
	}
	url := p.URL()
	if url == "" {
		cancel()
		t.Fatalf("it never answered: %s", p.status().Missing)
	}
	resp, err := http.Get(url + "/info")
	if err != nil {
		t.Fatal(err)
	}
	var info struct{ Voice struct{ Name string } }
	json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	if info.Voice.Name != "en_US-danny-low" {
		t.Errorf("started with %q, want the preferred voice", info.Voice.Name)
	}
	if b := p.builtin(); len(b) != 1 || b[0].Name != piperModel || b[0].Model != "en_US-danny-low" || !b[0].OwnPiper() {
		t.Errorf("listed as %+v", b)
	}
	// It goes with the station.
	cancel()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("it did not stop")
	}
	if piperAnswers(url) || p.URL() != "" || p.builtin() != nil {
		t.Error("the server outlived the station")
	}
}

// Settings' AI section is three parts, Agent, Say and Listen, each with
// its own models, the one in use marked, Use on the others, and an add
// form that names its role and offers only that role's providers.
func TestAIPartsOnSettings(t *testing.T) {
	f := newFullStation(t)
	m := f.s.jams.models
	for _, c := range []jam.ModelConfig{
		{Name: "gpt", Role: jam.RoleAgent, Provider: "openai", Model: "gpt-5.6-sol", APIKey: "sk-a", Default: true},
		{Name: "claude", Role: jam.RoleAgent, Provider: "anthropic", Model: "claude-x", APIKey: "sk-b"},
		{Name: "tts", Role: jam.RoleSay, Provider: "openai", Model: "gpt-4o-mini-tts", APIKey: "sk-c"},
		{Name: "stt", Role: jam.RoleListen, Provider: "openai", Model: "gpt-transcribe", APIKey: "sk-d"},
	} {
		if err := m.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	m.Builtin = func() []jam.ModelConfig {
		return []jam.ModelConfig{{Name: piperModel, Role: jam.RoleSay, Provider: "piper-server", Model: "en_US-danny-low"}}
	}
	ck, csrf := f.session(f.users["admin"].ID, stageSignedIn)
	admin := caller{name: "admin", decor: withCookie(ck), csrf: csrf, signed: true, admin: true, person: true}
	html := f.do(admin, "GET", "/settings", "", true).Body.String()
	part := func(role string) string {
		i := strings.Index(html, `<section class="aipart" id="ai-`+role+`">`)
		if i < 0 {
			t.Fatalf("no %s part", role)
		}
		j := strings.Index(html[i:], "</section>")
		return html[i : i+j]
	}
	agent, say, listen := part("agent"), part("say"), part("listen")
	if !(strings.Index(html, `id="ai-agent"`) < strings.Index(html, `id="ai-say"`) && strings.Index(html, `id="ai-say"`) < strings.Index(html, `id="ai-listen"`)) {
		t.Error("the parts are not Agent, Say, Listen")
	}
	// The test station has an agent model of its own, so two are offered.
	if !strings.Contains(agent, "<b>gpt</b> <span class=\"pill inuse\">in use</span>") || strings.Count(agent, ">Use</button>") != 2 {
		t.Error("Agent: gpt should be in use, the others offered with Use")
	}
	// Piper, on this machine, is in use where no voice was marked.
	if !strings.Contains(say, "<b>piper</b> <span class=\"pill inuse\">in use</span>") || !strings.Contains(say, "<b>tts</b><span") {
		t.Error("Say: piper should be in use over tts")
	}
	if strings.Contains(listen, "piper") || !strings.Contains(listen, "<b>stt</b> <span class=\"pill inuse\">in use</span>") {
		t.Error("Listen: stt alone, in use")
	}
	for role, want := range map[string]string{"agent": "anthropic", "say": "piper-server", "listen": "openai"} {
		p := part(role)
		if !strings.Contains(p, `<input type="hidden" name="role" value="`+role+`">`) || !strings.Contains(p, `<option value="`+want+`">`) {
			t.Errorf("%s: its form lacks the role or %s", role, want)
		}
		if !strings.Contains(p, `id="help-ai-`+role+`"`) || !strings.Contains(p, "set-up-jams.md#") {
			t.Errorf("%s: no help with a guide link", role)
		}
	}
	if strings.Contains(say, `value="anthropic"`) || strings.Contains(listen, `value="piper-server"`) {
		t.Error("a part offers a provider its role cannot use")
	}
}
