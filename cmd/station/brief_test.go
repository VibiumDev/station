package main

import "testing"

func TestBriefShortensALongPrompt(t *testing.T) {
	short := "check that 1+1=2 in the calculator app"
	if got := brief(short, 120); got != short {
		t.Errorf("a short prompt changed: %q", got)
	}
	long := "Open https://soundcheck.vibium.dev/?seconds=20 in Safari. Tap Mic, and allow the microphone if the phone asks. The page then records the microphone for 20 seconds and plays the recording back."
	got := brief(long, 120)
	if n := len([]rune(got)); n > 120 || got[len(got)-len("…"):] != "…" {
		t.Errorf("%d characters: %q", n, got)
	}
	if got != "Open https://soundcheck.vibium.dev/?seconds=20 in Safari. Tap Mic, and allow the microphone if the phone asks. The page…" {
		t.Errorf("not cut at a word: %q", got)
	}
	if got := brief("two\n\nlines   of   prompt", 120); got != "two lines of prompt" {
		t.Errorf("line breaks kept: %q", got)
	}
	if got := brief("ééééééééééé", 5); got != "éééé…" {
		t.Errorf("cut inside a character: %q", got)
	}
}
