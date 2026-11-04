# Nostr Keys

Every station and every rig has a Nostr key, and every signed message
between them is a Nostr event. This page starts from zero: what Nostr is,
what the keys are, what they do for Station and Roadie today, and what
they could do later.

## What Nostr is

Nostr is a small open protocol for signed messages. Most people who have
heard of it know it as the thing under a few social networks that have no
company in the middle. The social networks are not the point here. The
plumbing is, and the plumbing is three ideas:

1. **Keys, not accounts.** Nobody signs you up. You make a key
   pair on your own machine: a public half you hand out and a secret half
   you keep. Nobody issued it, so nobody can take it away or lock you out.
2. **A message is a signed event.** An event is a small blob of JSON: who
   sent it (the public key), when, what kind of message it is, a few
   tags, some content, and a signature made with the secret half. Anyone
   holding the public key can check the signature. Nobody can forge an
   event or change one after the fact.
3. **Relays pass events around.** A relay is a plain server that stores
   events and hands them out to whoever asks. Anybody can run one.

That is the whole protocol. The details live in numbered documents called
NIPs (Nostr Implementation Possibilities). NIP-01 is the event format, and
the rest each add one thing.

Station and Roadie use the first two ideas and skip the third. They never
talk to a relay. Nothing is published, and nothing depends on a relay
being up. What they take from Nostr is the identity format and the
message format, because those are the parts that are hard to get right,
and the parts that would have to be right if a station and a rig run by
two different people ever needed to trust each other.

The same thinking that put Vibium on WebDriver BiDi instead of a
proprietary protocol applies here: build on an open format that many
people have implemented and argued over, not on one that a single company
or a single project controls.

## npub and nsec

A key pair is two very long numbers that belong together. Nostr spells
them so a person can read them and never mix them up:

- An **npub** (`npub1…`) is the public key. It is your name. Safe to show,
  print, or paste into a code or a config file.
- An **nsec** (`nsec1…`) is the secret key. It signs things. It never
  leaves the machine that made it.

The nearest everyday thing is a username and a password, with one twist.
A password is sent to the server, and the server checks it. An nsec is
never sent anywhere. It signs the message on your own machine, the
signature goes out, and the signature is what gets checked. The server
never had anything to leak.

Station keeps its secret key in `station.key` in its data directory.
Roadie keeps the rig's in `rig.key` in its config directory. Both are made
the first time the program runs, as hex or as an nsec. Everything else in
either program carries only public keys and signatures.

For the record: the keys are secp256k1 (the same curve Bitcoin uses), the
signatures are BIP-340 Schnorr, the npub/nsec spelling is NIP-19, and the
event format is NIP-01. Every kind of event Station sends, and what is
inside each one, is in [kinds.md](../kinds.md).

## Who has a key

**The station.** Its key signs the hold grants that let a browser or an
agent use a device, the revocations that end them, and the station's own
profile (kind 0: its name and its tunnel address). The same key, hashed,
is the secret behind the TURN relay's per-hold credentials, so the relay
keeps no list of credentials at all. `station key` prints the npub, the
hex and the fingerprint, and the Settings page shows them under Network.

**Each rig.** A Roadie Pi has its own key. It signs the enrollment
request, the hello that answers the station's challenge every time the
tunnel connects, and the announcements that say which devices the rig
offers. The rig's npub is the Key column in the station's Rigs table, and
it is what the station looks up when the tunnel opens.

**Devices, later.** There is room for a device to have a key of its own,
tied to its rig by a rig-signed announcement, so a phone's identity and
history could follow it to another Pi. Roadie does not make one yet; its
phone is named by the rig, `rig_…/phone`.

**Operators, later.** The spec reserves a key for the person or business
that owns rigs, and an event (kind 7703) for them to say "this rig is
mine". Neither exists yet. In single-player Station the owner and the
operator are the same person, and the password stands in.

## What the keys do today

**The rig never has a password.** The rig has no credential for the
station and the station has none for the rig. Enrollment sends a signed
request. Every time the tunnel connects, the station sends a random
challenge and the rig signs it. The station looks the public key up and
marks the rig online. There is nothing to leak on either side, except a
secret that never travels.

**Enrollment pins the station.** An enrollment code ends with the first
four hex characters of the station's key (its fingerprint). Before
`roadie enroll` sends anything, it fetches the station's signed profile,
checks the key against the code, and refuses a station whose key does not
match. That is what makes LAN mode safe to enroll on, where the station's
certificate is self-signed and a fake station on the same network could
otherwise grab the first contact. From then on the station's npub is in
the rig's policy file and only that key's grants are accepted.

**The Pi is the final gate.** A hold is a grant: an event signed by the
station naming the rig, the device, the hold, its sequence number, the
scopes (`view`, `input`, `bidi`, `admin`), and an expiry ten minutes out.
Every request that reaches Roadie through the tunnel carries one, and
Roadie checks it itself: the signature against the npubs in its policy,
the rig and device, the time, the event id against a replay list, the
sequence number against the highest it has seen, and the hold against the
revocations it has received. Nothing the station says is taken on the
station's word. It is believed because the rig can check the signature.
A station that was broken into still could not forge a rig's
announcements, and a rig cannot forge a grant.

**The operator's consent travels with the rig.** The policy file on the
rig lists the stations it accepts, by npub, and for each one the scopes
it may grant, whether admin grants are taken, the longest session and the
hours. A rig can be in more than one list: your own station and a hosted
one. Because every grant can be checked, each station gets exactly what
the operator allowed and no more, and a policy change on the rig takes
effect without the station's help.

**Auditing comes free.** Every message between a station and a rig is
signed by whoever sent it, and every one the station accepts is stored
exactly as signed, in an append-only table. So the audit trail is the
traffic itself. There is no separate log to keep honest. "Who held this
phone at three o'clock?" is answered by a grant the station signed and a
hello the rig signed. Anyone with the two npubs can check both, and
neither could have been written after the fact or by the other party.
Refused events are not stored, so the table holds only what was accepted.
The owner's own actions on the pages (making or revoking a token,
removing a rig, renaming the station) are not events. They go to a plain
audit table beside it, the one part of the record that rests on the
station's word alone.

**Bring your own key.** The key files accept an nsec, so a station or a
rig can run under a key that already exists. A rig's key is still best
its own: it signs on every reconnect and should be cheap to revoke.

## Why Nostr's formats and not our own

A signing key and a signed JSON message are easy to invent and hard to
get right, and a home-made format is understood by exactly one program.
Nostr's format is small (one curve, one signature scheme, one event
layout, one way to spell keys), it is widely implemented, and its
libraries have been picked over in public. Station uses
[`fiatjaf.com/nostr`](https://pkg.go.dev/fiatjaf.com/nostr), pure Go, no
cgo. Any Nostr tool can check an event out of Station's store.

The usual alternatives each miss something:

- **API keys or passwords for rigs** are shared secrets. Both sides hold
  them, either side can leak them, and a third party can verify nothing.
- **JSON Web Tokens** would sign just as well, but a JWT is a token, not
  an identity. There is no story for a rig, a device or an operator being
  known outside the system that issued it.
- **X.509 certificates** give you that, at the price of running a
  certificate authority and its ceremony for every rig.

Nostr keys are 32 bytes, need no authority, and the identity story comes
free.

The costs are small, but real. secp256k1 is not what hardware secure
elements prefer: the ATECC608 found on Pi boards does P-256, so a
hardware-held rig key would need the key-type field the spec reserves for
it. And naming a rig by its public key means a lost `rig.key` is a new
rig, enrolled again with a new code, which is the intended answer.

## What it could do later

None of this is built, and the spec puts most of it after the
single-player milestones. The reason to pick the format early is that
each of these becomes an addition instead of a redesign.

**Find a station by its npub.** A station could publish its profile and a
list of the rigs it hosts to Nostr relays. Rigs and customers would then
find it by key, and an operator could list a rig by vouching for it.

**Rigs run by other people.** Station already stores events behind an
interface shaped like a relay's, with kinds in the NIP-01 ranges and
every party in `p` tags. Turning on a private relay over that store
(khatru, from the same module) would let rigs that somebody else runs
read and write their own events and nothing else, logged in by their keys
(NIP-42), with anything that crosses a third party encrypted (NIP-44).
Opening the public kinds to public reads later is a policy switch, not a
rewrite.

**A phone that keeps its identity.** With a device key, a phone moved to
a different Pi keeps its npub, its history and, if a hosting service ever
rates devices, its reputation. The old rig's binding is revoked and the
new rig signs a new one.

**Files by hash, same keys.** Session recordings, screenshots and
Vibium's `record.zip` traces are files, not events. Blossom, Nostr's file
protocol, names a file by its SHA-256 and allows uploads and downloads
with signed events from the same keys. A recording would be downloadable
by the hold's parties and nobody else, and its link would survive the
file moving between servers.

**The important keys off the box.** NIP-46 lets a key live in a "bunker"
that signs on request. The keys that sign rarely and matter most (an
operator's, a device's, the station's identity) could live there, with
the station's identity key signing a short-lived key for the day's
grants. Rig keys stay on the rig, where signing on every reconnect is
cheap and so is revoking.

**Agents with keys of their own.** Agents hold devices today with API
tokens the station issues. An agent with a key could sign its requests,
and a grant would name its npub. An action on a phone would then trace
to one agent, across stations, by a signature instead of a token table.

**Meeting without a tunnel.** Station brokers WebRTC signaling itself,
over the rig's tunnel. Nostr relays can also carry that signaling between
two peers with no server in common; Microduck's simulator, below, does
exactly that. A viewer and a rig that both have keys could meet that way
when no station sits between them, with the rig still demanding a grant
before it answers.

## Who else builds on Nostr

Nostr's keys and events are infrastructure for more than social
networks, and a few well-known projects use them the way Station does.

**Block's Buzz.** In July 2026 Block released
[Buzz](https://github.com/block/buzz), an open-source, self-hostable
workspace where people and AI agents share the same channels. Its
[announcement](https://engineering.block.xyz/blog/buzz) lays out the
model: an identity is a keypair and every action is a signed event; each
agent gets its own key; the agent's owner signs a narrowly scoped
authorization, the agent signs its own work, and removing the owner means
the agent cannot reconnect. The server you run is a Nostr relay (NIP-01,
NIP-42 for login, NIP-34 for Git). That is the same shape as Station's
operator attestation and the agent keys described above, and Block gives
the same reason: "A protocol anyone can rebuild is a protocol nobody can
lock you into." Block's CEO, Jack Dorsey, has also funded Nostr
development personally since 2022.

**Bitchat.** The Bluetooth mesh chat app uses Nostr relays as its
internet transport when Bluetooth is unavailable, and runs its location
channels over them.

**Nostr Wallet Connect** (NIP-47) lets an app ask a user's Lightning
wallet to pay by sending it encrypted events over relays. Many wallets
and apps implement it.

**Microduck.** Pollen Robotics' browser
[simulator](https://huggingface.co/spaces/pollen-robotics/microduck-simulator)
for its small open-source biped shows other visitors' robots live, with
WebRTC signaled over public Nostr relays through
[Trystero](https://github.com/dmotz/trystero): the "meeting without a
tunnel" mechanism above, with no server of its own. Its trust model is
the opposite of Station's, throwaway identities in a room anyone can
join, which is exactly what the policy file and the grants are there to
avoid.

## Further reading

- [kinds.md](../kinds.md): every event kind, who signs it, and what the
  rig checks in each tag.
- [The remote spec](../remote-spec.md), "Nostr and federation readiness":
  the design these choices come from, and the Blossom, relay and bunker
  plans.
- [The Nostr NIPs](https://github.com/nostr-protocol/nips): NIP-01
  (events), NIP-19 (npub and nsec), NIP-40 (expiration), NIP-42 (relay
  authentication), NIP-44 (encryption), NIP-46 (bunkers).
