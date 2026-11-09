package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
)

// --- API tokens are made by people, for themselves on their Account
// page or for anyone by an admin on Settings, and never above what the
// token's user may do: admin for an admin, drive for a user with drive or
// jam on some device, watch for one who may only watch.

// tokenCeiling is the highest level a user's tokens may have; "" for a
// user given no device yet, whose token could do nothing.
func (s *server) tokenCeiling(ctx context.Context, u station.User) station.TokenLevel {
	if u.Admin {
		return station.TokenAdmin
	}
	best, err := s.db.BestLevel(ctx, u.Teams)
	switch {
	case err != nil:
		return ""
	case best.AtLeast(station.LevelDrive):
		return station.TokenDrive
	case best.AtLeast(station.LevelWatch):
		return station.TokenWatch
	}
	return ""
}

func tokenRank(l station.TokenLevel) int {
	switch l {
	case station.TokenWatch:
		return 1
	case station.TokenDrive:
		return 2
	case station.TokenAdmin:
		return 3
	}
	return 0
}

// tokenLevels are the levels up to a ceiling, as a form offers them.
func tokenLevels(ceiling station.TokenLevel) []string {
	var out []string
	for _, l := range []station.TokenLevel{station.TokenWatch, station.TokenDrive, station.TokenAdmin} {
		if tokenRank(l) <= tokenRank(ceiling) {
			out = append(out, string(l))
		}
	}
	return out
}

// makeToken makes a named token for a user at a level no higher than the
// user's ceiling, and returns its secret.
func (s *server) makeToken(ctx context.Context, p station.Principal, u station.User, name, levelName string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return "", errors.New("a token needs a name of up to 60 characters")
	}
	level, err := station.ParseTokenLevel(levelName)
	if err != nil {
		return "", err
	}
	ceiling := s.tokenCeiling(ctx, u)
	if ceiling == "" {
		return "", fmt.Errorf("%s has no device access yet, so no token of theirs could do anything", u.Login)
	}
	if tokenRank(level) > tokenRank(ceiling) {
		return "", fmt.Errorf("%s may have tokens up to %s", u.Login, ceiling)
	}
	secret, err := s.db.CreateToken(ctx, name, level, u.ID)
	if err != nil {
		return "", err
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "token", Detail: "created " + name + " (" + string(level) + ") for " + u.Login})
	return secret, nil
}

// ownTokens are the named tokens that act as a user, the owner's older
// ones among them; a jam's short-lived token is not listed.
func (s *server) ownTokens(ctx context.Context, u station.User) []sqlite.APIToken {
	all, _ := s.db.Tokens(ctx)
	now := time.Now()
	var out []sqlite.APIToken
	for _, t := range all {
		mine := t.UserID == u.ID || t.UserID == "" && u.Login == station.OwnerLogin
		if mine && t.Device == "" && !t.Expired(now) {
			out = append(out, t)
		}
	}
	return out
}

// accountTokens is POST /account/tokens: a token for oneself.
func (s *server) accountTokens(w http.ResponseWriter, r *http.Request, p station.Principal) {
	ctx := r.Context()
	u, err := s.db.User(ctx, p.User)
	if err != nil {
		s.render(w, "account", s.accountData(r, p, map[string]any{"Message": "Only a signed-in person makes tokens."}))
		return
	}
	secret, err := s.makeToken(ctx, p, u, r.FormValue("name"), r.FormValue("level"))
	if err != nil {
		s.render(w, "account", s.accountData(r, p, map[string]any{"Message": capitalize(err.Error()) + "."}))
		return
	}
	s.render(w, "account", s.accountData(r, p, map[string]any{"NewToken": secret, "NewTokenName": strings.TrimSpace(r.FormValue("name"))}))
}

// accountTokenRevoke is POST /account/tokens/revoke: one of one's own.
func (s *server) accountTokenRevoke(w http.ResponseWriter, r *http.Request, p station.Principal) {
	ctx, name := r.Context(), r.FormValue("name")
	u, _ := s.db.User(ctx, p.User)
	for _, t := range s.ownTokens(ctx, u) {
		if t.Name == name {
			if err := s.db.RevokeToken(ctx, name); err != nil {
				s.render(w, "account", s.accountData(r, p, map[string]any{"Message": err.Error()}))
				return
			}
			s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "token", Detail: "revoked " + name})
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
	}
	s.render(w, "account", s.accountData(r, p, map[string]any{"Message": "That token is not yours."}))
}
