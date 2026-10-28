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
	err := Render(&buf, Page{Title: "pi / phone", DeviceID: "rig_1/phone", DeviceName: "phone",
		Shortcuts: []station.Shortcut{{Label: "Home", Keys: "GUI+h"}}, CSRF: "tok<>"})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{`data-device="rig_1/phone"`, `data-csrf="tok&lt;&gt;"`, `GUI&#43;h`, `/viewer/webrtc.js`, `id="feed"`, `id="rtcVideo"`, `id="rtcAudio"`, `href="/"`} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "onclick=") || strings.Contains(html, "style=") {
		t.Error("inline script or style: the page must satisfy a strict CSP")
	}
	srv := httptest.NewServer(http.StripPrefix("/viewer/", Assets()))
	defer srv.Close()
	for name, ctype := range map[string]string{"webrtc.js": "application/javascript", "input.js": "application/javascript", "viewer.js": "application/javascript", "viewer.css": "text/css"} {
		resp, err := http.Get(srv.URL + "/viewer/" + name)
		if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s: %v %v", name, resp, err)
		}
	}
	if resp, _ := http.Get(srv.URL + "/viewer/page.html"); resp.StatusCode != 404 {
		t.Error("the template is served")
	}
	if !bytes.Contains(WebRTCJS, []byte("__roadieRTCConfig")) || !bytes.Contains(InputJS, []byte("__roadieInput")) {
		t.Error("modules")
	}
}
