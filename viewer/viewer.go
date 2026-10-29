// Package viewer is the browser side shared by a station and a Roadie
// host: the WebRTC transport module, the input mapping, and the station's
// device-neutral viewer page. A Roadie host serves its own page with the
// phone's buttons and takes the transport module from here, so both run
// the same code against the same host.
package viewer

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/VibiumDev/station"
)

//go:embed webrtc.js input.js viewer.js viewer.css page.html
var files embed.FS

func mustRead(name string) []byte {
	data, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}

// WebRTCJS is the transport module, for a host that serves its own page.
var WebRTCJS = mustRead("webrtc.js")

// InputJS is the input mapping module.
var InputJS = mustRead("input.js")

var page = template.Must(template.ParseFS(files, "page.html"))

// Page is what the station's viewer page is rendered with.
type Page struct {
	Title      string
	DeviceID   string
	DeviceName string
	Shortcuts  []station.Shortcut
	Input      string // the device's pointer mode, "touch" or "mouse", when it says
	API        string // prefix of the station's API, usually empty
	Assets     string // prefix the module files are served under, usually empty
	CSRF       string // the session's CSRF token, sent back with API calls
	Back       string // where the arrow goes
}

// ShortcutsJSON is the device's shortcuts for the page's data attribute.
func (p Page) ShortcutsJSON() string {
	data, _ := json.Marshal(p.Shortcuts)
	return string(data)
}

// Render writes the viewer page.
func Render(w io.Writer, p Page) error {
	if p.Back == "" {
		p.Back = "/"
	}
	return page.Execute(w, p)
}

// Assets serves the module files; mount it at "<prefix>/viewer/" with the
// prefix stripped, so the page's script tags resolve.
func Assets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		var ctype string
		switch {
		case strings.HasSuffix(name, ".js"):
			ctype = "application/javascript; charset=utf-8"
		case strings.HasSuffix(name, ".css"):
			ctype = "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := files.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	})
}
