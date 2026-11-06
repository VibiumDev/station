# Jam Sessions: a prompt on the station

Nov 6, 2026 · draft for review

## Summary and goals

The station's front page is a prompt box, the kind ChatGPT and Claude
open on: a few lines tall, wide, growing with what is typed, with a
send button in its corner. A person signs in, reads "What can I check
for you?" above it, types a claim or an errand, and an agent
drives a device on the station to do it, while the page shows the
agent's steps and the device being driven. When the agent is done the
answer sits in the transcript, and the recording of what it did sits in
the Record Store, playable from the Records page like any other. Each of
these is a jam session, a jam for short.

The agent is Vibium's own. `vibium check` verifies a claim and ends with
a verdict; `vibium run` carries out a goal and ends when it is done. Both
reach a phone the way the agent tutorial already does, through a station
token and the relayed BiDi session, and both take their model from the
same `VIBIUM_AI_*` settings every Vibium user has, which here the
station's Settings page writes. The page puts a front on that, and adds
nothing to the agent itself.

The goals, in order:

1. **One prompt, one key.** Once the owner has picked a model provider
   and pasted its key on Settings, anyone who can sign in to the
   station can type a sentence and watch a phone act on it.
2. **The same agent as the CLI.** The page runs what `vibium check` and
   `vibium run` run, so a claim behaves the same from a terminal, a
   script and the page, and the recording is the same recording.
3. **The work can run anywhere.** Where the agent runs is a choice
   behind one interface: a container on the station's host, a Sprite
   on Fly, or a plain process, so a hosted station and a self-hosted
   one share the page and differ only in the launcher.
4. **The rig stays the final gate.** A jam is a hold like any other,
   signed by the station, checked by the rig, limited by the rig's
   policy. Nothing about the agent is trusted by the Pi.

## Naming

The page and its list are **Jams**, and each one is a jam session, which
keeps Vibium's music theme going: a roadie sets up the rig, a jam is
players improvising around a theme, and the Record Store keeps the record
that comes out of it. Each prompt starts a jam. A check is a jam that
ends with a verdict, and an errand is a jam that ends when the goal is
done. The word promises nothing a plain errand cannot deliver, and it
names the thing rather than the interaction. "Checks" is the product's
word on its landing page and the primary use, and the prompt keeps that
voice, but a list of checks with no verdict in half of them would look
like half of them failed. "Chat" names the interaction and nothing about
phones. "Sessions" was the plain choice and would have done, except that
the word already has two jobs here and would have needed a qualifier in
every sentence that mentioned both.

Those two jobs keep the word, under names that tell them apart. The
connection an agent drives a device through is an **API session**:
WebDriver BiDi today, opened with `session.new` at the station's
`/session` endpoint, and tomorrow perhaps a full-duplex audio call to a
phone, which is no less a session for having no BiDi in it. The rig's
policy also limits the length of a session, meaning the span of a hold,
whoever holds it, and that is what the Devices page means when it says a
host records sessions. A jam takes a hold and drives the device through
an API session, so it is one of the rig's sessions and contains one of
the API's. In this document a session with no qualifier is the
lower-level kind, and the thing this spec adds is always a jam.

The page does not ask whether a prompt is a check or an errand. The agent
infers it from the phrasing, as the CLI's two commands do by being two
commands, and the jam's header says which it took: checking, or doing.

## How a jam goes

A person types a prompt and sends it. The station:

1. Makes a jam record with an id, the prompt, the person, the device
   chosen (or the only one), and the time.
2. Mints a token for the jam: control scope, this device only, this jam
   only, expiring with the jam's time limit. The token never reaches the
   browser.
3. Starts the jam's box with the token, the station's address and the
   provider settings. The box connects to the station's `/session` with
   the token, which takes a hold on the device under the person's
   identity, with the jam named in the hold's subject, so the Devices
   page says who is driving and the rig's recording, when the rig
   records, lands against that hold.
4. Connects to the box's own endpoint, starts a recording, and sends the
   claim or the goal.
5. Streams what comes back to the page: each step the agent took, each
   screenshot it looked at, each tool call and its answer, and at the
   end the verdict or the goal's completion, or an error.
6. When the result arrives, stops the recording with upload, closes the
   session, which releases the hold, destroys the box, and stores the
   transcript with the jam record and the recording's SHA-256.

The page shows the transcript as it arrives, and beside it the device
viewer in watching mode, which the station already does for a device
someone else holds. The person sees the phone move as the agent drives
it, and can press Hold to take the device back, which ends the jam.

## The runner

The runner is an interface with one job: start a jam and stream its
events. Behind it there is always the same program in the same kind of
box, a container most of the time, and only the launcher differs.

**The box is the Vibium binary alone.** `vibium check`, `vibium run`,
the agent loop behind them, the model clients for every provider Vibium
supports and the `ai.env` loader are all inside the one Go binary that
the CLI is; the Node package is an SDK that sends that binary BiDi
commands. A jam therefore needs no Node, no Python and no model library
of its own. The image is a static binary and the CA certificates, a few
tens of megabytes with nothing else to patch. Writing a fresh loop
around an OpenAI-compatible client would duplicate the agent and break
the second goal, since the page and the CLI could then disagree on a
verdict.

**The station sits in the CLI's chair.** The binary is started as
`vibium daemon start --connect wss://<station>/session`, with the jam's
token as its connect header and the provider settings in its
environment, which is how it reaches a phone from a laptop today. It
then serves its own BiDi endpoint inside the box, and the station
connects to that as a client, exactly as the CLI does: `session.new`,
start a recording, `vibium:check.run` with the claim or
`vibium:run.run` with the goal, stop the recording with upload, read
the result. A follow-up from the person is another command on the same
session. The WebSocket the box already speaks is the small server
between the two, and it is the one transport that works wherever the
box runs, where stdin and stdout would not reach a Sprite. The
station's BiDi client exists already in `station-probe` and becomes a
package.

**Where the box runs** is the launcher's choice, and there are three:

- *A container on the station's host*, when the station is on a Pi or
  a PC. Docker or podman starts the image with no volumes, no devices,
  a read-only root, every capability dropped, a memory limit and
  network egress only, and removes it when the jam ends. Rootless
  podman is preferred, because a Docker socket is root on the host and
  the station should not hold that.
- *A Sprite on Fly*, when the station is on Fly, where a container
  cannot start a container. The station asks Fly for a Sprite (or a
  Machine) from the same image on its private network, with a token
  scoped to that app kept as a station secret, reaches the box's
  endpoint over that network, and destroys it when the jam ends. This
  serves a hosted station too, where many jams run at once and no two
  share a process.
- *A plain subprocess*, where there is no container runtime: the same
  binary under the station's own user. The least isolation, and enough
  for a single-player station on a laptop.

The rig is not a launcher. It has the phone at the shortest distance,
but it is the operator's machine and the final gate: it checks grants
and refuses what its policy does not allow, and it may belong to
someone other than the person typing the prompt. A customer's model key
and prompt do not go there.

The contract between the station and a box:

- Input, at start: the station's `/session` URL, the jam's token, and
  the provider settings as the `VIBIUM_AI_*` variables and the
  provider's key variable, in the box's arguments and environment.
- Output: the box's BiDi endpoint, from which the station reads the
  result and, once the binary emits them, progress events; and the
  recording, which the box uploads itself with the presign flow the
  Record Store already has, under the jam's token, so the Records page
  lists it against the jam's hold beside the rig's own recording of the
  same hold.
- Control: the station closes the session and destroys the box when
  the result is in, the person takes the device back, the time limit
  passes, or the person signs out.

## Provider settings

Jams are bring your own keys. Settings gains an **AI** section where
the owner picks a provider from the ones Vibium's CLI knows (OpenAI,
xAI, Anthropic, Google Gemini, an OpenAI-compatible server, or a local
one), names the model, pastes the provider's API key, and can set the
base URL and the reasoning effort where the provider takes them. Saving
runs `vibium ready ai` with those values and shows its result on the
page, so a wrong key or an unknown model is seen there and not in a
failed jam. The key is seen once, as it is typed, and never again: the
page says that one is set, offers Replace and Remove, and nothing a
browser receives afterwards contains it.

The station keeps the settings in Vibium's own format, an `ai.env` file
in its data directory written with mode 0600 like `station.key`, holding
`VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL`, the optional `VIBIUM_AI_BASE_URL`
and `VIBIUM_AI_REASONING_EFFORT`, and the provider's key variable
(`OPENAI_API_KEY`, `XAI_API_KEY`, `ANTHROPIC_API_KEY` or
`GOOGLE_API_KEY`). It is the same file `vibium config init` writes for a
person, so the box needs nothing new: the station hands the file's
variables to the box as its environment, whichever launcher starts it,
and the binary behaves exactly as it would on a laptop. Whatever the page
leaves unset, the station host's environment fills in, so a deployment
that sets the key as a platform secret works without typing it into
the page. Changing the settings is an owner action and goes in the
audit table like making a token, without the key.

In single-player Station there is one owner and so one set of keys. A
hosted station is bring your own keys per account, with the same
section on each account's settings, the file replaced by a per-account
store, and each Sprite receiving only its own account's variables.

## Pages and API

The root page, `/`, becomes the Jams page: the prompt box, sized and
placed as the chat products place theirs rather than as a hero, a
device picker beside it when more than one device is online, and under
it the list of past jams, with when, who, the device, the prompt, and
the verdict where there is one. The list of devices moves to `/devices`, and the docs that
point at it follow. The top bar reads Jams, Devices, Records, Settings,
and the brand keeps linking to the root. `/jams` serves the same page as
the root, so the list has a name of its own, and one jam lives at
`/jams/{id}`, with the transcript, the watching viewer while the jam is
live, and the recording's Play when it is done. A station with no model
configured still lands on the prompt, which says so and points at
Settings, so that the first screen is what the station is for.

The API follows the pages, under the token model R5 built:

| Method and path | What it does |
| --- | --- |
| `POST /v1/jams` | Starts a jam: `prompt`, `device`, and `mode` when the caller wants to force check or run. Needs control. Returns the jam with its id. |
| `GET /v1/jams` | The caller's jams; every jam for an admin. |
| `GET /v1/jams/{id}` | One jam with its transcript and the recording's SHA-256. |
| `GET /v1/jams/{id}/events` | The event stream, as server-sent events, from the start of the jam; a finished jam replays and closes. |
| `POST /v1/jams/{id}/stop` | Ends the jam and releases the hold. |

A read-only token sees jams and their events and starts none. The API
session routes are untouched: `wss /session` and the per-device BiDi
route stay what they are, and an agent that opens an API session
directly is not a jam and appears on no list but the Devices page, as
today.

## Holds, limits and cost

A jam is a hold, so the rig's limits apply unchanged: the scopes the
policy allows, the hours, the longest session the policy permits. Two of
the station's own rules need care. The idle rule releases a hold
untouched for five minutes, and an agent waiting on a model can sit
longer than that between inputs, so the box's events count as
activity, and a jam with no event for the idle time is stopped rather
than silently losing its phone. And a jam has a time limit of its own,
thirty minutes by default, which the token's expiry enforces even if the
box hangs.

Model cost is the person's, on their own key. The first milestone
records the tokens the provider reports in the jam's transcript, so the
list can show what a jam cost, and sets no budget. A budget per jam and
per day is the hosted station's concern.

## Security

- The model key is a secret the owner typed. It lives in `ai.env` in
  the data directory, readable by the station's user alone, goes to the
  box as environment and nowhere else, and never appears in the
  database, in a page after it is typed, in a log or in a URL.
- The box has nothing but the jam: no volumes, no devices, a read-only
  root, no capabilities, egress only, a memory limit, and a life no
  longer than the jam. The key reaches it as environment and dies with
  it.
- The launcher's own credential is the one new privilege. A Docker
  socket on a host is root, so podman runs rootless where it can, and
  the Fly token for Sprites is scoped to the one app and kept as a
  station secret, never in the config file.
- A jam's token is scoped to one device and one jam and expires with
  the jam; it travels in a header, never a URL, like every other token.
- Page content and screenshots the agent reads are data from the device,
  not instructions from the person; the agent's own prompt separates the
  two, and the station does not add the device's output to anything it
  signs.
- The rig checks the jam's grant like any grant. A box that misbehaves
  can do no more than the hold allows, and the rig's recording, when
  on, records it.
- The transcript may contain what the screen showed. It is stored with
  the jam's visibility: the person who started it and admins, like a
  private recording.

## Milestones and acceptance

- [ ] **R8 Jams in a container**: the Jams page at the root with the
  prompt, the Devices page at `/devices`, the AI section on Settings
  with provider, model and key and the readiness check, the image, the
  container launcher (Docker or podman) with the subprocess launcher as
  the fallback, the station's BiDi client as a package, the transcript
  with the watching viewer beside it, the recording in the Record
  Store. Gate: a claim typed on the dev Pi's station starts a
  container, drives the phone through the station, the verdict shows
  on the page, the jam's recording plays on the Records page, and the
  container is gone afterwards. The same claim through `vibium check`
  from a laptop gives the same verdict.
- [ ] **R9 Jams on Fly**: the Sprite launcher behind the same
  interface, chosen by a flag, with the Sprite destroyed after every
  jam. Gate: the R8 gate passes on the Fly test station, with the jam
  in a Sprite and nothing of it in the station's own machine.

## Open questions

- [ ] Progress events. `vibium check` and `vibium run` answer once, at
  the end, with the verdict and the evidence, and emit nothing on the
  way, so the transcript that fills as the agent works needs the
  binary to send progress as BiDi events during a check or a run,
  which is a Vibium change like the upload option. The events are per
  step and come from the binary's own loop, not from the provider:
  the model APIs are one request and one reply per turn, with the
  reply streamed in pieces at most, and no channel back while the
  model works, so the binary is the only party that knows when a step
  starts, what it asked, what came back and what it did. Until then
  the page shows the viewer's live picture and the final result.
- [ ] A prompt that names two devices ("on the phone and the laptop").
  The first milestone takes one device per jam.
- [ ] Docker or podman on the dev Pi and in the self-host guide, and
  whether the station pulls the image itself or expects it present.
- [ ] The Vibium branch with `vibium record stop --upload`, which the
  box's upload leans on, is not merged.
- [ ] Follow-up turns: whether a second prompt on the same session
  should carry the first exchange as the `context` that
  `vibium:check.run` accepts, or start fresh.
- [ ] Whether a jam's hold should be refused by rigs that do not allow
  recording, or should simply run unrecorded like any hold.
- [ ] Whether a station with no model configured should land on Devices
  until one is, rather than on the prompt with a pointer to Settings as
  written above.
