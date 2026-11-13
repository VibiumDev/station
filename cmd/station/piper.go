package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/VibiumDev/station/jam"
)

// The station's own Piper: Piper's speech server, started and kept
// running by the station, so a jam's say speaks with a voice already
// loaded. Piper is the owner's install and a program of its own, under
// its own license; the station only starts it and talks to it.

// piperModel is the name the station's own Piper has on Settings.
const piperModel = "piper"

type piperRunner struct {
	python    string // the interpreter that has piper-tts with its server
	voicesDir string
	logPath   string
	logf      func(string, ...any)
	preferred func() string // the voice to load first, if it is installed
	ready     time.Duration // how long a start may take to answer

	mu      sync.Mutex
	url     string // "" while it is not answering
	voice   string // the voice it started with
	missing string // why it is not running, for Settings
	done    chan struct{}
}

// findPiper finds the interpreter that runs Piper's server: the flag,
// then STATION_PIPER, then a virtualenv under the data directory, then
// the piper program on PATH. A path to the piper program stands for the
// interpreter its first line names.
func findPiper(flag, dataDir string) (string, error) {
	candidates := []string{flag, os.Getenv("STATION_PIPER"), filepath.Join(dataDir, "piper", "venv", "bin", "python")}
	if p, err := exec.LookPath("piper"); err == nil {
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
	return "", errors.New("Piper is not installed: no --piper, no STATION_PIPER, no " + filepath.Join(dataDir, "piper", "venv") + " and no piper on PATH")
}

// pythonBehind is the interpreter behind a path: the path itself when it
// is a Python, else the one its #! line names.
func pythonBehind(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if strings.HasPrefix(filepath.Base(path), "python") {
		return path, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if !strings.HasPrefix(line, "#!") || len(fields) == 0 {
		return "", fmt.Errorf("%s is neither a Python nor a script that names one", path)
	}
	python := fields[0]
	if filepath.Base(python) == "env" && len(fields) > 1 {
		if python, err = exec.LookPath(fields[1]); err != nil {
			return "", err
		}
	}
	if !strings.Contains(filepath.Base(python), "python") {
		return "", fmt.Errorf("%s runs with %s, not a Python", path, python)
	}
	return python, nil
}

// Voices are the voices in the folder: each NAME.onnx with its
// NAME.onnx.json, by name.
func (p *piperRunner) Voices() []string {
	if p == nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(p.voicesDir, "*.onnx"))
	var out []string
	for _, m := range matches {
		if st, err := os.Stat(m + ".json"); err == nil && st.Mode().IsRegular() {
			out = append(out, strings.TrimSuffix(filepath.Base(m), ".onnx"))
		}
	}
	sort.Strings(out)
	return out
}

// URL is where the server answers, or "" while it does not. A station
// with no Piper at all has a nil runner.
func (p *piperRunner) URL() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url
}

// piperStatus is the Piper row on Settings.
type piperStatus struct {
	Python, Dir, URL, Voice, Missing, Log string
	Voices                                []string
}

func (p *piperRunner) status() piperStatus {
	if p == nil {
		return piperStatus{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return piperStatus{Python: p.python, Dir: p.voicesDir, URL: p.url, Voice: p.voice, Missing: p.missing, Log: p.logPath}
}

// builtin is the station's own Piper as a say model, while it answers.
func (p *piperRunner) builtin() []jam.ModelConfig {
	p.mu.Lock()
	url, voice := p.url, p.voice
	p.mu.Unlock()
	if url == "" {
		return nil
	}
	return []jam.ModelConfig{{Name: piperModel, Role: jam.RoleSay, Provider: "piper-server", Model: voice}}
}

// run starts the server and keeps it running until ctx ends. A server
// that stops is started again, after a wait that grows to a minute.
func (p *piperRunner) run(ctx context.Context) {
	defer close(p.done)
	wait := 2 * time.Second
	for ctx.Err() == nil {
		voices := p.Voices()
		if len(voices) == 0 {
			p.setMissing("no voices in " + p.voicesDir + ". Download a voice's .onnx and .onnx.json files there.")
			if !sleepCtx(ctx, 30*time.Second) {
				return
			}
			continue
		}
		voice := voices[0]
		if want := p.preferred(); want != "" {
			for _, v := range voices {
				if v == want {
					voice = want
				}
			}
		}
		started := time.Now()
		err := p.serve(ctx, voice)
		if ctx.Err() != nil {
			return
		}
		p.setMissing(fmt.Sprintf("its server stopped (%v). The log is %s.", err, p.logPath))
		p.logf("piper: %v; starting again in %s", err, wait)
		if time.Since(started) > 5*time.Minute {
			wait = 2 * time.Second
		}
		if !sleepCtx(ctx, wait) {
			return
		}
		wait = min(2*wait, time.Minute)
	}
}

// serve runs one server with a voice loaded until it stops or ctx ends.
func (p *piperRunner) serve(ctx context.Context, voice string) error {
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
	cmd := exec.Command(p.python, "-m", "piper.http_server", "-m", filepath.Join(p.voicesDir, voice+".onnx"),
		"--data-dir", p.voicesDir, "--host", "127.0.0.1", "--port", strconv.Itoa(port))
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
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.After(p.ready)
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("it exited before it answered: %v", err)
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-deadline:
			stop()
			return fmt.Errorf("it did not answer within %s", p.ready)
		case <-time.After(250 * time.Millisecond):
		}
		if piperAnswers(url) {
			break
		}
	}
	p.mu.Lock()
	p.url, p.voice, p.missing = url, voice, ""
	p.mu.Unlock()
	p.logf("piper: speaking at %s with %s", url, voice)
	defer func() {
		p.mu.Lock()
		p.url = ""
		p.mu.Unlock()
	}()
	select {
	case err := <-exited:
		return fmt.Errorf("it exited: %v", err)
	case <-ctx.Done():
		stop()
		return ctx.Err()
	}
}

func (p *piperRunner) setMissing(why string) {
	p.mu.Lock()
	p.missing = why
	p.mu.Unlock()
}

// piperAnswers says whether a Piper server answers at url.
func piperAnswers(url string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(url + "/info")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// warm has the server load a voice now, so the first jam to use it does
// not wait for the load.
func (p *piperRunner) warm(voice string) {
	url := p.URL()
	if url == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"text": ".", "voice": voice})
	c := http.Client{Timeout: 2 * time.Minute}
	if resp, err := c.Post(url+"/synthesize", "application/json", bytes.NewReader(body)); err == nil {
		resp.Body.Close()
	}
}

// sleepCtx waits d, and says false when ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
