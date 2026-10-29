# Station API

All calls need a signed-in session (the pages), a tailnet identity in
tailnet mode, or `Authorization: Bearer <token>` with a token from the
Settings page. Browser sessions send the CSRF token the page carries in
`X-CSRF-Token` on anything but GET. Errors are `{"error": "..."}`.

A device id is `<rig id>/<name>`, such as `rig_3f9a1c02/phone`, and
appears in paths as two segments.

| Method and path | Purpose |
|---|---|
| `GET /v1/devices` | Every rig's devices: id, kind, capabilities, shortcuts, the pointer mode the rig suggests (`input`: `touch` or `mouse`), rig, online, and the lease held on it |
| `POST /v1/devices/{rig}/{name}/lease` | Take the device, or a seat watching whoever has it. Body `{"scopes": ["view", "input"]}`; `view` is always included. Returns the signed grant, the lease and whether the caller is an observer |
| `DELETE /v1/devices/{rig}/{name}/lease` | Release the caller's lease. An observer cannot end the holder's |
| `wss /v1/devices/{rig}/{name}/signal` | WebRTC signaling, relayed to the rig. The first message is `{"type":"hello","grant":{...}}`; the rig checks the grant and answers with its offer |
| `GET /v1/devices/{rig}/{name}/stream` | The device's MJPEG stream, relayed from the rig at the rig's relay cap (a few frames a second); `?fps=` asks for less. The station signs a view grant for the caller: their own lease, or a seat watching the holder's |
| `GET /v1/devices/{rig}/{name}/screenshot` | One JPEG from the rig (`?quality=30..95`), the same way |
| `wss /v1/devices/{rig}/{name}/hid` | Input as the rig's HID WebSocket takes it, relayed with an input grant; the caller must hold the device's lease already (409 otherwise) |
| `wss /v1/devices/{rig}/{name}/audio` | The device's sound for the fallback, relayed with a view grant: a JSON parameters message, then binary chunks; through a station a Roadie sends 16 kHz mono 16-bit |
| `wss /v1/devices/{rig}/{name}/mic` | The caller's microphone into the device as 16 kHz mono 16-bit chunks, relayed with an input grant (the lease must be held) |

Browser WebSockets must come from the station's own origin; a request
with an `Origin` header naming another site is refused.
| `GET /v1/station` | The station's name, key, tunnel URL and signed profile; what a rig checks at enrollment |
| `POST /v1/enroll` | A rig's enrollment request: `{"event": <kind 7702>}` with the code, name and devices; see [docs/kinds.md](docs/kinds.md) |
| `wss /v1/tunnel` | The rig tunnel: a challenge, the rig's signed hello, then yamux |

## Leases

One lease holds a device at a time, numbered per device. A request with
`input`, `bidi` or `admin` from someone else takes over with a new lease,
and the rig ends the old one's sessions. A view-only request while someone
holds the device gets a grant on their lease as an observer. The holder's
own request again refreshes the lease and returns a fresh grant; grants
last ten minutes, so a viewer refreshes every three and sends the new
grant to the rig on the control channel (`{"type":"grant","grant":{...}}`).
A lease nobody refreshes or uses for five minutes ends, and every lease
ends after eight hours; the rig is told with a signed revocation.

```json
{
  "grant": {"kind": 7700, "pubkey": "...", "tags": [["p", "<rig key>"], ["rig", "rig_3f9a1c02"], ["dev", "rig_3f9a1c02/phone"], ["sub", "owner"], ["lease", "lse_9f3c0a11"], ["seq", "42"], ["scope", "view"], ["scope", "input"], ["turn", "1793208000:lse_9f3c0a11", "<credential>", "stun:203.0.113.9:3478", "turn:203.0.113.9:3478?transport=udp"], ["expiration", "1793180000"], ["nonce", "..."]], "sig": "..."},
  "ice": [{"urls": ["stun:203.0.113.9:3478", "turn:203.0.113.9:3478?transport=udp"], "username": "1793208000:lse_9f3c0a11", "credential": "<credential>"}],
  "observer": false,
  "lease": {"id": "lse_9f3c0a11", "seq": 42, "subject": "owner", "scopes": ["view", "input"], "since": "...", "expires": "..."},
  "signal": "/v1/devices/rig_3f9a1c02/phone/signal",
  "online": true
}
```

Scopes: `view` (video, speaker audio, screenshots, MJPEG, status), `input`
(HID and the microphone), `bidi` (WebDriver BiDi), `admin` (the device's
settings and resets, only where the rig's policy allows it). A read-only
token gets `view` only.

`ice` lists the station's STUN and TURN servers, shaped like the browser's
`RTCIceServer`, with the lease's own TURN credential, good until the
lease's maximum time; it is empty when the station runs no TURN server.
The same servers travel to the rig in the grant's `stun` and `turn` tags,
so both peers can be reached across NATs. A client that wants to prove
the relay path sets its ICE transport policy to `relay`.

## station-probe

`station-probe` is the headless check the spec asks for, built from
`cmd/station-probe`:

```bash
station-probe -station https://station.example -token stk_... [-device rig_x/phone] [-t 10s] [-ice relay|all] [-tap 0.5,0.5] [-insecure]
```

It lists the devices, leases one with `view` and `input`, connects over
WebRTC through the station's signaling with the media forced through TURN
(`-ice relay`, the default), prints the frame rate, bitrate, keyframes,
the candidate pair and its round trip, taps where `-tap` says (fractions
of the picture) and compares screenshots before and after, presses the
device's Home shortcut when it announces one, and releases the lease. It
exits non-zero when anything fails, including a relay-only run whose
media did not go through TURN. `-insecure` accepts a LAN station's
self-signed certificate; on a LAN, relay-only fails by design, since TURN
refuses private peers.

## Pages

| Page | What the owner does |
|---|---|
| `/login` | Sign in with the password; in tailnet mode an allowed identity needs none |
| `/` | The devices: rig, kind, online, who controls it, Open |
| `/devices/{rig}/{name}/view` | The viewer: WebRTC picture, sound, touch and keyboard, the mic, the device's own buttons, stats, Take control and Release |
| `/settings` | Enrollment codes, rigs, API tokens, the password, the network |

The viewer's files are at `/viewer/webrtc.js`, `/viewer/input.js`,
`/viewer/viewer.js` and `/viewer/viewer.css`; a Roadie serves the same
`webrtc.js` from this module. The viewer takes `?ice=relay` (media
through TURN only) and `?transport=mjpeg` (the MJPEG fallback through the
station from the start, with input over the relayed HID WebSocket); the
fallback also kicks in on its own when WebRTC cannot connect.
