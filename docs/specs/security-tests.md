# Security tests: the plan

Teams, the second factor and token levels gave the station many more
paths than it had as a single-player tool, and every one of them is a
place a mistake could let someone see or do what they should not. The
existing tests check each mechanism on its own: the store, the sign-in
steps, token checks on the rig, the record store's rules. What they do
not do is ask, for every route the station serves and every kind of
caller, whether the answer is the one the specs promise. This plan adds
that, layer by layer, and fixes what it finds. It is written to be
followed in order; each phase leaves the suite green.

## Phase 1: the authorization matrix

One test drives the real handler, the same `http.Handler` the binary
serves, with a complete station assembled in memory: database, hold
manager, tunnel and relay (with no rig attached), record store on a
temporary directory, jams switched off. It then walks a table of every
route against every kind of caller and asserts the status class.

The callers:

| Caller | How it is made |
|---|---|
| nobody | no cookie, no header |
| a session awaiting a code | password passed, code not yet |
| a session setting up | password passed, no authenticator yet |
| a locked user's session | signed in, then locked |
| a regular user with no team | signed in |
| watch, drive and jam users | signed in, one team each, one level on device A, nothing on device B |
| an admin | signed in |
| tokens | watch, drive and admin tokens of the drive user and of the admin; a jam's device-bound token |
| a tailnet identity | allowed (admin) and unknown |

The expected answers, in short: nobody and the sessions on their way in
get 401 from the API and a redirect from the pages, to the step they are
at; the locked session is nobody; `/settings/*`, `/v1/users`, `/v1/teams`
and `/v1/access` are 403 for every regular user and for every token but
an admin's admin token; device routes answer by the caller's level on
that device and 403 or 404 on device B; a watch token never gets a 2xx
from anything that changes state, pages included; a device-bound token
gets 403 off its device. CSRF: every POST page and every state-changing
API call from a session without the token is 403, and the same call
with it is not. A tailnet identity's change sent by another site's page
is 403, and the same change from the station's own page or from a client
that is no browser is not. Cookies: HttpOnly, SameSite=Lax, Secure under TLS. A
cross-origin WebSocket upgrade is 403.

The matrix is the backbone: a new route added without an entry fails
the test, so the table is kept complete by a check that every pattern
the mux knows appears in it.

## Phase 2: records, public and private

Real blobs in the temporary store, uploaded under the station's own
authorization as a rig would. Then, for each of the callers above:
`GET /{sha}`, `HEAD /{sha}`, `GET /records/{sha}/play`, `/thumb.jpg`,
`/files/…`, `POST /v1/records/{sha}/download-auth`, `GET /v1/records`
and `GET /list/{pubkey}`, against a private record on device A, one on
device B, one with no device, and a public one. Then the changes:
`DELETE /{sha}`, `POST /records/delete`, `/records/public`,
`/records/golden`, `/records/tags`, `POST /v1/records/{sha}/golden` and
`PUT /v1/records/{sha}/tags`, which only the uploader or an admin may
make, never a watch token, never an admin's drive token on another's
record. Public records are readable by nobody-in-particular by link and
appear in nobody's list but their uploader's and admins'. A record made
private again is gone from the link.

## Phase 3: jams and holds

A jam is visible to whoever may watch its device, its starter and
admins, and no one else; its follow-ups, Keep and Stop belong to its
starter, admins, and people at jam level on its device, and to nobody
at watch or drive. Starting one needs jam level on the device and a
drive token at least. Holds: a watcher's hold is view-only; a drive
user takes a free device, takes it from a watcher, and takes it from
another regular user, but not from an admin; an admin takes it from
anyone; release is one's own; a watch token cannot take control and a
device-bound token cannot touch device B. The relayed endpoints refuse
a scope the hold lacks and a caller below the level the scope needs.
These run against the hold manager and the handlers with the relay
answering 502 for want of a rig, which is enough to see the
authorization decision.

## Phase 4: sign-in and sessions through the handler

The flows already have unit tests; these walk them through the real
routes and headers: cookie flags on every Set-Cookie; the session id
changes at the code step and at setup; sign-out ends the session and
the next request is nobody; a locked user's open session dies at its
next request; a session awaiting a code cannot reach the API or any
page but its own; a setup link for a locked or removed user is spent;
`next` after sign-in refuses `//`, `/\`, and absolute addresses; the
device cookie is rejected when forged, when for another user and when
signed with another key; the backoff and the address lock through the
login form; the password rules at every entry (setup link, Account,
`station passwd`); `GET /v1/users` and `/account` never show a hash, a
secret or a backup code.

## Phase 5: enrollment and the rig's side of trust

The `enroll` package has no tests of its own. Add: a code is single-use
and expires in 15 minutes; a code with the wrong station fingerprint is
refused; a removed rig's key is refused from then on, on enrollment and
on the tunnel; a rig's announcement cannot claim another rig's devices.
The grant checks in `token` and `rig` stay as they are and gain a case
for a grant whose device the rig does not have.

## Phase 6: Roadie

Roadie's own auth has tests for its modes, its login cookie and
`safeNext`. Add: in token mode every state-changing endpoint (HID,
mic, settings, rotate, resets) is 401 without the bearer and 200 with
it; the view-only page never carries a control the server would act
on; through the rig library's gate, a grant with the view scope alone
is refused at every input and admin endpoint on the allowlist and
admitted at every view one, as a table over `rigAllowlist`; a grant for
a device the Roadie does not have is refused; a replayed grant id is
refused.

## Phase 7: parsers and fuzzing

Short fuzz targets, run in the ordinary test run for a few seconds and
kept as seeds: `ParseTags`, `ParseLevel`, `ParseTokenLevel`,
`ParseScopes`, `safeNext`, `normalizeCode`, `totpCheck` input,
`checkPasswordRules`, the Blossom `ReadAuth` header, and the setup
link's token. None may panic, and the ones that gate access must refuse
what they do not understand.

## Phase 8: keeping it that way

CI runs the station's tests under `-race`, and the matrix test's
completeness check means a new route cannot land without saying who
may call it. The plan's "found" list
below records every hole the suite turned up and the commit that closed
it, so the history is readable later.

## Where the tests live

| Phase | Tests |
|---|---|
| 1 | `cmd/station/e2e_test.go`: `TestAuthorizationMatrix`, `TestMatrixIsComplete`, `TestCSRFOnEveryChange`, `TestIdentityChangesComeFromThisStation`, `TestCookieFlagsAndOrigin` |
| 2 | `cmd/station/records_e2e_test.go`, with `records/records_test.go` for the store's own rules |
| 3 | `cmd/station/holds_e2e_test.go`, with `hold/hold_test.go` for the manager |
| 4 | `cmd/station/signin_test.go`, `auth_test.go`, `tokens_test.go`, `TestNoSecretsLeakAndLockBites` in `e2e_test.go` |
| 5 | `enroll/enroll_test.go`, `rig/rig_test.go`, `token/token_test.go` |
| 6 | Roadie's `auth_routes_test.go`, `allowlist_test.go`, `auth_test.go`, with `rig/rig_test.go` for the gate |
| 7 | `cmd/station/fuzz_test.go`, `records/fuzz_test.go` |
| 8 | `.github/workflows/ci.yml` runs the station's tests under `-race`; the matrix's completeness check |
| Review | `TestStoredFilesCannotActAsPages` in `cmd/station/records_e2e_test.go`, `TestServedFilesAreSandboxed` in `records/records_test.go`, `TestHiddenArgumentsNeverReachVibium` in `jam/loop_test.go`, `TestUnpackLimitHoldsForAnyDeclaredSize` and `TestThumbnailStaysInsideItsRecording` in `records/thumb_test.go` |

## Found and fixed

Phase 1, the matrix, on its first runs:

- A watch-level user, or anyone who could see a jam, could send it
  follow-ups, Keep it and Stop it, through the API and the page forms:
  the checks only asked who may see the jam. Now `jams.act` asks for
  its starter, an admin's power, or jam level on its device, and
  answers 403 ("watching a jam does not let you steer it").
- The jam forms, the record forms (public, delete, golden, tags) and
  the Account changes were pages any signed-in caller could post,
  watch tokens included, since pages were gated at view. The forms
  that change things are gated at control now, and Account changes
  take a person, never a token.
- An admin's watch or drive token could delete, publish and refile any
  record, because the record store asked only `Admin`. It asks
  `Principal.Manages()` now: an admin in person or an admin's admin
  token; other tokens change only what their own subject uploaded.
- The tags form hid a refusal in a redirect; it answers 403 or 404.
- The drive and view pages rendered any device for anyone signed in,
  device B included; they now 404 for a device the caller may not
  watch, and the drive page sends a watcher to the view page.
- Device routes were authorized against no device in the wrappers,
  leaving each handler to check, and several did not: a user with no
  access could list a device's recordings (empty), release or leave a
  reservation on it (no-ops that still answered 200), and learn its
  hold state from the relay's 409 before any check. The `page` and
  `api` wrappers now authorize every `{rig}/{name}` route against that
  device (404 on pages, 403 on the API) before the handler runs.
- Starting a jam checked for the Vibium binary and the named device's
  existence before asking whether the caller might, so a stranger
  learned both; the refusals are typed `forbidden` and come first, and
  the API answers 403.
- The generic REST relay took an input-scoped hold for any person who
  was not a read-only token, so a watch-level user got 403 where view
  would have done; it asks for control first and falls back to view.
- Leaving a reservation was a view action; it is a control action.
- An identity sign-in (no password) could start replacing an
  authenticator it does not have; both authenticator changes now ask
  for a password first.

Phases 2 to 5, on their first runs:

- A regular user at drive could recall an admin's hold, since every
  person recalled: the hold endpoint now asks `mayRecall`, which lets
  a person recall another regular user or an agent and an admin recall
  anyone, and leaves a regular user in line behind an admin's hold.
- A jam's device-bound token, acting as its admin starter, could read
  records of other devices and device-less ones; a bound token now
  reads its own device's records alone.
- `safeNext` let `/\evil.example` through, which a browser reads as
  `//evil.example`; it refuses backslashes, line breaks and NULs now.
- Nothing else: the record store's rules, the lists, the enrollment
  handler and the throttles answered as the specs say.
- Later, with the Vibium upload path in view: a record uploaded through
  a token was owned by the token's subject alone, so the person behind
  it could not delete or share it. Records now carry the uploading
  user's id and `owns` matches by it; a watch token still changes
  nothing (`mayChange`).

A review of the whole station by a second reader, after the phases:

- A stored file was served in place under the media type its uploader
  named, from the station's own address. A person at watch level could
  upload an HTML page and a script, and the browser of an admin who
  opened the link ran them as the admin. A file inside a recording did
  the same by its name, and an SVG screenshot by its thumbnail. Every
  stored file, thumbnail and unpacked file is sandboxed now. One a
  browser would render as a document is sent as a download.
- A jam's model was shown each tool with the parameters a jam needs,
  but an argument it sent beyond those went to Vibium all the same.
  Vibium saves a screenshot under any file name it is given, so a
  prompt, or words on the device's screen, could overwrite a file on
  the station's disk. Arguments outside the schema are dropped before
  the call now.
- A recording's thumbnail was read from the path its trace named for
  the last frame, joined to the unpacked copy unchecked. An uploader
  could name another person's picture in the store, or any picture on
  the host, and get it back as their own thumbnail. A name that led to
  a device file would have read without end. A frame is a plain file
  name inside the recording now, a regular file, and at most 8 MiB.
- A zip is unpacked up to twice the largest file the store takes. The
  count was signed, and a first entry that declared 2^63 bytes turned
  it negative, so the entries after it unpacked without limit. A
  kilobyte of upload could put any amount on the disk. The size is
  compared unsigned now, before it is counted.
