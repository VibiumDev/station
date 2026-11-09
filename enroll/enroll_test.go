package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
	"github.com/VibiumDev/station/token"
)

// The enrollment handler's own checks: the request must be a signed
// enrollment event naming this station, with a code that is open, and
// the code is spent by its first use.
func TestServeEnrollChecks(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	key, rigKey := nostr.Generate(), nostr.Generate()
	h := &Handler{Key: key, Name: "test", TunnelURL: "wss://st.example/v1/tunnel", Devices: db, Events: db, Codes: db, Audit: db}
	offers, _ := json.Marshal([]Offer{{Name: "phone", Kind: "phone", Caps: []string{"video"}}})
	code := func(life time.Duration) string {
		c := NewCode(key.Public())
		normalized, _, err := ParseCode(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveCode(ctx, normalized, time.Now().Add(life)); err != nil {
			t.Fatal(err)
		}
		return c
	}
	event := func(sk nostr.SecretKey, station string, code string, mutate func(*nostr.Event)) nostr.Event {
		ev := nostr.Event{Kind: token.KindEnrollment, CreatedAt: nostr.Now(),
			Tags: nostr.Tags{{"p", station}, {"code", code}, {"name", "pi"}}, Content: string(offers)}
		if err := ev.Sign(sk); err != nil {
			t.Fatal(err)
		}
		if mutate != nil {
			mutate(&ev)
		}
		return ev
	}
	post := func(body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/enroll", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeEnroll(w, r)
		return w
	}
	me := key.Public().Hex()
	w := httptest.NewRecorder()
	h.ServeEnroll(w, httptest.NewRequest("GET", "/v1/enroll", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
	cases := []struct {
		name string
		body any
		code int
		want string
	}{
		{"not json", "x", http.StatusBadRequest, "bad JSON"},
		{"another kind", map[string]any{"event": func() nostr.Event {
			ev := event(rigKey, me, code(time.Hour), nil)
			ev.Kind = token.KindEnrollment + 1
			ev.Sign(rigKey)
			return ev
		}()}, http.StatusBadRequest, "not an enrollment"},
		{"tampered after signing", map[string]any{"event": event(rigKey, me, code(time.Hour), func(ev *nostr.Event) { ev.Content = "[]" })}, http.StatusBadRequest, "bad signature"},
		{"another station", map[string]any{"event": event(rigKey, nostr.Generate().Public().Hex(), code(time.Hour), nil)}, http.StatusBadRequest, "another station"},
		{"no code", map[string]any{"event": func() nostr.Event {
			ev := nostr.Event{Kind: token.KindEnrollment, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", me}, {"name", "pi"}}, Content: string(offers)}
			ev.Sign(rigKey)
			return ev
		}()}, http.StatusBadRequest, "code and name"},
		{"made-up code", map[string]any{"event": event(rigKey, me, NewCode(key.Public()), nil)}, http.StatusForbidden, ""},
		{"expired code", map[string]any{"event": event(rigKey, me, code(-time.Second), nil)}, http.StatusForbidden, ""},
		{"a device named with a slash", map[string]any{"event": func() nostr.Event {
			bad, _ := json.Marshal([]Offer{{Name: "a/b", Kind: "phone"}})
			ev := nostr.Event{Kind: token.KindEnrollment, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", me}, {"code", code(time.Hour)}, {"name", "pi"}}, Content: string(bad)}
			ev.Sign(rigKey)
			return ev
		}()}, http.StatusBadRequest, "without a slash"},
	}
	for _, c := range cases {
		w := post(c.body)
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s (want %d %q)", c.name, w.Code, strings.TrimSpace(w.Body.String()), c.code, c.want)
		}
	}
	if rigs, _ := db.Rigs(ctx); len(rigs) != 0 {
		t.Fatalf("a refused request enrolled a rig: %+v", rigs)
	}
	// A good request enrolls; the same code again is spent; the same key
	// with a fresh code keeps its rig id.
	good := code(time.Hour)
	w = post(map[string]any{"event": event(rigKey, me, good, nil)})
	if w.Code != 200 {
		t.Fatalf("enroll: %d %s", w.Code, w.Body)
	}
	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	rig, err := db.RigByKey(ctx, rigKey.Public())
	if err != nil || rig.ID != resp.RigID || len(rig.Devices) != 1 || rig.Devices[0].ID != rig.ID+"/phone" || resp.Station != me {
		t.Fatalf("enrolled rig: %+v %v, response %+v", rig, err, resp)
	}
	if w := post(map[string]any{"event": event(rigKey, me, good, nil)}); w.Code != http.StatusForbidden {
		t.Errorf("a spent code: %d", w.Code)
	}
	if w := post(map[string]any{"event": event(rigKey, me, code(time.Hour), nil)}); w.Code != 200 {
		t.Errorf("enrolling again: %d", w.Code)
	}
	if again, _ := db.RigByKey(ctx, rigKey.Public()); again.ID != rig.ID {
		t.Errorf("re-enrolling changed the rig id: %s -> %s", rig.ID, again.ID)
	}
	// Another key with a code of its own is another rig.
	other := nostr.Generate()
	if w := post(map[string]any{"event": event(other, me, code(time.Hour), nil)}); w.Code != 200 {
		t.Errorf("a second rig: %d", w.Code)
	}
	if rigs, _ := db.Rigs(ctx); len(rigs) != 2 {
		t.Errorf("%d rigs", len(rigs))
	}
	_ = station.NewID
}

// Codes carry the station's fingerprint and read the same however typed.
func TestCodes(t *testing.T) {
	key := nostr.Generate()
	c := NewCode(key.Public())
	n1, fp, err := ParseCode(c)
	if err != nil || fp != Fingerprint(key.Public()) {
		t.Fatalf("parse: %q %q %v", n1, fp, err)
	}
	n2, _, _ := ParseCode(strings.ToLower(strings.ReplaceAll(c, "-", " ")))
	if n1 != n2 {
		t.Errorf("typed differently: %q vs %q", n1, n2)
	}
	if _, _, err := ParseCode("nope"); err == nil {
		t.Error("nonsense parsed")
	}
	if NewCode(key.Public()) == c {
		t.Error("two codes alike")
	}
}
