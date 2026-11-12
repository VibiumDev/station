package records

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
	"github.com/VibiumDev/station/sqlite"
)

type fixture struct {
	t      *testing.T
	store  *Store
	db     *sqlite.DB
	mux    *http.ServeMux
	key    nostr.SecretKey // the station's
	rigKey nostr.SecretKey
	now    time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := &fixture{t: t, db: db, key: nostr.Generate(), rigKey: nostr.Generate(), now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	f.store = &Store{Blobs: blob.Disk{Dir: t.TempDir()}, Index: db, Events: db, Holds: db, Devices: db, Signer: Key(f.key),
		HoldsOf: db.HoldsOf, BaseURL: "https://st.example", MaxFile: 1 << 20, MaxTotal: 4 << 20, MaxAge: 7 * 24 * time.Hour,
		Audit: db, Now: func() time.Time { return f.now }}
	ctx := context.Background()
	if err := db.PutRig(ctx, station.Rig{ID: "rig_1", Name: "pi", PubKey: f.rigKey.Public(), EnrolledAt: f.now}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDevices(ctx, "rig_1", []station.Device{{ID: "rig_1/phone", Kind: "phone"}, {ID: "rig_1/tablet", Kind: "phone"}}); err != nil {
		t.Fatal(err)
	}
	f.hold("hld_1", "rig_1/phone", "token:ci", f.now.Add(-10*time.Minute), f.now.Add(time.Hour))
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("PUT /upload", f.store.ServeUpload)
	f.mux.HandleFunc("GET /list/{pubkey}", func(w http.ResponseWriter, r *http.Request) {
		f.store.ServeList(w, r, r.PathValue("pubkey"), principal(r))
	})
	f.mux.HandleFunc("/{sha}", func(w http.ResponseWriter, r *http.Request) {
		f.store.ServeBlob(w, r, r.PathValue("sha"), principal(r))
	})
	return f
}

// principal is the station's view of the caller, from a test header.
func principal(r *http.Request) *station.Principal {
	sub := r.Header.Get("X-Test-Subject")
	if sub == "" {
		return nil
	}
	return &station.Principal{Subject: sub, Admin: sub == "owner"}
}

func (f *fixture) hold(id, device, subject string, from, until time.Time) station.Hold {
	f.t.Helper()
	h, err := f.db.Create(context.Background(), station.Hold{ID: id, DeviceID: device, RigID: "rig_1", Subject: subject,
		Scopes: []station.Scope{station.ScopeView, station.ScopeBidi}, CreatedAt: from, ActiveAt: from, ExpiresAt: until})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (f *fixture) do(method, path string, body []byte, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, req)
	return w
}

// upload asks the station for an authorization as subject and sends the file.
func (f *fixture) upload(subject string, body []byte, name, hold string) (Descriptor, *httptest.ResponseRecorder) {
	f.t.Helper()
	ev, err := f.store.UploadAuth(context.Background(), station.Principal{Subject: subject, Admin: subject == "owner"},
		UploadRequest{SHA256: hashOf(body), Size: int64(len(body)), Type: "application/zip", Name: name, HoldID: hold})
	if err != nil {
		f.t.Fatalf("upload auth: %v", err)
	}
	w := f.do("PUT", "/upload", body, "Authorization", Header(ev), "Content-Type", "application/zip")
	var d Descriptor
	json.Unmarshal(w.Body.Bytes(), &d)
	return d, w
}

func (f *fixture) fileEvents(sha string) []nostr.Event {
	var out []nostr.Event
	for ev := range f.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{KindFile}, Tags: nostr.TagMap{"x": []string{sha}}}, 10) {
		out = append(out, ev)
	}
	return out
}

func TestUploadAndRead(t *testing.T) {
	f := setup(t)
	body := []byte("PK a recording")
	d, w := f.upload("token:ci", body, "record.zip", "")
	if w.Code != 200 || d.Hold != "hld_1" || d.Device != "rig_1/phone" || d.Uploader != "token:ci" || d.URL != "https://st.example/"+hashOf(body)+".zip" {
		t.Fatalf("upload: %d %s %+v", w.Code, w.Body, d)
	}
	evs := f.fileEvents(d.SHA256)
	if len(evs) != 1 || evs[0].Tags.Find("hold")[1] != "hld_1" || evs[0].Tags.Find("sub")[1] != "token:ci" || evs[0].Tags.Find("p")[1] != f.rigKey.Public().Hex() {
		t.Fatalf("file event: %v", evs)
	}
	if d.Event != evs[0].ID.Hex() {
		t.Fatal("descriptor names another event")
	}
	path := "/" + d.SHA256 + ".zip"
	if w := f.do("GET", path, nil, "X-Test-Subject", "token:ci"); w.Code != 200 || w.Body.String() != string(body) || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("get as uploader: %d %q %s", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
	if w := f.do("HEAD", path, nil, "X-Test-Subject", "owner"); w.Code != 200 {
		t.Fatalf("head as owner: %d", w.Code)
	}
	if w := f.do("GET", path, nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Fatalf("get as another token: %d", w.Code)
	}
	if w := f.do("GET", path, nil); w.Code != 401 {
		t.Fatalf("get with nothing: %d", w.Code)
	}
	if w := f.do("GET", path+"?download", nil, "X-Test-Subject", "owner"); !strings.Contains(w.Header().Get("Content-Disposition"), `filename="record.zip"`) {
		t.Fatalf("download disposition: %q", w.Header().Get("Content-Disposition"))
	}

	// A t=get authorization the station signed lets the file out with
	// no station credential; one for another hash does not.
	get, err := f.store.DownloadAuth(context.Background(), station.Principal{Subject: "token:ci"}, d.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.do("GET", path, nil, "Authorization", Header(get)); w.Code != 200 {
		t.Fatalf("get with a download auth: %d %s", w.Code, w.Body)
	}
	other := []byte("other")
	f.upload("owner", other, "other.zip", "hld_1")
	if w := f.do("GET", "/"+hashOf(other), nil, "Authorization", Header(get)); w.Code != 403 {
		t.Fatalf("a download auth for one blob opened another: %d", w.Code)
	}
	if _, err := f.store.DownloadAuth(context.Background(), station.Principal{Subject: "token:other"}, d.SHA256); err == nil {
		t.Fatal("a stranger got a download auth")
	}

	// Public: anyone. Then deleted by the owner: gone, event kept.
	if err := f.store.SetPublic(context.Background(), station.Principal{Subject: "token:other"}, d.SHA256, true); err == nil {
		t.Fatal("a stranger made it public")
	}
	if err := f.store.SetPublic(context.Background(), station.Principal{Subject: "token:ci"}, d.SHA256, true); err != nil {
		t.Fatal(err)
	}
	if w := f.do("GET", path, nil); w.Code != 200 {
		t.Fatalf("public get: %d", w.Code)
	}
	if w := f.do("DELETE", path, nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Fatalf("delete by a stranger: %d", w.Code)
	}
	if w := f.do("DELETE", path, nil, "X-Test-Subject", "owner"); w.Code != 200 {
		t.Fatalf("delete by the owner: %d %s", w.Code, w.Body)
	}
	if w := f.do("GET", path, nil, "X-Test-Subject", "owner"); w.Code != 404 {
		t.Fatalf("after delete: %d", w.Code)
	}
	if len(f.fileEvents(d.SHA256)) != 1 {
		t.Fatal("the file event should outlive the bytes")
	}
	if f.store.Blobs.Exists(d.SHA256) {
		t.Fatal("the bytes should be gone")
	}
}

func TestUploadRefusals(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	body := []byte("PK a recording")
	p := station.Principal{Subject: "token:ci"}
	good, _ := f.store.UploadAuth(ctx, p, UploadRequest{SHA256: hashOf(body), Size: int64(len(body)), Name: "r.zip"})

	if w := f.do("PUT", "/upload", body); w.Code != 401 {
		t.Fatalf("no auth: %d", w.Code)
	}
	if w := f.do("PUT", "/upload", []byte("PK not those bytes"), "Authorization", Header(good)); w.Code != 400 {
		t.Fatalf("wrong bytes: %d %s", w.Code, w.Body)
	}
	if f.store.Blobs.Exists(hashOf(body)) {
		t.Fatal("a refused upload left a file")
	}
	if w := f.do("PUT", "/upload", body[:5], "Authorization", Header(good)); w.Code != 400 {
		t.Fatalf("short body: %d", w.Code)
	}
	stale := good
	stale.Tags = nostr.Tags{{"t", "upload"}, {"x", hashOf(body)}, {"expiration", strconv.FormatInt(f.now.Add(-time.Minute).Unix(), 10)}}
	stale.Sign(f.key)
	if w := f.do("PUT", "/upload", body, "Authorization", Header(stale)); w.Code != 401 {
		t.Fatalf("expired auth: %d %s", w.Code, w.Body)
	}
	stranger := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"t", "upload"}, {"x", hashOf(body)},
		{"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}}}
	stranger.Sign(nostr.Generate())
	if w := f.do("PUT", "/upload", body, "Authorization", Header(stranger)); w.Code != 403 {
		t.Fatalf("a key the station does not know: %d %s", w.Code, w.Body)
	}
	get, _ := f.store.DownloadAuth(ctx, station.Principal{Subject: "owner", Admin: true}, hashOf(body))
	_ = get
	if w := f.do("PUT", "/upload", body, "Authorization", "Nostr not-base64!"); w.Code != 400 {
		t.Fatalf("junk auth: %d", w.Code)
	}
	f.store.MaxFile = 4
	if _, err := f.store.UploadAuth(ctx, p, UploadRequest{SHA256: hashOf(body), Size: int64(len(body))}); err == nil {
		t.Fatal("too big should be refused at the auth")
	}
	f.store.MaxFile = 0
	if w := f.do("PUT", "/upload", body, "Authorization", Header(good)); w.Code != 200 {
		t.Fatalf("the good upload: %d %s", w.Code, w.Body)
	}
	// The same bytes again are no error, and no second event.
	if w := f.do("PUT", "/upload", body, "Authorization", Header(good)); w.Code != 200 {
		t.Fatalf("again: %d %s", w.Code, w.Body)
	}
	if len(f.fileEvents(hashOf(body))) != 1 {
		t.Fatal("a second upload of the same bytes made a second event")
	}
	// Read-only or wrong-sha authorizations are not uploads.
	if _, err := f.store.UploadAuth(ctx, p, UploadRequest{SHA256: "nope", Size: 3}); err == nil {
		t.Fatal("a bad hash should be refused")
	}
}

func TestRigUploads(t *testing.T) {
	f := setup(t)
	body := []byte("a screenshot")
	sign := func(dev, hold string, key nostr.SecretKey) nostr.Event {
		ev := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"t", "upload"}, {"x", hashOf(body)},
			{"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}, {"name", "shot.jpg"}}}
		if dev != "" {
			ev.Tags = append(ev.Tags, nostr.Tag{"dev", dev})
		}
		if hold != "" {
			ev.Tags = append(ev.Tags, nostr.Tag{"hold", hold})
		}
		ev.Sign(key)
		return ev
	}
	if w := f.do("PUT", "/upload", body, "Authorization", Header(sign("rig_2/phone", "", f.rigKey)), "Content-Type", "image/jpeg"); w.Code != 403 {
		t.Fatalf("another rig's device: %d %s", w.Code, w.Body)
	}
	f.hold("hld_t", "rig_1/tablet", "owner", f.now, f.now.Add(time.Hour))
	if w := f.do("PUT", "/upload", body, "Authorization", Header(sign("rig_1/phone", "hld_t", f.rigKey)), "Content-Type", "image/jpeg"); w.Code != 403 {
		t.Fatalf("a hold on another device: %d %s", w.Code, w.Body)
	}
	w := f.do("PUT", "/upload", body, "Authorization", Header(sign("rig_1/phone", "hld_1", f.rigKey)), "Content-Type", "image/jpeg")
	var d Descriptor
	json.Unmarshal(w.Body.Bytes(), &d)
	if w.Code != 200 || d.Uploader != "rig:rig_1" || d.Hold != "hld_1" || d.Type != "image/jpeg" || !strings.HasSuffix(d.URL, ".jpg") {
		t.Fatalf("rig upload: %d %s", w.Code, w.Body)
	}
	// The hold's subject reads it, a stranger does not, the rig's own
	// t=get does.
	if w := f.do("GET", "/"+d.SHA256, nil, "X-Test-Subject", "token:ci"); w.Code != 200 {
		t.Fatalf("the hold's subject: %d", w.Code)
	}
	if w := f.do("GET", "/"+d.SHA256, nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Fatalf("a stranger: %d", w.Code)
	}
	get := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"t", "get"}, {"x", d.SHA256},
		{"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}}}
	get.Sign(f.rigKey)
	if w := f.do("GET", "/"+d.SHA256, nil, "Authorization", Header(get)); w.Code != 200 {
		t.Fatalf("the rig's get: %d %s", w.Code, w.Body)
	}
	// The rig's list, by a t=list it signs; a stranger's listing is refused.
	list := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"t", "list"},
		{"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}}}
	list.Sign(f.rigKey)
	w = f.do("GET", "/list/"+f.rigKey.Public().Hex(), nil, "Authorization", Header(list))
	var ds []Descriptor
	json.Unmarshal(w.Body.Bytes(), &ds)
	if w.Code != 200 || len(ds) != 1 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := f.do("GET", "/list/"+f.rigKey.Public().Hex(), nil); w.Code != 401 {
		t.Fatalf("list by nobody: %d", w.Code)
	}
	if w := f.do("GET", "/list/"+f.rigKey.Public().Hex(), nil, "X-Test-Subject", "owner"); w.Code != 200 {
		t.Fatalf("list by the owner: %d", w.Code)
	}
}

// Removing a rig refuses its key from then on. Its signature no longer
// reads, lists or deletes what it uploaded while it was enrolled, and the
// records stay for the people who could read them.
func TestRemovedRigKeyIsRefused(t *testing.T) {
	f := setup(t)
	body := []byte("a recording the rig made")
	signed := func(verb string) string {
		ev := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"t", verb}, {"x", hashOf(body)},
			{"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}, {"dev", "rig_1/phone"}, {"hold", "hld_1"}}}
		ev.Sign(f.rigKey)
		return Header(ev)
	}
	list := "/list/" + f.rigKey.Public().Hex()
	if w := f.do("PUT", "/upload", body, "Authorization", signed("upload"), "Content-Type", "text/plain"); w.Code != 200 {
		t.Fatalf("the rig's upload: %d %s", w.Code, w.Body)
	}
	if get, ls := f.do("GET", "/"+hashOf(body), nil, "Authorization", signed("get")), f.do("GET", list, nil, "Authorization", signed("list")); get.Code != 200 || ls.Code != 200 {
		t.Fatalf("while enrolled: get %d, list %d", get.Code, ls.Code)
	}
	if err := f.db.RemoveRig(context.Background(), "rig_1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path, verb string
		want               int
	}{
		{"GET", "/" + hashOf(body), "get", 403},
		{"GET", list, "list", 401},
		{"DELETE", "/" + hashOf(body), "delete", 403},
		{"PUT", "/upload", "upload", 403},
	} {
		var sent []byte
		if c.method == "PUT" {
			sent = body
		}
		if w := f.do(c.method, c.path, sent, "Authorization", signed(c.verb)); w.Code != c.want {
			t.Errorf("the removed rig's %s: %d, want %d", c.verb, w.Code, c.want)
		}
	}
	if w := f.do("GET", "/"+hashOf(body), nil, "X-Test-Subject", "token:ci"); w.Code != 200 {
		t.Errorf("the hold's subject reading the record after the rig went: %d", w.Code)
	}
}

func TestHoldResolution(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	body := []byte("x")
	req := UploadRequest{SHA256: hashOf(body), Size: 1}
	ci := station.Principal{Subject: "token:ci"}
	// hld_1 is token:ci's current hold.
	ev, err := f.store.UploadAuth(ctx, ci, req)
	if err != nil || tagValue(&ev, "hold") != "hld_1" || tagValue(&ev, "dev") != "rig_1/phone" {
		t.Fatalf("current hold: %v %v", ev.Tags, err)
	}
	// Someone else's hold by name: refused. The owner may name anyone's.
	req.HoldID = "hld_1"
	if _, err := f.store.UploadAuth(ctx, station.Principal{Subject: "token:other"}, req); err == nil {
		t.Fatal("another token used my hold")
	}
	if _, err := f.store.UploadAuth(ctx, station.Principal{Subject: "owner", Admin: true}, req); err != nil {
		t.Fatalf("the owner naming a hold: %v", err)
	}
	req.HoldID = "hld_nope"
	if _, err := f.store.UploadAuth(ctx, ci, req); err == nil {
		t.Fatal("a hold that does not exist")
	}
	req.HoldID = ""
	// Two current holds: ambiguous.
	f.hold("hld_2", "rig_1/tablet", "token:ci", f.now, f.now.Add(time.Hour))
	if _, err := f.store.UploadAuth(ctx, ci, req); err == nil {
		t.Fatal("two held devices should need a name")
	}
	// None current, one ended an hour ago: that one.
	f.now = f.now.Add(2 * time.Hour)
	ev, err = f.store.UploadAuth(ctx, ci, req)
	if err != nil || tagValue(&ev, "hold") != "hld_2" {
		t.Fatalf("the newest ended hold: %v %v", ev.Tags, err)
	}
	// Nothing in a day: refused.
	f.now = f.now.Add(48 * time.Hour)
	if _, err := f.store.UploadAuth(ctx, ci, req); err == nil {
		t.Fatal("no hold in a day should be refused")
	}
	if _, err := f.store.UploadAuth(ctx, station.Principal{Subject: "token:new"}, req); err == nil {
		t.Fatal("a token with no holds")
	}
	ev, err = f.store.UploadAuth(ctx, station.Principal{Subject: "owner", Admin: true}, req)
	if err != nil || tagValue(&ev, "hold") != "" {
		t.Fatalf("the owner with no hold keeps the file on its own: %v %v", ev.Tags, err)
	}
}

func TestSweep(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.store.MaxTotal = 0
	old := []byte("old recording")
	f.now = f.now.Add(-10 * 24 * time.Hour)
	d1, w := f.upload("token:ci", old, "old.zip", "hld_1")
	if w.Code != 200 {
		t.Fatalf("old upload: %d %s", w.Code, w.Body)
	}
	f.now = f.now.Add(10 * 24 * time.Hour)
	d2, _ := f.upload("token:ci", []byte("new recording"), "new.zip", "hld_1")
	gone, err := f.store.Sweep(ctx)
	if err != nil || len(gone) != 1 || gone[0].SHA256 != d1.SHA256 {
		t.Fatalf("age sweep: %v %v", gone, err)
	}
	if len(f.fileEvents(d1.SHA256)) != 1 || f.store.Blobs.Exists(d1.SHA256) {
		t.Fatal("age sweep should keep the event and drop the bytes")
	}
	// By size: the oldest goes first until the total fits.
	d3, _ := f.upload("token:ci", []byte("third recording, a little longer"), "third.zip", "hld_1")
	f.store.MaxTotal = int64(len("third recording, a little longer")) + 2
	gone, err = f.store.Sweep(ctx)
	if err != nil || len(gone) != 1 || gone[0].SHA256 != d2.SHA256 {
		t.Fatalf("size sweep: %v %v", gone, err)
	}
	if _, err := f.store.Index.Blob(ctx, d3.SHA256); err != nil {
		t.Fatal("the newest should stay")
	}
	n, size, _ := f.store.Index.BlobTotal(ctx)
	if n != 1 || size > f.store.MaxTotal {
		t.Fatalf("after the sweep: %d blobs, %d bytes", n, size)
	}
}

func TestReadAuthEdges(t *testing.T) {
	f := setup(t)
	r := httptest.NewRequest("GET", "/x", nil)
	if ev, err := f.store.ReadAuth(r); ev != nil || err != nil {
		t.Fatal("no header should be nil, nil")
	}
	r.Header.Set("Authorization", "Bearer stk_notnostr")
	if ev, err := f.store.ReadAuth(r); ev != nil || err != nil {
		t.Fatal("a bearer header is not a Blossom auth")
	}
	ev := nostr.Event{Kind: 1, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}}}
	ev.Sign(f.key)
	r.Header.Set("Authorization", Header(ev))
	if _, err := f.store.ReadAuth(r); err == nil {
		t.Fatal("a kind 1 event is not an authorization")
	}
	good := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(f.now.Unix()), Tags: nostr.Tags{{"t", "get"}, {"expiration", strconv.FormatInt(f.now.Add(time.Minute).Unix(), 10)}}}
	good.Sign(f.key)
	good.Content = "tampered"
	r.Header.Set("Authorization", Header(good))
	if _, err := f.store.ReadAuth(r); err == nil {
		t.Fatal("a tampered event should fail its id check")
	}
	_ = io.EOF
}

func TestParseTags(t *testing.T) {
	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{"", "", false},
		{"#Flaky, release-1.2 flaky", "flaky release-1.2", false},
		{"checkout\tVAR.parts\nhome_screen", "checkout var.parts home_screen", false},
		{"has space?", "", true},
		{"-leading", "", true},
		{"#", "", false},
		{strings.Repeat("a", 41), "", true},
	}
	for _, c := range cases {
		got, err := ParseTags(c.in)
		if (err != nil) != c.bad || strings.Join(got, " ") != c.want {
			t.Errorf("%q: got %v, %v", c.in, got, err)
		}
	}
	var many []string
	for i := 0; i <= MaxTags; i++ {
		many = append(many, "t"+strings.Repeat("x", i))
	}
	if _, err := ParseTags(strings.Join(many, " ")); err == nil {
		t.Error("twenty-one tags passed")
	}
}

// Golden and tags: the uploader and the owner may file a record, a
// stranger may not, and the descriptor carries both.
func TestGoldenAndTags(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	d, w := f.upload("token:ci", []byte("PK a recording"), "record.zip", "")
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if err := f.store.SetGolden(ctx, station.Principal{Subject: "token:other"}, d.SHA256, true); err == nil {
		t.Fatal("a stranger made it golden")
	}
	if _, err := f.store.SetTags(ctx, station.Principal{Subject: "token:other"}, d.SHA256, "flaky"); err == nil {
		t.Fatal("a stranger tagged it")
	}
	if err := f.store.SetGolden(ctx, station.Principal{Subject: "token:ci"}, d.SHA256, true); err != nil {
		t.Fatal(err)
	}
	tags, err := f.store.SetTags(ctx, station.Principal{Subject: "owner", Admin: true}, d.SHA256, "#Flaky release-1.2")
	if err != nil || strings.Join(tags, " ") != "flaky release-1.2" {
		t.Fatalf("tags: %v %v", tags, err)
	}
	if _, err := f.store.SetTags(ctx, station.Principal{Subject: "owner", Admin: true}, d.SHA256, "bad?tag"); err == nil {
		t.Fatal("a bad tag passed")
	}
	b, _ := f.store.Index.Blob(ctx, d.SHA256)
	desc := f.store.Describe(b)
	if !desc.Golden || strings.Join(desc.Tags, " ") != "flaky release-1.2" {
		t.Errorf("descriptor: %+v", desc)
	}
	if err := f.store.SetGolden(ctx, station.Principal{Subject: "owner", Admin: true}, "0000000000000000000000000000000000000000000000000000000000000000", true); err == nil {
		t.Fatal("golden on nothing")
	}
}

// A store with no page policy around it sandboxes what it serves all the
// same, and sends a file a browser would render as a download.
func TestServedFilesAreSandboxed(t *testing.T) {
	f := setup(t)
	body := []byte(`<!doctype html><script src="/x"></script>`)
	ev, err := f.store.UploadAuth(context.Background(), station.Principal{Subject: "token:ci"},
		UploadRequest{SHA256: hashOf(body), Size: int64(len(body)), Type: "text/html", Name: "p.html", HoldID: "hld_1"})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.do("PUT", "/upload", body, "Authorization", Header(ev), "Content-Type", "text/html"); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	h := f.do("GET", "/"+hashOf(body), nil, "X-Test-Subject", "token:ci").Header()
	if h.Get("Content-Type") != "text/html" || h.Get("Content-Security-Policy") != "sandbox" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != `attachment; filename="p.html"` {
		t.Fatalf("a stored page: %v", h)
	}
}
