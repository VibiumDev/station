package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
	"unicode"

	"rsc.io/qr"
)

// The second factor: time-based one-time codes as every authenticator
// app makes them (RFC 6238 over HMAC-SHA1, six digits, 30-second steps),
// backup codes shown once and kept hashed, and the setup links that
// start it all.

const (
	totpPeriod = 30 * time.Second
	totpDigits = 6
	// totpSkew is how many steps either side of now a code may come
	// from: one, for a phone's clock a little off.
	totpSkew = 1

	backupCodeCount = 10
	// backupAlphabet leaves out the letters and digits people confuse.
	backupAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

	inviteLife  = 24 * time.Hour
	minPassword = 12
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret is a fresh authenticator secret, 160 bits in base32.
func newTOTPSecret() string {
	var b [20]byte
	rand.Read(b[:])
	return totpEncoding.EncodeToString(b[:])
}

// totpStep is the time step a moment falls in.
func totpStep(t time.Time) int64 { return t.Unix() / int64(totpPeriod/time.Second) }

// totpCode is the code an authenticator shows for a secret at a step.
func totpCode(secret string, step int64) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, n%mod), nil
}

// totpCheck says whether a code is right for the secret around now, and
// which step it belongs to, for the store to refuse a second use.
func totpCheck(secret, code string, now time.Time) (int64, bool) {
	code = digitsOnly(code)
	if len(code) != totpDigits {
		return 0, false
	}
	step := totpStep(now)
	for d := int64(0); d <= totpSkew; d++ {
		for _, s := range []int64{step - d, step + d} {
			want, err := totpCode(secret, s)
			if err == nil && hmac.Equal([]byte(want), []byte(code)) {
				return s, true
			}
		}
	}
	return 0, false
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// otpauthURL is what the QR code says: the account and its secret, for
// an authenticator app to add.
func otpauthURL(issuer, login, secret string) string {
	label := url.PathEscape(issuer + ":" + login)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(int(totpPeriod / time.Second))}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// qrSVG draws a QR code of the text as an inline SVG: dark modules on a
// light field whatever the page's theme, so a phone can read it.
func qrSVG(text string) (template.HTML, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	size := code.Size + 2*quiet
	var path strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	svg := fmt.Sprintf(`<svg class="qr" viewBox="0 0 %d %d" role="img" aria-label="QR code for the authenticator app" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		size, size, size, size, path.String())
	return template.HTML(svg), nil
}

// newBackupCodes makes a set of backup codes, each ten letters and
// digits in two groups.
func newBackupCodes() []string {
	out := make([]string, backupCodeCount)
	for i := range out {
		var b [10]byte
		rand.Read(b[:])
		var s strings.Builder
		for j, c := range b {
			if j == 5 {
				s.WriteByte('-')
			}
			s.WriteByte(backupAlphabet[int(c)%len(backupAlphabet)])
		}
		out[i] = s.String()
	}
	return out
}

// normalizeCode strips what a person may have added to a code: spaces,
// dashes, capitals.
func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, code)
}

// hashCode is how a backup code or a setup link is kept: a SHA-256 of
// its normalized form, in hex.
func hashCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return hex.EncodeToString(sum[:])
}

// hashToken is how a setup link's token is kept.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// isBackupCode says whether what was typed looks like a backup code
// rather than an authenticator's six digits.
func isBackupCode(code string) bool { return len(normalizeCode(code)) == backupCodeCount }

// weakWords are the words a password may not be, however dressed up
// with digits and symbols.
var weakWords = map[string]bool{
	"password": true, "passwort": true, "qwerty": true, "qwertyuiop": true, "letmein": true, "welcome": true,
	"changeme": true, "iloveyou": true, "admin": true, "administrator": true, "secret": true, "station": true,
	"vibium": true, "roadie": true, "iphone": true, "android": true, "monkey": true, "dragon": true,
	"football": true, "baseball": true, "sunshine": true, "princess": true, "abcdefghijkl": true,
}

// checkPassword rules on a new password: long enough, not the login or
// the part of it before the @, and not a common word with digits around
// it. The rules are few on purpose; the second factor does the rest.
func checkPasswordRules(password, login string) error {
	switch {
	case len(password) < minPassword:
		return fmt.Errorf("a password needs at least %d characters", minPassword)
	case len(password) > 200:
		return errors.New("a password is at most 200 characters")
	}
	low := strings.ToLower(password)
	login = strings.ToLower(strings.TrimSpace(login))
	local, _, _ := strings.Cut(login, "@")
	if login != "" && strings.Contains(low, login) || len(local) >= 3 && strings.Contains(low, local) {
		return errors.New("a password may not contain your login")
	}
	letters := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return r
		}
		return -1
	}, low)
	digits := digitsOnly(low)
	if weakWords[letters] || letters == "" && sequential(digits) {
		return errors.New("that password is too common; try a few unrelated words")
	}
	return nil
}

// sequential says whether digits run up, down or repeat, wrapping past
// 9: 1234567890…, 987654…, 111111….
func sequential(d string) bool {
	if len(d) < 2 {
		return true
	}
	up, down, same := true, true, true
	for i := 1; i < len(d); i++ {
		prev, cur := int(d[i-1]-'0'), int(d[i]-'0')
		if cur != (prev+1)%10 {
			up = false
		}
		if cur != (prev+9)%10 {
			down = false
		}
		if cur != prev {
			same = false
		}
	}
	return up || down || same
}
