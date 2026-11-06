package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
	"github.com/VibiumDev/station/records"
	"github.com/VibiumDev/station/viewer"
)

// --- The Record Store: recordings, screenshots and logs as Blossom blobs ---

// recordsError answers with the status a Record Store refusal carries.
func recordsError(w http.ResponseWriter, err error) {
	var e *records.Error
	if errors.As(err, &e) {
		apiError(w, e.Status, e.Message)
		return
	}
	apiError(w, http.StatusInternalServerError, err.Error())
}

// principalOrNil is the station's view of a Blossom caller, when it has
// one: a token, a session or a tailnet identity; none is no error here,
// since a Blossom authorization may stand on its own.
func (s *server) principalOrNil(r *http.Request) *station.Principal {
	p, err := s.auth.Authenticate(r)
	if err != nil {
		return nil
	}
	return &p
}

// blossomBlob is GET, HEAD and DELETE /<sha256>[.ext].
func (s *server) blossomBlob(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete && s.auth.sessionOf(r) != nil && !s.auth.csrfOK(r) {
		apiError(w, http.StatusForbidden, "missing or stale CSRF token")
		return
	}
	s.records.ServeBlob(w, r, r.PathValue("sha"), s.principalOrNil(r))
}

// blossomList is GET /list/<pubkey>.
func (s *server) blossomList(w http.ResponseWriter, r *http.Request) {
	s.records.ServeList(w, r, r.PathValue("pubkey"), s.principalOrNil(r))
}

// uploadAuth is POST /v1/records/upload-auth: {"sha256", "size", "type",
// "name", "hold"} gives a signed Blossom authorization to upload that
// one file, good for five minutes, and where to send it.
func (s *server) uploadAuth(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var req records.UploadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with sha256 and size is needed")
		return
	}
	ev, err := s.records.UploadAuth(r.Context(), p, req)
	if err != nil {
		recordsError(w, err)
		return
	}
	apiJSON(w, map[string]any{
		"authorization": records.Header(ev),
		"event":         ev,
		"upload":        strings.TrimSuffix(s.publicURL, "/") + "/upload",
		"expires":       time.Now().Add(records.AuthLifetime).UTC().Format(time.RFC3339),
	})
}

// downloadAuth is POST /v1/records/{sha}/download-auth: a signed
// authorization to fetch one blob, for a caller who may read it.
func (s *server) downloadAuth(w http.ResponseWriter, r *http.Request, p station.Principal) {
	sha := r.PathValue("sha")
	ev, err := s.records.DownloadAuth(r.Context(), p, sha)
	if err != nil {
		recordsError(w, err)
		return
	}
	b, err := s.db.Blob(r.Context(), sha)
	if err != nil {
		recordsError(w, err)
		return
	}
	apiJSON(w, map[string]any{
		"authorization": records.Header(ev),
		"event":         ev,
		"url":           s.records.URL(b),
		"expires":       time.Now().Add(records.AuthLifetime).UTC().Format(time.RFC3339),
	})
}

// visibleRecords lists the blobs a principal may read, narrowed by
// device and hold.
func (s *server) visibleRecords(r *http.Request, p station.Principal, device string) ([]station.Blob, error) {
	q := station.BlobQuery{DeviceID: device, HoldID: r.FormValue("hold"), Limit: 500}
	bs, err := s.db.Blobs(r.Context(), q)
	if err != nil {
		return nil, err
	}
	var out []station.Blob
	for _, b := range bs {
		if s.records.MayRead(r.Context(), b, p) {
			out = append(out, b)
		}
	}
	return out, nil
}

// listRecords is GET /v1/records (?device=, ?hold=) and
// GET /v1/devices/{rig}/{name}/recordings: the blobs the caller may read.
func (s *server) listRecords(w http.ResponseWriter, r *http.Request, p station.Principal) {
	device := r.FormValue("device")
	if r.PathValue("rig") != "" {
		device = deviceID(r)
	}
	bs, err := s.visibleRecords(r, p, device)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []records.Descriptor{}
	for _, b := range bs {
		out = append(out, s.records.Describe(b))
	}
	apiJSON(w, out)
}

// recordView is a blob as the Records page shows it.
type recordView struct {
	station.Blob
	URL     string
	When    string
	Size    string
	Device  string
	Label   string
	CanEdit bool
	CanPlay bool
}

// humanAge is a retention age for the pages: empty for none.
func humanAge(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < 24*time.Hour:
		return d.String()
	case d < 48*time.Hour:
		return "a day"
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return strconv.FormatInt(n, 10) + " B"
}

func (s *server) recordsData(r *http.Request, p station.Principal, extra map[string]any) (map[string]any, error) {
	device := r.FormValue("device")
	bs, err := s.visibleRecords(r, p, device)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	if devs, err := s.devices(r.Context()); err == nil {
		for _, d := range devs {
			if d.Label != "" {
				labels[d.ID] = d.Label
			} else {
				labels[d.ID] = d.Name
			}
		}
	}
	views := make([]recordView, 0, len(bs))
	for _, b := range bs {
		views = append(views, recordView{Blob: b, URL: s.records.URL(b), When: b.CreatedAt.Local().Format("Jan 2 15:04"), Size: humanSize(b.Size),
			Device: b.DeviceID, Label: labels[b.DeviceID], CanEdit: p.Admin || p.Subject == b.Uploader, CanPlay: viewer.HasPlayer() && (b.Type == "application/zip")})
	}
	count, size, _ := s.db.BlobTotal(r.Context())
	data := map[string]any{
		"Page": "records", "Principal": p, "CSRF": s.auth.csrfToken(r), "Records": views, "Device": device,
		"DeviceLabel": labels[device], "Count": count, "Total": humanSize(size),
		"MaxFile": humanSize(s.records.MaxFile), "MaxTotal": humanSize(s.records.MaxTotal), "MaxAge": humanAge(s.records.MaxAge),
	}
	for k, v := range extra {
		data[k] = v
	}
	return data, nil
}

// playPage is GET /records/{sha}/play: the recording in the embedded
// Record Player, which fetches the file with the page's own session.
func (s *server) playPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	sha := r.PathValue("sha")
	b, err := s.db.Blob(r.Context(), sha)
	if err != nil || !s.records.MayRead(r.Context(), b, p) {
		http.Error(w, "no such recording", http.StatusNotFound)
		return
	}
	if !viewer.HasPlayer() {
		http.Redirect(w, r, s.records.URL(b)+"?download", http.StatusSeeOther)
		return
	}
	name := b.Name
	if name == "" {
		name = sha[:12]
	}
	// The player runs WebAssembly for terminal recordings, which the
	// station's usual policy does not allow; this page alone does.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; worker-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
	s.render(w, "play", map[string]any{"Page": "records", "Principal": p, "CSRF": s.auth.csrfToken(r), "Blob": b, "Title": name,
		"Src": "/" + b.SHA256 + blobExt(b.Type), "Download": s.records.URL(b) + "?download", "Device": b.DeviceID,
		"When": b.CreatedAt.Local().Format("Jan 2, 2006 15:04"), "Size": humanSize(b.Size), "PlayerVersion": viewer.PlayerVersion()})
}

func blobExt(mediaType string) string { return blob.Extension(mediaType) }

func (s *server) recordsPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	data, err := s.recordsData(r, p, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "records", data)
}

// backToRecords returns to the Records page the form came from.
func backToRecords(w http.ResponseWriter, r *http.Request) {
	to := "/records"
	if d := r.FormValue("device"); d != "" {
		to += "?device=" + url.QueryEscape(d)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// recordPublic is POST /records/public: sha256 and public=1|0.
func (s *server) recordPublic(w http.ResponseWriter, r *http.Request, p station.Principal) {
	err := s.records.SetPublic(r.Context(), p, r.FormValue("sha256"), r.FormValue("public") == "1")
	if err != nil {
		recordsError(w, err)
		return
	}
	backToRecords(w, r)
}

// recordDelete is POST /records/delete: sha256.
func (s *server) recordDelete(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if err := s.records.Delete(r.Context(), p, r.FormValue("sha256")); err != nil {
		recordsError(w, err)
		return
	}
	backToRecords(w, r)
}

// parseSize reads "256M", "2G", "512K" or plain bytes.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a size (bytes, or K, M, G)", s)
	}
	return n * mult, nil
}
