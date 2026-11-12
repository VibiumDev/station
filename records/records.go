// Package records is the Record Store: session recordings, screenshots
// and logs kept as Blossom blobs, each named by its SHA-256, uploaded
// with a signed authorization and listed per device and hold.
//
// Agents never hold keys. The station signs an upload authorization on
// the account's behalf (UploadAuth), the agent sends it with the file,
// and the store sees an ordinary Blossom upload. A rig signs its own
// uploads with its rig key. For every blob the station signs a file
// event (kind 1063) that names the hash, the hold, the device and the
// uploader, which stays in the event store after retention removes the
// bytes.
package records

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/blob"
)

const (
	// KindAuth is Blossom's authorization event (BUD-01).
	KindAuth nostr.Kind = 24242
	// KindFile is NIP-94 file metadata, the station's index entry for a blob.
	KindFile nostr.Kind = 1063
	// AuthLifetime is how long an authorization the station signs is good for.
	AuthLifetime = 5 * time.Minute
	// RecentHolds is how far back an upload that names no hold looks for
	// the uploader's last one.
	RecentHolds = 24 * time.Hour
)

// Signer signs on the account's behalf: the station key in single-player
// Station, a key per customer in a hosted service.
type Signer interface {
	Sign(ev *nostr.Event) error
	PubKey() nostr.PubKey
}

// Key is a Signer holding a secret key.
type Key nostr.SecretKey

func (k Key) Sign(ev *nostr.Event) error { return ev.Sign(nostr.SecretKey(k)) }
func (k Key) PubKey() nostr.PubKey       { return nostr.SecretKey(k).Public() }

// Store is the Record Store.
type Store struct {
	Blobs   blob.Store
	Index   station.BlobIndex
	Events  station.EventStore
	Holds   station.HoldStore
	Devices station.DeviceStore
	Signer  Signer
	// HoldsOf lists a subject's holds since a time, newest first, for an
	// upload that names no hold.
	HoldsOf func(ctx context.Context, subject string, since time.Time) ([]station.Hold, error)
	// BaseURL is where blobs are fetched: BaseURL/<sha256>.<ext>.
	BaseURL string
	// UnpackDir keeps each recording unpacked beside its zip; empty keeps
	// none, and cards then show no picture.
	UnpackDir string
	// Limits; zero means none.
	MaxFile  int64
	MaxTotal int64
	MaxAge   time.Duration
	Audit    station.AuditSink // optional
	Now      func() time.Time  // optional, for tests
	// Access says whether a principal may see a device's records, as a
	// station with teams decides from the device's sharing; nil means
	// only admins, uploaders and holders may, besides public files.
	Access func(ctx context.Context, p station.Principal, deviceID string) bool
}

// Error is a refusal with the HTTP status it maps to.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(status int, format string, args ...any) error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) audit(ctx context.Context, e station.AuditEntry) {
	if s.Audit != nil {
		if e.At.IsZero() {
			e.At = s.now()
		}
		s.Audit.Audit(ctx, e)
	}
}

// UploadRequest is what an agent asks an authorization for.
type UploadRequest struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Type   string `json:"type,omitempty"`
	Name   string `json:"name,omitempty"`
	HoldID string `json:"hold,omitempty"`
}

// Descriptor is a blob as Blossom describes it (BUD-02), with what the
// station knows besides.
type Descriptor struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
	Uploaded int64  `json:"uploaded"`
	Name     string `json:"name,omitempty"`
	Uploader string `json:"uploader,omitempty"`
	Hold     string `json:"hold,omitempty"`
	Device   string `json:"device,omitempty"`
	Public   bool   `json:"public,omitempty"`
	Event    string `json:"event,omitempty"`
	// Golden is the station's word for a pinned or bookmarked record;
	// Tags are the words people filed it under.
	Golden bool     `json:"golden,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	// A recording's contents, once unpacked.
	Steps      int    `json:"steps,omitempty"`
	Frames     int    `json:"frames,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Files      string `json:"files,omitempty"`
}

// Describe is a blob's descriptor.
func (s *Store) Describe(b station.Blob) Descriptor {
	d := Descriptor{URL: s.URL(b), SHA256: b.SHA256, Size: b.Size, Type: b.Type, Uploaded: b.CreatedAt.Unix(),
		Name: b.Name, Uploader: b.Uploader, Hold: b.HoldID, Device: b.DeviceID, Public: b.Public, Event: b.EventID,
		Steps: b.Steps, Frames: b.Frames, DurationMS: b.Duration.Milliseconds(), Golden: b.Golden, Tags: b.Tags}
	if b.Type == "application/zip" && s.UnpackDir != "" {
		d.Files = strings.TrimSuffix(s.BaseURL, "/") + "/records/" + b.SHA256 + "/files/"
	}
	return d
}

// URL is where a blob is fetched.
func (s *Store) URL(b station.Blob) string {
	return strings.TrimSuffix(s.BaseURL, "/") + "/" + b.SHA256 + blob.Extension(b.Type)
}

// holdFor finds the hold an upload belongs to: the one named, which must
// be the principal's own unless they are an admin, or else the
// principal's current hold, or their newest in the last day. An admin
// with none keeps the file on its own.
func (s *Store) holdFor(ctx context.Context, p station.Principal, holdID string) (station.Hold, error) {
	if holdID != "" {
		h, err := s.Holds.Hold(ctx, holdID)
		if errors.Is(err, station.ErrNotFound) {
			return h, refuse(http.StatusNotFound, "no hold %s", holdID)
		}
		if err != nil {
			return h, err
		}
		if h.Subject != p.Subject && !p.Admin {
			return h, refuse(http.StatusForbidden, "hold %s is not yours", holdID)
		}
		return h, nil
	}
	if s.HoldsOf == nil {
		return station.Hold{}, refuse(http.StatusBadRequest, "name the hold the recording belongs to")
	}
	hs, err := s.HoldsOf(ctx, p.Subject, s.now().Add(-RecentHolds))
	if err != nil {
		return station.Hold{}, err
	}
	var held []station.Hold
	for _, h := range hs {
		if h.Held(s.now()) {
			held = append(held, h)
		}
	}
	switch {
	case len(held) == 1:
		return held[0], nil
	case len(held) > 1:
		return station.Hold{}, refuse(http.StatusBadRequest, "you hold %d devices; name the hold", len(held))
	case len(hs) > 0:
		return hs[0], nil
	case p.Admin:
		// The owner may keep a file that belongs to no hold.
		return station.Hold{}, nil
	}
	return station.Hold{}, refuse(http.StatusBadRequest, "no hold of yours in the last day; name the hold")
}

func (s *Store) checkRoom(ctx context.Context, size int64) error {
	if size <= 0 {
		return refuse(http.StatusBadRequest, "a size is needed")
	}
	if s.MaxFile > 0 && size > s.MaxFile {
		return refuse(http.StatusRequestEntityTooLarge, "a file is at most %d bytes here", s.MaxFile)
	}
	if s.MaxTotal > 0 {
		_, total, err := s.Index.BlobTotal(ctx)
		if err != nil {
			return err
		}
		if total+size > s.MaxTotal {
			return refuse(http.StatusInsufficientStorage, "the store is full (%d of %d bytes)", total, s.MaxTotal)
		}
	}
	return nil
}

func nonce() nostr.Tag {
	var b [8]byte
	rand.Read(b[:])
	return nostr.Tag{"nonce", hex.EncodeToString(b[:])}
}

// UploadAuth signs an authorization for p to upload one file: Blossom's
// kind 24242 with t=upload and x=<sha256>, good for AuthLifetime, plus
// the station's own tags naming the uploader, the hold, the device and
// the rig, so the upload needs no lookup. The agent sends the event
// base64-encoded in "Authorization: Nostr ..." with the file.
func (s *Store) UploadAuth(ctx context.Context, p station.Principal, req UploadRequest) (nostr.Event, error) {
	if !blob.ValidSHA(req.SHA256) {
		return nostr.Event{}, refuse(http.StatusBadRequest, "sha256 must be 64 lowercase hex characters")
	}
	if err := s.checkRoom(ctx, req.Size); err != nil {
		return nostr.Event{}, err
	}
	h, err := s.holdFor(ctx, p, req.HoldID)
	if err != nil {
		return nostr.Event{}, err
	}
	now := s.now()
	ev := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(now.Unix()), Content: "Upload " + req.Name,
		Tags: nostr.Tags{
			{"t", "upload"},
			{"x", req.SHA256},
			{"size", strconv.FormatInt(req.Size, 10)},
			{"expiration", strconv.FormatInt(now.Add(AuthLifetime).Unix(), 10)},
			{"sub", p.Subject},
		}}
	if p.User != "" {
		ev.Tags = append(ev.Tags, nostr.Tag{"user", p.User})
	}
	for _, t := range [][2]string{{"hold", h.ID}, {"dev", h.DeviceID}, {"rig", h.RigID}} {
		if t[1] != "" {
			ev.Tags = append(ev.Tags, nostr.Tag{t[0], t[1]})
		}
	}
	if req.Type != "" {
		ev.Tags = append(ev.Tags, nostr.Tag{"m", req.Type})
	}
	if req.Name != "" {
		ev.Tags = append(ev.Tags, nostr.Tag{"name", req.Name})
	}
	ev.Tags = append(ev.Tags, nonce())
	if err := s.Signer.Sign(&ev); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// DownloadAuth signs an authorization to fetch one blob (t=get), for a
// principal who may read it, so the file can be fetched by something
// that has no station credential of its own.
func (s *Store) DownloadAuth(ctx context.Context, p station.Principal, sha string) (nostr.Event, error) {
	b, err := s.Index.Blob(ctx, sha)
	if errors.Is(err, station.ErrNotFound) {
		return nostr.Event{}, refuse(http.StatusNotFound, "no such blob")
	}
	if err != nil {
		return nostr.Event{}, err
	}
	if !s.mayRead(ctx, b, &p, nil) {
		return nostr.Event{}, refuse(http.StatusForbidden, "not yours to read")
	}
	now := s.now()
	ev := nostr.Event{Kind: KindAuth, CreatedAt: nostr.Timestamp(now.Unix()), Content: "Get " + b.Name,
		Tags: nostr.Tags{{"t", "get"}, {"x", sha}, {"expiration", strconv.FormatInt(now.Add(AuthLifetime).Unix(), 10)}, {"sub", p.Subject}, nonce()}}
	if err := s.Signer.Sign(&ev); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// Header is an event as the Authorization header carries it.
func Header(ev nostr.Event) string {
	data, _ := json.Marshal(ev)
	return "Nostr " + base64.StdEncoding.EncodeToString(data)
}

// ReadAuth reads a Blossom authorization from the request: nil without
// one, an error for one that is malformed, forged or expired.
func (s *Store) ReadAuth(r *http.Request) (*nostr.Event, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Nostr ") {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[6:]))
	if err != nil {
		return nil, refuse(http.StatusBadRequest, "the authorization is not base64")
	}
	var ev nostr.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, refuse(http.StatusBadRequest, "the authorization is not an event")
	}
	if ev.Kind != KindAuth || !ev.CheckID() || !ev.VerifySignature() {
		return nil, refuse(http.StatusUnauthorized, "the authorization is not a signed kind 24242 event")
	}
	exp := ev.Tags.Find("expiration")
	if len(exp) < 2 {
		return nil, refuse(http.StatusUnauthorized, "the authorization has no expiration")
	}
	at, err := strconv.ParseInt(exp[1], 10, 64)
	if err != nil || time.Unix(at, 0).Before(s.now()) {
		return nil, refuse(http.StatusUnauthorized, "the authorization has expired")
	}
	if time.Unix(int64(ev.CreatedAt), 0).After(s.now().Add(time.Minute)) {
		return nil, refuse(http.StatusUnauthorized, "the authorization is from the future")
	}
	return &ev, nil
}

func verb(ev *nostr.Event) string {
	if t := ev.Tags.Find("t"); len(t) >= 2 {
		return t[1]
	}
	return ""
}

func tagValue(ev *nostr.Event, name string) string {
	if t := ev.Tags.Find(name); len(t) >= 2 {
		return t[1]
	}
	return ""
}

// origin is who an authorization speaks for, and what it may name.
type origin struct {
	user     string // the id of the user behind the uploader, when known
	uploader string // "token:ci", "owner", a login, or "rig:<id>"
	rig      station.Rig
	hold     station.Hold
	deviceID string
	rigID    string
}

// originOf says whose authorization this is: the account's own, which
// carries the uploader and the hold it was signed for, or an enrolled
// rig's, which may name only its own devices and holds.
func (s *Store) originOf(ctx context.Context, ev *nostr.Event) (origin, error) {
	if ev.PubKey == s.Signer.PubKey() {
		o := origin{uploader: tagValue(ev, "sub"), user: tagValue(ev, "user"), deviceID: tagValue(ev, "dev"), rigID: tagValue(ev, "rig")}
		if id := tagValue(ev, "hold"); id != "" {
			if h, err := s.Holds.Hold(ctx, id); err == nil {
				o.hold = h
			}
		}
		if o.rigID != "" {
			o.rig, _ = s.Devices.Rig(ctx, o.rigID)
		}
		if o.uploader == "" {
			o.uploader = "owner"
		}
		return o, nil
	}
	rig, err := s.Devices.RigByKey(ctx, ev.PubKey)
	if err != nil {
		return origin{}, refuse(http.StatusForbidden, "the authorization is not signed by this station or one of its rigs")
	}
	o := origin{uploader: "rig:" + rig.ID, rig: rig, rigID: rig.ID, deviceID: tagValue(ev, "dev")}
	if o.deviceID != "" {
		if r, _, ok := station.SplitDeviceID(o.deviceID); !ok || r != rig.ID {
			return origin{}, refuse(http.StatusForbidden, "%s is not one of rig %s's devices", o.deviceID, rig.ID)
		}
	}
	if id := tagValue(ev, "hold"); id != "" {
		h, err := s.Holds.Hold(ctx, id)
		if err != nil || h.RigID != rig.ID || (o.deviceID != "" && h.DeviceID != o.deviceID) {
			return origin{}, refuse(http.StatusForbidden, "hold %s is not on rig %s", id, rig.ID)
		}
		o.hold = h
		if o.deviceID == "" {
			o.deviceID = h.DeviceID
		}
	}
	return o, nil
}

func writeError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Status: http.StatusInternalServerError, Message: err.Error()}
	}
	w.Header().Set("X-Reason", e.Message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	json.NewEncoder(w).Encode(map[string]string{"error": e.Message})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ServeUpload is PUT /upload (BUD-02): the body is the file, and the
// Authorization header carries a kind 24242 event with t=upload and the
// file's hash in x, signed by the station (from UploadAuth) or by an
// enrolled rig. The bytes are hashed as they arrive and refused when
// they do not match.
func (s *Store) ServeUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	auth, err := s.ReadAuth(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if auth == nil {
		writeError(w, refuse(http.StatusUnauthorized, "send Authorization: Nostr <base64 event>; the station signs one at POST /v1/records/upload-auth"))
		return
	}
	if verb(auth) != "upload" {
		writeError(w, refuse(http.StatusForbidden, "the authorization is not for an upload"))
		return
	}
	sha := tagValue(auth, "x")
	if !blob.ValidSHA(sha) {
		writeError(w, refuse(http.StatusBadRequest, "the authorization names no file hash in x"))
		return
	}
	size := r.ContentLength
	if size <= 0 {
		writeError(w, refuse(http.StatusLengthRequired, "Content-Length is needed"))
		return
	}
	if want := tagValue(auth, "size"); want != "" && want != strconv.FormatInt(size, 10) {
		writeError(w, refuse(http.StatusBadRequest, "the body is %d bytes; the authorization was for %s", size, want))
		return
	}
	o, err := s.originOf(ctx, auth)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.checkRoom(ctx, size); err != nil {
		writeError(w, err)
		return
	}
	if err := s.Blobs.Put(sha, r.Body, size); err != nil {
		if errors.Is(err, blob.ErrMismatch) {
			writeError(w, refuse(http.StatusBadRequest, "the body does not hash to %s at %d bytes", sha, size))
			return
		}
		writeError(w, err)
		return
	}
	mediaType := r.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = tagValue(auth, "m")
	}
	if mediaType == "" || mediaType == "application/x-www-form-urlencoded" {
		mediaType = "application/octet-stream"
	}
	mediaType, _, _ = strings.Cut(mediaType, ";")
	b := station.Blob{SHA256: sha, Size: size, Type: strings.TrimSpace(mediaType), Name: tagValue(auth, "name"), Uploader: o.uploader, User: o.user,
		PubKey: auth.PubKey.Hex(), HoldID: o.hold.ID, DeviceID: o.deviceID, RigID: o.rigID, CreatedAt: s.now()}
	if prev, err := s.Index.Blob(ctx, sha); err == nil {
		// The same bytes again: the first upload's record stands.
		writeJSON(w, s.Describe(prev))
		return
	}
	fe, err := s.fileEvent(b, o.rig)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.Events.SaveEvent(fe); err != nil {
		writeError(w, err)
		return
	}
	b.EventID = fe.ID.Hex()
	if err := s.Index.PutBlob(ctx, b); err != nil {
		writeError(w, err)
		return
	}
	// A recording is unpacked now, once; a zip that is no recording is
	// kept as it came.
	if nb, err := s.Unpacked(ctx, b); err == nil {
		b = nb
	}
	s.audit(ctx, station.AuditEntry{Subject: o.uploader, Action: "upload", DeviceID: o.deviceID, HoldID: o.hold.ID,
		Detail: fmt.Sprintf("%s %s (%d bytes, %s)", b.Name, sha[:12], size, b.Type)})
	w.WriteHeader(http.StatusOK)
	writeJSON(w, s.Describe(b))
}

// fileEvent is the station's index entry for a blob: NIP-94 file
// metadata signed by the account key, naming the hash, size and type,
// where it is fetched, and the hold, device, rig and uploader it came
// from. It outlives the bytes.
func (s *Store) fileEvent(b station.Blob, rig station.Rig) (nostr.Event, error) {
	tags := nostr.Tags{
		{"x", b.SHA256},
		{"size", strconv.FormatInt(b.Size, 10)},
		{"m", b.Type},
		{"url", s.URL(b)},
		{"sub", b.Uploader},
	}
	if b.Name != "" {
		tags = append(tags, nostr.Tag{"name", b.Name})
	}
	if b.HoldID != "" {
		tags = append(tags, nostr.Tag{"hold", b.HoldID})
	}
	if b.DeviceID != "" {
		tags = append(tags, nostr.Tag{"dev", b.DeviceID})
	}
	if b.RigID != "" {
		tags = append(tags, nostr.Tag{"rig", b.RigID})
	}
	if rig.PubKey != nostr.ZeroPK {
		tags = append(tags, nostr.Tag{"p", rig.PubKey.Hex()})
	}
	ev := nostr.Event{Kind: KindFile, CreatedAt: nostr.Timestamp(b.CreatedAt.Unix()), Tags: tags, Content: b.Name}
	if err := s.Signer.Sign(&ev); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// mayRead says whether a blob may be fetched: public blobs by anyone;
// otherwise by an admin, the uploader, the hold's subject, or the holder
// of a t=get authorization for it signed by the station or by the rig
// that uploaded it.
func (s *Store) mayRead(ctx context.Context, b station.Blob, p *station.Principal, auth *nostr.Event) bool {
	if b.Public {
		return true
	}
	if p != nil && (p.Device == "" || p.Device == b.DeviceID) {
		if p.Admin || owns(p, b) {
			return true
		}
		if s.Access != nil && b.DeviceID != "" && s.Access(ctx, *p, b.DeviceID) {
			return true
		}
		if b.HoldID != "" {
			if h, err := s.Holds.Hold(ctx, b.HoldID); err == nil && h.Subject == p.Subject {
				return true
			}
		}
	}
	if auth != nil && verb(auth) == "get" && tagValue(auth, "x") == b.SHA256 {
		return auth.PubKey == s.Signer.PubKey() || auth.PubKey.Hex() == b.PubKey
	}
	return false
}

// owns says whether a blob is the principal's own: uploaded under their
// subject, or by them or a token of theirs, as the user behind it.
func owns(p *station.Principal, b station.Blob) bool {
	return p.Subject == b.Uploader || p.User != "" && p.User == b.User
}

// mayChange says whether a principal may delete, share or refile a blob:
// an admin's power, or their own, and never through a watch token.
func mayChange(p *station.Principal, b station.Blob) bool {
	return !p.ReadOnly && (p.Manages() || owns(p, b))
}

// MayRead says whether a principal may fetch a blob.
func (s *Store) MayRead(ctx context.Context, b station.Blob, p station.Principal) bool {
	return s.mayRead(ctx, b, &p, nil)
}

// mayDelete says who may remove a blob: an admin, the uploader, or a
// t=delete authorization for it signed by the station or the uploading
// rig.
func (s *Store) mayDelete(b station.Blob, p *station.Principal, auth *nostr.Event) bool {
	if p != nil && mayChange(p, b) {
		return true
	}
	if auth != nil && verb(auth) == "delete" && tagValue(auth, "x") == b.SHA256 {
		return auth.PubKey == s.Signer.PubKey() || auth.PubKey.Hex() == b.PubKey
	}
	return false
}

// inert are the media types a browser shows without running anything in
// them: pictures, video, sound, archives and plain text.
var inert = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	"video/mp4": true, "video/webm": true, "audio/wav": true, "audio/x-wav": true, "audio/mpeg": true,
	"application/zip": true, "application/json": true, "text/plain": true,
}

func inertType(mediaType string) bool {
	t, _, _ := strings.Cut(mediaType, ";")
	return inert[strings.ToLower(strings.TrimSpace(t))]
}

// contain keeps a stored file from acting as a page of the station. Its
// type is the uploader's word, and a file inside a recording has the name
// the zip gave it, so an HTML or SVG file would run script under the
// station's own origin. The sandbox directive takes that origin away,
// beside whatever policy the response already carries. A type a browser
// would render as a document goes out as a download.
func contain(w http.ResponseWriter, mediaType string) {
	h := w.Header()
	policy := "sandbox"
	if page := h.Get("Content-Security-Policy"); page != "" {
		policy = page + "; sandbox"
	}
	h.Set("Content-Security-Policy", policy)
	h.Set("X-Content-Type-Options", "nosniff")
	if !inertType(mediaType) && h.Get("Content-Disposition") == "" {
		h.Set("Content-Disposition", "attachment")
	}
}

// ServeBlob is GET, HEAD and DELETE /<sha256>[.ext] (BUD-01, BUD-02). p
// is the station's own idea of the caller, when it has one.
func (s *Store) ServeBlob(w http.ResponseWriter, r *http.Request, sha string, p *station.Principal) {
	ctx := r.Context()
	sha, _, _ = strings.Cut(sha, ".")
	if !blob.ValidSHA(sha) {
		writeError(w, refuse(http.StatusNotFound, "not a blob"))
		return
	}
	b, err := s.Index.Blob(ctx, sha)
	if errors.Is(err, station.ErrNotFound) {
		writeError(w, refuse(http.StatusNotFound, "no such blob"))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	auth, err := s.ReadAuth(r)
	if err != nil {
		writeError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !s.mayRead(ctx, b, p, auth) {
			if p == nil && auth == nil {
				writeError(w, refuse(http.StatusUnauthorized, "sign in, send a token, or send a t=get authorization"))
			} else {
				writeError(w, refuse(http.StatusForbidden, "not yours to read"))
			}
			return
		}
		f, err := s.Blobs.Open(sha)
		if errors.Is(err, blob.ErrNotFound) {
			writeError(w, refuse(http.StatusGone, "the file is gone; its event remains"))
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		defer f.Close()
		name := b.Name
		if name == "" {
			name = sha + blob.Extension(b.Type)
		}
		w.Header().Set("Content-Type", b.Type)
		w.Header().Set("ETag", `"`+sha+`"`)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if r.URL.Query().Has("download") || !inertType(b.Type) {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(name, `"`, "")+"\"")
		}
		contain(w, b.Type)
		http.ServeContent(w, r, name, b.CreatedAt, f)
	case http.MethodDelete:
		if !s.mayDelete(b, p, auth) {
			writeError(w, refuse(http.StatusForbidden, "not yours to delete"))
			return
		}
		who := ""
		if p != nil {
			who = p.Subject
		} else if auth != nil {
			who = auth.PubKey.Hex()
		}
		if err := s.remove(ctx, b, who, "delete"); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		writeError(w, refuse(http.StatusMethodNotAllowed, "GET, HEAD or DELETE"))
	}
}

// remove deletes the bytes and the index row; the file event stays.
func (s *Store) remove(ctx context.Context, b station.Blob, who, action string) error {
	if err := s.Blobs.Delete(b.SHA256); err != nil {
		return err
	}
	if err := s.Index.DeleteBlob(ctx, b.SHA256); err != nil {
		return err
	}
	s.removeUnpacked(b.SHA256)
	s.audit(ctx, station.AuditEntry{Subject: who, Action: action, DeviceID: b.DeviceID, HoldID: b.HoldID,
		Detail: fmt.Sprintf("%s %s (%d bytes)", b.Name, b.SHA256[:12], b.Size)})
	return nil
}

// Delete removes a blob on a principal's behalf, with the same rules as
// DELETE.
func (s *Store) Delete(ctx context.Context, p station.Principal, sha string) error {
	b, err := s.Index.Blob(ctx, sha)
	if errors.Is(err, station.ErrNotFound) {
		return refuse(http.StatusNotFound, "no such blob")
	}
	if err != nil {
		return err
	}
	if !s.mayDelete(b, &p, nil) {
		return refuse(http.StatusForbidden, "not yours to delete")
	}
	return s.remove(ctx, b, p.Subject, "delete")
}

// SetPublic marks a blob readable by anyone, or private again; admins
// and the uploader may.
func (s *Store) SetPublic(ctx context.Context, p station.Principal, sha string, public bool) error {
	b, err := s.Index.Blob(ctx, sha)
	if errors.Is(err, station.ErrNotFound) {
		return refuse(http.StatusNotFound, "no such blob")
	}
	if err != nil {
		return err
	}
	if !mayChange(&p, b) {
		return refuse(http.StatusForbidden, "not yours to share")
	}
	if err := s.Index.SetBlobPublic(ctx, sha, public); err != nil {
		return err
	}
	action := "private"
	if public {
		action = "public"
	}
	s.audit(ctx, station.AuditEntry{Subject: p.Subject, Action: action, DeviceID: b.DeviceID, HoldID: b.HoldID, Detail: b.Name + " " + sha[:12]})
	return nil
}

// own returns the blob when p may change how it is filed: an admin or
// the uploader.
func (s *Store) own(ctx context.Context, p station.Principal, sha string) (station.Blob, error) {
	b, err := s.Index.Blob(ctx, sha)
	if errors.Is(err, station.ErrNotFound) {
		return b, refuse(http.StatusNotFound, "no such blob")
	}
	if err != nil {
		return b, err
	}
	if !mayChange(&p, b) {
		return b, refuse(http.StatusForbidden, "not yours to file")
	}
	return b, nil
}

// SetGolden marks a blob golden, the station's word for pinned or
// bookmarked, or plain again; admins and the uploader may.
func (s *Store) SetGolden(ctx context.Context, p station.Principal, sha string, golden bool) error {
	b, err := s.own(ctx, p, sha)
	if err != nil {
		return err
	}
	if err := s.Index.SetBlobGolden(ctx, sha, golden); err != nil {
		return err
	}
	action := "plain"
	if golden {
		action = "golden"
	}
	s.audit(ctx, station.AuditEntry{Subject: p.Subject, Action: action, DeviceID: b.DeviceID, HoldID: b.HoldID, Detail: b.Name + " " + sha[:12]})
	return nil
}

// SetTags files a blob under the tags typed, replacing those it had;
// admins and the uploader may. It returns the tags as kept.
func (s *Store) SetTags(ctx context.Context, p station.Principal, sha, typed string) ([]string, error) {
	tags, err := ParseTags(typed)
	if err != nil {
		return nil, err
	}
	b, err := s.own(ctx, p, sha)
	if err != nil {
		return nil, err
	}
	if err := s.Index.SetBlobTags(ctx, sha, tags); err != nil {
		return nil, err
	}
	s.audit(ctx, station.AuditEntry{Subject: p.Subject, Action: "tags", DeviceID: b.DeviceID, HoldID: b.HoldID, Detail: b.Name + " " + sha[:12] + ": " + strings.Join(tags, " ")})
	return tags, nil
}

// Tag limits: a tag is a word of letters, digits and . _ -, lowercased,
// with or without a leading #.
const (
	MaxTags   = 20
	MaxTagLen = 40
)

// ParseTags reads the tags a person typed, separated by spaces or
// commas, with or without a leading #, and returns them lowercased,
// without repeats, in the order typed.
func ParseTags(typed string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, word := range strings.FieldsFunc(typed, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\t' }) {
		tag := strings.ToLower(strings.TrimPrefix(word, "#"))
		if tag == "" {
			continue
		}
		if len(tag) > MaxTagLen || !tagOK(tag) {
			return nil, refuse(http.StatusBadRequest, "a tag is letters, digits, . _ or -, up to %d characters: %q is not", MaxTagLen, word)
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	if len(out) > MaxTags {
		return nil, refuse(http.StatusBadRequest, "at most %d tags", MaxTags)
	}
	return out, nil
}

func tagOK(tag string) bool {
	for i, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// ServeList is GET /list/<pubkey> (BUD-02): the blobs a key uploaded,
// for an admin or for a t=list authorization signed by that key.
func (s *Store) ServeList(w http.ResponseWriter, r *http.Request, pubkey string, p *station.Principal) {
	auth, err := s.ReadAuth(r)
	if err != nil {
		writeError(w, err)
		return
	}
	allowed := p != nil && p.Admin
	if auth != nil && verb(auth) == "list" && auth.PubKey.Hex() == pubkey {
		allowed = true
	}
	if !allowed {
		writeError(w, refuse(http.StatusUnauthorized, "sign in as the owner, or send a t=list authorization from that key"))
		return
	}
	bs, err := s.Index.Blobs(r.Context(), station.BlobQuery{PubKey: pubkey})
	if err != nil {
		writeError(w, err)
		return
	}
	out := []Descriptor{}
	for _, b := range bs {
		out = append(out, s.Describe(b))
	}
	writeJSON(w, out)
}

// Sweep applies retention: blobs older than MaxAge go, then the oldest
// until the total fits MaxTotal. The file events stay.
func (s *Store) Sweep(ctx context.Context) ([]station.Blob, error) {
	var gone []station.Blob
	if s.MaxAge > 0 {
		old, err := s.Index.Blobs(ctx, station.BlobQuery{Before: s.now().Add(-s.MaxAge), OldestFirst: true})
		if err != nil {
			return gone, err
		}
		for _, b := range old {
			if err := s.remove(ctx, b, "station", "retention"); err != nil {
				return gone, err
			}
			gone = append(gone, b)
		}
	}
	if s.MaxTotal > 0 {
		_, total, err := s.Index.BlobTotal(ctx)
		if err != nil {
			return gone, err
		}
		for total > s.MaxTotal {
			oldest, err := s.Index.Blobs(ctx, station.BlobQuery{OldestFirst: true, Limit: 1})
			if err != nil || len(oldest) == 0 {
				return gone, err
			}
			if err := s.remove(ctx, oldest[0], "station", "retention"); err != nil {
				return gone, err
			}
			gone = append(gone, oldest[0])
			total -= oldest[0].Size
		}
	}
	return gone, nil
}
