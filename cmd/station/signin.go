package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/VibiumDev/station"
)

// --- Signing in: the password, then a code; setup links for new users
// and reset ones; the Account page's own password, backup codes and
// authenticator. These pages sit outside page(), since a session on its
// way in is nobody yet, so each checks the session and the CSRF token
// itself.

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

// signinData is what the pages outside the sidebar share.
func (s *server) signinData(r *http.Request, extra map[string]any) map[string]any {
	data := map[string]any{"CSRF": s.auth.csrfToken(r)}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	switch _, err := s.auth.Authenticate(r); {
	case err == nil:
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	case errors.Is(err, errCodeDue):
		http.Redirect(w, r, "/login/code", http.StatusSeeOther)
		return
	case errors.Is(err, errSetupDue):
		http.Redirect(w, r, "/account/setup", http.StatusSeeOther)
		return
	}
	s.render(w, "login", s.signinData(r, map[string]any{"Next": safeNext(r.FormValue("next")), "NoUsers": !s.auth.hasUsers(r.Context())}))
}

func (s *server) loginPost(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSpace(r.FormValue("login"))
	next := safeNext(r.FormValue("next"))
	st, ok := s.auth.login(w, r, login, r.FormValue("password"), next)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login", s.signinData(r, map[string]any{"Next": next, "Login": login, "Error": "That login and password do not match, or too many tries came too fast.", "NoUsers": !s.auth.hasUsers(r.Context())}))
		return
	}
	if st == stageSetup {
		http.Redirect(w, r, "/account/setup", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login/code", http.StatusSeeOther)
}

// codePage is GET /login/code: the second step, for a session awaiting it.
func (s *server) codePage(w http.ResponseWriter, r *http.Request) {
	sess, u, ok := s.auth.sessionUser(r)
	if !ok || sess.stage != stageCode {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, "code", s.signinData(r, map[string]any{"Login": u.Login}))
}

func (s *server) codePost(w http.ResponseWriter, r *http.Request) {
	if !s.auth.csrfOK(r) {
		http.Error(w, "the form has expired; go back and try again", http.StatusForbidden)
		return
	}
	sess, u, found := s.auth.sessionUser(r)
	if !found || sess.stage != stageCode {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next := safeNext(sess.next)
	ok, backup, left := s.auth.code(w, r, r.FormValue("code"))
	if !ok {
		if s.auth.sessionOf(r) == nil {
			s.auth.logout(w, r)
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "code", s.signinData(r, map[string]any{"Login": u.Login, "Error": "That code is not right, or was used already."}))
		return
	}
	if backup {
		s.db.Audit(r.Context(), station.AuditEntry{Subject: u.Login, Action: "sign-in", Detail: "with a backup code, " + plural(left, "left")})
		if left <= 3 {
			next = "/account"
		}
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what
}

// --- Setup links

// setupURL is where a setup link's token leads, at the station's public
// address when it has one.
func (s *server) setupURL(token string) string {
	return strings.TrimSuffix(s.publicURL, "/") + "/setup/" + token
}

// makeInvite makes a user a fresh setup link, replacing any older one,
// and returns it; the station keeps only its hash.
func (s *server) makeInvite(ctx context.Context, p station.Principal, u station.User) (string, error) {
	token := randomID()
	inv := station.Invite{UserID: u.ID, ExpiresAt: time.Now().Add(inviteLife), By: p.Subject}
	if err := s.db.PutInvite(ctx, hashToken(token), inv); err != nil {
		return "", err
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "user", Detail: "made a setup link for " + u.Login})
	return s.setupURL(token), nil
}

// setupLinkPage is GET /setup/{token}: choose a password, for the user
// the link was made for.
func (s *server) setupLinkPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	inv, err := s.db.Invite(r.Context(), hashToken(token), time.Now())
	if err != nil {
		w.WriteHeader(http.StatusGone)
		s.render(w, "setup", s.signinData(r, map[string]any{"Expired": true}))
		return
	}
	u, err := s.db.User(r.Context(), inv.UserID)
	if err != nil || u.Locked {
		w.WriteHeader(http.StatusGone)
		s.render(w, "setup", s.signinData(r, map[string]any{"Expired": true}))
		return
	}
	s.render(w, "setup", s.signinData(r, map[string]any{"Token": token, "Login": u.Login, "UserName": u.Name}))
}

// setupLinkPost is POST /setup/{token}: the password is set, the link
// is spent, and the session goes on to the authenticator.
func (s *server) setupLinkPost(w http.ResponseWriter, r *http.Request) {
	if !s.auth.csrfOK(r) {
		http.Error(w, "the form has expired; go back and try again", http.StatusForbidden)
		return
	}
	ctx, token := r.Context(), r.PathValue("token")
	inv, err := s.db.Invite(ctx, hashToken(token), time.Now())
	if err != nil {
		w.WriteHeader(http.StatusGone)
		s.render(w, "setup", s.signinData(r, map[string]any{"Expired": true}))
		return
	}
	u, err := s.db.User(ctx, inv.UserID)
	if err != nil || u.Locked {
		w.WriteHeader(http.StatusGone)
		s.render(w, "setup", s.signinData(r, map[string]any{"Expired": true}))
		return
	}
	pw, again := r.FormValue("password"), r.FormValue("again")
	msg := ""
	if err := checkPasswordRules(pw, u.Login); err != nil {
		msg = capitalize(err.Error()) + "."
	} else if pw != again {
		msg = "The two passwords differ."
	}
	if msg != "" {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, "setup", s.signinData(r, map[string]any{"Token": token, "Login": u.Login, "UserName": u.Name, "Error": msg}))
		return
	}
	hash, err := hashPassword(pw)
	if err == nil {
		err = s.db.SetPassword(ctx, u.ID, hash)
	}
	if err == nil {
		err = s.db.UseInvite(ctx, hashToken(token))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: u.Login, Action: "password", Detail: "set from a setup link"})
	s.auth.drop(r)
	s.auth.begin(w, u.ID, stageSetup, "/")
	http.Redirect(w, r, "/account/setup", http.StatusSeeOther)
}

// --- The authenticator

// accountSetupPage is GET /account/setup: the QR code and the secret,
// and a field for the first code.
func (s *server) accountSetupPage(w http.ResponseWriter, r *http.Request) {
	sess, u, ok := s.auth.sessionUser(r)
	switch {
	case !ok:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	case sess.stage == stageCode:
		http.Redirect(w, r, "/login/code", http.StatusSeeOther)
		return
	case sess.stage != stageSetup:
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	s.render(w, "account-setup", s.setupData(r, sess, u, ""))
}

func (s *server) setupData(r *http.Request, sess *session, u station.User, msg string) map[string]any {
	secret := s.auth.setupSecret(sess)
	svg, _ := qrSVG(otpauthURL(s.title(), u.Login, secret))
	return s.signinData(r, map[string]any{"Login": u.Login, "QR": svg, "Secret": spaced(secret), "Replacing": u.TwoFactor, "Error": msg})
}

// spaced writes a secret in groups of four, for typing it in by hand.
func spaced(secret string) string {
	var b strings.Builder
	for i, c := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// accountSetupPost is POST /account/setup: the code that confirms the
// authenticator; the backup codes follow, shown once.
func (s *server) accountSetupPost(w http.ResponseWriter, r *http.Request) {
	if !s.auth.csrfOK(r) {
		http.Error(w, "the form has expired; go back and try again", http.StatusForbidden)
		return
	}
	sess, u, ok := s.auth.sessionUser(r)
	if !ok || sess.stage != stageSetup {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next := safeNext(sess.next)
	codes, ok := s.auth.confirmSetup(w, r, r.FormValue("code"))
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "account-setup", s.setupData(r, sess, u, "That code is not right. Codes change every 30 seconds; try the one showing now."))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: u.Login, Action: "two-factor", Detail: "set up an authenticator"})
	s.render(w, "account-codes", s.signinData(r, map[string]any{"Codes": codes, "Next": next, "Fresh": true}))
}

// accountSetupCancel is POST /account/setup/cancel: a user replacing
// their authenticator thinks better of it and keeps the old one.
func (s *server) accountSetupCancel(w http.ResponseWriter, r *http.Request) {
	if !s.auth.csrfOK(r) {
		http.Error(w, "the form has expired; go back and try again", http.StatusForbidden)
		return
	}
	if s.auth.cancelSetup(w, r) {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/account/setup", http.StatusSeeOther)
}

// --- The Account page, for people signed in

func (s *server) accountPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.render(w, "account", s.accountData(r, p, nil))
}

func (s *server) accountData(r *http.Request, p station.Principal, extra map[string]any) map[string]any {
	ctx := r.Context()
	u, _ := s.db.User(ctx, p.User)
	var teams []string
	if all, err := s.db.Teams(ctx); err == nil {
		for _, t := range all {
			for _, id := range u.Teams {
				if id == t.ID {
					teams = append(teams, t.Name)
				}
			}
		}
	}
	left, _ := s.db.BackupCodesLeft(ctx, u.ID)
	data := map[string]any{"Page": "account", "Principal": p, "CSRF": s.auth.csrfToken(r), "User": u, "TeamNames": teams, "Token": !person(p), "BackupLeft": left,
		"Tokens": s.ownTokens(ctx, u), "TokenLevels": tokenLevels(s.tokenCeiling(ctx, u))}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

// withPassword runs an Account change that needs the current password:
// the check, then fn, and the message for the page.
func (s *server) withPassword(r *http.Request, p station.Principal, fn func(u station.User) (string, error)) string {
	ctx := r.Context()
	u, err := s.db.User(ctx, p.User)
	if err != nil {
		return "Only a signed-in person can do that."
	}
	hash, _ := s.db.PasswordHash(ctx, u.ID)
	if hash != "" && !checkPassword(hash, r.FormValue("current")) {
		time.Sleep(500 * time.Millisecond)
		return "That is not your current password."
	}
	msg, err := fn(u)
	if err != nil {
		return capitalize(err.Error()) + "."
	}
	return msg
}

// changePassword is POST /account/password: the current password, then
// a new one that meets the rules, twice.
func (s *server) changePassword(w http.ResponseWriter, r *http.Request, p station.Principal) {
	msg := s.withPassword(r, p, func(u station.User) (string, error) {
		pw, again := r.FormValue("password"), r.FormValue("again")
		if err := checkPasswordRules(pw, u.Login); err != nil {
			return "", err
		}
		if pw != again {
			return "", errors.New("the two passwords differ")
		}
		hash, err := hashPassword(pw)
		if err == nil {
			err = s.db.SetPassword(r.Context(), u.ID, hash)
		}
		if err != nil {
			return "", err
		}
		s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "password", Detail: "changed their own"})
		return "Password changed.", nil
	})
	s.render(w, "account", s.accountData(r, p, map[string]any{"Message": msg}))
}

// accountCodes is POST /account/codes: a fresh set of backup codes,
// shown once, replacing the old.
func (s *server) accountCodes(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var codes []string
	msg := s.withPassword(r, p, func(u station.User) (string, error) {
		if !u.TwoFactor {
			return "", errors.New("set up an authenticator first")
		}
		var err error
		codes, err = s.auth.newBackupCodes(r.Context(), u.ID)
		return "", err
	})
	if codes == nil {
		s.render(w, "account", s.accountData(r, p, map[string]any{"Message": msg}))
		return
	}
	s.db.Audit(r.Context(), station.AuditEntry{Subject: p.Subject, Action: "two-factor", Detail: "made new backup codes"})
	s.render(w, "account-codes", s.signinData(r, map[string]any{"Codes": codes, "Next": "/account"}))
}

// accountAuthenticator is POST /account/authenticator: start replacing
// the authenticator; the old one stands until a code confirms the new.
func (s *server) accountAuthenticator(w http.ResponseWriter, r *http.Request, p station.Principal) {
	msg := s.withPassword(r, p, func(u station.User) (string, error) { return "", nil })
	if msg != "" {
		s.render(w, "account", s.accountData(r, p, map[string]any{"Message": msg}))
		return
	}
	if sess := s.auth.sessionOf(r); sess != nil {
		s.auth.startSetup(w, r, sess)
	}
	http.Redirect(w, r, "/account/setup", http.StatusSeeOther)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
