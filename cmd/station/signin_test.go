package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
)

func signinServer(t *testing.T) (*server, station.Principal) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	ctx := context.Background()
	hash, _ := hashPassword("owner-secret-123")
	if _, err := db.SeedOwner(ctx, hash); err != nil {
		t.Fatal(err)
	}
	owner, _ := db.UserByLogin(ctx, station.OwnerLogin)
	a := newAuth(&Config{}, db, nil, false, false)
	a.delay = 0
	s := &server{cfg: &Config{}, db: db, auth: a, publicURL: "https://st.example"}
	return s, principalOf(owner, "")
}

// cookies carries a response's cookies into the next request.
func cookies(w *httptest.ResponseRecorder, r *http.Request) *http.Request {
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func form(method, target string, values url.Values, from *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if from != nil {
		cookies(from, r)
	}
	return r
}

// A new user goes from the setup link to signed in: a password that
// meets the rules, the authenticator confirmed by a code, the backup
// codes shown once; the link is spent on the way and the pages wait
// for them at each step.
func TestSetupLinkToSignedIn(t *testing.T) {
	s, owner := signinServer(t)
	a, ctx := s.auth, context.Background()
	ann, link, err := s.addUser(ctx, owner, "ann@example.com", "Ann", false)
	if err != nil || !strings.HasPrefix(link, "https://st.example/setup/") {
		t.Fatalf("add: %q %v", link, err)
	}
	token := strings.TrimPrefix(link, "https://st.example/setup/")
	get := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/setup/"+token, nil)
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		s.setupLinkPage(w, r)
		return w
	}
	if w := get(token); w.Code != 200 || !strings.Contains(w.Body.String(), "Welcome, Ann.") || !strings.Contains(w.Body.String(), "ann@example.com") {
		t.Fatalf("setup page: %d %.200s", w.Code, w.Body.String())
	}
	if w := get("nonsense"); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "This link is spent") {
		t.Errorf("a made-up link: %d", w.Code)
	}
	post := func(token string, pw, again string) *httptest.ResponseRecorder {
		r := form("POST", "/setup/"+token, url.Values{"password": {pw}, "again": {again}}, nil)
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		s.setupLinkPost(w, r)
		return w
	}
	if w := post(token, "short", "short"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "at least 12 characters") {
		t.Errorf("a short password: %d", w.Code)
	}
	if w := post(token, "Password2026!!", "Password2026!!"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "too common") {
		t.Errorf("a common password: %d", w.Code)
	}
	if w := post(token, "correct horse battery", "correct horse batter"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "differ") {
		t.Errorf("mismatched passwords: %d", w.Code)
	}
	w := post(token, "correct horse battery", "correct horse battery")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/account/setup" {
		t.Fatalf("the password: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := get(token); w.Code != http.StatusGone {
		t.Errorf("the link opened twice: %d", w.Code)
	}
	// With the password set, the session is at the setup stage: pages
	// send it there, the API knows nobody, and the setup page shows a QR.
	r := cookies(w, httptest.NewRequest("GET", "/devices", nil))
	if _, err := a.Authenticate(r); !errors.Is(err, errSetupDue) || !errors.Is(err, station.ErrUnauthenticated) {
		t.Fatalf("setup stage: %v", err)
	}
	pw := httptest.NewRecorder()
	s.page(station.ActionView, func(http.ResponseWriter, *http.Request, station.Principal) { t.Error("the page ran") }).ServeHTTP(pw, r)
	if pw.Code != http.StatusSeeOther || pw.Header().Get("Location") != "/account/setup" {
		t.Errorf("a page before setup: %d %s", pw.Code, pw.Header().Get("Location"))
	}
	sw := httptest.NewRecorder()
	s.accountSetupPage(sw, cookies(w, httptest.NewRequest("GET", "/account/setup", nil)))
	if sw.Code != 200 || !strings.Contains(sw.Body.String(), `<svg class="qr"`) || !strings.Contains(sw.Body.String(), "Or type the secret in") || strings.Contains(sw.Body.String(), "Keep the old one") {
		t.Fatalf("setup page: %d %.300s", sw.Code, sw.Body.String())
	}
	sess := a.sessionOf(r)
	if sess == nil || sess.stage != stageSetup || sess.secret == "" {
		t.Fatalf("session: %+v", sess)
	}
	csrf := sess.csrf
	code, _ := totpCode(sess.secret, totpStep(time.Now()))
	wrong := code[:5] + string('0'+(code[5]-'0'+1)%10)
	cw := httptest.NewRecorder()
	s.accountSetupPost(cw, form("POST", "/account/setup", url.Values{"csrf": {csrf}, "code": {wrong}}, w))
	if cw.Code != http.StatusUnauthorized || !strings.Contains(cw.Body.String(), "not right") {
		t.Errorf("a wrong code: %d", cw.Code)
	}
	if u, _ := s.db.User(ctx, ann.ID); u.TwoFactor {
		t.Fatal("a wrong code set the authenticator")
	}
	cw = httptest.NewRecorder()
	s.accountSetupPost(cw, form("POST", "/account/setup", url.Values{"csrf": {csrf}, "code": {code}}, w))
	if cw.Code != 200 || !strings.Contains(cw.Body.String(), "Your backup codes.") {
		t.Fatalf("the right code: %d %.300s", cw.Code, cw.Body.String())
	}
	codes := regexp.MustCompile(`<li>([a-z2-9]{5}-[a-z2-9]{5})</li>`).FindAllStringSubmatch(cw.Body.String(), -1)
	if len(codes) != backupCodeCount {
		t.Fatalf("%d backup codes shown", len(codes))
	}
	if u, _ := s.db.User(ctx, ann.ID); !u.TwoFactor || !u.HasPassword || u.LastLogin.IsZero() {
		t.Errorf("after setup: %+v", u)
	}
	// Signed in now, under a fresh session id.
	in := cookies(cw, httptest.NewRequest("GET", "/devices", nil))
	if p, err := a.Authenticate(in); err != nil || p.User != ann.ID || p.Admin {
		t.Fatalf("signed in: %+v %v", p, err)
	}
	if old, _ := r.Cookie(sessionCookie); a.sessions[old.Value] != nil {
		t.Error("the setup-stage id still works")
	}
	// The Account page says so.
	var out strings.Builder
	data := s.accountData(in, principalOf(station.User{ID: ann.ID}, ""), nil)
	data["Name"], data["Named"], data["Host"] = "Station", true, ""
	if err := pages.ExecuteTemplate(&out, "account", data); err != nil {
		t.Fatal(err)
	}
	if h := out.String(); !strings.Contains(h, "10 backup codes left") || !strings.Contains(h, "Replace the authenticator") || !strings.Contains(h, `name="current"`) {
		t.Errorf("account page: %.400s", h)
	}

	// Signing in again: the password, then a code; a backup code works
	// once; wrong codes end the attempt after five.
	a.logout(httptest.NewRecorder(), in)
	backup := codes[0][1]
	signIn := func(code string) (*httptest.ResponseRecorder, *http.Request) {
		lw := httptest.NewRecorder()
		s.loginPost(lw, form("POST", "/login", url.Values{"login": {"Ann@Example.com"}, "password": {"correct horse battery"}, "next": {"/jams"}}, nil))
		if lw.Code != http.StatusSeeOther || lw.Header().Get("Location") != "/login/code" {
			t.Fatalf("login: %d %s", lw.Code, lw.Header().Get("Location"))
		}
		cr := cookies(lw, httptest.NewRequest("GET", "/login/code", nil))
		if _, err := a.Authenticate(cr); !errors.Is(err, errCodeDue) {
			t.Fatalf("code stage: %v", err)
		}
		pw := httptest.NewRecorder()
		s.page(station.ActionView, func(http.ResponseWriter, *http.Request, station.Principal) {}).ServeHTTP(pw, cr)
		if pw.Header().Get("Location") != "/login/code" {
			t.Errorf("a page before the code: %d %s", pw.Code, pw.Header().Get("Location"))
		}
		cpw := httptest.NewRecorder()
		s.codePage(cpw, cr)
		if cpw.Code != 200 || !strings.Contains(cpw.Body.String(), "One more step.") {
			t.Fatalf("code page: %d", cpw.Code)
		}
		csrf := a.sessionOf(cr).csrf
		w := httptest.NewRecorder()
		req := form("POST", "/login/code", url.Values{"csrf": {csrf}, "code": {code}}, lw)
		s.codePost(w, req)
		return w, req
	}
	w, _ = signIn(strings.ToUpper(backup))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/jams" {
		t.Fatalf("a backup code: %d %s", w.Code, w.Header().Get("Location"))
	}
	if p, err := a.Authenticate(cookies(w, httptest.NewRequest("GET", "/", nil))); err != nil || p.User != ann.ID {
		t.Fatalf("after the backup code: %+v %v", p, err)
	}
	if left, _ := s.db.BackupCodesLeft(ctx, ann.ID); left != backupCodeCount-1 {
		t.Errorf("%d codes left", left)
	}
	w, _ = signIn(backup)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "not right") {
		t.Errorf("a spent backup code: %d", w.Code)
	}
	// The code that confirmed the setup was spent with its step; the
	// next step's code is within the window and new.
	secret, last, _ := s.db.SecondFactor(ctx, ann.ID)
	if last != totpStep(time.Now()) && last != totpStep(time.Now())-1 {
		t.Errorf("the setup's step was not kept: %d", last)
	}
	spent, _ := totpCode(secret, last)
	w, _ = signIn(spent)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a code from a spent step: %d", w.Code)
	}
	code, _ = totpCode(secret, totpStep(time.Now())+1)
	w, _ = signIn(code)
	if w.Code != http.StatusSeeOther {
		t.Errorf("the authenticator's code: %d", w.Code)
	}
	// Locked: the session ends and the password no longer signs in.
	if err := s.setLocked(ctx, owner, ann.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(cookies(w, httptest.NewRequest("GET", "/", nil))); err == nil {
		t.Error("a locked user's session lives")
	}
	if _, ok := a.login(httptest.NewRecorder(), httptest.NewRequest("POST", "/login", nil), "ann@example.com", "correct horse battery", "/"); ok {
		t.Error("a locked user signed in")
	}
	s.setLocked(ctx, owner, ann.ID, false)
	// Reset: nothing of the sign-in is left but a new link.
	link2, err := s.resetSignIn(ctx, owner, ann.ID)
	if err != nil || !strings.HasPrefix(link2, "https://st.example/setup/") || link2 == link {
		t.Fatalf("reset: %q %v", link2, err)
	}
	if u, _ := s.db.User(ctx, ann.ID); u.HasPassword || u.TwoFactor {
		t.Errorf("after reset: %+v", u)
	}
	if _, ok := a.login(httptest.NewRecorder(), httptest.NewRequest("POST", "/login", nil), "ann@example.com", "correct horse battery", "/"); ok {
		t.Error("the old password signed in after a reset")
	}
}

// Five wrong codes end the attempt, the address is locked, and the
// login waits.
func TestWrongCodesEndTheAttempt(t *testing.T) {
	s, _ := signinServer(t)
	a, ctx := s.auth, context.Background()
	owner, _ := s.db.UserByLogin(ctx, station.OwnerLogin)
	s.db.SetSecondFactor(ctx, owner.ID, newTOTPSecret(), time.Now())
	lw := httptest.NewRecorder()
	st, ok := a.login(lw, httptest.NewRequest("POST", "/login", nil), "owner", "owner-secret-123", "/")
	if !ok || st != stageCode {
		t.Fatalf("login: %v %v", st, ok)
	}
	for i := 0; i < maxFailures; i++ {
		// As if the login's wait had passed: a try during it is refused
		// unseen and counts for nothing.
		a.mu.Lock()
		if m := a.logins["owner"]; m != nil {
			m.last = m.last.Add(-backoff(m.n))
		}
		a.mu.Unlock()
		r := cookies(lw, httptest.NewRequest("POST", "/login/code", nil))
		if ok, _, _ := a.code(httptest.NewRecorder(), r, "000000"); ok {
			t.Fatal("a wrong code passed")
		}
	}
	if a.sessionOf(cookies(lw, httptest.NewRequest("GET", "/", nil))) != nil {
		t.Error("the session outlived five wrong codes")
	}
	if _, ok := a.login(httptest.NewRecorder(), httptest.NewRequest("POST", "/login", nil), "owner", "owner-secret-123", "/"); ok {
		t.Error("the right password signed in while throttled")
	}
	// Another address waits too, by the login: five misses mean four
	// seconds.
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "198.51.100.7:4000"
	if _, ok := a.login(httptest.NewRecorder(), r, "owner", "owner-secret-123", "/"); ok {
		t.Error("the login was not slowed across addresses")
	}
	if w := a.loginWait("owner"); w <= 3*time.Second || w > 4*time.Second {
		t.Errorf("the login's wait is %v, want about 4 s", w)
	}
	if a.loginWait("nobody") != 0 {
		t.Error("an untouched login waits")
	}
}

// A login's wait doubles with each miss after the first two and is
// forgotten after a quiet lockout; a browser with the user's device
// cookie from an earlier sign-in is never held up by it, and is measured
// by its own cookie instead.
func TestLoginBackoffAndDeviceCookie(t *testing.T) {
	for _, c := range []struct {
		n    int
		want time.Duration
	}{{0, 0}, {1, 0}, {2, 0}, {3, time.Second}, {4, 2 * time.Second}, {6, 8 * time.Second}, {12, 512 * time.Second}, {13, lockout}, {40, lockout}} {
		if got := backoff(c.n); got != c.want {
			t.Errorf("backoff(%d) = %v, want %v", c.n, got, c.want)
		}
	}
	s, _ := signinServer(t)
	a, ctx := s.auth, context.Background()
	owner, _ := s.db.UserByLogin(ctx, station.OwnerLogin)
	secret := newTOTPSecret()
	s.db.SetSecondFactor(ctx, owner.ID, secret, time.Now())
	addr := 0
	from := func(method, target string) *http.Request {
		addr++
		r := httptest.NewRequest(method, target, nil)
		r.RemoteAddr = "203.0.113." + strconv.Itoa(addr) + ":5000"
		return r
	}
	// A full sign-in marks the browser as the owner's device.
	lw := httptest.NewRecorder()
	if _, ok := a.login(lw, from("POST", "/login"), "owner", "owner-secret-123", "/"); !ok {
		t.Fatal("login")
	}
	code, _ := totpCode(secret, totpStep(time.Now()))
	cw := httptest.NewRecorder()
	if ok, _, _ := a.code(cw, cookies(lw, from("POST", "/login/code")), code); !ok {
		t.Fatal("code")
	}
	var device *http.Cookie
	for _, c := range cw.Result().Cookies() {
		if c.Name == deviceCookie {
			device = c
		}
	}
	if device == nil || !device.HttpOnly || device.MaxAge != int(deviceLife/time.Second) {
		t.Fatalf("device cookie: %+v", device)
	}
	if nonce, ok := a.deviceOf(cookies(cw, from("GET", "/")), owner.ID); !ok || nonce == "" {
		t.Error("the device cookie is not the owner's")
	}
	if _, ok := a.deviceOf(cookies(cw, from("GET", "/")), "usr_other"); ok {
		t.Error("the device cookie vouched for another user")
	}
	forged := httptest.NewRequest("GET", "/", nil)
	forged.AddCookie(&http.Cookie{Name: deviceCookie, Value: owner.ID + ".nonce.0000"})
	if _, ok := a.deviceOf(forged, owner.ID); ok {
		t.Error("a forged device cookie passed")
	}
	// Three misses from three addresses: the login now waits a second,
	// and the right password from a fourth address is refused unseen.
	for i := 0; i < 3; i++ {
		if _, ok := a.login(httptest.NewRecorder(), from("POST", "/login"), "Owner", "wrong", "/"); ok {
			t.Fatal("a wrong password passed")
		}
	}
	if w := a.loginWait("owner"); w <= 0 || w > time.Second {
		t.Fatalf("wait after three misses: %v", w)
	}
	if _, ok := a.login(httptest.NewRecorder(), from("POST", "/login"), "owner", "owner-secret-123", "/"); ok {
		t.Error("the right password passed during the wait")
	}
	// The owner's own browser is not held up, and its sign-in forgets
	// the misses.
	r := from("POST", "/login")
	r.AddCookie(device)
	if st, ok := a.login(httptest.NewRecorder(), r, "owner", "owner-secret-123", "/"); !ok || st != stageCode {
		t.Errorf("the device cookie did not vouch: %v %v", st, ok)
	}
	if a.loginWait("owner") != 0 {
		t.Error("a sign-in did not clear the login's misses")
	}
	// The device cookie is measured on its own: five misses with it from
	// five addresses lock that cookie, not the login.
	for i := 0; i < maxFailures; i++ {
		r := from("POST", "/login")
		r.AddCookie(device)
		a.login(httptest.NewRecorder(), r, "owner", "wrong", "/")
	}
	r = from("POST", "/login")
	r.AddCookie(device)
	if _, ok := a.login(httptest.NewRecorder(), r, "owner", "owner-secret-123", "/"); ok {
		t.Error("a locked device cookie still signed in")
	}
	if a.loginWait("owner") != 0 {
		t.Error("misses with a device cookie counted against the login")
	}
	// Misses are forgotten after a quiet lockout.
	a.loginMiss("ann@example.com")
	a.loginMiss("ann@example.com")
	a.loginMiss("ann@example.com")
	a.mu.Lock()
	a.logins["ann@example.com"].last = time.Now().Add(-lockout - time.Second)
	a.mu.Unlock()
	if a.loginWait("ann@example.com") != 0 || a.logins["ann@example.com"] != nil {
		t.Error("old misses were kept")
	}
}

// A user with a password but no authenticator, the owner from before,
// is sent to set one up after the password, and can keep the old one
// when replacing it later.
func TestPasswordWithoutAuthenticatorSetsOneUp(t *testing.T) {
	s, _ := signinServer(t)
	a, ctx := s.auth, context.Background()
	lw := httptest.NewRecorder()
	st, ok := a.login(lw, httptest.NewRequest("POST", "/login", nil), "owner", "owner-secret-123", "/settings")
	if !ok || st != stageSetup {
		t.Fatalf("login: %v %v", st, ok)
	}
	r := cookies(lw, httptest.NewRequest("GET", "/settings", nil))
	if _, err := a.Authenticate(r); !errors.Is(err, errSetupDue) {
		t.Fatalf("stage: %v", err)
	}
	lp := httptest.NewRecorder()
	s.loginPage(lp, r)
	if lp.Header().Get("Location") != "/account/setup" {
		t.Errorf("the sign-in page should send a setup-stage session on: %s", lp.Header().Get("Location"))
	}
	if a.cancelSetup(httptest.NewRecorder(), r) {
		t.Error("a user with no authenticator cancelled the setup")
	}
	sess := a.sessionOf(r)
	a.setupSecret(sess)
	code, _ := totpCode(sess.secret, totpStep(time.Now()))
	w := httptest.NewRecorder()
	codes, ok := a.confirmSetup(w, r, code)
	if !ok || len(codes) != backupCodeCount {
		t.Fatalf("confirm: %v %d", ok, len(codes))
	}
	in := cookies(w, httptest.NewRequest("GET", "/settings", nil))
	p, err := a.Authenticate(in)
	if err != nil || !p.Admin {
		t.Fatalf("signed in: %+v %v", p, err)
	}
	// Replacing: the old authenticator stands until the new one confirms.
	owner, _ := s.db.UserByLogin(ctx, station.OwnerLogin)
	oldSecret, _, _ := s.db.SecondFactor(ctx, owner.ID)
	aw := httptest.NewRecorder()
	s.accountAuthenticator(aw, form("POST", "/account/authenticator", url.Values{"csrf": {a.sessionOf(in).csrf}, "current": {"wrong"}}, w), p)
	if aw.Code != 200 || !strings.Contains(aw.Body.String(), "not your current password") {
		t.Errorf("a wrong current password: %d", aw.Code)
	}
	aw = httptest.NewRecorder()
	s.accountAuthenticator(aw, form("POST", "/account/authenticator", url.Values{"csrf": {a.sessionOf(in).csrf}, "current": {"owner-secret-123"}}, w), p)
	if aw.Header().Get("Location") != "/account/setup" {
		t.Fatalf("replace: %d %s", aw.Code, aw.Header().Get("Location"))
	}
	r2 := cookies(aw, httptest.NewRequest("GET", "/account/setup", nil))
	if _, err := a.Authenticate(r2); !errors.Is(err, errSetupDue) {
		t.Fatalf("replacing stage: %v", err)
	}
	sp := httptest.NewRecorder()
	s.accountSetupPage(sp, r2)
	if !strings.Contains(sp.Body.String(), "A new authenticator.") || !strings.Contains(sp.Body.String(), "Keep the old one") {
		t.Error("the replacing page should offer to keep the old one")
	}
	cw := httptest.NewRecorder()
	if !a.cancelSetup(cw, r2) {
		t.Fatal("cancel")
	}
	if now, _, _ := s.db.SecondFactor(ctx, owner.ID); now != oldSecret {
		t.Error("cancelling changed the authenticator")
	}
	if p, err := a.Authenticate(cookies(cw, httptest.NewRequest("GET", "/", nil))); err != nil || !p.Admin {
		t.Errorf("after cancel: %+v %v", p, err)
	}
}

// New backup codes replace the old set, and need the current password.
func TestNewBackupCodes(t *testing.T) {
	s, owner := signinServer(t)
	ctx := context.Background()
	u, _ := s.db.UserByLogin(ctx, station.OwnerLogin)
	s.db.SetSecondFactor(ctx, u.ID, newTOTPSecret(), time.Now())
	first, _ := s.auth.newBackupCodes(ctx, u.ID)
	w := httptest.NewRecorder()
	s.accountCodes(w, form("POST", "/account/codes", url.Values{"current": {"owner-secret-123"}}, nil), owner)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "the old set no longer works") {
		t.Fatalf("codes: %d %.200s", w.Code, w.Body.String())
	}
	if _, ok, _ := s.db.UseBackupCode(ctx, u.ID, hashCode(first[0])); ok {
		t.Error("an old backup code still works")
	}
	if left, _ := s.db.BackupCodesLeft(ctx, u.ID); left != backupCodeCount {
		t.Errorf("%d left", left)
	}
	w = httptest.NewRecorder()
	s.accountCodes(w, form("POST", "/account/codes", url.Values{"current": {"nope"}}, nil), owner)
	if !strings.Contains(w.Body.String(), "not your current password") {
		t.Error("new codes without the password")
	}
	// Changing the password needs the current one and the rules.
	w = httptest.NewRecorder()
	s.changePassword(w, form("POST", "/account/password", url.Values{"current": {"owner-secret-123"}, "password": {"short"}, "again": {"short"}}, nil), owner)
	if !strings.Contains(w.Body.String(), "at least 12 characters") {
		t.Error("a short new password passed")
	}
	w = httptest.NewRecorder()
	s.changePassword(w, form("POST", "/account/password", url.Values{"current": {"owner-secret-123"}, "password": {"a much better passphrase"}, "again": {"a much better passphrase"}}, nil), owner)
	if !strings.Contains(w.Body.String(), "Password changed.") {
		t.Error("the change did not take")
	}
	if hash, _ := s.db.PasswordHash(ctx, u.ID); !checkPassword(hash, "a much better passphrase") {
		t.Error("the new password is not kept")
	}
}
