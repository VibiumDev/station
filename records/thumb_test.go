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
	f.store.ThumbDir = filepath.Join(t.TempDir(), "thumbs")
	body := recordingZip(t, "page@screen-1.jpeg", "page@screen-2.jpeg")
	d, w := f.upload("token:ci", body, "r.zip", "hld_1")
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	b, _ := f.store.Index.Blob(context.Background(), d.SHA256)
	data, ctype, err := f.store.Thumbnail(context.Background(), b)
	if err != nil || ctype != "image/jpeg" || data[len(data)-1] != 1 {
		t.Fatalf("thumbnail: %v %s %v", err, ctype, data[len(data)-1:])
	}
	if _, err := os.Stat(f.store.thumbPath(d.SHA256)); err != nil {
		t.Fatal("the thumbnail should be cached")
	}
	// Served to a reader, refused to a stranger; gone with the blob.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records/{sha}/thumb.jpg", func(w http.ResponseWriter, r *http.Request) {
		f.store.ServeThumbnail(w, r, r.PathValue("sha"), principal(r))
	})
	f.mux = mux
	if w := f.do("GET", "/records/"+d.SHA256+"/thumb.jpg", nil, "X-Test-Subject", "token:ci"); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("serve: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/thumb.jpg", nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Fatalf("stranger: %d", w.Code)
	}
	if err := f.store.Delete(context.Background(), station.Principal{Subject: "owner", Admin: true}, d.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.store.thumbPath(d.SHA256)); err == nil {
		t.Fatal("the thumbnail should go with the blob")
	}
}

func TestThumbnailOfOtherFiles(t *testing.T) {
	f := setup(t)
	f.store.ThumbDir = filepath.Join(t.TempDir(), "thumbs")
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
