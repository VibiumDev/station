# Vibium Station

Vibium Station brokers access to remote devices. Each machine with a device
on it, a [Roadie](https://github.com/VibiumDev/roadie) Pi with a phone for
now, enrolls as a rig and keeps one outbound tunnel to the station; people
and agents reach the devices through the station's pages and API. The
station signs every session as a lease grant that the rig checks itself, so
the rig stays the final gate.

`station` is the single-player binary: one owner, their own rigs, a
password or a tailnet identity, one SQLite file. The packages it is built
from take an authenticator, an authorizer and stores as interfaces, so a
hosted service can embed them instead of forking.

Status: R3 of the [remote spec](docs/remote-spec.md). LAN and tailnet mode,
enrollment, the tunnel, leases and grants, and the viewer over WebRTC work.
Public mode with TURN (R4), the REST, BiDi and MJPEG relay for agents (R5),
and packaging (R6) are next. Progress and numbers are in
[docs/progress.md](docs/progress.md).

## Run it

Go 1.27 or newer.

```bash
go build ./cmd/station
./station passwd                      # the owner's password
./station                             # LAN mode: https://<your ip>:8443, a self-signed certificate
./station --tailnet --allow you@example.com   # tailnet mode: https://station.<tailnet>.ts.net, sign in by identity
```

The first run makes the station's key and prints its `npub`. Tailnet mode
joins your tailnet as a node named `station` through `tsnet` and prints a
login link once; set `TS_AUTHKEY` to skip that. `--control-url` points it
at a Headscale server instead.

Then enroll a rig:

1. On the station's Settings page (or `station enroll`), make an
   enrollment code. It is single use, lasts 15 minutes, and ends with the
   station key's fingerprint.
2. On the rig: `roadie enroll https://<station> --code ABCD-1234-7F3A`. On
   the same LAN the URL can be left out; the station advertises itself over
   mDNS. The rig checks the station's key against the code, sends its key
   and devices, and writes its policy file.
3. Restart `roadie`. The device appears on the station's front page; Open
   takes a lease and shows it over WebRTC.

Agents use API tokens from the Settings page or `station token create
NAME`, sent as `Authorization: Bearer`. A read-only token may watch but
never take control. See [API.md](API.md).

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
| `--listen` | `:8443` | LAN mode address |
| `--tailnet` | off | Join the tailnet with tsnet and serve there only |
| `--hostname` | `station` | The node's name on the tailnet |
| `--control-url` | Tailscale's | A Headscale or other control server |
| `--allow` | none | Tailnet logins and tags that may sign in by identity (saved) |
| `--public-url` | derived | How rigs and browsers reach the station; the tunnel URL follows |
| `--name` | `station` | The station's name (saved) |
| `--no-mdns` | off | Do not advertise on the LAN |

## Packages

| Package | Holds |
|---|---|
| root `station` | The model (rigs, devices, leases, principals, scopes) and the five interfaces an embedding application implements: `Authenticator`, `Authorizer`, `DeviceStore`, `LeaseStore`, `AuditSink`; `EventStore` is khatru's |
| `token` | Lease grants and revocations as signed Nostr events; see [docs/kinds.md](docs/kinds.md) |
| `lease` | One lease per device: sequence numbers, takeover by input scope, observers, idle and maximum time |
| `tunnel` | The rig's outbound WebSocket, a signed challenge, then HTTP over yamux streams |
| `signal`, `relay` | The viewer's signaling WebSocket and the reverse proxy to a rig |
| `enroll` | Codes, the profile and enrollment endpoints, mDNS |
| `rig` | The rig library: key, policy file, enrollment, the tunnel client, the gate and allowlist |
| `sqlite` | The one-file store, pure Go |
| `viewer` | The shared browser code and the station's viewer page |
| `cmd/station` | The single-player binary |

## Security in short

Every key is secp256k1 with BIP-340 Schnorr, shown as an npub. A lease
grant is a signed event with the rig, device, lease id, sequence number,
scopes and a ten-minute expiry; the rig refuses forged, expired,
wrong-device and older-sequence grants, replays, revoked leases and
anything its policy file does not allow. Tokens travel in headers or the
first WebSocket message, never in URLs. The station always serves TLS. A
rig pins the station's certificate at enrollment when no root signs it,
and the station's key always.
