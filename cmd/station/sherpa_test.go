package main

import (
	"bytes"
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
	"github.com/coder/websocket"
)

// fakeSherpaServer is a stand-in for the station's server script: /info
// names the model folder it was started with.
const fakeSherpaServer = `import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
args = sys.argv[1:]
port = int(args[args.index("--port") + 1])
model = os.path.basename(args[args.index("--model") + 1])
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        body = json.dumps({"model": model, "args": args}).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
HTTPServer(("127.0.0.1", port), H).serve_forever()
`

// sherpaModelDir makes a model's folder with the files named.
func sherpaModelDir(t *testing.T, dir, name string, files ...string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, name), 0o700)
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, name, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

var sherpaWhole = []string{"tokens.txt", "encoder-epoch-99.int8.onnx", "decoder-epoch-99.onnx", "joiner-epoch-99.int8.onnx"}

func TestFindSherpa(t *testing.T) {
	dir := t.TempDir()
	// A path the owner named that is wrong is said so, not passed over.
	if _, err := findSherpa(filepath.Join(dir, "gone"), dir); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("a wrong --sherpa: %v", err)
	}
	python := filepath.Join(dir, "sherpa", "venv", "bin", "python")
	os.MkdirAll(filepath.Dir(python), 0o700)
	os.WriteFile(python, nil, 0o755)
	t.Setenv("STATION_SHERPA", "")
	if got, err := findSherpa("", dir); err != nil || got != python {
		t.Errorf("the virtualenv in the data directory: %q %v", got, err)
	}
}

// The script the station brings is at least Python that parses.
func TestSherpaServerScript(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	cmd := exec.Command(python, "-I", "-c", "import ast, sys; ast.parse(sys.stdin.read())")
	cmd.Stdin = bytes.NewReader(sherpaServer)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sherpa_server.py: %v\n%s", err, out)
	}
	for _, want := range []string{`default="127.0.0.1"`, `"Origin" in request.headers`} {
		if !bytes.Contains(sherpaServer, []byte(want)) {
			t.Errorf("the script lost %s", want)
		}
	}
}

func TestSherpaRunner(t *testing.T) {
	real, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	dir := t.TempDir()
	models := filepath.Join(dir, "sherpa")
	sherpaModelDir(t, models, "en-big", sherpaWhole...)
	sherpaModelDir(t, models, "en-small", sherpaWhole...)
	sherpaModelDir(t, models, "no-joiner", sherpaWhole[:3]...)
	sherpaModelDir(t, models, "no-tokens", sherpaWhole[1:]...)
	sherpaModelDir(t, models, "venv", "pyvenv.cfg")
	os.WriteFile(filepath.Join(models, "tokens.txt"), []byte("x"), 0o600) // a stray file is no model

	// A python that runs the stand-in whatever script it is handed, and
	// passes on what it was started with.
	fake := filepath.Join(dir, "fake.py")
	os.WriteFile(fake, []byte(fakeSherpaServer), 0o600)
	python := filepath.Join(dir, "python3")
	os.WriteFile(python, []byte("#!/bin/sh\nexec "+real+" "+fake+" \"$@\"\n"), 0o755)

	want := "en-small"
	p := &sherpaRunner{python: python, dir: models, logPath: filepath.Join(models, "server.log"), ready: 20 * time.Second,
		logf: t.Logf, preferred: func() string { return want }, again: make(chan struct{}, 1), done: make(chan struct{})}
	if got := strings.Join(p.Models(), " "); got != "en-big en-small" {
		t.Fatalf("models: %s", got)
	}
	if p.builtin() != nil {
		t.Error("listed before it answers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.run(ctx)
	loaded := func(model string) string {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if st := p.status(); st.URL != "" && st.Model == model {
				return st.URL
			}
		}
		t.Fatalf("%s never loaded: %+v", model, p.status())
		return ""
	}
	url := loaded("en-small")
	if !strings.HasPrefix(url, "ws://127.0.0.1:") {
		t.Fatalf("it answers at %s", url)
	}
	resp, err := http.Get("http" + strings.TrimPrefix(url, "ws") + "/info")
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Model string
		Args  []string
	}
	json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	script := filepath.Join(models, "server.py")
	if info.Model != "en-small" || len(info.Args) < 2 || info.Args[0] != "-I" || info.Args[1] != script || !strings.Contains(strings.Join(info.Args, " "), "--host 127.0.0.1") {
		t.Errorf("started with %q %v, want the preferred model, -I and the station's script on 127.0.0.1", info.Model, info.Args)
	}
	if got, _ := os.ReadFile(script); !bytes.Equal(got, sherpaServer) {
		t.Error("the station's script was not written beside the models")
	}
	if b := p.builtin(); len(b) != 1 || b[0].Name != sherpaModel || b[0].Role != jam.RoleListen || b[0].Model != "en-small" || !b[0].OwnSherpa() {
		t.Errorf("listed as %+v", b)
	}
	// Another model is another start.
	want = "en-big"
	p.reload()
	if again := loaded("en-big"); again == url {
		t.Error("the same server after another model was picked")
	}
	if sherpaAnswers("http" + strings.TrimPrefix(url, "ws")) {
		t.Error("the first server still answers")
	}
	url = p.URL()
	// It goes with the station.
	cancel()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("it did not stop")
	}
	if sherpaAnswers("http"+strings.TrimPrefix(url, "ws")) || p.URL() != "" || p.builtin() != nil {
		t.Error("the server outlived the station")
	}
}

// The station's own sherpa-onnx on Settings: in use over a hosted model,
// its models in a list, and Use loads another.
func TestSherpaOnSettings(t *testing.T) {
	f := newFullStation(t)
	dir := t.TempDir()
	sherpaModelDir(t, dir, "en-a", sherpaWhole...)
	sherpaModelDir(t, dir, "en-b", sherpaWhole...)
	f.s.sherpa = &sherpaRunner{python: "/usr/bin/python3", dir: dir, url: "ws://127.0.0.1:6006", model: "en-a", again: make(chan struct{}, 1)}
	f.s.jams.models.Builtin = f.s.sherpa.builtin
	if err := f.s.jams.models.Save(jam.ModelConfig{Name: "stt", Role: jam.RoleListen, Provider: "openai", Model: "gpt-transcribe", APIKey: "sk-d"}); err != nil {
		t.Fatal(err)
	}
	ck, csrf := f.session(f.users["admin"].ID, stageSignedIn)
	admin := caller{name: "admin", decor: withCookie(ck), csrf: csrf, signed: true, admin: true, person: true}
	listen := func() string {
		html := f.do(admin, "GET", "/settings/ai", "", true).Body.String()
		html = html[strings.Index(html, `<section class="aipart" id="ai-listen">`):]
		return html[:strings.Index(html, "</section>")]
	}
	part := listen()
	row := part[strings.Index(part, "<b>sherpa</b>"):]
	row = row[:strings.Index(row, "</li>")]
	if !strings.Contains(row, "in use") || !strings.Contains(row, "sherpa-onnx, on this station") || !strings.Contains(row, `<select name="model"`) ||
		!strings.Contains(row, `data-current="en-a"`) || !strings.Contains(row, "<option>en-b</option>") || !strings.Contains(row, `type="submit" hidden>Use</button>`) {
		t.Errorf("sherpa's row, in use, should list its models and hide Use: %s", row)
	}
	if strings.Contains(row, "Replace key") || strings.Contains(part, "isn't installed") || !strings.Contains(part, "ws://127.0.0.1:6006") {
		t.Errorf("the listen part: %s", part)
	}
	// What a jam's Vibium is told.
	env := strings.Join(f.s.jams.env("secret", jam.ModelConfig{}), " ")
	if !strings.Contains(env, "VIBIUM_LISTEN_PROVIDER=sherpa-server") || !strings.Contains(env, "VIBIUM_SHERPA_URL=ws://127.0.0.1:6006") || strings.Contains(env, "sk-d") {
		t.Errorf("a jam's environment: %s", env)
	}
	// Use with another model saves it and starts the server again.
	if w := f.do(admin, "POST", "/settings/sherpa/model", "model=en-b", true); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings/ai#ai-listen" {
		t.Fatalf("Use: %d %q", w.Code, w.Header().Get("Location"))
	}
	if c, _ := f.s.jams.models.Get("sherpa"); !c.Default || c.Model != "en-b" || c.Builtin || !c.OwnSherpa() {
		t.Errorf("Use should put sherpa in use with en-b: %+v", c)
	}
	select {
	case <-f.s.sherpa.again:
	default:
		t.Error("the server was not told to load the model")
	}
	if w := f.do(admin, "POST", "/settings/sherpa/model", "model=..%2Fx", true); !strings.Contains(w.Body.String(), "No such sherpa-onnx model") {
		t.Errorf("a model not installed: %d", w.Code)
	}
	// A hosted model marked in use takes over, and Use comes back.
	if err := f.s.jams.models.SetDefault("stt"); err != nil {
		t.Fatal(err)
	}
	if part := listen(); !strings.Contains(part, "<b>stt</b> <span class=\"pill inuse\">in use</span>") || strings.Contains(part, "data-current") {
		t.Errorf("stt in use: %s", part)
	}
	// With the server down, a jam is not pointed at it and the page says why.
	f.s.sherpa.url, f.s.sherpa.missing = "", "its server stopped (exit status 1)."
	f.s.jams.models.SetDefault("sherpa")
	if env := strings.Join(f.s.jams.env("secret", jam.ModelConfig{}), " "); strings.Contains(env, "sherpa") {
		t.Errorf("a jam's environment with the server down: %s", env)
	}
	if part := listen(); !strings.Contains(part, "sherpa-onnx: its server stopped (exit status 1).") {
		t.Errorf("no reason on the page: %s", part)
	}
}

// With STATION_TEST_SHERPA naming a Python that has sherpa-onnx and
// websockets, and STATION_TEST_SHERPA_MODEL a model's folder, the
// station's script runs for real: it loads the model, answers a program
// and turns a browser's page away.
func TestSherpaServerForReal(t *testing.T) {
	python, model := os.Getenv("STATION_TEST_SHERPA"), os.Getenv("STATION_TEST_SHERPA_MODEL")
	if python == "" || model == "" {
		t.Skip("set STATION_TEST_SHERPA and STATION_TEST_SHERPA_MODEL to run sherpa-onnx itself")
	}
	dir := t.TempDir()
	if err := os.Symlink(model, filepath.Join(dir, "model")); err != nil {
		t.Fatal(err)
	}
	p := &sherpaRunner{python: python, dir: dir, logPath: filepath.Join(dir, "server.log"), ready: 3 * time.Minute,
		logf: t.Logf, preferred: func() string { return "" }, again: make(chan struct{}, 1), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	go p.run(ctx)
	for p.URL() == "" && ctx.Err() == nil {
		if why := p.status().Missing; why != "" {
			log, _ := os.ReadFile(p.logPath)
			t.Fatalf("%s\n%s", why, log)
		}
		time.Sleep(100 * time.Millisecond)
	}
	url := p.URL()
	if _, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://elsewhere.example"}}}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("a browser's page was not turned away: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 4*16000)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("Done")); err != nil {
		t.Fatal(err)
	}
	var last struct {
		Text    string `json:"text"`
		Final   bool   `json:"is_final"`
		EOF     bool   `json:"is_eof"`
		Segment *int   `json:"segment"`
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("before its Done!: %v", err)
		}
		if string(data) == "Done!" {
			break
		}
		if err := json.Unmarshal(data, &last); err != nil {
			t.Fatalf("%q: %v", data, err)
		}
	}
	if !last.Final || !last.EOF || last.Segment == nil || strings.TrimSpace(last.Text) != "" {
		t.Errorf("a second of silence ended with %+v", last)
	}
	cancel()
	<-p.done
}
