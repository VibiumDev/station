package viewer

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlayerAssets(t *testing.T) {
	if !HasPlayer() || PlayerVersion() == "" {
		t.Skip("no player embedded")
	}
	h := Assets()
	for _, c := range []struct {
		path, ctype string
	}{
		{"/player/vibium-record-player.js", "application/javascript; charset=utf-8"},
		{"/player/VERSION", ""},
		{"/player/third-party/ghostty/LICENSE.txt", "text/plain; charset=utf-8"},
		{"/player/nope.js", ""},
		{"/player/../../etc/passwd", ""},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", c.path, nil))
		if c.ctype == "" {
			if w.Code != http.StatusNotFound {
				t.Errorf("%s: want 404, got %d", c.path, w.Code)
			}
			continue
		}
		if w.Code != 200 || w.Header().Get("Content-Type") != c.ctype {
			t.Errorf("%s: %d %s", c.path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// Every asset the bundle names must be served.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/player/vibium-record-player.js", nil))
	for _, name := range []string{"ghostty-vt", "JetBrainsMono-Regular"} {
		if !contains(w.Body.String(), "assets/"+name) {
			t.Errorf("the bundle does not name %s", name)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/player/assets/"+assetNamed(t, "ghostty-vt"), nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/wasm" {
		t.Errorf("wasm asset: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func assetNamed(t *testing.T, prefix string) string {
	t.Helper()
	entries, err := files.ReadDir("player/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) >= len(prefix) && e.Name()[:len(prefix)] == prefix {
			return e.Name()
		}
	}
	t.Fatalf("no asset starts with %s", prefix)
	return ""
}
