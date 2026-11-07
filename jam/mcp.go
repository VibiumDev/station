// Package jam is the station's own agent: a loop that drives a device
// through Vibium's tools for a prompt typed on the station, with a model
// the owner set up. See docs/specs/jams.md.
package jam

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// MCP is a client for one MCP server over its standard input and output,
// newline-delimited JSON-RPC: `vibium mcp`, started by the station for
// one jam and connected to the station's own /session with the jam's
// token in its environment.
type MCP struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	writeM sync.Mutex
	mu     sync.Mutex
	nextID int
	wait   map[int]chan response
	done   chan struct{}
	err    error // why the reader stopped
	Logf   func(format string, args ...any)
}

// ToolSchema is one tool as the server lists it.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Content is one item of a tool's result: text, or an image.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Data string `json:"data,omitempty"` // base64
	MIME string `json:"mimeType,omitempty"`
}

// CallResult is what a tool call returned. IsError is the server saying
// the tool failed, with the reason in the text, which the model may act
// on; a transport failure is a Go error instead.
type CallResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Text joins the result's text items.
func (r CallResult) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Image is the result's first image, decoded, with its media type.
func (r CallResult) Image() ([]byte, string) {
	for _, c := range r.Content {
		if c.Type == "image" && c.Data != "" {
			b, err := decodeBase64(c.Data)
			if err == nil {
				return b, c.MIME
			}
		}
	}
	return nil, ""
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// ErrClosed says the server has gone away.
var ErrClosed = errors.New("the MCP server has stopped")

// StartMCP runs the server with the given environment and performs the
// MCP handshake. Its standard error goes to Logf, a line at a time.
func StartMCP(ctx context.Context, path string, args []string, env []string, logf func(string, ...any)) (*MCP, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	m := &MCP{cmd: cmd, in: in, wait: map[int]chan response{}, done: make(chan struct{}), Logf: logf}
	go m.read(out)
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				logf("vibium: %s", line)
			}
		}
	}()
	if err := m.initialize(ctx); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

// newMCP wraps an already open pair of pipes, for tests.
func newMCP(in io.WriteCloser, out io.Reader) *MCP {
	m := &MCP{in: in, wait: map[int]chan response{}, done: make(chan struct{}), Logf: func(string, ...any) {}}
	go m.read(out)
	return m
}

func (m *MCP) read(out io.Reader) {
	r := bufio.NewReaderSize(out, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var msg struct {
				ID *int `json:"id"`
				response
			}
			if json.Unmarshal(line, &msg) == nil && msg.ID != nil {
				m.mu.Lock()
				ch := m.wait[*msg.ID]
				delete(m.wait, *msg.ID)
				m.mu.Unlock()
				if ch != nil {
					ch <- msg.response
				}
			}
		}
		if err != nil {
			m.mu.Lock()
			m.err = err
			for id, ch := range m.wait {
				close(ch)
				delete(m.wait, id)
			}
			m.mu.Unlock()
			close(m.done)
			return
		}
	}
}

func (m *MCP) initialize(ctx context.Context) error {
	_, err := m.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "station", "version": "1"}})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return m.notify("notifications/initialized", nil)
}

// Tools lists the server's tools.
func (m *MCP) Tools(ctx context.Context) ([]ToolSchema, error) {
	raw, err := m.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Tools []ToolSchema `json:"tools"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return res.Tools, nil
}

// Call runs one tool.
func (m *MCP) Call(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := m.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, fmt.Errorf("tools/call %s: %w", name, err)
	}
	return res, nil
}

func (m *MCP) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan response, 1)
	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	m.nextID++
	id := m.nextID
	m.wait[id] = ch
	m.mu.Unlock()
	if err := m.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		m.mu.Lock()
		delete(m.wait, id)
		m.mu.Unlock()
		return nil, err
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		if res.Error != nil {
			return nil, res.Error
		}
		return res.Result, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.wait, id)
		m.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (m *MCP) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return m.write(msg)
}

func (m *MCP) write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	m.writeM.Lock()
	defer m.writeM.Unlock()
	_, err = m.in.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return nil
}

// Done is closed when the server's output ends, which is the server
// having exited.
func (m *MCP) Done() <-chan struct{} { return m.done }

// Close ends the server: its standard input is closed, which has
// `vibium mcp` end its session and exit, and a server still running
// after a few seconds is killed.
func (m *MCP) Close() error {
	m.in.Close()
	if m.cmd == nil {
		return nil
	}
	exited := make(chan error, 1)
	go func() { exited <- m.cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-time.After(5 * time.Second):
		m.cmd.Process.Kill()
		return <-exited
	}
}

// LookPath finds the Vibium binary: the path given, else "vibium" on
// the station's PATH.
func LookPath(flag string) (string, error) {
	if flag != "" {
		if _, err := os.Stat(flag); err != nil {
			return "", err
		}
		return flag, nil
	}
	return exec.LookPath("vibium")
}
