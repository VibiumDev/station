package jam

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// A one-pixel PNG, for screenshots.
var pixel, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

// fakeTools answers like vibium mcp on a phone.
type fakeTools struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]string // tool -> error text (isError)
	dead  bool
}

func (f *fakeTools) Tools(ctx context.Context) ([]ToolSchema, error) {
	schema := func(props string) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{` + props + `,"page":{"type":"string"}},"required":["x","y","page"]}`)
	}
	return []ToolSchema{
		{Name: "browser_start", Description: "Start."},
		{Name: "browser_screenshot", Description: "Shot.", InputSchema: json.RawMessage(`{"type":"object","properties":{"filename":{"type":"string"},"page":{"type":"string"}}}`)},
		{Name: "browser_tap", Description: "Tap.", InputSchema: schema(`"x":{"type":"number"},"y":{"type":"number"}`)},
		{Name: "browser_map", Description: "Map (a browser's)."},
	}, nil
}

func (f *fakeTools) Call(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return CallResult{}, ErrClosed
	}
	f.calls = append(f.calls, name)
	if msg, ok := f.fail[name]; ok {
		return CallResult{IsError: true, Content: []Content{{Type: "text", Text: msg}}}, nil
	}
	switch name {
	case "browser_screenshot":
		return CallResult{Content: []Content{{Type: "image", Data: base64.StdEncoding.EncodeToString(pixel), MIME: "image/png"}}}, nil
	case "browser_start":
		return CallResult{Content: []Content{{Type: "text", Text: "Connected to remote browser"}}}, nil
	}
	return CallResult{Content: []Content{{Type: "text", Text: "Tapped at (1, 2)"}}}, nil
}

// scripted plays replies in order and records the requests.
type scripted struct {
	mu       sync.Mutex
	replies  []Reply
	requests [][]Message
	forced   []string
}

func (s *scripted) Complete(ctx context.Context, messages []Message, tools []Tool, force string) (Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, append([]Message(nil), messages...))
	s.forced = append(s.forced, force)
	if len(s.replies) == 0 {
		return Reply{}, errors.New("the script ran out")
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

func call(id, name, args string) ToolCall {
	c := ToolCall{ID: id, Type: "function"}
	c.Function.Name, c.Function.Arguments = name, args
	return c
}

func toolReply(calls ...ToolCall) Reply {
	return Reply{Message: Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls", Usage: Usage{Prompt: 10, Completion: 2}}
}

func textReply(text string) Reply {
	return Reply{Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}
}

const verdict = `{"status":"passed","summary":"The home screen shows.","evidence":[{"type":"observation","summary":"Icons and a clock."}]}`

func collect() (func(Event), func() []Event) {
	var mu sync.Mutex
	var events []Event
	return func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() },
		func() []Event { mu.Lock(); defer mu.Unlock(); return append([]Event(nil), events...) }
}

func kinds(events []Event) string {
	var out []string
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return strings.Join(out, " ")
}

func TestLoopCheckThenFollowUp(t *testing.T) {
	model := &scripted{replies: []Reply{
		toolReply(call("c1", "browser_tap", `{"x":1,"y":2}`)),
		toolReply(call("c2", "return_verdict", verdict)),
		// the follow-up
		toolReply(call("c3", "return_result", `{"status":"completed","summary":"Done.","evidence":[{"type":"observation","summary":"Settings opened."}]}`)),
	}}
	tools := &fakeTools{}
	emit, events := collect()
	l := New(model, tools, Options{Prompt: "Is the phone on its home screen?", DeviceKind: "phone", Emit: emit, IdleWait: 2 * time.Second})
	go l.Run(context.Background())
	// Wait for the first verdict, then send a follow-up.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ev := events(); len(ev) > 0 && ev[len(ev)-1].Kind == "verdict" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no verdict: %s", kinds(events()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := l.Turn("Now open Settings."); err != nil {
		t.Fatal(err)
	}
	<-l.Done()
	res := l.Result()
	if res.Status != "done" || res.Verdicts != 2 || res.Err != nil || res.Reason != "no follow-up" || res.Last.Took != "run" {
		t.Errorf("result: %+v", res)
	}
	got := kinds(events())
	want := "turn shot usage call result shot usage verdict turn usage verdict end"
	if got != want {
		t.Errorf("events:\n got %s\nwant %s", got, want)
	}
	// The model saw the system instruction, the prompt and a screenshot
	// first; tap's result went back as a tool message with a fresh
	// screenshot after it.
	first := model.requests[0]
	if len(first) != 3 || first[0].Role != "system" || first[1].Content != "Is the phone on its home screen?" || first[2].Role != "user" {
		t.Errorf("first request: %+v", first)
	}
	second := model.requests[1]
	if len(second) != 6 || second[3].Role != "assistant" || second[4].Role != "tool" || second[4].Content != "Tapped at (1, 2)" || second[5].Role != "user" {
		t.Errorf("second request: %d messages", len(second))
	}
	// The tools offered: the phone's, without page ids, plus both result tools.
	if strings.Join(tools.calls, " ") != "browser_start browser_screenshot browser_tap browser_screenshot" {
		t.Errorf("tool calls: %v", tools.calls)
	}
	if v := events()[7].Verdict; v == nil || v.Word() != "PASS" || v.Took != "check" || len(v.Evidence) != 1 {
		t.Errorf("verdict event: %+v", v)
	}
	if (Verdict{Status: "inconclusive"}).Word() != "REVIEW" || (Verdict{Status: "not_completed"}).Word() != "NOT COMPLETED" {
		t.Error("display words")
	}
}

func TestDeviceTools(t *testing.T) {
	l := New(&scripted{}, &fakeTools{}, Options{DeviceKind: "phone"})
	tools, err := l.deviceTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, " ") != "browser_screenshot browser_tap return_verdict return_result" {
		t.Errorf("phone tools: %v", names)
	}
	if strings.Contains(string(tools[1].Parameters), "page") || !strings.Contains(string(tools[1].Parameters), `"required":["x","y"]`) {
		t.Errorf("tap schema not narrowed: %s", tools[1].Parameters)
	}
	if strings.Contains(string(tools[0].Parameters), "filename") {
		t.Errorf("screenshot schema keeps filename: %s", tools[0].Parameters)
	}
	l = New(&scripted{}, &fakeTools{}, Options{DeviceKind: "phone", Mode: "check"})
	tools, _ = l.deviceTools(context.Background())
	if tools[len(tools)-1].Name != "return_verdict" || len(tools) != 3 {
		t.Errorf("check mode tools: %d", len(tools))
	}
}

func TestLoopCorrectsAndLimits(t *testing.T) {
	// Text without a result gets one corrective turn; then the verdict.
	model := &scripted{replies: []Reply{textReply("I think it is on the home screen."), toolReply(call("c1", "return_verdict", verdict))}}
	emit, events := collect()
	l := New(model, &fakeTools{}, Options{Prompt: "home?", Mode: "check", Emit: emit, IdleWait: 50 * time.Millisecond})
	res := l.Run(context.Background())
	if res.Status != "done" || model.forced[1] != "return_verdict" {
		t.Errorf("corrective: %+v forced %v", res, model.forced)
	}
	if !strings.Contains(kinds(events()), "text usage verdict") {
		t.Errorf("events: %s", kinds(events()))
	}
	// A bad verdict goes back as a tool error; a failing tool too.
	model = &scripted{replies: []Reply{
		toolReply(call("c1", "return_verdict", `{"status":"maybe","summary":"?"}`)),
		toolReply(call("c2", "browser_tap", `{"x":1,"y":2}`)),
		toolReply(call("c3", "return_verdict", verdict)),
	}}
	tools := &fakeTools{fail: map[string]string{"browser_tap": "tap failed: off screen"}}
	emit, events = collect()
	l = New(model, tools, Options{Prompt: "home?", Emit: emit, IdleWait: 50 * time.Millisecond})
	res = l.Run(context.Background())
	if res.Status != "done" || res.Actions != 3 {
		t.Errorf("after errors: %+v", res)
	}
	req := model.requests[1]
	if c, _ := req[len(req)-1].Content.(string); !strings.HasPrefix(c, "Error: status must be") {
		t.Errorf("bad verdict feedback: %v", req[len(req)-1].Content)
	}
	var sawErr bool
	for _, e := range events() {
		sawErr = sawErr || (e.Kind == "result" && e.Error && strings.Contains(e.Text, "off screen"))
	}
	if !sawErr {
		t.Errorf("no error result event: %s", kinds(events()))
	}
	// The action limit forces the result.
	model = &scripted{replies: []Reply{
		toolReply(call("c1", "browser_tap", `{"x":1,"y":2}`)),
		toolReply(call("c2", "browser_tap", `{"x":1,"y":2}`)),
		toolReply(call("c3", "return_verdict", `{"status":"inconclusive","summary":"Out of actions.","reason":"insufficient_evidence"}`)),
	}}
	l = New(model, &fakeTools{}, Options{Prompt: "home?", Mode: "check", MaxActions: 1, IdleWait: 50 * time.Millisecond})
	res = l.Run(context.Background())
	if res.Status != "done" || res.Last.Status != "inconclusive" || model.forced[2] != "return_verdict" {
		t.Errorf("limit: %+v forced %v", res, model.forced)
	}
}

func TestLoopStopAndFailure(t *testing.T) {
	// Stop before any verdict: stopped. A dead Vibium: failed.
	model := &scripted{replies: []Reply{toolReply(call("c1", "browser_tap", `{"x":1,"y":2}`)), toolReply(call("c2", "browser_tap", `{"x":1,"y":2}`))}}
	tools := &fakeTools{}
	l := New(model, tools, Options{Prompt: "home?"})
	go func() { time.Sleep(20 * time.Millisecond); l.Stop("the owner took the device") }()
	res := l.Run(context.Background())
	if res.Status != "stopped" && res.Status != "failed" {
		t.Errorf("stop: %+v", res)
	}
	if res.Status == "stopped" && res.Reason != "the owner took the device" {
		t.Errorf("stop reason: %+v", res)
	}
	tools = &fakeTools{dead: true}
	l = New(&scripted{}, tools, Options{Prompt: "home?"})
	res = l.Run(context.Background())
	if res.Status != "failed" || !strings.Contains(res.Reason, "opening the session") {
		t.Errorf("dead vibium: %+v", res)
	}
	if err := l.Turn("more"); err != ErrEnded {
		t.Errorf("turn after end: %v", err)
	}
}
