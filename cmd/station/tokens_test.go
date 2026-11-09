package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/VibiumDev/station"
)

// A token is never above its user: nothing for a user given no device,
// watch for a watcher, drive for a driver, everything for an admin; and
// people revoke only their own.
func TestTokenCeilings(t *testing.T) {
	s, owner := signinServer(t)
	ctx := context.Background()
	ann, _, _ := s.addUser(ctx, owner, "ann@example.com", "Ann", false)
	team, _ := s.addTeam(ctx, owner, "QA")
	s.setMember(ctx, owner, team.ID, ann.ID, true)
	annU, _ := s.db.User(ctx, ann.ID)
	if c := s.tokenCeiling(ctx, annU); c != "" {
		t.Errorf("ceiling with no access: %q", c)
	}
	if _, err := s.makeToken(ctx, owner, annU, "eyes", "watch"); err == nil || !strings.Contains(err.Error(), "no device access") {
		t.Errorf("a token for a user with nothing: %v", err)
	}
	s.setAccess(ctx, owner, "rig_a/phone", team.ID, "watch")
	if c := s.tokenCeiling(ctx, annU); c != station.TokenWatch {
		t.Errorf("ceiling at watch: %q", c)
	}
	if _, err := s.makeToken(ctx, owner, annU, "eyes", "watch"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.makeToken(ctx, owner, annU, "hands", "drive"); err == nil || !strings.Contains(err.Error(), "up to watch") {
		t.Errorf("a drive token for a watcher: %v", err)
	}
	s.setAccess(ctx, owner, "rig_b/phone", team.ID, "drive")
	if c := s.tokenCeiling(ctx, annU); c != station.TokenDrive {
		t.Errorf("ceiling at drive: %q", c)
	}
	if _, err := s.makeToken(ctx, owner, annU, "hands", "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.makeToken(ctx, owner, annU, "boss", "admin"); err == nil || !strings.Contains(err.Error(), "up to drive") {
		t.Errorf("an admin token for a regular user: %v", err)
	}
	if _, err := s.makeToken(ctx, owner, annU, "", "drive"); err == nil {
		t.Error("a nameless token")
	}
	if _, err := s.makeToken(ctx, owner, annU, "odd", "boss"); err == nil {
		t.Error("a made-up level")
	}
	ownerU, _ := s.db.User(ctx, owner.User)
	if c := s.tokenCeiling(ctx, ownerU); c != station.TokenAdmin {
		t.Errorf("an admin's ceiling: %q", c)
	}
	if _, err := s.makeToken(ctx, owner, ownerU, "root", "admin"); err != nil {
		t.Fatal(err)
	}
	if got := tokenLevels(station.TokenDrive); strings.Join(got, ",") != "watch,drive" {
		t.Errorf("levels up to drive: %v", got)
	}
	if got := tokenLevels(""); got != nil {
		t.Errorf("levels up to nothing: %v", got)
	}
	names := func(u station.User) string {
		var out []string
		for _, t := range s.ownTokens(ctx, u) {
			out = append(out, t.Name+":"+string(t.Level))
		}
		return strings.Join(out, " ")
	}
	if names(annU) != "eyes:watch hands:drive" || names(ownerU) != "root:admin" {
		t.Errorf("own tokens: ann %q, owner %q", names(annU), names(ownerU))
	}
	// Revoking: only one's own.
	annP := principalOf(annU, "")
	w := httptest.NewRecorder()
	s.accountTokenRevoke(w, form("POST", "/account/tokens/revoke", url.Values{"name": {"root"}}, nil), annP)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "not yours") || names(ownerU) != "root:admin" {
		t.Errorf("revoking another's token: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.accountTokenRevoke(w, form("POST", "/account/tokens/revoke", url.Values{"name": {"eyes"}}, nil), annP)
	if w.Code != http.StatusSeeOther || names(annU) != "hands:drive" {
		t.Errorf("revoking one's own: %d, left %q", w.Code, names(annU))
	}
	// Making one's own on the Account page, within the ceiling.
	w = httptest.NewRecorder()
	s.accountTokens(w, form("POST", "/account/tokens", url.Values{"name": {"more"}, "level": {"admin"}}, nil), annP)
	if !strings.Contains(w.Body.String(), "up to drive") {
		t.Error("the Account page let a regular user make an admin token")
	}
	w = httptest.NewRecorder()
	s.accountTokens(w, form("POST", "/account/tokens", url.Values{"name": {"more"}, "level": {"watch"}}, nil), annP)
	body := w.Body.String()
	if !strings.Contains(body, "shown once") || !strings.Contains(body, "stk_") || !strings.Contains(body, `<option value="drive" selected>`) || strings.Contains(body, `<option value="admin"`) {
		t.Errorf("the Account page after making a token: %.300s", body)
	}
}
