# Station API

All calls need a signed-in session (the pages), a tailnet identity in
tailnet mode, or `Authorization: Bearer <token>` with a token from the
Settings page. Browser sessions send the CSRF token the page carries in
`X-CSRF-Token` on anything but GET. Errors are `{"error": "..."}`.

A device id is `<rig id>/<name>`, such as `rig_3f9a1c02/phone`, and
appears in paths as two segments.

| Method and path | Purpose |
|---|---|
| `GET /v1/devices` | Every rig's devices: id, kind, capabilities, shortcuts, rig, online, and the lease held on it |
| `POST /v1/devices/{rig}/{name}/lease` | Take the device, or a seat watching whoever has it. Body `{"scopes": ["view", "input"]}`; `view` is always included. Returns the signed grant, the lease and whether the caller is an observer |
| `DELETE /v1/devices/{rig}/{name}/lease` | Release the caller's lease. An observer cannot end the holder's |
| `wss /v1/devices/{rig}/{name}/signal` | WebRTC signaling, relayed to the rig. The first message is `{"type":"hello","grant":{...}}`; the rig checks the grant and answers with its offer |
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
  "grant": {"kind": 7700, "pubkey": "...", "tags": [["p", "<rig key>"], ["rig", "rig_3f9a1c02"], ["dev", "rig_3f9a1c02/phone"], ["sub", "owner"], ["lease", "lse_9f3c0a11"], ["seq", "42"], ["scope", "view"], ["scope", "input"], ["expiration", "1793180000"], ["nonce", "..."]], "sig": "..."},
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

## Pages

| Page | What the owner does |
|---|---|
| `/login` | Sign in with the password; in tailnet mode an allowed identity needs none |
| `/` | The devices: rig, kind, online, who controls it, Open |
| `/devices/{rig}/{name}/view` | The viewer: WebRTC picture, sound, touch and keyboard, the mic, the device's own buttons, stats, Take control and Release |
| `/settings` | Enrollment codes, rigs, API tokens, the password, the network |

The viewer's files are at `/viewer/webrtc.js`, `/viewer/input.js`,
`/viewer/viewer.js` and `/viewer/viewer.css`; a Roadie serves the same
`webrtc.js` from this module.
