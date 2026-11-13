# Set Up Jams

A jam session is a prompt you type on the station's front page, which an
AI chat model then carries out on a device. This guide sets up the
models a jam session uses: the AI chat model that drives the device, and
the text to speech and speech to text models it uses to talk and listen.
Apart from installing Vibium and Piper, everything here is done on the
**AI** tab of **Settings**.

The **AI models** section of that tab has three parts: **Agent** (the AI
chat model), **Say** (text to speech) and **Listen** (speech to text).
You can add more than one model to each part. New jam sessions use the
one marked **in use**, and **Use** on another row switches to it. The
**?** next to each part says where its files are kept.

## 1. Vibium

A jam session drives the device through `vibium mcp`. The station looks
for Vibium at `--vibium`, then `STATION_VIBIUM`, then `vibium` on its
`PATH`, and the **Vibium** row under **Jam sessions** shows the path it
found. Restart the station after installing or replacing it.

The Docker image, which Fly.io runs, comes with a Vibium nightly already.
On a host of your own, install one of the three below under
`/usr/local/bin`: the service's sandbox (`ProtectHome=yes`) keeps the
station out of home directories, so a binary in your checkout is not found.

### A tagged release

Releases ship their binaries as npm packages, `@vibium/linux-arm64` and
`@vibium/linux-x64`. The station needs only the binary inside, so Node is
not needed:

```bash
ARCH=arm64   # or x64 on a PC
VERSION=26.8.21
PKG=https://registry.npmjs.org/@vibium/linux-$ARCH
curl -fsSLo vibium.tgz $PKG/-/linux-$ARCH-$VERSION.tgz
curl -fsSL $PKG/$VERSION | grep -o '"integrity":"[^"]*"'
echo "sha512-$(openssl dgst -sha512 -binary vibium.tgz | base64 -w0)"
tar xzf vibium.tgz package/bin/vibium
sudo install -m 755 package/bin/vibium /usr/local/bin/vibium
vibium --version
```

The two integrity strings must be the same. Release 26.8.21 cannot tap,
swipe, long press or type text on a phone, and cannot send a jam's record
to the station, so use a nightly until a later release is out. The Docker
image does not take a release yet.

### A nightly

Nightly builds are static binaries on Vibium's GitHub
[releases](https://github.com/VibiumDev/vibium/releases):

```bash
ARCH=arm64   # or amd64
NIGHTLY=nightly-2026.11.12-dev.20261112015632-bf3355b
curl -fLO https://github.com/VibiumDev/vibium/releases/download/$NIGHTLY/vibium-linux-$ARCH
curl -fLO https://github.com/VibiumDev/vibium/releases/download/$NIGHTLY/SHA256SUMS
sha256sum vibium-linux-$ARCH
grep "vibium-linux-$ARCH\$" SHA256SUMS
sudo install -m 755 vibium-linux-$ARCH /usr/local/bin/vibium
vibium --version
```

The two checksums printed must be the same. With this build a jam on a
phone can tap, swipe, long press, type text, send its record, and speak
and listen. The Docker image installs this nightly by default; for
another, add `--build-arg VIBIUM_NIGHTLY=<its tag>` to `docker build` or
`fly deploy`.

Speaking through the station's own Piper (step 4) needs a Vibium with
Piper server support, which no nightly has yet: build your own for that.

### Your own build

To use a Vibium you built, such as one from a branch that has no nightly
yet, build it from your Vibium checkout with the station clone's script.
The version it stamps is Vibium's own plus the commit, such as
`26.8.21-local.328d74a`, with `.dirty` when the checkout has changes that
are not committed. On a host:

```bash
ARCHES=arm64 scripts/vibium-local.sh ../vibium   # amd64 on a PC
sudo install -m 755 deploy/vibium/vibium-linux-arm64 /usr/local/bin/vibium
vibium --version                                  # vibium v26.8.21-local.<commit>
```

For the Docker image, which is how Fly.io runs it, build for amd64 and
tell the image to take it: add `--build-arg VIBIUM_LOCAL=1` to `docker
build`, or to the `fly deploy` command in
[Deploy to Fly.io](deploy-to-fly.md#4-deploy).

```bash
ARCHES=amd64 scripts/vibium-local.sh ../vibium
```

The binaries in `deploy/vibium/` stay out of git. The build log says
`Vibium from deploy/vibium` with the file's checksum, and the build stops if
`VIBIUM_LOCAL=1` is set but the file is missing. Leave the build argument
out to go back to the nightly.

## 2. An agent model

The agent is the AI chat model that runs the session: it reads the
screenshots and decides what to tap and type. Under **Agent**, press
**Add an agent model** and fill in the form:

- **Name**: any name you like, such as `gpt`.
- **Provider**: OpenAI, Anthropic, Google Gemini, xAI, or a server of
  your own with an OpenAI-compatible API.
- **Model**: the provider's name for the model.
- **API key**: saved in a file under `ai/` in the data directory, which
  only the station's user can read.

When you press **Add**, the station sends the model a short test
message, so a wrong key or model name shows up right away. Once an agent
model is set, jam sessions turn on and the prompt appears on the front
page.

## 3. Voices: listen and say

A jam session can listen to the device and talk into it when a **Listen**
model and a **Say** model are set. Vibium makes these calls itself, using
the settings the station passes to it. The agent's key is not passed on.

If Piper is installed, as step 4 shows, jam sessions talk with Piper on
this machine. With an OpenAI key you can also use OpenAI's text to speech
and speech to text:

| Part | Button | Provider | Model |
|------|--------|----------|-------|
| Say | **Add a voice** | OpenAI | `gpt-4o-mini-tts` |
| Listen | **Add a listen model** | OpenAI | `gpt-transcribe` |

Each model needs its own copy of the key, because models don't share
keys. Speech to text on this machine isn't supported yet.

## 4. Talk with Piper, on this machine

[Piper](https://github.com/OHF-voice/piper1-gpl) is text to speech that
runs on the station's own machine, with no key and no network. When it
is installed, it is the station's default voice. The station runs Piper's
server itself and keeps a voice loaded, so a phrase takes about a second
instead of the 10 to 15 seconds it takes to start Piper for each phrase
on a Raspberry Pi 4.

Piper is a separate program under its own license (GPL-3.0), so the
station doesn't include it. You install it once, and the station finds
it.

1. Install Piper with its server in a folder named `piper` in the data
   directory. With the data directory at `/var/lib/station`:

   ```bash
   sudo -u station python3 -m venv /var/lib/station/piper/venv
   sudo -u station /var/lib/station/piper/venv/bin/pip install "piper-tts[http]"
   ```

   If Piper is installed somewhere else, point the station at the Python
   that has `piper-tts[http]`, with `--piper` or with `STATION_PIPER` in
   `/etc/default/station`. A `piper` program on the station's `PATH` is
   found too.

2. Download one or more voices into the same folder:

   ```bash
   sudo -u station /var/lib/station/piper/venv/bin/python -m piper.download_voices \
     --download-dir /var/lib/station/piper en_US-lessac-medium
   ```

   Each voice is two files, `NAME.onnx` and `NAME.onnx.json`. The
   [Piper voices](https://huggingface.co/rhasspy/piper-voices) page lists
   them all.

3. Restart the station: `sudo systemctl restart station`.

The station starts Piper's server on a free port of `127.0.0.1`, loads a
voice, and restarts the server if it stops. On the **AI** tab, under
**Say**:

- A voice named `piper` is listed without you adding it. It is in use
  unless you chose another voice.
- If more than one Piper voice is installed, the `piper` row has a list
  of them. Pick one and press ▶ to hear it, then **Use** to switch jam
  sessions to it. The server loads the new voice right away.
- **Test phrase**, above the list, holds what ▶ says, up to 50
  characters. ▶ sends it to the voice's real server or API and plays the
  result in your browser, so you can hear a voice before you use it.
- If Piper isn't running, a line under the list says why. The server's
  own output is in `piper/server.log`.

To use a cloud voice instead, add it as in step 3 and press **Use** on
its row.

To use a Piper server on another machine, add a voice with the provider
**Piper server**, the voice's name as the model, and the server's address
as the base URL.

## When something is off

- No prompt on the front page: no agent model is set, or jam sessions
  are turned off on the AI tab.
- **Vibium** says not found: see step 1.
- No `piper` voice under **Say**: the line under the list says why.
  Piper's server takes a few seconds to load a voice after a restart.
- A session's say fails with `engine_failed: no Piper server answered`:
  the server stopped, or a base URL names the wrong address.
- A session's say fails with `unknown say provider "piper-server"`: the
  station's Vibium is older than Piper server support.
