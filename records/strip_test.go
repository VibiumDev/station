package records

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/VibiumDev/station"
)

// filmZip is a recording of n frames, each w by h and one flat gray that
// tells which it is: frame i is gray 10+8i. extra goes into the trace as
// more frame lines.
func filmZip(t *testing.T, n, w, h int, extra ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	tr, _ := zw.Create("trace.trace")
	tr.Write([]byte(`{"version":8,"type":"context-options","libraryName":"vibium"}` + "\n"))
	// Written last frame first: the strip goes by time, not by line.
	for i := n - 1; i >= 0; i-- {
		fmt.Fprintf(tr, `{"type":"screencast-frame","pageId":"page@screen","sha1":"page@screen-%d.jpeg","width":%d,"height":%d,"timestamp":%d}`+"\n", i, w, h, 100*i)
	}
	for _, line := range extra {
		tr.Write([]byte(line + "\n"))
	}
	for i := range n {
		img := image.NewGray(image.Rect(0, 0, w, h))
		for p := range img.Pix {
			img.Pix[p] = uint8(10 + 8*i)
		}
		f, _ := zw.Create(fmt.Sprintf("resources/page@screen-%d.jpeg", i))
		if err := jpeg.Encode(f, img, nil); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	return buf.Bytes()
}

// frameOf says which frame a strip picture came from, and its size.
func frameOf(t *testing.T, data []byte) (frame, w, h int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	gray := color.GrayModel.Convert(img.At(b.Dx()/2, b.Dy()/2)).(color.Gray).Y
	return (int(gray) - 10 + 4) / 8, b.Dx(), b.Dy()
}

func TestStripIndex(t *testing.T) {
	for _, c := range []struct{ n, k, i, want int }{
		{30, 24, 0, 0}, {30, 24, 23, 29}, {30, 24, 12, 15},
		{5, 5, 0, 0}, {5, 5, 2, 2}, {5, 5, 4, 4},
		{1, 1, 0, 0}, {100, 24, 23, 99}, {2, 2, 1, 1},
	} {
		if got := stripIndex(c.n, c.k, c.i); got != c.want {
			t.Errorf("frame %d of %d from %d: %d, want %d", c.i, c.k, c.n, got, c.want)
		}
	}
	// Never a frame twice, and always in order.
	last := -1
	for i := range 24 {
		got := stripIndex(30, 24, i)
		if got <= last {
			t.Errorf("frame %d is %d after %d", i, got, last)
		}
		last = got
	}
}

func TestStripOfARecording(t *testing.T) {
	f := setup(t)
	f.store.UnpackDir = filepath.Join(t.TempDir(), "unpacked")
	// Two names that are no plain file names: they are not frames.
	d, w := f.upload("token:ci", filmZip(t, 30, 180, 448,
		`{"type":"screencast-frame","sha1":"../../../etc/passwd","timestamp":5000}`,
		`{"type":"screencast-frame","sha1":"..","timestamp":6000}`), "r.zip", "hld_1")
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	ctx := context.Background()
	b, _ := f.store.Index.Blob(ctx, d.SHA256)
	if StripCount(b) != StripFrames {
		t.Fatalf("a strip of %d from %d frames", StripCount(b), b.Frames)
	}
	for i, want := range map[int]int{0: 0, 12: 15, 23: 29} {
		data, err := f.store.StripFrame(ctx, b, i)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if frame, w, h := frameOf(t, data); frame != want || w != 90 || h != stripHeight {
			t.Errorf("frame %d: recording's frame %d at %dx%d, want %d at 90x%d", i, frame, w, h, want, stripHeight)
		}
	}
	for _, i := range []int{-1, 24, 1000} {
		if _, err := f.store.StripFrame(ctx, b, i); !errors.Is(err, ErrNoThumbnail) {
			t.Errorf("frame %d: %v", i, err)
		}
	}
	// Scaled once: the kept copy answers with the recording's own gone.
	os.RemoveAll(f.store.unpackDir(d.SHA256))
	if data, err := f.store.StripFrame(ctx, b, 12); err != nil || len(data) == 0 {
		t.Errorf("the kept frame: %v", err)
	}

	// Served to a reader, refused to a stranger, gone with the blob.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records/{sha}/strip/{i}", func(w http.ResponseWriter, r *http.Request) {
		var i int
		fmt.Sscanf(r.PathValue("i"), "%d.jpg", &i)
		f.store.ServeStrip(w, r, r.PathValue("sha"), i, principal(r))
	})
	f.mux = mux
	if w := f.do("GET", "/records/"+d.SHA256+"/strip/0.jpg", nil, "X-Test-Subject", "token:ci"); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("serve: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/strip/0.jpg", nil, "X-Test-Subject", "token:other"); w.Code != 403 {
		t.Errorf("a stranger: %d", w.Code)
	}
	if w := f.do("GET", "/records/"+d.SHA256+"/strip/24.jpg", nil, "X-Test-Subject", "token:ci"); w.Code != 404 {
		t.Errorf("past the last: %d", w.Code)
	}
	if err := f.store.Delete(ctx, station.Principal{Subject: "owner", Admin: true}, d.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.store.stripDir(d.SHA256)); err == nil {
		t.Error("the strip should go with the blob")
	}
}

// A short recording's strip is all its frames, and a short frame is not
// stretched.
func TestStripOfAShortRecording(t *testing.T) {
	f := setup(t)
	f.store.UnpackDir = filepath.Join(t.TempDir(), "unpacked")
	d, w := f.upload("token:ci", filmZip(t, 3, 40, 80), "r.zip", "hld_1")
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	ctx := context.Background()
	b, _ := f.store.Index.Blob(ctx, d.SHA256)
	if StripCount(b) != 3 {
		t.Fatalf("a strip of %d", StripCount(b))
	}
	for i := range 3 {
		data, err := f.store.StripFrame(ctx, b, i)
		if err != nil {
			t.Fatal(err)
		}
		if frame, w, h := frameOf(t, data); frame != i || w != 40 || h != 80 {
			t.Errorf("frame %d: %d at %dx%d", i, frame, w, h)
		}
	}
	if _, err := f.store.StripFrame(ctx, b, 3); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("past the last: %v", err)
	}
	if StripCount(station.Blob{Type: "image/png", Frames: 9}) != 0 {
		t.Error("a strip of something that is no recording")
	}
}

// A frame is found by a plain name, as a regular file, and is decoded
// only when its header claims a screen's size.
func TestStripRefusesWhatIsNoFrame(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "resources"), 0o700)
	os.WriteFile(filepath.Join(dir, "resources", "ok.jpeg"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "outside.jpeg"), []byte("x"), 0o600)
	os.Symlink("/etc/hostname", filepath.Join(dir, "resources", "link.jpeg"))
	if _, err := frameFile(dir, "ok.jpeg"); err != nil {
		t.Errorf("a plain name: %v", err)
	}
	for _, name := range []string{"../outside.jpeg", "resources/ok.jpeg", "..", ".", "", ".hidden", "a/b", `a\b`, "link.jpeg", "nope.jpeg", "a\x00b"} {
		if p, err := frameFile(dir, name); err == nil {
			t.Errorf("%q found as %s", name, p)
		}
	}

	// A PNG whose header claims 60000 by 60000 pixels.
	var png bytes.Buffer
	png.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := append([]byte("IHDR"), 0, 0, 0xEA, 0x60, 0, 0, 0xEA, 0x60, 8, 2, 0, 0, 0)
	binary.Write(&png, binary.BigEndian, uint32(13))
	png.Write(ihdr)
	binary.Write(&png, binary.BigEndian, crc32.ChecksumIEEE(ihdr))
	huge := filepath.Join(dir, "huge.png")
	os.WriteFile(huge, png.Bytes(), 0o600)
	if _, err := scaleFrame(huge, stripHeight); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("an enormous picture: %v", err)
	}
	if _, err := scaleFrame(filepath.Join(dir, "resources", "ok.jpeg"), stripHeight); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("a file that is no picture: %v", err)
	}
}

func TestShrinkAverages(t *testing.T) {
	// Left half black, right half white, 8 by 8, to 4 by 4.
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := 4; x < 8; x++ {
			src.SetGray(x, y, color.Gray{255})
		}
	}
	got := shrink(src, 4)
	if b := got.Bounds(); b.Dx() != 4 || b.Dy() != 4 {
		t.Fatalf("size %v", b)
	}
	if l, r := color.GrayModel.Convert(got.At(0, 0)).(color.Gray).Y, color.GrayModel.Convert(got.At(3, 3)).(color.Gray).Y; l != 0 || r != 255 {
		t.Errorf("left %d right %d", l, r)
	}
	if shrink(src, 8) != image.Image(src) || shrink(src, 100) != image.Image(src) {
		t.Error("a picture already short enough should come back as it is")
	}
}
