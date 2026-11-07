package jam

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// A fake server on pipes: answers initialize, tools/list and tools/call
// the way vibium mcp does, one line per message.
func fakeServer(t *testing.T) *MCP {
	clientIn, serverOut := io.Pipe() // server writes, client reads
	serverIn, clientOut := io.Pipe() // client writes, server reads
	go func() {
		sc := bufio.NewScanner(serverIn)
		for sc.Scan() {
			var req struct {
				ID     *int           `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
				continue
			}
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}}
			case "tools/list":
				result = map[string]any{"tools": []map[string]any{{"name": "browser_tap", "description": "Tap.", "inputSchema": map[string]any{"type": "object"}}}}
			case "tools/call":
				name, _ := req.Params["name"].(string)
				if name == "nope" {
					result = map[string]any{"content": []map[string]any{{"type": "text", "text": "unknown tool: nope"}}, "isError": true}
				} else {
					result = map[string]any{"content": []map[string]any{{"type": "text", "text": "Tapped at (1, 2)"}, {"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"}}}
				}
			default:
				line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
				serverOut.Write(append(line, '\n'))
				continue
			}
			line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
			serverOut.Write(append(line, '\n'))
		}
		serverOut.Close()
	}()
	m := newMCP(clientOut, clientIn)
	t.Cleanup(func() { clientOut.Close() })
	return m
}

func TestMCPClient(t *testing.T) {
	m := fakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := m.Tools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "browser_tap" {
		t.Fatalf("tools: %v, %v", tools, err)
	}
	res, err := m.Call(ctx, "browser_tap", map[string]any{"x": 1, "y": 2})
	if err != nil || res.IsError || res.Text() != "Tapped at (1, 2)" {
		t.Fatalf("call: %+v, %v", res, err)
	}
	if img, mime := res.Image(); string(img) != "hello" || mime != "image/png" {
		t.Errorf("image: %q %q", img, mime)
	}
	bad, err := m.Call(ctx, "nope", nil)
	if err != nil || !bad.IsError || bad.Text() != "unknown tool: nope" {
		t.Errorf("isError result: %+v, %v", bad, err)
	}
	if _, err := m.call(ctx, "ping", nil); err == nil {
		t.Error("an unknown method should be a JSON-RPC error")
	}
	m.Close()
	select {
	case <-m.Done():
	case <-time.After(2 * time.Second):
		t.Error("the reader did not stop after close")
	}
	if _, err := m.Call(ctx, "browser_tap", nil); err == nil {
		t.Error("a call after close should fail")
	}
}
