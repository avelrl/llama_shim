# V3 Responses WebSocket

Last updated: April 24, 2026.

This document records the V3 implementation status for Responses API WebSocket
mode in the shim for the current shim-local Responses subset.

It does not change the frozen V2 contract.
It does not claim exact hosted transport parity.

## Why This Exists

Before this V3 track, the shim supported HTTP `POST /v1/responses` and SSE
streaming for the local subset. That was enough for broad compatibility because
Codex could fall back to HTTP when the WebSocket upgrade was rejected, but it
left a transport gap:

- Codex CLI 0.124 started attempting `ws://.../v1/responses`, and current
  `codex-cli 0.125.0` still expects that transport to be available
- the old shim returned HTTP 405 because `/v1/responses` only accepted `POST`
- Codex then fell back to HTTP and completed successfully
- the run was functionally usable, but it did not exercise the lower-latency
  Responses WebSocket transport

The official OpenAI docs now define Responses WebSocket mode as a first-class
transport for long-running, tool-heavy workflows. V3 now implements that
transport for the shim-local subset while keeping exact hosted parity out of
scope.

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

- `POST /v1/responses` remains the HTTP create path
- `GET /v1/responses` without WebSocket upgrade still returns HTTP 405
- `GET /v1/responses` with WebSocket upgrade accepts a persistent Responses
  WebSocket connection when `responses.websocket.enabled=true`
- SSE create-stream and retrieve-stream are implemented for the local subset
- WebSocket server frames reuse the existing completed-response replay event
  path and write the same JSON payloads that SSE places in `data:`
- the repo uses `github.com/coder/websocket`
- `cmd/responses-websocket-smoke` covers direct WebSocket stateful
  continuation, native `shell` and `apply_patch` replay events, local
  `file_search`, `web_search`, `image_generation`, remote MCP, cached MCP
  follow-up, hosted/server `tool_search`, and `tool_search` follow-up
- Codex CLI smokes now fail if Codex logs HTTP 405 from
  `ws://.../v1/responses`
- the V3 coding-tools HTTP/SSE status is closed as a `Broad subset`, including
  a real Codex CLI task matrix smoke that edits scratch workspace files and
  verifies a tiny Go bugfix

The model/tool loop is shared with HTTP. WebSocket is now an additional
transport adapter for the current shim-local Responses subset.

## V3 Rollout Goal

The V3 goal was not a small demo endpoint.

The V3 goal was feature-complete WebSocket support for the current shim-local
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

## Implemented Shape

The implementation avoids copying the response-generation stack.

1. Add `github.com/coder/websocket`.
2. Teach the `/v1/responses` route to detect WebSocket upgrade requests before
   the current `POST` method check.
3. Add `responseHandler.websocket(w, r)`.
4. Factor completed-response replay into
   `forEachCompletedResponseReplayEvent`, shared by SSE and WebSocket.
5. Strip WebSocket-unused create fields (`stream`, `stream_options`,
   `background`) before dispatching to the normal Responses create path.
6. Execute one `response.create` at a time by processing frames synchronously on
   a single connection.
7. Emit each replay event payload as one JSON text frame.
8. Reject malformed JSON and unsupported WebSocket message types with documented
   `error` frames and keep the connection open when safe.
9. Force an internal shadow-store write for WebSocket-created responses so the
   most recent `store=false` response can be used as `previous_response_id` on
   the same shim while public retrieve/delete/input-items still respect
   `store=false`.
10. Keep unsupported and proxy-only routes on the same policy they have over
    HTTP: local subset where implemented, explicit validation where local-only
    cannot support the request, and no upstream WebSocket tunneling in V3.

## Event Shape

The WebSocket server sends the same JSON object that SSE currently places
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

The shim does not wrap this in an extra shim-owned envelope. The official docs
say server events and ordering match the existing Responses streaming event
model.

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

The shim advertises a capability flag under `/debug/capabilities` before
claiming the feature in matrix wording.

Shape:

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

The facts are intentionally shim-owned and conservative:

- enabled or disabled
- local subset, not hosted parity
- endpoint path
- sequential only
- no multiplexing

## Tests And Regression

Implemented focused coverage:

- `GET /v1/responses` without upgrade still returns method-not-allowed
- WebSocket upgrade succeeds on `/v1/responses`
- invalid JSON frame returns a WebSocket `error` message
- unsupported message type returns a WebSocket `error` message
- `response.create` emits `response.created` and `response.completed`
- output item and text delta events preserve the current SSE payload shape
- local `shell` and `apply_patch` direct Responses requests work through
  WebSocket
- local `file_search`, `web_search`, `image_generation`, remote MCP, and
  hosted/server `tool_search` smoke through WebSocket
- cached remote MCP and `tool_search` follow-up flows work through WebSocket
- repeated `response.create` works on the same socket
- `previous_response_id` continuation works with stored local responses
- `store=false` continuation works for the most recent response on the same
  socket
- `/debug/capabilities` reports the WebSocket local subset
- HTTP/SSE behavior remains unchanged

Regression checks:

```bash
go test ./internal/httpapi -run 'WebSocket|ResponsesStream|Codex'
go test ./...
make lint
git diff --check
```

## Smoke

Repo-owned smoke target:

```bash
make responses-websocket-smoke
```

The smoke connects to `ws://127.0.0.1:18080/v1/responses`, sends sequential
`response.create` messages, verifies stateful continuation, exercises direct
native local `shell` and `apply_patch` replay events, and covers the devstack
local families for `file_search`, `web_search`, `image_generation`, remote
MCP, cached MCP follow-up, hosted/server `tool_search`, and `tool_search`
follow-up.

The existing Codex CLI smokes were updated:

- HTTP 405 from `ws://.../v1/responses` now fails the smoke
- Codex must still complete the basic `exec_command` smoke
- Codex must still complete the scratch task matrix smoke

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

## Matrix Status

```text
Responses WebSocket mode | Broad subset | Keep local transport boundary
explicit | `/v1/responses` WebSocket accepts sequential `response.create`
messages for the full current shim-local Responses subset and emits existing
Responses streaming events as JSON text frames. No multiplexing, no upstream
WebSocket proxying, and no exact hosted connection quota/lifetime parity
claimed.
```

Do not use `Implemented` unless all supported HTTP local subsets have matching
WebSocket coverage, Codex CLI smoke proves WebSocket use without HTTP fallback,
and exact hosted edge cases are either implemented or explicitly judged out of
the label.

## Resolved Decisions

- WebSocket library: `github.com/coder/websocket`
- Runtime switch: `responses.websocket.enabled`, default `true`
- Capability shape: `.surfaces.responses.websocket`
- Smoke shape: direct `make responses-websocket-smoke` plus Codex CLI smokes
  that reject WebSocket HTTP 405
