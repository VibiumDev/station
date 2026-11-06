package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestPutOpenDelete(t *testing.T) {
	d := Disk{Dir: t.TempDir()}
	body := "a recording"
	sha := hashOf(body)
	if err := d.Put(sha, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if !d.Exists(sha) {
		t.Fatal("stored blob missing")
	}
	f, err := d.Open(sha)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != body {
		t.Fatalf("read back %q", got)
	}
	if err := d.Delete(sha); err != nil {
		t.Fatal(err)
	}
	if d.Exists(sha) {
		t.Fatal("still there after delete")
	}
	if _, err := d.Open(sha); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open after delete: %v", err)
	}
	if err := d.Delete(sha); err != nil {
		t.Fatalf("a second delete should be quiet: %v", err)
	}
}

func TestPutRefusesWrongBytes(t *testing.T) {
	d := Disk{Dir: t.TempDir()}
	body := "a recording"
	sha := hashOf(body)
	cases := map[string]struct {
		sha  string
		body string
		size int64
	}{
		"wrong hash": {hashOf("something else"), body, int64(len(body))},
		"short body": {sha, body[:5], int64(len(body))},
		"long body":  {sha, body + "!", int64(len(body))},
		"bad hash":   {"nothex", body, int64(len(body))},
		"uppercase":  {strings.ToUpper(sha), body, int64(len(body))},
		"wrong size": {sha, body, int64(len(body)) + 1},
	}
	for name, c := range cases {
		if err := d.Put(c.sha, strings.NewReader(c.body), c.size); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: got %v, want ErrMismatch", name, err)
		}
		if d.Exists(c.sha) {
			t.Errorf("%s: a refused put left a file", name)
		}
	}
	if left, _ := io.ReadAll(strings.NewReader("")); len(left) != 0 {
		t.Fatal("unreachable")
	}
}

func TestExtension(t *testing.T) {
	if Extension("application/zip") != ".zip" || Extension("image/jpeg; charset=binary") != ".jpg" || Extension("application/x-unknown") != "" {
		t.Fatal("extensions")
	}
}
