package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/hold"
	"github.com/VibiumDev/station/jam"
)

// jams runs the station's jam sessions: a prompt typed on the Jams page
// becomes a loop in the jam package driving a device through a
// `vibium mcp` subprocess, under a token minted for the jam. See
// docs/specs/jams.md.
type jams struct {
	s        *server
	models   *jam.Store
	dir      string // screenshots, under <id>/<seq>.png
	vibium   string // the binary; "" when not found
	missing  string // why, when vibium is ""
	loopback string // ws://127.0.0.1:port, where the subprocess reaches /session
	mu       sync.Mutex
	live     map[string]*liveJam
}

// liveJam is a jam while it runs.
type liveJam struct {
	j       station.Jam
	loop    *jam.Loop
	mu      sync.Mutex
	seq     int
	subs    map[chan station.JamEvent]struct{}
	unwatch func()
	grace   int64 // when the jam ends after a settled result, Unix milliseconds, while a watcher may still keep it open
}

// watched says whether anyone follows the jam's events right now, which
// is what the page does while a jam runs.
func (lj *liveJam) watched() bool {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	return len(lj.subs) > 0
}

// Limits a jam has of its own, which the token's expiry backs.
const (
	jamTimeLimit  = 30 * time.Minute
	jamMaxActions = 100
	jamGrace      = 15 * time.Second
	jamMaxPrompt  = 4000
)

// jamSubject is the hold's subject while a jam drives a device: the
// jam's token.
func jamSubject(id string) string { return "token:jam:" + id }

// jamOfSubject is the jam a hold's subject names, or "".
func jamOfSubject(subject string) string {
	if id, ok := strings.CutPrefix(subject, "token:jam:"); ok {
		return id
	}
	return ""
}

func urlQuery(s string) string { return url.QueryEscape(s) }

// on says whether Jams is on: an agent model is set and the switch is
// not off.
func (m *jams) on() bool {
	if m == nil {
		return false
	}
	m.s.nameMu.RLock()
	off := m.s.cfg.JamsOff
	m.s.nameMu.RUnlock()
	return !off && len(m.models.OfRole(jam.RoleAgent)) > 0
}

// possible says whether an agent model exists, whatever the switch.
func (m *jams) possible() bool { return m != nil && len(m.models.OfRole(jam.RoleAgent)) > 0 }

// examples are the prompts a device's kind suggests under the box.
var examples = map[string][]string{
	"phone": {
		"Is the phone on its home screen?",
		"Open Settings and tell me which version of the operating system it runs.",
		"Open the clock app and set a timer for one minute.",
	},
	"browser": {
		"Is a page open, and what is its title?",
		"Open example.com and tell me what the page says.",
	},
}

// greetings rotate above the prompt box, as the chat products' do:
// casual, plain, about doing as much as checking, and never naming the
// device.
var greetings = []string{
	"What are we doing today?",
	"What should we check?",
	"Ready when you are.",
	"Where do we start?",
	"Let's try something.",
	"What's the plan?",
}

func greeting() string {
	return greetings[rand.IntN(len(greetings))]
}

func examplesFor(kind string) []string {
	if e, ok := examples[kind]; ok {
		return e
	}
	return examples["phone"]
}

// start begins a jam for a principal: the record, the token, the
// subprocess and the loop. Empty device or model mean the only online
// device and the default agent model.
func (m *jams) start(ctx context.Context, p station.Principal, prompt, deviceID, modelName, mode string) (station.Jam, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || len(prompt) > jamMaxPrompt {
		return station.Jam{}, fmt.Errorf("a prompt is a sentence or a few, up to %d characters", jamMaxPrompt)
	}
	if mode != "" && mode != "check" && mode != "run" {
		return station.Jam{}, errors.New("mode is check, run, or left out")
	}
	// Who may, before what the station can: a refusal never says more
	// than the caller is owed.
	if p.Device != "" && deviceID != "" && deviceID != p.Device {
		return station.Jam{}, forbidden("this token may only use " + p.Device)
	}
	dev, err := m.pickDevice(ctx, p, deviceID)
	if err != nil {
		return station.Jam{}, err
	}
	if err := m.s.auth.Authorize(ctx, p, station.ActionBidi, dev.ID); err != nil {
		return station.Jam{}, forbidden("this login may not start jams on " + dev.ID)
	}
	if m.vibium == "" {
		return station.Jam{}, errors.New("the station cannot find Vibium: " + m.missing)
	}
	// A jam does not wait in line: a held device is said so up front,
	// before Vibium is started for nothing.
	if h, err := m.s.db.Current(ctx, dev.ID); err == nil && hold.Controls(h.Scopes) {
		if id := jamOfSubject(h.Subject); id != "" {
			return station.Jam{}, errors.New("jam " + id + " still holds " + dev.ID + "; stop it first, or wait for it to end")
		}
		return station.Jam{}, errors.New(dev.ID + " is held by " + h.Subject)
	}
	var model jam.ModelConfig
	if modelName == "" {
		var ok bool
		if model, ok = m.models.Default(jam.RoleAgent); !ok {
			return station.Jam{}, errors.New("no agent model is set on Settings")
		}
	} else if model, err = m.models.Get(modelName); err != nil || model.Role != jam.RoleAgent {
		return station.Jam{}, errors.New("no agent model named " + modelName)
	}
	if problem := jam.ChatToolsProblem(model.Provider, model.Model); problem != "" {
		return station.Jam{}, errors.New(problem)
	}
	j := station.Jam{ID: station.NewID("jam"), Subject: p.Subject, DeviceID: dev.ID, Model: model.Name, Prompt: prompt, Mode: mode, Status: "running", CreatedAt: time.Now()}
	if err := m.s.db.CreateJam(ctx, j); err != nil {
		return station.Jam{}, err
	}
	secret, err := m.s.db.CreateScopedToken(ctx, "jam:"+j.ID, station.TokenDrive, dev.ID, j.CreatedAt.Add(jamTimeLimit+time.Minute), p.User)
	if err != nil {
		return station.Jam{}, err
	}
	lj := &liveJam{j: j, subs: map[chan station.JamEvent]struct{}{}}
	m.mu.Lock()
	m.live[j.ID] = lj
	m.mu.Unlock()
	lj.emit(m, jam.Event{Kind: "start", Text: dev.ID + " with " + model.Name})
	logf := func(format string, args ...any) {
		line := strings.TrimPrefix(fmt.Sprintf(format, args...), "vibium: vibium: ")
		fmt.Printf("jam %s: %s\n", j.ID, line)
	}
	mcp, err := jam.StartMCP(ctx, m.vibium, []string{"mcp"}, m.env(secret, model), logf)
	if err != nil {
		m.finish(lj, jam.Result{Status: "failed", Reason: "starting Vibium: " + err.Error(), Err: err}, nil)
		return j, errors.New("starting Vibium: " + err.Error())
	}
	lj.loop = jam.New(model.Client(), mcp, jam.Options{Prompt: prompt, Mode: mode, DeviceKind: dev.Kind, MaxActions: jamMaxActions, TimeLimit: jamTimeLimit,
		Grace: jamGrace, Watched: lj.watched, Shortcuts: jamShortcuts(dev.Shortcuts), About: deviceAbout(dev), Speaks: m.has(jam.RoleSay), Hears: m.has(jam.RoleListen), Emit: func(ev jam.Event) { lj.emit(m, ev) }})
	m.s.db.Audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "jam", DeviceID: dev.ID, Detail: "started " + j.ID + " with " + model.Name})
	go m.watchHold(lj)
	go func() {
		// Vibium records the jam: its steps, what was said and what was
		// heard, with the sound. The record goes to this station when the
		// jam ends, next to the rig's own record of the hold.
		recording := m.recordJam(mcp, j.ID, logf)
		res := lj.loop.Run(context.Background())
		recording()
		mcp.Close()
		m.finish(lj, res, nil)
	}()
	return j, nil
}

// recordJam starts Vibium's recording of a jam and returns what ends it
// and sends it here. A jam runs the same without a record, so a failure
// is logged and nothing more.
func (m *jams) recordJam(mcp *jam.MCP, id string, logf func(string, ...any)) func() {
	path := filepath.Join(m.s.dir, "vibium", "records", id+".zip")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := mcp.Call(ctx, "browser_record_start", map[string]any{"name": id, "path": path})
	if err != nil || res.IsError {
		logf("no record of this jam: %v %s", err, res.Text())
		return func() {}
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if res, err := mcp.Call(ctx, "browser_record_stop", map[string]any{"upload": true}); err != nil || res.IsError {
			logf("the jam's record was not sent: %v %s", err, res.Text())
		}
		os.Remove(path)
	}
}

// deviceAbout words what the device is for the model's instruction:
// "an iPhone 15 Pro Max on iOS 26.6", or "" when the rig said nothing.
func deviceAbout(d deviceView) string {
	var b strings.Builder
	if d.Model != "" {
		b.WriteString(article(d.Model) + " " + d.Model)
	}
	if d.Platform != "" {
		if b.Len() > 0 {
			b.WriteString(" on ")
		} else {
			b.WriteString("running ")
		}
		b.WriteString(platformName(d.Platform))
		if d.OSVersion != "" {
			b.WriteString(" " + d.OSVersion)
		}
	}
	return b.String()
}

// article is "a" or "an" for the word that follows.
func article(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an"
	}
	return "a"
}

// jamShortcuts turns the device's announced shortcuts, in the viewer's
// key syntax, into Vibium's: GUI is Meta and Ctrl is Control.
func jamShortcuts(in []station.Shortcut) []jam.Shortcut {
	var out []jam.Shortcut
	for _, s := range in {
		parts := strings.Split(s.Keys, "+")
		for i, p := range parts {
			switch strings.ToLower(strings.TrimSpace(p)) {
			case "gui", "cmd", "win", "super":
				parts[i] = "Meta"
			case "ctrl":
				parts[i] = "Control"
			default:
				parts[i] = strings.TrimSpace(p)
			}
		}
		out = append(out, jam.Shortcut{Label: s.Label, Keys: strings.Join(parts, "+")})
	}
	return out
}

// takeResult records a delivered result on the jam. The jam's result is
// its first prompt's; a later result of the same kind replaces it, as
// when a check is run again after a fix, and one of the other kind, an
// errand after a check, shows in the transcript alone.
func takeResult(j *station.Jam, v *jam.Verdict) {
	if v == nil || (j.Took != "" && j.Took != v.Took) {
		return
	}
	j.Took, j.Result, j.Summary = v.Took, v.Status, v.Summary
}

// pickDevice is the device a jam drives: the one named, or the only
// online one, among those the principal may start jams on.
func (m *jams) pickDevice(ctx context.Context, p station.Principal, want string) (deviceView, error) {
	devs, err := m.s.visibleDevices(ctx, p)
	if err != nil {
		return deviceView{}, err
	}
	// A device the person sees but may not start jams on gets its own
	// answer, rather than looking absent.
	seen := len(devs)
	devs = slices.DeleteFunc(devs, func(d deviceView) bool { return d.ID == "" || !d.Level.AtLeast(station.LevelJam) })
	if len(devs) == 0 && seen > 0 && !p.Admin {
		if want != "" {
			return deviceView{}, forbidden("you may not start jams on " + want + "; an admin sets the jam level on Settings")
		}
		return deviceView{}, forbidden("none of your devices allows jams; an admin sets the jam level on Settings")
	}
	if err != nil {
		return deviceView{}, err
	}
	var online []deviceView
	for _, d := range devs {
		if d.ID == "" {
			continue
		}
		if want != "" && (d.ID == want || d.Name == want) {
			if !d.Online {
				return deviceView{}, errors.New(d.ID + " is offline")
			}
			return d, nil
		}
		if d.Online {
			online = append(online, d)
		}
	}
	switch {
	case want != "":
		return deviceView{}, forbidden("no device named " + want + " is yours to use")
	case len(online) == 0:
		return deviceView{}, errors.New("no device is online")
	case len(online) > 1:
		return deviceView{}, errors.New("more than one device is online; name one")
	}
	return online[0], nil
}

// has says whether the owner set a default model for a role.
func (m *jams) has(role string) bool {
	_, ok := m.models.Default(role)
	return ok
}

// env is the subprocess's environment: the station's own /session with
// the jam's token, Vibium's directories under the station's, and the
// listen and say models' settings. The agent's key is not in it.
func (m *jams) env(secret string, _ jam.ModelConfig) []string {
	vdir := filepath.Join(m.s.dir, "vibium")
	env := []string{
		"VIBIUM_CONNECT_URL=" + m.loopback + "/session",
		"VIBIUM_CONNECT_API_KEY=" + secret,
		"VIBIUM_LOAD_AI_ENV=0",
		"VIBIUM_CONFIG_DIR=" + filepath.Join(vdir, "config"),
		"VIBIUM_CACHE_DIR=" + filepath.Join(vdir, "cache"),
		"VIBIUM_RUNTIME_DIR=" + filepath.Join(vdir, "run"),
	}
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "LANG"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	for _, role := range []string{jam.RoleListen, jam.RoleSay} {
		if c, ok := m.models.Default(role); ok {
			env = append(env, c.Env()...)
		}
	}
	return env
}

// watchHold finds the hold the jam's session took and ends the jam when
// the hold ends, which is how the owner's Hold takes the device back.
func (m *jams) watchHold(lj *liveJam) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-lj.loop.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		h, err := m.s.db.Current(context.Background(), lj.j.DeviceID)
		if err != nil || h.Subject != jamSubject(lj.j.ID) {
			continue
		}
		lj.mu.Lock()
		lj.j.HoldID = h.ID
		j := lj.j
		lj.mu.Unlock()
		m.s.db.UpdateJam(context.Background(), j)
		stop := m.s.relay.Watch(h.ID, func() { lj.loop.Stop("the device was taken") })
		lj.mu.Lock()
		lj.unwatch = stop
		lj.mu.Unlock()
		return
	}
}

// emit stores one event, writes its screenshot to disk, and hands it to
// whoever is watching the jam.
func (lj *liveJam) emit(m *jams, ev jam.Event) {
	lj.mu.Lock()
	lj.seq++
	seq := lj.seq
	if ev.PNG != nil {
		dir := filepath.Join(m.dir, lj.j.ID)
		if os.MkdirAll(dir, 0o700) == nil && os.WriteFile(filepath.Join(dir, strconv.Itoa(seq)+".png"), ev.PNG, 0o600) == nil {
			ev.Image = "/jams/" + lj.j.ID + "/shots/" + strconv.Itoa(seq) + ".png"
		}
	}
	// The record keeps up with the transcript: the count of actions and
	// tokens as they happen, written with each verdict. The grace after
	// a settled result is a moment, not history: the watchers hear of
	// it and the page shows it, but the transcript does not keep it.
	var record *station.Jam
	lj.grace = 0
	switch ev.Kind {
	case "grace":
		lj.grace = ev.Until
	case "call":
		lj.j.Actions++
	case "usage":
		lj.j.TokensIn += ev.In
		lj.j.TokensOut += ev.Out
	case "verdict":
		takeResult(&lj.j, ev.Verdict)
		j := lj.j
		record = &j
	}
	data, _ := json.Marshal(ev)
	e := station.JamEvent{JamID: lj.j.ID, Seq: seq, At: time.Now(), Kind: ev.Kind, Data: data}
	subs := make([]chan station.JamEvent, 0, len(lj.subs))
	for ch := range lj.subs {
		subs = append(subs, ch)
	}
	lj.mu.Unlock()
	if ev.Kind != "grace" {
		m.s.db.AddJamEvent(context.Background(), e)
	}
	if record != nil {
		m.s.db.UpdateJam(context.Background(), *record)
	}
	for _, ch := range subs {
		select {
		case ch <- e:
		default: // a watcher that has stopped reading
		}
	}
}

// finish writes how the jam ended, drops its token and lets it go.
func (m *jams) finish(lj *liveJam, res jam.Result, _ error) {
	ctx := context.Background()
	lj.mu.Lock()
	lj.j.Status, lj.j.EndedAt = res.Status, time.Now()
	if res.Err != nil {
		lj.j.Error = res.Reason
	}
	j := lj.j
	unwatch := lj.unwatch
	lj.mu.Unlock()
	if unwatch != nil {
		unwatch()
	}
	m.s.db.UpdateJam(ctx, j)
	m.s.db.RevokeToken(ctx, "jam:"+j.ID)
	if j.HoldID != "" {
		m.s.holds.Release(ctx, j.HoldID, jamSubject(j.ID)) // already gone when Vibium closed the session
	}
	m.s.db.Audit(ctx, station.AuditEntry{Subject: j.Subject, Action: "jam", DeviceID: j.DeviceID, HoldID: j.HoldID, Detail: j.ID + " " + res.Status + endReasonText(res.Reason)})
	m.mu.Lock()
	delete(m.live, j.ID)
	for ch := range lj.subs {
		close(ch)
	}
	lj.subs = nil
	m.mu.Unlock()
}

func endReasonText(r string) string {
	if r == "" {
		return ""
	}
	return ": " + r
}

// closeLeftovers marks the jams a previous run left open as stopped.
func (m *jams) closeLeftovers(ctx context.Context) {
	open, _ := m.s.db.OpenJams(ctx)
	for _, j := range open {
		j.Status, j.Error, j.EndedAt = "stopped", "the station restarted", time.Now()
		m.s.db.UpdateJam(ctx, j)
		m.s.db.RevokeToken(ctx, "jam:"+j.ID)
	}
}

// get is a jam, live or stored, when the principal may see it.
func (m *jams) get(ctx context.Context, p station.Principal, id string) (station.Jam, bool, error) {
	m.mu.Lock()
	lj := m.live[id]
	m.mu.Unlock()
	var j station.Jam
	if lj != nil {
		lj.mu.Lock()
		j = lj.j
		lj.mu.Unlock()
	} else {
		var err error
		if j, err = m.s.db.Jam(ctx, id); err != nil {
			return j, false, err
		}
	}
	if !p.Admin && j.Subject != p.Subject && !m.s.auth.level(ctx, p, j.DeviceID).AtLeast(station.LevelWatch) {
		return j, false, station.ErrNotFound
	}
	return j, lj != nil, nil
}

// act finds a jam the principal may act on, with a follow-up, Keep or
// Stop: its starter, an admin, or someone at jam level on its device.
// Watching the device lets one see the jam, not steer it.
func (m *jams) act(ctx context.Context, p station.Principal, id string) (station.Jam, bool, error) {
	j, live, err := m.get(ctx, p, id)
	if err != nil {
		return j, live, err
	}
	if p.Manages() || j.Subject == p.Subject || m.s.auth.Authorize(ctx, p, station.ActionBidi, j.DeviceID) == nil {
		return j, live, nil
	}
	return j, live, station.ErrForbidden
}

// visibleJams keeps the jams a principal may see: all for an admin, else
// their own and those run on devices their teams may watch.
func (s *server) visibleJams(ctx context.Context, p station.Principal, list []station.Jam) []station.Jam {
	if p.Admin {
		return list
	}
	out := []station.Jam{}
	levels := map[string]station.Level{}
	for _, j := range list {
		l, ok := levels[j.DeviceID]
		if !ok {
			l = s.auth.level(ctx, p, j.DeviceID)
			levels[j.DeviceID] = l
		}
		if j.Subject == p.Subject || l.AtLeast(station.LevelWatch) {
			out = append(out, j)
		}
	}
	return out
}

// turn queues a follow-up prompt on an open jam.
func (m *jams) turn(id, prompt string) error {
	m.mu.Lock()
	lj := m.live[id]
	m.mu.Unlock()
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || len(prompt) > jamMaxPrompt {
		return errors.New("a prompt is a sentence or a few")
	}
	if lj == nil || lj.loop == nil {
		return jam.ErrEnded
	}
	return lj.loop.Turn(prompt)
}

// keep holds a jam open through the grace after a settled result.
func (m *jams) keep(id string) bool {
	m.mu.Lock()
	lj := m.live[id]
	m.mu.Unlock()
	if lj == nil || lj.loop == nil {
		return false
	}
	lj.mu.Lock()
	lj.grace = 0
	lj.mu.Unlock()
	return lj.loop.Keep() == nil
}

// graceUntil is when an open jam ends after a settled result, in Unix
// milliseconds, or 0 when it is not in that moment.
func (m *jams) graceUntil(id string) int64 {
	m.mu.Lock()
	lj := m.live[id]
	m.mu.Unlock()
	if lj == nil {
		return 0
	}
	lj.mu.Lock()
	defer lj.mu.Unlock()
	return lj.grace
}

// stop ends an open jam.
func (m *jams) stop(id, why string) bool {
	m.mu.Lock()
	lj := m.live[id]
	m.mu.Unlock()
	if lj == nil || lj.loop == nil {
		return false
	}
	lj.loop.Stop(why)
	return true
}

// subscribe hands back the events after seq and a channel for the rest,
// or no channel when the jam has ended.
func (m *jams) subscribe(ctx context.Context, id string, after int) ([]station.JamEvent, chan station.JamEvent, func()) {
	m.mu.Lock()
	lj := m.live[id]
	var ch chan station.JamEvent
	if lj != nil {
		ch = make(chan station.JamEvent, 256)
		lj.mu.Lock()
		if lj.subs != nil {
			lj.subs[ch] = struct{}{}
		} else {
			close(ch)
		}
		lj.mu.Unlock()
	}
	m.mu.Unlock()
	past, _ := m.s.db.JamEvents(ctx, id, after)
	cancel := func() {
		if lj == nil {
			return
		}
		lj.mu.Lock()
		if lj.subs != nil {
			delete(lj.subs, ch)
		}
		lj.mu.Unlock()
	}
	return past, ch, cancel
}

// recordingOf is the record of the jam's hold, when there is one: the
// jam's own, which Vibium made and which carries the sound, or else the
// rig's record of the hold.
func (m *jams) recordingOf(ctx context.Context, j station.Jam) (station.Blob, bool) {
	if j.HoldID == "" {
		return station.Blob{}, false
	}
	bs, err := m.s.db.Blobs(ctx, station.BlobQuery{HoldID: j.HoldID, Limit: 10})
	if err != nil {
		return station.Blob{}, false
	}
	var first *station.Blob
	for i, b := range bs {
		if b.Type != "application/zip" {
			continue
		}
		if b.Uploader == jamSubject(j.ID) {
			return b, true
		}
		if first == nil {
			first = &bs[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return station.Blob{}, false
}

// brief shortens a long line for a list: at most n characters, cut at a
// word where one is near, with an ellipsis.
func brief(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := n - 1
	for i := cut; i > cut-20 && i > 0; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(r[:cut]), " ,.;:") + "…"
}

// Handlers.

// off answers for the Jams pages and API while Jams is off.
func (s *server) jamsOff(w http.ResponseWriter, r *http.Request) bool {
	if s.jams.on() {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		apiError(w, http.StatusNotFound, "Jams is off: set a model provider on Settings")
	} else {
		http.Error(w, "Jams is off. A model provider on Settings turns it on.", http.StatusNotFound)
	}
	return true
}

// eventView is an event as the page renders it.
type eventView struct {
	Seq     int
	When    string
	Kind    string
	Text    string
	Tool    string
	Args    string
	Error   bool
	Image   string
	Verdict *jam.Verdict
	Word    string
	In, Out int
}

func eventViews(events []station.JamEvent) []eventView {
	var out []eventView
	for _, e := range events {
		var ev jam.Event
		json.Unmarshal(e.Data, &ev)
		v := eventView{Seq: e.Seq, When: e.At.Local().Format("15:04:05"), Kind: e.Kind, Text: ev.Text, Tool: ev.Tool, Error: ev.Error, Image: ev.Image, Verdict: ev.Verdict, In: ev.In, Out: ev.Out}
		if len(ev.Args) > 0 && string(ev.Args) != "{}" {
			v.Args = string(ev.Args)
		}
		if ev.Verdict != nil {
			v.Word = ev.Verdict.Word()
		}
		out = append(out, v)
	}
	return out
}

// homeData is what the front page shows: the prompt box with its
// pickers and examples.
func (s *server) homeData(r *http.Request, p station.Principal, extra map[string]any) map[string]any {
	devs, _ := s.visibleDevices(r.Context(), p)
	var online []deviceView
	for _, d := range devs {
		if d.ID != "" && d.Online && d.Level.AtLeast(station.LevelJam) && (p.Device == "" || p.Device == d.ID) {
			online = append(online, d)
		}
	}
	kind := "phone"
	if len(online) > 0 {
		kind = online[0].Kind
	}
	agents := s.jams.models.OfRole(jam.RoleAgent)
	data := map[string]any{"Page": "home", "Principal": p, "CSRF": s.auth.csrfToken(r), "Devices": online, "Models": agents, "Examples": examplesFor(kind), "Ask": greeting()}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

// jamsData is what the Jams page shows: the list.
func (s *server) jamsData(r *http.Request, p station.Principal) map[string]any {
	ctx := r.Context()
	devs, _ := s.devices(ctx)
	list, _ := s.db.Jams(ctx, "", 200)
	return map[string]any{"Page": "jams", "Principal": p, "CSRF": s.auth.csrfToken(r), "Jams": jamViews(s.visibleJams(ctx, p, list), devs)}
}

type jamView struct {
	station.Jam
	Label string
	When  string
	Word  string
	Open  bool
}

func jamViews(list []station.Jam, devs []deviceView) []jamView {
	labels := map[string]string{}
	for _, d := range devs {
		labels[d.ID] = d.Title
	}
	var out []jamView
	for _, j := range list {
		v := jamView{Jam: j, Label: labels[j.DeviceID], When: j.CreatedAt.Local().Format("Jan 2 15:04"), Open: j.Open()}
		if v.Label == "" {
			v.Label = j.DeviceID
		}
		if j.Result != "" {
			v.Word = (jam.Verdict{Status: j.Result}).Word()
		}
		out = append(out, v)
	}
	return out
}

// jamsPage is GET /jams, the list.
func (s *server) jamsPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	s.render(w, "jams", s.jamsData(r, p))
}

// startJamForm is POST /jams from the page.
func (s *server) startJamForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	if p.ReadOnly {
		s.render(w, "home", s.homeData(r, p, map[string]any{"Message": "A read-only sign-in can watch jams, not start them.", "Prompt": r.FormValue("prompt")}))
		return
	}
	j, err := s.jams.start(r.Context(), p, r.FormValue("prompt"), r.FormValue("device"), r.FormValue("model"), r.FormValue("mode"))
	if err != nil {
		s.render(w, "home", s.homeData(r, p, map[string]any{"Message": err.Error(), "Prompt": r.FormValue("prompt")}))
		return
	}
	http.Redirect(w, r, "/jams/"+j.ID, http.StatusSeeOther)
}

// jamPage is GET /jams/{id}.
func (s *server) jamPage(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	ctx := r.Context()
	j, live, err := s.jams.get(ctx, p, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	events, _ := s.db.JamEvents(ctx, j.ID, 0)
	views := eventViews(events)
	last := 0
	if len(events) > 0 {
		last = events[len(events)-1].Seq
	}
	dev, _ := s.db.Device(ctx, j.DeviceID)
	label := j.DeviceID
	if dev.ID != "" {
		label = dev.Title()
	}
	data := map[string]any{"Page": "jams", "Principal": p, "CSRF": s.auth.csrfToken(r), "Jam": j, "Open": live, "Events": views, "Last": last,
		"DeviceLabel": label, "When": j.CreatedAt.Local().Format("Jan 2, 2006 15:04"), "Took": j.Took, "Message": r.URL.Query().Get("msg")}
	if j.Result != "" {
		data["Word"] = (jam.Verdict{Status: j.Result}).Word()
	}
	if live {
		data["GraceUntil"] = s.jams.graceUntil(j.ID)
		data["GraceSeconds"] = int(jamGrace / time.Second)
	}
	rigID, _, _ := station.SplitDeviceID(j.DeviceID)
	if live && s.tunnel.IsOnline(rigID) {
		data["Watch"] = "/devices/" + j.DeviceID + "/view?minimal=1"
	}
	if b, ok := s.jams.recordingOf(ctx, j); ok {
		data["Recording"] = b
		data["Play"] = "/records/" + b.SHA256 + "/play"
	}
	s.render(w, "jam", data)
}

// jamTurnForm is POST /jams/{id}/turns from the page.
func (s *server) jamTurnForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.jams.act(r.Context(), p, id); err != nil {
		jamFormError(w, r, err)
		return
	}
	if err := s.jams.turn(id, r.FormValue("prompt")); err != nil {
		http.Redirect(w, r, "/jams/"+id+"?msg="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/jams/"+id, http.StatusSeeOther)
}

// jamKeepForm is POST /jams/{id}/keep from the page: the watcher keeps
// a jam open through the grace after a settled result.
func (s *server) jamKeepForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.jams.act(r.Context(), p, id); err != nil {
		jamFormError(w, r, err)
		return
	}
	s.jams.keep(id)
	http.Redirect(w, r, "/jams/"+id, http.StatusSeeOther)
}

// jamStopForm is POST /jams/{id}/stop from the page.
func (s *server) jamStopForm(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.jams.act(r.Context(), p, id); err != nil {
		jamFormError(w, r, err)
		return
	}
	s.jams.stop(id, "stopped by "+p.Subject)
	s.waitEnded(id, 3*time.Second)
	http.Redirect(w, r, "/jams/"+id, http.StatusSeeOther)
}

// waitEnded gives a stopped jam a moment to write its end, so the page
// that follows shows it.
func (s *server) waitEnded(id string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.jams.mu.Lock()
		_, live := s.jams.live[id]
		s.jams.mu.Unlock()
		if !live {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// jamShot is GET /jams/{id}/shots/{seq}.png.
func (s *server) jamShot(w http.ResponseWriter, r *http.Request, p station.Principal) {
	id := r.PathValue("id")
	if _, _, err := s.jams.get(r.Context(), p, id); err != nil {
		http.NotFound(w, r)
		return
	}
	seq, err := strconv.Atoi(strings.TrimSuffix(r.PathValue("seq"), ".png"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, filepath.Join(s.jams.dir, id, strconv.Itoa(seq)+".png"))
}

// API.

// listModels is GET /v1/models: names and roles, never a key.
func (s *server) listModels(w http.ResponseWriter, r *http.Request, _ station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	list, _ := s.jams.models.List()
	out := []map[string]any{}
	for _, c := range list {
		out = append(out, map[string]any{"name": c.Name, "role": c.Role, "provider": c.Provider, "model": c.Model, "default": c.Default})
	}
	apiJSON(w, out)
}

// startJam is POST /v1/jams.
func (s *server) startJam(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	var body struct {
		Prompt, Device, Model, Mode string
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "a JSON object with prompt, and device, model and mode when wanted")
		return
	}
	j, err := s.jams.start(r.Context(), p, body.Prompt, body.Device, body.Model, body.Mode)
	if errors.Is(err, station.ErrForbidden) {
		apiError(w, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(j)
}

// listJams is GET /v1/jams.
func (s *server) listJams(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	list, err := s.db.Jams(r.Context(), "", 400)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiJSON(w, s.visibleJams(r.Context(), p, list))
}

// getJam is GET /v1/jams/{id}: the jam, its transcript, and the
// recording's hash.
func (s *server) getJam(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	j, _, err := s.jams.get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusNotFound, "no such jam")
		return
	}
	events, _ := s.db.JamEvents(r.Context(), j.ID, 0)
	if events == nil {
		events = []station.JamEvent{}
	}
	out := map[string]any{"jam": j, "events": events}
	if b, ok := s.jams.recordingOf(r.Context(), j); ok {
		out["recording"] = b.SHA256
	}
	apiJSON(w, out)
}

// jamTurn is POST /v1/jams/{id}/turns.
func (s *server) jamTurn(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.jams.act(r.Context(), p, id); err != nil {
		jamActError(w, err)
		return
	}
	var body struct{ Prompt string }
	json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&body)
	switch err := s.jams.turn(id, body.Prompt); {
	case errors.Is(err, jam.ErrEnded):
		apiError(w, http.StatusConflict, "the jam has ended")
	case errors.Is(err, jam.ErrBusy):
		apiError(w, http.StatusConflict, "a prompt is already waiting")
	case err != nil:
		apiError(w, http.StatusBadRequest, err.Error())
	default:
		apiJSON(w, map[string]any{"queued": true})
	}
}

// keepJam is POST /v1/jams/{id}/keep.
func (s *server) keepJam(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	j, _, err := s.jams.act(r.Context(), p, id)
	if err != nil {
		jamActError(w, err)
		return
	}
	apiJSON(w, map[string]any{"kept": s.jams.keep(id), "jam": j})
}

// stopJam is POST /v1/jams/{id}/stop.
func (s *server) stopJam(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.jams.act(r.Context(), p, id); err != nil {
		jamActError(w, err)
		return
	}
	stopped := s.jams.stop(id, "stopped by "+p.Subject)
	if stopped {
		s.waitEnded(id, 3*time.Second)
	}
	j, _, _ := s.jams.get(r.Context(), p, id)
	apiJSON(w, map[string]any{"stopped": stopped, "jam": j})
}

// jamEvents is GET /v1/jams/{id}/events: the transcript as server-sent
// events from the start, or after ?after=<seq> or Last-Event-ID, then
// live until the jam ends; a finished jam replays and closes.
// forbidden is a refusal that reads as its message and is ErrForbidden
// to errors.Is, so the API answers 403 and the page shows the words.
type forbidden string

func (f forbidden) Error() string      { return string(f) }
func (forbidden) Is(target error) bool { return target == station.ErrForbidden }

// jamFormError is jamActError for the page's forms.
func jamFormError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, station.ErrForbidden) {
		http.Error(w, "watching a jam does not let you steer it", http.StatusForbidden)
		return
	}
	http.NotFound(w, r)
}

// jamActError words a refusal to act on a jam: hidden when they may not
// even see it, forbidden when they may watch it but not steer it.
func jamActError(w http.ResponseWriter, err error) {
	if errors.Is(err, station.ErrForbidden) {
		apiError(w, http.StatusForbidden, "watching a jam does not let you steer it")
		return
	}
	apiError(w, http.StatusNotFound, "no such jam")
}

func (s *server) jamEvents(w http.ResponseWriter, r *http.Request, p station.Principal) {
	if s.jamsOff(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, _, err := s.jams.get(r.Context(), p, id); err != nil {
		apiError(w, http.StatusNotFound, "no such jam")
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	if v, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil {
		after = v
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, "streaming is not supported here")
		return
	}
	past, ch, cancel := s.jams.subscribe(r.Context(), id, after)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(e station.JamEvent) {
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, e.Data)
	}
	last := after
	for _, e := range past {
		send(e)
		last = e.Seq
	}
	flusher.Flush()
	if ch == nil {
		return
	}
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": keep\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Seq <= last {
				continue
			}
			send(e)
			last = e.Seq
			flusher.Flush()
			if e.Kind == "end" {
				return
			}
		}
	}
}
