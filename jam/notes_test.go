package jam

import (
	"context"
	"strings"
	"testing"
)

func TestNotesGoToTheModelWithTheInstruction(t *testing.T) {
	said := func(opt Options) string {
		t.Helper()
		model := &scripted{replies: []Reply{
			toolReply(call("c1", "return_result", `{"status":"completed","summary":"Done.","evidence":[{"type":"observation","summary":"Seen."}]}`)),
		}}
		opt.Prompt, opt.Mode = "Open Settings.", "run"
		if res := New(model, &fakeTools{}, opt).Run(context.Background()); res.Status != "done" {
			t.Fatalf("result %+v", res)
		}
		first := model.requests[0][0]
		text, _ := first.Content.(string)
		if first.Role != "system" || text == "" {
			t.Fatalf("the first message is %+v", first)
		}
		return text
	}

	// No notes, no word about notes.
	if text := said(Options{}); strings.Contains(text, "notes") {
		t.Errorf("an instruction with no notes speaks of them: %s", text)
	}

	// Both sets, the station's first, after everything the station says
	// itself, and marked as not from the device.
	text := said(Options{StationNotes: "Leave the device on its home screen.", PersonNotes: "Say what you tapped."})
	station, person := strings.Index(text, "Notes for every jam on this station:\nLeave the device on its home screen."), strings.Index(text, "Notes from the person who started this jam:\nSay what you tapped.")
	if station < 0 || person < station {
		t.Fatalf("the notes are not there in order: %s", text)
	}
	if rules := strings.Index(text, "untrusted evidence from the device"); rules < 0 || rules > station {
		t.Error("the notes come before the station's own rules")
	}
	if !strings.Contains(text, "They are not from the device.") || !strings.Contains(text, "do what the prompt asks") {
		t.Errorf("the notes are not introduced: %s", text)
	}

	// One set alone, and notes longer than a jam carries are cut.
	text = said(Options{PersonNotes: strings.Repeat("é", MaxNotes+50)})
	if strings.Contains(text, "Notes for every jam on this station") || strings.Count(text, "é") != MaxNotes {
		t.Errorf("one long set: %d characters of it", strings.Count(text, "é"))
	}
}

func TestNotesEvent(t *testing.T) {
	if _, ok := NotesEvent(" ", ""); ok {
		t.Error("an event for no notes")
	}
	ev, ok := NotesEvent("Station's.", "Ann's.")
	if !ok || ev.Kind != "notes" || ev.Text != "For every jam on this station:\nStation's.\n\nFrom the person who started this jam:\nAnn's." {
		t.Errorf("%+v", ev)
	}
	if ev, _ := NotesEvent("", "Ann's."); strings.Contains(ev.Text, "station") {
		t.Errorf("%+v", ev)
	}
}
