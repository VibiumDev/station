# Vibium Station

Vibium Station brokers access to remote devices. Each machine with a device
on it, a [Roadie](https://github.com/VibiumDev/roadie) Pi with a phone for
now, enrolls as a rig and keeps one outbound tunnel to the station; people
and agents reach the devices through the station's pages and API. The
station signs every session as a hold grant that the rig checks itself, so
the rig stays the final gate.

`station` is the self-hosted binary, which serves one operator and their
own rigs, lets people sign in with a login, a password and a code from
an authenticator app, or by a tailnet identity, and keeps admins apart
from regular users, who see only the devices their teams were given
([docs/specs/teams.md](docs/specs/teams.md),
[docs/specs/second-factor.md](docs/specs/second-factor.md)),
and keeps its store in one SQLite file. The packages it is built from
take an authenticator, an authorizer and stores as interfaces, so a
hosted service can embed them instead of forking.

Status: R5 of the [remote spec](docs/remote-spec.md), under review.
These parts work:

- LAN, tailnet and public mode
- enrollment
- the tunnel
- holds and grants
- the viewer over WebRTC with the embedded TURN server behind it
- the MJPEG fallback through the station
- the REST and BiDi relay for agents, with `/session` as the entry point
- `station-probe`, which checks a station from outside
- the Record Store (R7, under way): recordings and screenshots as
  Blossom blobs, uploaded with authorizations the station signs for
  agents or a rig signs itself, listed per device and hold, and
  played in place by the embedded Record Player
- Jams (R8, under way): a prompt on the station's front page that a
  model the owner set up on Settings carries out on a device through
  Vibium's tools, with each step shown as it happens, and the list of
  jams at `/jams`; [docs/specs/jams.md](docs/specs/jams.md)

Packaging and hardening (R6) are under review.

## Run it

Go 1.27.4 or newer.

```bash
go build ./cmd/station
./station passwd                      # the owner's password (login: owner); an authenticator app is set up at the first sign-in
./station                             # LAN mode: https://<your ip>:8443, a self-signed certificate
./station --tailnet --allow you@example.com   # tailnet mode: https://station.<tailnet>.ts.net, sign in by identity
./station --public station.example.com  # public mode: its own Let's Encrypt certificate on :443, TURN on :3478
./station --http :8080 --public-url https://station.example.com --turn-public-ip 203.0.113.9   # behind your own TLS proxy
```

No release is published yet, so the binary comes from a build like the
one above. A release will carry static binaries for linux/amd64 and
linux/arm64 as tarballs, with the systemd unit inside, and a Docker
image at `ghcr.io/vibiumdev/station`.
[docs/how-to-guides/self-host.md](docs/how-to-guides/self-host.md)
installs a station as a sandboxed systemd service on a Linux host of your
own, and
[docs/how-to-guides/deploy-to-fly.md](docs/how-to-guides/deploy-to-fly.md)
puts one on Fly.io.
[docs/how-to-guides/set-up-jams.md](docs/how-to-guides/set-up-jams.md)
sets up the models a jam uses, including speech through a local Piper
server.

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
[`deploy/fly/fly.toml`](deploy/fly/fly.toml) is a working Fly.io setup,
in which TLS ends at Fly's edge, TURN binds to `fly-global-services` on
the app's dedicated IPv4 and the data directory lives on a volume.

The viewer is Roadie's own, served from the `viewer` package by both: a
rig that announces an `extras` script (Roadie does) gets its own buttons
and panels added to the page through the relay, and the shell itself
never names a phone. When WebRTC cannot connect, the viewer falls back
to the MJPEG stream relayed through the station, which the rig caps at a
few frames a second, while input goes over a relayed WebSocket.
`?transport=mjpeg` on the viewer's URL picks that from the start and
`?ice=relay` forces the media through TURN, to check the relay path from
a browser.

Then enroll a rig:

1. On the station's Settings page (or `station enroll`), make an
   enrollment code. It is single use, lasts 15 minutes, and ends with the
   station key's fingerprint.
2. On the rig: `roadie enroll https://<station> --code ABCD-1234-7F3A`. On
   the same LAN the URL can be left out; the station advertises itself over
   mDNS. The rig checks the station's key against the code, sends its key
   and devices, and writes its policy file. On a Pi where Roadie runs as
   a service the command is
   `sudo -u roadie roadie enroll https://<station> --code ABCD-1234-7F3A`,
   followed by `sudo systemctl restart roadie`.
3. Restart `roadie`, with `--label "Dev iPhone 15"` to name the device on
   the station's pages and `--platform ios` so they say what it runs. The
   device appears on the station's Devices page; Drive takes a hold and
   shows it over WebRTC. The same link opened while an
   agent or someone else holds the device watches it instead, with a Hold
   button that takes it; `?watch=1` only ever watches.

Agents use API tokens from the Account page, the Settings page or
`station token create NAME`, sent as `Authorization: Bearer`. A token
acts as its user at a level of its own, never above the user's: watch
only looks, drive holds and drives devices and runs jams and agents,
and admin manages the station too. See [API.md](API.md), and
[docs/tutorials/agent-through-the-station.md](docs/tutorials/agent-through-the-station.md)
for pointing Claude Code, Codex, the Vibium CLI or a script at a device
through the station, which comes down to the address, the token and the
hold. Driving the device itself is the rig's tutorial, Roadie's for a
phone.
[docs/explanation/nostr-keys.md](docs/explanation/nostr-keys.md) explains
why every station, rig and device key is a Nostr key, what the signed
events do today, and what they leave open.

## Commands and flags

| Command | What it does |
|---|---|
| `station` | Run the station |
| `station passwd [--reset] [login]` | Set a user's password, the owner's by default (argon2id, in the database); `--reset` also forgets their authenticator and backup codes, so the next sign-in sets up a new one |
| `station enroll` | Print a one-time enrollment code |
| `station token create NAME [--level watch\|drive\|admin]`, `token list`, `token revoke NAME` | API tokens; drive is the default, `--read-only` means watch |
| `station key` | The station's key as npub, hex and fingerprint |
| `station version` | The build's version |

| Flag | Default | Meaning |
|---|---|---|
| `--data-dir` | `$STATION_DIR`, else the user config dir + `/station` | Where `config.json`, `station.key`, `station.db`, the certificate, tsnet state, the Record Store and the AI models live |
| `--listen` | `:8443`, with `--public` `:443` | HTTPS address in LAN mode and with `--public` |
| `--tailnet` | off | Join the tailnet with tsnet and serve there only |
| `--hostname` | `station` | The node's name on the tailnet |
| `--control-url` | Tailscale's | A Headscale or other control server |
| `--public HOST` | | Public mode: HTTPS on `:443` with a Let's Encrypt certificate for HOST, fetched and renewed by the station itself (its challenge is answered on `:80`) and cached in the data directory |
| `--http ADDR` | | Public mode behind your own TLS proxy (or Fly.io): plain HTTP on ADDR; needs `--public-url`. Login lockouts then count by `Fly-Client-IP` or the first `X-Forwarded-For` address |
| `--allow` | none | Tailnet logins and tags that may sign in by identity (saved) |
| `--public-url` | derived | How rigs and browsers reach the station; the tunnel URL follows |
| `--name` | `station` | The station's name, shown on its pages and told to rigs when they enroll (saved; the Settings page changes it too) |
| `--no-mdns` | off | Do not advertise on the LAN |
| `--records-dir` | `blobs/` in the data directory | Where the Record Store keeps its files |
| `--records-max-file` | `256M` | The largest file the Record Store takes |
| `--records-max-total` | `2G` | How much it keeps before the oldest files go; `0` for no limit |
| `--records-max-age` | `720h` | How long it keeps a file; `0` for ever |
| `--vibium` | `$STATION_VIBIUM`, else `vibium` on `PATH` | The Vibium binary a jam drives devices with, through `vibium mcp` |
| `--piper` | `$STATION_PIPER`, else `piper/venv/bin/python` in the data directory, else `piper` on `PATH` | The Python that has `piper-tts[http]`: the station runs Piper's server with it, so jams speak with a voice from `piper/` in the data directory |
| `--turn` | `auto` | Run the TURN server: `on`, `off`, or `auto` (on in public mode) |
| `--turn-listen` | `:3478` | TURN's UDP address; `fly-global-services:3478` on Fly.io |
| `--turn-public-ip` | the `--public` host's address | The public address TURN and its relays are reached at |
| `--turn-relay-addr` | the listen address's host | The address relay sockets bind; `fly-global-services` on Fly.io |
| `--turn-ports` | `49152-65535` | The relay port range, one port per allocation, reachable from outside |
| `--turn-deny` | none | More destinations TURN refuses, comma-separated CIDRs |
| `--turn-mtu` | 1600 | TURN's inbound MTU |

`STATION_PASSWORD` in the environment sets the owner's password when the
station starts, for hosts whose secrets are environment variables; the
owner sets up an authenticator at their first sign-in, like everyone.
Other users never get a password from anyone: an admin adds them on
Settings and passes on the setup link, where they choose their own and
add an authenticator.
`STATION_VIBIUM` names the Vibium binary the same way `--vibium` does,
and `VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL` and the provider's key
variable in the environment are one more agent model on Settings, named
`host`, for a deployment that keeps the key as a platform secret.

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
| `sdnotify` | Tells systemd the service is ready, alive (the watchdog) or stopping; a no-op outside systemd |
| `blob` | Files by their SHA-256 on disk, hashed as they are written |
| `records` | The Record Store: Blossom's upload, fetch, list and delete, the authorizations the station signs on an agent's behalf, the file event per blob, and retention |
| `viewer` | The shell: the one viewer page a station and a Roadie both serve, plus the embedded Record Player under `player/` (built from the [record-player](https://github.com/VibiumDev/record-player) repository by `scripts/update-player.sh`) (Roadie's toolbar, settings panel and on-screen keyboard, the MJPEG reader, sound and microphone, input in picture space, WebRTC), told its endpoints by a config and extended by a rig's own extras module |
| `cmd/station` | The station binary |
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
