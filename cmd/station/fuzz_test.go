package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/VibiumDev/station"
)

// The parsers that gate a sign-in never panic, and refuse what they do
// not understand. The seeds run with every test; `go test -fuzz` digs.

func FuzzSafeNext(f *testing.F) {
	for _, s := range []string{"", "/", "/jams", "//evil", "/\\evil", "https://x", "/a\r\nb", "/\x00", "/\t/evil"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := safeNext(s)
		if got != "/" && (!strings.HasPrefix(got, "/") || strings.HasPrefix(got, "//") ||
			strings.ContainsFunc(got, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f })) {
			t.Fatalf("safeNext(%q) = %q", s, got)
		}
	})
}

func FuzzCodes(f *testing.F) {
	secret := newTOTPSecret()
	for _, s := range []string{"", "123456", "12 34 56", "abcde-fghjk", "ABCDE FGHJK", strings.Repeat("9", 40), "\xff\xfe", "é1234567"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		normalizeCode(s)
		hashCode(s)
		isBackupCode(s)
		if _, ok := totpCheck(secret, s, time.Now()); ok && len(digitsOnly(s)) != totpDigits {
			t.Fatalf("totpCheck accepted %q", s)
		}
		if _, ok := totpCheck("", s, time.Now()); ok {
			t.Fatalf("an empty secret accepted %q", s)
		}
	})
}

func FuzzPasswordRules(f *testing.F) {
	for _, s := range []string{"", "short", "correct horse battery", "Password2026!!", "ann@example.com-x", strings.Repeat("a", 300)} {
		f.Add(s, "ann@example.com")
	}
	f.Fuzz(func(t *testing.T, pw, login string) {
		err := checkPasswordRules(pw, login)
		if err == nil && (len(pw) < minPassword || len(pw) > 200) {
			t.Fatalf("accepted %q", pw)
		}
		login = strings.ToLower(strings.TrimSpace(login))
		if err == nil && utf8.ValidString(login) && login != "" && strings.Contains(strings.ToLower(pw), login) {
			t.Fatalf("accepted a password holding the login: %q %q", pw, login)
		}
	})
}

func FuzzLevels(f *testing.F) {
	for _, s := range []string{"", "none", "watch", "drive", "jam", "admin", "boss", "WATCH", "read-only"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if l, err := station.ParseLevel(s); err == nil && l != station.LevelNone && l != station.LevelWatch && l != station.LevelDrive && l != station.LevelJam {
			t.Fatalf("ParseLevel(%q) = %q", s, l)
		}
		if l, err := station.ParseTokenLevel(s); err == nil && l != station.TokenWatch && l != station.TokenDrive && l != station.TokenAdmin {
			t.Fatalf("ParseTokenLevel(%q) = %q", s, l)
		}
		if scopes, err := station.ParseScopes([]string{s}); err == nil {
			for _, sc := range scopes {
				if sc != station.ScopeView && sc != station.ScopeInput && sc != station.ScopeBidi && sc != station.ScopeAdmin {
					t.Fatalf("ParseScopes(%q) = %v", s, scopes)
				}
			}
		}
	})
}

func FuzzSetupToken(f *testing.F) {
	for _, s := range []string{"", "nonsense", strings.Repeat("a", 48), "../etc/passwd", "%00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if hashToken(s) == hashToken(s+"x") {
			t.Fatal("hash collision")
		}
	})
}
