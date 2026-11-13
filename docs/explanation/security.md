# Security in Short

The station decides who may use a device, and the rig has the last word.

- **Keys.** Every key is secp256k1 with BIP-340 Schnorr, shown as an npub.
  [Nostr keys](nostr-keys.md) explains why, and what the signed events do.
- **Grants.** A hold grant is a signed event with the rig, the device, the
  hold's id and sequence number, its scopes, and a ten-minute expiry. The
  rig refuses forged, expired, wrong-device and older-sequence grants,
  replays, revoked holds, and anything its policy file does not allow.
- **Enrollment.** A code is single-use, lasts 15 minutes, and ends with
  the station key's fingerprint, which the rig checks, so a rig enrolls
  only with the station you meant. Removing a rig refuses its key from
  then on.
- **Tokens.** API tokens travel in headers or the first WebSocket message,
  never in URLs. The station keeps only their hashes.
- **TLS.** The station always serves TLS, itself or through the proxy in
  front of it. A rig pins the station's certificate at enrollment when no
  root signs it, and the station's key always.
- **TURN.** Credentials are per hold, derived from the station's key, and
  good for the hold's maximum time. The relay refuses expired and ended
  holds, caps allocations per hold, and never sends to a private, loopback,
  link-local, shared-address or unique-local destination, so it is not a
  proxy into anyone's network.
- **People.** Passwords are argon2id hashes, every password sign-in asks
  for a code from an authenticator app, and sign-in is throttled. Regular
  users see only the devices their teams were given
  ([teams](../specs/teams.md), [second factor](../specs/second-factor.md)).

The tests behind these rules are listed in
[security-tests.md](../specs/security-tests.md).
