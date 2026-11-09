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

// stuck is a model that answers only when the request is cancelled.
type stuck struct{}

func (stuck) Complete(ctx context.Context, _ []Message, _ []Tool, _ string) (Reply, error) {
	<-ctx.Done()
	return Reply{}, ctx.Err()
}

func textReply(text string) Reply {
	return Reply{Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}
}

const verdict = `{"status":"passed","summary":"The home screen shows.","evidence":[{"type":"observation","summary":"Icons and a clock."}]}`

// review is an inconclusive verdict, which keeps a jam open for a person.
const review = `{"status":"inconclusive","summary":"A dialog covers the screen.","reason":"insufficient_evidence","evidence":[{"type":"observation","summary":"A dialog."}]}`

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
		toolReply(call("c2", "return_verdict", review)),
		// the follow-up, whose completed result settles the jam
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
	if res.Status != "done" || res.Verdicts != 2 || res.Err != nil || res.Reason != "" || res.Last.Took != "run" {
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
	if v := events()[7].Verdict; v == nil || v.Word() != "REVIEW" || v.Took != "check" || len(v.Evidence) != 1 || v.Settled() {
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
	first := l // the goroutine stops this loop, not whatever l names later
	go func() { time.Sleep(20 * time.Millisecond); first.Stop("the owner took the device") }()
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

func TestLoopFollowUpInWordsAndTidy(t *testing.T) {
	// After an open result, a follow-up that asks nothing of the device
	// is answered in words, with no result forced; the jam ends on its
	// own and presses Home.
	model := &scripted{replies: []Reply{
		toolReply(call("c1", "return_verdict", review)),
		textReply("Glad it helped. Nothing more to do here."),
	}}
	tools := &fakeTools{}
	emit, events := collect()
	l := New(model, tools, Options{Prompt: "home?", DeviceKind: "phone", Emit: emit, IdleWait: 300 * time.Millisecond, About: "an iPhone 15 Pro Max on iOS 26.6",
		Shortcuts: []Shortcut{{Label: "Home", Keys: "Meta+h"}, {Label: "Back", Keys: "Escape"}, {Label: "Recents", Keys: "Alt+Tab"}, {Label: "Lock", Keys: "Control+Meta+q"}}})
	go l.Run(context.Background())
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
	if err := l.Turn("cool, that's it"); err != nil {
		t.Fatal(err)
	}
	<-l.Done()
	res := l.Result()
	if res.Status != "done" || res.Verdicts != 1 || res.Last.Took != "check" || res.Reason != "no follow-up" {
		t.Errorf("result: %+v", res)
	}
	got, want := kinds(events()), "turn shot usage verdict turn usage text call result end"
	if got != want {
		t.Errorf("events:\n got %s\nwant %s", got, want)
	}
	if model.forced[1] != "" {
		t.Errorf("a follow-up in words was forced to %q", model.forced[1])
	}
	if calls := strings.Join(tools.calls, " "); !strings.HasSuffix(calls, "browser_keys") {
		t.Errorf("no Home at the end: %s", calls)
	}
	if ev := events(); string(ev[7].Args) != `{"keys":"Meta+h"}` || ev[8].Error {
		t.Errorf("tidy events: %+v %+v", ev[7], ev[8])
	}
	// The instruction names the device's keys and asks for tidying.
	sys, _ := model.requests[0][0].Content.(string)
	for _, want := range []string{"On this device, Home is Meta+h, Back is Escape and Recents is Alt+Tab.", "close any browser tabs you opened", "reply in a sentence or two instead", "The device is an iPhone 15 Pro Max on iOS 26.6: a phone"} {
		if !strings.Contains(sys, want) {
			t.Errorf("instruction lacks %q", want)
		}
	}
	if strings.Contains(sys, "Swiping up") || strings.Contains(sys, "Lock") {
		t.Error("instruction keeps the gesture when the device has keys, or tells of the Lock chord")
	}

	// A settled result ends the jam at once: no wait, Home pressed, and
	// a follow-up is refused.
	model = &scripted{replies: []Reply{toolReply(call("c1", "return_verdict", verdict))}}
	tools = &fakeTools{}
	emit, events = collect()
	l = New(model, tools, Options{Prompt: "home?", DeviceKind: "phone", Emit: emit, IdleWait: time.Hour, Shortcuts: []Shortcut{{Label: "Home", Keys: "Meta+h"}}})
	started := time.Now()
	res = l.Run(context.Background())
	if res.Status != "done" || res.Reason != "" || res.Verdicts != 1 || time.Since(started) > 2*time.Second {
		t.Errorf("settled: %+v after %s", res, time.Since(started))
	}
	if got := kinds(events()); !strings.HasSuffix(got, "verdict call result end") {
		t.Errorf("settled events: %s", got)
	}
	if err := l.Turn("and now?"); err != ErrEnded {
		t.Errorf("turn after a settled result: %v", err)
	}

	// Stopped by a person: the screen stays as it is, no Home.
	tools = &fakeTools{}
	l = New(stuck{}, tools, Options{Prompt: "home?", Shortcuts: []Shortcut{{Label: "Home", Keys: "Meta+h"}}})
	go func() { time.Sleep(20 * time.Millisecond); l.Stop("the owner took the device") }()
	if res := l.Run(context.Background()); res.Status != "stopped" {
		t.Errorf("stop: %+v", res)
	}
	if calls := strings.Join(tools.calls, " "); strings.Contains(calls, "browser_keys") {
		t.Errorf("Home pressed after a stop: %s", calls)
	}
	// No keys announced: the instruction falls back to the gesture, and
	// nothing is pressed at the end.
	model = &scripted{replies: []Reply{toolReply(call("c1", "return_verdict", verdict))}}
	tools = &fakeTools{}
	l = New(model, tools, Options{Prompt: "home?", DeviceKind: "phone", IdleWait: 20 * time.Millisecond})
	l.Run(context.Background())
	if sys, _ := model.requests[0][0].Content.(string); !strings.Contains(sys, "Swiping up from the bottom") {
		t.Error("instruction lacks the gesture")
	}
	if calls := strings.Join(tools.calls, " "); strings.Contains(calls, "browser_keys") {
		t.Errorf("Home pressed with no Home announced: %s", calls)
	}
}

// A settled result ends the jam at once when nobody watches, and after a
// grace when someone does, which a follow-up or Keep cuts short.
func TestLoopGraceForWatchers(t *testing.T) {
	watching := func() bool { return true }
	waitFor := func(t *testing.T, events func() []Event, kind string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if ev := events(); len(ev) > 0 && ev[len(ev)-1].Kind == kind {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s: %s", kind, kinds(events()))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	// Kept open: the grace is cancelled and the jam waits as an open
	// result does, then ends when no follow-up comes.
	model := &scripted{replies: []Reply{toolReply(call("c1", "return_verdict", verdict))}}
	emit, events := collect()
	l := New(model, &fakeTools{}, Options{Prompt: "home?", Emit: emit, Watched: watching, Grace: 5 * time.Second, IdleWait: 200 * time.Millisecond})
	if err := l.Keep(); err != nil { // a Keep before the moment means nothing
		t.Fatal(err)
	}
	go l.Run(context.Background())
	waitFor(t, events, "grace")
	if u := events()[4].Until; u < time.Now().Add(3*time.Second).UnixMilli() || u > time.Now().Add(6*time.Second).UnixMilli() {
		t.Errorf("grace until: %d", u)
	}
	if err := l.Keep(); err != nil {
		t.Fatal(err)
	}
	<-l.Done()
	res := l.Result()
	if res.Status != "done" || res.Reason != "no follow-up" {
		t.Errorf("kept: %+v", res)
	}
	if got, want := kinds(events()), "turn shot usage verdict grace kept end"; got != want {
		t.Errorf("kept events:\n got %s\nwant %s", got, want)
	}
	if err := l.Keep(); err != ErrEnded {
		t.Errorf("keep after the end: %v", err)
	}
	// A follow-up during the grace continues the conversation.
	model = &scripted{replies: []Reply{toolReply(call("c1", "return_verdict", verdict)), textReply("Yes: the clock and the icons were there.")}}
	emit, events = collect()
	l = New(model, &fakeTools{}, Options{Prompt: "home?", Emit: emit, Watched: watching, Grace: 5 * time.Second, IdleWait: 200 * time.Millisecond})
	go l.Run(context.Background())
	waitFor(t, events, "grace")
	if err := l.Turn("are you sure?"); err != nil {
		t.Fatal(err)
	}
	<-l.Done()
	if got, want := kinds(events()), "turn shot usage verdict grace turn usage text end"; got != want {
		t.Errorf("follow-up events:\n got %s\nwant %s", got, want)
	}
	// Nobody speaks up: the jam ends when the grace runs out.
	model = &scripted{replies: []Reply{toolReply(call("c1", "return_verdict", verdict))}}
	emit, events = collect()
	l = New(model, &fakeTools{}, Options{Prompt: "home?", Emit: emit, Watched: watching, Grace: 100 * time.Millisecond, IdleWait: time.Hour})
	started := time.Now()
	res = l.Run(context.Background())
	if res.Status != "done" || res.Reason != "" || time.Since(started) > 2*time.Second {
		t.Errorf("grace over: %+v after %s", res, time.Since(started))
	}
	if got, want := kinds(events()), "turn shot usage verdict grace end"; got != want {
		t.Errorf("grace events:\n got %s\nwant %s", got, want)
	}
	// Nobody watching: no grace at all.
	model = &scripted{replies: []Reply{toolReply(call("c1", "return_verdict", verdict))}}
	emit, events = collect()
	l = New(model, &fakeTools{}, Options{Prompt: "home?", Emit: emit, Watched: func() bool { return false }, Grace: time.Hour, IdleWait: time.Hour})
	l.Run(context.Background())
	if got, want := kinds(events()), "turn shot usage verdict end"; got != want {
		t.Errorf("unwatched events:\n got %s\nwant %s", got, want)
	}
}
