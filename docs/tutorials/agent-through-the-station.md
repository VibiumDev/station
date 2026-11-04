# Reach a device through the station from a laptop

A Vibium Station is a server that puts your phones and other devices
within reach from anywhere. Each device is plugged into a small computer
that runs its remote-control software, such as a Raspberry Pi running
Roadie. The station calls that computer a rig. A rig joins a station
once, keeps a connection open to it from then on, and the station passes
traffic through to the devices on it. So an agent on your laptop never
talks to the Pi: it talks to the station, with an address and a token.

This page is the station's part of getting an agent onto a device,
whether the agent is Claude Code, Codex, the Vibium CLI or a script of
your own: the address, the token, and what it means to hold a device.
What to do with the device once you have it is the rig's own tutorial.
For a phone on a Roadie, that is
[Automate Roadie with Vibium](https://github.com/VibiumDev/roadie/blob/main/docs/tutorials/automate-with-vibium.md).

Everything here is plain WebDriver BiDi, the browser-automation protocol
Vibium speaks, over one WebSocket. Nothing on the laptop knows how the
station works inside; the agent talks to a URL with a token, the way it
would talk to a remote browser.

## What you need

- The station's URL, such as `https://example.vibium.cloud` for a hosted
  one or `https://station.<tailnet>.ts.net` for your own.
- An API token from the station's Settings page (or `station token create
  laptop`), shown once. It starts with `stk_`.
- The device listed on the station's front page as online.
- [Vibium](https://github.com/VibiumDev/vibium) on the laptop:
  `npm install -g vibium`, or `npx vibium` for one-off use.

## The address and the token

Two environment variables tell every Vibium tool, the MCP server and the
clients where the device is:

```bash
export VIBIUM_CONNECT_URL=wss://example.vibium.cloud/session
export VIBIUM_CONNECT_API_KEY=stk_...
```

`/session` is the standard BiDi entry point. The station gives it your
default device, the first online one. With several devices, name one
with the `vibium:device` capability on `session.new`. A device's id is
its rig's id and its own name, `rig_3f9a1c02/phone`, as the station's
front page shows it; the name alone, `phone`, does when it is unique.
Each device also has a URL of its own,
`wss://.../v1/devices/<rig>/<name>/bidi`, which the hold endpoint below
returns.

The token travels as `Authorization: Bearer` on the WebSocket upgrade.
Vibium sends it that way from `VIBIUM_CONNECT_API_KEY`; a client of your
own passes the header itself. Tokens never go in URLs.

From here on, follow the rig's tutorial: the same commands, scripts and
MCP setup work unchanged, because the station relays the socket to the
rig byte for byte.

## Holding the device

One agent or person drives a device at a time, and the station keeps
track of who: that is a **hold**. Opening the socket takes the hold on
the device, yours until you close the session or leave it idle for five
minutes. A second agent asking for the same device waits in line. A
person watching the device on the station's web page can take it back;
while you have it, that page says "held by token:laptop", `laptop` being
your token's name.
Closing the socket releases the device at once, so `vibium stop` and a
script's `bro.stop()` both hand it back.

To keep the device across several sessions, say a CLI run and then the
MCP server, take the hold yourself first. It is then yours until you
release it or leave it idle for five minutes, whatever sessions come and
go on it:

```bash
curl -s -X POST -H "Authorization: Bearer stk_..." \
  -d '{"scopes": ["view", "bidi"]}' \
  https://example.vibium.cloud/v1/devices/<rig>/<name>/hold
# ... any number of sessions ...
curl -s -X DELETE -H "Authorization: Bearer stk_..." \
  https://example.vibium.cloud/v1/devices/<rig>/<name>/hold
```

The scopes say what the hold may do: `view` the screen, `input` to touch
and type through the station's own viewer, `bidi` for an agent. The hold
endpoint also queues: a token asking for a held device gets `202` and
its place in line, and `?wait=30` waits there for up to 30 seconds.
[API.md](../../API.md) has the details.

## Tokens

A token made read-only may watch a device but never hold it, so an
agent needs one that may control. The secret is shown once, when the
token is made. Revoking a token cuts its live sessions at once.

## When something is off

- `session not created: held by token:ci, place 1 in line`: another
  agent has the device. Wait, ask it to stop, or release it from the
  station's devices page.
- `session not created: no device is online`: the rig, the Pi the device
  is plugged into, is not connected to the station. Its row on the
  station's Rigs page says when it was last seen.
- `this token may only view`: the token was made read-only.
- The socket closes while a script runs: the hold ended, by a person
  taking the device at the viewer, by the five idle minutes, or by the
  eight-hour maximum. Start again.
- Anything on the device itself, such as a key chord that does nothing
  or a tap that lands elsewhere, is in the rig's tutorial.
