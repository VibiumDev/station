# Packages

`station` is the self-hosted binary: one operator, their own rigs, and one
SQLite file for everything it keeps. It is built from Go packages that a
hosted service can embed instead of forking. They take an authenticator, an
authorizer and their stores as interfaces, and their handlers mount under
any path prefix.

| Package | Holds |
|---|---|
| root `station` | The model (rigs, devices, holds, principals, scopes) and the five interfaces an embedding application implements: `Authenticator`, `Authorizer`, `DeviceStore`, `HoldStore`, `AuditSink`; `EventStore` is khatru's |
| `token` | Hold grants and revocations as signed Nostr events; see [kinds.md](../kinds.md) |
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
| `jam` | Jam sessions: the station's own agent loop, the AI models and their providers, and the settings handed to Vibium for speech |
| `viewer` | The one viewer page a station and a Roadie both serve (the toolbar, settings panel and on-screen keyboard, the MJPEG reader, sound and microphone, input in picture space, WebRTC), told its endpoints by a config and extended by a rig's own extras module. The embedded Record Player is under `player/`, built from the [record-player](https://github.com/VibiumDev/record-player) repository by `scripts/update-player.sh` |
| `cmd/station` | The station binary |
| `cmd/station-probe` | A headless check of any station: token, hold, WebRTC through TURN only, a tap and the screenshot it changes |
