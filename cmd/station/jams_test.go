package main

import (
	"testing"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/jam"
)

// A jam's result is its first prompt's. A later result of the same
// kind replaces it; one of the other kind stays in the transcript.
func TestTakeResultKeepsTheJamsKind(t *testing.T) {
	var j station.Jam
	takeResult(&j, nil)
	if j.Took != "" {
		t.Errorf("nil result set %+v", j)
	}
	takeResult(&j, &jam.Verdict{Took: "check", Status: "passed", Summary: "Checkout works."})
	takeResult(&j, &jam.Verdict{Took: "run", Status: "completed", Summary: "Session ended."})
	if j.Took != "check" || j.Result != "passed" || j.Summary != "Checkout works." {
		t.Errorf("an errand after a check took over: %+v", j)
	}
	takeResult(&j, &jam.Verdict{Took: "check", Status: "failed", Summary: "The coupon field is gone."})
	if j.Result != "failed" || j.Summary != "The coupon field is gone." {
		t.Errorf("a second check did not replace the first: %+v", j)
	}
}

// The viewer's key syntax becomes Vibium's: GUI is Meta, Ctrl is Control.
func TestJamShortcutsSpeakVibium(t *testing.T) {
	got := jamShortcuts([]station.Shortcut{{Label: "Home", Keys: "GUI+h"}, {Label: "Back", Keys: "Escape"}, {Label: "Lock", Keys: "Ctrl+GUI+q"}, {Label: "Recents", Keys: "Alt+Tab"}})
	want := []jam.Shortcut{{Label: "Home", Keys: "Meta+h"}, {Label: "Back", Keys: "Escape"}, {Label: "Lock", Keys: "Control+Meta+q"}, {Label: "Recents", Keys: "Alt+Tab"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %v, want %v", i, got[i], want[i])
		}
	}
	if jamShortcuts(nil) != nil {
		t.Error("no shortcuts should stay none")
	}
}
