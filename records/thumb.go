package records

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
)

// ErrNoThumbnail says a blob has no picture to show for it.
var ErrNoThumbnail = errors.New("no thumbnail")

// A frame line names its file in sha1; the keys come in any order (Vibium
// writes type first, Roadie alphabetically).
var (
	frameLine = regexp.MustCompile(`"type"\s*:\s*"screencast-frame"`)
	frameName = regexp.MustCompile(`"sha1"\s*:\s*"([^"]+)"`)
)

// Thumbnail is a picture for a blob: a screenshot is its own, and a
// recording's is the last frame its trace names, taken out of the zip
// on first use and kept under ThumbDir. The media type comes with it.
func (s *Store) Thumbnail(ctx context.Context, b station.Blob) ([]byte, string, error) {
	switch {
	case strings.HasPrefix(b.Type, "image/"):
		f, err := s.Blobs.Open(b.SHA256)
		if err != nil {
			return nil, "", err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 8<<20))
		return data, b.Type, err
	case b.Type != "application/zip":
		return nil, "", ErrNoThumbnail
	}
	if s.ThumbDir != "" {
		if data, err := os.ReadFile(s.thumbPath(b.SHA256)); err == nil {
			return data, "image/jpeg", nil
		}
	}
	data, ctype, err := lastFrame(s.Blobs, b.SHA256)
	if err != nil {
		return nil, "", err
	}
	if s.ThumbDir != "" && ctype == "image/jpeg" {
		if err := os.MkdirAll(s.ThumbDir, 0o700); err == nil {
			os.WriteFile(s.thumbPath(b.SHA256), data, 0o600)
		}
	}
	return data, ctype, nil
}

func (s *Store) thumbPath(sha string) string { return filepath.Join(s.ThumbDir, sha+".jpg") }

func (s *Store) removeThumb(sha string) {
	if s.ThumbDir != "" {
		os.Remove(s.thumbPath(sha))
	}
}

// lastFrame reads a recording zip and returns the last frame its trace
// names, from resources/.
func lastFrame(store blob.Store, sha string) ([]byte, string, error) {
	f, err := store.Open(sha)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, "", err
	}
	ra, ok := f.(io.ReaderAt)
	if !ok {
		return nil, "", ErrNoThumbnail
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return nil, "", ErrNoThumbnail
	}
	var last string
	for _, zf := range zr.File {
		if !strings.HasSuffix(zf.Name, ".trace") || zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if !frameLine.Match(line) {
				continue
			}
			if m := frameName.FindSubmatch(line); m != nil {
				last = string(m[1])
			}
		}
		rc.Close()
	}
	if last == "" {
		return nil, "", ErrNoThumbnail
	}
	for _, zf := range zr.File {
		if zf.Name != "resources/"+last && zf.Name != last {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, "", err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		if err != nil {
			return nil, "", err
		}
		ctype := http.DetectContentType(data)
		if !strings.HasPrefix(ctype, "image/") {
			return nil, "", ErrNoThumbnail
		}
		return data, ctype, nil
	}
	return nil, "", ErrNoThumbnail
}

// ServeThumbnail answers GET with a blob's thumbnail for a caller who may
// read the blob.
func (s *Store) ServeThumbnail(w http.ResponseWriter, r *http.Request, sha string, p *station.Principal) {
	b, err := s.Index.Blob(r.Context(), sha)
	if errors.Is(err, station.ErrNotFound) {
		writeError(w, refuse(http.StatusNotFound, "no such blob"))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if !s.mayRead(r.Context(), b, p, nil) {
		writeError(w, refuse(http.StatusForbidden, "not yours to read"))
		return
	}
	data, ctype, err := s.Thumbnail(r.Context(), b)
	if errors.Is(err, ErrNoThumbnail) {
		writeError(w, refuse(http.StatusNotFound, "no picture for this file"))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("ETag", `"`+sha+`"`)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", b.CreatedAt, bytes.NewReader(data))
}
