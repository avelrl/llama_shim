# V3 Responses WebSocket

Last updated: April 24, 2026.

This document records the V3 plan for implementing Responses API WebSocket
mode in the shim for the full current shim-local Responses subset.

It does not change the frozen V2 contract.
It does not claim exact hosted transport parity before code, tests, fixtures,
and smoke coverage exist.

## Why This Exists

The current shim supports HTTP `POST /v1/responses` and SSE streaming for the
local subset. That is enough for broad compatibility and for the current Codex
CLI smoke path because Codex falls back to HTTP when the WebSocket upgrade is
rejected.

The fallback is still a gap:

- Codex CLI 0.124 first attempts `ws://.../v1/responses`
- the shim currently returns HTTP 405 because `/v1/responses` only accepts
  `POST`
- Codex then falls back to HTTP and completes successfully
- the run is functionally usable, but it does not exercise the lower-latency
  Responses WebSocket transport

The official OpenAI docs now define Responses WebSocket mode as a first-class
transport for long-running, tool-heavy workflows. That makes this a natural V3
track after the native coding-tools smoke work.

## Official References Reviewed

This design note was checked on April 24, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [WebSocket Mode](https://developers.openai.com/api/docs/guides/websocket-mode)
  - [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
  - [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
  - [Responses streaming events reference](https://developers.openai.com/api/docs/api-reference/responses-streaming)
  - [Codex configuration reference](https://developers.openai.com/codex/config-reference)

The practical contract from the docs:

- the WebSocket endpoint is `/v1/responses`
- each turn starts with a client JSON message of type `response.create`
- the payload mirrors the normal Responses create body
- transport-specific HTTP fields such as `stream` and `background` are not used
  in WebSocket mode
- server events and ordering match the existing Responses streaming event
  model
- a single WebSocket connection may receive multiple `response.create`
  messages, but only one response is in flight at a time
- there is no multiplexing support
- the connection has a documented 60-minute maximum lifetime
- `previous_response_id` semantics match HTTP mode, with a connection-local
  cache for the most recent response
- `store=false` can still continue from the connection-local cache; if the id
  is no longer cached, the expected error is `previous_response_not_found`

Codex config also exposes:

- `openai_base_url`, which lets Codex target this shim as the built-in OpenAI
  provider
- `model_providers.<id>.supports_websockets`, which advertises whether a
  provider supports the Responses WebSocket transport

## Current Code Status

As of April 24, 2026:

- `/v1/responses` only accepts HTTP `POST`
- a WebSocket upgrade to `/v1/responses` returns HTTP 405
- SSE create-stream and retrieve-stream are implemented for the local subset
- the local stream emitter is currently tied to `http.ResponseWriter` and SSE
  formatting
- Codex CLI smoke passes through HTTP fallback
- the smoke explicitly tolerates WebSocket 405 only when HTTP fallback
  completes successfully
- the V3 coding-tools HTTP/SSE status is closed as a `Broad subset`, including
  a real Codex CLI coding-task smoke that edits a scratch workspace file

This means the model/tool loop is mostly present. The missing piece is a
transport adapter that can receive `response.create` messages and write
Responses streaming events as WebSocket JSON messages.

## V3 Rollout Goal

The V3 goal is not a small demo endpoint.

The V3 goal is feature-complete WebSocket support for the current shim-local
Responses subset that already works through HTTP and SSE.

That means:

- one event-generation path shared by SSE and WebSocket
- no second, drift-prone WebSocket-only replay implementation
- Codex CLI smoke without tolerated WebSocket 405 when WebSocket support is
  enabled
- explicit documentation that this is a local transport subset, not exact
  hosted parity

## V3 Full Current-Subset Scope

Support:

- WebSocket upgrade on `GET /v1/responses`
- authentication and existing request middleware behavior equivalent to HTTP
  `/v1/responses`
- client text frames containing JSON objects
- `type: "response.create"` messages
- all Responses create fields that are already supported by the shim-local HTTP
  and SSE subset
- sequential processing on a single connection
- repeated `response.create` messages on the same connection
- `previous_response_id` continuation using existing stored local state
- connection-local cache for the most recent response, including `store=false`
  continuation on the same socket
- server events emitted as JSON WebSocket messages with the same payload shape
  as the current SSE `data:` payloads
- error messages shaped as documented WebSocket `error` events where possible

The V3 WebSocket path should cover the same local create-stream families that
the shim already supports:

- ordinary assistant text
- stateful local continuation through `previous_response_id`
- custom functions and custom tool bridge flows
- Codex `exec_command` compatibility bridge
- native local `shell`
- native local `apply_patch`
- local `file_search`
- local `web_search`
- local `image_generation`
- local `computer`
- local `code_interpreter`
- local remote-MCP `server_url` subset
- local hosted/server `tool_search` subset
- server-side compaction behavior already supported by the local HTTP/SSE path

Do not include in V3:

- multiplexing
- parallel in-flight responses on one socket
- binary frames
- upstream WebSocket proxying
- exact hosted latency/cache behavior
- exact hosted connection quota semantics
- full 60-minute lifetime enforcement beyond a conservative server timeout
- Realtime API WebSocket behavior; this track is only Responses WebSocket mode

Those deferred items belong in [v5-scope.md](v5-scope.md), not V4.

## Suggested Implementation Shape

Avoid copying the response-generation stack.

1. Add a WebSocket dependency.
   Prefer a small, maintained Go library such as `nhooyr.io/websocket` or
   `github.com/coder/websocket`.
2. Teach the `/v1/responses` route to detect WebSocket upgrade requests before
   the current `POST` method check.
3. Add `responseHandler.websocket(w, r)`.
4. Factor the current SSE emitter behind a small event-sink interface:

   ```go
   type responseEventSink interface {
       write(eventType string, payload any) error
       done() error
   }
   ```

5. Keep the existing SSE behavior as one sink implementation.
6. Add a WebSocket sink that writes each event payload as one JSON text frame.
7. Reuse the existing local create-stream path for `response.create` across all
   currently supported local routes.
8. For now, reject unsupported WebSocket message types with a JSON `error`
   message and keep the connection open when it is safe to continue.
9. Add a per-connection in-flight guard so a second `response.create` is
   rejected while the previous one is still running.
10. Track the most recent response id on the connection for `store=false`
    continuation behavior.
11. Keep unsupported and proxy-only routes on the same policy they have over
    HTTP: local subset where implemented, explicit validation where local-only
    cannot support the request, and no upstream WebSocket tunneling in V3.

## Event Shape

The WebSocket server should send the same JSON object that SSE currently places
inside `data:`.

Example:

```json
{
  "type": "response.created",
  "sequence_number": 1,
  "response": {
    "id": "resp_...",
    "object": "response",
    "status": "in_progress"
  }
}
```

Do not wrap this in an extra shim-owned envelope unless an upstream fixture
shows that OpenAI does so. The official docs say server events and ordering
match the existing Responses streaming event model.

## Error Shape

For validation and runtime errors, prefer the documented WebSocket error shape:

```json
{
  "type": "error",
  "status": 400,
  "error": {
    "code": "previous_response_not_found",
    "message": "Previous response with id 'resp_abc' not found.",
    "param": "previous_response_id"
  }
}
```

For shim-owned validation failures that already map to OpenAI-style
`invalid_request_error`, preserve that error type in the nested `error` object.

## Capability Reporting

Add a shim-owned capability flag under `/debug/capabilities` before claiming
the feature in matrix wording.

Suggested shape:

```json
{
  "responses": {
    "websocket": {
      "enabled": true,
      "support": "local_subset",
      "endpoint": "/v1/responses",
      "sequential": true,
      "multiplexing": false
    }
  }
}
```

If the final schema uses a different nesting style, keep the same facts:

- enabled or disabled
- local subset, not hosted parity
- endpoint path
- sequential only
- no multiplexing

## Tests Required

Focused unit/integration coverage:

- `GET /v1/responses` without upgrade still returns method-not-allowed
- WebSocket upgrade succeeds on `/v1/responses`
- invalid JSON frame returns a WebSocket `error` message
- unsupported message type returns a WebSocket `error` message
- `response.create` emits `response.created` and `response.completed`
- output item and text delta events preserve the current SSE payload shape
- local tool-loop request works through WebSocket for the current Codex bridge
  path
- local `shell` and `apply_patch` direct Responses requests work through
  WebSocket if those tools are already enabled
- repeated `response.create` works on the same socket
- `previous_response_id` continuation works with stored local responses
- `store=false` continuation works for the most recent response on the same
  socket
- stale uncached `store=false` continuation returns `previous_response_not_found`
- a second in-flight `response.create` on the same socket is rejected
- HTTP/SSE behavior remains unchanged

Regression checks:

```bash
go test ./internal/httpapi -run 'WebSocket|ResponsesStream|Codex'
go test ./...
make lint
git diff --check
```

## Smoke Required

Add a repo-owned smoke target after implementation.

Suggested target:

```bash
make responses-websocket-smoke
```

The smoke should:

- start from the devstack
- connect to `ws://127.0.0.1:18080/v1/responses`
- send a simple `response.create`
- assert `response.created`
- assert at least one model output event
- assert `response.completed`
- send a second `response.create` with `previous_response_id`
- assert the second turn completes
- exercise at least one local tool-loop route
- exercise direct native local `shell`
- exercise direct native local `apply_patch`

Add focused smoke coverage for the other currently supported local families as
the implementation reaches them:

- `file_search`
- `web_search`
- `image_generation`
- `computer`
- `code_interpreter`
- MCP `server_url`
- hosted/server `tool_search`
- local compaction continuation

Then update the existing Codex CLI smoke:

- remove tolerance for WebSocket 405
- assert Codex CLI completes without falling back from WebSocket to HTTP when
  `supports_websockets` is enabled
- keep a separate HTTP fallback smoke only if we intentionally support clients
  or providers that disable WebSocket transport

## Fixture Policy

No upstream fixture is required for V3 if the implementation only claims:

- the documented endpoint
- `response.create` input messages
- existing Responses streaming event payloads
- sequential, non-multiplexed processing
- the current shim-local HTTP/SSE subset

Add upstream fixtures before claiming anything stronger. Deferred fixture-backed
claims belong in [v5-scope.md](v5-scope.md), especially:

- exact hosted error ordering
- exact close codes
- connection lifetime and quota behavior
- hosted cache hydration timing
- exact behavior for failed continuations
- hosted WebSocket behavior for native `shell` and `apply_patch` tool traces

## Matrix Status Path

Initial matrix wording should stay conservative:

```text
Responses WebSocket mode | V3 | Stage after HTTP/SSE local subset is stable |
No current V2 claim; Codex CLI currently uses HTTP fallback when WebSocket
upgrade returns 405.
```

After implementation and smoke:

```text
Responses WebSocket mode | Broad subset | Keep local transport boundary
explicit | `/v1/responses` WebSocket accepts sequential `response.create`
messages for the full current shim-local Responses subset and emits existing
Responses streaming events as JSON text frames. No multiplexing, no upstream
WebSocket proxying, and no exact hosted connection quota/lifetime parity
claimed.
```

Do not use `Implemented` unless all supported HTTP local subsets have matching
WebSocket coverage and the Codex CLI smoke proves WebSocket use without HTTP
fallback.

## Open Questions

- Which WebSocket library should be standardized for this repo?
- Should WebSocket support be always on, or controlled by config?
- Should `/debug/capabilities` expose WebSocket support under `.responses` or
  under a separate `.transports` object?
- What exact Codex config should the smoke use to force or advertise
  `supports_websockets=true`?
- Should the full-family WebSocket smoke live in one script or split into
  focused slices to keep diagnostics readable?

Keep these open until implementation starts. They do not block documenting the
track.
