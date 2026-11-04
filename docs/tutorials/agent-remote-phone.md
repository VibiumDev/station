# Drive a phone from your laptop with an agent

You have a phone on a Roadie somewhere, enrolled with a Vibium Station,
and an agent on your laptop: Claude Code, Codex, or a script you are
writing. This is how the agent gets the phone, what it can do with it,
and how you keep a recording of what it did.

Everything below is plain WebDriver BiDi over one WebSocket. Nothing on
the laptop knows about the station's tunnel, holds or keys; the agent
talks to a URL with a token, the way it would talk to a remote browser.

## What you need

- The station's URL, such as `https://example.vibium.cloud` for a hosted
  one or `https://station.<tailnet>.ts.net` for your own.
- An API token from the station's Settings page (or `station token create
  laptop`), shown once. It starts with `stk_`.
- The phone listed on the station's front page as online.
- [Vibium](https://github.com/VibiumDev/vibium) on the laptop:
  `npm install -g vibium`, or `npx vibium` for one-off use.

Two environment variables tell every Vibium tool where the phone is:

```bash
export VIBIUM_CONNECT_URL=wss://example.vibium.cloud/session
export VIBIUM_CONNECT_API_KEY=stk_...
```

`/session` is the standard BiDi entry point. The station gives it your
default device, the first online one. With several phones, name one
with the `vibium:device` capability on `session.new`, or use the
device's own URL, `wss://.../v1/devices/<rig>/<name>/bidi`, which the
station's hold endpoint returns.

Opening the socket takes a **hold** on the phone: yours until you close
the session or leave it idle for five minutes. A second agent asking for
the same phone waits in line; a person at the station's viewer can take
it back (the viewer then says "held by token:laptop" while you have it).

To keep the phone across several sessions, say a CLI run and then the
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

## Three ways the agent drives the phone

### 1. The Vibium MCP server, for Claude Code or Codex

Add the server with the two variables, so the agent's tools reach the
phone instead of a local browser:

```bash
claude mcp add vibium \
  -e VIBIUM_CONNECT_URL=wss://example.vibium.cloud/session \
  -e VIBIUM_CONNECT_API_KEY=stk_... \
  -- npx -y vibium mcp
```

Then ask for what you want in words. A phone has no DOM, so the tools
that matter are `browser_start`, `browser_screenshot`, `browser_keys`,
`browser_tap`, `browser_long_press`, and `browser_record_start` and
`browser_record_stop`. A prompt like this works:

> Start a recording to calc.zip, take a screenshot of the phone, open
> Search with Meta+Space, type calc, press Enter to launch the
> Calculator, screenshot again, press Meta+h to go home, and stop the
> recording.

The agent sees each screenshot, so it can check that the Calculator
came up. Keys do everything in this tutorial; nothing depends on where
an icon sits on the screen. On an iPhone, `Meta+Space` opens Search,
`Meta+h` is the Home chord, and `Shift` wakes the screen. `browser_keys`
sends one key or chord per call, so a word is one call per letter.
`browser_tap` is there for what only a finger reaches.

### 2. The Vibium CLI, by hand or from a script

```bash
vibium start                      # connects to $VIBIUM_CONNECT_URL
vibium keys Meta+h                # home screen
vibium record start -o calc.zip   # screenshots per step from here on
vibium screenshot -o home.png
vibium keys Meta+Space            # open Search
vibium keys c                     # one key per call: c, a, l, c
vibium keys a
vibium keys l
vibium keys c
vibium keys Enter                 # launch the top hit, the Calculator
vibium screenshot -o calc.png
vibium keys Meta+h
vibium record stop                # writes calc.zip
vibium stop                       # ends the session and releases the phone
```

`vibium keys` takes one key or chord, so a word is one call per letter.
`vibium screenshot` saves what the phone shows right now. When only a
finger will do, `vibium tap X Y` taps the picture at those pixels;
`vibium start` prints the session, and the picture's size is in the
`roadie:viewport` capability (446 by 972 for an iPhone 15 Pro Max in
portrait).

### 3. An SDK script, in JavaScript or Python

JavaScript (`npm install vibium-client`):

```js
import { browser } from 'vibium-client';
import { writeFileSync } from 'node:fs';

const bro = await browser.start(process.env.VIBIUM_CONNECT_URL, {
  headers: { Authorization: 'Bearer ' + process.env.VIBIUM_CONNECT_API_KEY },
});
try {
  const page = await bro.page();
  await page.keyboard.press('Meta+h');
  await page.context.recording.start({ path: 'calc.zip' });
  writeFileSync('home.jpg', await page.screenshot());
  await page.keyboard.press('Meta+Space');
  await page.keyboard.type('calc');
  await page.keyboard.press('Enter');
  await new Promise((r) => setTimeout(r, 2000));
  writeFileSync('calc.jpg', await page.screenshot());
  await page.keyboard.press('Meta+h');
  console.log(await page.context.recording.stop());
} finally {
  await bro.stop();
}
```

Python (`pip install vibium`):

```python
import asyncio, os
from vibium.async_api import browser

async def main():
    bro = await browser.start(os.environ["VIBIUM_CONNECT_URL"],
                              headers={"Authorization": "Bearer " + os.environ["VIBIUM_CONNECT_API_KEY"]})
    try:
        page = await bro.page()
        await page.keyboard.press("Meta+h")
        await page.context.recording.start(path="calc.zip")
        open("home.jpg", "wb").write(await page.screenshot())
        await page.keyboard.press("Meta+Space")
        await page.keyboard.type("calc")
        await page.keyboard.press("Enter")
        await asyncio.sleep(2)
        open("calc.jpg", "wb").write(await page.screenshot())
        await page.keyboard.press("Meta+h")
        print(await page.context.recording.stop())
    finally:
        await bro.stop()

asyncio.run(main())
```

Both start from the URL and the token in the environment; `start()` also
takes the URL and headers as arguments, as shown. The SDKs run the
Vibium engine on the laptop and talk to it; the engine holds the socket
to the station.

## The recording

`calc.zip` is a Playwright trace. Open it with the trace viewer:

```bash
npx playwright show-trace calc.zip
```

Inside, `trace.trace` lists each action with its timing (the Home press,
the screenshots, the keys), `trace.network` is empty, since a phone makes
no page requests the engine can see, and `resources/` holds a JPEG of
the phone's screen for every step, named by the time it was taken. A
recording made through the station has no video track: the engine
records video on the machine the browser runs on, and here that is the
rig. Screenshots are the record.

A session from this tutorial, run against the test station with each of
the three paths, gave a trace of six steps from each SDK, seventeen
from the CLI (which records its page lookup before every command as a
step of its own), and the same two pictures everywhere: the home screen
before the search and the Calculator after it.

## When something is off

- `session not created: held by token:ci, place 1 in line`: another
  agent has the phone. Wait, ask it to stop, or release it from the
  station's devices page.
- `this token may only view`: the token was made read-only; agents need
  one that may control.
- The socket closes while a script runs: the hold ended, by a person
  taking the phone at the viewer, by the five idle minutes, or by the
  eight-hour maximum. Start again.
- `Meta+Space` opens nothing: the phone is asleep or locked. `Shift`
  wakes it; unlock it once by hand, and `Meta+h` first puts it on the
  home screen, where Search opens.
- Enter opened the wrong thing: the top hit follows what the phone was
  used for lately. Type the whole name, `calculator`.
- Taps land somewhere else: coordinates are the picture's pixels, not
  the phone's screen; read the size from `roadie:viewport`.
