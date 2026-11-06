package records

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/VibiumDev/station"
)

// a small JPEG: the SOI marker and enough for content sniffing.
var tinyJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, bytes.Repeat([]byte{1}, 40)...)

func recordingZip(t *testing.T, frames ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// The trace first, whole: a zip writer closes an entry when the next
	// one is created.
	tr, _ := zw.Create("trace.trace")
	tr.Write([]byte(`{"version":8,"type":"context-options","libraryName":"vibium"}` + "\n"))
	for i, f := range frames {
		tr.Write([]byte(`{"type":"before","callId":"call@1"}` + "\n"))
		if i%2 == 0 {
			tr.Write([]byte(`{"type":"screencast-frame","pageId":"page@screen","sha1":"` + f + `","width":4,"height":4,"timestamp":` + string(rune('0'+i)) + `}` + "\n"))
		} else {
			// Roadie's order: the keys alphabetical, sha1 before type.
			tr.Write([]byte(`{"height":4,"pageId":"page@screen","sha1":"` + f + `","timestamp":` + string(rune('0'+i)) + `,"type":"screencast-frame","width":4}` + "\n"))
		}
	}
	for i, f := range frames {
		w, _ := zw.Create("resources/" + f)
		w.Write(append(append([]byte{}, tinyJPEG...), byte(i)))
	}
	zw.Close()
	return buf.Bytes()
}

func TestThumbnailIsTheLastFrame(t *testing.T) {
	f := setup(t)
	f.store.UnpackDir = filepath.Join(t.TempDir(), "unpacked")
	body := recordingZip(t, "page@screen-1.jpeg", "page@screen-2.jpeg")
	d, w := f.upload("token:ci", body, "r.zip", "hld_1")
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	b, _ := f.store.Index.Blob(context.Background(), d.SHA256)
	if b.Steps != 2 || b.Frames != 2 || b.Thumb != "page@screen-2.jpeg" || b.Duration <= 0 || d.Steps != 2 || d.Files == "" {
		t.Fatalf("read at upload: %+v / %+v", b, d)
	}
	if _, err := os.Stat(filepath.Join(f.store.unpackDir(d.SHA256), "resources", "page@screen-2.jpeg")); err != nil {
		t.Fatal("the recording should be unpacked")
	}
	data, ctype, err := f.store.Thumbnail(context.Background(), b)
	if err != nil || ctype != "image/jpeg" || data[len(data)-1] != 1 {
		t.Fatalf("thumbnail: %v %s %v", err, ctype, data[len(data)-1:])
	}
	// Served to a reader, refused to a stranger; gone with the blob.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records/{sha}/thumb.jpg", func(w http.ResponseWriter, r *http.Request) {
		f.store.ServeThumbnail(w, r, r.PathValue("sha"), principal(r))
	})
	mux.HandleFunc("GET /records/{sha}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		f.store.ServeFile(w, r, r.PathValue("sha"), r.PathValue("path"), principal(r))
	})
	f.mux = mux
	if w := f.do("GET", "/records/"+d.SHA256+"/files/resources/page@screen-1.jpeg", nil, "X-Test-Subject", "token:ci"); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("a frame by file: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/files/trace.trace", nil, "X-Test-Subject", "token:ci"); w.Code != 200 || w.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("the trace by file: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, bad := range []string{"/records/" + d.SHA256 + "/files/resources", "/records/" + d.SHA256 + "/files/nope.txt"} {
		if w := f.do("GET", bad, nil, "X-Test-Subject", "token:ci"); w.Code != 404 {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/files/trace.trace", nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Fatalf("a stranger reading files: %d", w.Code)
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/thumb.jpg", nil, "X-Test-Subject", "token:ci"); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("serve: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/thumb.jpg", nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Fatalf("stranger: %d", w.Code)
	}
	if err := f.store.Delete(context.Background(), station.Principal{Subject: "owner", Admin: true}, d.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.store.unpackDir(d.SHA256)); err == nil {
		t.Fatal("the unpacked copy should go with the blob")
	}
}

func TestUnpackStaysInside(t *testing.T) {
	f := setup(t)
	root := t.TempDir()
	f.store.UnpackDir = filepath.Join(root, "unpacked")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"../../evil.txt", "/abs.txt", "ok/../fine.txt"} {
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
	}
	zw.Close()
	d, w := f.upload("token:ci", buf.Bytes(), "slip.zip", "hld_1")
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	dir := f.store.unpackDir(d.SHA256)
	for _, want := range []string{"evil.txt", "abs.txt", "fine.txt"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s should land inside the recording's directory", want)
		}
	}
	for _, outside := range []string{filepath.Join(root, "evil.txt"), filepath.Join(root, "..", "evil.txt"), "/abs.txt"} {
		if _, err := os.Stat(outside); err == nil {
			t.Errorf("%s landed outside", outside)
		}
	}
}

func TestThumbnailOfOtherFiles(t *testing.T) {
	f := setup(t)
	f.store.UnpackDir = filepath.Join(t.TempDir(), "unpacked")
	ctx := context.Background()
	// A zip with no frames has none.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("trace.trace")
	w.Write([]byte(`{"type":"context-options"}` + "\n"))
	zw.Close()
	d, _ := f.upload("token:ci", buf.Bytes(), "empty.zip", "hld_1")
	b, _ := f.store.Index.Blob(ctx, d.SHA256)
	if _, _, err := f.store.Thumbnail(ctx, b); !errors.Is(err, ErrNoThumbnail) {
		t.Fatalf("frameless zip: %v", err)
	}
	// A screenshot is its own thumbnail.
	shot := append(append([]byte{}, tinyJPEG...), 7)
	ev, err := f.store.UploadAuth(ctx, station.Principal{Subject: "token:ci"}, UploadRequest{SHA256: hashOf(shot), Size: int64(len(shot)), Type: "image/jpeg", Name: "shot.jpg", HoldID: "hld_1"})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.do("PUT", "/upload", shot, "Authorization", Header(ev), "Content-Type", "image/jpeg"); w.Code != 200 {
		t.Fatalf("shot upload: %d %s", w.Code, w.Body)
	}
	b, _ = f.store.Index.Blob(ctx, hashOf(shot))
	data, ctype, err := f.store.Thumbnail(ctx, b)
	if err != nil || ctype != "image/jpeg" || !bytes.Equal(data, shot) {
		t.Fatalf("screenshot: %v %s", err, ctype)
	}
}
