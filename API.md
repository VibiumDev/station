# Station API

All calls need a signed-in session (the pages), a tailnet identity in
tailnet mode, or `Authorization: Bearer <token>` with a token from the
Settings page. Browser sessions send the CSRF token the page carries in
`X-CSRF-Token` on anything but GET. Errors are `{"error": "..."}`.

A device id is `<rig id>/<name>`, such as `rig_3f9a1c02/phone`, and
appears in paths as two segments.

| Method and path | Purpose |
|---|---|
| `GET /v1/devices` | Every rig's devices: id, kind, capabilities, shortcuts, the pointer mode the rig suggests (`input`: `touch` or `mouse`), `label` (the device's name for people, when the rig gives one) and `platform` (`ios`, `android`, `mac`, `windows`, `linux`), rig, online, and the hold held on it |
| `POST /v1/devices/{rig}/{name}/hold` | Take the device, or a seat watching whoever has it. Body `{"scopes": ["view", "input"]}`; `view` is always included. Returns the signed grant, the hold, whether the caller is watching, and the signaling and BiDi URLs. A person recalls a device someone else holds; an agent (a token) gets a place in line instead, `202 {"queued": true, "position": 2, "holder": "token:ci"}`, and `?wait=30` waits up to 30 s there for the hold. With `"watch": true` a held device is watched instead, whoever asks and whatever the scopes; a free one is taken when the scopes include control, and is `409 {"free": true}` when they do not, so a watcher never holds a device by itself |
| `DELETE /v1/devices/{rig}/{name}/hold` | Release the caller's hold, or leave the line. An observer cannot end the holder's |
| `POST /v1/devices/{rig}/{name}/reservation` | A place in line for a set time: `{"at": "2026-10-30T15:00:00Z", "scopes": [...]}`, `at` left out for now. The place sorts ahead of the line once its time comes, and must be taken within five minutes of it |
| `DELETE /v1/devices/{rig}/{name}/reservation` | Give the place up |
| `wss /session` | The standard WebDriver BiDi entry point: `session.new` picks the device by the `vibium:device` capability, else the first online one; then relayed byte for byte like the route below, and the hold is released when the socket closes |
| `wss /v1/devices/{rig}/{name}/bidi` | WebDriver BiDi, byte for byte: the socket is relayed to the rig's own `/session`, authenticated at the upgrade (the bearer token, or the page's cookie from the station's own origin) and authorized for the device, the `bidi` scope and a hold, which is taken on the spot when the device is free and given back when the socket closes. Nothing is inserted or rewritten; the first message is the client's `session.new` |
| `/v1/devices/{rig}/{name}/api/{path}` | The rig's REST under `/api`, relayed with a grant carrying the caller's hold scopes (a token's request takes a free device with `input` when the token may control; a person's needs a hold already). The rig's allowlist decides which scope each endpoint needs: HID input, status with `view`, settings and resets with `admin` |
| `GET /v1/devices/{rig}/{name}/health` | The rig's health, with `view` |
| `GET /v1/devices/{rig}/{name}/extras.js` | The device's viewer extras, relayed from the path it announced (`extras` on the device), with `view` |
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
the device, or its hold ends, the first in line gets a hold of its own.
Any request or connection on that hold counts as using it, and if it
goes unused for a minute the device passes to the next. A place nobody
asks about for five minutes is dropped. The line lives in memory, so a
station restart empties it and a waiting agent asks again. A reservation
is a place in line for a set time, sorting ahead once its time comes; a
device sitting free at that time goes to it on the next tick.

One observer at a time: a second view-only subject asking for a held
device gets 409 until the first's grant lapses.

The viewer page uses `"watch": true` for everything but its Hold button,
so one link serves the driver and the watcher: opening it on a free
device takes the device, opening it on a held device watches, and only
the button takes a held device (from an agent at once; a person's hold
is recalled the same way, since there is one owner). A page that
watches a device nobody holds holds nothing itself, which is what lets
an agent take the device while someone follows along; the page looks
again every five seconds and shows the next holder's picture.
`?watch=1` opens the page as a watcher that never asks for control.

A relayed request (`stream`, `screenshot`, `health`, `audio`, `hid`,
`mic`, `bidi`, `extras.js`, `api/...`) rides on a hold, which is the
caller's own or, for `view`, a seat watching the holder's. A token's
request on a free device takes one with the scope the endpoint needs, so
a single screenshot works without a hold call first. A person's request
on a free device does not: it is `409 {"free": true}`, because people
take devices through the hold endpoint on purpose, and a page left
watching must not take the device by polling. Every relayed answer
carries `X-Hold`, the id of the hold it rode on, and `X-Hold-Observer: 1`
when that is someone else's; the page uses them to notice the device
changing hands.

The rig limits every hold to 120 requests and messages a second with a
burst of 240, and to one MJPEG stream, one audio socket, one microphone
socket, one BiDi socket and two input sockets at once; past that it
answers 429.

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

The scopes are:

- `view`: video, speaker audio, screenshots, MJPEG, status
- `input`: HID and the microphone
- `bidi`: WebDriver BiDi
- `admin`: the device's settings and resets, only where the rig's policy
  allows it

A read-only token gets `view` only.

`ice` lists the station's STUN and TURN servers, shaped like the browser's
`RTCIceServer` and carrying the hold's own TURN credential, which is good
until the hold's maximum time; it is empty when the station runs no TURN
server. The same servers travel to the rig in the grant's `stun` and
`turn` tags, so both peers can be reached across NATs. A client that
wants to prove the relay path sets its ICE transport policy to `relay`.

## WebDriver BiDi for agents

An agent with a token needs nothing of the station's own: it opens the
BiDi URL the hold endpoint returned (or simply the device's `.../bidi`
path, which takes the hold when the device is free) with
`Authorization: Bearer` on the upgrade and speaks WebDriver BiDi to the
rig, `session.new` first. The relay carries the socket byte for byte;
the rig's BiDi is what answers, Roadie's for a phone. A hold that ends,
by release, idle time, a recall or a revocation, closes the socket with
a WebSocket close, never a BiDi message. The other way round, a socket
that took the device gives it back when it closes; a hold taken through
the hold endpoint first spans as many sockets as the agent opens, until
it releases the hold or leaves it idle for five minutes. Vibium points
at it with `VIBIUM_CONNECT_URL` and `VIBIUM_CONNECT_API_KEY`.

`wss://<station>/session` is the standard BiDi-only entry point: open it
with the token and send `session.new`. The station reads that one
message to pick the device, which is the one named by the
`vibium:device` capability
(`{"capabilities": {"alwaysMatch": {"vibium:device": "rig_3f9a1c02/phone"}}}`,
the id or just the name) or else the first online device. It then takes
the hold, opens the rig's own BiDi socket and forwards the message
untouched; from then on it moves frames both ways without reading them,
and releases the hold when the socket closes. A device that is offline,
unknown or held by someone else answers `session.new` with a BiDi error
(`session not created`) and closes. The device path above remains for
clients that were handed it. See
[docs/bidi-conventions.md](docs/bidi-conventions.md) and the tutorial,
[docs/tutorials/agent-through-the-station.md](docs/tutorials/agent-through-the-station.md).

`station-probe -bidi` runs that sequence as a plain BiDi client, and
with `-bidi-direct ws://<rig>/session` it runs the same sequence straight
at the rig as well, comparing the shape of every reply.

## The Record Store

Recordings, screenshots and logs live in the Record Store as
[Blossom](https://github.com/hzrd149/blossom) blobs: every file is named
by its SHA-256, uploaded with a signed authorization, and tied to the hold
it came from. The station signs that authorization on an agent's behalf,
so an agent keeps its API token and never holds a key while the store
still sees an ordinary Blossom upload, and a rig signs its own uploads
with its rig key. For every file the station signs a file event (kind
1063, see [docs/kinds.md](docs/kinds.md)) naming the hash, the hold, the
device and the uploader, and that event stays in the event store after
retention removes the bytes.

### Uploading

An agent with a `record.zip` from a run hashes it, asks for an
authorization, and sends the file with it:

```bash
SHA=$(sha256sum record.zip | cut -c1-64); SIZE=$(stat -c %s record.zip)
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"sha256\":\"$SHA\",\"size\":$SIZE,\"type\":\"application/zip\",\"name\":\"record.zip\"}" \
  https://station.example/v1/records/upload-auth
# {"authorization":"Nostr eyJ…","upload":"https://station.example/upload","expires":"…"}
curl -s -X PUT -H "Authorization: Nostr eyJ…" -H 'Content-Type: application/zip' \
  --data-binary @record.zip https://station.example/upload
```

`POST /v1/records/upload-auth` takes `sha256` and `size`, and optionally
`type`, `name` and `hold`; the token must be one that can control
devices. The hold is the one named, which must be the caller's own.
Without one the station takes the caller's current hold, or the newest
of the last day when none is current, and refuses when the caller holds
several devices at once; the owner may upload with no hold at all. The
answer carries a kind 24242 event signed by the station key, with
`t=upload`, `x=<sha256>`, the size, a five-minute expiration and the
station's own tags (`sub`, `hold`, `dev`, `rig`), ready to send as the
`Authorization` header. A file over `--records-max-file`, or one that
would take the store past `--records-max-total`, is refused here with
413 or 507.

`PUT /upload` is Blossom's upload (BUD-02): the body is the file,
`Content-Length` its size and `Content-Type` its media type. The bytes
are hashed as they arrive and refused with 400 when they do not match
`x`. The answer is the blob descriptor: `url`, `sha256`, `size`, `type`
and `uploaded`, plus `name`, `uploader`, `hold`, `device`, `public` and
`event`, the id of the file event, and for a recording `steps`, `frames`,
`duration_ms` and `files`, where its unpacked files are served from.
A recording is unpacked once, when it arrives (or at the station's next
start for one from before), and kept beside its zip under
`unpacked/<sha256>/`, so the cards, the thumbnails and the files above
come from the directory and the zip is never opened again; the station
reads the trace then for the steps, the frames and how long the run was.
The unpacked copy takes about as much room again as the zip, goes with
it, and never lands outside its own directory whatever the zip names. The same bytes uploaded again return
the first upload's descriptor. A rig uploads with an authorization it
signs itself, naming its device in a `dev` tag and the hold in a `hold`
tag, and the station checks that both are the rig's own.

### Fetching

`GET /<sha256>[.ext]`, and `HEAD`, is Blossom's fetch (BUD-01). A public
file needs nothing. Any other needs the owner's session, a token that
uploaded it or held the device at the time, or an `Authorization: Nostr`
event with `t=get` and `x=<sha256>` signed by the station or by the rig
that uploaded it. `POST /v1/records/{sha256}/download-auth` gives such
an event to a caller who may read the file, for handing to something
without a station credential of its own; it lasts five minutes and, as a
bearer, travels in a header and never in a URL. `?download` adds a
`Content-Disposition` with the file's name.

`GET /v1/records` lists what the caller may read, newest first, narrowed
by `?device=<rig>/<name>` or `?hold=<id>`, and
`GET /v1/devices/{rig}/{name}/recordings` is the same for one device.
`GET /list/<pubkey hex>` is Blossom's listing of what a key uploaded, for
the owner or for a `t=list` event signed by that key.

`DELETE /<sha256>` removes the bytes and the index entry, while the file
event stays, for the owner, the uploader, or a `t=delete` event signed
by the station or the uploading rig. The Records page does the same, and
marks a file public or private again.

### Retention

`--records-max-age` (30 days) and `--records-max-total` (2 GB) bound the
store: past either, the oldest files go, once at start and then every
hour. `--records-max-file` (256 MB) caps a single upload, and
`--records-dir` moves the files, which otherwise live in `blobs/` in the
data directory. The file events remain, so what was recorded, by whom
and on which hold stays on record after the bytes are gone.

## station-probe

`station-probe` is the headless check the spec asks for, built from
`cmd/station-probe`:

```bash
station-probe -station https://station.example -token stk_... [-device rig_x/phone] [-t 10s] [-ice relay|all] [-tap 0.5,0.5] [-insecure]
```

It lists the devices and takes one with `view` and `input`, then
connects over WebRTC through the station's signaling with the media
forced through TURN (`-ice relay`, the default) and prints the frame
rate, bitrate, keyframes, the candidate pair and its round trip. It taps
where `-tap` says (fractions of the picture) and compares screenshots
before and after, presses the device's Home shortcut when it announces
one, and then releases the hold. It exits non-zero when anything fails,
including a relay-only run whose media did not go through TURN.
`-insecure` accepts a LAN station's self-signed certificate; on a LAN,
relay-only fails by design, since TURN refuses private peers.

## Pages

| Page | What the owner does |
|---|---|
| `/login` | Sign in with the password; in tailnet mode an allowed identity needs none |
| `/` | The devices: rig, kind, online, who controls it, Open, Records |
| `/devices/{rig}/{name}/view` | The viewer: Roadie's shell with the WebRTC picture, sound, touch and keyboard, the mic, the settings panel, the on-screen keyboard, stats, the device's own buttons and panels from its extras, and Hold and Release. One link for driving and watching: a free device is taken, a held one is watched, and Hold takes it; Release ends the hold and returns to the device list. `?watch=1` watches only; on a touchscreen two fingers moving together turn the wheel and a pinch becomes Apple Maps' one-finger zoom on a device whose pointer stands in for touch (see Roadie's `/view`); `?minimal=1`, `?input=`, `?transport=`, `?ice=relay`, `?toolbar=`, `?align=`, `?zoom=` (a percentage, or `fit` for the room the picture has, the default on a phone or tablet) and `?stats=1` as on a Roadie |
| `/records` | The Record Store: every recording, screenshot and log the caller may read, newest first, or one device's with `?device=`, as cards showing each recording's last frame, or with `?view=list` as a list; Play, Download, make a file public or private, Delete |
| `/records/{sha256}/thumb.jpg` | A recording's last frame, from its unpacked copy, or a screenshot itself; the same readers as the file |
| `/records/{sha256}/files/{path}` | One file out of a recording's unpacked copy, such as `trace.trace` or `resources/<frame>`; the same readers as the file |
| `/records/{sha256}/play` | A recording in the embedded [Record Player](https://github.com/VibiumDev/record-player), framed under the station's own bar with Download and a full-page link; the player is served by the station at `/viewer/player/app/?record=<sha256>.zip` and fetches the file with the browser's own session |
| `/settings` | Enrollment codes, rigs, API tokens, the password, the network, the Record Store's room and limits |

The viewer's files are at `/viewer/shell.js`, `/viewer/shell.css`,
`/viewer/webrtc.js` and the audio worklets; a Roadie serves the same
files from this module, so its `/view` and the station's page are one.
A device's extras (`GET /v1/devices/{rig}/{name}/extras.js`, relayed from
the path the device announced) add its own buttons and settings rows.
The viewer takes `?ice=relay` (media through TURN only) and
`?transport=mjpeg` (the MJPEG fallback through the station from the
start, with input over the relayed HID WebSocket); the fallback also
kicks in on its own when WebRTC cannot connect.
