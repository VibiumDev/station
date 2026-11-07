package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A records form returns to the page it came from: the same device,
// view and hold filter, not the grid of every device.
func TestBackToRecordsKeepsView(t *testing.T) {
	cases := []struct{ form, want string }{
		{"", "/records"},
		{"device=rig_a%2Fphone", "/records?device=rig_a%2Fphone"},
		{"device=rig_a%2Fphone&view=list", "/records?device=rig_a%2Fphone&view=list"},
		{"view=list&hold=h1", "/records?hold=h1&view=list"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/records/public", strings.NewReader(c.form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		backToRecords(w, r)
		if got := w.Header().Get("Location"); got != c.want {
			t.Errorf("form %q: redirected to %q, want %q", c.form, got, c.want)
		}
	}
}
