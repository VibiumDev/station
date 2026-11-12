# Set Up Jams

A jam is a prompt on the station's front page that a model carries out on
a device. This guide sets up the models a jam uses: the agent that drives
the device, and the voices that listen and speak. Everything here is on
**Settings**, under **AI models**, except the Vibium binary and a Piper
server.

## 1. Vibium

A jam drives the device through `vibium mcp`. The station finds Vibium at
`--vibium`, then `STATION_VIBIUM`, then `vibium` on its `PATH`. Settings
shows the path it found, in the **Vibium** row. Install it as
[the self-host guide](self-host.md#1-get-the-binary) describes, and
restart the station after installing or replacing it.

## 2. An agent model

The agent reasons, reads the screenshots and calls the tools. Under **AI
models**, fill in the form:

- **name**: any name, such as `gpt`.
- **role**: `agent`.
- **provider**: OpenAI, Anthropic, Google Gemini, xAI, or a server of your
  own that speaks OpenAI's API.
- **model**: the provider's name for the model.
- **API key**: seen once as it is typed, then kept in a file under `ai/`
  in the data directory that only the station's user can read.

**Add model** asks the model for one word, so a wrong key shows at once.
With an agent model set, Jams turn on and the prompt appears on the
front page.

## 3. Voices: listen and say

A jam can hear the device and speak into it when a **listen** model and a
**say** model are set. Vibium makes these calls itself, with the settings
the station hands it. The agent's key is not passed on.

With an OpenAI key:

| Role | Provider | Model |
|------|----------|-------|
| listen | OpenAI | `gpt-transcribe` |
| say | OpenAI | `gpt-4o-mini-tts` |

Each needs the key typed again: models do not share keys.

## 4. Say through a local Piper server

[Piper](https://github.com/OHF-voice/piper1-gpl) speaks on the station's
own machine, with no key and no network. Started for every phrase it is
slow: about 10 to 15 seconds on a Raspberry Pi 4. Its server keeps a
voice loaded, and then a phrase takes about a second.

The station does not start Piper. You run the server next to the
station, and the station tells Vibium where it is. This needs a Vibium
that knows Piper servers: `vibium say --help` shows a
`VIBIUM_PIPER_URL` example when it does.

1. Install Piper with its server, and a voice:

   ```bash
   python3 -m venv ~/piper
   ~/piper/bin/pip install "piper-tts[http]"
   mkdir -p ~/piper/voices
   ~/piper/bin/python -m piper.download_voices --download-dir ~/piper/voices en_US-lessac-medium
   ```

   The [Piper voices](https://huggingface.co/rhasspy/piper-voices) page
   lists the others.

2. Start the server on this machine only:

   ```bash
   ~/piper/bin/python -m piper.http_server \
     -m ~/piper/voices/en_US-lessac-medium.onnx \
     --data-dir ~/piper/voices --host 127.0.0.1 --port 5000
   ```

   It asks for no password, so keep `--host 127.0.0.1`. To keep it
   running, Vibium's guide
   [Keep a Piper Voice Loaded](https://github.com/VibiumDev/vibium/blob/main/docs/how-to-guides/keep-a-piper-voice-loaded.md)
   has a service file.

3. On **Settings**, under **AI models**, add a model:

   - **name**: such as `piper`.
   - **role**: `say`.
   - **provider**: Piper server (say).
   - **model**: the voice's name, the file name without `.onnx`, such as
     `en_US-lessac-medium`.
   - **base URL**: the server's address. Empty means
     `http://127.0.0.1:5000`.
   - **API key**: none.
   - Tick **default for its role** if another say model is set.

   A Piper server can only be a say model. Listening still needs a listen
   model of its own.

4. Check it from the station's machine. `curl http://127.0.0.1:5000/info`
   names the loaded voice. A jam started after this speaks with Piper.

## When something is off

- No prompt on the front page: no agent model is set, or Jams are off on
  Settings.
- **Vibium** on Settings says not found: see step 1.
- A jam's say fails with `engine_failed: no Piper server answered`: the
  server is not running, or the base URL names another address.
- A jam's say fails with `unknown say provider "piper-server"`: the
  station's Vibium is older than Piper server support.
