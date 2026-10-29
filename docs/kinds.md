# Station event kinds

Every signed message in Station is a Nostr event (NIP-01: `id`, `pubkey`,
`created_at`, `kind`, `tags`, `content`, `sig`), signed with BIP-340 Schnorr
over secp256k1. Nothing is published; events travel over the tunnel, the
data channel or the first WebSocket message, and are stored verbatim in the
event store. The kind numbers below sit in the NIP-01 ranges so a relay would
treat each one correctly if the store ever backs one.

| Kind | Range | Event | Signed by | Stored |
|---|---|---|---|---|
| 0 | metadata | Station profile: `{"name":...}` | Station | Replaced |
| 7700 | regular | Lease grant (the session token) | Station | Kept |
| 7701 | regular | Lease revocation | Station | Kept |
| 7702 | regular | Enrollment request | Rig | Kept |
| 7703 | regular | Operator attestation ("this rig is mine") | Operator | Kept |
| 17700 | replaceable | Rig announcement: name, devices | Rig | Newest per rig |
| 27700 | ephemeral | Tunnel hello: the signed challenge | Rig | Never |
| 37700 | addressable, `d` = device id | Device announcement: kind, capabilities, device key | Rig | Newest per device |

Every event names its parties in `p` tags: the station, the rig and, when
the device has a key, the device. Station's own tags are single words.

## Lease grant (7700)

| Tag | Value | The rig checks |
|---|---|---|
| `p` | the rig's public key | Names this rig |
| `p` | the device's key, when it has one | |
| `rig` | the rig id the station assigned | Matches what enrollment returned |
| `dev` | the device id, `<rig id>/<device name>` | One of this rig's devices |
| `sub` | who holds the lease: `owner`, `token:<name>` or a login | Logged |
| `lease` | the lease id, `lse_<hex>` | The current lease, or a higher `seq` |
| `seq` | the device's lease counter | Never lower than the one it has |
| `scope` | one per tag: `view`, `input`, `bidi`, `admin` | Each operation's scope, and the rig policy allows it |
| `stun` | STUN URLs, when the station has them | Nothing; the rig gathers candidates with them |
| `turn` | the lease's TURN username (`<expiry>:<lease id>`), its credential, then the TURN URLs | Nothing; the rig gathers relay candidates with them. The credential lasts the lease's maximum time |
| `expiration` | NIP-40, 10 minutes after `created_at` | Not expired, 30 s skew |
| `nonce` | 8 random bytes, hex | Nothing; it keeps every grant's id distinct |

`content` is empty. The event `id` is the replay nonce: a grant is accepted
once per connection. Refreshed grants carry the same `lease` and `seq`.

## Lease revocation (7701)

Tags `p` (rig), `lease`, `e` (the grant's id). The rig ends every session on
that lease within a second.

## Enrollment request (7702)

Signed by the rig key on first contact. Tags `p` (the station, from the
code's fingerprint once the station's key is known), `code` (the enrollment
code), `name` (the rig's name). `content` carries the devices as JSON, the
same shape as the announcements below.

## Rig announcement (17700)

Sent on every tunnel connect and whenever the devices change. Tags `p`
(station), `name`, and one `d`-less `dev` tag per device id. `content` is the
JSON list of devices: `{"id":"pi-0007/phone","kind":"phone","caps":["video","audio","hid","bidi","mjpeg"],"shortcuts":[{"label":"Home","keys":"GUI+h"}]}`.
`shortcuts` are device-provided toolbar buttons the shared viewer renders
and sends as key commands, so Station's code never names a phone.

## Tunnel hello (27700)

The rig's answer to the station's challenge: tags `p` (station) and
`challenge` (the nonce the station sent). Ephemeral: checked, never stored.

## Device announcement (37700)

Addressable by `d` = device id. Tags `p` (station), `p` (device key, when
the device has one), `kind`, one `cap` per capability. Binding a device key
to a rig is this event, signed by the rig; moving the device to another rig
means a new announcement from the new rig.
