# Commands and Flags

Everything the `station` binary takes: its commands, its flags, the
environment variables it reads, and what each mode needs from the network.
To set a station up step by step, see
[Self-host a station](../how-to-guides/self-host.md) or
[Deploy to Fly.io](../how-to-guides/deploy-to-fly.md).

## Commands

| Command | What it does |
|---|---|
| `station` | Run the station |
| `station passwd [--reset] [login]` | Set a user's password, the owner's by default (argon2id, in the database); `--reset` also forgets their authenticator and backup codes, so the next sign-in sets up a new one |
| `station enroll` | Print a one-time enrollment code |
| `station token create NAME [--level watch\|drive\|admin]`, `token list`, `token revoke NAME` | API tokens; drive is the default, `--read-only` means watch |
| `station key` | The station's key as npub, hex and fingerprint |
| `station version` | The build's version |

## Flags

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
| `--name` | `station` | The station's name, shown on its pages and told to rigs when they enroll (saved; Settings changes it too) |
| `--no-mdns` | off | Do not advertise on the LAN |
| `--records-dir` | `blobs/` in the data directory | Where the Record Store keeps its files |
| `--records-max-file` | `256M` | The largest file the Record Store takes |
| `--records-max-total` | `2G` | How much it keeps before the oldest files go; `0` for no limit |
| `--records-max-age` | `720h` | How long it keeps a file; `0` for ever |
| `--vibium` | `$STATION_VIBIUM`, else `vibium` on `PATH` | The Vibium binary a jam session drives devices with, through `vibium mcp` |
| `--piper` | `$STATION_PIPER`, else `piper/venv/bin/python` in the data directory, else `piper` on `PATH` | The Python that has `piper-tts[http]`: the station runs Piper's server with it, so jam sessions speak with a voice from `piper/` in the data directory |
| `--turn` | `auto` | Run the TURN server: `on`, `off`, or `auto` (on in public mode) |
| `--turn-listen` | `:3478` | TURN's UDP address; `fly-global-services:3478` on Fly.io |
| `--turn-public-ip` | the `--public` host's address | The public address TURN and its relays are reached at |
| `--turn-relay-addr` | the listen address's host | The address relay sockets bind; `fly-global-services` on Fly.io |
| `--turn-ports` | `49152-65535` | The relay port range, one port per allocation, reachable from outside |
| `--turn-deny` | none | More destinations TURN refuses, comma-separated CIDRs |
| `--turn-mtu` | 1600 | TURN's inbound MTU |

## Environment variables

| Variable | What it does |
|---|---|
| `STATION_DIR` | The data directory, when `--data-dir` is not given |
| `STATION_PASSWORD` | Sets the owner's password when the station starts, for hosts whose secrets are environment variables. The owner still sets up an authenticator at their first sign-in. |
| `STATION_VIBIUM` | The Vibium binary, like `--vibium` |
| `STATION_PIPER` | Piper's Python, like `--piper` |
| `TS_AUTHKEY` | Lets tailnet mode join without printing a login link |
| `VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL` and the provider's key variable | One more agent model on Settings, named `host`, for a deployment that keeps the key as a platform secret |

Other users never get a password from anyone. An admin adds them on
Settings and passes on the setup link, where they choose their own and
add an authenticator. An account can also be given an expiry, for a trial or a
demo: at that time it stops working as if it were locked, and loses any
device it holds.

## Modes

**LAN.** The station serves HTTPS on `:8443` with a certificate it makes
itself, and advertises itself over mDNS so a rig on the same network can
enroll without its address.

**Tailnet.** The station joins your tailnet as a node named `station`
through `tsnet` and prints a login link once; `TS_AUTHKEY` skips that.
`--control-url` points it at a Headscale server instead. Identities on
`--allow` sign in with no password.

**Public.** The station gets its own Let's Encrypt certificate and turns
the TURN server on, so browsers and rigs behind NATs still get a WebRTC
path. UDP 3478 and the relay ports (`--turn-ports`) must be reachable on
`--turn-public-ip`. TURN takes only the per-hold credentials the station
hands out with each grant, and never relays to private, loopback,
link-local, shared or unique-local addresses, nor to `--turn-deny` ranges.

**Behind a proxy.** `--http` serves plain HTTP for a TLS proxy in front.
[`Dockerfile`](../../Dockerfile) builds the image, and
[`deploy/fly/fly.toml`](../../deploy/fly/fly.toml) is a working Fly.io
setup, in which TLS ends at Fly's edge, TURN binds to `fly-global-services`
on the app's dedicated IPv4, and the data directory lives on a volume.

## The viewer

The viewer is Roadie's own, served from the `viewer` package by both. A rig
that announces an `extras` script (Roadie does) gets its own buttons and
panels added to the page through the relay, and the shell itself never names
a phone. When WebRTC cannot connect, the viewer falls back to the MJPEG
stream relayed through the station, which the rig caps at a few frames a
second, while input goes over a relayed WebSocket. `?transport=mjpeg` on the
viewer's URL picks that from the start, and `?ice=relay` forces the media
through TURN, to check the relay path from a browser.
