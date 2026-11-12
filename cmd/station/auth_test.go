package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
)

// A token minted for a jam signs in as the jam, bound to its device: it
// may act on that device alone, and not at all once it has expired.
func TestJamTokenIsBoundToItsDevice(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.SeedOwner(ctx, "hash"); err != nil { // tokens without an owner of their own are the owner's
		t.Fatal(err)
	}
	secret, err := db.CreateScopedToken(ctx, "jam:jam_1", station.TokenDrive, "rig_a/phone", time.Now().Add(time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	a := newAuth(&Config{}, db, nil, false, false)
	r := httptest.NewRequest("GET", "/session", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	p, err := a.Authenticate(r)
	if err != nil || p.Subject != "token:jam:jam_1" || p.Device != "rig_a/phone" || p.ReadOnly {
		t.Fatalf("principal: %+v, %v", p, err)
	}
	if err := a.Authorize(ctx, p, station.ActionControl, "rig_a/phone"); err != nil {
		t.Errorf("its own device: %v", err)
	}
	if err := a.Authorize(ctx, p, station.ActionControl, "rig_a/tablet"); err == nil {
		t.Error("another device was allowed")
	}
	if err := a.Authorize(ctx, p, station.ActionView, ""); err != nil {
		t.Errorf("a call about no device in particular: %v", err)
	}
	if err := a.Authorize(ctx, p, station.ActionManage, ""); err == nil {
		t.Error("a jam's token may not manage")
	}
	old, _ := db.CreateScopedToken(ctx, "jam:jam_0", station.TokenDrive, "rig_a/phone", time.Now().Add(-time.Second), "")
	r.Header.Set("Authorization", "Bearer "+old)
	if _, err := a.Authenticate(r); err == nil {
		t.Error("an expired token signed in")
	}
}

// People sign in with a login and a password; an admin does everything,
// and a regular user does with a device what their teams' level allows.
func TestLoginAndLevels(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	ownerHash, _ := hashPassword("owner-secret")
	if _, err := db.SeedOwner(ctx, ownerHash); err != nil {
		t.Fatal(err)
	}
	ann := station.User{ID: "usr_ann", Login: "ann@example.com", Name: "Ann"}
	if err := db.PutUser(ctx, ann); err != nil {
		t.Fatal(err)
	}
	annHash, _ := hashPassword("ann-secret")
	db.SetPassword(ctx, ann.ID, annHash)
	db.PutTeam(ctx, station.Team{ID: "team_qa", Name: "QA"})
	db.SetMember(ctx, "team_qa", ann.ID, true)
	db.SetAccess(ctx, station.Access{DeviceID: "rig_a/phone", TeamID: "team_qa", Level: station.LevelDrive})
	a := newAuth(&Config{}, db, nil, false, false)
	a.delay = 0
	// Both have authenticators, so a sign-in is the password and a code.
	ownerUser, _ := db.UserByLogin(ctx, station.OwnerLogin)
	secrets := map[string]string{"owner": newTOTPSecret(), "ann@example.com": newTOTPSecret()}
	db.SetSecondFactor(ctx, ownerUser.ID, secrets["owner"], time.Now())
	db.SetSecondFactor(ctx, ann.ID, secrets["ann@example.com"], time.Now())

	signIn := func(login, password string) (station.Principal, bool) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/login", nil)
		st, ok := a.login(w, r, login, password, "/")
		if !ok {
			return station.Principal{}, false
		}
		if st != stageCode {
			t.Fatalf("%s: stage %v, want the code step", login, st)
		}
		r = httptest.NewRequest("POST", "/login/code", nil)
		for _, c := range w.Result().Cookies() {
			r.AddCookie(c)
		}
		if _, err := a.Authenticate(r); !errors.Is(err, errCodeDue) {
			t.Fatalf("%s before the code: %v", login, err)
		}
		code, _ := totpCode(secrets[strings.ToLower(login)], totpStep(time.Now()))
		w = httptest.NewRecorder()
		if ok, _, _ := a.code(w, r, code); !ok {
			return station.Principal{}, false
		}
		next := httptest.NewRequest("GET", "/", nil)
		for _, c := range w.Result().Cookies() {
			next.AddCookie(c)
		}
		p, err := a.Authenticate(next)
		return p, err == nil
	}
	if _, ok := signIn("ann@example.com", "wrong"); ok {
		t.Fatal("a wrong password signed in")
	}
	if _, ok := signIn("nobody@example.com", "ann-secret"); ok {
		t.Fatal("an unknown login signed in")
	}
	owner, ok := signIn("Owner", "owner-secret")
	if !ok || !owner.Admin || owner.Subject != "owner" || owner.User == "" {
		t.Fatalf("owner: %+v %v", owner, ok)
	}
	p, ok := signIn("ANN@example.com", "ann-secret")
	if !ok || p.Admin || p.Subject != "ann@example.com" || p.User != "usr_ann" || len(p.Teams) != 1 {
		t.Fatalf("ann: %+v %v", p, ok)
	}
	cases := []struct {
		who    station.Principal
		action station.Action
		device string
		ok     bool
	}{
		{p, station.ActionView, "rig_a/phone", true},
		{p, station.ActionControl, "rig_a/phone", true},
		{p, station.ActionBidi, "rig_a/phone", false},
		{p, station.ActionView, "rig_b/phone", false},
		{p, station.ActionControl, "rig_b/phone", false},
		{p, station.ActionView, "", true},
		{p, station.ActionManage, "", false},
		{p, station.ActionAdmin, "rig_a/phone", false},
		{owner, station.ActionBidi, "rig_b/phone", true},
		{owner, station.ActionManage, "", true},
		{owner, station.ActionAdmin, "rig_b/phone", true},
	}
	for _, c := range cases {
		err := a.Authorize(ctx, c.who, c.action, c.device)
		if (err == nil) != c.ok {
			t.Errorf("%s %s on %q: %v, want ok=%v", c.who.Subject, c.action, c.device, err, c.ok)
		}
	}
	if a.level(ctx, p, "rig_a/phone") != station.LevelDrive || a.level(ctx, owner, "rig_z/phone") != station.LevelJam {
		t.Error("levels")
	}
	// A token acts as its user within its level, and dies with a locked
	// user: a drive token drives the user's devices and never manages, a
	// watch token only looks, and an admin token manages only when its
	// user is an admin.
	secret, _ := db.CreateToken(ctx, "anns", station.TokenDrive, ann.ID)
	r := httptest.NewRequest("GET", "/v1/devices", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	tp, err := a.Authenticate(r)
	if err != nil || tp.Subject != "token:anns" || tp.User != ann.ID || len(tp.Teams) != 1 || tp.Admin {
		t.Fatalf("token principal: %+v %v", tp, err)
	}
	if a.Authorize(ctx, tp, station.ActionControl, "rig_a/phone") != nil || a.Authorize(ctx, tp, station.ActionManage, "") == nil {
		t.Error("a token should drive its user's devices and never manage")
	}
	ownerSecret, _ := db.CreateToken(ctx, "owners", station.TokenDrive, "")
	r.Header.Set("Authorization", "Bearer "+ownerSecret)
	if op, err := a.Authenticate(r); err != nil || !op.Admin || op.Token != station.TokenDrive || a.Authorize(ctx, op, station.ActionManage, "") == nil || a.Authorize(ctx, op, station.ActionAdmin, "rig_b/phone") == nil {
		t.Errorf("an owner's drive token sees everything but manages nothing: %+v %v", op, err)
	}
	watchSecret, _ := db.CreateToken(ctx, "anns-eyes", station.TokenWatch, ann.ID)
	r.Header.Set("Authorization", "Bearer "+watchSecret)
	if wp, err := a.Authenticate(r); err != nil || !wp.ReadOnly || wp.Token != station.TokenWatch || a.Authorize(ctx, wp, station.ActionView, "rig_a/phone") != nil || a.Authorize(ctx, wp, station.ActionControl, "rig_a/phone") == nil {
		t.Errorf("a watch token looks and never controls: %+v %v", wp, err)
	}
	annAdmin, _ := db.CreateToken(ctx, "anns-admin", station.TokenAdmin, ann.ID)
	r.Header.Set("Authorization", "Bearer "+annAdmin)
	if ap, err := a.Authenticate(r); err != nil || ap.Admin || a.Authorize(ctx, ap, station.ActionManage, "") == nil {
		t.Errorf("a regular user's admin token manages nothing: %+v %v", ap, err)
	}
	ownerAdmin, _ := db.CreateToken(ctx, "owners-admin", station.TokenAdmin, "")
	r.Header.Set("Authorization", "Bearer "+ownerAdmin)
	if ap, err := a.Authenticate(r); err != nil || !ap.Admin || a.Authorize(ctx, ap, station.ActionManage, "") != nil || a.Authorize(ctx, ap, station.ActionAdmin, "rig_b/phone") != nil {
		t.Errorf("an admin's admin token manages: %+v %v", ap, err)
	}
	if _, err := station.ParseTokenLevel("boss"); err == nil {
		t.Error("a made-up level parsed")
	}
	if l, _ := station.ParseTokenLevel(""); l != station.TokenDrive {
		t.Error("the default level is drive")
	}
	ann.Locked = true
	db.PutUser(ctx, ann)
	r.Header.Set("Authorization", "Bearer "+secret)
	if _, err := a.Authenticate(r); err == nil {
		t.Error("a locked user's token signed in")
	}
	if _, ok := signIn("ann@example.com", "ann-secret"); ok {
		t.Error("a locked user signed in")
	}
}

// A write that stands on a tailnet identity must come from the station's
// own pages: the identity rides along whichever site's page sent it. A
// token is in a header another site cannot set.
func TestIdentityWritesAreSameOrigin(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	who := func(ctx context.Context, remoteAddr string) (string, string, []string, error) {
		return "root@example.com", "Root", nil, nil
	}
	a := newAuth(&Config{Allow: []string{"root@example.com"}}, db, who, false, false)
	for _, c := range []struct {
		name   string
		method string
		header [2]string
		bearer bool
		ok     bool
	}{
		{"a client that is no browser", "POST", [2]string{}, false, true},
		{"the station's own page", "POST", [2]string{"Origin", "https://example.com"}, false, true},
		{"its own page, by the browser's word", "POST", [2]string{"Sec-Fetch-Site", "same-origin"}, false, true},
		{"another site's form", "POST", [2]string{"Origin", "https://evil.example"}, false, false},
		{"another site, by the browser's word", "POST", [2]string{"Sec-Fetch-Site", "cross-site"}, false, false},
		{"another host on the same site", "DELETE", [2]string{"Sec-Fetch-Site", "same-site"}, false, false},
		{"a token from anywhere", "POST", [2]string{"Origin", "https://evil.example"}, true, true},
		{"a read from anywhere", "GET", [2]string{"Sec-Fetch-Site", "cross-site"}, false, true},
	} {
		r := httptest.NewRequest(c.method, "https://example.com/jams", nil)
		if c.header[0] != "" {
			r.Header.Set(c.header[0], c.header[1])
		}
		if c.bearer {
			r.Header.Set("Authorization", "Bearer anything")
		}
		if got := a.csrfOK(r); got != c.ok {
			t.Errorf("%s: allowed %v, want %v", c.name, got, c.ok)
		}
	}
}

// A tailnet identity on --allow is an admin and becomes a user; one an
// admin added signs in as that user; a stranger is nobody.
func TestTailnetIdentitiesAreUsers(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	db.PutUser(ctx, station.User{ID: "usr_bob", Login: "bob@example.com"})
	db.PutUser(ctx, station.User{ID: "usr_cat", Login: "cat@example.com", Name: "Catherine"})
	who := func(ctx context.Context, remoteAddr string) (string, string, []string, error) {
		switch remoteAddr {
		case "100.64.0.1:1":
			return "root@example.com", "Root", nil, nil
		case "100.64.0.2:1":
			return "bob@example.com", "Bob Example", nil, nil
		case "100.64.0.4:1":
			return "cat@example.com", "Cat", nil, nil
		}
		return "stranger@example.com", "Stranger", nil, nil
	}
	a := newAuth(&Config{Allow: []string{"root@example.com"}}, db, who, false, false)
	req := func(addr string) station.Principal {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = addr
		p, _ := a.Authenticate(r)
		return p
	}
	if p := req("100.64.0.1:1"); !p.Admin || p.User == "" {
		t.Errorf("allowed identity: %+v", p)
	}
	if u, err := db.UserByLogin(ctx, "root@example.com"); err != nil || !u.Admin || u.Name != "Root" {
		t.Errorf("the allowed identity should be a user named by the tailnet: %+v %v", u, err)
	}
	if p := req("100.64.0.2:1"); p.Admin || p.User != "usr_bob" {
		t.Errorf("known identity: %+v", p)
	}
	if u, _ := db.User(ctx, "usr_bob"); u.Name != "Bob Example" {
		t.Errorf("a user with no name should take the tailnet's: %q", u.Name)
	}
	if p := req("100.64.0.4:1"); p.User != "usr_cat" {
		t.Errorf("cat: %+v", p)
	}
	if u, _ := db.User(ctx, "usr_cat"); u.Name != "Catherine" {
		t.Errorf("a name an admin set should stay: %q", u.Name)
	}
	if _, err := db.UserByLogin(ctx, "stranger@example.com"); err == nil {
		t.Error("a stranger became a user")
	}
	if p := req("100.64.0.3:1"); p.User != "" {
		t.Errorf("a stranger signed in: %+v", p)
	}
}
