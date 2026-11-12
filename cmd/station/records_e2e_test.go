package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/records"
)

// uploadAs stores a file as a principal would through the real routes,
// and returns its hash.
func (f *fullStation) uploadAs(p station.Principal, body []byte, name, hold string) string {
	f.t.Helper()
	return f.uploadTyped(p, body, "application/zip", name, hold)
}

// uploadTyped is uploadAs under the media type the uploader names.
func (f *fullStation) uploadTyped(p station.Principal, body []byte, mediaType, name, hold string) string {
	f.t.Helper()
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	ev, err := f.s.records.UploadAuth(f.ctx, p, records.UploadRequest{SHA256: sha, Size: int64(len(body)), Type: mediaType, Name: name, HoldID: hold})
	if err != nil {
		f.t.Fatalf("upload auth for %s: %v", name, err)
	}
	r := httptest.NewRequest("PUT", "/upload", bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Authorization", records.Header(ev))
	r.Header.Set("Content-Type", mediaType)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 200 {
		f.t.Fatalf("upload %s: %d %s", name, w.Code, w.Body)
	}
	return sha
}

// Phase 2: who reads which record, through every route that hands a
// record out, and who may change one.
func TestRecordsPublicAndPrivate(t *testing.T) {
	f := newFullStation(t)
	callers := f.callers()
	now := time.Now()
	// Four records: the admin's private one on A (the harness's), the
	// admin's on B, one with no device from an admin who held nothing,
	// and a public one on A.
	if _, err := f.db.Create(f.ctx, station.Hold{ID: "hld_b", DeviceID: devB, RigID: "rig_b", Subject: "owner", Scopes: []station.Scope{station.ScopeView, station.ScopeInput},
		CreatedAt: now.Add(-time.Hour), ActiveAt: now.Add(-time.Hour), ExpiresAt: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	root, _ := f.db.UserByLogin(f.ctx, "root@example.com")
	privA := f.sha
	privB := f.uploadAs(f.admin, []byte("PK on device B"), "b.zip", "hld_b")
	noDev := f.uploadAs(station.Principal{Subject: "root@example.com", Admin: true, User: root.ID}, []byte("PK from nowhere"), "nowhere.zip", "")
	public := f.uploadAs(f.admin, []byte("PK for everyone"), "public.zip", "hld_a")
	if err := f.db.SetBlobPublic(f.ctx, public, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := f.db.Blob(f.ctx, noDev); b.DeviceID != "" {
		t.Fatalf("the device-less record has device %q", b.DeviceID)
	}
	adminUnbound := func(c caller) bool { return c.signed && c.admin && c.bound == "" }
	blobs := []struct {
		name, sha string
		may       rule
	}{
		{"private on A", privA, ruleReadA},
		{"private on B", privB, adminUnbound},
		{"no device", noDev, adminUnbound},
		{"public", public, rulePublic},
	}
	for _, b := range blobs {
		for _, c := range callers {
			want := b.may(c)
			// Blossom GET and HEAD, the Play page, a file inside, and a download authorization.
			for _, probe := range []struct{ method, path string }{
				{"GET", "/" + b.sha}, {"HEAD", "/" + b.sha},
				{"GET", "/records/" + b.sha + "/play"},
				{"GET", "/records/" + b.sha + "/files/x.txt"},
				{"POST", "/v1/records/" + b.sha + "/download-auth"},
			} {
				w := f.do(c, probe.method, probe.path, "", true)
				got := !denied(w)
				if probe.path != "/"+b.sha && !c.signed && b.sha == public {
					continue // the pages and the API want a sign-in even for a public record; the link is the Blossom route
				}
				if got != want {
					t.Errorf("%s: %s %s as %s: %d (want allowed=%v)", b.name, probe.method, probe.path, c.name, w.Code, want)
				}
			}
		}
	}
	// The list shows each caller what they may read, and the public
	// record to everyone signed in.
	for _, c := range callers {
		if !c.signed {
			continue
		}
		w := f.do(c, "GET", "/v1/records", "", true)
		if w.Code != 200 {
			t.Errorf("list as %s: %d", c.name, w.Code)
			continue
		}
		var list []struct {
			SHA256 string `json:"sha256"`
		}
		json.Unmarshal(w.Body.Bytes(), &list)
		have := map[string]bool{}
		for _, d := range list {
			have[d.SHA256] = true
		}
		for _, b := range blobs {
			if have[b.sha] != b.may(c) {
				t.Errorf("list as %s: %s listed=%v, readable=%v", c.name, b.name, have[b.sha], b.may(c))
			}
		}
	}
	// Private again, and the link is dark.
	admin := callers[8]
	if admin.name != "admin" {
		t.Fatalf("caller 8 is %s", admin.name)
	}
	// The list has no Access column. Share sits with the other actions,
	// held like Delete: lit on a shared record, where it makes it private.
	list := f.do(admin, "GET", "/records?view=list&n=100", "", true).Body.String()
	if strings.Contains(list, "<th>Access</th>") || strings.Contains(list, "make public") {
		t.Error("the list still has an Access column")
	}
	if !strings.Contains(list, `name="sha256" value="`+public+`"><input type="hidden" name="public" value="0"><button class="small icon hold share on" type="button" data-hold`) {
		t.Error("a shared record has no lit Share button that makes it private")
	}
	if !strings.Contains(list, `name="public" value="1"><button class="small icon hold share" type="button" data-hold title="Hold to share`) {
		t.Error("a private record has no held Share button")
	}
	if !strings.Contains(list, " · shared</span>") {
		t.Error("a shared record is not marked in the list")
	}
	// Filmstrip is the third view: a row a record, the same actions under
	// it, and the choice remembered like the others.
	w := f.do(admin, "GET", "/records?view=strip&n=100", "", true)
	strips := w.Body.String()
	if !strings.Contains(strips, `<a href="/records?n=100&amp;view=strip" aria-current="page">Filmstrip</a>`) || !strings.Contains(strips, `<article class="striprow">`) || strings.Contains(strips, `<table class="records">`) {
		t.Error("view=strip is not the filmstrip view")
	}
	if !strings.Contains(strips, `<span class="act"><form class="inline" method="post" action="/records/golden">`) || !strings.Contains(strips, `class="small icon hold share on" type="button" data-hold`) {
		t.Error("a filmstrip row lacks the list's actions")
	}
	if c := w.Header().Get("Set-Cookie"); !strings.Contains(c, "station-records-view=strip") {
		t.Errorf("the view is not remembered: %q", c)
	}
	if strings.Contains(list, "striprow") || !strings.Contains(list, `aria-current="page">List</a>`) {
		t.Error("the list view changed")
	}
	if w := f.do(admin, "POST", "/records/public", "sha256="+public+"&public=0", true); w.Code != http.StatusSeeOther {
		t.Fatalf("making private: %d %s", w.Code, w.Body)
	}
	if w := f.do(callers[0], "GET", "/"+public, "", true); w.Code != http.StatusUnauthorized {
		t.Errorf("a private record by its link, as nobody: %d", w.Code)
	}
	// A regular user's own record: theirs to publish and delete; a
	// watcher of the device reads it and changes nothing; an admin's
	// drive token reads it and changes nothing; an admin in person may.
	if _, err := f.db.Create(f.ctx, station.Hold{ID: "hld_d", DeviceID: devA, RigID: "rig_a", Subject: "drive@example.com", Scopes: []station.Scope{station.ScopeView, station.ScopeInput},
		CreatedAt: now.Add(-time.Hour), ActiveAt: now.Add(-time.Hour), ExpiresAt: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	drive := principalOf(f.users["drive"], "")
	mine := f.uploadAs(drive, []byte("PK the driver's own"), "mine.zip", "hld_d")
	by := map[string]caller{}
	for _, c := range callers {
		by[c.name] = c
	}
	for _, c := range []struct {
		who    string
		read   bool
		change bool
	}{
		{"watch user", true, false}, {"drive user", true, true}, {"jam user", true, false}, {"no team", false, false},
		{"admin's drive token", true, false}, {"admin's watch token", true, false}, {"admin's admin token", true, true}, {"admin", true, true},
		{"drive user's drive token", true, true},  // the token acts as the driver, who uploaded it
		{"drive user's watch token", true, false}, // a watch token changes nothing, even its user's own
	} {
		w := f.do(by[c.who], "GET", "/"+mine, "", true)
		if (w.Code == 200) != c.read {
			t.Errorf("%s reads the driver's record: %d (want %v)", c.who, w.Code, c.read)
		}
		w = f.do(by[c.who], "POST", "/records/golden", "sha256="+mine+"&make=golden", true)
		if (w.Code == http.StatusSeeOther) != c.change {
			t.Errorf("%s changes the driver's record: %d (want %v)", c.who, w.Code, c.change)
		}
	}
	if w := f.do(by["drive user"], "POST", "/records/delete", "sha256="+mine, true); w.Code != http.StatusSeeOther {
		t.Errorf("the driver deleting their own: %d", w.Code)
	}
	if _, err := f.db.Blob(f.ctx, mine); err == nil {
		t.Error("the driver's record is still there")
	}
	_ = strconv.Itoa
}

// Stored files reach a browser from the station's own address, under the
// types and names their uploaders chose. None may act as a page of the
// station: every one is sandboxed, and one a browser would render as a
// document is a download. What the pages use stays as it was: the zip
// the player fetches, pictures, thumbnails and ?download.
func TestStoredFilesCannotActAsPages(t *testing.T) {
	f := newFullStation(t)
	ck, _ := f.session(f.users["admin"].ID, stageSignedIn)
	admin := caller{name: "admin", decor: withCookie(ck)}
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, bytes.Repeat([]byte{1}, 40)...)
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for _, e := range []struct {
		name string
		data []byte
	}{
		{"trace.trace", []byte(`{"type":"screencast-frame","sha1":"frame.jpeg","timestamp":1}` + "\n")},
		{"resources/frame.jpeg", jpeg},
		{"x.html", []byte(`<!doctype html><script src="x.js"></script>`)},
		{"x.js", []byte(`alert(document.domain)`)},
	} {
		w, _ := zw.Create(e.name)
		w.Write(e.data)
	}
	zw.Close()
	script := f.uploadTyped(f.admin, []byte(`alert(document.domain)`), "text/javascript", "a.js", "hld_a")
	page := f.uploadTyped(f.admin, []byte(`<!doctype html><script src="/`+script+`"></script>`), "text/html", "notes.html", "hld_a")
	drawing := f.uploadTyped(f.admin, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script href="/`+script+`"/></svg>`), "image/svg+xml", "shot.svg", "hld_a")
	picture := f.uploadTyped(f.admin, jpeg, "image/jpeg", "shot.jpg", "hld_a")
	recording := f.uploadTyped(f.admin, zbuf.Bytes(), "application/zip", "rec.zip", "hld_a")
	for _, c := range []struct {
		path, mediaType string
		download        bool
	}{
		{"/" + page, "text/html", true},
		{"/" + script, "text/javascript", true},
		{"/" + drawing, "image/svg+xml", true},
		{"/records/" + drawing + "/thumb.jpg", "image/svg+xml", true},
		{"/records/" + recording + "/files/x.html", "text/html", true},
		{"/records/" + recording + "/files/x.js", "javascript", true},
		{"/" + picture, "image/jpeg", false},
		{"/records/" + picture + "/thumb.jpg", "image/jpeg", false},
		{"/" + recording + ".zip", "application/zip", false},
		{"/records/" + recording + "/thumb.jpg", "image/jpeg", false},
		{"/records/" + recording + "/files/resources/frame.jpeg", "image/jpeg", false},
	} {
		w := f.do(admin, "GET", c.path, "", false)
		h := w.Header()
		if w.Code != 200 || !strings.Contains(h.Get("Content-Type"), c.mediaType) {
			t.Errorf("%s: %d %s", c.path, w.Code, h.Get("Content-Type"))
			continue
		}
		if csp := h.Get("Content-Security-Policy"); csp != pageCSP+"; sandbox" {
			t.Errorf("%s: not sandboxed beside the page policy: %q", c.path, csp)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", c.path)
		}
		if got := strings.HasPrefix(h.Get("Content-Disposition"), "attachment"); got != c.download {
			t.Errorf("%s: Content-Disposition %q (want a download: %v)", c.path, h.Get("Content-Disposition"), c.download)
		}
	}
	// The player fetches the zip whole, and ?download names any file.
	if w := f.do(admin, "GET", "/"+recording+".zip", "", false); !bytes.Equal(w.Body.Bytes(), zbuf.Bytes()) {
		t.Errorf("the recording came back changed: %d bytes of %d", w.Body.Len(), zbuf.Len())
	}
	if w := f.do(admin, "GET", "/"+picture+"?download", "", false); w.Header().Get("Content-Disposition") != `attachment; filename="shot.jpg"` {
		t.Errorf("?download: %q", w.Header().Get("Content-Disposition"))
	}
	if w := f.do(admin, "GET", "/"+page, "", false); w.Header().Get("Content-Disposition") != `attachment; filename="notes.html"` {
		t.Errorf("a page's download: %q", w.Header().Get("Content-Disposition"))
	}
}
