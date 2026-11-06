# Sessions: a prompt on the station

Nov 6, 2026 · draft for review

## Summary and goals

A station page with one big prompt. A person signs in, reads "What can I
check for you?", types a claim or an errand, and an agent drives a device
on the station to do it, while the page shows the agent's steps and the
device being driven. When the agent is done the answer sits in the
transcript, and the recording of what it did sits in the Record Store,
playable from the Records page like any other.

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
   sandbox per session later, so a hosted station and a self-hosted one
   share the page and differ only in the runner.
4. **The rig stays the final gate.** A session is a hold like any other,
   signed by the station, checked by the rig, limited by the rig's
   policy. Nothing about the agent is trusted by the Pi.

## Naming

The page and its list are **Sessions**, which keeps Vibium's music theme
going: a roadie sets up the rig, the session is the time spent playing,
and the Record Store keeps what came out of it. Each prompt starts a
session. A check is a session that ends with a verdict, and an errand is
a session that ends when the goal is done. The word sits beside Devices
and Records as a noun for a list, and it promises nothing a plain errand
cannot deliver. "Checks" is the product's word on its landing page and
the primary use, and the prompt keeps that voice, but a list of checks
with no verdict in half of them would look like half of them failed.
"Chat" names the interaction and nothing about phones.

The page does not ask whether a prompt is a check or an errand. The
agent infers it from the phrasing, as the CLI's two commands do by being
two commands, and the session's header says which it took: checking, or
doing.

The word already has two jobs in this system, and both sit close enough
to the new one to help rather than hurt. WebDriver BiDi has sessions:
`session.new` is how an agent gets a browser or a phone, and the
station's `/session` endpoint is where it asks for one. The rig's policy
also limits the length of a session, meaning the span of a hold, which
is what the Devices page means too when it says a host records
sessions. A session in this spec takes a hold and drives the device
through a BiDi session, so it is one of the rig's sessions and contains
one of BiDi's. When the two have to be told apart, the ones described
here are **chat sessions**, and the low-level ones are **WebDriver BiDi
sessions**, or BiDi sessions for short. In this document a session with
no qualifier is a chat session. The paths use the qualifier, so the
two never meet in a URL bar: the page is `/chats`, one session is
`/chats/{id}`, and the API is `/v1/chats`, while `/session`, singular
and unversioned, stays the BiDi entry point the standard spells out.
`/sessions` would sit one letter from that endpoint in every link and
every doc, and a short root such as `/c/` or `/s/` says nothing to
someone reading a link, which a station with a dozen routes has no
need to save. The label in the top bar is Sessions all the same; a
label that differs from its path is ordinary, as GitHub's "Pull
requests" lives at `/pulls`.

## How a session goes

A person types a prompt and presses Check. The station:

1. Makes a session record with an id, the prompt, the person, the
   device chosen (or the only one), and the time.
2. Takes a hold on the device under the person's identity, with the
   session named in the hold's subject, so the Devices page says who is
   driving and the rig's recording, when the rig records, lands against
   that hold.
3. Mints a token for the session: control scope, this device only, this
   session only, expiring with the session's time limit. The token never
   reaches the browser.
4. Starts the runner with the prompt, the device, the token, the
   station's address and the provider settings, and reads its event
   stream.
5. Streams the events to the page: each step the agent took, each
   screenshot it looked at, each tool call and its answer, and at the
   end the verdict or the goal's completion, or an error.
6. When the runner ends, releases the hold, stores the transcript with
   the session record, and links the session to the recording the
   runner uploaded.

The page shows the transcript as it arrives, and beside it the device
viewer in watching mode, which the station already does for a device
someone else holds. The person sees the phone move as the agent drives
it, and can press Hold to take the device back, which ends the session.

## The runner

The runner is an interface with one job: start a session and stream its
events. Two implementations are planned, and the page cannot tell them
apart.

**The subprocess runner** starts `vibium check` or `vibium run` on the
station's own host, with the session's token, the station's address and
the provider settings in its environment, and reads the CLI's event
output. It needs Node and the Vibium CLI installed beside the station,
which the self-host guide and the Fly image gain as an optional step. It
is the first implementation, because it is the least machinery, and it
serves a single-player station completely.

**The sandbox runner** starts a disposable machine per session, a Fly
Machine or a Sprite with the Vibium CLI inside, gives it the same
environment, and reads the same stream back over the sandbox's
connection. The sandbox is destroyed when the session ends. It serves a
hosted station, where prompts and keys must not share a process with
the station, and where many sessions happen at once.

The rig is not a runner. It has Node and it holds the phone at the
shortest distance, but it is the operator's machine and the final gate:
it checks grants and refuses what its policy does not allow, and it may
belong to someone other than the person typing the prompt. A customer's
model key and prompt do not go there.

The contract between the station and a runner:

- Input: the prompt, the device id, the session's token, the station's
  URL, and the provider settings as the `VIBIUM_AI_*` variables.
- Output: a stream of events in the order they happen, each with a
  time, a kind (step, screenshot, tool, verdict, done, error) and its
  content; and at the end, the SHA-256 of the recording the runner
  uploaded.
- Control: stop, which the station sends when the person takes the
  device back, the time limit passes, or the person signs out.

The recording is uploaded by the runner itself with the presign flow
the Record Store already has: the runner asks the station for an upload
authorization under its session token, and sends the zip. The station
marks the file with the session's hold, so the Records page lists it
against the session as it lists the rig's own recording of the same
hold.

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
there and not in a failed session. The key itself is never shown and
never sent to a browser. A hosted station will want settings per
account, which is the sandbox runner's milestone to bring.

## Pages and API

The top bar reads Devices, Records, Sessions, Settings. The Sessions
page, at `/chats`, has the prompt and, under it, the list of past
sessions: when, who, the device, the prompt, and the verdict where
there is one. A session page, at `/chats/{id}`, has the transcript,
the watching viewer while the session is live, and the recording's
Play when it is done.

The API follows the pages, under the token model R5 built:

| Method and path | What it does |
| --- | --- |
| `POST /v1/chats` | Starts a session: `prompt`, `device`, and `mode` when the caller wants to force check or run. Needs control. Returns the session with its id. |
| `GET /v1/chats` | The caller's sessions; every session for an admin. |
| `GET /v1/chats/{id}` | One session with its transcript and the recording's SHA-256. |
| `GET /v1/chats/{id}/events` | The event stream, as server-sent events, from the start of the session; a finished session replays and closes. |
| `POST /v1/chats/{id}/stop` | Ends the session and releases the hold. |

A read-only token sees sessions and their events and starts none. The
BiDi entry point, `wss /session`, is untouched: an agent that opens a
BiDi session directly is not a chat session and appears on no list but
the Devices page, as today.

## Holds, limits and cost

A session is a hold, so the rig's limits apply unchanged: the scopes the
policy allows, the hours, the longest session the policy permits. Two of
the station's own rules need care. The idle rule releases a hold
untouched for five minutes, and an agent waiting on a model can sit
longer than that between inputs, so the runner's events count as
activity, and a session with no event for the idle time is stopped
rather than silently losing its phone. And a session has a time limit of
its own, thirty minutes by default, which the token's expiry enforces
even if the runner hangs.

Model cost is the person's, on their own key. The first milestone
records the tokens the provider reports in the session's transcript, so
the list can show what a session cost, and sets no budget. A budget per
session and per day is the hosted station's concern.

## Security

- The model key stays where it is: in the host's environment or the
  sandbox's, never in the station's database and never in a page.
- A session's token is scoped to one device and one session and expires
  with the session; it travels in a header, never a URL, like every
  other token.
- Page content and screenshots the agent reads are data from the device,
  not instructions from the person; the agent's own prompt separates the
  two, and the station does not add the device's output to anything it
  signs.
- The rig checks the session's grant like any grant. A runner that
  misbehaves can do no more than the hold allows, and the rig's
  recording, when on, records it.
- The transcript may contain what the screen showed. It is stored with
  the session's visibility: the person who started it and admins, like
  a private recording.

## Milestones and acceptance

- [ ] **R8 Sessions on the host**: the Sessions page with the prompt,
  the subprocess runner, the event stream, the watching viewer beside
  the transcript, the AI section on Settings, the recording in the
  Record Store. Gate: a claim typed on the page drives the phone through
  the station, the verdict shows on the page, and the session's
  recording plays on the Records page. The same claim through
  `vibium check` from a laptop gives the same verdict.
- [ ] **R9 Sessions in a sandbox**: the sandbox runner behind the same
  interface, chosen by a flag, with the sandbox destroyed after every
  session. Gate: the R8 gate passes with the runner switched, and
  nothing on the station host runs Node.

## Open questions

- [ ] Whether `vibium check` and `vibium run` emit an event stream a
  runner can read today, or whether the CLI gains a `--events` output
  for it. The transcript the page shows depends on this.
- [ ] A prompt that names two devices ("on the phone and the laptop").
  The first milestone takes one device per session.
- [ ] Node in the station's image for the subprocess runner, on Fly and
  in the self-host guide: an optional layer, or a second image.
- [ ] The Vibium branch with `vibium record stop --upload`, which the
  runner's upload leans on, is not merged.
- [ ] Whether a session's hold should be refused by rigs that do not
  allow recording, or should simply run unrecorded like any hold.
