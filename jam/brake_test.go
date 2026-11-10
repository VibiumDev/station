package jam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"
)

// picture is a phone's screen, 480 by 960 and grey, with white marks on
// it: each mark is x, y, width, height.
func picture(marks ...[4]int) []byte {
	img := image.NewGray(image.Rect(0, 0, 480, 960))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	for _, m := range marks {
		for y := m[1]; y < m[1]+m[3]; y++ {
			for x := m[0]; x < m[0]+m[2]; x++ {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

var (
	menuOpen   = picture([4]int{200, 100, 240, 300})
	menuClosed = picture()
)

func tap(x, y float64) map[string]any { return map[string]any{"x": x, "y": y} }

func TestBrakeStopsACircle(t *testing.T) {
	// The jam that prompted it: two buttons 60 points apart, tapped in
	// turn, a few points off each time, opening and closing one menu.
	b := &brake{}
	var said []brakeSays
	for i := 0; i < 5; i++ {
		said = append(said,
			b.note("browser_tap", tap(float64(342+i%3), float64(84+i%2)), outcomeOf(menuOpen, "")),
			b.note("browser_tap", tap(float64(403+i%2), 85), outcomeOf(menuClosed, "")))
	}
	// The first tap on each button is new. Every one after goes a step
	// deeper: the word after four, the stop after eight.
	want := []brakeSays{brakeQuiet, brakeQuiet, brakeQuiet, brakeQuiet, brakeQuiet, brakeWarns, brakeQuiet, brakeQuiet, brakeQuiet, brakeStops}
	for i := range want {
		if said[i] != want[i] {
			t.Fatalf("action %d: the brake said %d, want %d (all: %v)", i+1, said[i], want[i], said)
		}
	}
	if len(b.tried) != 2 {
		t.Errorf("%d different attempts, want the two buttons", len(b.tried))
	}
}

func TestBrakeLeavesProgressAlone(t *testing.T) {
	// One button pressed thirty times, the screen new each time: a
	// counter going up, a digit typed again.
	b := &brake{}
	for i := 0; i < 30; i++ {
		digit := [4]int{40 + 22*(i%18), 200 + 60*(i/18), 20, 35}
		if said := b.note("browser_tap", tap(240, 800), outcomeOf(picture(digit), "")); said != brakeQuiet || b.depth != 0 {
			t.Fatalf("press %d: the brake said %d at depth %d", i+1, said, b.depth)
		}
	}
	// Different buttons with the screen standing still are each new once.
	b = &brake{}
	for i := 0; i < 6; i++ {
		if said := b.note("browser_tap", tap(float64(40+60*i), 500), outcomeOf(menuClosed, "")); said != brakeQuiet || b.depth != 0 {
			t.Fatalf("button %d: the brake said %d at depth %d", i+1, said, b.depth)
		}
	}
}

func TestBrakeComesOutOfACircleWithSomethingNew(t *testing.T) {
	b := &brake{}
	for i := 0; i < 4; i++ {
		b.note("browser_keys", map[string]any{"keys": "Escape"}, outcomeOf(menuClosed, ""))
	}
	if b.depth != 3 {
		t.Fatalf("depth %d after four of the same, want 3", b.depth)
	}
	b.note("browser_tap", tap(100, 100), outcomeOf(menuOpen, ""))
	b.note("browser_type", map[string]any{"text": "hello"}, outcomeOf(picture([4]int{40, 400, 200, 30}), ""))
	if b.depth != 1 {
		t.Errorf("depth %d after two new actions, want 1", b.depth)
	}
	// Other keys are another action.
	if b.note("browser_keys", map[string]any{"keys": "Meta+h"}, outcomeOf(menuClosed, "")); b.depth != 0 {
		t.Errorf("depth %d after other keys", b.depth)
	}
}

func TestBrakeSeesTheSameScreen(t *testing.T) {
	plain := outcomeOf(menuClosed, "")
	// A caret that blinked, two points wide, is the same screen.
	if !plain.same(outcomeOf(picture([4]int{100, 500, 2, 20}), "")) {
		t.Error("a blinking caret made a new screen")
	}
	// A digit that appeared is not.
	if plain.same(outcomeOf(picture([4]int{100, 500, 20, 35}), "")) {
		t.Error("a new digit was not seen")
	}
	// The same screen as a JPEG, which is how a phone's shots arrive, is
	// the same screen.
	shot, _ := png.Decode(bytes.NewReader(menuOpen))
	var lossy bytes.Buffer
	jpeg.Encode(&lossy, shot, &jpeg.Options{Quality: 60})
	if !outcomeOf(menuOpen, "").same(outcomeOf(lossy.Bytes(), "")) {
		t.Error("a JPEG of a screen is not the same screen")
	}
	// A picture that cannot be read and an action that said nothing are
	// outcomes nobody knows.
	unknown := outcomeOf(pixel, "")
	if unknown.same(unknown) || unknown.same(outcomeOf(nil, "")) {
		t.Error("two unknown outcomes are the same")
	}
}

func TestBrakeCountsFailuresAndNotLooks(t *testing.T) {
	// A tool that fails the same way each time is a circle with no screen.
	b := &brake{}
	var last brakeSays
	for i := 0; i < 9; i++ {
		last = b.note("browser_click", map[string]any{"selector": "#go"}, outcomeOf(nil, "Error: no element matches #go"))
	}
	if last != brakeStops {
		t.Errorf("nine identical failures: the brake said %d at depth %d", last, b.depth)
	}
	// Looking and waiting are not attempts, however often.
	b = &brake{}
	for i := 0; i < 20; i++ {
		for _, tool := range []string{"browser_screenshot", "browser_sleep", "vibium_listen"} {
			if said := b.note(tool, map[string]any{"ms": 1000.0}, outcomeOf(menuClosed, "")); said != brakeQuiet || b.depth != 0 {
				t.Fatalf("%s: the brake said %d at depth %d", tool, said, b.depth)
			}
		}
	}
}

// menuPhone is a phone with one menu: a tap on the left opens it, any
// other tap closes it.
type menuPhone struct {
	fakeTools
	open bool
	taps int
}

func (p *menuPhone) Call(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	shot := func() CallResult {
		screen := menuClosed
		if p.open {
			screen = menuOpen
		}
		return CallResult{Content: []Content{{Type: "image", Data: base64.StdEncoding.EncodeToString(screen), MIME: "image/png"}}}
	}
	switch name {
	case "browser_screenshot":
		return shot(), nil
	case "browser_tap":
		p.taps++
		x, _ := args["x"].(float64)
		p.open = x < 380
		return CallResult{Content: []Content{{Type: "text", Text: "Tapped"}}}, nil
	}
	return CallResult{Content: []Content{{Type: "text", Text: "ok"}}}, nil
}

// circler taps two buttons in turn for as long as it is let.
type circler struct {
	mu       sync.Mutex
	calls    int
	forced   []string
	requests [][]Message
}

func (c *circler) Complete(ctx context.Context, messages []Message, tools []Tool, force string) (Reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.forced = append(c.forced, force)
	c.requests = append(c.requests, append([]Message(nil), messages...))
	id := "c" + strings.Repeat("i", c.calls)
	if force != "" {
		return toolReply(call(id, force, `{"status":"not_completed","summary":"I kept opening and closing the same menu."}`)), nil
	}
	if c.calls%2 == 1 {
		return toolReply(call(id, "browser_tap", `{"x":343,"y":85}`)), nil
	}
	return toolReply(call(id, "browser_tap", `{"x":403,"y":85}`)), nil
}

func TestLoopBrakesAJamGoingInCircles(t *testing.T) {
	phone, model := &menuPhone{}, &circler{}
	emit, events := collect()
	l := New(model, phone, Options{Prompt: "Record a voice note.", Mode: "run", IdleWait: 50 * time.Millisecond, Emit: emit})
	res := l.Run(context.Background())

	// Two new taps, eight more in the circle, then the result. Without
	// the brake this model runs to the action limit of 100.
	if phone.taps != 10 {
		t.Errorf("%d taps reached the phone, want 10", phone.taps)
	}
	if res.Status != "done" || res.Last == nil || res.Last.Status != "not_completed" {
		t.Fatalf("result %+v", res)
	}
	if n := len(model.forced); n != 11 || model.forced[10] != "return_result" || model.forced[9] != "" {
		t.Errorf("forced %v", model.forced)
	}

	// The page shows both words from the station, in order.
	var words []string
	for _, e := range events() {
		if e.Kind == "brake" {
			words = append(words, e.Text)
		}
	}
	if len(words) != 2 || !strings.HasPrefix(words[0], "Going in circles") || !strings.HasPrefix(words[1], "Still going in circles") {
		t.Errorf("brake events %q", words)
	}

	// The model is told after the sixth tap, and told to finish after the tenth.
	told := func(request []Message, phrase string) bool {
		for _, m := range request {
			if s, _ := m.Content.(string); m.Role == "user" && strings.Contains(s, phrase) {
				return true
			}
		}
		return false
	}
	if told(model.requests[5], "try something different") || !told(model.requests[6], "try something different") {
		t.Error("the word to try another way did not come after the sixth tap")
	}
	if told(model.requests[9], "no more actions are taken") || !told(model.requests[10], "no more actions are taken") {
		t.Error("the word to finish did not come after the tenth tap")
	}
	for _, request := range model.requests {
		answered(t, request)
	}
}

func TestLoopLetsAJamThatMovesOnRun(t *testing.T) {
	// The same two taps, but only twice round: no word from the station.
	phone := &menuPhone{}
	model := &scripted{replies: []Reply{
		toolReply(call("c1", "browser_tap", `{"x":343,"y":85}`)),
		toolReply(call("c2", "browser_tap", `{"x":403,"y":85}`)),
		toolReply(call("c3", "browser_tap", `{"x":343,"y":85}`)),
		toolReply(call("c4", "browser_tap", `{"x":403,"y":85}`)),
		toolReply(call("c5", "return_result", `{"status":"completed","summary":"Done.","evidence":[{"type":"observation","summary":"The menu closed."}]}`)),
	}}
	emit, events := collect()
	l := New(model, phone, Options{Prompt: "Open and close the menu twice.", Mode: "run", Emit: emit})
	if res := l.Run(context.Background()); res.Status != "done" || res.Last.Status != "completed" {
		t.Fatalf("result %+v", res)
	}
	if strings.Contains(kinds(events()), "brake") {
		t.Errorf("the brake spoke: %s", kinds(events()))
	}
	for _, f := range model.forced {
		if f != "" {
			t.Errorf("forced %v", model.forced)
		}
	}
}

// answered fails the test unless every message with tool calls is
// followed at once by an answer to each call, in order: the only
// conversation a model's API takes.
func answered(t *testing.T, request []Message) {
	t.Helper()
	for i, m := range request {
		for n, c := range m.ToolCalls {
			at := i + 1 + n
			if at >= len(request) || request[at].Role != "tool" || request[at].ToolCallID != c.ID {
				t.Fatalf("call %s of message %d is not answered at message %d", c.ID, i, at)
			}
		}
	}
}

func TestLoopAnswersEveryCallBeforeItsScreenshots(t *testing.T) {
	// A model may ask for several things in one message.
	phone := &menuPhone{}
	model := &scripted{replies: []Reply{
		toolReply(call("c1", "browser_tap", `{"x":343,"y":85}`), call("c2", "browser_tap", `{"x":403,"y":85}`)),
		toolReply(call("c3", "return_result", `{"status":"completed","summary":"Done.","evidence":[{"type":"observation","summary":"The menu closed."}]}`)),
	}}
	l := New(model, phone, Options{Prompt: "Open and close the menu.", Mode: "run"})
	if res := l.Run(context.Background()); res.Status != "done" || res.Last.Status != "completed" {
		t.Fatalf("result %+v", res)
	}
	request := model.requests[1]
	answered(t, request)
	// Both screenshots follow the answers, the menu open and then closed.
	tail := request[len(request)-4:]
	if tail[0].Role != "tool" || tail[1].Role != "tool" {
		t.Fatalf("the answers are not together: %s %s", tail[0].Role, tail[1].Role)
	}
	for i, want := range [][]byte{menuOpen, menuClosed} {
		parts, _ := tail[2+i].Content.([]Part)
		raw, _ := json.Marshal(parts)
		if tail[2+i].Role != "user" || !strings.Contains(string(raw), base64.StdEncoding.EncodeToString(want)[:80]) {
			t.Errorf("screenshot %d is not where it belongs", i+1)
		}
	}
}

func TestLoopBrakesInsideOneMessage(t *testing.T) {
	// Twelve taps on one spot in one message, as a real model sent them.
	var taps []ToolCall
	for i := 0; i < 12; i++ {
		taps = append(taps, call("t"+strings.Repeat("i", i+1), "browser_tap", `{"x":403,"y":85}`))
	}
	phone := &menuPhone{}
	model := &scripted{replies: []Reply{
		toolReply(taps...),
		toolReply(call("r", "return_result", `{"status":"not_completed","summary":"Tapping there changes nothing."}`)),
	}}
	emit, events := collect()
	l := New(model, phone, Options{Prompt: "Tap the heading 12 times.", Mode: "run", IdleWait: 50 * time.Millisecond, Emit: emit})
	if res := l.Run(context.Background()); res.Status != "done" || res.Last == nil || res.Last.Status != "not_completed" {
		t.Fatalf("result %+v", res)
	}
	// The first tap is new and four more go in the circle. The station
	// then has a word, and the other seven wait for the model to read it.
	if phone.taps != 5 {
		t.Errorf("%d taps reached the phone, want 5", phone.taps)
	}
	request := model.requests[1]
	answered(t, request)
	unrun, word := 0, -1
	for i, m := range request {
		s, _ := m.Content.(string)
		if m.Role == "tool" && strings.HasPrefix(s, "Error: not run") {
			unrun++
		}
		if m.Role == "user" && strings.Contains(s, "try something different") {
			word = i
		}
	}
	if unrun != 7 {
		t.Errorf("%d calls were answered as not run, want 7", unrun)
	}
	if word != len(request)-1 {
		t.Errorf("the station's word is message %d of %d, want the last", word+1, len(request))
	}
	if n := strings.Count(kinds(events()), "brake"); n != 1 {
		t.Errorf("%d brake events, want 1", n)
	}
	if model.forced[1] != "" {
		t.Errorf("the result was forced: %v", model.forced)
	}
}
