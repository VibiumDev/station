# Jam Sessions: a prompt on the station

Nov 6, 2026 · draft for review

## Summary and goals

The station's front page, once the owner has set up a model, is a
prompt box, the kind ChatGPT and Claude open on: a few lines tall,
wide, growing with what is typed, with a send button in its corner. A
person signs in, reads "What can I check for you?" above it, types a
claim or an errand, and an agent drives a device on the station to do
it, while the page shows the agent's steps and the device being driven.
When the agent is done the answer sits in the transcript, and the
recording of what it did sits in the Record Store, playable from the
Records page like any other. Each of these is a jam session, a jam for
short.

The point is a station that can show what it is for on its own. Most
use of a station is an agent on a laptop or in a pipeline, Claude Code,
Codex or the Vibium CLI, driving a device headlessly through `/session`
with a token, and nothing here changes for them. A jam is for the
other case: a demo, a quick question about a phone, a first try after
enrolling a rig, where there is no laptop set up and the station itself
should be enough. That sets the ambition. The agent is small, the
first milestone is modest, and anything a remote agent already does
better stays with the remote agent.

The agent is what a person does today when they point Claude Code or
Codex at a phone through Vibium: a model, a loop, and Vibium's tools.
The station supplies the loop, a few hundred lines of Go, and the model
is one the owner set up on Settings, picked at the prompt when there is
more than one. The tools are Vibium's own, served by `vibium mcp`
running beside the station and connected to the station's `/session`
with the jam's token, so a jam reaches the phone the way every other
agent does, under a hold the rig checks. Nothing is added to Vibium
for a jam's sake, and the agent's key never reaches Vibium.

The goals, in order:

1. **One prompt, your keys.** Once the owner has set up a model and
   its key on Settings, anyone who can sign in to the station can type
   a sentence and watch a phone act on it. More models are more choices
   at the prompt, never more setup for the person typing.
2. **The same tools as the CLI and MCP.** A jam does what Claude Code
   does with `vibium mcp` on the same phone, through the same tools
   with the same schemas, so a step that works from a laptop works from
   the page, and when a host with a document model appears, a browser
   VM or an emulator, Vibium's tools for it come along. Hearing and
   speaking come the same way, once Vibium has `listen` and `say`.
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

The page does not ask whether a prompt is a check or an errand. The
loop offers the model two result tools, one that delivers a verdict
and one that delivers a result, and the model calls the one that fits
the prompt, which is the whole of the inference; the jam's header then
says which it took: checking, or doing. A caller who wants to force it
can, through the API's `mode`.

## How a jam goes

A person types a prompt and sends it. The station:

1. Makes a jam record with an id, the prompt, the person, the device
   chosen (or the only one), the agent model chosen (or the default),
   and the time.
2. Mints a token for the jam: control scope, this device only, this jam
   only, expiring with the jam's time limit. The token never reaches the
   browser.
3. Starts `vibium mcp` as a subprocess with the station's own
   `/session` URL and the jam's token in its environment, the way the
   tutorials set `VIBIUM_CONNECT_URL` and `VIBIUM_CONNECT_API_KEY` for
   a laptop, speaks MCP to it over its standard input and output, and
   has it open the session. That takes a hold on the device in the jam
   token's name, so the hold's subject names the jam while the jam
   record names the person. The Devices page then shows the jam as the
   holder, the rig's recording, when the rig records, lands against that
   hold, and because the hold belongs to a token and the owner is a
   person, the owner's Hold button can take the device back.
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

A follow-up that asks for another check or goal ends with a result of
its own, and one that asks nothing of the device, a question or a
thanks, is answered in words. The jam's result, in its header and on
the list, is its first prompt's: a later result of the same kind
replaces it, as when a check is run again after a fix, and one of the
other kind shows in the transcript alone, so a check that passed is
not reported as an errand because the person said "that's it" after.

The agent leaves the device as it found it. The instruction asks the
model to close the browser tabs it opened and return to the home
screen before it delivers a result, and when a jam ends on its own,
after its idle wait or at a limit, the loop presses the device's Home
shortcut, the one the rig announces for the viewer's toolbar, so a
model that forgot still leaves the device on its home screen. A jam a
person stopped, or whose device was taken, leaves the screen as it is,
since the person is about to look at it. The instruction names the
device's shortcuts in Vibium's key syntax, Home is Meta+h and Back is
Escape on an Android phone through Roadie, rather than key names a
HID keyboard reads differently.

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
is the same table Vibium documents. A jam has one agent model, the one
picked at the prompt, and that is the only model the loop talks to.
The models for hearing and speaking go to Vibium, which makes those
calls itself, below.

**Vibium's tools** come through `vibium mcp`. The station is the
MCP client, over standard input and output, and asks the server for its
tool list at the start of each jam. Vibium lists every tool it has
whatever it is connected to, so the loop narrows the list by the
device's kind, which the station knows from the rig's announcement: on
a phone the model gets screenshot, tap, long press, swipe, keys, type
and the few others a screen allows, and on a future host with a
document model it would get the map, find, click and fill set the CLI
uses on a browser. Session, recording and file tools stay out of the
model's reach on every kind, as they do in Vibium's own check. Vibium
is a static Go binary, so the station host needs one file beside the
station, which the self-host guide and the Fly image add; the station
finds it on the path or by a flag, and the Settings page says when it
is missing.

**Ears and a voice** are Vibium's too. Vibium is getting `listen`, a
command that hears the device and returns what was said, and `say`,
one that speaks a sentence into it, each calling a speech model of its
own, and both will be tools in `vibium mcp` like tap and screenshot.
A jam gets them the way it gets every other tool, and the station adds
no tool of its own. What the two commands ask of a remote device goes
over the API session as a BiDi command, which Roadie answers from the
phone's speaker and into its microphone, the same sound the viewer's
audio tracks carry, and the station's relay forwards the bytes without
looking, as it does for a screenshot. The models the two commands call
are set on Settings like the agent's, under their own roles, and the
station puts their variables in the subprocess's environment under the
names Vibium gives them, so Vibium makes those calls with its own
client and the loop never sees sound at all: a listen returns text, and
every agent model reads text. The tools appear only when the role's
model is set and the device announces audio, and the example prompts
about sound stay hidden with them.

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
WebSocket and no secret of the agent's: that key stays in the loop, and
Vibium gets the jam's token, which the hold already bounds, plus the
speech models' keys when it hears and speaks. A box would earn its
keep only for an agent that is a large runtime of someone else's, and
that agent is better run where it lives, remotely, through `/session`.

The rig is never the agent. It has the phone at the shortest distance,
but it is the operator's machine and the final gate: it checks grants
and refuses what its policy does not allow, and it may belong to
someone other than the person typing the prompt. A customer's model key
and prompt do not go there.

## Provider settings

Jams are bring your own keys. Settings gains an **AI** section that
opens with what it is for, a model turns on Jams, and holds a list of
**models**. Each has a name of the owner's choosing, a role, a provider
(OpenAI, xAI, Anthropic, Google Gemini, an OpenAI-compatible server, or
a local one), the provider's name for the model, the provider's API
key, and where the provider takes them a base URL and a reasoning
effort. The role says what a model is for. An **agent** model is a
jam's brain: it reasons, sees the screenshots and calls the tools, and
one is picked at jam start. A **listen** model turns the phone's sound
into text for `vibium listen`, and a **say** model turns text into
speech for `vibium say`: OpenAI's audio endpoints, a local whisper or
TTS server, or anything else Vibium's client reaches, since Anthropic
has neither, so a Claude demo that listens needs a second key. A role
for live audio is reserved at the end. Each role has a default, the
one model of that role until the owner marks another. Adding a model sends one small
request with its values and shows the answer on the page, so a wrong
key or an unknown model is seen there and not in a failed jam. The key
is seen once, as it is typed, and never again: the row says that one is
set, offers Replace and Remove, and nothing a browser receives
afterwards contains it. The section also holds the Jams switch, on by
default once an agent model is set, which is what puts Jams in the top
bar and the prompt on the front page.

With one agent model the prompt has no picker and the person typing
sees none of this. With two or more, a picker beside the prompt names
them with the default selected, so a demo can run the same prompt on
the same phone with GPT, Claude, Gemini and a local model, and the
jam's header and the list say which one did it. A jam keeps its model
through its follow-up turns.

The station keeps each model in Vibium's own format, one file per model
in an `ai` directory under the data directory, written with mode 0600
like `station.key`, holding `VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL`, the
optional `VIBIUM_AI_BASE_URL` and `VIBIUM_AI_REASONING_EFFORT`, and the
provider's key variable (`OPENAI_API_KEY`, `XAI_API_KEY`,
`ANTHROPIC_API_KEY` or `GOOGLE_API_KEY`), plus the role and the default
mark in two variables of the station's own, which Vibium's loader skips
because it reads only the names it knows. Each file is what `vibium
config init` writes for a person, so one copied from a laptop under a
name works as it is. When the station host's environment carries
Vibium's variables, as a deployment that sets the key as a platform
secret does, they show as one more agent model named `host`, which the
page lists and cannot edit, and which is the default when no file is.
Changing a model is an owner action and goes in the audit table like
making a token, without the key.

Single-player Station has one owner and so one list, the station's,
and everyone who can start a jam picks from it. That list is also what
a team sharing a station on a tailnet needs to keep separate bills: two
people add their own keys as two named models and each picks their own
at the prompt, seeing the other's name and never the key. The second
level, a person's own models offered before the station's with the
person's default winning per role, is the shape for when Station has
accounts, which is a hosted station today and Station itself the day
it grows from single-player to a team: people who know each other on
one station, not strangers on a service. It is written down here so
the store gets a subject column from the start, and it is built then.

## Pages and API

`/` is the prompt box alone once an agent model is set and Jams is on,
as the chat products open, and a redirect to `/devices` otherwise, so
the front page is whichever the station is for right now. The box is
sized and placed as the chat products place theirs rather than as a
hero, with a device picker beside it when more than one device is
online and a model picker when more than one agent model is set, and
two or three example prompts under it for the device's kind and the
tools it has, which a demo can click and a first-time visitor can learn
from. The Jams page, at `/jams`, is the list of past jams, with when,
who, the device, the model, the prompt, and the result where there is
one. The list of devices moves to `/devices`, and the docs that point
at it follow. The sidebar reads New jam, Jams, Devices, Records,
Settings, and the brand keeps linking to `/`. One jam lives at `/jams/{id}`, with the transcript,
the prompt box for the next turn while the jam is open, the watching
viewer while it is live, and the recording's Play when it is done.

Jams exist only once an agent model is set. Until then the station
looks as it does today, `/` sending to `/devices` and no Jams in the
sidebar, and the only mention is where the owner will act on it: the AI
section on Settings opens with a line saying that a model turns on
Jams, a prompt on the front page that drives the station's devices. The
owner is on Settings before anything can be jammed, to enroll a rig, so
the line is seen in time without being shown to anyone who cannot act
on it. Adding the first agent model turns Jams on and puts the prompt
on `/`; the switch beside the list turns Jams off again without
forgetting the keys, for a station that is only a broker and wants its devices on
the front page. While Jams is off, its pages and its API answer 404
with a line saying so. A greyed-out prompt with a polite note was the
alternative, and it would sit on the front page of every station that
never means to jam, apologising to people who cannot fix it.

The API follows the pages, under the token model R5 built:

| Method and path | What it does |
| --- | --- |
| `POST /v1/jams` | Starts a jam: `prompt`, `device`, `model` (an agent model's name; the default when absent), and `mode` when the caller wants to force check or run. Needs control. Returns the jam with its id. |
| `GET /v1/jams` | The caller's jams; every jam for an admin. |
| `GET /v1/jams/{id}` | One jam with its model, its transcript and the recording's SHA-256. |
| `GET /v1/models` | The models set on Settings, by name and role, so a caller knows what to pass. Never a key. |
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
minutes and a hundred actions by default, which the loop enforces and
the token's expiry backs even if the loop hangs. A listen or a say
counts as an action, and its seconds count against the clock.

Model cost falls on the key the model was set up with. The first
milestone records the tokens the provider reports in the jam's
transcript, so the list can show what a jam cost, and sets no budget. A budget per jam
and per day is the hosted station's concern.

## Recordings

A jam's recording is the rig's recording of its hold, which the Record
Store already ties to the hold and the Records page already lists, so
the jam's page links to it with nothing new. It is the stronger
recording in any case: it shows what the phone showed and received, and
the agent cannot alter it. The transcript, with the model's text, the
tool calls and the token counts, stays in the station's database with
the jam, and the jam's page shows the two side by side. A rig that does
not record leaves a jam with its transcript alone, which is an open
question below until the loop records for itself.

## Security

- A model's key is a secret the owner typed. It lives in the model's
  file in the `ai` directory, readable by the station's user alone, and
  never appears in the database, in a page after it is typed, in a log
  or in a URL. The agent model's key is read by the loop, sent only to
  that provider's endpoint over HTTPS, and never reaches Vibium's
  environment. The listen and say models' keys go into Vibium's
  environment, since Vibium makes those calls, and nowhere else.
- The jam's token is scoped to one device and one jam and expires with
  the jam; it travels in a header, never a URL, like every other token,
  and it is the only credential of the station's that Vibium receives.
- What the screen shows, and what the speaker plays, is data from the
  device, not instructions from the person. The system instruction
  says so, the loop puts every observation, a screenshot or a
  transcript of sound, in a user turn marked untrusted, as Vibium
  does, and the station adds nothing from the device to anything it
  signs.
- The loop offers the model no tool beyond the device tools, Vibium's
  and the station's: no session control, no recording control, no
  files, no shell. The
  password guard is the instruction and the result tool, not a probe,
  since a screen has no fields to inspect.
- The rig checks the jam's grant like any grant. A loop that misbehaves
  can do no more than the hold allows, and the rig's recording, when
  on, records it.
- The transcript may contain what the screen showed. It is stored with
  the jam's visibility: the person who started it and admins, like a
  private recording.

## Milestone and acceptance

- [ ] **R8 Jams on a phone**: the prompt and the example prompts on
  `/`, the list of jams at `/jams`, the Devices page at `/devices`, `/`
  sending to the devices while Jams is off, the AI section on Settings with the list of
  models, the readiness request on each and the Jams switch, the model
  picker when more than one agent model is set, the `jam` package with
  the loop, the OpenAI-compatible client and the MCP client, `vibium
  mcp` beside the station on the Pi and in the Fly image, the event
  stream, the transcript with the watching viewer beside it, follow-up
  turns, the rig's recording linked from the jam. Gate: a claim typed
  on the page drives the phone through the station, each step shows as
  it happens, the verdict shows at the end, a follow-up prompt
  continues on the same phone, the same claim run under a second model
  says which one did it, and the jam's recording plays from the jam's
  page and the Records page. The same on the Fly test station, with
  nothing changed but the address.
- [ ] **R9 Jams can listen**: once Vibium has `listen` and Roadie
  answers it. The listen role on Settings, its variables passed to the
  subprocess, the tool offered when a listen model is set and the
  device announces audio, an example prompt that uses it, and the
  transcript showing what was heard. Gate: "Open YouTube, play the
  first video and tell me what is said in its first ten seconds" typed
  on the page plays the video on the phone, the jam listens, and the
  result quotes what it heard; the same with a local whisper server as
  the listen model.
- [ ] **R10 Jams can speak**: once Vibium has `say`. The say role, the
  tool, and an example prompt that talks to the phone. Gate: "Ask the
  phone's assistant what time it is and tell me what it answers" says
  the question into the phone's microphone, the assistant answers out
  loud, the jam listens, and the result quotes the answer.

## Later, if wanted

None of these is in R8, R9 or R10, and each is a separate decision.

- A **realtime** role: a model that takes and returns audio live,
  which is a session of a different shape than the loop and the
  full-duplex call the naming section leaves room for.
- An audio-native agent model hearing the sound itself instead of a
  transcript of it.
- A person's own models beside the station's, as the provider settings
  describe, when Station has accounts.
- The loop recording for itself, in Vibium's format, so a jam on a rig
  that does not record still leaves a recording, and so the model's
  text, and the words a listen heard, can sit beside the pictures in
  the Record Player. The rig's recording can carry the sound, but the
  words come back from the speech model to Vibium and the station and
  never reach the rig. Roadie already writes the format, so the writer
  would become a package both share.
- Native clients for providers whose OpenAI-compatible endpoints fall
  short.
- A budget per jam and per day, for a hosted station.
- Vibium's own `check` learning a screen, so a jam's verdict and the
  CLI's are the same code. A Vibium decision, not the station's.
- A full agent as the jam's brain, Claude Code or Codex in a container
  or a Sprite, speaking the same event stream to the same page. Noted
  because it was considered; the framing above says such an agent is
  better run where it lives.

## Open questions

- [ ] Whether the OpenAI-compatible endpoints Anthropic and Google
  serve carry everything the loop sends, tool calls with image inputs
  in particular, or whether those two need native clients from the
  start. A demo with Claude depends on the answer.
- [ ] What `vibium mcp` does on its first tool call when connected to
  a station: which tool opens the session, and whether the hold is
  taken then or at start. The first day of R8 settles it.
- [ ] What `vibium listen` and `vibium say` ask of a remote device
  over BiDi, which Roadie then answers from the speaker and into the
  microphone, and under which variable names Vibium takes their
  models, which the station passes through. Both are Vibium's to
  decide; the station follows.
- [ ] A prompt that names two devices ("on the phone and the laptop").
  The first milestone takes one device per jam.
- [ ] Whether the station should download the Vibium binary itself
  when it is missing, pinned to a version it was tested with, or leave
  that to the self-host guide and the image.
- [ ] Whether a jam should be refused on a rig that does not record,
  or run with its transcript alone, until the loop records for itself.
