package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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
	if r.Method == http.MethodDelete && !s.auth.csrfOK(r) {
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
// one file, good for five minutes, and where to send it. Watching is
// read-only: it takes drive access to some device.
func (s *server) uploadAuth(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if !p.Admin {
		u, err := s.db.User(r.Context(), p.User)
		if err != nil || tokenRank(s.tokenCeiling(r.Context(), u)) < tokenRank(station.TokenDrive) {
			apiError(w, http.StatusForbidden, "watch access is read-only: uploading a record takes drive access to a device")
			return
		}
	}
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

// recordsQuery is what the Records page is asked to show: the device
// and hold, the words and #tags typed, golden records alone, the view,
// and which page of how many.
type recordsQuery struct {
	Device, Hold string
	Raw          string   // the search as typed
	Text         string   // its words that are not tags
	Tags         []string // its #tags, as filed
	Golden       bool
	View         string // "grid", "list" or "strip" when chosen or remembered; "" means the default, grid
	Per, Page    int
}

// The browser's last choices on the Records page, kept in cookies so the
// plain Records link brings the page back as it was: the view, and how
// many records a page shows.
const (
	viewCookie = "station-records-view"
	perCookie  = "station-records-per"
)

// recordsView says whether a word names one of the page's views.
func recordsView(v string) bool { return v == "grid" || v == "list" || v == "strip" }

// perPage is the choice of records per page; defaultPer is the first
// page's.
var perPage = []int{10, 25, 50, 100}

const defaultPer = 25

// parseRecordsQuery reads the page's query: q is words and #tags, n one
// of 10, 25, 50 or 100, page from 1, golden=1 golden records alone,
// view=list the list and view=strip the filmstrips.
func parseRecordsQuery(r *http.Request) recordsQuery {
	q := recordsQuery{Device: r.FormValue("device"), Hold: r.FormValue("hold"), Raw: strings.TrimSpace(r.FormValue("q")),
		Golden: r.FormValue("golden") == "1", Per: defaultPer, Page: 1}
	view := r.FormValue("view")
	if view == "" {
		if c, err := r.Cookie(viewCookie); err == nil {
			view = c.Value
		}
	}
	if recordsView(view) {
		q.View = view
	}
	var words []string
	for _, w := range strings.Fields(q.Raw) {
		if strings.HasPrefix(w, "#") && len(w) > 1 {
			if tags, err := records.ParseTags(w); err == nil && len(tags) == 1 {
				q.Tags = append(q.Tags, tags[0])
				continue
			}
		}
		words = append(words, w)
	}
	q.Text = strings.Join(words, " ")
	per := r.FormValue("n")
	if per == "" {
		if c, err := r.Cookie(perCookie); err == nil {
			per = c.Value
		}
	}
	if n, err := strconv.Atoi(per); err == nil {
		for _, p := range perPage {
			if n == p {
				q.Per = n
			}
		}
	}
	if p, err := strconv.Atoi(r.FormValue("page")); err == nil && p > 1 {
		q.Page = p
	}
	return q
}

// values is the query as URL parameters, with the changes given as
// key, value pairs; an empty value drops the key, and any change but
// the page itself goes back to the first page.
func (q recordsQuery) values(changes ...string) url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val == "" || (k == "page" && val == "1") {
			v.Del(k)
			return
		}
		v.Set(k, val)
	}
	set("device", q.Device)
	set("hold", q.Hold)
	set("q", q.Raw)
	if q.View != "" {
		v.Set("view", q.View)
	}
	if q.Golden {
		v.Set("golden", "1")
	}
	if q.Per != defaultPer {
		v.Set("n", strconv.Itoa(q.Per))
	}
	set("page", strconv.Itoa(q.Page))
	paged := false
	for i := 0; i+1 < len(changes); i += 2 {
		set(changes[i], changes[i+1])
		paged = paged || changes[i] == "page"
	}
	if len(changes) > 0 && !paged {
		v.Del("page")
	}
	return v
}

// Link is the Records page with the query changed, for the templates.
func (q recordsQuery) Link(changes ...string) string {
	v := q.values(changes...)
	if len(v) == 0 {
		return "/records"
	}
	return "/records?" + v.Encode()
}

// field is one hidden form field.
type field struct{ Name, Value string }

// back is the query as hidden fields, so a form returns to the same
// page; keep leaves out what the search form sets itself.
func (q recordsQuery) back(keep bool) []field {
	var out []field
	v := q.values()
	for _, k := range []string{"device", "hold", "view", "golden", "q", "n", "page"} {
		if keep && (k == "q" || k == "n" || k == "page") {
			continue
		}
		if val := v.Get(k); val != "" {
			out = append(out, field{k, val})
		}
	}
	return out
}

// withTag is the search with a tag added, or taken out when it is there.
func (q recordsQuery) withTag(tag string) string {
	var words []string
	found := false
	for _, w := range strings.Fields(q.Raw) {
		if strings.EqualFold(w, "#"+tag) {
			found = true
			continue
		}
		words = append(words, w)
	}
	if !found {
		words = append(words, "#"+tag)
	}
	return strings.Join(words, " ")
}

// paging places a page in a list of found records: the page as clamped,
// how many pages there are, and the slice bounds.
func paging(found, per, page int) (p, pages, from, to int) {
	pages = (found + per - 1) / per
	if pages < 1 {
		pages = 1
	}
	p = min(max(page, 1), pages)
	from = (p - 1) * per
	to = min(from+per, found)
	return p, pages, from, to
}

// visibleRecords lists the blobs a principal may read, as the query
// narrows them.
func (s *server) visibleRecords(r *http.Request, p station.Principal, q recordsQuery) ([]station.Blob, error) {
	bq := station.BlobQuery{DeviceID: q.Device, HoldID: q.Hold, Text: q.Text, Tags: q.Tags, Golden: q.Golden, Limit: 5000}
	bs, err := s.db.Blobs(r.Context(), bq)
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

// listRecords is GET /v1/records (?device=, ?hold=, ?q=, ?tag=,
// ?golden=1) and GET /v1/devices/{rig}/{name}/recordings: the blobs the
// caller may read.
func (s *server) listRecords(w http.ResponseWriter, r *http.Request, p station.Principal) {
	q := parseRecordsQuery(r)
	if r.PathValue("rig") != "" {
		q.Device = deviceID(r)
	}
	for _, t := range r.Form["tag"] {
		if tags, err := records.ParseTags(t); err == nil {
			q.Tags = append(q.Tags, tags...)
		}
	}
	bs, err := s.visibleRecords(r, p, q)
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

// setRecordGolden is POST /v1/records/{sha}/golden: {"golden": true}
// marks the record golden, false plain again.
func (s *server) setRecordGolden(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		Golden bool `json:"golden"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with golden is needed")
		return
	}
	sha := r.PathValue("sha")
	if err := s.records.SetGolden(r.Context(), p, sha, body.Golden); err != nil {
		recordsError(w, err)
		return
	}
	b, _ := s.db.Blob(r.Context(), sha)
	apiJSON(w, s.records.Describe(b))
}

// setRecordTags is PUT /v1/records/{sha}/tags: {"tags": ["flaky",
// "release-1.2"]}, or the tags as one string of words, replaces the
// record's tags.
func (s *server) setRecordTags(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		Tags json.RawMessage `json:"tags"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with tags is needed")
		return
	}
	var typed string
	var list []string
	if json.Unmarshal(body.Tags, &list) == nil {
		typed = strings.Join(list, " ")
	} else if json.Unmarshal(body.Tags, &typed) != nil {
		apiError(w, http.StatusBadRequest, "tags is a list of words, or one string of them")
		return
	}
	sha := r.PathValue("sha")
	if _, err := s.records.SetTags(r.Context(), p, sha, typed); err != nil {
		recordsError(w, err)
		return
	}
	b, _ := s.db.Blob(r.Context(), sha)
	apiJSON(w, s.records.Describe(b))
}

// recordView is a blob as the Records page shows it.
type recordView struct {
	station.Blob
	Title   string // the name without its extension, or the hash's start
	Kind    string // the file's kind in a word: zip, jpg, txt, else the media type
	URL     string
	Play    string
	When    string
	Size    string
	Device  string
	Label   string
	CanEdit bool
	CanPlay bool
	// Open is where the record's name and picture lead: the player for a
	// recording, the file itself when a browser can show or play it, and
	// a download for anything else. OpenWord names which: Play, Open or
	// Download.
	Open, OpenWord string
	// HasThumb says a picture can be shown: a recording's last frame, or
	// the image itself.
	HasThumb bool
	// Sound is a sound file, shown as its waveform: wide and short, where
	// a recording's frames are tall.
	Sound bool
	// Meta is what a recording holds, such as "17 steps · 18 s".
	Meta string
	// Strip is the recording's filmstrip: the numbers of its frames.
	Strip []int
	// TagLinks are the record's tags, each a link that searches for it.
	TagLinks []chip
}

// chip is a tag as the page shows it: a link that adds it to the
// search, or takes it out when it is on, with how many records carry it.
type chip struct {
	Tag string
	N   int
	URL string
	On  bool
}

// perChoice is one of the page sizes on offer.
type perChoice struct {
	N  int
	On bool
}

// recordTitle is a record's name as a person reads it: the file name
// without its extension, since the type shows beside it and Download
// keeps the whole name, or the start of the hash for a nameless file.
func recordTitle(b station.Blob) string {
	if b.Name == "" {
		return b.SHA256[:12] + "…"
	}
	if i := strings.LastIndex(b.Name, "."); i > 0 {
		return b.Name[:i]
	}
	return b.Name
}

// recordKind is the file's kind in a word, from the extension its media
// type takes, or the type itself when none is known.
func recordKind(b station.Blob) string {
	if ext := strings.TrimPrefix(blob.Extension(b.Type), "."); ext != "" {
		return ext
	}
	return b.Type
}

// recordingMeta words a recording's steps and length.
func recordingMeta(b station.Blob) string {
	if b.Steps == 0 && b.Frames == 0 {
		return ""
	}
	parts := []string{}
	if b.Steps > 0 {
		parts = append(parts, fmt.Sprintf("%d steps", b.Steps))
	}
	if b.Duration > 0 {
		parts = append(parts, b.Duration.Round(time.Second).String())
	}
	return strings.Join(parts, " · ")
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
	q := parseRecordsQuery(r)
	bs, err := s.visibleRecords(r, p, q)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	if devs, err := s.devices(r.Context()); err == nil {
		for _, d := range devs {
			labels[d.ID] = d.Title
		}
	}
	// The tags in use among what matched, most used first, as chips
	// that narrow the search further or let a tag go.
	counts := map[string]int{}
	for _, b := range bs {
		for _, t := range b.Tags {
			counts[t]++
		}
	}
	var chips []chip
	for t, n := range counts {
		chips = append(chips, chip{Tag: t, N: n, URL: q.Link("q", q.withTag(t)), On: slices.Contains(q.Tags, t)})
	}
	slices.SortFunc(chips, func(a, b chip) int {
		if a.On != b.On {
			if a.On {
				return -1
			}
			return 1
		}
		if a.N != b.N {
			return b.N - a.N
		}
		return strings.Compare(a.Tag, b.Tag)
	})
	if len(chips) > 16 {
		chips = chips[:16]
	}
	found := len(bs)
	pageNo, pages, from, to := paging(found, q.Per, q.Page)
	q.Page = pageNo
	views := make([]recordView, 0, to-from)
	for _, b := range bs[from:to] {
		v := recordView{Blob: b, Title: recordTitle(b), Kind: recordKind(b), URL: s.records.URL(b), Play: playURL(b), When: b.CreatedAt.Local().Format("Jan 2 15:04"), Size: humanSize(b.Size),
			Device: b.DeviceID, Label: labels[b.DeviceID], CanEdit: p.Admin || p.Subject == b.Uploader, CanPlay: viewer.HasPlayer() && (b.Type == "application/zip"),
			HasThumb: b.Type == "application/zip" || strings.HasPrefix(b.Type, "image/") || records.IsWAV(b.Type), Sound: records.IsSound(b.Type), Meta: recordingMeta(b)}
		switch {
		case v.CanPlay:
			v.Open, v.OpenWord = "/records/"+b.SHA256+"/play", "Play"
		case records.OpensInPlace(b.Type):
			v.Open, v.OpenWord = v.URL, "Open"
		default:
			v.Open, v.OpenWord = v.URL+"?download", "Download"
		}
		for _, t := range b.Tags {
			v.TagLinks = append(v.TagLinks, chip{Tag: t, URL: q.Link("q", q.withTag(t)), On: slices.Contains(q.Tags, t)})
		}
		for i := range records.StripCount(b) {
			v.Strip = append(v.Strip, i)
		}
		views = append(views, v)
	}
	var pers []perChoice
	for _, n := range perPage {
		pers = append(pers, perChoice{N: n, On: n == q.Per})
	}
	golden := "1"
	if q.Golden {
		golden = ""
	}
	count, size, _ := s.db.BlobTotal(r.Context())
	data := map[string]any{
		"Page": "records", "Principal": p, "CSRF": s.auth.csrfToken(r), "Records": views, "Device": q.Device, "Grid": q.View == "" || q.View == "grid", "List": q.View == "list", "Strip": q.View == "strip", "Hold": q.Hold,
		"DeviceLabel": labels[q.Device], "Count": count, "Total": humanSize(size),
		"MaxFile": humanSize(s.records.MaxFile), "MaxTotal": humanSize(s.records.MaxTotal), "MaxAge": humanAge(s.records.MaxAge),
		"Q": q, "Query": q.Raw, "GoldenOnly": q.Golden, "Filtered": q.Raw != "" || q.Golden,
		"GridURL": q.Link("view", "grid"), "ListURL": q.Link("view", "list"), "StripURL": q.Link("view", "strip"), "GoldenURL": q.Link("golden", golden), "ClearURL": q.Link("q", "", "golden", ""), "AllURL": q.Link("device", "", "hold", ""),
		"Keep": q.back(true), "Back": q.back(false), "Chips": chips, "Pers": pers,
		"Found": found, "From": from + 1, "To": to, "PageNo": pageNo, "Pages": pages,
	}
	if pageNo > 1 {
		data["PrevURL"] = q.Link("page", strconv.Itoa(pageNo-1))
	}
	if pageNo < pages {
		data["NextURL"] = q.Link("page", strconv.Itoa(pageNo+1))
	}
	for k, v := range extra {
		data[k] = v
	}
	return data, nil
}

// playPage is GET /records/{sha}/play: the recording in the embedded
// Record Player, framed under the station's own top bar. The player
// fetches the file from this station with the browser's own session.
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
	var tagLinks []chip
	for _, t := range b.Tags {
		tagLinks = append(tagLinks, chip{Tag: t, URL: "/records?q=" + url.QueryEscape("#"+t)})
	}
	data := map[string]any{"Page": "records", "Principal": p, "CSRF": s.auth.csrfToken(r), "Title": recordTitle(b),
		"Player": playURL(b), "Full": "/records/" + sha + "/play?full", "Download": s.records.URL(b) + "?download", "Device": b.DeviceID, "Hold": b.HoldID, "Uploader": b.Uploader,
		"When": b.CreatedAt.Local().Format("Jan 2, 2006 15:04"), "Size": humanSize(b.Size),
		"SHA": sha, "Golden": b.Golden, "Tags": strings.Join(b.Tags, " "), "TagLinks": tagLinks, "CanEdit": p.Admin || p.Subject == b.Uploader,
		"Return": "/records/" + sha + "/play", "Message": r.URL.Query().Get("msg")}
	// ?full is the same frame filling the window, without the bar.
	if r.URL.Query().Has("full") {
		s.render(w, "playfull", data)
		return
	}
	s.render(w, "play", data)
}

// playURL opens a blob in the embedded player, framed by a station page:
// chrome=hidden drops the player's own logo, title and theme switch and
// starts its timeline at about a quarter of the frame, and the player
// takes the station's colors and theme from the framing page by itself.
func playURL(b station.Blob) string {
	return viewer.PlayerPath + "?record=" + url.QueryEscape(b.SHA256+blob.Extension(b.Type)) + "&chrome=hidden"
}

func (s *server) recordsPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	// A choice made here is remembered for the next plain visit.
	if v := r.FormValue("view"); recordsView(v) {
		s.remember(w, viewCookie, v)
	}
	if n := r.FormValue("n"); n != "" && parseRecordsQuery(r).Per == atoi(n) {
		s.remember(w, perCookie, n)
	}
	data, err := s.recordsData(r, p, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "records", data)
}

// remember keeps a Records page choice in a cookie for a year.
func (s *server) remember(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/records", HttpOnly: true, Secure: s.auth.tls, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 3600})
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// backToRecords returns to the Records page the form came from, with
// its search, view and page, or to the record's own page when the form
// says so.
func backToRecords(w http.ResponseWriter, r *http.Request) {
	if ret := r.FormValue("return"); strings.HasPrefix(ret, "/records/") && !strings.Contains(ret, "//") {
		http.Redirect(w, r, ret, http.StatusSeeOther)
		return
	}
	q := url.Values{}
	for _, k := range []string{"device", "view", "hold", "q", "n", "page", "golden"} {
		if v := r.FormValue(k); v != "" {
			q.Set(k, v)
		}
	}
	to := "/records"
	if len(q) > 0 {
		to += "?" + q.Encode()
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// recordGolden is POST /records/golden: sha256 and make=golden|plain.
// The field is not called golden, since that name is the page's filter
// and the form returns to the page with its filters as they were.
func (s *server) recordGolden(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if err := s.records.SetGolden(r.Context(), p, r.FormValue("sha256"), r.FormValue("make") == "golden"); err != nil {
		recordsError(w, err)
		return
	}
	backToRecords(w, r)
}

// recordTags is POST /records/tags from a record's Play page: sha256
// and the tags as typed.
func (s *server) recordTags(w http.ResponseWriter, r *http.Request, p station.Principal) {
	sha := r.FormValue("sha256")
	to := "/records/" + url.PathEscape(sha) + "/play"
	if _, err := s.records.SetTags(r.Context(), p, sha, r.FormValue("tags")); err != nil {
		var refusal *records.Error
		if errors.As(err, &refusal) { // not theirs, or no such record: say so plainly
			recordsError(w, err)
			return
		}
		http.Redirect(w, r, to+"?msg="+urlQuery(err.Error()), http.StatusSeeOther)
		return
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
