# Station API

All calls need a signed-in session (the pages), a tailnet identity in
tailnet mode, or `Authorization: Bearer <token>` with a token from the
Settings page. Browser sessions send the CSRF token the page carries in
`X-CSRF-Token` on anything but GET. Errors are `{"error": "..."}`.

A device id is `<rig id>/<name>`, such as `rig_3f9a1c02/phone`, and
appears in paths as two segments.

| Method and path | Purpose |
|---|---|
| `GET /v1/devices` | Every rig's devices: id, kind, capabilities, shortcuts, the pointer mode the rig suggests (`input`: `touch` or `mouse`), rig, online, and the hold held on it |
| `POST /v1/devices/{rig}/{name}/hold` | Take the device, or a seat watching whoever has it. Body `{"scopes": ["view", "input"]}`; `view` is always included. Returns the signed grant, the hold, whether the caller is watching, and the signaling and BiDi URLs. A person recalls a device someone else holds; an agent (a token) gets a place in line instead, `202 {"queued": true, "position": 2, "holder": "token:ci"}`, and `?wait=30` waits up to 30 s there for the hold |
| `DELETE /v1/devices/{rig}/{name}/hold` | Release the caller's hold, or leave the line. An observer cannot end the holder's |
| `POST /v1/devices/{rig}/{name}/reservation` | A place in line for a set time: `{"at": "2026-10-30T15:00:00Z", "scopes": [...]}`, `at` left out for now. The place sorts ahead of the line once its time comes, and must be taken within five minutes of it |
| `DELETE /v1/devices/{rig}/{name}/reservation` | Give the place up |
| `wss /v1/devices/{rig}/{name}/bidi` | WebDriver BiDi, byte for byte: the socket is relayed to the rig's own `/session`, authenticated at the upgrade (the bearer token, or the page's cookie from the station's own origin) and authorized for the device, the `bidi` scope and a hold, which is taken on the spot when the device is free. Nothing is inserted or rewritten; the first message is the client's `session.new` |
| `/v1/devices/{rig}/{name}/api/{path}` | The rig's REST under `/api`, relayed with a grant carrying the caller's hold scopes (a free device is taken with `input` when the caller may control). The rig's allowlist decides which scope each endpoint needs: HID input, status with `view`, settings and resets with `admin` |
| `GET /v1/devices/{rig}/{name}/health` | The rig's health, with `view` |
| `wss /v1/devices/{rig}/{name}/signal` | WebRTC signaling, relayed to the rig. The first message is `{"type":"hello","grant":{...}}`; the rig checks the grant and answers with its offer |
| `GET /v1/devices/{rig}/{name}/stream` | The device's MJPEG stream, relayed from the rig at the rig's relay cap (a few frames a second); `?fps=` asks for less. The station signs a view grant for the caller: their own hold, or a seat watching the holder's |
| `GET /v1/devices/{rig}/{name}/screenshot` | One JPEG from the rig (`?quality=30..95`), the same way |
| `wss /v1/devices/{rig}/{name}/hid` | Input as the rig's HID WebSocket takes it, relayed with an input grant; the caller must hold the device's hold already (409 otherwise) |
| `wss /v1/devices/{rig}/{name}/audio` | The device's sound for the fallback, relayed with a view grant: a JSON parameters message, then binary chunks; through a station a Roadie sends 16 kHz mono 16-bit |
| `wss /v1/devices/{rig}/{name}/mic` | The caller's microphone into the device as 16 kHz mono 16-bit chunks, relayed with an input grant (the hold must be held) |

Browser WebSockets must come from the station's own origin; a request
with an `Origin` header naming another site is refused.
| `GET /v1/station` | The station's name, key, tunnel URL and signed profile; what a rig checks at enrollment |
| `POST /v1/enroll` | A rig's enrollment request: `{"event": <kind 7702>}` with the code, name and devices; see [docs/kinds.md](docs/kinds.md) |
| `wss /v1/tunnel` | The rig tunnel: a challenge, the rig's signed hello, then yamux |

## Holds

A device has one hold at a time, numbered per device. A request with
`input`, `bidi` or `admin` from someone else takes over with a new hold,
and the rig ends the old one's sessions. A view-only request while someone
holds the device gets a grant on their hold as an observer. The holder's
own request again refreshes the hold and returns a fresh grant; grants
last ten minutes, so a viewer refreshes every three and sends the new
grant to the rig on the control channel (`{"type":"grant","grant":{...}}`).
A hold nobody refreshes or uses for five minutes ends (a hold with a
relayed connection open counts as in use), and every hold ends after
eight hours; the rig is told with a signed revocation, and the station
and the rig both close every connection relayed on it.

People recall, agents queue: a request with `input`, `bidi` or `admin`
from a signed-in person takes the device from whoever has it, while the
same request from a token gets a place in line. When the holder releases
the device, or its hold ends, the first in line gets a hold of its own,
which it must use within a minute, by any request or connection on it,
or the device passes to the next. A place nobody asks about for five
minutes is dropped. The line lives in memory, so a station restart
empties it and a waiting agent asks again. A reservation is a place in
line for a set time, sorting ahead once its time comes; a device sitting
free at that time goes to it on the next tick.

One observer at a time: a second view-only subject asking for a held
device gets 409 until the first's grant lapses.

The rig limits every hold: 120 requests and messages a second with a
burst of 240, one MJPEG stream, one audio socket, one microphone socket,
one BiDi socket and two input sockets at once; past that it answers 429.

```json
{
  "grant": {"kind": 7700, "pubkey": "...", "tags": [["p", "<rig key>"], ["rig", "rig_3f9a1c02"], ["dev", "rig_3f9a1c02/phone"], ["sub", "owner"], ["hold", "hld_9f3c0a11"], ["seq", "42"], ["scope", "view"], ["scope", "input"], ["turn", "1793208000:hld_9f3c0a11", "<credential>", "stun:203.0.113.9:3478", "turn:203.0.113.9:3478?transport=udp"], ["expiration", "1793180000"], ["nonce", "..."]], "sig": "..."},
  "ice": [{"urls": ["stun:203.0.113.9:3478", "turn:203.0.113.9:3478?transport=udp"], "username": "1793208000:hld_9f3c0a11", "credential": "<credential>"}],
  "observer": false,
  "hold": {"id": "hld_9f3c0a11", "seq": 42, "subject": "owner", "scopes": ["view", "input"], "since": "...", "expires": "..."},
  "signal": "/v1/devices/rig_3f9a1c02/phone/signal",
  "bidi": "/v1/devices/rig_3f9a1c02/phone/bidi",
  "online": true,
  "waiting": 0
}
```

Scopes: `view` (video, speaker audio, screenshots, MJPEG, status), `input`
(HID and the microphone), `bidi` (WebDriver BiDi), `admin` (the device's
settings and resets, only where the rig's policy allows it). A read-only
token gets `view` only.

`ice` lists the station's STUN and TURN servers, shaped like the browser's
`RTCIceServer`, with the hold's own TURN credential, good until the
hold's maximum time; it is empty when the station runs no TURN server.
The same servers travel to the rig in the grant's `stun` and `turn` tags,
so both peers can be reached across NATs. A client that wants to prove
the relay path sets its ICE transport policy to `relay`.

## WebDriver BiDi for agents

An agent with a token needs nothing of the station's own: it opens the
BiDi URL the hold endpoint returned (or simply the device's `.../bidi`
path, which takes the hold when the device is free) with
`Authorization: Bearer` on the upgrade and speaks WebDriver BiDi to the
rig, `session.new` first. The relay carries the socket byte for byte;
the rig's BiDi is what answers, Roadie's for a phone. A hold that ends,
by release, idle time, a recall or a revocation, closes the socket with
a WebSocket close, never a BiDi message. Vibium points at it with
`VIBIUM_CONNECT_URL` and `VIBIUM_CONNECT_API_KEY`. A standard
`wss://<station>/session` entry point, where `session.new` picks the
device, is planned once device selection is settled; the device path
stays as the control plane's route. See
[docs/bidi-conventions.md](docs/bidi-conventions.md).

`station-probe -bidi` runs that sequence as a plain BiDi client and,
with `-bidi-direct ws://<rig>/session`, the same sequence straight at
the rig, comparing the shape of every reply.

## station-probe

`station-probe` is the headless check the spec asks for, built from
`cmd/station-probe`:

```bash
station-probe -station https://station.example -token stk_... [-device rig_x/phone] [-t 10s] [-ice relay|all] [-tap 0.5,0.5] [-insecure]
```

It lists the devices, takes one with `view` and `input`, connects over
WebRTC through the station's signaling with the media forced through TURN
(`-ice relay`, the default), prints the frame rate, bitrate, keyframes,
the candidate pair and its round trip, taps where `-tap` says (fractions
of the picture) and compares screenshots before and after, presses the
device's Home shortcut when it announces one, and releases the hold. It
exits non-zero when anything fails, including a relay-only run whose
media did not go through TURN. `-insecure` accepts a LAN station's
self-signed certificate; on a LAN, relay-only fails by design, since TURN
refuses private peers.

## Pages

| Page | What the owner does |
|---|---|
| `/login` | Sign in with the password; in tailnet mode an allowed identity needs none |
| `/` | The devices: rig, kind, online, who controls it, Open |
| `/devices/{rig}/{name}/view` | The viewer: WebRTC picture, sound, touch and keyboard, the mic, the device's own buttons, stats, Hold and Release |
| `/settings` | Enrollment codes, rigs, API tokens, the password, the network |

The viewer's files are at `/viewer/webrtc.js`, `/viewer/input.js`,
`/viewer/viewer.js` and `/viewer/viewer.css`; a Roadie serves the same
`webrtc.js` from this module. The viewer takes `?ice=relay` (media
through TURN only) and `?transport=mjpeg` (the MJPEG fallback through the
station from the start, with input over the relayed HID WebSocket); the
fallback also kicks in on its own when WebRTC cannot connect.
