// Package viewer is the browser side shared by a station and a Roadie
// host: one shell, with Roadie's look and all of its viewer's features,
// served by both, with the WebRTC transport module and the audio
// worklets. The shell is device-neutral; a rig's own buttons and panels
// come from an extras module the shell loads, straight from a Roadie or
// through a station's relay.
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

//go:embed webrtc.js shell.js shell.css shell.html pcm-player.js pcm-sender.js pages.css pages.js vibium-logo.png plex-sans.woff2 plex-mono-400.woff2 plex-mono-500.woff2 rajdhani-600.woff2 rajdhani-700.woff2 player
var files embed.FS

func mustRead(name string) []byte {
	data, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}

// WebRTCJS is the transport module, for a host that serves it alone.
var WebRTCJS = mustRead("webrtc.js")

var page = template.Must(template.ParseFS(files, "shell.html"))

// Endpoints is where the shell reaches the device: a Roadie's own paths,
// or a station's relayed ones.
type Endpoints struct {
	Stream   string `json:"stream"`         // the MJPEG stream, read with ?as=bytes
	Health   string `json:"health"`         // the host's health JSON
	Snapshot string `json:"snapshot"`       // one JPEG, ?quality=
	HID      string `json:"hid"`            // the input WebSocket
	Audio    string `json:"audio"`          // the sound WebSocket
	Mic      string `json:"mic"`            // the microphone WebSocket
	Signal   string `json:"signal"`         // WebRTC signaling
	API      string `json:"api"`            // the prefix of the host's /api, for extras
	Hold     string `json:"hold,omitempty"` // a station's hold endpoint
}

// Config is what the shell is told about the page it runs on.
type Config struct {
	// Mode is "local" (a Roadie's own page: no holds) or "station".
	Mode string `json:"mode"`
	// Device is the device id on a station, empty on a Roadie.
	Device string `json:"device,omitempty"`
	// Name is shown in the title and screenshots.
	Name string    `json:"name,omitempty"`
	API  Endpoints `json:"api"`
	// InputMode preselects mouse or touch for this load; empty leaves it
	// to the browser's choice.
	InputMode string `json:"inputMode,omitempty"`
	// Transport preselects webrtc or mjpeg for this load.
	Transport string `json:"transport,omitempty"`
	// WebRTC says the host offers it.
	WebRTC bool `json:"webrtc"`
	// Shortcuts are the device's announced buttons, shown when there are
	// no extras to provide better ones.
	Shortcuts []station.Shortcut `json:"shortcuts,omitempty"`
	// CSRF is a station session's token, sent back with API calls.
	CSRF string `json:"csrf,omitempty"`
	// Extras is the URL of the device's extras module, when it has one.
	Extras string `json:"extras,omitempty"`
	// Info is what the extras want to know about the host, opaque here.
	Info map[string]any `json:"info,omitempty"`
	// Minimal says the toolbar is hidden: a page in a frame or driven by
	// a script, which gets a top-left picture rather than a centered one.
	Minimal bool `json:"minimal,omitempty"`
	// Recorded says the host records every hold on this device and
	// uploads it to the station's Record Store; the toolbar says so.
	Recorded bool `json:"recorded,omitempty"`
}

// Page is what the shell page is rendered with.
type Page struct {
	Title  string
	Assets string // prefix the module files are served under, usually empty
	// Minimal hides the toolbar, for a page embedded in a frame.
	Minimal bool
	// Home is where the toolbar's first button leads, the host's front
	// page as a rule; empty leaves the button out. HomeTitle names it.
	Home      string
	HomeTitle string
	Config    Config
}

// ConfigJSON is the config for the page's data attribute.
func (p Page) ConfigJSON() string {
	cfg := p.Config
	cfg.Minimal = p.Minimal
	data, _ := json.Marshal(cfg)
	return string(data)
}

// Render writes the shell page.
func Render(w io.Writer, p Page) error {
	return page.Execute(w, p)
}

// Local is the config for a Roadie's own page: its endpoints at the root.
func Local() Endpoints {
	return Endpoints{Stream: "/stream", Health: "/health", Snapshot: "/snapshot", HID: "/api/hid/ws",
		Audio: "/audio", Mic: "/api/mic", Signal: "/api/webrtc/signal", API: "/api"}
}

// Relayed is the config for a device on a station, under its API prefix.
func Relayed(prefix, deviceID string) Endpoints {
	d := prefix + "/v1/devices/" + deviceID
	return Endpoints{Stream: d + "/stream", Health: d + "/health", Snapshot: d + "/screenshot", HID: d + "/hid",
		Audio: d + "/audio", Mic: d + "/mic", Signal: d + "/signal", API: d + "/api", Hold: d + "/hold"}
}

// Assets serves the module files; mount it at "<prefix>/viewer/" with the
// prefix stripped, so the page's script tags resolve. The embedded Record
// Player's files are under player/.
func Assets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		// The player keeps its files in directories of its own, and its
		// page is the directory itself.
		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		player := strings.HasPrefix(rel, "player/")
		if player {
			name = rel
			if name == "player/app" {
				name = "player/app/index.html"
			}
		}
		var ctype string
		switch {
		case strings.HasSuffix(name, ".js"):
			ctype = "application/javascript; charset=utf-8"
		case strings.HasSuffix(name, ".css"):
			ctype = "text/css; charset=utf-8"
		case strings.HasSuffix(name, ".woff2"):
			ctype = "font/woff2"
		case strings.HasSuffix(name, ".ttf"):
			ctype = "font/ttf"
		case strings.HasSuffix(name, ".wasm"):
			ctype = "application/wasm"
		case strings.HasSuffix(name, ".json"):
			ctype = "application/json"
		case player && strings.HasSuffix(name, ".html"):
			ctype = "text/html; charset=utf-8"
		case player && strings.HasSuffix(name, ".txt"):
			ctype = "text/plain; charset=utf-8"
		case player && strings.HasSuffix(name, ".ico"):
			ctype = "image/x-icon"
		case strings.HasSuffix(name, ".png"):
			ctype = "image/png"
		case player && strings.HasSuffix(name, ".svg"):
			ctype = "image/svg+xml"
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
		if ctype == "text/html; charset=utf-8" {
			// The player runs WebAssembly for terminal recordings and draws
			// frames from blobs, which the station's usual policy does not
			// allow; its page alone does.
			w.Header().Set("Content-Security-Policy", PlayerCSP)
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(data)
			return
		}
		switch {
		case player, ctype == "font/woff2", ctype == "image/png":
			w.Header().Set("Cache-Control", "public, max-age=604800")
		default:
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Write(data)
	})
}

// PlayerCSP is the Content Security Policy the embedded player's page
// is served with: the station's, plus what the player needs, and it may
// be framed by the station's own pages.
const PlayerCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; worker-src 'self' blob:; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'self'; object-src 'none'"

// PlayerPath is where the embedded Record Player's page is served, under
// the assets prefix; a recording opens there with ?record=<sha256>.zip.
const PlayerPath = "/viewer/player/app/"

// PlayerVersion is the Record Player commit the embedded player was
// built from, or empty when none is embedded.
func PlayerVersion() string {
	v, err := files.ReadFile("player/VERSION")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(v))
}

// HasPlayer says whether the Record Player is embedded.
func HasPlayer() bool {
	_, err := files.ReadFile("player/app/index.html")
	return err == nil
}
