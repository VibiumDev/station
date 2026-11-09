# Vibium Station: standing rules for coding agents

Vibium Station brokers access to remote devices. It is a set of Go packages
plus `cmd/station`, a binary anyone can self-host: one operator, their
own rigs, ideally a tailnet, with local users in teams, admins and
regular users (`docs/specs/teams.md`). Roadie (`VibiumDev/roadie`,
at `../roadie`) is the first device provider: each Pi with a phone enrolls
as a rig. Headless and desktop browsers come later, so the model is
device-neutral from the start.

## Where things are

- `docs/remote-spec.md` is the source of truth for the remote work (Roadie
  Remote and Station). Read it in full before writing code.
- `docs/progress.md` holds done work, decisions, measured numbers and known
  issues. Update it at every checkpoint so a fresh session can continue.
  It is a local file, ignored by git and never pushed.
- Station's event kinds are listed in one file, `docs/kinds.md`.
- The two repos move in lock step. A `go.work` in the parent directory
  (`~/Projects`) ties them together for development and is never committed.
  Commit to Station first, tag releases, then bump Roadie's dependency.
- Never `git push` without asking first, even when told to "commit and
  push". Commits stay per repo. Commit messages are short and to the point,
  no essays. No Co-Authored-By or other attribution lines, in any repo.

## Open source boundaries

- Everything here must make sense for one person running their own Pis. No
  vendor accounts, email services, billing or provider-only code. Local
  users and teams are in (`docs/specs/teams.md`); multi-customer concerns
  belong to services that embed these packages.
- Embeddable: hosted services embed the packages through five small, stable
  interfaces: `Authenticator`, `Authorizer`, `DeviceStore`, `HoldStore`,
  `AuditSink`. Handlers must mount under any path prefix.
- Device-neutral: rigs (enrolled machines) offer devices with a `kind`
  (`phone` now, `browser` later) and capabilities (`video`, `audio`, `hid`,
  `bidi`, `mjpeg`). Station's code never says "phone"; phone-specific code
  lives in Roadie. Holds, tokens and API paths name the device; the tunnel
  belongs to the rig.
- Station owns the rig library: tunnel client, enrollment, session-token
  checks and the endpoint allowlist helper. Roadie imports it.
- Nostr-ready: every key is secp256k1 with BIP-340 Schnorr, shown as
  npub/nsec (NIP-19); signed events use the NIP-01 format with Station's own
  documented kinds; NIP-44 for private content that crosses a third party.
  Use fiatjaf.com/nostr or btcec, pure Go only. The transport stays the
  tunnel; never publish private events; do not use NIP-90.
- Relay-ready, without a relay: no Blossom file store and no embedded Nostr
  relay yet. Store every signed or accepted event verbatim in an append-only
  `events` table behind an `EventStore` interface (save, query by NIP-01
  filter, delete) modelled on khatru's event store, with a SQLite
  implementation. Refused or invalid events are not stored. Live hold state
  stays in ordinary tables. Use NIP-01 kind ranges: regular for hold grants,
  revocations and enrollment requests; replaceable for rig announcements;
  addressable with a `d` tag per device; kind 0 for the station profile.
  Name every event's parties in `p` tags. Events are never edited: a
  correction is a new event.
- Rig policy is the operator's consent: a file on the rig lists accepted
  stations by npub and, per station, allowed scopes, whether admin grants
  are accepted, and maximum session length. One active hold across all
  stations; the rig arbitrates. No station can exceed what the operator
  allowed.

## Security rules

- Users with passwords: argon2id hashes in the database (`station passwd
  [login]`; the owner is the first admin), a second factor on every
  password sign-in (TOTP from an authenticator app, backup codes kept
  hashed; `docs/specs/second-factor.md`), new users through single-use
  setup links rather than passwords anyone else chose, login throttled
  by address with lockout and by login with a growing wait that a
  browser's device cookie from an earlier sign-in bypasses, cookies `HttpOnly` `Secure`
  `SameSite=Lax`, CSRF tokens on state-changing requests, a strict
  Content Security Policy.
- Named API tokens stored hashed, optionally read-only. Tokens travel only
  in headers or the first WebSocket message, never in URLs. Browser
  WebSockets check `Origin`. Logs never contain passwords, tokens or TURN
  credentials.
- Enrollment codes are single-use, expire in 15 minutes, and carry the
  station key's fingerprint. The station advertises `_vibium-station._tcp`
  over mDNS on the LAN. Removing a rig refuses its key from then on.
- TURN is locked down: per-hold short-lived credentials only; refuse
  relaying to private, loopback and link-local addresses plus configured
  extra ranges (for example Fly.io's `fdaa::/16`); listen address,
  advertised IP and MTU configurable. Off by default in tailnet mode.
- The relay is dumb: forward REST and BiDi bytes for an authorized hold and
  never interpret HID commands. The Pi validates everything again.
- Secrets come from env vars or files outside the repo. Never commit keys.

## Engineering rules

- Prefer the standard library and small dependencies; no cgo (pure-Go
  SQLite); never GStreamer. `govulncheck` runs in CI. CI builds linux/arm64,
  linux/amd64 and darwin/arm64.
- Packages as the spec names them: `tunnel`, `signal`, `relay`, `hold`,
  `token`, `turn`, `viewer`, plus `rig`, `cmd/station` and
  `cmd/station-probe`.
- Holds: per-device seq, takeover by input scope, one view-only observer,
  release after 5 idle minutes or 8 hours. Hold grants live 10 minutes and
  are refreshed over the data channel or tunnel.
- Pages are extremely minimal, like exe.dev and Roadie's pages:
  server-rendered HTML, system fonts, monochrome plus one accent colour, no
  frameworks, no build step. The viewer is the device's screen plus a thin
  toolbar, and the same viewer module serves the station and Roadie's
  `/view`.
- Tests: token checks (valid, expired, wrong device, wrong signer, older
  seq, missing scope, endpoint allowlist, replayed event id, grant outside
  the rig policy, station not in the policy), enrollment (wrong fingerprint,
  reused or expired code), the tunnel protocol, TURN lockdown, and
  EventStore filter queries.
- Release artifacts by R6: static binaries for linux/amd64 and arm64, a
  Docker image, a systemd unit, a sample `fly.toml`, and
  `docs/self-hosting.md`.
- Update `README.md`, `API.md` and `docs/` for every user-visible change.

## Environments

- Develop against a local `station` on the dev Pi, then tailnet mode
  (`--tailnet`, tsnet), then the public test station. Deploy only to the
  public test station you are given a scoped token for, and to nothing else.
- From R4 on, run `cmd/station-probe` against the public test station and
  include its output in the milestone report.

## How to work

- Milestones R0 to R6 in order, each with a gate (see the spec). Plan the
  steps inside a milestone yourself. At the end of each milestone, stop,
  report what changed, how it was tested, measured numbers and any deviation
  from the spec, and wait for approval.
- If something in the spec is wrong or impossible, stop, explain, and
  propose the smallest alternative.
