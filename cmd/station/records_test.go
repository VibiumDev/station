package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station"
)

// A records form returns to the page it came from: the same device,
// view and hold filter, not the grid of every device.
func TestBackToRecordsKeepsView(t *testing.T) {
	cases := []struct{ form, want string }{
		{"", "/records"},
		{"device=rig_a%2Fphone", "/records?device=rig_a%2Fphone"},
		{"device=rig_a%2Fphone&view=list", "/records?device=rig_a%2Fphone&view=list"},
		{"view=list&hold=h1", "/records?hold=h1&view=list"},
		{"view=list&q=%23flaky+checkout&n=50&page=3&golden=1", "/records?golden=1&n=50&page=3&q=%23flaky+checkout&view=list"},
		{"view=list&sha256=abc&make=golden", "/records?view=list"},
		{"return=%2Frecords%2Fabc%2Fplay&view=list", "/records/abc/play"},
		{"return=https%3A%2F%2Felsewhere.example%2F&view=list", "/records?view=list"},
		{"return=%2Frecords%2F%2Felsewhere.example", "/records"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/records/public", strings.NewReader(c.form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		backToRecords(w, r)
		if got := w.Header().Get("Location"); got != c.want {
			t.Errorf("form %q: redirected to %q, want %q", c.form, got, c.want)
		}
	}
}

// The page's query: words and #tags, a page size from the list, a page
// from one, and links that change one thing and start over at page one.
func TestParseRecordsQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/records?q=%23Flaky+checkout+%23release-1.2+%23bad%3F&n=50&page=2&golden=1&view=list&device=rig_a%2Fphone", nil)
	q := parseRecordsQuery(r)
	if strings.Join(q.Tags, " ") != "flaky release-1.2" || q.Text != "checkout #bad?" || q.Per != 50 || q.Page != 2 || !q.Golden || !q.List || q.Device != "rig_a/phone" {
		t.Errorf("query: %+v", q)
	}
	if got := q.Link("page", "3"); got != "/records?device=rig_a%2Fphone&golden=1&n=50&page=3&q=%23Flaky+checkout+%23release-1.2+%23bad%3F&view=list" {
		t.Errorf("page link: %s", got)
	}
	if got := q.Link("view", ""); strings.Contains(got, "view=") || strings.Contains(got, "page=") {
		t.Errorf("a view change should drop the view and the page: %s", got)
	}
	if got := q.withTag("flaky"); got != "checkout #release-1.2 #bad?" {
		t.Errorf("tag off: %q", got)
	}
	if got := q.withTag("new"); got != "#Flaky checkout #release-1.2 #bad? #new" {
		t.Errorf("tag on: %q", got)
	}
	keep := q.back(true)
	for _, f := range keep {
		if f.Name == "q" || f.Name == "n" || f.Name == "page" {
			t.Errorf("the search form keeps %s", f.Name)
		}
	}
	if len(q.back(false)) != 6 {
		t.Errorf("back fields: %+v", q.back(false))
	}
	plain := parseRecordsQuery(httptest.NewRequest(http.MethodGet, "/records?n=33&page=0", nil))
	if plain.Per != defaultPer || plain.Page != 1 || plain.Link() != "/records" {
		t.Errorf("defaults: %+v %s", plain, plain.Link())
	}
	// The browser's last view and page size come back from cookies when
	// the address says nothing, and the address wins when it does.
	back := httptest.NewRequest(http.MethodGet, "/records", nil)
	back.AddCookie(&http.Cookie{Name: viewCookie, Value: "list"})
	back.AddCookie(&http.Cookie{Name: perCookie, Value: "50"})
	if got := parseRecordsQuery(back); !got.List || got.View != "list" || got.Per != 50 || got.Link() != "/records?n=50&view=list" {
		t.Errorf("remembered: %+v %s", got, got.Link())
	}
	grid := httptest.NewRequest(http.MethodGet, "/records?view=grid", nil)
	grid.AddCookie(&http.Cookie{Name: viewCookie, Value: "list"})
	if got := parseRecordsQuery(grid); got.List || got.View != "grid" {
		t.Errorf("the address should win: %+v", got)
	}
}

func TestPaging(t *testing.T) {
	cases := []struct{ found, per, page, p, pages, from, to int }{
		{0, 25, 1, 1, 1, 0, 0},
		{25, 25, 1, 1, 1, 0, 25},
		{26, 25, 1, 1, 2, 0, 25},
		{26, 25, 2, 2, 2, 25, 26},
		{26, 25, 9, 2, 2, 25, 26},
		{7, 10, 0, 1, 1, 0, 7},
	}
	for _, c := range cases {
		p, pages, from, to := paging(c.found, c.per, c.page)
		if p != c.p || pages != c.pages || from != c.from || to != c.to {
			t.Errorf("paging(%d, %d, %d) = %d %d %d %d, want %d %d %d %d", c.found, c.per, c.page, p, pages, from, to, c.p, c.pages, c.from, c.to)
		}
	}
}

// The Records templates render with records in both views and on the
// Play page, golden and tagged, for an editor and a reader.
func TestRecordsTemplatesRender(t *testing.T) {
	q := parseRecordsQuery(httptest.NewRequest(http.MethodGet, "/records?q=%23flaky&view=list", nil))
	rec := func(golden, canEdit bool) recordView {
		b := station.Blob{SHA256: strings.Repeat("ab", 32), Size: 1234, Type: "application/zip", Name: "record.zip", Uploader: "token:ci", HoldID: "hld_1", DeviceID: "rig_a/phone", Golden: golden, Tags: []string{"flaky"}, CreatedAt: time.Now()}
		return recordView{Blob: b, Title: recordTitle(b), URL: "/" + b.SHA256 + ".zip", When: "Nov 8 10:47", Size: "1 KB", Device: b.DeviceID, Label: "phone", CanEdit: canEdit, CanPlay: true, HasThumb: true, Meta: "3 steps",
			TagLinks: []chip{{Tag: "flaky", URL: q.Link("q", ""), On: true}}}
	}
	base := map[string]any{"Page": "records", "Principal": station.Principal{Subject: "owner", Admin: true}, "CSRF": "c", "Name": "Station", "Named": true,
		"Device": "rig_a/phone", "DeviceLabel": "phone", "Hold": "", "Count": 2, "Total": "2 KB", "MaxFile": "256 MB", "MaxTotal": "2 GB", "MaxAge": "30 days",
		"Q": q, "Query": q.Raw, "GoldenOnly": false, "Filtered": true, "GridURL": q.Link("view", ""), "ListURL": q.Link("view", "list"), "GoldenURL": q.Link("golden", "1"),
		"ClearURL": q.Link("q", ""), "AllURL": q.Link("device", ""), "Keep": q.back(true), "Back": q.back(false),
		"Chips": []chip{{Tag: "flaky", N: 2, URL: "/records", On: true}}, "Pers": []perChoice{{10, false}, {25, true}}, "Found": 2, "From": 1, "To": 2, "PageNo": 1, "Pages": 2, "NextURL": q.Link("page", "2")}
	for _, grid := range []bool{true, false} {
		data := map[string]any{}
		for k, v := range base {
			data[k] = v
		}
		data["Grid"] = grid
		data["Records"] = []recordView{rec(true, true), rec(false, true), rec(false, false)}
		var out strings.Builder
		if err := pages.ExecuteTemplate(&out, "records", data); err != nil {
			t.Fatalf("records grid=%v: %v", grid, err)
		}
		html := out.String()
		for _, want := range []string{`name="make" value="golden"`, `name="make" value="plain"`, `class="tag on"`, `Page 1 of 2`, `Older ›`, `data-submit`, `#flaky<span class="n">2</span>`} {
			if !strings.Contains(html, want) {
				t.Errorf("records grid=%v lacks %q", grid, want)
			}
		}
		if !grid && !strings.Contains(html, `class="minithumb"`) {
			t.Error("the list has no tiny thumbnail")
		}
		if !grid && !strings.Contains(html, `class="small icon hold" type="button" data-hold title="Hold to delete"`) {
			t.Error("the list's Delete should be held, not clicked")
		}
		if !strings.Contains(html, `<b><a href="/records/`+strings.Repeat("ab", 32)+`/play">record</a></b>`) {
			t.Errorf("grid=%v: the record's name should open it", grid)
		}
		if strings.Contains(html, `?device=`) {
			t.Errorf("grid=%v: a device name should not be a filter link", grid)
		}
		if strings.Count(html, `aria-pressed="true"`) != 1 {
			t.Errorf("grid=%v: golden stars pressed: %d", grid, strings.Count(html, `aria-pressed="true"`))
		}
	}
	play := map[string]any{"Page": "records", "Principal": station.Principal{Subject: "owner", Admin: true}, "CSRF": "c", "Name": "Station", "Named": true, "Title": "record.zip",
		"Player": "/viewer/player/app/?record=x", "Full": "/records/x/play?full", "Download": "/x.zip?download", "Device": "rig_a/phone", "Hold": "hld_1", "Uploader": "token:ci",
		"When": "Nov 8, 2026 10:47", "Size": "1 KB", "SHA": "x", "Golden": true, "Tags": "flaky release-1.2", "TagLinks": []chip{{Tag: "flaky", URL: "/records?q=%23flaky"}}, "CanEdit": true, "Return": "/records/x/play", "Message": "a tag is letters"}
	var out strings.Builder
	if err := pages.ExecuteTemplate(&out, "play", play); err != nil {
		t.Fatalf("play: %v", err)
	}
	if html := out.String(); !strings.Contains(html, `value="flaky release-1.2"`) || !strings.Contains(html, `name="return" value="/records/x/play"`) || !strings.Contains(html, "a tag is letters") {
		t.Error("the play page lacks the tags form, the return or the message")
	}
	play["CanEdit"] = false
	out.Reset()
	if err := pages.ExecuteTemplate(&out, "play", play); err != nil {
		t.Fatalf("play as a reader: %v", err)
	}
	if html := out.String(); strings.Contains(html, "tagsform") || !strings.Contains(html, `href="/records?q=%23flaky"`) {
		t.Error("a reader should see the tags as links, not a form")
	}
}

// The Devices page offers Drive and Watch for each device, as the wheel
// and the eye, plus Records.
func TestDevicesPageOffersDriveAndWatch(t *testing.T) {
	devs := []deviceView{{ID: "rig_a/iphone_1", Name: "iphone_1", Kind: "phone", Platform: "ios", OSVersion: "26.6", Model: "iPhone 15 Pro Max", Title: "iPhone 15 Pro Max", Online: true, Rig: rigView{ID: "rig_a", Name: "pi"}, Hold: &holdView{Subject: "guest", Watching: true}}}
	var out strings.Builder
	if err := pages.ExecuteTemplate(&out, "index", map[string]any{"Page": "index", "Devices": devs, "Principal": station.Principal{Subject: "owner", Admin: true}, "CSRF": "c", "JamsOn": true, "Name": "Station", "Named": true}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`href="/devices/rig_a/iphone_1/drive" title="Drive"`, `href="/devices/rig_a/iphone_1/view" title="Watch"`, `href="/records?device=rig_a%2fiphone_1"`, "iOS 26.6", `<b><a href="/devices/rig_a/iphone_1/view">iPhone 15 Pro Max</a></b>`, "available · watched by guest"} {
		if !strings.Contains(html, want) {
			t.Errorf("devices page lacks %q", want)
		}
	}
	if strings.Contains(html, `title="Open"`) {
		t.Error("the page still says Open")
	}
}

func TestRecordTitle(t *testing.T) {
	for name, want := range map[string]string{"record.zip": "record", "hld_972d1871.zip": "hld_972d1871", "shot.final.png": "shot.final", "notes": "notes", ".hidden": ".hidden"} {
		if got := recordTitle(station.Blob{Name: name, SHA256: strings.Repeat("ab", 32)}); got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
	if got := recordTitle(station.Blob{SHA256: strings.Repeat("ab", 32)}); got != "abababababab…" {
		t.Errorf("nameless: %q", got)
	}
}
