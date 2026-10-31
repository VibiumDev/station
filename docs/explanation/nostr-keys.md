# Nostr Keys

Every station and every rig in Station and Roadie has a Nostr key, and
every signed message between them is a Nostr event. If you've never heard
of Nostr, start here. If you have, skip to "Who has a key".

## What Nostr is

Nostr is a small open protocol for signed messages. It's best known as
the basis for a few social networks that have no company in the middle,
but the protocol itself is three ideas, and that's all of it:

- **You are a key, not an account.** Instead of signing up somewhere, you
  make a key pair on your own machine: a public half you hand out and a
  secret half you keep. Nobody issues it, so nobody can take it away.
- **A message is an event.** An event is a small JSON object: who sent it
  (the public key), when, what kind of message it is, a few tags, some
  content, and a signature made with the secret half. Anyone holding the
  public key can check the signature. Nobody can forge an event or change
  one after the fact.
- **Relays pass events around.** A relay is a plain server that stores
  events and hands them to whoever asks. Anyone can run one, and the
  network is whatever relays people choose to use.

The protocol is written down in numbered documents called NIPs (Nostr
Implementation Possibilities). NIP-01 is the event format; the rest add
one thing each.

Station and Roadie use the first two ideas and not the third. They don't
talk to any relay. No event is published, and nothing depends on a relay
being up. What they borrow is the identity format and the message format,
because those are the part that's hard to get right, and the part that
would have to be right if a station and a rig run by different people
ever needed to trust each other.

## What an npub and an nsec are

A key pair is two very long numbers that belong together. Nostr spells
them so people can read them and tell them apart:

- An **npub** (`npub1…`) is the public key. It's your name. Safe to show,
  print, paste into a code or a config file.
- An **nsec** (`nsec1…`) is the secret key. It signs. It never leaves the
  machine that made it.

Think of the npub as a username that nobody else can claim, and the nsec
as the password that proves it's you, with one difference: a password is
sent to the server to be checked, and an nsec never is. It signs the
message locally, the signature goes out, and the signature is what gets
checked.

Station keeps its secret key in `station.key` in its data directory, and
Roadie keeps the rig's in `rig.key` in its config directory. Each is made
the first time the program runs, as hex or as an nsec. Everything else in
either program carries only public keys and signatures.

For the record: the keys are secp256k1 (the curve Bitcoin uses), the
signatures are BIP-340 Schnorr, the spellings are NIP-19, and the message
format is NIP-01. Station's event kinds and what each one carries are
listed in [kinds.md](../kinds.md).

## Who has a key

**The station.** Its key signs the hold grants that let a browser or an
agent use a device, the revocations that end them, and the station's own
profile (kind 0: its name and its tunnel address). The same key, hashed,
is the secret behind the TURN relay's per-hold credentials, so the relay
keeps no list of credentials at all. `station key` prints the npub, the
hex and the fingerprint, and the Settings page shows them under Network.

**Each rig.** A Roadie Pi has its own key. It signs the enrollment
request, the hello that answers the station's challenge each time the
tunnel connects, and the announcements that say which devices the rig
offers. The rig's npub is the Key column of the station's Rigs table, and
it's what the station looks up when the tunnel opens.

**Devices, later.** There's room for a device to have a key of its own,
tied to its rig by a rig-signed announcement, so a phone's identity and
history could follow it to another Pi. Roadie doesn't make one yet; its
phone is named by the rig, `rig_…/phone`.

**Operators, later.** The spec reserves a key for the person or business
that owns rigs, and an event (kind 7703) by which they say "this rig is
mine". Neither exists yet. In single-player Station the owner and the
operator are the same person, and the password stands in.

## What this gets us today

**The rig proves itself without a password.** The rig has no credential
for the station and the station has none for the rig. Enrollment sends a
signed request. Every time the tunnel connects, the station sends a
random challenge and the rig signs it. The station looks the public key
up and marks the rig online. There's nothing to leak from either side
except a secret that never travels.

**Enrollment pins the station.** An enrollment code ends with the first
four hex characters of the station's key, its fingerprint. Before `roadie
enroll` sends anything, it fetches the station's signed profile, checks
the key against the code, and refuses a station whose key doesn't match.
That's what makes LAN mode safe to enroll on, where the station's
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
station's word; it's believed because the rig can check the signature. A
station that was broken into couldn't forge a rig's announcements, and a
rig can't forge a grant.

**The operator's consent travels with the rig.** The policy file on the
rig lists the stations it accepts by npub, and for each one the scopes it
may grant, whether admin grants are taken, the longest session and the
hours. A rig can be in more than one list: your own station and a hosted
one. Because grants can be checked, each station gets exactly what the
operator allowed and no more, and a policy change on the rig takes effect
without the station's help.

**Auditing comes free.** Every message between a station and a rig is
signed by whoever sent it, and every one the station accepts is stored
exactly as signed, in an append-only table. So the audit trail is the
traffic itself; there's no separate log to keep honest. "Who held this
phone at three o'clock?" is answered by a grant the station signed and a
hello the rig signed. Anyone with the two npubs can check both, and
neither could have been written after the fact or by the other party.
Refused events aren't stored, so the table holds only what was accepted.
The owner's own actions on the pages (making or revoking a token, removing
a rig, renaming the station) aren't events; they go to a plain audit
table beside it, the one part of the record that rests on the station's
word alone.

**Bring your own key.** The key files accept an nsec, so a station or a
rig can run under a key that already exists. A rig's key is still best
its own: it signs on every reconnect and should be cheap to revoke.

## Why Nostr's formats and not our own?

A signing key and a signed JSON message are easy to invent and hard to
get right, and a home-made format is understood by exactly one program.
Nostr's is small (one curve, one signature scheme, one event layout, one
way to spell keys), widely implemented, and its libraries have been
picked over in public. Station uses
[`fiatjaf.com/nostr`](https://pkg.go.dev/fiatjaf.com/nostr), pure Go, no
cgo. Any Nostr tool can check an event out of Station's store.

The usual alternatives each miss something:

- **API keys or passwords for rigs** are shared secrets. Both sides hold
  them, either side can leak them, and a third party can verify nothing.
- **JSON Web Tokens** would sign just as well, but a JWT is a token, not
  an identity. There's no story for a rig, a device or an operator being
  known outside the system that issued it.
- **X.509 certificates** give you that, at the price of a certificate
  authority and its ceremony for every rig.

Nostr keys are 32 bytes, need no authority, and the identity story comes
free.

The costs are small. secp256k1 isn't what hardware secure elements
prefer: the ATECC608 found on Pi boards does P-256, so a hardware-held rig
key would need the key-type field the spec reserves for it. And naming a
rig by its public key means a lost `rig.key` is a new rig, enrolled again
with a new code, which is the answer we want anyway.

## What it could do later

None of this is built, and the spec puts most of it after the
single-player milestones. The point of picking the format early is that
each would be an addition, not a redesign.

**Find a station by its npub.** A station could publish its profile and a
list of the rigs it hosts to Nostr relays. Rigs and customers would then
find it by key, and an operator could list a rig by vouching for it.

**Rigs run by other people.** Station stores events behind an interface
shaped like a relay's, with kinds in the NIP-01 ranges and every party in
`p` tags. Turning on a private relay over that store (khatru, from the
same module) would let rigs another operator runs read and write only
their own events, logged in by their keys (NIP-42), with content that
crosses a third party encrypted (NIP-44). Opening the public kinds to
public reads later is a policy switch, not a rewrite.

**A phone that keeps its identity.** With a device key, a phone moved to
a different Pi keeps its npub, its history and, if a hosting service ever
rates devices, its reputation. The old rig's binding is revoked and the
new rig signs a new one.

**Files by hash, with the same keys.** Session recordings, screenshots
and Vibium's `record.zip` traces are files, not events. Blossom, Nostr's
file protocol, names a file by its SHA-256 and allows uploads and
downloads with signed events from the same keys. A recording would be
downloadable by the hold's parties and nobody else, and its link would
survive the file moving between servers.

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
over the rig's tunnel. Nostr relays can also carry that signaling
between two peers with no server in common; the next section shows one
project doing exactly that. A viewer and a rig that both have keys could
meet that way when no station sits between them, with the rig still
demanding a grant before it answers.

## What Microduck does with Nostr

[Microduck](https://github.com/pollen-robotics/microduck) is a small
open-source biped robot from Pollen Robotics (part of Hugging Face), built
for reinforcement learning. Its browser
[simulator](https://huggingface.co/spaces/pollen-robotics/microduck-simulator)
and the [librezoo fork](https://github.com/librezoo/microduck-simulator)
have a multiplayer mode: every open tab shows the other visitors' ducks as
see-through ghosts. The networking is
[Trystero](https://github.com/dmotz/trystero), peer-to-peer WebRTC whose
signaling goes over public Nostr relays, so a static page with no backend
can find its peers. Tabs join one fixed room name, broadcast the duck's
pose (trunk, fourteen joints, jaw, colour, variant) ten or fifteen times
a second under a throwaway peer id, and the fork's privacy notes say the
rest: relay operators see the signaling and its metadata, WebRTC peers
see each other's addresses, the project can't delete copies others
already received, and the mode is off by default and left out of the
fork's iOS build altogether.

Two things from that apply here, in opposite directions.

The rendezvous trick is real and works: public Nostr relays are enough
for two browsers that share a room name to swap WebRTC offers with no
server of their own. That's the mechanism behind "meeting without a
tunnel" above, and it costs nothing to keep that door open because
Station's keys are already the kind the relays expect.

The trust model is the opposite of Station's, on purpose. Microduck's
peers are throwaway identities in a public room, any tab may join, and
the privacy notes have to warn about what relays and peers can see.
Station's identities are persistent and pinned, its events are private
first and encrypted when they cross a third party, and nobody reaches a
device without a grant the device's own rig has checked. The relay trick
is worth borrowing; the open room is what the policy file, the grants
and the private relay are there to avoid.

## Further reading

- [kinds.md](../kinds.md): every event kind, who signs it, and what the
  rig checks in each tag.
- [The remote spec](../remote-spec.md), "Nostr and federation readiness":
  the design these choices come from, and the Blossom, relay and bunker
  plans.
- [The Nostr NIPs](https://github.com/nostr-protocol/nips): NIP-01
  (events), NIP-19 (npub and nsec), NIP-40 (expiration), NIP-42 (relay
  authentication), NIP-44 (encryption), NIP-46 (bunkers).
