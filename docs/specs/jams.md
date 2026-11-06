# Jam Sessions: a prompt on the station

Nov 6, 2026 · draft for review

## Summary and goals

The station's front page is one big prompt. A person signs in, reads
"What can I check for you?", types a claim or an errand, and an agent
drives a device on the station to do it, while the page shows the
agent's steps and the device being driven. When the agent is done the
answer sits in the transcript, and the recording of what it did sits in
the Record Store, playable from the Records page like any other. Each of
these is a jam session, a jam for short.

The agent is Vibium's own. `vibium check` verifies a claim and ends with
a verdict; `vibium run` carries out a goal and ends when it is done. Both
reach a phone the way the agent tutorial already does, through a station
token and the relayed BiDi session, and both take their model from the
same provider settings every Vibium user has. The page puts a front on
that, and adds nothing to the agent itself.

The goals, in order:

1. **One prompt, no setup.** Anyone who can sign in to the station and
   has configured a model for Vibium once can type a sentence and watch
   a phone act on it.
2. **The same agent as the CLI.** The page runs what `vibium check` and
   `vibium run` run, so a claim behaves the same from a terminal, a
   script and the page, and the recording is the same recording.
3. **The work can run anywhere.** Where the agent process runs is a
   choice behind one interface: on the station's host first, in a
   sandbox per jam later, so a hosted station and a self-hosted one
   share the page and differ only in the runner.
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
2. Takes a hold on the device under the person's identity, with the jam
   named in the hold's subject, so the Devices page says who is driving
   and the rig's recording, when the rig records, lands against that
   hold.
3. Mints a token for the jam: control scope, this device only, this jam
   only, expiring with the jam's time limit. The token never reaches the
   browser.
4. Starts the runner with the prompt, the device, the token, the
   station's address and the provider settings, and reads its event
   stream.
5. Streams the events to the page: each step the agent took, each
   screenshot it looked at, each tool call and its answer, and at the
   end the verdict or the goal's completion, or an error.
6. When the runner ends, releases the hold, stores the transcript with
   the jam record, and links the jam to the recording the runner
   uploaded.

The page shows the transcript as it arrives, and beside it the device
viewer in watching mode, which the station already does for a device
someone else holds. The person sees the phone move as the agent drives
it, and can press Hold to take the device back, which ends the jam.

## The runner

The runner is an interface with one job: start a jam and stream its
events. Two implementations are planned, and the page cannot tell them
apart.

**The subprocess runner** starts `vibium check` or `vibium run` on the
station's own host, with the jam's token, the station's address and the
provider settings in its environment, and reads the CLI's event output.
It needs Node and the Vibium CLI installed beside the station, which the
self-host guide and the Fly image gain as an optional step. It is the
first implementation, because it is the least machinery, and it serves a
single-player station completely.

**The sandbox runner** starts a disposable machine per jam, a Fly
Machine or a Sprite with the Vibium CLI inside, gives it the same
environment, and reads the same stream back over the sandbox's
connection. The sandbox is destroyed when the jam ends. It serves a
hosted station, where prompts and keys must not share a process with
the station, and where many jams happen at once.

The rig is not a runner. It has Node and it holds the phone at the
shortest distance, but it is the operator's machine and the final gate:
it checks grants and refuses what its policy does not allow, and it may
belong to someone other than the person typing the prompt. A customer's
model key and prompt do not go there.

The contract between the station and a runner:

- Input: the prompt, the device id, the jam's token, the station's URL,
  and the provider settings as the `VIBIUM_AI_*` variables.
- Output: a stream of events in the order they happen, each with a
  time, a kind (step, screenshot, tool, verdict, done, error) and its
  content; and at the end, the SHA-256 of the recording the runner
  uploaded.
- Control: stop, which the station sends when the person takes the
  device back, the time limit passes, or the person signs out.

The recording is uploaded by the runner itself with the presign flow
the Record Store already has: the runner asks the station for an upload
authorization under its jam token, and sends the zip. The station marks
the file with the jam's hold, so the Records page lists it against the
jam as it lists the rig's own recording of the same hold.

## Provider settings

The runner reads the model from the same place the CLI does: the
`VIBIUM_AI_PROVIDER`, `VIBIUM_AI_MODEL`, `VIBIUM_AI_BASE_URL` and
`VIBIUM_AI_REASONING_EFFORT` variables and the provider's own key
variable, with `~/.config/vibium/ai.env` filled in for whatever is
unset, as `vibium config init` writes it. The station adds nothing of
its own in the first milestone: the settings are the station host's
environment and that file, and a person who has run `vibium check` on
the host has configured the page.

Settings gains an **AI** section that shows the provider and model in
use and the result of Vibium's readiness check, so a missing key is seen
there and not in a failed jam. The key itself is never shown and never
sent to a browser. A hosted station will want settings per account,
which is the sandbox runner's milestone to bring.

## Pages and API

The root page, `/`, becomes the Jams page: the prompt, a device picker
when more than one device is online, and under it the list of past
jams, with when, who, the device, the prompt, and the verdict where
there is one. The list of devices moves to `/devices`, and the docs that
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
longer than that between inputs, so the runner's events count as
activity, and a jam with no event for the idle time is stopped rather
than silently losing its phone. And a jam has a time limit of its own,
thirty minutes by default, which the token's expiry enforces even if the
runner hangs.

Model cost is the person's, on their own key. The first milestone
records the tokens the provider reports in the jam's transcript, so the
list can show what a jam cost, and sets no budget. A budget per jam and
per day is the hosted station's concern.

## Security

- The model key stays where it is: in the host's environment or the
  sandbox's, never in the station's database and never in a page.
- A jam's token is scoped to one device and one jam and expires with
  the jam; it travels in a header, never a URL, like every other token.
- Page content and screenshots the agent reads are data from the device,
  not instructions from the person; the agent's own prompt separates the
  two, and the station does not add the device's output to anything it
  signs.
- The rig checks the jam's grant like any grant. A runner that
  misbehaves can do no more than the hold allows, and the rig's
  recording, when on, records it.
- The transcript may contain what the screen showed. It is stored with
  the jam's visibility: the person who started it and admins, like a
  private recording.

## Milestones and acceptance

- [ ] **R8 Jams on the host**: the Jams page at the root with the
  prompt, the Devices page at `/devices`, the subprocess runner, the
  event stream, the watching viewer beside the transcript, the AI
  section on Settings, the recording in the Record Store. Gate: a claim
  typed on the page drives the phone through the station, the verdict
  shows on the page, and the jam's recording plays on the Records page.
  The same claim through `vibium check` from a laptop gives the same
  verdict.
- [ ] **R9 Jams in a sandbox**: the sandbox runner behind the same
  interface, chosen by a flag, with the sandbox destroyed after every
  jam. Gate: the R8 gate passes with the runner switched, and nothing
  on the station host runs Node.

## Open questions

- [ ] Whether `vibium check` and `vibium run` emit an event stream a
  runner can read today, or whether the CLI gains a `--events` output
  for it. The transcript the page shows depends on this.
- [ ] A prompt that names two devices ("on the phone and the laptop").
  The first milestone takes one device per jam.
- [ ] Node in the station's image for the subprocess runner, on Fly and
  in the self-host guide: an optional layer, or a second image.
- [ ] The Vibium branch with `vibium record stop --upload`, which the
  runner's upload leans on, is not merged.
- [ ] Whether a jam's hold should be refused by rigs that do not allow
  recording, or should simply run unrecorded like any hold.
- [ ] Whether a station with no model configured should land on Devices
  until one is, rather than on the prompt with a pointer to Settings as
  written above.
