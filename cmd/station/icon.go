package main

import (
	_ "embed"
	"net/http"
)

// The station's icon, for browser tabs and home screens: the satellite
// antenna, as Roadie's is the guitar.
//
//go:embed icon.png
var iconPNG []byte

func serveIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(iconPNG)
}
