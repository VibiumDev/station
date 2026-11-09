package records

import (
	"strings"
	"testing"
)

// Tags typed by anyone: never a panic, and only well-formed tags kept.
func FuzzParseTags(f *testing.F) {
	for _, s := range []string{"", "a", "a b", "#flaky release-1.2", "UPPER", "a..b", strings.Repeat("x", 100), strings.Repeat("t ", 50), "\xff", "a/b", "é"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		tags, err := ParseTags(s)
		if err != nil {
			return
		}
		if len(tags) > 20 {
			t.Fatalf("%d tags", len(tags))
		}
		for _, tag := range tags {
			if tag == "" || len(tag) > 40 || strings.ToLower(tag) != tag || strings.ContainsAny(tag, " #/\\") {
				t.Fatalf("kept %q from %q", tag, s)
			}
		}
	})
}
