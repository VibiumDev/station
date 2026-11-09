package viewer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VibiumDev/station"
)

func TestRenderAndAssets(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{Mode: "station", Device: "rig_1/phone", Name: "pi / phone", API: Relayed("", "rig_1/phone"),
		Shortcuts: []station.Shortcut{{Label: "Home", Keys: "GUI+h"}}, CSRF: "tok<>", Extras: "/v1/devices/rig_1/phone/extras.js", WebRTC: true}
	if err := Render(&buf, Page{Title: "pi / phone", Config: cfg, Minimal: true}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{`data-config="`, `rig_1/phone/stream`, `tok\u003c\u003e`, `/viewer/webrtc.js`, `/viewer/shell.js`, `/viewer/shell.css`,
		`id="feed"`, `id="rtcVideo"`, `id="rtcAudio"`, `id="takeBtn"`, `id="qslider"`, `rig_1/phone/extras.js`, `#toolbar { display:none !important; }`} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "onclick=") || strings.Contains(html, "javascript:") {
		t.Error("inline script: the page must satisfy a strict script CSP")
	}
	var local bytes.Buffer
	if err := Render(&local, Page{Title: "roadie", Config: Config{Mode: "local", API: Local()}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(local.String(), "extras.js") || !strings.Contains(local.String(), `/api/webrtc/signal`) {
		t.Error("a local page without extras names them, or lacks its endpoints")
	}
	srv := httptest.NewServer(http.StripPrefix("/viewer/", Assets()))
	defer srv.Close()
	for name, ctype := range map[string]string{"webrtc.js": "application/javascript", "shell.js": "application/javascript", "shell.css": "text/css", "pcm-player.js": "application/javascript", "vibium-logo.png": "image/png"} {
		resp, err := http.Get(srv.URL + "/viewer/" + name)
		if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s: %v %v", name, resp, err)
		}
	}
	if resp, _ := http.Get(srv.URL + "/viewer/shell.html"); resp.StatusCode != 404 {
		t.Error("the template is served")
	}
	if !bytes.Contains(WebRTCJS, []byte("__roadieRTCConfig")) {
		t.Error("the transport module")
	}
}

// A watching page carries the flag in its config and a way to the
// driving page; a driving page a way to the watching one.
func TestRenderWatchAndDriveLinks(t *testing.T) {
	var watch strings.Builder
	if err := Render(&watch, Page{Title: "Watching pi / phone", Config: Config{Mode: "station", Watch: true, API: Relayed("", "rig_a/phone")}, Drive: "/devices/rig_a/phone/drive"}); err != nil {
		t.Fatal(err)
	}
	if h := watch.String(); !strings.Contains(h, `&#34;watch&#34;:true`) && !strings.Contains(h, `"watch":true`) || !strings.Contains(h, `id="driveLink"`) || strings.Contains(h, `id="watchLink"`) {
		t.Errorf("watching page: %q", h[:min(len(h), 300)])
	}
	var drive strings.Builder
	if err := Render(&drive, Page{Title: "pi / phone", Config: Config{Mode: "station", API: Relayed("", "rig_a/phone")}, Watch: "/devices/rig_a/phone/view"}); err != nil {
		t.Fatal(err)
	}
	if h := drive.String(); strings.Contains(h, `watch&#34;:true`) || strings.Contains(h, `"watch":true`) || !strings.Contains(h, `id="watchLink"`) || strings.Contains(h, `id="driveLink"`) {
		t.Error("driving page carries the watch flag or the wrong link")
	}
}

// The panel's input rows are wrapped so a watching page can hide them,
// and the shell hides them, the microphone and the keyboard, and tells
// a rig's extras that it watches.
func TestWatchingPageHidesInput(t *testing.T) {
	var out strings.Builder
	if err := Render(&out, Page{Title: "Watching", Config: Config{Mode: "local", Watch: true, API: Local()}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `id="inputRows"`) {
		t.Error("the input rows have no wrapper to hide")
	}
	shell, _ := files.ReadFile("shell.js")
	js := string(shell)
	for _, want := range []string{"['ptt', 'kbdBtn', 'inputRows']", "watching: watchOnly,", "return !watchOnly && (!station", "'watching only'", "'Stop watching'", "if (hold && !observer && !released) {", "'Stopped watching.'", "['homeLink', 'watchLink']"} {
		if !strings.Contains(js, want) {
			t.Errorf("shell.js lacks %q", want)
		}
	}
}
