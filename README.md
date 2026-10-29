# Vibium Station

Vibium Station brokers access to remote devices. Each machine with a device
on it, a [Roadie](https://github.com/VibiumDev/roadie) Pi with a phone for
now, enrolls as a rig and keeps one outbound tunnel to the station; people
and agents reach the devices through the station's pages and API. The
station signs every session as a hold grant that the rig checks itself, so
the rig stays the final gate.

`station` is the single-player binary: one owner, their own rigs, a
password or a tailnet identity, one SQLite file. The packages it is built
from take an authenticator, an authorizer and stores as interfaces, so a
hosted service can embed them instead of forking.

Status: R4 of the [remote spec](docs/remote-spec.md). LAN, tailnet and
public mode, enrollment, the tunnel, holds and grants, the viewer over
WebRTC with the embedded TURN server behind it and the MJPEG fallback
through the station work, and `station-probe` checks a station from
outside. The REST and BiDi relay for agents (R5) and packaging (R6) are
next. Progress and numbers are in [docs/progress.md](docs/progress.md).

## Run it

Go 1.27 or newer.

```bash
go build ./cmd/station
./station passwd                      # the owner's password
./station                             # LAN mode: https://<your ip>:8443, a self-signed certificate
./station --tailnet --allow you@example.com   # tailnet mode: https://station.<tailnet>.ts.net, sign in by identity
./station --acme station.example.com  # public mode: Let's Encrypt on :443 (HTTP-01 on :80), TURN on :3478
./station --http :8080 --public-url https://station.example.com --turn-public-ip 203.0.113.9   # behind your own TLS proxy
```

The first run makes the station's key and prints its `npub`. Tailnet mode
joins your tailnet as a node named `station` through `tsnet` and prints a
login link once; set `TS_AUTHKEY` to skip that. `--control-url` points it
at a Headscale server instead.

Public mode turns the TURN server on, so browsers and rigs behind NATs
still get a WebRTC path: UDP 3478 and the relay ports (`--turn-ports`,
the ephemeral range by default) must be reachable on `--turn-public-ip`.
TURN takes only the per-hold credentials the station hands out with each
grant and never relays to private, loopback, link-local, shared or
unique-local addresses, nor to `--turn-deny` ranges. On a host where
secrets are environment variables, `STATION_PASSWORD` sets the owner's
password at start. [`Dockerfile`](Dockerfile) builds the image and
[`deploy/fly/fly.toml`](deploy/fly/fly.toml) is a working Fly.io setup:
TLS at Fly's edge, TURN bound to `fly-global-services` on the app's
dedicated IPv4, the data directory on a volume.

The viewer is Roadie's own, served from the `viewer` package by both: a
rig that announces an `extras` script (Roadie does) gets its own buttons
and panels added to the page through the relay, and the shell itself
never names a phone. When WebRTC cannot connect, the viewer falls back to the MJPEG stream
relayed through the station, capped at a few frames a second on the rig,
with input over a relayed WebSocket. `?transport=mjpeg` on the viewer's
URL picks that from the start and `?ice=relay` forces the media through
TURN, to check the relay path from a browser.

Then enroll a rig:

1. On the station's Settings page (or `station enroll`), make an
   enrollment code. It is single use, lasts 15 minutes, and ends with the
   station key's fingerprint.
2. On the rig: `roadie enroll https://<station> --code ABCD-1234-7F3A`. On
   the same LAN the URL can be left out; the station advertises itself over
   mDNS. The rig checks the station's key against the code, sends its key
   and devices, and writes its policy file.
3. Restart `roadie`. The device appears on the station's front page; Open
   takes a hold and shows it over WebRTC. The same link opened while an
   agent or someone else holds the device watches it instead, with a Hold
   button that takes it; `?watch=1` only ever watches.

Agents use API tokens from the Settings page or `station token create
NAME`, sent as `Authorization: Bearer`. A read-only token may watch but
never take control. See [API.md](API.md), and
[docs/tutorials/agent-remote-phone.md](docs/tutorials/agent-remote-phone.md)
for driving a phone from a laptop with Claude Code, Codex, the Vibium
CLI or an SDK script, with a recording at the end.

## Commands and flags

| Command | What it does |
|---|---|
| `station` | Run the station |
| `station passwd` | Set the owner's password (argon2id, in `config.json`) |
| `station enroll` | Print a one-time enrollment code |
| `station token create NAME [--read-only]`, `token list`, `token revoke NAME` | API tokens |
| `station key` | The station's key as npub, hex and fingerprint |

| Flag | Default | Meaning |
|---|---|---|
| `--data-dir` | `$STATION_DIR`, else the user config dir + `/station` | Where `config.json`, `station.key`, `station.db`, the certificate and tsnet state live |
| `--listen` | `:8443`, with `--acme` `:443` | HTTPS address in LAN mode and with `--acme` |
| `--tailnet` | off | Join the tailnet with tsnet and serve there only |
| `--hostname` | `station` | The node's name on the tailnet |
| `--control-url` | Tailscale's | A Headscale or other control server |
| `--acme HOST` | | Public mode: HTTPS with a Let's Encrypt certificate for HOST, the HTTP-01 challenge on `:80`, the certificate cached in the data directory |
| `--http ADDR` | | Public mode behind your own TLS proxy (or Fly.io): plain HTTP on ADDR; needs `--public-url`. Login lockouts then count by `Fly-Client-IP` or the first `X-Forwarded-For` address |
| `--allow` | none | Tailnet logins and tags that may sign in by identity (saved) |
| `--public-url` | derived | How rigs and browsers reach the station; the tunnel URL follows |
| `--name` | `station` | The station's name (saved) |
| `--no-mdns` | off | Do not advertise on the LAN |
| `--turn` | `auto` | Run the TURN server: `on`, `off`, or `auto` (on in public mode) |
| `--turn-listen` | `:3478` | TURN's UDP address; `fly-global-services:3478` on Fly.io |
| `--turn-public-ip` | the `--acme` host's address | The public address TURN and its relays are reached at |
| `--turn-relay-addr` | the listen address's host | The address relay sockets bind; `fly-global-services` on Fly.io |
| `--turn-ports` | `49152-65535` | The relay port range, one port per allocation, reachable from outside |
| `--turn-deny` | none | More destinations TURN refuses, comma-separated CIDRs |
| `--turn-mtu` | 1600 | TURN's inbound MTU |

`STATION_PASSWORD` in the environment sets the owner's password when the
station starts, for hosts whose secrets are environment variables.

## Packages

| Package | Holds |
|---|---|
| root `station` | The model (rigs, devices, holds, principals, scopes) and the five interfaces an embedding application implements: `Authenticator`, `Authorizer`, `DeviceStore`, `HoldStore`, `AuditSink`; `EventStore` is khatru's |
| `token` | Hold grants and revocations as signed Nostr events; see [docs/kinds.md](docs/kinds.md) |
| `hold` | One hold per device: sequence numbers, takeover by input scope, observers, idle and maximum time |
| `tunnel` | The rig's outbound WebSocket, a signed challenge, then HTTP over yamux streams |
| `signal`, `relay` | The viewer's signaling WebSocket and the reverse proxy to a rig |
| `enroll` | Codes, the profile and enrollment endpoints, mDNS |
| `rig` | The rig library: key, policy file, enrollment, the tunnel client, the gate and allowlist |
| `turn` | The embedded TURN server (pion/turn): per-hold credentials, no relaying to private, loopback, link-local, shared or configured ranges |
| `sqlite` | The one-file store, pure Go |
| `viewer` | The shell: the one viewer page a station and a Roadie both serve (Roadie's toolbar, settings panel and on-screen keyboard, the MJPEG reader, sound and microphone, input in picture space, WebRTC), told its endpoints by a config and extended by a rig's own extras module |
| `cmd/station` | The single-player binary |
| `cmd/station-probe` | A headless check of any station: token, hold, WebRTC through TURN only, a tap and the screenshot it changes |

## Security in short

Every key is secp256k1 with BIP-340 Schnorr, shown as an npub. A hold
grant is a signed event with the rig, device, hold id, sequence number,
scopes and a ten-minute expiry; the rig refuses forged, expired,
wrong-device and older-sequence grants, replays, revoked holds and
anything its policy file does not allow. Tokens travel in headers or the
first WebSocket message, never in URLs. The station always serves TLS,
itself or through the proxy in front of it. A rig pins the station's
certificate at enrollment when no root signs it, and the station's key
always. TURN credentials are per hold, derived from the station's key
and good for the hold's maximum time; the relay refuses expired and
ended holds, caps allocations per hold, and never sends to a private,
loopback, link-local, shared-address or unique-local destination, so it
is not a proxy into anyone's network.
