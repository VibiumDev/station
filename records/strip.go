package records

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // a frame may be a PNG
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/VibiumDev/station"
)

// The Filmstrip view shows a recording as a row of its frames, first to
// last, that scrolls sideways. A frame there is small, so each is scaled
// down once and kept beside the unpacked recording.

// stripFrames is the most frames a strip offers of one recording. A
// rig's recording has at most as many, so its strip is every frame; a
// longer one is that many spread evenly from the first to the last.
var stripFrames = 600

const (
	// stripHeight is twice the height the page shows, for dense screens.
	stripHeight = 224
	// A file past these is no screen's picture, and is not decoded.
	maxFrameBytes  = 16 << 20
	maxFramePixels = 40 << 20
	// stripKeep is how many recordings' frame lists stay in memory.
	stripKeep = 512
)

var (
	// Scaling a frame takes a core for a moment. Two at a time leaves the
	// rest of a small host to the work it is there for.
	stripSlots = make(chan struct{}, 2)

	stripMu    sync.Mutex
	stripLists = map[string][]string{} // a recording's frames in order, by its hash

	// A frame's name is a plain file name: it is joined to a directory.
	frameName = regexp.MustCompile(`^[A-Za-z0-9@_-][A-Za-z0-9@._-]{0,199}$`)
)

// StripCount is how many frames a recording's strip has.
func StripCount(b station.Blob) int {
	if b.Type != "application/zip" {
		return 0
	}
	return min(b.Frames, stripFrames)
}

// stripIndex is which of n frames the i-th of a strip of k is: the first
// and the last at the ends, the others evenly between. A strip of one is
// the last frame, as the thumbnail is.
func stripIndex(n, k, i int) int {
	if k <= 1 {
		return n - 1
	}
	return (i*(n-1) + (k-1)/2) / (k - 1)
}

func (s *Store) stripDir(sha string) string { return s.unpackDir(sha) + ".frames" }

// StripFrame is the i-th frame of a recording's strip as a small JPEG.
func (s *Store) StripFrame(ctx context.Context, b station.Blob, i int) ([]byte, error) {
	if b.Type != "application/zip" || s.UnpackDir == "" || i < 0 || i >= stripFrames {
		return nil, ErrNoThumbnail
	}
	kept := filepath.Join(s.stripDir(b.SHA256), strconv.Itoa(i)+".jpg")
	if data, err := os.ReadFile(kept); err == nil {
		return data, nil
	}
	b, err := s.Unpacked(ctx, b)
	if err != nil {
		return nil, err
	}
	names, err := s.frameNames(b.SHA256)
	if err != nil {
		return nil, err
	}
	k := min(len(names), stripFrames)
	if i >= k {
		return nil, ErrNoThumbnail
	}
	file, err := frameFile(s.unpackDir(b.SHA256), names[stripIndex(len(names), k, i)])
	if err != nil {
		return nil, err
	}
	select {
	case stripSlots <- struct{}{}:
		defer func() { <-stripSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, err := scaleFrame(file, stripHeight)
	if err != nil {
		return nil, err
	}
	// Kept for the next reader. A failure here costs only the work again.
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err == nil {
		if tmp, err := os.CreateTemp(filepath.Dir(kept), "*.tmp"); err == nil {
			_, werr := tmp.Write(data)
			if cerr := tmp.Close(); werr == nil && cerr == nil {
				os.Rename(tmp.Name(), kept)
			}
			os.Remove(tmp.Name())
		}
	}
	return data, nil
}

// frameNames lists an unpacked recording's frames in the order they were
// taken, as its traces name them.
func (s *Store) frameNames(sha string) ([]string, error) {
	stripMu.Lock()
	names, ok := stripLists[sha]
	stripMu.Unlock()
	if ok {
		return names, nil
	}
	type frame struct {
		name string
		at   float64
	}
	var frames []frame
	dir := s.unpackDir(sha)
	seen := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if seen++; seen > maxUnpackFiles {
			return errors.New("too many files")
		}
		if d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(p, ".trace") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			if !bytes.Contains(sc.Bytes(), []byte(`"screencast-frame"`)) {
				continue
			}
			var e struct {
				Type      string  `json:"type"`
				SHA1      string  `json:"sha1"`
				Timestamp float64 `json:"timestamp"`
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "screencast-frame" || !frameName.MatchString(e.SHA1) {
				continue
			}
			frames = append(frames, frame{e.SHA1, e.Timestamp})
		}
		return sc.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(frames, func(a, b int) bool { return frames[a].at < frames[b].at })
	names = make([]string, len(frames))
	for i, f := range frames {
		names[i] = f.name
	}
	stripMu.Lock()
	if len(stripLists) >= stripKeep {
		clear(stripLists)
	}
	stripLists[sha] = names
	stripMu.Unlock()
	return names, nil
}

// frameFile finds a frame's file in an unpacked recording: a plain name
// under resources/ or at the top, and a regular file of a picture's size.
func frameFile(dir, name string) (string, error) {
	if !frameName.MatchString(name) {
		return "", ErrNoThumbnail
	}
	for _, p := range []string{filepath.Join(dir, "resources", name), filepath.Join(dir, name)} {
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > maxFrameBytes {
			continue
		}
		return p, nil
	}
	return "", ErrNoThumbnail
}

// scaleFrame reads a picture and returns it as a JPEG no taller than
// height.
func scaleFrame(file string, height int) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The header first: a picture that claims an enormous size is never
	// given the memory to decode.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxFramePixels {
		return nil, ErrNoThumbnail
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(io.LimitReader(f, maxFrameBytes))
	if err != nil {
		return nil, ErrNoThumbnail
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, shrink(src, height), &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// shrink scales a picture down to a height, each new pixel the average of
// those it covers. A picture already that short is returned as it is.
func shrink(src image.Image, height int) image.Image {
	sb := src.Bounds()
	if sb.Dy() <= height {
		return src
	}
	width := max(1, sb.Dx()*height/sb.Dy())
	rgba := image.NewRGBA(image.Rect(0, 0, sb.Dx(), sb.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, sb.Min, draw.Src)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		y0, y1 := y*sb.Dy()/height, (y+1)*sb.Dy()/height
		for x := range width {
			x0, x1 := x*sb.Dx()/width, (x+1)*sb.Dx()/width
			var r, g, b uint32
			for yy := y0; yy < y1; yy++ {
				row := rgba.Pix[yy*rgba.Stride+x0*4 : yy*rgba.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					r, g, b = r+uint32(row[i]), g+uint32(row[i+1]), b+uint32(row[i+2])
				}
			}
			n := uint32((x1 - x0) * (y1 - y0))
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return dst
}

// ServeStrip answers GET with one frame of a recording's strip for a
// caller who may read the recording.
func (s *Store) ServeStrip(w http.ResponseWriter, r *http.Request, sha string, i int, p *station.Principal) {
	b, err := s.readable(r, sha, p)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := s.StripFrame(r.Context(), b, i)
	if errors.Is(err, ErrNoThumbnail) {
		writeError(w, refuse(http.StatusNotFound, "no such frame"))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", `"`+b.SHA256+"-"+strconv.Itoa(i)+`"`)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", b.CreatedAt, bytes.NewReader(data))
}
