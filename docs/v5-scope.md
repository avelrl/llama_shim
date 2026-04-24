# V5 Hosted Parity And Advanced Transports

Last updated: April 24, 2026.

This document is the parking lot for compatibility work that should not be
folded into V3 and should not be mixed into V4 extension/plugin/security work.

V2 is the broad compatibility facade.
V3 is practical runtime and transport expansion around the shim-local facade.
V4 is opinionated extension, plugin, memory, and hardening work.
V5 is where the project can take on expensive exact hosted-parity and advanced
transport behavior after the practical V3 substrate is stable.

## Why V5 Exists

Some work is valuable, but it is too exacting to bundle into V3:

- the official docs confirm a surface exists but do not pin every observable
  wire edge
- upstream behavior needs live fixtures before the shim can make a claim
- the work is about hosted parity rather than making the local shim useful
- implementing it too early would slow down practical Codex compatibility

Those items should not go into V4 just because they are post-V3. V4 is for
shim-owned extensions, plugin architecture, memory, and hardening. V5 is for
deeper compatibility fidelity.

## Candidate V5 Tracks

### 1. Exact Responses WebSocket Hosted Parity

V3 implements WebSocket mode for the current shim-local Responses subset. V5
can take on exact hosted transport fidelity once the practical path is stable.

Candidate work:

- exact WebSocket close codes and close reasons
- exact hosted error ordering
- exact behavior for malformed frames, unsupported message types, and
  mid-response client disconnects
- documented 60-minute connection limit with fixture-backed behavior
- hosted connection quota semantics such as
  `websocket_connection_limit_reached`
- exact failed-continuation cache eviction behavior
- exact `store=false` / Zero Data Retention continuation edge cases
- exact connection-local cache hydration and eviction timing
- exact behavior when a persisted `store=true` response is continued from a new
  socket

Do not claim these from inference. Add live upstream fixtures or direct
observations before changing matrix wording.

### 2. Upstream WebSocket Proxying

V3 should not proxy WebSocket connections upstream. It should own the
shim-local WebSocket subset.

V5 can add upstream WebSocket proxying if it becomes necessary for
`responses.mode=prefer_upstream` or provider-specific deployments.

Candidate work:

- raw WebSocket tunnel to upstream `/v1/responses`
- auth/header/query preservation rules
- backpressure and cancellation behavior
- upstream close-code passthrough
- observability without logging sensitive frame bodies
- fallback behavior when upstream does not support WebSocket
- interaction with `responses.mode=prefer_upstream`

### 3. Hosted Tool Choreography Over WebSocket

V3 can reuse the current shim-local Responses streaming event payloads over
WebSocket.

V5 is where exact hosted choreography can be pursued for tool families whose
wire behavior is only partially documented.

Candidate work:

- fixture-backed native `shell` WebSocket event order
- fixture-backed native `apply_patch` WebSocket event order
- code interpreter progress/artifact events over WebSocket
- hosted `web_search`, `file_search`, `image_generation`, `computer`, MCP, and
  `tool_search` event edge cases over WebSocket
- exact failure/status behavior for tool calls over WebSocket
- exact artifact, annotation, and citation placement when the transport is
  WebSocket rather than SSE

### 4. Realtime API WebSocket Compatibility

Responses WebSocket mode and Realtime API WebSocket are different surfaces.

V3 WebSocket is only for `/v1/responses`. If the project later needs Realtime
API compatibility, track it here instead of expanding the V3 Responses
transport work.

Candidate work:

- Realtime WebSocket endpoint shape
- session and conversation events
- audio frame behavior
- tool calls in Realtime sessions
- Realtime-specific auth, error, and close semantics

Do not mix this into Responses WebSocket implementation.

### 5. WebSocket Fixture And Capture Tooling

Current upstream capture tooling focuses on HTTP/SSE. Exact WebSocket parity
will need its own capture path.

Candidate work:

- `cmd/upstream-ws-capture`
- sanitized raw frame transcript format
- parsed fixture format for WebSocket events
- deterministic replay tests for WebSocket fixture streams
- capture templates under `internal/httpapi/testdata/upstream/`
- docs for when WebSocket fixtures are mandatory

## Non-Goals

V5 is not a general wishlist.

Do not put these here:

- shim-owned memory or plugin features; use [v4-scope.md](v4-scope.md)
- local runtime safety or sandbox hardening; use V4 or runtime-hardening docs
- basic WebSocket support for the current shim-local Responses subset; use
  [v3-websocket.md](v3-websocket.md)
- new backends that are useful but unrelated to exact hosted parity; use
  [v3-scope.md](v3-scope.md) or V4 depending on ownership

## Promotion Rule

Move a V5 item into an implementation plan only when all of these are true:

- V3 practical behavior exists and is tested
- the hosted behavior is observable and materially different from the V3 local
  subset
- live upstream fixtures or official docs justify the stronger claim
- matrix wording can state exactly what is now compatible
- the work does not require weakening existing local behavior

If any of those are missing, keep the item parked here.
