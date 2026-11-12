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
