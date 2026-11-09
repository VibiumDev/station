package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// The codes are RFC 6238's: its SHA-1 test vectors, cut to six digits.
func TestTOTPVectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for _, c := range []struct {
		at   int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {20000000000, "353130"}} {
		got, err := totpCode(secret, totpStep(time.Unix(c.at, 0)))
		if err != nil || got != c.want {
			t.Errorf("at %d: %q %v, want %q", c.at, got, err, c.want)
		}
	}
	if _, err := totpCode("not base32!", 1); err == nil {
		t.Error("a bad secret made a code")
	}
}

// A code is good for its own step and one either side, spaces and all,
// and for nothing further.
func TestTOTPCheckWindow(t *testing.T) {
	secret := newTOTPSecret()
	if len(secret) != 32 {
		t.Fatalf("secret %q", secret)
	}
	now := time.Now()
	step := totpStep(now)
	for d := int64(-2); d <= 2; d++ {
		code, _ := totpCode(secret, step+d)
		got, ok := totpCheck(secret, code[:3]+" "+code[3:], now)
		if want := d >= -1 && d <= 1; ok != want || ok && got != step+d {
			t.Errorf("step %+d: ok=%v step=%d, want ok=%v", d, ok, got, want)
		}
	}
	if _, ok := totpCheck(secret, "12345", now); ok {
		t.Error("five digits passed")
	}
	if _, ok := totpCheck("", "123456", now); ok {
		t.Error("an empty secret passed")
	}
}

// Backup codes are ten, in two groups of five from an alphabet without
// the characters people confuse, and read the same however typed.
func TestBackupCodes(t *testing.T) {
	codes := newBackupCodes()
	if len(codes) != backupCodeCount {
		t.Fatalf("%d codes", len(codes))
	}
	shape := regexp.MustCompile(`^[a-hj-km-np-z2-9]{5}-[a-hj-km-np-z2-9]{5}$`)
	seen := map[string]bool{}
	for _, c := range codes {
		if !shape.MatchString(c) || seen[c] {
			t.Errorf("code %q", c)
		}
		seen[c] = true
		if !isBackupCode(c) || !isBackupCode(strings.ToUpper(c)) {
			t.Errorf("%q not taken for a backup code", c)
		}
	}
	if hashCode("ABCDE-FGHJK") != hashCode("abcde fghjk") || hashCode("abcde-fghjk") == hashCode("abcde-fghjm") {
		t.Error("hashes")
	}
	if isBackupCode("123456") || isBackupCode("") {
		t.Error("six digits are an authenticator's")
	}
	if hashToken("a") == hashToken("b") || len(hashToken("a")) != 64 {
		t.Error("token hashes")
	}
}

// The password rules: length, not the login, not a common word.
func TestPasswordRules(t *testing.T) {
	login := "ann@example.com"
	for _, c := range []struct {
		pw string
		ok bool
	}{
		{"correct horse battery", true},
		{"Tr0ub4dor&3 plus more", true},
		{"short", false},
		{"Password2026!!", false},
		{"qwertyuiop12", false},
		{"123456789012", false},
		{"111111111111", false},
		{"ann@example.com1", false},
		{"my ann is here!!", false},
		{strings.Repeat("x", 201), false},
		{"xK9#mP2$vL5@qR8!", true},
	} {
		err := checkPasswordRules(c.pw, login)
		if (err == nil) != c.ok {
			t.Errorf("%q: %v, want ok=%v", c.pw, err, c.ok)
		}
	}
	if err := checkPasswordRules("some long password here", "ab"); err != nil {
		t.Errorf("a two-letter login is too short to forbid: %v", err)
	}
}

func TestQRAndOtpauth(t *testing.T) {
	u := otpauthURL("Vibium Station", "ann@example.com", "GEZDGNBV")
	if !strings.HasPrefix(u, "otpauth://totp/Vibium%20Station:ann@example.com?") || !strings.Contains(u, "secret=GEZDGNBV") || !strings.Contains(u, "issuer=Vibium+Station") {
		t.Errorf("otpauth %q", u)
	}
	svg, err := qrSVG(u)
	if err != nil || !strings.HasPrefix(string(svg), `<svg class="qr" viewBox="0 0 `) || !strings.Contains(string(svg), `<path d="M`) {
		t.Errorf("svg: %v %.80s", err, svg)
	}
	if spaced("ABCDEFGHIJ") != "ABCD EFGH IJ" {
		t.Error(spaced("ABCDEFGHIJ"))
	}
}
