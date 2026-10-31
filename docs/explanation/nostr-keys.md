# Nostr Keys

Every key in Station and Roadie is a Nostr key, and every signed message
between them is a Nostr event. Neither talks to the Nostr network: no
relay is involved, nothing is published, and nothing depends on a relay
being there. What they take from Nostr is the identity format and the
event format, so that a station, a rig and the person who runs them each
have one durable identity that works now, inside one owner's setup, and
would keep working if they ever had to trust each other across the open
network. This page explains what the keys are, what they do today, why
Nostr's shapes rather than our own, and what they leave open.

## What an npub and an nsec are

A Nostr identity is a secp256k1 key pair, the curve Bitcoin uses, with
BIP-340 Schnorr signatures. NIP-19 gives the two halves readable spellings:

- An **npub** (`npub1…`) is the public key. It is the identity: safe to show,
  print, put in a code or a policy file.
- An **nsec** (`nsec1…`) is the secret key. It signs, and it never leaves
  the machine that made it.

Station and Roadie keep the secret half in a file: `station.key` in the
station's data directory, `rig.key` in Roadie's config directory, each made
the first time the program runs and holding the key as hex or as an nsec.
Everything else carries only npubs, hex public keys and signatures.

A signed message is a NIP-01 event: a JSON object with the signer's public
key, a time, a kind number, tags, content, an id that is the hash of all
of that, and a signature over the id. Anyone with the npub can check it
and nobody can alter it. Station's kinds and their tags are listed in
[kinds.md](../kinds.md).

## Who has a key

**The station.** Its key signs the hold grants that let a browser or an
agent use a device, the revocations that end them, and the station's own
profile (kind 0: its name and its tunnel address). The same key, hashed, is the
secret behind the TURN relay's per-hold credentials, so the relay keeps no
list of credentials at all. `station key` prints the npub, the hex and the
fingerprint, and the Settings page shows them under Network.

**Each rig.** A Roadie Pi has its own key. It signs the enrollment
request, the hello that answers the station's challenge each time the
tunnel connects, and the announcements that say which devices the rig
offers. The rig's npub is the Key column of the station's Rigs table, and
it is what the station looks up when the tunnel opens.

**Devices, later.** The model leaves room for a device to have a key of
its own, bound to its rig by a rig-signed announcement, so a phone's
identity and history could follow it to another Pi. Roadie does not make
one yet; its phone is named by the rig, `rig_…/phone`.

**Operators, later.** The spec reserves a key for the person or business
that owns rigs, and an event (kind 7703) by which they say "this rig is
mine". Neither exists yet; in single-player Station the owner and the
operator are the same person and the password stands in.

## What the keys do today

**The rig proves itself without a password.** The rig has no credential
for the station and the station has none for the rig. Enrollment sends a
signed request; every tunnel connection answers a random challenge with
a signed hello; the station looks the public key up and marks the rig
online. There is nothing to leak from either side except a secret that
never travels.

**Enrollment pins the station.** An enrollment code ends with the first
four hex characters of the station's key, its fingerprint. Before `roadie
enroll` sends anything it fetches the station's signed profile, checks the
key against the code, and refuses a station whose key does not match.
That is what makes LAN mode safe to enroll on, where the station's
certificate is self-signed and a spoofed station on the same network
could otherwise take the first contact. From then on the station's npub
is in the rig's policy file and only that key's grants are accepted.

**The rig is the final gate.** A hold is a grant: an event signed by the
station naming the rig, the device, the hold, its sequence number, the
scopes (`view`, `input`, `bidi`, `admin`), and an expiry ten minutes out.
Every request that reaches Roadie through the tunnel presents one, and
Roadie checks it itself: the signature against the npubs in its policy,
the rig and device, the time, the event id against a replay list, the
sequence against the highest it has seen, and the hold against the
revocations it has received. Nothing the station says is believed on the
station's word; it is believed because the rig can verify the signature.
A station that was compromised could not forge a rig's announcements, and
a rig cannot forge a grant.

**The operator's consent travels with the policy.** The policy file on the
rig lists the stations it accepts by npub, and for each one the scopes it
may grant, whether admin grants are taken, the longest session and the
hours. A rig can be in more than one list: its owner's own station and a
hosted one. Because grants are verifiable, each station gets exactly what
the operator allowed and no more, and a policy change on the rig takes
effect without the station's cooperation.

**The record is signed.** Every event the station signs or accepts is
stored exactly as signed, in an append-only table. "Who held this phone
at three o'clock" is answered by a grant the station signed and a hello
the rig signed, neither of which could be written after the fact.

**A key made elsewhere works.** The key files accept an nsec, so a station
or a rig can run under a key that already exists. A rig's key is still
best its own: it signs on every reconnect and should be cheap to revoke.

## Why Nostr's shapes and not our own

A signing key and a signed JSON message are easy to invent and hard to
get right, and a home-made format is understood by exactly one program.
Nostr's format is small (one curve, one signature scheme, one event
layout, one way to spell keys), widely implemented, and comes with
libraries that have been argued over in public. Station uses
[`fiatjaf.com/nostr`](https://pkg.go.dev/fiatjaf.com/nostr), pure Go, no
cgo. Any Nostr tool can check an event out of Station's store.

The alternatives each miss something. API keys or passwords for rigs are
shared secrets: both sides hold them, either side can leak them, and a
third party can verify nothing. JSON Web Tokens over Ed25519 would sign
just as well, but a JWT is a token and not an identity; there is no story
for a rig, a device or an operator being known outside the system that
issued it. X.509 certificates give that, at the price of a certificate
authority and its ceremony for every rig. Nostr keys are 32 bytes, need
no authority, and the identity story comes free.

The costs are small. secp256k1 is not what hardware secure elements
prefer: the ATECC608 found on Pi boards does P-256, so a hardware-held rig
key would need the key-type field the spec reserves for it. And choosing a
public key as a rig's name means a lost `rig.key` is a new rig, enrolled
again with a new code, which is the intended answer.

## What this leaves open

None of the following is built, and the spec puts most of it after the
single-player milestones. The point of choosing the format early is that
each would be an addition, not a redesign.

**Finding a station by its npub.** A station could publish its kind 0
profile and a listing of rigs it hosts to Nostr relays. Rigs and
customers would then find it by key, and an operator could list a rig by
attesting to it.

**Rigs run by other people.** Station stores events behind an interface
shaped like a relay's, with kinds in the NIP-01 ranges and every party in
`p` tags. Turning on a private relay over that store (khatru, from the
same module) would let rigs that another operator runs read and write
only their own events, authenticated by their keys (NIP-42), with content
that crosses a third party encrypted (NIP-44). Opening the public kinds
to public reads later is a policy switch.

**A phone that keeps its identity.** With a device key, a phone moved to
a different Pi keeps its npub, its history and, if a hosting service ever
rates devices, its reputation; the old rig's binding is revoked and the
new rig signs a new one.

**Files by hash, with the same keys.** Session recordings, screenshots and
Vibium's `record.zip` traces are files, and Blossom, Nostr's blob
protocol, addresses a file by its SHA-256 and authorizes uploads and
downloads with signed events from the same keys. A recording would be
downloadable by the hold's parties and nobody else, and its link would
survive the file moving between servers.

**Keeping the important keys off the box.** NIP-46 lets a key live in a
"bunker" that signs on request. The keys that sign rarely and matter most
(an operator's, a device's, a station's identity) could live there, with
the station's identity key signing a short-lived key for the day's grants.
Rig keys would stay on the rig, where signing on every reconnect is
cheap and revoking is cheap too.

**Agents as principals.** Agents hold devices today with API tokens the
station issues. An agent with a key of its own could sign its requests,
and a grant's `sub` would name its npub: an action on a phone traceable to
a particular agent, across stations, by a signature rather than a token
table.

**Meeting without a tunnel.** Station brokers WebRTC signaling itself,
over the rig's tunnel. Nostr relays can also carry signaling between two
peers that have no server in common; see the next section. A viewer and
a rig that both have keys could meet that way when no station sits
between them, with the rig still demanding a grant before it answers.

## What Microduck does with Nostr

[Microduck](https://github.com/pollen-robotics/microduck) is a small
open-source biped robot from Pollen Robotics (part of Hugging Face), built
for reinforcement learning. Its browser
[simulator](https://huggingface.co/spaces/pollen-robotics/microduck-simulator)
and the [librezoo fork](https://github.com/librezoo/microduck-simulator)
have a multiplayer mode in which every open tab shows the other visitors'
ducks as translucent ghosts. The networking is
[Trystero](https://github.com/dmotz/trystero): peer-to-peer WebRTC whose
signaling goes over public Nostr relays, so a static page with no backend
can find its peers. Tabs join a fixed room name, broadcast the duck's pose
(trunk, fourteen joints, jaw, colour, variant) ten or fifteen times a
second under an ephemeral peer identifier, and the fork's privacy notes
say the rest: relay operators process the signaling and its metadata
under their own practices, WebRTC peers see each other's addresses, the
project cannot delete copies others already received, and the mode is
off by default and left out of the fork's iOS build altogether.

Two things from that apply here, in opposite directions.

The rendezvous pattern is real and works: public Nostr relays are enough
for two browsers that share a room name to exchange WebRTC offers with no
server of their own. That is the mechanism behind "meeting without a
tunnel" above, and it costs nothing to keep open because Station's keys
are already the kind the relays expect.

The trust model is the opposite of Station's, on purpose. Microduck's
peers are throwaway identities in a public room, any tab may join, and
the privacy notes have to warn about what relays and peers can see.
Station's identities are persistent and pinned, its events are private
first and encrypted when they cross a third party, and nobody reaches a
device without a grant the device's own rig has verified. The relay
pattern is worth borrowing; the open room is what the policy file,
the grants and the private relay are there to avoid.

## Further reading

- [kinds.md](../kinds.md): every event kind, who signs it, and what the
  rig checks in each tag.
- [The remote spec](../remote-spec.md), "Nostr and federation readiness":
  the design these choices come from, and the Blossom, relay and bunker
  plans.
- [The Nostr NIPs](https://github.com/nostr-protocol/nips): NIP-01
  (events), NIP-19 (npub and nsec), NIP-40 (expiration), NIP-42 (relay
  authentication), NIP-44 (encryption), NIP-46 (bunkers).
