# Reach a device remotely through the station

A Vibium Station is a server that puts your phones and other devices
within reach from anywhere. Each device is plugged into a small computer
that runs its remote-control software, such as a Raspberry Pi running
Roadie, and the station calls that computer a rig. A rig joins a
station once and keeps a connection open to it from then on, and the
station passes traffic through that connection to the devices on the
rig. An agent on your laptop therefore never talks to the Pi: it talks
to the station, using an address and a token.

This page covers the station's part of getting an agent onto a device,
whether that agent is Claude Code, Codex, the Vibium CLI or a script of
your own. It explains where to point the agent, which token to give it,
and what it means to hold a device. What to do with the device once you
have it belongs to the rig's own tutorial, which for a phone on a Roadie
is
[Automate Roadie with Vibium](https://github.com/VibiumDev/roadie/blob/main/docs/tutorials/automate-with-vibium.md).

Everything here is plain WebDriver BiDi, the browser-automation protocol
Vibium speaks, carried over one WebSocket. Nothing on the laptop needs
to know how the station works inside, because the agent simply talks to
a URL with a token, as it would to a remote browser.

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

`/session` is the standard BiDi entry point, and the station gives it
your default device, which is the first one online. With several
devices, name the one you want with the `vibium:device` capability on
`session.new`. A device's id is the rig's id followed by the device's
name, for example `rig_3f9a1c02/phone`, which is how the station's front
page shows it; the name alone is enough when only one device carries
it. Each device also has a URL of its own,
`wss://.../v1/devices/<rig>/<name>/bidi`, which the hold endpoint
described below returns.

The token travels as `Authorization: Bearer` on the WebSocket upgrade,
which is how Vibium sends it from `VIBIUM_CONNECT_API_KEY`; a client of
your own passes the header itself. Tokens never go in URLs.

From here on, follow the rig's tutorial: because the station relays the
socket to the rig byte for byte, the same commands, scripts and MCP
setup work unchanged.

## Holding the device

Only one agent or person drives a device at a time, and the station
keeps track of who that is; this is called a **hold**. Opening the
socket takes the hold on the device, and it stays yours until you close
the session or leave it idle for five minutes. A second agent asking for
the same device waits in line, while a person watching the device on the
station's web page can take it back at any time. While you have it, that
page says "held by token:laptop", where `laptop` is your token's name.
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

The scopes say what the hold may do: `view` shows the screen, `input`
allows touching and typing through the station's own viewer, and `bidi`
is what an agent needs. The hold endpoint also keeps a queue, so a token
asking for a held device gets `202` with its place in line, and adding
`?wait=30` waits there for up to 30 seconds. [API.md](../../API.md) has
the details.

## Tokens

A token made read-only may watch a device but never hold it, so an
agent needs one that may control. The secret is shown only once, when
the token is made, and revoking a token cuts its live sessions at once.

## When something is off

- `session not created: held by token:ci, place 1 in line`: another
  agent has the device. Wait, ask it to stop, or release it from the
  station's devices page.
- `session not created: no device is online`: the rig, which is the Pi
  the device is plugged into, is not connected to the station. Its row
  on the station's Rigs page says when it was last seen.
- `this token may only view`: the token was made read-only.
- The socket closes while a script runs: your hold ended, either because
  a person took the device at the viewer, because it sat idle for five
  minutes, or because it reached the eight-hour maximum. Start again.
- Anything on the device itself, such as a key chord that does nothing
  or a tap that lands elsewhere, is in the rig's tutorial.
