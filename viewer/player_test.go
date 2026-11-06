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
		{"/player/app", "text/html; charset=utf-8"},
		{"/player/app/index.html", "text/html; charset=utf-8"},
		{"/player/VERSION", ""},
		{"/player/app/third-party/ghostty/LICENSE.txt", "text/plain; charset=utf-8"},
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
	// The page names its script and stylesheet under the station's path,
	// and carries the player's own policy.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/player/app/", nil))
	if !contains(w.Body.String(), `src="/viewer/player/app/assets/`) || w.Header().Get("Content-Security-Policy") != PlayerCSP {
		t.Errorf("the page: %d %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/player/app/assets/"+assetNamed(t, "ghostty-vt"), nil))
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
	entries, err := files.ReadDir("player/app/assets")
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
