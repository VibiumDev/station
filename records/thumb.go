package records

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/VibiumDev/station"
)

// A recording is kept unpacked beside its zip, under UnpackDir/<sha256>,
// from the moment it is uploaded: the station reads its trace once for
// what the cards show (steps, frames, how long it ran), takes the last
// frame from the files for the thumbnail, and can serve any file of it
// without opening the zip again. The unpacked copy goes with the blob.

// ErrNoThumbnail says a blob has no picture to show for it.
var ErrNoThumbnail = errors.New("no thumbnail")

const maxUnpackFiles = 20000

func (s *Store) unpackDir(sha string) string { return filepath.Join(s.UnpackDir, sha) }

// Unpacked makes sure a recording is unpacked and read, and returns the
// blob as the index then has it. Blobs that are not zips are returned
// as they are.
func (s *Store) Unpacked(ctx context.Context, b station.Blob) (station.Blob, error) {
	if b.Type != "application/zip" || s.UnpackDir == "" {
		return b, nil
	}
	if st, err := os.Stat(s.unpackDir(b.SHA256)); err == nil && st.IsDir() && (b.Steps > 0 || b.Frames > 0 || b.Thumb != "") {
		return b, nil
	}
	stats, err := s.unpack(b.SHA256)
	if err != nil {
		return b, err
	}
	b.Steps, b.Frames, b.Duration, b.Thumb = stats.steps, stats.frames, stats.duration, stats.last
	if err := s.Index.PutBlob(ctx, b); err != nil {
		return b, err
	}
	return b, nil
}

// UnpackAll unpacks every recording that is not yet, for stores from
// before unpacking and for anything a crash left behind.
func (s *Store) UnpackAll(ctx context.Context) (int, error) {
	if s.UnpackDir == "" {
		return 0, nil
	}
	bs, err := s.Index.Blobs(ctx, station.BlobQuery{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range bs {
		if b.Type != "application/zip" {
			continue
		}
		before := b
		if nb, err := s.Unpacked(ctx, b); err == nil && (nb.Steps != before.Steps || nb.Thumb != before.Thumb) {
			n++
		}
	}
	return n, nil
}

type recordingStats struct {
	steps, frames int
	duration      time.Duration
	last          string // the last frame's file, under resources/
}

// unpack writes a zip's files under UnpackDir/<sha256>, through a
// temporary directory, refusing anything that would land outside it and
// more than twice MaxFile in all, and reads the trace as it goes.
func (s *Store) unpack(sha string) (recordingStats, error) {
	var stats recordingStats
	f, err := s.Blobs.Open(sha)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return stats, err
	}
	ra, ok := f.(io.ReaderAt)
	if !ok {
		return stats, errors.New("the blob store cannot be read at random")
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return stats, fmt.Errorf("not a zip: %w", err)
	}
	limit := int64(1 << 30)
	if s.MaxFile > 0 {
		limit = 2 * s.MaxFile
	}
	final := s.unpackDir(sha)
	tmp := final + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return stats, err
	}
	defer os.RemoveAll(tmp)
	var total int64
	for i, zf := range zr.File {
		if i >= maxUnpackFiles {
			return stats, errors.New("too many files to unpack")
		}
		if zf.FileInfo().IsDir() {
			continue
		}
		// Cleaned against the root, a name cannot climb out of it.
		name := strings.TrimPrefix(path.Clean("/"+zf.Name), "/")
		if name == "" || strings.ContainsRune(name, 0) {
			continue
		}
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if !strings.HasPrefix(dst, tmp+string(os.PathSeparator)) {
			return stats, fmt.Errorf("refusing %q", zf.Name)
		}
		if total += int64(zf.UncompressedSize64); total > limit {
			return stats, fmt.Errorf("more than %d bytes unpacked", limit)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return stats, err
		}
		rc, err := zf.Open()
		if err != nil {
			return stats, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			rc.Close()
			return stats, err
		}
		_, err = io.Copy(out, io.LimitReader(rc, int64(zf.UncompressedSize64)+1))
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return stats, err
		}
		if strings.HasSuffix(name, ".trace") {
			if err := readTrace(dst, &stats); err != nil {
				return stats, err
			}
		}
	}
	os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		return stats, err
	}
	return stats, nil
}

// readTrace adds a trace file's steps, frames and end time to stats.
func readTrace(file string, stats *recordingStats) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var end float64
	for sc.Scan() {
		var e struct {
			Type      string  `json:"type"`
			Class     string  `json:"class"`
			SHA1      string  `json:"sha1"`
			Timestamp float64 `json:"timestamp"`
			StartTime float64 `json:"startTime"`
			EndTime   float64 `json:"endTime"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "before":
			if e.Class != "Tracing" {
				stats.steps++
			}
			end = max(end, e.StartTime)
		case "after":
			end = max(end, e.EndTime)
		case "screencast-frame":
			stats.frames++
			stats.last = e.SHA1
			end = max(end, e.Timestamp)
		}
	}
	if end > 0 {
		stats.duration = time.Duration(end) * time.Millisecond
	}
	return sc.Err()
}

// Thumbnail is a picture for a blob with its media type: a screenshot is
// its own, and a recording's is the last frame its trace named.
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
	b, err := s.Unpacked(ctx, b)
	if err != nil {
		return nil, "", err
	}
	if b.Thumb == "" {
		return nil, "", ErrNoThumbnail
	}
	for _, rel := range []string{"resources/" + b.Thumb, b.Thumb} {
		data, err := os.ReadFile(filepath.Join(s.unpackDir(b.SHA256), filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		ctype := http.DetectContentType(data)
		if !strings.HasPrefix(ctype, "image/") {
			return nil, "", ErrNoThumbnail
		}
		return data, ctype, nil
	}
	return nil, "", ErrNoThumbnail
}

func (s *Store) removeUnpacked(sha string) {
	if s.UnpackDir != "" {
		os.RemoveAll(s.unpackDir(sha))
		os.RemoveAll(s.unpackDir(sha) + ".tmp")
	}
}

// ServeThumbnail answers GET with a blob's thumbnail for a caller who may
// read the blob.
func (s *Store) ServeThumbnail(w http.ResponseWriter, r *http.Request, sha string, p *station.Principal) {
	b, err := s.readable(r, sha, p)
	if err != nil {
		writeError(w, err)
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

// ServeFile answers GET with one file out of an unpacked recording, such
// as trace.trace or resources/<frame>, for a caller who may read it.
func (s *Store) ServeFile(w http.ResponseWriter, r *http.Request, sha, rel string, p *station.Principal) {
	b, err := s.readable(r, sha, p)
	if err != nil {
		writeError(w, err)
		return
	}
	if b.Type != "application/zip" {
		writeError(w, refuse(http.StatusNotFound, "not a recording"))
		return
	}
	if _, err := s.Unpacked(r.Context(), b); err != nil {
		writeError(w, err)
		return
	}
	dir := s.unpackDir(sha)
	name := strings.TrimPrefix(path.Clean("/"+rel), "/")
	full := filepath.Join(dir, filepath.FromSlash(name))
	if name == "" || !strings.HasPrefix(full, dir+string(os.PathSeparator)) {
		writeError(w, refuse(http.StatusNotFound, "no such file"))
		return
	}
	f, err := os.Open(full)
	if err != nil {
		writeError(w, refuse(http.StatusNotFound, "no such file"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, refuse(http.StatusNotFound, "no such file"))
		return
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	switch {
	case strings.HasSuffix(name, ".trace"), strings.HasSuffix(name, ".network"):
		ctype = "application/x-ndjson"
	case ctype == "":
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, b.CreatedAt, f)
}

// readable finds a blob the caller may read.
func (s *Store) readable(r *http.Request, sha string, p *station.Principal) (station.Blob, error) {
	b, err := s.Index.Blob(r.Context(), sha)
	if errors.Is(err, station.ErrNotFound) {
		return b, refuse(http.StatusNotFound, "no such blob")
	}
	if err != nil {
		return b, err
	}
	if !s.mayRead(r.Context(), b, p, nil) {
		return b, refuse(http.StatusForbidden, "not yours to read")
	}
	return b, nil
}
