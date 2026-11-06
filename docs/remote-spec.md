# Roadie Remote: WebRTC and Vibium Station Spec

Oct 25, 2026 · @Jason Huggins

## Summary and goals

Roadie Remote makes a Roadie Pi usable from anywhere on the Internet, as part of the open-source Roadie project. It adds WebRTC video, audio and input to Roadie, and a small server component, **Vibium Station**, that you run on your LAN, on your tailnet or on any server. Video and audio are equally important. MJPEG sends every frame as a complete JPEG over TCP, which costs roughly ten times H.264's bandwidth for a mostly static phone screen and builds up delay on a congested link; WebRTC fixes both, and input rides the same connection. MJPEG stays as the simple, always-works fallback and the snapshot source for agents.

Vibium Station is a sibling open-source project from Vibium that brokers access to remote devices. Roadie is its first device provider: each Pi with a phone enrolls as a rig. Headless and desktop browsers are planned as a second kind, so Station's model is device-neutral from the start. Station runs single player: one owner, their own rigs, a password, and ideally a tailnet. It is also a set of Go packages, so a hosted service can embed it with its own accounts and storage instead of forking it. Multi-customer concerns (accounts, vendor email, billing, a provider's production setup) belong to such services and are out of scope here.

1. **Low-latency remote video and audio**: H.264 video plus two-way USB audio (the phone's speaker to the user, the user's mic to the phone) over WebRTC, glass-to-glass under \~250 ms on a decent connection, at a fraction of MJPEG's bandwidth.
2. **Low-latency remote input**: touch, keyboard and mouse in Roadie's existing JSON command format on the same peer connection; REST and WebDriver BiDi through the station for agents.
3. **No inbound ports on the Pi**: every Pi dials out to its station; works behind home or office NAT.
4. **The Pi is the final gate**: it verifies every session itself and enforces its own limits, even if the station is wrong or compromised.
5. **The Pi is hard to break**: whoever controls the phone cannot reach, modify or brick the Pi.
6. **Easy to self-host**: run `roadie` on the Pi and `station` on another machine on your LAN, a spare Raspberry Pi, a cheap VPS or Fly.io; enroll with a one-time code; sign in with a password.
7. **Embeddable**: the station's packages take an authenticator, an authorizer and stores as interfaces, so a hosted service can reuse them without forking.

## Scope and phasing

First release: everything below. Later: polish that a growing community of self-hosters will ask for.

|  | In scope (open source) | Out of scope here |
| --- | --- | --- |
| Pi | WebRTC video, audio and input; capture and encoder; tunnel client; token checks; hardening scripts | Vendor-specific images |
| Station | Tunnel endpoint, signaling, REST/BiDi relay, TURN, leases, shared viewer, as packages plus the `station` binary | Multi-tenant accounts, billing |
| Login | One owner, password; optional tailnet-only mode | Email codes, SSO, customer sign-up |
| Agents | A few named API tokens for the owner's agents | Per-customer keys and plans |
| Hosting | Any server; Docker image, systemd unit and a sample Fly config in the docs | A specific provider's production setup |

One Pi controls one device. The owner may enroll several Pis with one station.

**Not Pi-only.** In this spec "Pi" is shorthand for the Roadie host. A Raspberry Pi 4 is the reference host, but a Mac mini or a Linux PC must also work. Roadie detects what its host can do and announces it to the station as capabilities; nothing assumes Pi-only hardware.

|  | Raspberry Pi 4 | Linux PC | Mac mini |
| --- | --- | --- | --- |
| HID to the phone | USB gadget (`linux-gadget` backend) | QT Py relay and HID boards, or a gadget-capable USB port | QT Py relay and HID boards |
| Mic into the phone | Gadget microphone | HID board's USB microphone | HID board's USB microphone |
| Phone audio out | Gadget speaker, else HDMI | HDMI via the capture dongle | HDMI via the capture dongle |
| Ethernet gadget for the phone | Yes | Only with a gadget-capable port | No |
| H.264 encoder | V4L2 M2M hardware (`/dev/video11`) | VAAPI, NVENC or Quick Sync via ffmpeg; libx264 fallback | VideoToolbox via ffmpeg |
| Hardening scripts | systemd and nftables | systemd and nftables | launchd and pf (later) |

The Pi 5 has no hardware H.264 encoder, so Pi hosts are Pi 4s; a Pi 5 would fall back to software encoding.

Non-goals for now:

- Multiple devices per Pi, and several people controlling one device at once (one controller plus one read-only observer is supported).
- Multiple user accounts in `station`. Teams that need them can embed the packages.
- iOS-specific tooling beyond HID input and video.
- A media server (SFU). Peer-to-peer plus TURN covers a controller plus one observer per device.

## System overview

Three parts: `roadie` on each Pi, which dials out; `station` on a server you choose, which brokers access; and clients (a browser or an agent). Video, audio and human input go directly between browser and Pi whenever possible.

&#91;embedded content: System overview · clients, cloud, phone site\]

The Pi (highlighted) is the only component that touches the device, and it checks every token itself. The station never sees video unless TURN relays it, and even then only ciphertext. In tailnet mode, browser and Pi usually reach each other directly over the tailnet and TURN is rarely needed.

## Video and audio pipeline

Encode H.264 on the Pi 4's hardware encoder and send it over WebRTC with Pion; keep MJPEG as a second consumer of the same capture, encoded only while someone is using it.

### Pipeline

1. **Capture YUYV over USB 3**: the dongle offers both MJPEG and uncompressed YUYV (4:2:2). Roadie captures through `pion/mediadevices` today. For WebRTC, capture YUYV (see Capture format below); MJPEG capture remains the fallback for dongles without full-rate YUYV.
2. **Tap the raw frame before JPEG encoding**: today the capture loop turns every frame into an image and JPEG-encodes it in pure Go (`image/jpeg`) for `/stream`, whether or not anyone is watching. Split that: JPEG-encode only while an MJPEG consumer needs it (a `/stream` viewer, a snapshot, the relay), at that consumer's capped rate; H.264-encode only while a WebRTC viewer is connected. An idle Pi then does neither.
3. **Crop and convert**: apply the existing auto-crop as a sub-rectangle. A portrait phone inside a 1920×1080 frame is roughly 600×1080 of real pixels, so encoding only the crop cuts work and bitrate by about two thirds. Convert 4:2:2 to 4:2:0 (NV12 or I420) by dropping every other chroma row, which is cheap; or let the Pi 4's ISP (`/dev/video12`) crop, scale and convert in hardware.
4. **Encode H.264** with the best encoder the host has: on a Pi 4, `/dev/video11` via V4L2 M2M; on a Mac, VideoToolbox; on a Linux PC, VAAPI, NVENC or Quick Sync, with libx264 as the universal fallback.
5. **Packetize and send** with `pion/webrtc` v4 (already a dependency through `mediadevices`) as a `TrackLocalStaticSample`.

### Capture format: YUYV over USB 3

Capture YUYV. It skips a JPEG decode, keeps one lossy step instead of two (so text stays sharp), and costs about a third of MJPEG's CPU. Measured on the reference dongle, a UGREEN 15389 on USB 3, at 1080p30 into the Pi 4's hardware encoder (ffmpeg, 20 s):

|  | YUYV | MJPEG |
| --- | --- | --- |
| Capture rate | Steady 30 fps | 30 fps after a slower start |
| CPU before encoding | About 0.4 core (4:2:2 to 4:2:0 conversion) | About 1 core (software JPEG decode) |
| Kept pace | 0.995×, about 3% fewer frames | 1.0× |

YUYV at 1080p30 is about 1 Gbps, so it needs USB 3. Roadie reads the dongle's modes at startup and picks YUYV when full-rate YUYV is offered, otherwise MJPEG (decoded on `/dev/video10` if software decoding is too slow).

### Encoder settings

| Setting | Value | Why |
| --- | --- | --- |
| Profile | Constrained Baseline | Every browser decodes it; no B-frames, so lowest latency |
| Level | 4.0 (set explicitly) | `bcm2835-codec` defaults to level 1.0 (176×144) unless told otherwise ([moq-video notes](https://docs.rs/crate/moq-video/0.0.23/source/src/encode/backend/v4l2.rs)) |
| SPS/PPS | Repeat with every IDR | A viewer joining mid-stream can only start on a keyframe that carries them |
| GOP | 2 s, plus a forced IDR on PLI/FIR from the browser | Fast recovery after packet loss or a new viewer |
| Rate control | CBR, 0.5–4 Mbps, driven by Pion's congestion feedback | Phone UIs are mostly static, so low bitrates look sharp |
| Frame rate | 30 fps max; drop to 15 fps under congestion | Keeps latency low rather than buffering |
| Resolution presets | Native crop, 720p-height, 540p-height | Selectable by the viewer and API |

### Encoder access from Go

The encoder sits behind a small Go interface (`Encoder.Encode(frame) -> []Sample`, `SetBitrate`, `ForceKeyframe`). Implementations, in order of preference:

1. **Direct V4L2 M2M from Go** (ioctls via `golang.org/x/sys/unix`). Lowest latency and fewest processes; this is how [ustreamer added Pi hardware encoding](https://lalitm.com/hw-encoding-raspi/).
2. **`ffmpeg -c:v h264_v4l2m2m` subprocess** writing Annex-B to a pipe. Fastest to get working; acceptable for a first release if (1) slips.
3. **ffmpeg subprocess on other hosts**, choosing the best available encoder: `h264_videotoolbox` on macOS, `h264_vaapi`, `h264_nvenc` or `h264_qsv` on Linux PCs, and `libx264` as the universal fallback and for development. Go bindings for x264 and openh264 need cgo, so they are out.

Avoid a cgo dependency on GStreamer unless both (1) and (2) fail.

### MJPEG stays

`/stream` (MJPEG) and the single-frame snapshot endpoint stay as they are, for three reasons: agents want individual JPEG frames for vision; it is the fallback when UDP is blocked and TURN is unavailable; and it is the easiest thing to debug on a LAN. Remotely, MJPEG is served through the cloud relay at a capped frame rate (default 5 fps) so it cannot eat the Pi's uplink.

### Audio (two-way)

Audio is as important as video: both audio tracks ship in the first release. The relayed audio WebSockets are only the fallback for when WebRTC cannot connect.

Roadie already handles both directions on the LAN over WebSockets. Remotely, both become WebRTC audio tracks on the same peer connection as the video; the existing endpoints stay unchanged for LAN use and as the relayed fallback.

| Direction | Today on the LAN | Remote |
| --- | --- | --- |
| Phone to user (speaker) | `GET /audio`: 48 kHz stereo float32 PCM, about 3 Mbps; follows the gadget's USB speaker when the phone plays to it, HDMI otherwise | Audio track sent by the Pi |
| User to phone (mic) | `WS /api/hid/audio`: 16 kHz mono 16-bit PCM, 256 kbps, played out of the gadget microphone; needs a 16 kHz AudioContext, so no Firefox | Audio track from the browser's `getUserMedia`, decoded on the Pi and fed into the existing gadget-mic path |

Why tracks rather than relaying the WebSockets: raw PCM at about 3 Mbps is too heavy for a remote link; the browser's WebRTC stack adds jitter buffering, echo cancellation and sync with the video; and the mic no longer needs the 16 kHz AudioContext, so Firefox works too.

Codec: Roadie deliberately avoids cgo (its ALSA code talks to the kernel directly so the binary cross-compiles), and Go has no mature pure-Go Opus encoder. So:

- **First: G.722** in both directions (16 kHz wideband, 64 kbps, a WebRTC codec browsers offer), implemented in pure Go; PCMU as a fallback. The Pi offers only G.722 and PCMU in its SDP, so browsers do not default to Opus. It already matches the mic path's 16 kHz. Use Roadie's existing resampler between 48 kHz and 16 kHz.
- **Later, if speaker quality matters** (music, video apps): Opus via libopus behind a build tag, or an encoder subprocess, so the default build stays cgo-free.

The speaker track is on by default and mutable in the viewer; the mic track is sent only after the user turns the mic on. In MJPEG fallback mode, the viewer uses the relayed audio WebSockets, with the speaker stream downmixed to 16 kHz mono 16-bit (256 kbps) rather than 48 kHz float32.

## Input path

Roadie already takes input over a WebSocket (`/api/hid/ws`) as JSON commands such as `{"cmd": "touch", ...}`. Remotely, keep that exact format and change only the transport: the viewer sends the same JSON over a WebRTC data channel named `hid` on the peer connection that carries the video and audio. Agents keep using REST and WebDriver BiDi, relayed through the cloud. Everything lands in the same HID handler on the Pi.

Why a data channel instead of relaying the existing WebSocket: a relayed WebSocket travels browser to the station to the Pi's tunnel, while the video travels directly. Input would take a longer path than the picture it reacts to, usually adding tens of milliseconds, and TCP head-of-line blocking stalls touch streams on lossy links. The data channel follows the media's path (direct, or through TURN only when needed) and is covered by the same peer-connection authentication. The relayed WebSocket stays as the fallback when WebRTC cannot connect.

| Path | Used by | Transport | Latency target |
| --- | --- | --- | --- |
| WebRTC data channel `hid` | Viewer | The JSON commands `/api/hid/ws` accepts today, over ordered, reliable SCTP on the peer connection | < 50 ms added |
| WebRTC data channel `control` | Viewer | Quality changes, keyframe requests, stats | n/a |
| WebRTC audio tracks | Viewer | Phone speaker to user and user mic to phone (see Video and audio pipeline) | \~60 ms jitter buffer |
| `/api/hid/ws`, `/audio`, `/api/hid/audio`, relayed | Viewer in MJPEG fallback mode | WSS to the station's `/v1/devices/{id}/ws/hid`, `/ws/audio` and `/ws/mic`, relayed over the Pi's tunnel; speaker audio downmixed to 16 kHz mono 16-bit when relayed | < 150 ms added |
| REST | Agents, scripts | HTTPS to the station, relayed to the Pi over its tunnel | < 150 ms added |
| WebDriver BiDi | Vibium, Selenium, Puppeteer, WebDriverIO | WSS to the station, relayed to the Pi's existing BiDi endpoint | < 150 ms added |
| MJPEG / snapshot | Agents doing vision | HTTPS via the relay | n/a |

Rules:

- **One message format**: the data channel carries exactly the JSON commands `/api/hid/ws` accepts today. One handler serves both transports, so the viewer's input code changes only where it sends.
- **One controller at a time**: the Pi accepts input from exactly one lease. A second connection with input scope on the same lease takes over, and the first is told it was replaced. Connections with view scope only are observers and never take over; one observer is allowed, so the Pi encodes once and sends to at most two peers.
- **Coordinates stay in Roadie's absolute HID units** (0–32767 across the cropped frame), which are already independent of resolution and crop, so a quality change never misplaces a tap.
- **Rate limit on the Pi**, not only in the station (for example 200 HID events/s and 2,000 keystrokes/min), so a runaway agent cannot flood the phone.
- **The relay is dumb**: the station forwards REST/BiDi bytes for an authorized lease and never interprets HID commands. The Pi validates everything again.
- **Local access is checked per `--auth`.** Without a station, Roadie's `--auth` flag decides who may use `/view` and the local API:
  - **Unset (the default):** no checks, for quick local work, but Roadie then listens on localhost only.
  - `--auth=token`: the local admin token. `roadie token` prints it, `/view` asks for it once on a minimal `/login` page and keeps it in an HttpOnly cookie, and the API takes `Authorization: Bearer`.
  - `--auth=tailscale`: Roadie asks the local tailscaled who each connection belongs to (`WhoIs`) and allows the Tailscale logins and device tags listed in its config. Nothing to manage on a tailnet. Requests forwarded by `tailscale serve` (for an HTTPS `*.ts.net` name) are checked by their `Tailscale-User-Login` header instead, and are trusted only when Roadie listens on localhost.
  - `--auth=none`: no checks on any `--listen` address, chosen deliberately, with a warning on every start.

  Remote sessions through a station always need a station lease grant, whatever `--auth` says.

## Vibium Station

Vibium Station is the open-source server side: Go packages plus a single-player binary, `station`. It holds the Pis' outbound tunnels, brokers WebRTC signaling, relays REST and BiDi for agents, runs optional TURN, and signs the session tokens each Pi checks. It never sees video unless TURN relays it, and then only ciphertext.

Why a station at all: Pis sit behind NAT with no inbound ports, so something reachable must broker signaling, hold the tunnels and issue tokens. On a tailnet you could open a Pi's `/view` directly; the station adds one address for all your Pis, sign-in, agent tokens and leases, and also works off the tailnet.

### Repos, rigs and devices

Vibium Station lives in its own repo, a sibling of Roadie:

| Repo | Contains |
| --- | --- |
| `VibiumDev/station` | The station packages; `cmd/station`; `cmd/station-probe`; and the `rig` library: tunnel client, enrollment, session-token checks and the endpoint allowlist helper |
| `VibiumDev/roadie` | `cmd/roadie`, the Pi side: capture, encoder, WebRTC, HID and audio. Imports Station's `rig` library |

The two repos move in lock step for now. Develop with a Go workspace (`go.work` in the parent directory, not committed) so both build together; commit to Station first, then bump Roadie's dependency. Station releases are tagged, and Roadie and any embedding service pin a tag.

**Rigs and devices.** A rig is an enrolled machine with one rig key (an npub) and one tunnel. A rig offers one or more devices, each with a `kind` (`phone` now, `browser` later) and capabilities (`video`, `audio`, `hid`, `bidi`, `mjpeg`). A Roadie Pi is a rig with one phone. A future browser rig, on a Pi or a bigger machine, could offer several headless Chromes created on demand, with `bidi` and optionally `video`. Leases, session tokens and API paths name the device; the tunnel belongs to the rig. Station's code never says "phone": phone-specific behaviour lives in Roadie.

**BiDi as the common language.** Every device that agents drive exposes WebDriver BiDi through the station: Roadie's BiDi flavour for phones, Chrome's native BiDi for browsers. The same Vibium script can target either.

### Packages

| Package | What it does |
| --- | --- |
| `tunnel` | Accepts Pis' outbound WSS at `/v1/tunnel`, authenticates rig keys, multiplexes streams, keeps them alive |
| `signal` | Relays SDP and ICE between viewer and Pi; the first message carries the lease token |
| `relay` | REST, BiDi, MJPEG, screenshots and the fallback WebSockets, over the tunnel |
| `lease` | Per-device sequence numbers, takeover by input scope, one observer, release after 5 idle minutes or 8 hours |
| `token` | Signs lease grants (Nostr-format events, secp256k1 Schnorr) and short-lived TURN credentials |
| `turn` | Embedded `pion/turn`: per-lease credentials only; refuses private, loopback and link-local destinations; listen address, advertised public IP and MTU are configurable |
| `viewer` | The shared viewer module (video, audio, touch, keyboard, mic), embedded so the station and `roadie` serve the same code |

An embedding application implements five small interfaces: `Authenticator` (who is this request), `Authorizer` (may this principal do this to this device), `DeviceStore`, `LeaseStore` and `AuditSink`. Handlers mount under any prefix, so a hosted service can expose `/v1/phones` where `station` uses `/v1/devices`. No accounts, email vendors, billing or provider-specific code go into these packages.

### station (single player)

- **One owner.** A password set with `station passwd`, stored as an argon2id hash in the config file; login is rate-limited; session cookies are `HttpOnly`, `Secure`, `SameSite=Lax`, with CSRF tokens.
- **Agent tokens.** A few named API tokens, created and revoked on the settings page or the CLI, stored hashed, optionally read-only.
- **LAN mode.** The station does not have to be in the cloud: run `station` on any machine on the same network as the Pis (a spare Pi, a NAS, a desktop), reachable only from that network. Browsers and Pis then connect directly on the LAN, so TURN is never needed. To reach it from outside later, add tailnet mode or move it to a public server and re-point the Pis.
- **Tailnet mode (recommended).** `station --tailnet` joins your tailnet as its own node using Tailscale's `tsnet` Go library, with Tailscale's certificates. It is reachable only from your tailnet and exposes nothing to the Internet. In this mode `station` can also identify people by their Tailscale identity instead of a password: it asks tailscaled who each connection belongs to (`WhoIs`) and allows the Tailscale logins and device tags listed in its config, for example `you@example.com` for people and `tag:ci` for agent machines. The password stays available as a fallback. A `--control-url` flag points it at a self-hosted Headscale server instead of Tailscale's, for an all-open-source setup. Plain WireGuard, ZeroTier or Nebula need no special support: bind `station` to that interface's address, exactly as in LAN mode.
- **Public mode.** Listens on 443 with automatic Let's Encrypt certificates, or on plain HTTP behind your own reverse proxy. Password and tokens required; TURN usually on.
- **Enrollment.** One-time codes that carry the station's key fingerprint, and mDNS discovery on a LAN; see Enrollment and connection below. Removing a rig on the devices page revokes it.
- **Storage.** One SQLite file next to the config; no other services.
- **TURN.** Off by default in tailnet mode; on in public mode, which needs a public IP and UDP 3478 (TCP 3478 and TLS 5349 optional). Options for providers with UDP quirks, such as Fly.io's `fly-global-services` bind address, are documented in the self-hosting guide.
- **Runs anywhere.** One static binary for linux/amd64 and linux/arm64, a Docker image, a systemd unit, and a sample `fly.toml` in the docs.

### Enrollment and connection

A rig always calls the station; the station never reaches out. A rig exists because it enrolled, and it is online only while its tunnel is up.

**Enrollment, once per rig**

1. The owner creates a one-time code with `station enroll` or on the settings page. It is single-use, valid for 15 minutes, and ends with the first characters of the station's signing-key fingerprint, for example `ABCD-1234-7F3A`.
2. On first boot the rig generates a secp256k1 rig key, shown as an `npub`; the private half never leaves the rig.
3. The owner runs `roadie enroll https://station.example --code ABCD-1234-7F3A`. On a LAN the URL can be left out: the station advertises `_vibium-station._tcp` over mDNS and the rig finds it.
4. The rig checks that the station's key matches the fingerprint in the code and stops if it does not. This protects LAN mode, where the station has a self-signed certificate and a spoofed station could otherwise intercept the first enrollment.
5. The rig sends the code, its public key, a name and its devices (kind and capabilities). The station checks and burns the code, then stores the rig.
6. The station returns the rig id, its signing public key and the tunnel URL. The rig saves them and from then on trusts tokens signed only by that key.

**Every connection after that**

1. The rig dials `wss://<station>/v1/tunnel` and keeps it open, reconnecting with backoff and jitter.
2. The station sends a random challenge; the rig signs it with its rig key; the station looks up the key and marks the rig online.
3. The rig announces its current devices on each connect and whenever they change, for example when a phone is unplugged or the capture signal is lost.

Removing a rig on the station refuses its key from then on; the rig must enroll again.

### Pi tunnel

Each Pi keeps one outbound WebSocket to `wss://<station>/v1/tunnel`, reconnecting with exponential backoff and jitter. Streams: `signal`, `rest`, `bidi`, `mjpeg`, `hid`, `audio`, `mic`, `control`. The Pi authenticates by signing a server nonce with its secp256k1 rig key, generated on first boot and registered at enrollment. REST, BiDi and fallback traffic ride the tunnel; only WebRTC media goes peer-to-peer or through TURN.

## Nostr and federation readiness

Station is not a Nostr relay, but it uses Nostr's identity and event formats. Stations, rigs and the people who run them can then find and trust each other across the Nostr network later, and rigs run by different people can connect to stations they do not own without a redesign.

### Identity

- Every key is secp256k1 with BIP-340 Schnorr signatures, shown as an `npub` (NIP-19): each station, each rig, and each operator (the person or business that owns rigs). An operator key can be an existing Nostr identity.
- Stores keep a key-type field, so hardware-backed keys can be added later. Most secure elements, including the ATECC608 used on Pis, support P-256 rather than secp256k1.
- Library: [`fiatjaf.com/nostr`](https://pkg.go.dev/fiatjaf.com/nostr), the maintained rewrite of go-nostr, or btcec directly. Pure Go only; confirm no cgo.
- Persistent devices (a phone) also get their own key and `npub`, held on the rig and bound to it by a rig-signed event. A phone's identity, history and later its reputation then survive moving it to a different Pi: the old binding is revoked and the new rig signs a new one. Short-lived slots, such as a headless Chrome created for one session, have no key of their own and are named by their rig.

### Signed events

Station's signed messages use the Nostr event format (NIP-01: id, pubkey, created\_at, kind, tags, content, sig), with Station's own documented kinds.

| Event | Signed by | Where it goes |
| --- | --- | --- |
| Enrollment request | Rig | Tunnel only |
| Operator attestation ("this rig is mine") | Operator | Tunnel; public later if the operator lists the rig |
| Rig announcement (devices, capabilities) | Rig | Tunnel; public later for listed rigs |
| Lease grant (the session token) | Station | Tunnel, data channel or first WebSocket message; never published |
| Revocation | Station | Tunnel |
| Station profile | Station | Public later |

Private events are never published, and when one has to cross a third party its content is encrypted with NIP-44. The transport stays the tunnel; nothing depends on Nostr relays today. Later a station can publish its public events to Nostr relays so rigs and customers can find it by `npub`. Shared discovery should not be built on NIP-90 (Data Vending Machines), which is flagged unrecommended upstream in favour of use-case-specific standards ([D-Central summary](https://d-central.tech/nostr-data-vending-machines/)); Station defines its own kinds.

### Relay-ready event store

Station runs no relay in the first release, but it stores and shapes its events so a relay can be switched on later without a migration:

- **Every event kept verbatim.** Each event Station signs or accepts is stored exactly as signed, in NIP-01 form (id, pubkey, created\_at, kind, tags, content, sig), in an append-only `events` table in SQLite. Refused or invalid events are not stored. Live state (rigs, devices, leases, sequence numbers) stays in ordinary tables, because a lease change must be one transaction.
- **A relay-shaped interface.** Events are reached through an `EventStore` interface: save an event, query by NIP-01 filter (ids, authors, kinds, `#p` and `#d` tags, since, until, limit), delete. Its methods follow khatru's event-store interface, so the same store can later back an embedded khatru relay. Station ships a SQLite implementation; an embedding service may supply its own.
- **Kinds in the right NIP-01 ranges**, so a relay treats each one correctly: lease grants, revocations and enrollment requests are regular events (kept, for audit); a rig announcement is replaceable (only the newest per rig is kept); each device's announcement is addressable, with a `d` tag naming the device; the station profile is kind 0 metadata. Station's kind numbers are listed in one file, `docs/kinds.md`.
- **Parties in `p` tags.** Every event names its parties (station, rig and device keys) in `p` tags, alongside Station's own tags. Relays index only single-letter tags, so `p` tags are what make "readable only by the event's own parties" enforceable under NIP-42 later.
- **Never edited.** A correction is a new event; a removal is a NIP-09 deletion request.

**Private first, open later.** Everything is a Nostr key or a Nostr event from day one, but nothing is published. When a station needs to share events with rigs it does not run itself (rigs run by other operators), it switches on a private Nostr relay over the same event store, using [khatru](https://pkg.go.dev/fiatjaf.com/nostr/khatru), part of the same `fiatjaf.com/nostr` module: only enrolled `npub`s may write, reads require NIP-42 AUTH and are limited to the event's own parties. Opening up later is then a policy change rather than a rewrite: allow public reads of public kinds (station profiles, listings), or mirror them to public relays.

### Files: Blossom

"Records" in Vibium's sense (the `record.zip` traces Vibium produces), plus session videos, screenshots and logs, are files, not events. They go in a [Blossom](https://d-central.tech/blossom-protocol-reference/) store, Nostr's protocol for blobs over HTTP:

- **Durable links:** every file is addressed by its SHA-256 hash (BUD-01), so a link stays valid when the file moves between servers or is mirrored (BUD-04). Events refer to files by hash.
- **Nostr-key auth:** uploads, listing and deletion are authorized by signed Nostr events (BUD-02, BUD-11, BUD-12), using the same keys as everything else.
- **Private first:** protected files are supported. Blossom lets a server require a signed auth event on downloads too (BUD-01 returns 401 without one; BUD-11 defines the event). The station allows a download only for the file's owner and the parties of its lease. For files that also need protection from the storage itself, the uploader encrypts before upload and shares the key in an encrypted event (NIP-44).
- **Implementation:** [khatru's `blossom` package](https://pkg.go.dev/fiatjaf.com/nostr/khatru) can serve Blossom from the same Go process as the event store, backed by disk or object storage.

Who uploads: the rig uploads what it produces (session recordings, screenshots, logs), and the Vibium engine uploads a run's `record.zip` when the recording stops, because the engine already holds the station's address and the token it connected with. An `upload` option on `vibium:recording.stop` gives the CLI (`vibium record stop --upload`), the SDKs and the MCP tool the same switch from one implementation, and a documented `curl` pair covers any other client. In single-player Station the Blossom store is optional and runs next to the station; a hosted service can run a larger one.

Who signs: the party that produced the file signs the Blossom upload, and the station signs the index entry in every case. A recording the rig made is signed by the rig key in a plain BUD-02 upload, which makes it the stronger artifact, because it records what the phone showed and received and the agent cannot alter it after the fact; a recording the agent made on its own machine is the driver's account of the same session. People never sign: a person is identified by the station's session, and the station's event names them.

Agents never hold Nostr keys. Their credential stays the API token, which can be revoked on its own and asks nothing of CI, and the station signs on the account's behalf, the way a presigned URL does for object storage. The client computes the file's SHA-256 and size and asks `POST /v1/records/upload-auth` with its token, naming the hold it drove; when the request names no hold the station takes the token's current one, and refuses when that is ambiguous. The station checks that the token could drive that hold, and the quota, and answers with a kind 24242 auth event signed by the account key, scoped to `t=upload` and `x=<sha256>` and expiring five minutes out, so a leaked one is good only for uploading that same file within five minutes. The client PUTs the file to the Blossom store with that event in the `Authorization` header and the store verifies an ordinary Blossom signature. That keeps the store standard: the same flow works against the embedded store, a mirror or a customer's own Blossom server. The station then writes the file-metadata event (kind 1063) naming the hash, the hold, the device, the rig and the uploader (the token's name or the rig's npub), which is what the audit trail reads next to the grant the station signed and the hello the rig signed.

The account key is the npub that owns the files. In single-player Station it is the station key, behind a `Signer` interface, since setup makes no other key and the station is the owner's. A hosted service plugs a key per customer account into that interface, held server-side and encrypted at rest and never sent to agents. A customer who already has a Nostr key can later link their npub and sign uploads themselves, directly or through a NIP-46 bunker, and the presign step becomes optional for them. NIP-26 delegation is not used, since upstream marks it unrecommended.

Downloads outside the station's own pages work the same way with `t=get`, and the store requires the event on `GET` for every file that is not public, which Blossom permits but does not assume. The event is a bearer, so it travels in a header and never in a URL. A shareable link therefore cannot carry it, and the route that needs nothing new is to download the file from the station's recordings page under the signed-in session and open it in the Record Player. A direct "open in the Record Player" link would need the player to send a header or accept a one-time URL, which is a question for the player rather than for the station.

### Rig policy: the operator's consent

A rig can be enrolled with a station someone else runs, so the rig's operator, not the station, decides what that station may do. A policy file on the rig, editable only on the rig, sets:

- Which stations the rig accepts, by `npub`. It is a list: a rig may enroll with more than one station, for example its owner's own station and a hosted one.
- Per station: allowed scopes (view, input, bidi), whether admin grants are accepted (by default only from the operator's own station), maximum session length, and optional allowed hours.
- One active lease across all stations; the Pi arbitrates.

The Pi checks every grant against this policy as well as its signature, so no station can exceed what the operator allowed.

### Three kinds of rig, one model

| Kind | Operator | Example |
| --- | --- | --- |
| Owner's own | The station's owner | Single player, or a hosting service's own devices |
| User-owned | A user of someone else's station | Someone's own devices connected to a station they don't run, like GitHub self-hosted runners |
| Third-party | An independent operator | Independent operators offering devices through a station |

All three are ordinary rigs with an operator `npub` and a rig policy. Who may lease them, billing, vetting and reputation live in the hosting service, not in Station.

## Pi as the final gate

The station decides who should get a device; the Pi decides whether to obey. Every viewer connection and every relayed API call carries a short-lived session token that the Pi verifies itself, offline, against a public key pinned on the Pi.

### Session token

A lease grant is a signed event in the Nostr event format (NIP-01), signed by the station's secp256k1 key with BIP-340 Schnorr. It is never published; it travels only over the tunnel, the data channel or the first WebSocket message.

| Field | Example | Pi checks |
| --- | --- | --- |
| `pubkey` | The station's key | Must be a station in the rig policy, pinned at enrollment |
| `kind` | Station's lease-grant kind | Must be a lease grant |
| Tag `sub` | `user_42` | Logged for audit |
| Tags `rig`, `dev` | The rig's `npub`, `pi-0007/phone` | Must name this rig and one of its devices |
| Tags `lease`, `seq` | `lse_9f3c`, `42` | Must match the current lease, or carry a higher seq (a per-device counter the station increments for every new lease) |
| Tag `scope` | `view input bidi` | Each operation needs its scope (view: video, speaker audio, screenshots, MJPEG and status; input: HID and mic; bidi: WebDriver BiDi), and the rig policy must allow it; `view` alone is read-only |
| `created_at`, tag `expiration` (NIP-40) | 10-minute lifetime | Rejected if expired (Pi clock via NTP, 30 s skew) |
| `id` | The event hash | Unique; replays are refused; refreshed grants arrive over the data channel or tunnel |

### Rules the Pi enforces

1. **Pinned key**: the station's signing key is pinned at enrollment and rotated only by re-enrolling. A token signed by anything else is refused.
2. **One active lease**: the Pi stores the current lease id and seq. A token with a lower seq is refused; a newer lease ends the old session immediately.
3. **Revocation**: the station can push `revoke {lease}` over the tunnel; the Pi drops the peer connection and relayed streams within 1 s. If the tunnel is down, tokens still expire in 10 minutes.
4. **Local kill switch**: a local admin command (or an optional GPIO button) disables all remote control until re-enabled on-site.
5. **Admin vs user**: admin operations (update, reboot, change config, read logs) need a separate admin token type that user and agent tokens can never mint.
6. **Fail closed**: if the Pi cannot verify a token, it refuses. It never falls back to an unauthenticated mode while enrolled.

## Guardrails protecting the Pi

Whoever holds the lease controls the phone; the phone is connected to the Pi by USB (HID, audio and an Ethernet gadget that routes the phone's traffic). So treat both the remote user and the phone itself as untrusted, and give neither a way into the Pi.

| Threat | Guardrail |
| --- | --- |
| Remote user reaches a shell or the filesystem | No shell, file or exec endpoints on the remote API. The Pi enforces an explicit allowlist of Roadie endpoints per token type: user and agent tokens reach HID input, video, audio, screenshots, MJPEG, BiDi and read-only status; gadget and relay resets, crop and capture settings, and every other endpoint need an admin token. |
| Malformed or abusive input | Strict schema validation on every HID/REST/BiDi message; size limits; per-lease rate limits on the Pi. |
| Phone attacks the Pi over the Ethernet gadget | The `usb0` interface is in its own firewall zone (nftables): DHCP and DNS from the Pi allowed, everything else to the Pi dropped. Forwarding to the Internet only via NAT; no access to the Pi's LAN or other Pis. |
| Phone used for abuse through your network | Options, off by default: egress rate cap per phone (for example 20 Mbps), block SMTP (port 25), log egress volume per lease. Optional DNS filtering later. |
| Phone sends hostile USB traffic | The gadget exposes only HID (output to phone), UAC2 audio and NCM/ECM. No mass-storage or serial functions on remote leases. |
| Roadie crashes or hangs | systemd with `Restart=always` and the hardware watchdog; a health endpoint the station polls; auto-reboot after 3 failed health checks. |
| SD card corruption or tampering | Read-only root filesystem (overlayfs); config and keys on a small separate partition; logs in RAM with periodic upload. |
| Bad update bricks the fleet | Signed updates only (minisign or cosign); A/B Roadie binaries with automatic rollback if the new one fails health checks for 2 minutes; staged rollout, 1 Pi first. That comes later; until then, update each Pi by hand from a tested release, one at a time. |
| Lateral movement between Pis | Each Pi has its own rig key and only an outbound tunnel; Pis never talk to each other. |
| Station compromise | Pi checks token scope and device id itself; admin tokens are separate and rarely used; the local kill switch works without the station. |
| Roadie process runs with too much power | Runs as a dedicated user with only the needed device nodes (`/dev/video*`, `/dev/hidg*`, audio) and `CAP_NET_ADMIN` dropped; systemd sandboxing (`ProtectSystem=strict`, `NoNewPrivileges`). Gadget and firewall setup run in a separate root oneshot at boot. |

The owner's management access goes over a separate path (for example Tailscale SSH), never over the tunnel.

## Security

WebRTC media is encrypted end to end between browser and Pi (DTLS-SRTP), so even a TURN-relayed session shows the station only ciphertext. Tunnel-relayed traffic (REST, BiDi, screenshots, MJPEG) is TLS-protected in transit and readable by the station, which is your own server. The Pi-side guardrails are in the previous section.

### Station

- Password login with an argon2id hash, rate-limited, with a lockout after repeated failures; session cookies `HttpOnly`, `Secure`, `SameSite=Lax`; CSRF tokens on state-changing requests; a strict Content Security Policy.
- Tailnet mode is the recommended default; public mode only with TLS.
- Tokens travel only in headers or the first WebSocket message, never in URLs; browser WebSockets check `Origin`; logs never contain passwords, tokens or TURN credentials.
- **TURN is locked down**: per-lease short-lived credentials only, and no relaying to private, loopback or link-local addresses, plus any provider-internal ranges set in config (for example Fly.io's `fdaa::/16`). An open TURN server is a free proxy into internal networks.
- `govulncheck` runs in CI.

### Pi

- SSH with keys only and password login disabled; Tailscale SSH is a good fit.
- The rig key is a file on the SD card with no secure hardware behind it, so keep Pis physically secure; removing a Pi on the station revokes it.
- Enrollment pins the station's signing key; re-enroll to rotate it. Enrollment codes carry the station key's fingerprint, so a spoofed station on the LAN is refused.
- Local access needs --auth=token or --auth=tailscale unless Roadie listens only on localhost (from R1 on). On shared Wi-Fi, reach it over the tailnet, where traffic is encrypted, or restrict port 8080 with a firewall rule.

## Station pages and API

`station` serves a handful of pages and a small API. Customers of a hosted service see that service's own pages instead, built on the same packages and the same viewer.

| Page | What the owner does |
| --- | --- |
| `/login` | Sign in with the password |
| `/` | See enrolled devices: name, online status, current controller, Open button |
| `/devices/{id}/view` | The shared viewer: WebRTC video, audio, touch and keyboard; quality picker; latency readout; MJPEG fallback; Take control / Release; a copyable agent snippet |
| `/settings` | Change the password; create and revoke API tokens; create enrollment codes; see network mode and TURN status |

The viewer page comes from the station; the browser then connects to the Pi over WebRTC. Roadie's own `/view` on the Pi stays for LAN use behind the local admin token, and both serve the same viewer module.

**Look and feel.** Extremely minimal, in the spirit of exe.dev and Roadie's existing pages: server-rendered HTML, system fonts, mostly text, monochrome plus one accent colour, no frameworks, no build step. The viewer is the device's screen plus a thin toolbar.

| Method and path | Purpose |
| --- | --- |
| `GET /v1/devices` | List enrolled devices |
| `POST /v1/devices/{id}/lease` | Take control; returns a lease token, TURN credentials and the signaling URL |
| `DELETE /v1/devices/{id}/lease` | Release control |
| `GET /v1/devices/{id}/screenshot` | One JPEG frame |
| `GET /v1/devices/{id}/mjpeg` | MJPEG stream, capped frame rate |
| `POST /v1/devices/{id}/hid` | Roadie's HID commands, relayed |
| `wss://…/v1/devices/{id}/bidi` | WebDriver BiDi, relayed |
| `wss://…/v1/devices/{id}/signal` | WebRTC signaling for the viewer and headless clients |
| `wss://…/v1/devices/{id}/ws/hid`, `/ws/audio`, `/ws/mic` | Relayed fallback when WebRTC cannot connect |
| `wss://…/v1/tunnel` | Pi tunnel endpoint (rig keys only) |

API calls take `Authorization: Bearer <token>`; WebSockets take the token in the first message.

## Development and testing

One Claude Code session on the dev Pi, with its phone attached, builds and tests both `roadie` and `station`, so hardware changes are verified on the real device in seconds. Both repos are cloned side by side in `~/Projects` (`roadie/` and `station/`). Start Claude Code in `~/Projects`, so one session reads, edits, builds and tests both, with an uncommitted `go.work` there tying the modules together. Commits stay per repo, Station first.

| Station | Where | Used for |
| --- | --- | --- |
| Local | `station` on the dev Pi itself, at localhost | Fast loop for tunnel, auth, signaling and relay code |
| Tailnet | `station --tailnet` on the dev Pi or another tailnet machine | Tailnet mode with `tsnet`, the recommended setup |
| Public test | `station` in public mode on a small public server (any VPS, or a Fly app such as `vibium-station-test`), TURN on | The real Internet path: TLS, TURN, NAT |

The agent may deploy to the public test station with a token scoped to that one server or app, and to nothing else.

### Testing the remote path from the dev Pi

The dev Pi sits on the same LAN as its phone, so a naive test would connect peer-to-peer on the LAN and prove nothing about the Internet. Three tools close that gap:

1. **Relay-only mode**: a test client sets WebRTC's ICE transport policy to `relay`, forcing media through TURN. If video, audio and taps work that way, the Internet path works.
2. **`cmd/station-probe`**: a headless Go client (Pion) that signs in with a station API token, leases a device, connects over WebRTC (relay-only by default), checks frame rate, bitrate and round-trip latency, sends a tap, confirms the screenshot changed, and releases the lease. It exits non-zero on any failure, so it doubles as a smoke test for any station, including a hosted one.
3. **A human check from outside**: at R4, open the public test station's viewer from a phone on cellular data.

Hosted services run the same probe against their own stations.

## Milestones and acceptance criteria

Roadie's milestones are R0–R6. Each milestone runs in its own Claude Code session that starts from this spec and `docs/progress.md`, which the agent updates at every checkpoint (a local file, kept out of git). If time is short, R4 is the milestone everything else waits on.

### First release

- [ ] **R0 Encoder check** (a few hours; the capture format is already chosen): confirm which input formats `/dev/video11` accepts natively; compare the 4:2:2 to 4:2:0 conversion in Go with the ISP (`/dev/video12`); measure CPU, latency and dropped frames for YUYV at 1080p30 with the phone crop applied. **Gate:** numbers recorded in `docs/progress.md` and a conversion path chosen.
- [ ] **R1 WebRTC video on the LAN**: `/view?transport=webrtc` shows H.264 video from the Pi 4 hardware encoder; glass-to-glass latency under 150 ms (filming a stopwatch on the phone next to the browser); Pi CPU under 40% at 30 fps; JPEG encoding runs only while an MJPEG consumer needs it; local auth via `--auth` (token or tailscale; unchecked access only on localhost unless `--auth=none` is given); optional HTTPS directly on :443 with `--tls-cert` and `--tls-key`. **Gate:** latency and CPU targets met.
- [ ] **R2 Input and audio on the LAN**: taps, swipes and typing over the `hid` data channel; phone audio in the browser and browser mic to the phone, both as WebRTC audio tracks; MJPEG `/stream`, the existing WebSockets and REST/BiDi still pass their tests. **Gate:** input and two-way audio work in Chrome, Safari and Firefox.
- [ ] **R3 Vibium Station and single-player `station`**: the station packages with their five interfaces and the rig/device model, in their own repo; secp256k1 keys, Nostr-format lease grants and the relay-ready event store; the rig policy file, with Roadie using Station's rig library; `station` with password login, API tokens, fingerprint-checked enrollment codes with mDNS discovery on the LAN, SQLite and tailnet mode via `tsnet`; tunnel, signaling, leases and token signing; the shared viewer served by the station. The Pi refuses forged, expired, wrong-device and older-sequence tokens (tests for each). **Gate:** token tests pass and the dev phone is fully usable through the station over the tailnet.
- [ ] **R4 Public mode and TURN**: `station` in public mode with TLS on a public test server; embedded TURN, refusing expired credentials and private destinations (tested); `station-probe` passes relay-only; MJPEG fallback works; your cellular check passes. **Gate:** probe and cellular check pass.
- [ ] **R5 Agent relay**: REST, BiDi, screenshots, MJPEG and the fallback WebSockets relayed through the station; named tokens with read-only scope; the Pi's per-token endpoint allowlist and rate limits; `govulncheck` in CI. **Gate:** a Vibium script drives the dev phone through the station end to end.
- [ ] **R6 Pi hardening and packaging**: usb0 firewall with egress cap and SMTP block as options; sandboxed non-root service that restarts within 5 s; SSH keys only; the dev Pi's setup captured as a script or image; `station` release artifacts (binaries, Docker image, systemd unit, sample `fly.toml`) and a self-hosting guide, including running Roadie and `station` on a tailnet with HTTPS names and Tailscale identity. **Gate:** a port scan from the phone finds nothing on the Pi or LAN, and a fresh install following the guide works, and a Mac or Linux PC host using the board HID backend passes a LAN smoke test (video, taps, typing).
- [ ] **R7 Recordings**: a Blossom store in `station` behind a `BlobStore` interface with a disk implementation: `GET` and `HEAD` by SHA-256 (BUD-01), upload with a signed auth event (BUD-02), list and delete; downloads only through the station's own authentication (the owner's session, or a `t=get` event the station signs for a token on the hold or for a rig), the store requiring that event on `GET` for every file that is not public, with a per-file public switch and no capability links, since secrets never travel in URLs; for every blob a file-metadata event (kind 1063, NIP-94) signed by the station that names the hash, the hold, the device and the uploader in its tags, kept in the events table and left in place when retention removes the blob; retention by age and by size per station and a cap per file, with the data directory's volume sized for it; the devices page lists each hold's recordings with a download, and an "open in the Record Player" link for the public ones. Roadie records a hold in Vibium's recording format (a screenshot after each input and the input log, no video yet) when the rig policy allows it, and uploads the zip at hold end over the tunnel, signed with its rig key. A Vibium run's `record.zip` is uploaded with an auth event the station signs on the account's behalf: `POST /v1/records/upload-auth` takes the token, the hold, the hash and the size and returns a kind 24242 event signed by the account key (the station key in single-player, behind a `Signer` interface), good for that one hash and five minutes; the Vibium engine does this at recording stop when `vibium:recording.stop` is asked to upload, which reaches the CLI, the SDKs and the MCP tool at once (a Vibium change, like `type`), and a `curl` pair is documented for everything else; the metadata event names the token and the hold. Agents never hold keys. Gate: a hold driven from the viewer and one driven by Vibium through the Fly.io test station each leave a recording that opens in the Record Player; a download without the hold's authority is refused; retention removes an aged blob and its event remains.

### Later

- [ ] Signed Pi updates with A/B rollback.
- [ ] Passkeys for `station` login.
- [ ] Opus audio behind a build tag, if G.722 proves too thin.
- [ ] Adaptive bitrate tuned on real home connections; quality presets in the API.
- [ ] A grid page in the station for several devices at once, like Roadie's existing `/grid`.
- [ ] Nostr bunkers (NIP-46) for the keys that sign rarely and matter most: operator keys and device keys. The station's identity key in a bunker signs a short-lived rotating key for day-to-day lease grants. Rig keys stay on the rig, because they sign on every reconnect and are cheap to revoke.
- [ ] The embedded private Nostr relay: optional in `station`, after R7, which brings the Blossom store (see Files: Blossom).
- [ ] Station-managed certificates: `station` obtains and renews a wildcard certificate through an ACME DNS challenge (for example with the certmagic or lego libraries) and pushes it to enrolled rigs over the tunnel, so rigs never hold DNS credentials and no copy scripts are needed; `station tls status` and `station tls rotate` from the command line.

## Open questions and risks

The capture format is settled (see Capture format). Remaining questions:

- [ ] **Audio codec**: is G.722 (speech-grade, 16 kHz) good enough for the phone's speaker, or is Opus needed sooner? Confirm G.722 works in Safari's WebRTC.
- [ ] **Names**: `station` and `/v1/devices`, or something else?
- [ ] **tsnet dependency**: tailnet mode pulls Tailscale's library into `station`. Acceptable in the default build, or behind a build tag?

Risk to the timeline: getting V4L2 M2M encode working directly from Go can take longer than expected. The `ffmpeg h264_v4l2m2m` subprocess is the fallback that keeps the first release on schedule.

## Prompt for the coding agent

Export this doc as Markdown to `station/docs/remote-spec.md` (link it from Roadie's docs), then start Claude Code in `~/Projects` on the dev Pi and give it the prompt below. It covers R0–R6 and stops at each milestone for review.

```markdown
You are working in two open-source Go repositories cloned side by side on this dev Pi: Vibium Station (VibiumDev/station, at ~/Projects/station) and Roadie (VibiumDev/roadie, at ~/Projects/roadie). This session was started in ~/Projects, which also holds an uncommitted go.work that ties the two modules together. Roadie is a USB KVM for phones and computers: it captures a target's HDMI output from a USB 3 capture dongle (MJPEG or YUYV, via pion/mediadevices) and sends keyboard, mouse and multi-touch input to the target as a USB gadget from a Raspberry Pi 4. It already serves an MJPEG stream and a /view page on the LAN, takes input over a WebSocket, a REST API and a WebDriver BiDi endpoint, and handles USB audio both ways. All of that works and must keep working.

You are running on the dev Pi, which has a phone attached. Test Roadie changes directly on it.

Read station/docs/remote-spec.md in full before writing code. It is the source of truth. Your job is milestones R0 to R6: make Roadie work well and securely across the Internet with WebRTC, plus Vibium Station: Go packages and a single-player station binary that anyone can self-host.

## Open source boundaries

- Everything here must make sense for one person running their own Pis. No vendor accounts, email services, billing, multi-tenant users or provider-only code in this repo.
- Hosted services may embed the station packages. Keep their interfaces small and stable: Authenticator, Authorizer, DeviceStore, LeaseStore, AuditSink. Handlers must mount under any path prefix.
- Station is device-neutral: rigs (enrolled machines) offer devices with a kind (phone now, browser later) and capabilities (video, audio, hid, bidi, mjpeg). Station's code never says phone; phone-specific code lives in Roadie.
- Station owns the rig library (tunnel client, enrollment, token checks, endpoint allowlist); Roadie imports it. Commit to Station first, then bump Roadie. Never commit go.work.
- Not Pi-only: "Pi" means the Roadie host. A Raspberry Pi 4 is the reference, but a Mac mini and a Linux PC must keep working. Feature-detect the HID backend (USB gadget or the QT Py boards), audio paths, Ethernet gadget and encoder, and announce them as capabilities; never assume /dev/video11 or USB gadget mode exist. CI builds linux/arm64, linux/amd64 and darwin/arm64.
- Nostr-ready, as the spec describes: all keys secp256k1 with BIP-340 Schnorr, shown as npub/nsec (NIP-19); signed events in the NIP-01 event format with Station's own documented kinds; NIP-44 for private content that crosses a third party. Use fiatjaf.com/nostr or btcec, pure Go only. The transport stays the tunnel; never publish private events; do not use NIP-90. Do not build the Blossom file store or an embedded Nostr relay yet; both come later. But be relay-ready: store every signed or accepted event verbatim in an append-only events table behind an EventStore interface (save, query by NIP-01 filter, delete) modelled on khatru's event store, with a SQLite implementation; keep live lease state in ordinary tables; use NIP-01 kind ranges (regular for lease grants, revocations and enrollment requests, replaceable for rig announcements, addressable with a d tag per device, kind 0 for the station profile); list Station's kinds in docs/kinds.md; and name every event's parties in p tags.
- Rig policy: a local file on the rig lists accepted stations (by npub, more than one allowed) and, per station, allowed scopes, whether admin grants are accepted, and maximum session length. One active lease across all stations.

## What to build

1. In roadie (the Pi):
   - WebRTC with github.com/pion/webrtc/v4: one H.264 video track (Constrained Baseline, level 4.0 set explicitly, SPS/PPS on every IDR, 2 s GOP, forced IDR on PLI/FIR, CBR driven by congestion feedback).
   - An Encoder interface (Encode, SetBitrate, ForceKeyframe) with three implementations behind a flag: direct V4L2 M2M on /dev/video11 (preferred), an ffmpeg h264_v4l2m2m subprocess (fallback), and an ffmpeg subprocess for other hosts that picks h264_videotoolbox (macOS), h264_vaapi, h264_nvenc or h264_qsv (Linux PCs), or libx264 as the fallback (no cgo).
   - Capture: YUYV for WebRTC (measured on the reference UGREEN dongle: steady 1080p30 over USB 3, about 0.4 core against about 1 core for MJPEG); fall back to MJPEG only when the dongle lacks full-rate YUYV. R0 picks the 4:2:2 to 4:2:0 conversion path (Go or ISP). Take the raw frame before today's software JPEG encode: JPEG-encode only while an MJPEG consumer needs it, H.264-encode only while a WebRTC viewer is connected. Crop before encoding; convert 4:2:2 to 4:2:0 in Go or with the ISP (/dev/video12).
   - Data channels "hid" (exactly the JSON /api/hid/ws accepts today, 0-32767 coordinates, one handler for both transports) and "control". Keep /api/hid/ws, /audio and /api/hid/audio unchanged on the LAN; they are also the relayed fallback.
   - Two-way audio as WebRTC tracks: phone audio to the browser, browser mic into the gadget mic. G.722 in pure Go, PCMU fallback; offer only those in the SDP. No cgo for Opus.
   - Tunnel client to wss://<station>/v1/tunnel, authenticated with a secp256k1 rig key (BIP-340 Schnorr, shown as an npub); streams signal, rest, bidi, mjpeg, hid, audio, mic, control; reconnect with backoff and jitter. `roadie enroll <station> --code <code>` follows the enrollment flow in the spec: the code carries the station key's fingerprint, the Pi refuses a station whose key does not match, and with no URL the Pi finds the station on the LAN over mDNS (_vibium-station._tcp).
   - Lease-grant checks: Nostr-format events (NIP-01) signed with BIP-340 Schnorr by a station in the rig policy; tags sub/rig/dev/lease/seq/scope, NIP-40 expiration, event id as the replay nonce; every grant also checked against the rig policy; refuse older seq; scopes; revocation within 1 s; fail closed. Separate admin token type. User and agent tokens reach only an allowlist of endpoints (HID input, video, audio, screenshots, MJPEG, BiDi, read-only status); resets, crop and capture settings are admin-only. Per-lease rate limits.
   - /view gains WebRTC with automatic MJPEG fallback, using the shared viewer module.
   - Local auth from R1, chosen with --auth. Unset: no checks, and Roadie listens on localhost only. --auth=token: a local admin token (prefix roadie_local_, 32 random bytes) in a file only Roadie's user can read; `roadie token` prints it and `roadie token --rotate` replaces it; /view sends you to a minimal server-rendered /login page with one field, which sets an HttpOnly cookie for 30 days (/logout clears it); agents send Authorization: Bearer. --auth=tailscale: ask the local tailscaled who each connection belongs to (WhoIs, via tailscale.com/client/local) and allow the logins and tags in the config. --auth=none: no checks on any --listen address, with a warning on every start. Station lease grants are required for remote sessions regardless. HTTPS: --tls-cert and --tls-key make Roadie serve TLS itself (for example a wildcard certificate on --listen :443), and --http-redirect :80 sends plain HTTP to HTTPS; Roadie reloads the certificate files when they change, so renewal needs no restart, and `roadie tls status` shows the certificate's names and expiry; the systemd unit grants AmbientCapabilities=CAP_NET_BIND_SERVICE so Roadie can use ports below 1024 without root. The browser mic needs HTTPS (or localhost). Keep it simple: no pop-ups, no Basic Auth, no QR codes.
2. Vibium Station:
   - Packages tunnel, signal, relay, lease, token, turn and viewer, as described in the spec. Leases: per-device seq, takeover by input scope, one view-only observer, release after 5 idle minutes or 8 hours. TURN: embedded pion/turn, per-lease credentials, refuses private, loopback, link-local and configured extra ranges; listen address, advertised IP and MTU configurable.
   - cmd/station: one owner with a password (argon2id in the config file, `station passwd`), rate-limited login with lockout, HttpOnly Secure SameSite=Lax cookies, CSRF, strict CSP; named API tokens, hashed, optional read-only; one-time enrollment codes carrying the station key fingerprint, and mDNS advertisement on the LAN; SQLite next to the config; tailnet mode with tailscale.com/tsnet (`--tailnet`) and public mode with Let's Encrypt or behind a proxy; pages /login, /, /devices/{id}/view, /settings; API under /v1/devices and /v1/tunnel as listed in the spec. Tokens only in headers or the first WebSocket message, never URLs; Origin checks; never log secrets.
   - Style: extremely minimal like exe.dev and Roadie's pages: server-rendered HTML, system fonts, monochrome plus one accent colour, no frameworks, no build step.
   - Release artifacts by R6: static binaries for linux/amd64 and arm64, a Docker image, a systemd unit, a sample fly.toml, and docs/self-hosting.md.
3. cmd/station-probe (in the Station repo): a headless Pion client that signs in with a station API token, leases a device, connects relay-only, checks frames, latency and a tap, and exits non-zero on failure.
4. Pi hardening scripts under deploy/pi/: systemd units (non-root, ProtectSystem=strict, NoNewPrivileges, only needed device nodes, Restart=always, watchdog); a root oneshot for the USB gadget and nftables (usb0 gets DHCP and DNS from the Pi and NAT to the Internet only; egress cap and SMTP block as options, off by default). Notes for a read-only root filesystem; do not enable it automatically.

## Environments

- Develop against a local station on this Pi, then tailnet mode, then a public test station. You may deploy only to the public test station you are given a scoped token for.
- From R4 on, run station-probe against the public test station and include its output in your report.

## How to work

- Work R0 to R6 in order; each has a gate. Plan the steps inside a milestone yourself. At every checkpoint update station/docs/progress.md (done, decisions, numbers, known issues; a local file, kept out of git) so a fresh session can continue. At the end of each milestone, stop, report what changed, how you tested it, measured numbers and any deviation from the spec, and wait for approval.
- Keep existing behaviour and tests passing. Add tests for token checks (valid, expired, wrong device, wrong signer, older seq, missing scope, endpoint allowlist, replayed event id, grant outside the rig policy, station not in the policy), enrollment (wrong fingerprint, reused or expired code), the tunnel protocol, TURN lockdown, HID validation, and EventStore filter queries. govulncheck in CI.
- Prefer the standard library and small dependencies; no cgo; never GStreamer.
- Never add a remote endpoint that runs commands, reads or writes files, or changes Pi configuration for non-admin tokens.
- When changing the Pi's firewall, SSH or systemd in R6, apply changes with an automatic rollback timer so a mistake cannot cut off your own session.
- Secrets come from env vars or files outside the repo. Never commit keys.
- If something in the spec is wrong or impossible on this hardware, stop, explain, and propose the smallest alternative.
- Update README.md, API.md and docs/ for every user-visible change.

Roadie moves fast, and the spec was last checked against Roadie at commit 9168157 (Oct 26, 2026). Before planning, compare the spec with the current code and adapt the spec's names to what exists rather than adding parallel ones. Known differences: Roadie takes --port (auto from 8080), not --listen, so add a bind address alongside it; Roadie already advertises _roadie._tcp over mDNS with hashicorp/mdns; the endpoints are /snapshot, /raw-stream, /raw-snapshot, /api/settings, /api/crop, /api/rotate, /api/capture/reset, /api/usb/reset and /api/relay/reset (settings, crop, rotate and resets are admin-only remotely); the viewer already has a configurable toolbar, Home, Back, Recents, Lock, volume and Screenshot buttons, iPhone rotation and /grid, and the shared viewer must keep all of them; --hid-backend defaults to rp2040, with linux-gadget opt-in; setup/roadie.service exists for R6 to build on; docs/ follows tutorials, how-to, reference, explanation and plans. Start by reading both repos and the spec, then reply with a short plan for R0, any other mismatches you found, and your questions.
```

