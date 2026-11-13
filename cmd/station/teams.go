package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/sqlite"
)

// --- Users, teams and device access: the Teams section of Settings and
// its API. Admins only; the page wrapper and the API wrapper see to that.

// userView is a user as Settings shows them.
type userView struct {
	station.User
	TeamNames []string
	Self      bool   // the admin looking at the page, who cannot lock themselves out
	SignIn    string // how they sign in, in words
}

// signInWords says how a user signs in: a setup link still open, a
// password with an authenticator, one without until their next sign-in,
// or no password at all.
func signInWords(u station.User, invite time.Time) string {
	switch {
	case u.Locked:
		return "locked"
	case !invite.IsZero():
		return "setup link until " + invite.Local().Format("Jan 2 15:04")
	case u.TwoFactor:
		return "password and authenticator"
	case u.HasPassword:
		return "password; authenticator at next sign-in"
	}
	return "no password"
}

// teamView is a team with its members' logins, and the users who could
// still join it.
type teamView struct {
	station.Team
	Members  []station.User
	Joinable []station.User
}

// accessRow is one device's levels across every team.
type accessRow struct {
	Device deviceView
	Cells  []accessCell
}

type accessCell struct {
	Team  station.Team
	Level station.Level
}

// teamsData adds the Teams section to the Settings data.
func (s *server) teamsData(ctx context.Context, p station.Principal, data map[string]any) {
	users, _ := s.db.Users(ctx)
	teams, _ := s.db.Teams(ctx)
	accesses, _ := s.db.Accesses(ctx)
	invites, _ := s.db.OpenInvites(ctx, time.Now())
	devs, _ := s.devices(ctx)
	teamName := map[string]string{}
	for _, t := range teams {
		teamName[t.ID] = t.Name
	}
	byID := map[string]station.User{}
	logins := map[string]string{}
	for _, u := range users {
		byID[u.ID] = u
		logins[u.ID] = u.Login
	}
	var uviews []userView
	for _, u := range users {
		v := userView{User: u, Self: u.ID == p.User, SignIn: signInWords(u, invites[u.ID])}
		for _, t := range u.Teams {
			v.TeamNames = append(v.TeamNames, teamName[t])
		}
		uviews = append(uviews, v)
	}
	var tviews []teamView
	for _, t := range teams {
		v := teamView{Team: t}
		in := map[string]bool{}
		for _, id := range t.Members {
			in[id] = true
			if u, ok := byID[id]; ok {
				v.Members = append(v.Members, u)
			}
		}
		for _, u := range users {
			if !in[u.ID] && !u.Admin {
				v.Joinable = append(v.Joinable, u)
			}
		}
		tviews = append(tviews, v)
	}
	level := map[string]station.Level{}
	for _, a := range accesses {
		level[a.DeviceID+"|"+a.TeamID] = a.Level
	}
	var rows []accessRow
	for _, d := range devs {
		if d.ID == "" {
			continue
		}
		row := accessRow{Device: d}
		for _, t := range teams {
			row.Cells = append(row.Cells, accessCell{Team: t, Level: level[d.ID+"|"+t.ID]})
		}
		rows = append(rows, row)
	}
	data["Users"], data["Teams"], data["AccessRows"], data["TokenUsers"] = uviews, tviews, rows, logins
	data["Levels"] = []station.Level{station.LevelNone, station.LevelWatch, station.LevelDrive, station.LevelJam}
}

// The operations, shared by the page's forms and the API. Each returns
// the message the page shows, and an error for the API's status.

var errLockout = errors.New("you cannot take your own admin rights or sign-in away")

// addUser makes a user and their setup link, where they choose a
// password and add an authenticator; nobody chooses one for them.
func (s *server) addUser(ctx context.Context, p station.Principal, login, name string, admin bool) (station.User, string, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" || len(login) > 120 || strings.ContainsAny(login, " \t\n/\\") {
		return station.User{}, "", errors.New("a login is a word such as an email address, up to 120 characters")
	}
	u := station.User{ID: station.NewID("usr"), Login: login, Name: strings.TrimSpace(name), Admin: admin}
	if err := s.db.PutUser(ctx, u); err != nil {
		return station.User{}, "", err
	}
	role := "regular"
	if admin {
		role = "admin"
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "user", Detail: "added " + login + " (" + role + ")"})
	link, err := s.makeInvite(ctx, p, u)
	return u, link, err
}

func (s *server) setRole(ctx context.Context, p station.Principal, userID string, admin bool) error {
	if userID == p.User && !admin {
		return errLockout
	}
	u, err := s.db.User(ctx, userID)
	if err != nil {
		return err
	}
	u.Admin = admin
	if err := s.db.PutUser(ctx, u); err != nil {
		return err
	}
	role := "a regular user"
	if admin {
		role = "an admin"
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "user", Detail: u.Login + " is " + role})
	return nil
}

// setLocked locks a user out, ending their sessions, or lets them back.
func (s *server) setLocked(ctx context.Context, p station.Principal, userID string, locked bool) error {
	if userID == p.User && locked {
		return errLockout
	}
	u, err := s.db.User(ctx, userID)
	if err != nil {
		return err
	}
	u.Locked = locked
	if err := s.db.PutUser(ctx, u); err != nil {
		return err
	}
	what := "unlocked"
	if locked {
		what = "locked"
		s.auth.dropUser(userID)
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "user", Detail: what + " " + u.Login})
	return nil
}

// resetSignIn forgets a user's password, authenticator and backup codes,
// ends their sessions, and makes them a new setup link.
func (s *server) resetSignIn(ctx context.Context, p station.Principal, userID string) (string, error) {
	u, err := s.db.User(ctx, userID)
	if err != nil {
		return "", err
	}
	if err := s.db.ResetSignIn(ctx, u.ID); err != nil {
		return "", err
	}
	s.auth.dropUser(u.ID)
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "user", Detail: "reset " + u.Login + "'s sign-in"})
	return s.makeInvite(ctx, p, u)
}

func (s *server) removeUser(ctx context.Context, p station.Principal, userID string) error {
	if userID == p.User {
		return errLockout
	}
	u, err := s.db.User(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.db.RemoveUser(ctx, userID); err != nil {
		return err
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "user", Detail: "removed " + u.Login})
	return nil
}

func (s *server) addTeam(ctx context.Context, p station.Principal, name string) (station.Team, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return station.Team{}, errors.New("a team needs a name of up to 60 characters")
	}
	t := station.Team{ID: station.NewID("team"), Name: name}
	if err := s.db.PutTeam(ctx, t); err != nil {
		return station.Team{}, err
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "team", Detail: "added " + name})
	return t, nil
}

func (s *server) removeTeam(ctx context.Context, p station.Principal, teamID string) error {
	if err := s.db.RemoveTeam(ctx, teamID); err != nil {
		return err
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "team", Detail: "removed " + teamID})
	return nil
}

func (s *server) setMember(ctx context.Context, p station.Principal, teamID, userID string, in bool) error {
	if err := s.db.SetMember(ctx, teamID, userID, in); err != nil {
		return err
	}
	what := "left"
	if in {
		what = "joined"
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "team", Detail: userID + " " + what + " " + teamID})
	return nil
}

func (s *server) setAccess(ctx context.Context, p station.Principal, deviceID, teamID, levelName string) error {
	level, err := station.ParseLevel(levelName)
	if err != nil {
		return err
	}
	if deviceID == "" || teamID == "" {
		return errors.New("a device and a team are needed")
	}
	if err := s.db.SetAccess(ctx, station.Access{DeviceID: deviceID, TeamID: teamID, Level: level}); err != nil {
		return err
	}
	what := "none"
	if level != station.LevelNone {
		what = string(level)
	}
	s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "access", DeviceID: deviceID, Detail: teamID + ": " + what})
	return nil
}

// --- The page's forms: each goes back to the Teams section, or renders
// Settings with the message when something was wrong.

func (s *server) teamsForm(w http.ResponseWriter, r *http.Request, p station.Principal, err error) {
	if err != nil {
		s.render(w, "settings", s.settingsData(r, p, map[string]any{"Message": err.Error()}))
		return
	}
	http.Redirect(w, r, "/settings/access#teams", http.StatusSeeOther)
}

func (s *server) addUserForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	u, link, err := s.addUser(r.Context(), p, r.FormValue("login"), r.FormValue("name"), r.FormValue("admin") == "on")
	if err != nil {
		s.teamsForm(w, r, p, err)
		return
	}
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"SetupURL": link, "SetupLogin": u.Login}))
}

func (s *server) userRoleForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.teamsForm(w, r, p, s.setRole(r.Context(), p, r.FormValue("user"), r.FormValue("admin") == "1"))
}

func (s *server) userLockForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.teamsForm(w, r, p, s.setLocked(r.Context(), p, r.FormValue("user"), r.FormValue("locked") == "1"))
}

func (s *server) userResetForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	link, err := s.resetSignIn(r.Context(), p, r.FormValue("user"))
	if err != nil {
		s.teamsForm(w, r, p, err)
		return
	}
	u, _ := s.db.User(r.Context(), r.FormValue("user"))
	s.render(w, "settings", s.settingsData(r, p, map[string]any{"SetupURL": link, "SetupLogin": u.Login}))
}

func (s *server) userRemoveForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.teamsForm(w, r, p, s.removeUser(r.Context(), p, r.FormValue("user")))
}

func (s *server) addTeamForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	_, err := s.addTeam(r.Context(), p, r.FormValue("name"))
	s.teamsForm(w, r, p, err)
}

func (s *server) teamRemoveForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.teamsForm(w, r, p, s.removeTeam(r.Context(), p, r.FormValue("team")))
}

func (s *server) memberForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.teamsForm(w, r, p, s.setMember(r.Context(), p, r.FormValue("team"), r.FormValue("user"), r.FormValue("in") != "0"))
}

func (s *server) accessForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	s.teamsForm(w, r, p, s.setAccess(r.Context(), p, r.FormValue("device"), r.FormValue("team"), r.FormValue("level")))
}

// --- The API, for admins signed in; tokens never manage.

func teamsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, station.ErrNotFound):
		apiError(w, http.StatusNotFound, "no such user, team or device")
	case errors.Is(err, errLockout), errors.Is(err, sqlite.ErrLoginTaken), errors.Is(err, sqlite.ErrTeamExists):
		apiError(w, http.StatusConflict, err.Error())
	default:
		apiError(w, http.StatusBadRequest, err.Error())
	}
}

func decodeBody(r *http.Request, into any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(into)
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request, _ station.Principal) {
	users, err := s.db.Users(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if users == nil {
		users = []station.User{}
	}
	apiJSON(w, users)
}

// invitedUser is a user with the setup link just made for them.
type invitedUser struct {
	station.User
	SetupURL string `json:"setup_url"`
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		Login, Name string
		Admin       bool
	}
	if err := decodeBody(r, &body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with login is needed")
		return
	}
	u, link, err := s.addUser(r.Context(), p, body.Login, body.Name, body.Admin)
	if err != nil {
		teamsError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	apiJSON(w, invitedUser{User: u, SetupURL: link})
}

func (s *server) resetUser(w http.ResponseWriter, r *http.Request, p station.Principal) {
	link, err := s.resetSignIn(r.Context(), p, r.PathValue("id"))
	if err != nil {
		teamsError(w, err)
		return
	}
	apiJSON(w, map[string]string{"setup_url": link})
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct {
		Name   *string
		Admin  *bool
		Locked *bool
	}
	if err := decodeBody(r, &body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with name, admin or locked")
		return
	}
	ctx, id := r.Context(), r.PathValue("id")
	u, err := s.db.User(ctx, id)
	if err != nil {
		teamsError(w, err)
		return
	}
	if body.Name != nil {
		u.Name = strings.TrimSpace(*body.Name)
		if err = s.db.PutUser(ctx, u); err != nil {
			teamsError(w, err)
			return
		}
	}
	if body.Admin != nil {
		if err = s.setRole(ctx, p, id, *body.Admin); err != nil {
			teamsError(w, err)
			return
		}
	}
	if body.Locked != nil {
		if err = s.setLocked(ctx, p, id, *body.Locked); err != nil {
			teamsError(w, err)
			return
		}
	}
	u, _ = s.db.User(ctx, id)
	apiJSON(w, u)
}

func (s *server) deleteUser(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if err := s.removeUser(r.Context(), p, r.PathValue("id")); err != nil {
		teamsError(w, err)
		return
	}
	apiJSON(w, map[string]any{"removed": true})
}

func (s *server) listTeams(w http.ResponseWriter, r *http.Request, _ station.Principal) {
	teams, err := s.db.Teams(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if teams == nil {
		teams = []station.Team{}
	}
	apiJSON(w, teams)
}

func (s *server) createTeam(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct{ Name string }
	if err := decodeBody(r, &body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with name is needed")
		return
	}
	t, err := s.addTeam(r.Context(), p, body.Name)
	if err != nil {
		teamsError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	apiJSON(w, t)
}

func (s *server) deleteTeam(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if err := s.removeTeam(r.Context(), p, r.PathValue("id")); err != nil {
		teamsError(w, err)
		return
	}
	apiJSON(w, map[string]any{"removed": true})
}

func (s *server) putMember(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if err := s.setMember(r.Context(), p, r.PathValue("id"), r.PathValue("user"), r.Method != http.MethodDelete); err != nil {
		teamsError(w, err)
		return
	}
	apiJSON(w, map[string]any{"member": r.Method != http.MethodDelete})
}

func (s *server) listAccess(w http.ResponseWriter, r *http.Request, _ station.Principal) {
	acc, err := s.db.Accesses(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if acc == nil {
		acc = []station.Access{}
	}
	apiJSON(w, acc)
}

func (s *server) putAccess(w http.ResponseWriter, r *http.Request, p station.Principal) {
	var body struct{ Device, Team, Level string }
	if err := decodeBody(r, &body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON body with device, team and level is needed")
		return
	}
	if err := s.setAccess(r.Context(), p, body.Device, body.Team, body.Level); err != nil {
		teamsError(w, err)
		return
	}
	apiJSON(w, station.Access{DeviceID: body.Device, TeamID: body.Team, Level: station.Level(body.Level)})
}

// lastSeen words a user's last sign-in for the page.
func lastSeen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2 15:04")
}
