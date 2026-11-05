# WebDriver BiDi WebSocket endpoint conventions

## Summary

The W3C WebDriver BiDi specification **defines the WebSocket URL path**, but leaves the host, port, and use of TLS up to the implementation.

The two standard paths are:

```text
wss://example.com/session
wss://example.com/session/{sessionId}
```

- `/session` is the endpoint for starting a **BiDi-only session** (without an existing HTTP WebDriver session).
- `/session/{sessionId}` is the endpoint for connecting to an **existing WebDriver session**. The session ID must be a UUID in its string representation.

These examples use `wss://`, but the specification permits `ws://` as well.

## What the specification requires

| URL component | Requirement |
|---|---|
| Scheme | `ws://` or `wss://`, depending on whether the listener is secure |
| Hostname | Implementation-defined |
| Port | Implementation-defined |
| Path for a BiDi-only session | `/session` |
| Path for an existing session | `/session/{sessionId}` |
| Existing session ID | UUID string |
| Authentication / admission policy | Implementation-defined acceptance checks are permitted |

The specification constructs the WebSocket resource name as follows:

1. If the session is null, use `/session`.
2. Otherwise, use `/session/` followed by the session ID.

It constructs the full URL using the listener's host, port, and secure flag. In other words, the path is standardized, not the domain name or port.

## Two ways to establish a connection

### 1. BiDi-only connection

An implementation supporting BiDi-only sessions can publish a URL out-of-band, for example:

```text
wss://bidi.vibium.com/session
```

The client opens a WebSocket connection to that URL and sends the BiDi `session.new` command to create a session. The specification **recommends** supporting this mode (`should`), rather than requiring every implementation to offer it.

### 2. BiDi with an existing HTTP WebDriver session

A client requests BiDi when creating a classic WebDriver session:

```json
{
  "capabilities": {
    "alwaysMatch": {
      "webSocketUrl": true
    }
  }
}
```

The remote end returns the actual WebSocket URL in the session capabilities, for example:

```text
wss://bidi.vibium.com/session/550e8400-e29b-41d4-a716-446655440000
```

The client connects to that returned URL. Since the session already exists, it does not create a second one with `session.new`.

## Recommendation for Vibium

Use the spec's paths for both local and hosted endpoints:

```text
# Hosted, BiDi-only
wss://bidi.vibium.com/session

# Hosted, existing session
wss://bidi.vibium.com/session/{uuid}

# Local development (example port)
ws://127.0.0.1:9222/session
```

Keep authentication, tenant selection, and routing behind these public paths (for example, at a gateway). The spec allows implementation-defined checks before accepting a WebSocket connection, but **does not standardize a particular authentication mechanism**. Clients that use a browser-native WebSocket API may have restrictions on setting custom headers, so the authentication design needs to account for the intended clients.

Avoid making a nonstandard path such as `/bidi/connect` the *only* way to reach a BiDi endpoint if straightforward interoperability is the goal. Also, `/session` is not a generic WebSocket route: in BiDi-only mode it is specifically for establishing a new session.

## References

- [W3C WebDriver BiDi specification](https://www.w3.org/TR/webdriver-bidi/) - Transport and Establishing a Connection sections
- [MDN: Create a WebDriver BiDi connection](https://developer.mozilla.org/en-US/docs/Web/WebDriver/How_to/Create_BiDi_connection)
- [MDN: `session.new`](https://developer.mozilla.org/en-US/docs/Web/WebDriver/Reference/BiDi/Modules/session/new)

## Vibium-specific considerations: compatibility for humans and AI agents

Vibium's [Remote Browser Control tutorial](https://github.com/VibiumDev/vibium/blob/main/docs/tutorials/remote-browser.md) already establishes a useful convention: clients connect to a remote BiDi server with `ws://host:9515/session`, and the same connection URL works through the CLI, MCP server, JavaScript, and Python.

**Recommendation:** preserve that convention. For a future Vibium-hosted server, expose a canonical `wss://HOST/session` endpoint that supports genuine **BiDi-only** session creation (`session.new`). Expose `/session/{uuid}` for already-created sessions, and provide the exact URL through the standard `webSocketUrl` capability when a session is created via classic WebDriver HTTP.

### What to retain from Vibium's current documentation

- `VIBIUM_CONNECT_URL` as the common way to specify a remote endpoint across CLI, daemon, and MCP.
- `VIBIUM_CONNECT_API_KEY` for bearer-token authentication where Vibium's client supports it.
- `browser.start(url)` in the JS and Python SDKs, with header options where needed.
- The distinction between a **browser-level endpoint**, where the client creates a session, and an **existing-session URL**, where the client attaches without creating another session.

### Clean up the examples

The tutorial uses `/session` for the main examples, but the authenticated JavaScript and Python examples use `wss://cloud.example.com/bidi`. Make those examples consistently use `/session`, assuming they demonstrate a spec-style BiDi-only endpoint:

```js
const bro = await browser.start('wss://cloud.example.com/session', {
  headers: { Authorization: 'Bearer my-token' }
})
```

The tutorial also cites Selenium Grid's `ws://host:4444/session/<id>/se/bidi` as an existing-session URL. That is a real-world provider-specific route. **Clients should use the exact URL provided by the remote end**, rather than rewriting returned URLs to match a preferred path. Designing Vibium's own endpoints according to the spec is compatible with accepting provider-specific URLs as a client.

### Make server conformance an explicit goal

The URL is only the entry point. A genuinely compatible Vibium server should also:

1. Accept the WebSocket upgrade at the expected resource path.
2. Handle `session.new` and `session.end` with the specified BiDi behavior for BiDi-only sessions.
3. Support standard BiDi command and event envelopes, serialization, error responses, and session lifecycle.
4. Return correct `webSocketUrl` values from HTTP WebDriver session creation, if Vibium supports the classic HTTP entry point.
5. Be tested with at least one **generic, non-Vibium BiDi client**, not just Vibium's own SDK.

**Important distinction:** exposing the standard BiDi-only endpoint does *not* automatically make an HTTP WebDriver Classic server, and supporting custom Vibium RPC over a WebSocket does *not* make the endpoint WebDriver BiDi-compatible. The protocol behavior is what matters.

### Authentication and agent ergonomics

Using an `Authorization: Bearer ...` header at the WebSocket handshake is a practical choice for the Vibium CLI, MCP, and Node/Python clients. It is not a BiDi-defined authentication protocol, however, and browser-native WebSocket clients cannot generally set arbitrary headers. Consider that constraint before declaring it the sole supported authentication method.

For agents, optimize for predictable defaults:

- one canonical endpoint
- stable environment variable names
- correct protocol behavior
- a small copy-pasteable example
- meaningful standard errors

Avoid requiring agents to understand proprietary Vibium-specific paths or special session negotiation merely to begin automation.

### Suggested canonical examples

```bash
# Connect with Vibium CLI or MCP
export VIBIUM_CONNECT_URL=wss://bidi.vibium.com/session
export VIBIUM_CONNECT_API_KEY=YOUR_TOKEN
vibium go https://example.com

# Or run the MCP server
vibium mcp
```

The hostname above is illustrative, not a claim that a hosted endpoint is deployed.

## Infrastructure and relay design: stay boring at every boundary

The same design principle applies beyond the protocol handler: **Vibium should be as boring and conventional as possible at its external infrastructure boundaries, too.** A user or AI agent should not have to learn whether the endpoint is backed by a local process, a cloud VM, a container, a proxy, or a relay.

The important distinction is between **public contracts** and **internal implementation**. The W3C specification standardizes BiDi protocol behavior and WebSocket URL construction; it does **not** prescribe a cloud architecture, reverse proxy, relay, tenancy model, DNS naming scheme, or hosting provider. The following are Vibium design recommendations, not W3C requirements.

### Recommended externally visible contract

| Concern | Conventional default for Vibium |
|---|---|
| Public connection | Standard `wss://HOST/session` or a returned `wss://HOST/session/{uuid}` URL |
| Local connection | `ws://127.0.0.1:PORT/session` |
| Discovery | An explicit URL in config or `VIBIUM_CONNECT_URL`; for classic WebDriver, the returned `webSocketUrl` capability |
| Security | Ordinary TLS certificates and conventional HTTP/WebSocket authentication at the edge |
| Network transport | Standard HTTP WebSocket upgrade and WebSocket frames, without a custom client transport or handshake |
| Relays and gateways | Transparent from the client's perspective: preserve the BiDi messages and lifecycle end-to-end |
| Deployment | Stable DNS name and URL even when internal hosts, regions, containers, or devices change |
| Failure reporting | Clear WebSocket/HTTP handshake failures and standard BiDi errors, with documentation for retries and reconnection |

In particular, a hosted endpoint such as `wss://bidi.vibium.com/session` should look like an ordinary WebDriver BiDi server **regardless of where the actual browser runs**. That might be behind a load balancer, a reverse proxy, a private network connection, or a relay connected to an otherwise unreachable machine. Those details belong on the server side.

### Rules for cloud services and relays

1. **Treat a relay as transport infrastructure, not a new protocol.** It should carry the same WebSocket connection and BiDi commands/events that a direct connection would. Do not make clients implement a Vibium-only relay protocol just to send BiDi commands.
2. **Keep one canonical public URL shape.** Avoid exposing region IDs, tenant IDs, machine IDs, relay IDs, or ephemeral backend addresses in the path unless they are truly part of the public contract. Resolve placement and authorization internally.
3. **Preserve normal WebSocket behavior.** Support upgrade headers, bidirectional messages, long-running connections, ping/pong and idle-timeout policies suitable for automation. Configure proxies accordingly. Proxies must not silently reinterpret BiDi message payloads.
4. **Route each active session consistently.** BiDi sessions are stateful. If a load balancer or relay is involved, ensure messages for a connection reach the correct session owner; plan explicitly for backend restarts and broken connections rather than assuming connections can migrate invisibly.
5. **Keep authentication separate from session semantics.** Authenticate/authorize before granting control; do not overload `session.new` with proprietary credential fields. Document the supported authentication methods and any limitations for browser-native WebSocket callers.
6. **Make local and hosted behavior equivalent.** The same BiDi client and command sequence should work against local Vibium and hosted Vibium, apart from endpoint URL, credentials, and explicitly documented capabilities.
7. **Make the simple path the primary path.** A client with a URL and authorization should be able to open a WebSocket and use BiDi. Optional SDKs, discovery helpers, tunnels, and managed infrastructure should add convenience, not become prerequisites.

### Practical implications for agents

An agent will often infer how to connect from the standard, existing examples, and error messages. Familiar conventions reduce the amount of provider-specific knowledge an agent must invent or look up. A hosted Vibium service should therefore be usable by a generic BiDi client without importing a Vibium SDK, discovering a proprietary relay address, or calling an extra Vibium-specific control-plane API just to obtain a working protocol connection (unless provisioning a resource is explicitly a separate user action).

A useful interoperability test is: **can an unfamiliar developer or AI agent point an ordinary WebDriver BiDi client at the documented `wss://` URL, authenticate using documented standard WebSocket-compatible means, and successfully create or attach to a session?** Repeat that test through the actual production gateway or relay, not only against the backend on localhost.

## Application to Roadie R5 and Vibium Phones

This section applies the recommendations to two implementation plans dated October 2026:

- **Roadie Remote: WebRTC and Vibium Station Spec**, especially **R5 Agent relay**.
- **Vibium Phones: Hosted Service Spec**, especially **V3 Agent API**, which depends on R5.

The specs establish three different responsibilities:

| Layer | Responsibility | What the automation client should see |
|---|---|---|
| Roadie (rig) | Implements phone control, local REST/BiDi, capture, HID and audio; validates every lease grant and enforces endpoint allowlists and rate limits | A BiDi server with documented standard commands and any explicit vendor extensions |
| Vibium Station | Authenticates callers, issues leases, maintains outbound rig tunnels, forwards BiDi/REST and other traffic, provides TURN and signaling | A stable WebSocket URL and ordinary BiDi messages, not a tunnel protocol |
| Vibium Phones (`vibium-cloud`) | Accounts, tenant isolation, phone assignment, API keys, audit, billing and hosted deployment | The same BiDi protocol, regardless of which rig owns the phone |

Station's device-neutral model (`rig` -> `device`, with `phone` and future `browser` kinds) is a useful foundation. **Keep that device and infrastructure model internal to the control plane.** A BiDi client should not need to know about Nostr identities, station signatures, TURN, Fly.io, Pi tunnels or Blossom.

### Immediate R5 recommendation: don't widen the milestone

**Keep the R5 gate:** "A Vibium script drives the dev phone through the station end to end." Keep REST, BiDi, screenshots, MJPEG, fallback WebSockets, named read-only tokens, Pi endpoint allowlists, per-token/lease limits and `govulncheck` in CI in scope. Add focused protocol-interoperability tests rather than a new service architecture.

R5 currently specifies these device-specific routes:

```text
wss://station.example/v1/devices/{id}/bidi
wss://api.vibium.com/v1/phones/{id}/bidi  # hosted V3
```

Those are reasonable **control-plane or implementation-specific** routes, but they are not the W3C's conventional BiDi-only URL shape. They can continue to work during R5. Do **not** treat renaming the route as sufficient to become compliant, and do not delay end-to-end phone automation to reorganize all routes.

**Recommended R5 changes, in priority order:**

1. **Authenticate at the WebSocket HTTP upgrade, before BiDi starts.** For the Vibium CLI, Node, Python and other clients supporting arbitrary headers, accept `Authorization: Bearer ...` in the upgrade request. The current spec's "first WebSocket message carries the token" convention is acceptable for internal station signaling or a private tunnel, but **should not be required on the public BiDi socket**: a generic BiDi client expects the WebSocket messages to be BiDi commands and events. A browser-native WebSocket client cannot normally set a custom `Authorization` header, so explicitly plan a separate secure browser-compatible mechanism if that caller is supported (for example, a scoped authentication cookie obtained over HTTPS). Never put long-lived API keys or lease grants in query strings. Reject unauthorized upgrades with an appropriate HTTP status and no BiDi messages.
2. **Make BiDi relaying payload-transparent.** Station may inspect the request path and authenticate/authorize the connection, but it must not insert a proprietary message before `session.new`, rewrite BiDi command IDs, remap event names, alter results/errors or silently replay commands. Preserve message ordering and apply bounded buffering, backpressure, size limits and explicit close behavior. The Pi remains the final authorization and command-validation gate.
3. **Keep the lease model distinct from BiDi sessions.** Station leases allocate and authorize a device; a WebDriver BiDi session is a protocol lifecycle. A lease ID is **not automatically a WebDriver session UUID**. Define what happens when the lease expires, is revoked, is taken over, or the rig disconnects: stop forwarding, close the affected BiDi WebSocket, and report disconnects clearly. Do not manufacture a `/session/{id}` endpoint from a lease ID.
4. **Test the same BiDi exchange locally and through Station.** Run a real `session.new`/supported-command/`session.end` sequence (or attach to an existing session if that is Roadie's actual supported mode) over a direct Roadie socket and the public Station socket. Verify identical envelopes, values and errors for supported commands, apart from transport failures. At least one test should use a minimal generic BiDi WebSocket client, not only Vibium. A successful WebSocket connection alone is insufficient.
5. **Enforce authorization in both layers.** Station checks the caller's permitted device and scope; Roadie verifies the station-signed lease and enforces its own scope, endpoint allowlist, message validation and limits. Read-only tokens must not be able to send BiDi commands that cause input or other mutations merely because they reached the BiDi WebSocket. Classify or block mutating BiDi commands at the enforcing boundary; if a safe read-only subset is not established yet, refuse read-only BiDi access rather than granting broader permissions.

For the first pass, preserve R5's device-specific route as a documented compatibility path if necessary. The important immediate work is correct protocol behavior and handshake authorization. A standard `/session` alias can be layered on once the session lifecycle and device-selection rules are settled.

### Canonical endpoints without leaking device routing

**Target public BiDi interface** (illustrative hostnames):

```text
ws://127.0.0.1:PORT/session       # local BiDi-only entry point
wss://station.example/session    # self-hosted station, BiDi-only
wss://bidi.vibium.com/session    # future hosted BiDi-only endpoint
wss://HOST/session/{uuid}        # existing WebDriver session
```

A standard `/session` endpoint **creates a new BiDi-only session via `session.new`**; it does not mean "attach to whichever phone was most recently leased." Station must have a deterministic and authorized way to pick a device before allocating that session. Possible design choices include a previously selected device associated with an authenticated caller, a scoped short-lived session-creation grant, or a provider-specific capability at session creation. None of these device-selection mechanisms is defined by the BiDi specification, so document the chosen mechanism rather than presenting it as standard.

**Recommended separation:** use the existing REST control plane to list phones and obtain authorization/leases; return an **opaque, directly usable BiDi connection URL** for the selected device, and keep the public automation protocol ordinary BiDi. For a fully conventional `/session` entry point, make session creation actually work there rather than simply proxying a preselected session with incompatible lifecycle semantics. If a returned URL needs device-specific routing, the client may follow it exactly; prioritize returning a working endpoint over inventing a `/session` URL that cannot resolve the target device correctly.

An existing-session `/session/{uuid}` endpoint should use a real WebDriver session ID if that mode is offered. Clients connecting to third-party providers should **always use the remote end's returned `webSocketUrl` unchanged**, including Grid-specific paths.

### Authentication: protocol compatibility versus security

| Connection | Preferred treatment |
|---|---|
| Agent or SDK to public BiDi endpoint | Credential in WebSocket upgrade header; authorize user, device, scope and lease before `101 Switching Protocols` |
| Browser-native WebSocket client | Secure HTTP-established cookie or another documented browser-compatible upgrade authorization method; apply Origin protections; no API key in URL |
| Station to Roadie | Existing outbound rig tunnel, signed lease grants and per-message/operation checks; internal tunnel protocol may have its own handshake |
| WebRTC signaling and HID fallback | Can retain their separately documented first-message protocol where appropriate; do not confuse it with WebDriver BiDi |

Passing the initial handshake is necessary but **not** sufficient for authorization: revocation, lease takeover, session expiry, device scope and Pi-side validation must continue to apply during a long-lived connection.

### Additional R5/V3 acceptance criteria

These are **proposed additions** to the existing milestones, not claims that the implementation already satisfies them:

- [ ] **R5:** A generic BiDi WebSocket client connects through Station without a Vibium-specific first-message handshake and executes the supported BiDi session flow.
- [ ] **R5:** Direct Roadie and Station-relayed connections produce equivalent BiDi protocol messages for supported commands, responses and events.
- [ ] **R5:** Invalid/missing authorization is rejected before upgrade; revoked leases and rig disconnects terminate active sessions explicitly.
- [ ] **R5:** Read-only or wrong-device grants cannot invoke mutating HID/REST/BiDi operations, and the Pi's allowlist and rate limits remain effective even if Station misroutes or misauthorizes a request.
- [ ] **R5:** Large messages, slow clients, ping/pong, close frames and tunnel reconnects do not result in silent replay or protocol corruption.
- [ ] **V3:** The same externally documented BiDi client behavior works through `api.vibium.com` with per-customer device ownership, API-key restrictions and audit, without a Vibium-only BiDi negotiation step.
- [ ] **Later, when canonicalization ships:** `/session` has genuine BiDi-only `session.new` behavior and `/session/{uuid}` references actual WebDriver sessions; test them through the real production gateway.

### Boundaries worth protecting

- **Do not turn the public BiDi wire protocol into a Roadie API.** Phone-specific features not covered by WebDriver BiDi can use documented vendor-prefixed BiDi extensions, or the explicitly separate REST/HID API. A path ending in `/bidi` is not proof of protocol conformance.
- **Do not make a relay know more than it needs.** Station needs routing and access control, not semantic rewriting of every BiDi message. It may need command classification to enforce a read-only policy; that is a deliberate, narrow security exception, not license to transform the protocol.
- **Do not make the user choose an infrastructure topology.** Local Roadie, self-hosted Station and Vibium Phones should differ primarily in endpoint discovery, credentials and available capabilities, not in how standard BiDi commands are issued.
- **Do not prematurely conflate Nostr and BiDi.** Nostr keys and events can remain the internal identity and authorization machinery. They do not need to be exposed in the WebDriver BiDi handshake.

**Suggested implementation order:** handshake auth -> payload-transparent relay -> lease/revocation behavior -> generic-client compatibility test -> standard-path exposure and full session lifecycle. This preserves the original R5 end-to-end gate while moving the public interface toward standards-based interoperability.

---

**Bottom line:** Vibium should be standard at the protocol boundary and conventional at the infrastructure boundary, while staying flexible internally. Use gateways, relays, and cloud orchestration as needed, but avoid making their existence part of the user's automation model.
