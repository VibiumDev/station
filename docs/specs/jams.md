# Jam Sessions: a prompt on the station

Nov 6, 2026 · draft for review

## Summary and goals

The station's front page is a prompt box, the kind ChatGPT and Claude
open on: a few lines tall, wide, growing with what is typed, with a
send button in its corner. A person signs in, reads "What can I check
for you?" above it, types a claim or an errand, and an agent drives a
device on the station to do it, while the page shows the agent's steps
and the device being driven. When the agent is done the answer sits in
the transcript, and the recording of what it did sits in the Record
Store, playable from the Records page like any other. Each of these is
a jam session, a jam for short.

The agent is what a person does today when they point Claude Code or
Codex at a phone through Vibium: a model, a loop, and Vibium's tools.
The station supplies the loop, a few hundred lines of Go, and the model
is the one whose key the owner entered on Settings. The tools are
Vibium's own, served by `vibium mcp` running beside the station and
connected to the station's `/session` with the jam's token, so a jam
reaches the phone the way every other agent does, under a hold the rig
checks. Nothing is added to Vibium, and nothing in Vibium sees the key.

The goals, in order:

1. **One prompt, one key.** Once the owner has picked a model provider
   and pasted its key on Settings, anyone who can sign in to the
   station can type a sentence and watch a phone act on it.
2. **The same tools as the CLI and MCP.** A jam does what Claude Code
   does with `vibium mcp` on the same phone, through the same tools
   with the same schemas, so a step that works from a laptop works from
   the page, and when a host with a document model appears, a browser
   VM or an emulator, Vibium's tools for it come along.
3. **The agent is a conversation.** The loop keeps the exchange, so a
   second prompt continues where the first left off and where the
   phone is, and the transcript fills as the agent works, because the
   station is the loop and sees every step.
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
3. Starts `vibium mcp` as a subprocess with the station's own
   `/session` URL and the jam's token in its environment, the way the
   tutorials set `VIBIUM_CONNECT_URL` and `VIBIUM_CONNECT_API_KEY` for
   a laptop, and speaks MCP to it over its standard input and output.
   Vibium opens the session, which takes a hold on the device under the
   person's identity, with the jam named in the hold's subject, so the
   Devices page says who is driving and the rig's recording, when the
   rig records, lands against that hold.
4. Runs the loop: sends the model the instruction, the prompt and the
   tools Vibium offers for this device; executes each tool call the
   model returns through Vibium; takes a screenshot after each action
   and sends it back as the observation; repeats until the model
   delivers a verdict or a result, or a limit stops it.
5. Shows each step on the page as it happens: the tool call and its
   arguments, the observation, the screenshot, the model's visible
   text, and at the end the verdict or the goal's completion, or an
   error.
6. When the jam ends, stops Vibium, which closes the session and
   releases the hold, and stores the transcript with the jam record and
   the hash of the recording.

The page shows the transcript as it arrives, and beside it the device
viewer in watching mode, which the station already does for a device
someone else holds. The person sees the phone move as the agent drives
it, and can press Hold to take the device back, which ends the jam. A
second prompt while the jam is open continues the same conversation
with the phone where the agent left it; a prompt after the jam has
ended starts a new one.

## The agent

The agent has three parts, and only the first is new code.

**The loop** is a package in the station, `jam`, of the shape every
tool-calling agent has: a conversation, a tool list, a model request, a
tool call, an observation, again. It carries the system instruction
that Vibium's check and run carry today, which names the claim or the
goal, forbids credentials, purchases and irreversible actions, treats
what the screen shows as evidence and never as instructions, and ends
with one call to a result tool that delivers passed, failed or
inconclusive with evidence for a check, or completed or not completed
for an errand. It keeps the two most recent screenshots attached and
detaches the rest, as Vibium does, so the request does not grow with
every step. It emits an event for each thing it does, which is what the
page shows and what the transcript stores. And it holds the
conversation for the jam's life, so a follow-up is one more user turn.

**The model client** speaks the OpenAI-compatible protocol, chat
completions with tool calls and image inputs, over HTTPS to the base
URL the provider settings name. That one protocol reaches OpenAI, xAI,
local servers and any OpenAI-compatible endpoint directly, and reaches
Google and Anthropic through the compatible endpoints each of them
serves; where a compatible endpoint turns out to lack something the
loop needs, a native client for that provider follows. The settings use
Vibium's names, `VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL`,
`VIBIUM_AI_BASE_URL`, `VIBIUM_AI_REASONING_EFFORT` and the provider's
key variable, so a person's existing `ai.env` carries over, and the
provider table the station keeps (name, default base URL, key variable)
is the same table Vibium documents.

**The tools** are Vibium's, through `vibium mcp`. The station is the
MCP client, over standard input and output, and asks the server for its
tool list at the start of each jam. Vibium lists every tool it has
whatever it is connected to, so the loop narrows the list by the
device's kind, which the station knows from the rig's announcement: on
a phone the model gets screenshot, tap, long press, swipe, keys, type
and the few others a screen allows, and on a future host with a
document model it would get the map, find, click and fill set the CLI
uses on a browser. Session, recording and file tools stay out of the
model's reach on every kind, as they do in Vibium's own check. Vibium is a static Go binary, so the
station host needs one file beside the station, which the self-host
guide and the Fly image add; the station finds it on the path or by a
flag, and the Settings page says when it is missing.

Why not Vibium's own `check` and `run`, which are the same loop with the
same discipline? Because they were built for a browser the binary
launches itself: a live check refuses a remote connection outright, its
tools read the document and begin by mapping the page, which a phone
cannot answer, its limits are three minutes and two dozen actions,
compiled in, and it reports nothing until the verdict and keeps no
conversation. Teaching it a screen is a Vibium feature in its own right
and may be worth doing one day for the CLI's sake, at which point a jam
could use it for its verdicts and lose nothing. The page does not wait
for that.

Why not a container, or a Sprite on Fly? Because there is nothing left
to isolate. The loop is station code, the model is a web service, and
Vibium is a subprocess under the station's own user that holds a
WebSocket and no secret: the key stays in the loop, and Vibium gets only
the jam's token, which the hold already bounds. A box earns its keep
when the agent is a large runtime of someone else's, which is the later
case below, not this one.

The rig is never the agent. It has the phone at the shortest distance,
but it is the operator's machine and the final gate: it checks grants
and refuses what its policy does not allow, and it may belong to
someone other than the person typing the prompt. A customer's model key
and prompt do not go there.

## Provider settings

Jams are bring your own keys. Settings gains an **AI** section where
the owner picks a provider (OpenAI, xAI, Anthropic, Google Gemini, an
OpenAI-compatible server, or a local one), names the model, pastes the
provider's API key, and can set the base URL and the reasoning effort
where the provider takes them. Saving sends one small request with
those values and shows the answer on the page, so a wrong key or an
unknown model is seen there and not in a failed jam. The key is seen
once, as it is typed, and never again: the page says that one is set,
offers Replace and Remove, and nothing a browser receives afterwards
contains it.

The station keeps the settings in Vibium's own format, an `ai.env` file
in its data directory written with mode 0600 like `station.key`, holding
`VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL`, the optional `VIBIUM_AI_BASE_URL`
and `VIBIUM_AI_REASONING_EFFORT`, and the provider's key variable
(`OPENAI_API_KEY`, `XAI_API_KEY`, `ANTHROPIC_API_KEY` or
`GOOGLE_API_KEY`). It is the same file `vibium config init` writes for a
person, so one copied from a laptop works as it is. Whatever the page
leaves unset, the station host's environment fills in, so a deployment
that sets the key as a platform secret works without typing it into
the page. Changing the settings is an owner action and goes in the
audit table like making a token, without the key.

In single-player Station there is one owner and so one set of keys. A
hosted station is bring your own keys per account, with the same
section on each account's settings and the file replaced by a
per-account store, and each jam's loop reading only its own account's
settings.

## Pages and API

The root page, `/`, becomes the Jams page: the prompt box, sized and
placed as the chat products place theirs rather than as a hero, a
device picker beside it when more than one device is online, and under
it the list of past jams, with when, who, the device, the prompt, and
the verdict where there is one. The list of devices moves to `/devices`,
and the docs that point at it follow. The top bar reads Jams, Devices,
Records, Settings, and the brand keeps linking to the root. `/jams`
serves the same page as the root, so the list has a name of its own,
and one jam lives at `/jams/{id}`, with the transcript, the prompt box
for the next turn while the jam is open, the watching viewer while it
is live, and the recording's Play when it is done. A station with no
model configured still lands on the prompt, which says so and points at
Settings, so that the first screen is what the station is for.

The API follows the pages, under the token model R5 built:

| Method and path | What it does |
| --- | --- |
| `POST /v1/jams` | Starts a jam: `prompt`, `device`, and `mode` when the caller wants to force check or run. Needs control. Returns the jam with its id. |
| `GET /v1/jams` | The caller's jams; every jam for an admin. |
| `GET /v1/jams/{id}` | One jam with its transcript and the recording's SHA-256. |
| `POST /v1/jams/{id}/turns` | A follow-up prompt on an open jam. |
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
longer than that between inputs, so the loop's steps count as activity,
and a jam with no step for the idle time is stopped rather than
silently losing its phone. And a jam has limits of its own: thirty
minutes and a few hundred actions by default, which the loop enforces
and the token's expiry backs even if the loop hangs.

Model cost is the person's, on their own key. The first milestone
records the tokens the provider reports in the jam's transcript, so
the list can show what a jam cost, and sets no budget. A budget per jam
and per day is the hosted station's concern.

## Recordings

A jam leaves a recording in the Record Store, tied to its hold like any
other, and the Records page lists it against the jam. The rig's
recording of the hold, where the rig records, is the stronger one: it
shows what the phone showed and received, and the agent cannot alter
it. The loop also saves its own: each action with its arguments and the
screenshot that followed, in Vibium's recording format, which the
Record Player already plays and Roadie already writes, so the writer
becomes a package both share. That recording exists whether or not the
rig records, and it carries the model's text beside the pictures. The
transcript itself, with the token counts, stays in the station's
database with the jam.

## Security

- The model key is a secret the owner typed. It lives in `ai.env` in
  the data directory, readable by the station's user alone, is read by
  the loop and sent only to the provider's endpoint over HTTPS, and
  never appears in the database, in a page after it is typed, in a log,
  in a URL, or in Vibium's environment.
- The jam's token is scoped to one device and one jam and expires with
  the jam; it travels in a header, never a URL, like every other token,
  and it is the only credential Vibium receives.
- What the screen shows is data from the device, not instructions from
  the person. The system instruction says so, the loop puts every
  observation in a user turn marked untrusted, as Vibium does, and the
  station adds nothing from the device to anything it signs.
- The loop offers the model no tool beyond Vibium's device tools: no
  session control, no recording control, no files, no shell. The
  password guard is the instruction and the result tool, not a probe,
  since a screen has no fields to inspect.
- The rig checks the jam's grant like any grant. A loop that misbehaves
  can do no more than the hold allows, and the rig's recording, when
  on, records it.
- The transcript may contain what the screen showed. It is stored with
  the jam's visibility: the person who started it and admins, like a
  private recording.

## Milestones and acceptance

- [ ] **R8 Jams on a phone**: the Jams page at the root with the
  prompt, the Devices page at `/devices`, the AI section on Settings
  with provider, model and key and the readiness request, the `jam`
  package with the loop, the OpenAI-compatible client and the MCP
  client, `vibium mcp` beside the station on the Pi and in the Fly
  image, the event stream, the transcript with the watching viewer
  beside it, follow-up turns, the jam's own recording in the Record
  Store. Gate: a claim typed on the page drives the phone through the
  station, each step shows as it happens, the verdict shows at the
  end, a follow-up prompt continues on the same phone, and the jam's
  recording plays on the Records page. The same on the Fly test
  station, with nothing changed but the address.
- [ ] **R9 Bring your own agent**: a jam whose brain is not the
  station's loop but a full agent, Claude Code, Codex or `vibium check`
  once it runs on a screen, in a container on a Pi or a PC and a Sprite
  on Fly, speaking the same event stream to the same page. The box
  earns its keep here, because the runtime is large and someone
  else's. Gate: the R8 gate passes with the agent switched, and nothing
  of it runs in the station's own process.

## Open questions

- [ ] Whether the OpenAI-compatible endpoints Anthropic and Google
  serve carry everything the loop sends, tool calls with image inputs
  in particular, or whether those two need native clients from the
  start.
- [ ] A prompt that names two devices ("on the phone and the laptop").
  The first milestone takes one device per jam.
- [ ] Whether the station should download the Vibium binary itself
  when it is missing, pinned to a version it was tested with, or leave
  that to the self-host guide and the image.
- [ ] Whether Vibium's `check` should learn a screen, so that a jam's
  verdict and the CLI's are the same code; and until then, whether a
  jam's verdict should say it came from the station's loop.
- [ ] Whether a jam's hold should be refused by rigs that do not allow
  recording, or should simply run with the loop's own recording alone.
- [ ] Whether a station with no model configured should land on Devices
  until one is, rather than on the prompt with a pointer to Settings as
  written above.
