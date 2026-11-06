// Package blob keeps files by their SHA-256, the way Blossom addresses
// them: a file is stored once under its hash, and the hash is checked
// as the bytes come in, so a store never holds a file under a name that
// does not match it.
package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned for a hash the store does not hold.
var ErrNotFound = errors.New("no such blob")

// ErrMismatch is returned when the bytes do not hash to the name they
// were offered under, or are not the size that was declared.
var ErrMismatch = errors.New("the bytes do not match the hash or size")

// Store is where the bytes live; the index of what they are is the
// station's database.
type Store interface {
	// Put stores r's bytes under sha, which must be their SHA-256 in
	// lowercase hex; size is how many bytes to expect.
	Put(sha string, r io.Reader, size int64) error
	// Open reads a stored blob; the caller closes it.
	Open(sha string) (io.ReadSeekCloser, error)
	Delete(sha string) error
	Exists(sha string) bool
}

// ValidSHA says whether s is a lowercase hex SHA-256.
func ValidSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Disk is a Store in a directory: Dir/<first two hex>/<sha>.
type Disk struct {
	Dir string
}

func (d Disk) path(sha string) string { return filepath.Join(d.Dir, sha[:2], sha) }

// Put writes r to a temporary file next to its final place, hashing as
// it goes, and moves it into place only when the hash and size agree.
func (d Disk) Put(sha string, r io.Reader, size int64) error {
	if !ValidSHA(sha) {
		return fmt.Errorf("%w: %q is not a sha256", ErrMismatch, sha)
	}
	if size < 0 {
		return fmt.Errorf("%w: a size is needed", ErrMismatch)
	}
	final := d.path(sha)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".put-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	h := sha256.New()
	// One byte more than declared tells a long body from an exact one.
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return ErrMismatch
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, final)
}

// Open reads a stored blob.
func (d Disk) Open(sha string) (io.ReadSeekCloser, error) {
	if !ValidSHA(sha) {
		return nil, ErrNotFound
	}
	f, err := os.Open(d.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Delete removes a blob; a blob that is already gone is no error.
func (d Disk) Delete(sha string) error {
	if !ValidSHA(sha) {
		return ErrNotFound
	}
	err := os.Remove(d.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Exists says whether the store holds sha.
func (d Disk) Exists(sha string) bool {
	if !ValidSHA(sha) {
		return false
	}
	_, err := os.Stat(d.path(sha))
	return err == nil
}

// Extension is the file name suffix a media type is known by in a
// blob's URL, or empty.
func Extension(mediaType string) string {
	t, _, _ := strings.Cut(mediaType, ";")
	switch strings.TrimSpace(strings.ToLower(t)) {
	case "application/zip":
		return ".zip"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "application/json":
		return ".json"
	case "text/plain":
		return ".txt"
	case "application/pdf":
		return ".pdf"
	}
	return ""
}
