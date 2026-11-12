# Set Up Jams

A jam is a prompt on the station's front page that a model carries out on
a device. This guide sets up the models a jam uses: the agent that drives
the device, and the voices that listen and speak. Everything here is on
**Settings**, under **AI**, except installing Vibium and Piper.

The **AI** section has three parts: **Agent**, **Say** and **Listen**.
Each keeps as many models as you add, and one of them is **in use**: the
one a jam takes. **Use** on another row puts it in that place. Each part's
**?** says where its models and voices are kept.

## 1. Vibium

A jam drives the device through `vibium mcp`. The station finds Vibium at
`--vibium`, then `STATION_VIBIUM`, then `vibium` on its `PATH`. Settings
shows the path it found, in the **Vibium** row. Install it as
[the self-host guide](self-host.md#1-get-the-binary) describes, and
restart the station after installing or replacing it.

## 2. An agent model

The agent reasons, reads the screenshots and calls the tools. Under
**Agent**, press **Add an agent model** and fill in the form:

- **name**: any name, such as `gpt`.
- **provider**: OpenAI, Anthropic, Google Gemini, xAI, or a server of your
  own that speaks OpenAI's API.
- **model**: the provider's name for the model.
- **API key**: seen once as it is typed, then kept in a file under `ai/`
  in the data directory that only the station's user can read.

**Add** asks the model for one word, so a wrong key shows at once.
With an agent model set, Jams turn on and the prompt appears on the
front page.

## 3. Voices: listen and say

A jam can hear the device and speak into it when a **listen** model and a
**say** model are set. Vibium makes these calls itself, with the settings
the station hands it. The agent's key is not passed on.

A jam speaks with Piper on this machine when Piper is installed, as step
4 shows. With an OpenAI key, a cloud voice and listening work too:

| Part | Button | Provider | Model |
|------|--------|----------|-------|
| Say | **Add a voice** | OpenAI | `gpt-4o-mini-tts` |
| Listen | **Add a listen model** | OpenAI | `gpt-transcribe` |

Each needs the key typed again: models do not share keys. Listening on
this machine is not offered yet.

## 4. Speak with Piper, on this machine

[Piper](https://github.com/OHF-voice/piper1-gpl) speaks on the station's
own machine, with no key and no network. It is the station's default
voice when it is installed. The station runs Piper's server itself and
keeps a voice loaded, so a phrase takes about a second, where starting
Piper for every phrase takes 10 to 15 seconds on a Raspberry Pi 4.

Piper is a separate program under its own license (GPL-3.0). The station
does not include it: you install it once, and the station finds it.

1. Install Piper with its server, in a folder named `piper` in the data
   directory. With the data directory at `/var/lib/station`:

   ```bash
   sudo -u station python3 -m venv /var/lib/station/piper/venv
   sudo -u station /var/lib/station/piper/venv/bin/pip install "piper-tts[http]"
   ```

   A Piper installed elsewhere is named with `--piper`, or
   `STATION_PIPER` in `/etc/default/station`: the Python that has
   `piper-tts[http]`. A `piper` program on the station's `PATH` is found
   too.

2. Put one or more voices in the same folder:

   ```bash
   sudo -u station /var/lib/station/piper/venv/bin/python -m piper.download_voices \
     --download-dir /var/lib/station/piper en_US-lessac-medium
   ```

   A voice is two files, `NAME.onnx` and `NAME.onnx.json`. The
   [Piper voices](https://huggingface.co/rhasspy/piper-voices) page lists
   them.

3. Restart the station: `sudo systemctl restart station`.

The station starts Piper's server on a free port of `127.0.0.1`, loads a
voice, and restarts the server if it stops. On **Settings**, under
**AI**, in **Say**:

- A voice named `piper` is listed without adding it. It is in use unless
  another voice was marked in use.
- With more than one Piper voice installed, the `piper` row has a list
  of them. **Use voice** switches the voice, and the server loads it at
  once.
- The **Piper** line under the list says where Piper's server runs and
  how many voices it has, or why it is not running. The server's own
  output is in `piper/server.log`.
- **Hear** at the top of **Say** takes up to 50 characters. ▶ on a voice
  says them with that voice, through its real server or API, and plays
  them in the browser, so a voice is heard working before a jam needs it.

A cloud voice is a model of its own, as in step 3. **Use** on its row
makes jams speak with it instead.

A Piper server on another machine is a voice with provider **Piper
server**, the voice's name as its model and the server's address as its
base URL.

## When something is off

- No prompt on the front page: no agent model is set, or Jams are off on
  Settings.
- **Vibium** on Settings says not found: see step 1.
- No `piper` voice on Settings: the **Piper** line under **Say** says why. Piper's
  server takes some seconds to load a voice after a restart.
- A jam's say fails with `engine_failed: no Piper server answered`: the
  server stopped, or a base URL names another address.
- A jam's say fails with `unknown say provider "piper-server"`: the
  station's Vibium is older than Piper server support.
