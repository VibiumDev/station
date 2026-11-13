package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/VibiumDev/station/jam"
)

// The station's own sherpa-onnx: a speech-to-text server, started and
// kept running by the station, so a jam's listen finds the model loaded.
// sherpa-onnx is the owner's install. The server is a small script the
// station brings, since sherpa-onnx's own servers listen on every
// interface and this one answers on this machine only.

//go:embed sherpa_server.py
var sherpaServer []byte

// sherpaModel is the name the station's own sherpa-onnx has on Settings.
const sherpaModel = "sherpa"

type sherpaRunner struct {
	python    string // the interpreter that has sherpa-onnx and websockets
	dir       string // each model is a folder here; the script and the log are here too
	logPath   string
	logf      func(string, ...any)
	preferred func() string // the model to load, if it is installed
	ready     time.Duration // how long a start may take to answer

	mu      sync.Mutex
	url     string // "" while it is not answering
	model   string // the model it loaded
	missing string // why it is not running, for Settings
	again   chan struct{}
	done    chan struct{}
}

// findSherpa finds the interpreter that has sherpa-onnx: the flag, then
// STATION_SHERPA, then a virtualenv under the data directory, then the
// sherpa-onnx-cli program on PATH, which stands for the interpreter its
// first line names.
func findSherpa(flag, dataDir string) (string, error) {
	candidates := []string{flag, os.Getenv("STATION_SHERPA"), filepath.Join(dataDir, "sherpa", "venv", "bin", "python")}
	if p, err := exec.LookPath("sherpa-onnx-cli"); err == nil {
		candidates = append(candidates, p)
	}
	for i, c := range candidates {
		if c == "" {
			continue
		}
		python, err := pythonBehind(c)
		if err == nil {
			return python, nil
		}
		if i < 2 { // named by the owner: say what is wrong with it
			return "", err
		}
	}
	return "", errors.New("sherpa-onnx is not installed: no --sherpa, no STATION_SHERPA, no " + filepath.Join(dataDir, "sherpa", "venv") + " and no sherpa-onnx-cli on PATH")
}

// Models are the models in the folder: each folder with a tokens.txt
// and an encoder, a decoder and a joiner, by name.
func (p *sherpaRunner) Models() []string {
	if p == nil {
		return nil
	}
	entries, _ := os.ReadDir(p.dir)
	var out []string
	for _, e := range entries {
		dir := filepath.Join(p.dir, e.Name())
		if st, err := os.Stat(filepath.Join(dir, "tokens.txt")); err != nil || !st.Mode().IsRegular() {
			continue
		}
		whole := true
		for _, part := range []string{"encoder", "decoder", "joiner"} {
			files, _ := filepath.Glob(filepath.Join(dir, part+"*.onnx"))
			whole = whole && len(files) > 0
		}
		if whole {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// URL is where the server answers, or "" while it does not. A station
// with no sherpa-onnx at all has a nil runner.
func (p *sherpaRunner) URL() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url
}

// sherpaStatus is the sherpa-onnx row on Settings.
type sherpaStatus struct {
	Python, Dir, URL, Model, Missing, Log string
	Models                                []string
}

func (p *sherpaRunner) status() sherpaStatus {
	if p == nil {
		return sherpaStatus{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return sherpaStatus{Python: p.python, Dir: p.dir, URL: p.url, Model: p.model, Missing: p.missing, Log: p.logPath}
}

// builtin is the station's own sherpa-onnx as a listen model, while it
// answers.
func (p *sherpaRunner) builtin() []jam.ModelConfig {
	p.mu.Lock()
	url, model := p.url, p.model
	p.mu.Unlock()
	if url == "" {
		return nil
	}
	return []jam.ModelConfig{{Name: sherpaModel, Role: jam.RoleListen, Provider: "sherpa-server", Model: model}}
}

// reload has the server start again with the preferred model. A server
// holds one model, so another one is another start.
func (p *sherpaRunner) reload() {
	select {
	case p.again <- struct{}{}:
	default:
	}
}

var errReload = errors.New("another model was picked")

// run starts the server and keeps it running until ctx ends. A server
// that stops is started again, after a wait that grows to a minute.
func (p *sherpaRunner) run(ctx context.Context) {
	defer close(p.done)
	wait := 2 * time.Second
	for ctx.Err() == nil {
		models := p.Models()
		if len(models) == 0 {
			p.setMissing("no models in " + p.dir + ". Unpack a streaming Zipformer model's folder there.")
			select {
			case <-ctx.Done():
				return
			case <-p.again:
			case <-time.After(30 * time.Second):
			}
			continue
		}
		model := models[0]
		if want := p.preferred(); want != "" {
			for _, m := range models {
				if m == want {
					model = want
				}
			}
		}
		select {
		case <-p.again: // this start already loads what was picked
		default:
		}
		started := time.Now()
		err := p.serve(ctx, model)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errReload) {
			p.setMissing("")
			wait = 2 * time.Second
			continue
		}
		p.setMissing(fmt.Sprintf("its server stopped (%v). The log is %s.", err, p.logPath))
		p.logf("sherpa-onnx: %v; starting again in %s", err, wait)
		if time.Since(started) > 5*time.Minute {
			wait = 2 * time.Second
		}
		if !sleepCtx(ctx, wait) {
			return
		}
		wait = min(2*wait, time.Minute)
	}
}

// serve runs one server with a model loaded until it stops, another
// model is picked or ctx ends.
func (p *sherpaRunner) serve(ctx context.Context, model string) error {
	// The script is written at every start, so a new station brings its
	// own. Python runs it with -I: the folder beside it holds downloads,
	// which must not be importable.
	script := filepath.Join(p.dir, "server.py")
	if err := os.WriteFile(script, sherpaServer, 0o600); err != nil {
		return err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	out, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(p.python, "-I", script, "--model", filepath.Join(p.dir, model), "--host", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
	}
	hostport := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.After(p.ready)
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("it exited before it answered: %v", err)
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-p.again:
			stop()
			return errReload
		case <-deadline:
			stop()
			return fmt.Errorf("it did not answer within %s", p.ready)
		case <-time.After(250 * time.Millisecond):
		}
		if sherpaAnswers("http://" + hostport) {
			break
		}
	}
	p.mu.Lock()
	p.url, p.model, p.missing = "ws://"+hostport, model, ""
	p.mu.Unlock()
	p.logf("sherpa-onnx: listening at ws://%s with %s", hostport, model)
	defer func() {
		p.mu.Lock()
		p.url = ""
		p.mu.Unlock()
	}()
	select {
	case err := <-exited:
		return fmt.Errorf("it exited: %v", err)
	case <-p.again:
		stop()
		return errReload
	case <-ctx.Done():
		stop()
		return ctx.Err()
	}
}

func (p *sherpaRunner) setMissing(why string) {
	p.mu.Lock()
	p.missing = why
	p.mu.Unlock()
}

// sherpaAnswers says whether the station's sherpa-onnx server answers at
// url, which it does once its model is loaded.
func sherpaAnswers(url string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(url + "/info")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
