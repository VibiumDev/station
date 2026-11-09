// Package station is the model Vibium Station's packages share: rigs,
// devices, holds, principals and scopes, and the five interfaces an
// embedding application implements. Station's code never names a kind of
// device; what a rig offers is data.
package station

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
)

// EventStore keeps every event the station signs or accepts, verbatim. It
// is khatru's event-store interface, so the same store can back a relay
// later without a migration.
type EventStore = eventstore.Store

// Scope is what a hold grant allows on a device.
type Scope string

const (
	ScopeView  Scope = "view"  // video, speaker audio, screenshots, MJPEG, status
	ScopeInput Scope = "input" // HID input and the microphone
	ScopeBidi  Scope = "bidi"  // WebDriver BiDi
	ScopeAdmin Scope = "admin" // settings, resets, logs; only from a station the rig policy trusts with it
)

// AllScopes lists the scopes in their canonical order.
var AllScopes = []Scope{ScopeView, ScopeInput, ScopeBidi, ScopeAdmin}

// ParseScope checks that s names a scope.
func ParseScope(s string) (Scope, error) {
	for _, k := range AllScopes {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown scope %q", s)
}

// ParseScopes parses a list, refusing unknown and repeated scopes.
func ParseScopes(list []string) ([]Scope, error) {
	var out []Scope
	for _, s := range list {
		k, err := ParseScope(s)
		if err != nil {
			return nil, err
		}
		if HasScope(out, k) {
			return nil, fmt.Errorf("scope %q repeated", s)
		}
		out = append(out, k)
	}
	return out, nil
}

// HasScope says whether scopes contains s.
func HasScope(scopes []Scope, s Scope) bool {
	for _, k := range scopes {
		if k == s {
			return true
		}
	}
	return false
}

// ScopeStrings is the list as strings, for tags and JSON.
func ScopeStrings(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// Capabilities a device can announce.
const (
	CapVideo = "video"
	CapAudio = "audio"
	CapHID   = "hid"
	CapBidi  = "bidi"
	CapMJPEG = "mjpeg"
)

// Shortcut is a toolbar button the device's provider asks the shared
// viewer to show: a label and the key chord to send, in the viewer's key
// syntax ("GUI+h").
type Shortcut struct {
	Label string `json:"label"`
	Keys  string `json:"keys"`
}

// Device is one thing a rig offers.
type Device struct {
	ID        string     `json:"id"`            // "<rig id>/<name>"
	Kind      string     `json:"kind"`          // "phone", "browser", ...
	Caps      []string   `json:"caps"`          // see the Cap constants
	Key       string     `json:"key,omitempty"` // hex public key, when the device has its own
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
	// Input is how the device is best driven, "touch" or "mouse": the
	// viewer's pointer mode until the person picks one. Empty leaves it
	// to the viewer.
	Input string `json:"input,omitempty"`
	// Extras is the path on the rig of a script that adds the device's
	// own buttons and panels to the shared viewer; empty when none.
	Extras string `json:"extras,omitempty"`
	// Label is the device's name for people, such as "Dev iPhone 15",
	// when the id's name ("phone") is not one. Empty shows the name.
	Label string `json:"label,omitempty"`
	// Platform is what the device runs: "ios", "android", "macos",
	// "windows", "linux", or empty when the kind says enough.
	Platform string `json:"platform,omitempty"`
	// Model is the device's model name, such as "iPhone 15 Pro Max",
	// and OSVersion the version of what it runs, such as "26.6", both
	// as the rig was told; empty when it was not.
	Model     string `json:"model,omitempty"`
	OSVersion string `json:"os_version,omitempty"`
}

// Title is the device's name for people: its label, else its model,
// else its name in the address.
func (d Device) Title() string {
	switch {
	case d.Label != "":
		return d.Label
	case d.Model != "":
		return d.Model
	}
	return d.Name()
}

// Name is the device's name inside its rig.
func (d Device) Name() string {
	_, name, _ := SplitDeviceID(d.ID)
	return name
}

// Has says whether the device announced a capability.
func (d Device) Has(cap string) bool {
	for _, c := range d.Caps {
		if c == cap {
			return true
		}
	}
	return false
}

// DeviceID joins a rig id and a device name.
func DeviceID(rigID, name string) string { return rigID + "/" + name }

// SplitDeviceID takes a device id apart.
func SplitDeviceID(id string) (rigID, name string, ok bool) {
	rigID, name, ok = strings.Cut(id, "/")
	if !ok || rigID == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return rigID, name, true
}

// Rig is an enrolled machine: one key, one tunnel, one or more devices.
type Rig struct {
	ID         string
	Name       string
	PubKey     nostr.PubKey
	EnrolledAt time.Time
	Devices    []Device
}

// Principal is who a request comes from, as the Authenticator saw it.
type Principal struct {
	Subject  string // "owner", "token:<name>", or a login such as you@example.com
	ReadOnly bool   // may view, never control
	Admin    bool   // sees and manages everything; a regular user sees what their teams are given
	// Token is the token's level when the principal is one, "" for a
	// person: watch (read only), drive (everything on devices, jams and
	// agents included), or admin (the station's management too).
	Token TokenLevel
	// Device, when set, is the one device the principal may act on: a
	// token minted for a jam carries the jam's device.
	Device string
	// User is the id of the user behind the principal, a person signed
	// in or the owner of a token, and Teams the ids of the teams they
	// are in, which decide what a regular user may see and do.
	User  string
	Teams []string
}

// Manages says whether the principal wields an admin's power to change
// what is not theirs: an admin in person, or an admin's admin token.
// An admin's watch or drive token sees everything an admin sees and
// changes only what the admin themselves made.
func (p Principal) Manages() bool {
	return p.Admin && (p.Token == "" || p.Token == TokenAdmin)
}

// User is someone who signs in: an admin, who sees and manages
// everything, or a regular user, who sees what their teams are given.
type User struct {
	ID        string    `json:"id"`
	Login     string    `json:"login"` // lowercased; an email address as a rule
	Name      string    `json:"name,omitempty"`
	Admin     bool      `json:"admin"`
	Locked    bool      `json:"locked,omitempty"` // cannot sign in; sessions and tokens stop
	CreatedAt time.Time `json:"created"`
	LastLogin time.Time `json:"last_login,omitempty"`
	Teams     []string  `json:"teams,omitempty"` // team ids
	// How they sign in by password: whether one is set, and whether an
	// authenticator confirmed since.
	HasPassword bool      `json:"has_password"`
	TwoFactor   bool      `json:"two_factor"`
	TwoFactorAt time.Time `json:"two_factor_at,omitempty"`
}

// Invite is a setup link: one user's single-use way to choose a
// password and set up an authenticator, good until it expires.
type Invite struct {
	UserID    string
	ExpiresAt time.Time
	By        string // who made it
}

// OwnerLogin is the login of the first admin, whose password `station
// passwd` sets.
const OwnerLogin = "owner"

// TokenLevel is what an API token may do, within what its user may:
// watch (read only), drive (hold and drive devices, start jams and run
// agents, as the user's teams allow), or admin (manage the station as
// well, for an admin's token). Most tokens are drive.
type TokenLevel string

const (
	TokenWatch TokenLevel = "watch"
	TokenDrive TokenLevel = "drive"
	TokenAdmin TokenLevel = "admin"
)

// ParseTokenLevel reads a token level's name; "" is drive.
func ParseTokenLevel(s string) (TokenLevel, error) {
	switch TokenLevel(s) {
	case "", TokenDrive:
		return TokenDrive, nil
	case TokenWatch, TokenAdmin:
		return TokenLevel(s), nil
	case "read-only", "view":
		return TokenWatch, nil
	}
	return "", errors.New("a token's level is watch, drive or admin")
}

// Team groups users; devices are shared with teams.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Members   []string  `json:"members,omitempty"` // user ids
	CreatedAt time.Time `json:"created"`
}

// Level is what a team may do with a device: watch it, drive it, or
// also start jams and agents on it. Each level includes the ones
// before; the empty level is no access.
type Level string

const (
	LevelNone  Level = ""
	LevelWatch Level = "watch"
	LevelDrive Level = "drive"
	LevelJam   Level = "jam"
)

// Rank orders the levels, none lowest.
func (l Level) Rank() int {
	switch l {
	case LevelWatch:
		return 1
	case LevelDrive:
		return 2
	case LevelJam:
		return 3
	}
	return 0
}

// AtLeast says whether l includes what want allows.
func (l Level) AtLeast(want Level) bool { return l.Rank() >= want.Rank() }

// ParseLevel reads a level's name; "" and "none" are no access.
func ParseLevel(s string) (Level, error) {
	switch Level(s) {
	case LevelNone, "none":
		return LevelNone, nil
	case LevelWatch, LevelDrive, LevelJam:
		return Level(s), nil
	}
	return LevelNone, errors.New("a level is watch, drive, jam or none")
}

// Access is one team's level on one device.
type Access struct {
	DeviceID string `json:"device"`
	TeamID   string `json:"team"`
	Level    Level  `json:"level"`
}

// UserStore keeps users, teams, and what teams may do with devices.
type UserStore interface {
	Users(ctx context.Context) ([]User, error)
	User(ctx context.Context, id string) (User, error)
	UserByLogin(ctx context.Context, login string) (User, error)
	// PutUser creates a user, or updates everything but the password,
	// the second factor and the teams of one that exists.
	PutUser(ctx context.Context, u User) error
	RemoveUser(ctx context.Context, id string) error
	SetPassword(ctx context.Context, id, hash string) error
	PasswordHash(ctx context.Context, id string) (string, error)
	TouchUser(ctx context.Context, id string, at time.Time) error
	// The second factor: an authenticator secret once a code confirmed
	// it, the last time step a code was accepted for, and backup codes
	// kept as hashes, each good once.
	SetSecondFactor(ctx context.Context, id, secret string, at time.Time) error
	SecondFactor(ctx context.Context, id string) (secret string, lastStep int64, err error)
	// AcceptStep remembers a step a code was accepted for, and says no
	// when one at or after it was already used.
	AcceptStep(ctx context.Context, id string, step int64) (bool, error)
	SetBackupCodes(ctx context.Context, id string, hashes []string) error
	UseBackupCode(ctx context.Context, id, hash string) (left int, ok bool, err error)
	BackupCodesLeft(ctx context.Context, id string) (int, error)
	// ResetSignIn forgets a user's password, authenticator and backup codes.
	ResetSignIn(ctx context.Context, id string) error
	// Setup links, kept as hashes; a user has at most one open.
	PutInvite(ctx context.Context, hash string, inv Invite) error
	Invite(ctx context.Context, hash string, now time.Time) (Invite, error)
	UseInvite(ctx context.Context, hash string) error
	OpenInvites(ctx context.Context, now time.Time) (map[string]time.Time, error)
	Teams(ctx context.Context) ([]Team, error)
	PutTeam(ctx context.Context, t Team) error
	RemoveTeam(ctx context.Context, id string) error
	SetMember(ctx context.Context, teamID, userID string, in bool) error
	Accesses(ctx context.Context) ([]Access, error)
	// SetAccess sets a team's level on a device; LevelNone takes it away.
	SetAccess(ctx context.Context, a Access) error
	// LevelFor is the highest level any of the teams has on the device.
	LevelFor(ctx context.Context, teams []string, deviceID string) (Level, error)
	// BestLevel is the highest level any of the teams has on any device.
	BestLevel(ctx context.Context, teams []string) (Level, error)
}

// Hold is control of one device by one subject, numbered per device so
// a newer hold always wins on the rig.
type Hold struct {
	ID         string
	DeviceID   string
	RigID      string
	Seq        uint64
	Subject    string
	Scopes     []Scope
	CreatedAt  time.Time
	ActiveAt   time.Time // the last refresh or input
	ExpiresAt  time.Time // the hard end
	ReleasedAt time.Time // zero while held
}

// Held says whether the hold is still in force at t.
func (l Hold) Held(t time.Time) bool {
	return l.ReleasedAt.IsZero() && t.Before(l.ExpiresAt)
}

// Action is what a principal wants to do, for the Authorizer.
type Action string

const (
	ActionView    Action = "view"    // open the viewer, screenshots, status
	ActionControl Action = "control" // take a hold with input
	ActionBidi    Action = "bidi"    // drive the device with an agent
	ActionAdmin   Action = "admin"   // the device's admin endpoints
	ActionManage  Action = "manage"  // enroll and remove rigs, codes, tokens
)

// Errors the interfaces return.
var (
	ErrUnauthenticated = errors.New("not signed in")
	ErrForbidden       = errors.New("not allowed")
	ErrNotFound        = errors.New("not found")
	ErrNoHold          = errors.New("no hold")
)

// Authenticator says who a request comes from.
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

// Authorizer says whether a principal may do an action to a device
// (deviceID is empty for ActionManage).
type Authorizer interface {
	Authorize(ctx context.Context, p Principal, action Action, deviceID string) error
}

// DeviceStore holds rigs and their devices.
type DeviceStore interface {
	Rigs(ctx context.Context) ([]Rig, error)
	Rig(ctx context.Context, id string) (Rig, error)
	RigByKey(ctx context.Context, pk nostr.PubKey) (Rig, error)
	PutRig(ctx context.Context, rig Rig) error
	RemoveRig(ctx context.Context, id string) error
	// SetDevices replaces a rig's devices with what it last announced.
	SetDevices(ctx context.Context, rigID string, devices []Device) error
	Device(ctx context.Context, id string) (Device, error)
}

// HoldStore holds holds and each device's sequence counter.
type HoldStore interface {
	// Current is the hold held on a device, or ErrNoHold.
	Current(ctx context.Context, deviceID string) (Hold, error)
	// Create stores a new hold with the device's next sequence number and
	// returns it with Seq set.
	Create(ctx context.Context, hold Hold) (Hold, error)
	Hold(ctx context.Context, id string) (Hold, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Release(ctx context.Context, id string, at time.Time) error
	// Held lists every hold still held.
	Held(ctx context.Context) ([]Hold, error)
}

// AuditEntry is one thing that happened.
type AuditEntry struct {
	At       time.Time
	Subject  string
	Action   string
	DeviceID string
	HoldID   string
	Detail   string
}

// AuditSink receives audit entries.
type AuditSink interface {
	Audit(ctx context.Context, e AuditEntry)
}

// NewID makes a random id such as "rig_3f9a1c02".
func NewID(prefix string) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// Jam is one jam session: a prompt a person typed on the station, driven
// on a device by a model through the station's own loop. See
// docs/specs/jams.md.
type Jam struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"` // who started it
	DeviceID  string    `json:"device"`  // "<rig id>/<name>"
	Model     string    `json:"model"`   // the agent model's name on Settings
	Prompt    string    `json:"prompt"`  // the first prompt
	Mode      string    `json:"mode"`    // "check", "run", or "" when the model picks
	Status    string    `json:"status"`  // running, done, stopped, failed
	Took      string    `json:"took"`    // "check" or "run", once the model has chosen
	Result    string    `json:"result"`  // passed, failed, inconclusive, completed, not_completed
	Summary   string    `json:"summary"` // the model's explanation of the result
	HoldID    string    `json:"hold"`    // the hold the jam drove the device on
	Actions   int       `json:"actions"` // tool calls made
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created"`
	EndedAt   time.Time `json:"ended,omitempty"`
}

// Open says whether the jam is still running.
func (j Jam) Open() bool { return j.Status == "running" }

// JamEvent is one step of a jam as the page shows it and the transcript
// keeps it: a prompt, the model's text, a tool call and its result, a
// screenshot, the verdict, an error, the end.
type JamEvent struct {
	JamID string          `json:"-"`
	Seq   int             `json:"seq"`
	At    time.Time       `json:"at"`
	Kind  string          `json:"kind"`
	Data  json.RawMessage `json:"data"`
}

// Blob is a file in the Record Store, named by its SHA-256: a session
// recording, a screenshot, a log. The bytes live in a blob store; this
// is what the station knows about them.
type Blob struct {
	SHA256    string
	Size      int64
	Type      string // the media type
	Name      string // a file name hint, such as record.zip
	Uploader  string // "owner", "token:<name>", a login, or "rig:<rig id>"
	User      string // the id of the user behind the uploader, when a person or their token uploaded it
	PubKey    string // hex: the key that signed the upload
	HoldID    string
	DeviceID  string
	RigID     string
	EventID   string // the file event (kind 1063) the station signed for it
	Public    bool   // readable without any authorization
	CreatedAt time.Time
	// What a recording holds, read once when it is unpacked: its steps,
	// its frames, how long it ran, and the file of its last frame.
	Steps    int
	Frames   int
	Duration time.Duration
	Thumb    string
	// Golden marks a record worth keeping in view, the station's word
	// for a pin or a bookmark; Tags are the words people filed it under.
	Golden bool
	Tags   []string
}

// TagCount is a tag in use and how many blobs carry it.
type TagCount struct {
	Tag string
	N   int
}

// BlobQuery narrows a listing of blobs; the zero value lists them all,
// newest first.
type BlobQuery struct {
	DeviceID    string
	HoldID      string
	PubKey      string
	Before      time.Time // only blobs stored before this time
	OldestFirst bool
	Limit       int
	// Text keeps the blobs whose name, type, uploader, hold, device,
	// rig, hash or tags contain every word of it; Tags those carrying
	// every tag named; Golden the golden ones.
	Text   string
	Tags   []string
	Golden bool
}

// BlobIndex is the Record Store's index of what the blob store holds.
type BlobIndex interface {
	PutBlob(ctx context.Context, b Blob) error
	Blob(ctx context.Context, sha256 string) (Blob, error)
	Blobs(ctx context.Context, q BlobQuery) ([]Blob, error)
	DeleteBlob(ctx context.Context, sha256 string) error
	SetBlobPublic(ctx context.Context, sha256 string, public bool) error
	SetBlobGolden(ctx context.Context, sha256 string, golden bool) error
	// SetBlobTags replaces a blob's tags.
	SetBlobTags(ctx context.Context, sha256 string, tags []string) error
	// BlobTagCounts lists the tags in use, most used first.
	BlobTagCounts(ctx context.Context) ([]TagCount, error)
	// BlobTotal is how many blobs the index holds and their size in bytes.
	BlobTotal(ctx context.Context) (count int, size int64, err error)
}
