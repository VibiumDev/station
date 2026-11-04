# Reach a device remotely through the station

A Vibium Station is a server that puts your phones and other devices
within reach from anywhere. Each device is plugged into a small computer
that runs its remote-control software, such as a Raspberry Pi running
Roadie. The station calls that computer a rig. A rig joins a station
once and keeps a connection open to it from then on. The station passes
traffic through that connection to the devices on the rig. An agent on
your laptop therefore never talks to the Pi. It talks to the station,
using an address and a token.

This page covers the station's part of getting an agent onto a device.
The agent can be Claude Code, Codex, the Vibium CLI or a script of your
own. You need the station's address and a token, and you need to know
what it means to hold a device. What to do with the device once you have
it is the rig's own tutorial.
For a phone on a Roadie, that is
[Automate Roadie with Vibium](https://github.com/VibiumDev/roadie/blob/main/docs/tutorials/automate-with-vibium.md).

Everything here is plain WebDriver BiDi over one WebSocket. BiDi is the
browser-automation protocol Vibium speaks. Nothing on the laptop needs to
know how the station works inside. The agent talks to a URL with a
token, as it would to a remote browser.

## What you need

- The station's URL, such as `https://example.vibium.cloud` for a hosted
  one or `https://station.<tailnet>.ts.net` for your own.
- An API token from the station's Settings page (or `station token create
  laptop`), shown once. It starts with `stk_`.
- The device listed on the station's front page as online.
- [Vibium](https://github.com/VibiumDev/vibium) on the laptop:
  `npm install -g vibium`, or `npx vibium` for one-off use.

## The address and the token

Two environment variables tell the Vibium CLI, its MCP server and its
client libraries where the device is:

```bash
export VIBIUM_CONNECT_URL=wss://example.vibium.cloud/session
export VIBIUM_CONNECT_API_KEY=stk_...
```

`/session` is the standard BiDi entry point. The station gives it your
default device, the first online one. With several devices, name one
with the `vibium:device` capability on `session.new`. A device's id is
the rig's id followed by the device's name, for example
`rig_3f9a1c02/phone`. The station's front page shows it that way. The
name alone is enough when only one device has it. Each device also has
a URL of its own: `wss://.../v1/devices/<rig>/<name>/bidi`. The hold
endpoint described below returns it.

The token travels as `Authorization: Bearer` on the WebSocket upgrade.
Vibium sends it that way from `VIBIUM_CONNECT_API_KEY`; a client of your
own passes the header itself. Tokens never go in URLs.

From here on, follow the rig's tutorial. The station relays the socket
to the rig byte for byte, so the same commands, scripts and MCP setup
work unchanged.

## Holding the device

One agent or person drives a device at a time, and the station keeps
track of who. That is a **hold**. Opening the socket takes the hold on
the device. It stays yours until you close the session or leave it idle
for five minutes. A second agent asking for the same device waits in
line. A person watching the device on the station's web page can take
it back. While you have it, that page says "held by token:laptop", where
`laptop` is your token's name.
Closing the socket releases the device at once, so `vibium stop` and a
script's `bro.stop()` both hand it back.

To keep the device across several sessions, for example a CLI run
followed by the MCP server, take the hold yourself first. It then stays
yours until you release it or leave it idle for five minutes, no matter
how many sessions come and go on it:

```bash
curl -s -X POST -H "Authorization: Bearer stk_..." \
  -d '{"scopes": ["view", "bidi"]}' \
  https://example.vibium.cloud/v1/devices/<rig>/<name>/hold
# ... any number of sessions ...
curl -s -X DELETE -H "Authorization: Bearer stk_..." \
  https://example.vibium.cloud/v1/devices/<rig>/<name>/hold
```

The scopes say what the hold may do. `view` shows the screen. `input`
allows touching and typing through the station's own viewer. `bidi` is
what an agent needs. The hold endpoint also keeps a queue. A token
asking for a held device gets `202` and its place in line. Adding
`?wait=30` waits there for up to 30 seconds. [API.md](../../API.md) has
the details.

## Tokens

A token made read-only may watch a device but never hold it, so an
agent needs one that may control. The secret is shown only once, when
the token is made. Revoking a token cuts its live sessions at once.

## When something is off

- `session not created: held by token:ci, place 1 in line`: another
  agent has the device. Wait, ask it to stop, or release it from the
  station's devices page.
- `session not created: no device is online`: the rig is not connected
  to the station. The rig is the Pi the device is plugged into. Its row
  on the station's Rigs page says when it was last seen.
- `this token may only view`: the token was made read-only.
- The socket closes while a script runs: your hold ended. Either a
  person took the device at the viewer, or it sat idle for five minutes,
  or it reached the eight-hour maximum. Start again.
- Anything on the device itself, such as a key chord that does nothing
  or a tap that lands elsewhere, is in the rig's tutorial.
