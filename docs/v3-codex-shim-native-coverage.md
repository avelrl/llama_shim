# V3 Codex Shim-Native Coverage

Last updated: May 1, 2026.

Task id: `v3-codex-shim-native-coverage`

Status: request-shape capture and HTTP/WebSocket profile tasks implemented;
`apply_patch` tool-mode profiles remain planned follow-up work.

This document indexes Codex-through-shim compatibility gaps that are not just
benchmark breadth. They are shim-native or profile-specific behaviors that need
focused implementation and tests before they should be promoted into the
default `codex-core` or stable real-upstream gates.

The eval harness can expose these failures, but the fixes belong here because
they involve request-shape capture, model metadata profiles, tool bridge
semantics, or Chat-backed Responses behavior in the shim.

## References Checked

Checked on May 1, 2026:

- local official-docs index: [openapi/llms.txt](../openapi/llms.txt)
- OpenAI docs:
  - [Codex configuration reference](https://developers.openai.com/codex/config-reference)
  - [WebSocket Mode](https://developers.openai.com/api/docs/guides/websocket-mode)
  - [Evaluation best practices](https://developers.openai.com/api/docs/guides/evaluation-best-practices)
  - [Codex app-server API overview](https://developers.openai.com/codex/app-server)
- local `openai/codex` source checkout at commit
  `9121132c8f5412ae99c36363409759baa7e004f9`

Relevant source/docs constraints:

- Codex custom providers use `wire_api = "responses"`; current Codex does not
  support Chat Completions as a native provider wire API.
- Codex config exposes `features.unified_exec`, `features.shell_tool`,
  `features.apply_patch_freeform`, `model_catalog_json`,
  `developer_instructions`, and provider-level `supports_websockets`.
- WebSocket mode uses `response.create`, optional `generate: false`, and
  incremental continuation via `previous_response_id`.
- Current Codex source serializes `stream` into
  `ResponseCreateWsRequest`; this Codex-native profile checks the observed
  Codex request shape and does not present that field as hosted OpenAI parity.
- Codex app-server command APIs include `command/exec`,
  `command/exec/write`, resize, terminate, and output-delta notifications,
  which confirms that interactive command continuation is a real Codex runtime
  concept even when this shim tests it through `codex exec --json`.

## Scope

### 1. Interactive Command Session Bridge

Dedicated task:
[v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md).

Summary: keep `write_stdin_pty` in `codex-core-interactive` and `codex-compat`
until `exec_command` reliably preserves live session state, exposes a stable
model-visible `session_id`, and routes later `write_stdin` calls to the correct
process through both HTTP Responses and Chat-backed Responses paths.

### 2. Codex Request-Shape Capture And Profile Checks

Status: implemented on May 1, 2026.

Problem:

The harness validates task outcomes well, but request shape also needs to be a
first-class checker. Otherwise subtle regressions in headers, provider config,
model metadata, and transport mode are harder to diagnose.

Implemented coverage:

- HTTP Responses request shape sent by Codex:
  - path and method;
  - redacted authorization;
  - `x-client-request-id`;
  - `session_id` header when present;
  - `model`, `instructions`, `input`, `tools`, `tool_choice`, `stream`,
    `parallel_tool_calls`, `reasoning`, `store`, `include`, `text`, and
    `client_metadata` presence/absence as applicable;
  - advertised tool names and tool types.
- WebSocket request shape:
  - `response.create` message shape;
  - warmup `generate: false`;
  - generated request with incremental `previous_response_id`;
  - redacted handshake headers;
  - unexpected WebSocket dial/pump errors captured as diagnostic
    `websocket_error` request-shape entries.
- Generated Codex config knobs:
  - `supports_websockets`;
  - `features.unified_exec`;
  - `features.apply_patch_freeform`.

Run commands:

```bash
make codex-eval-shim-native
make codex-eval-shim-native-websocket
make codex-eval-shim-native-profiles
```

Artifacts:

- every task attempt that declares `expected.request_shapes` writes
  `.tmp/codex-eval-runs/<run-id>/tasks/<task-id>/attempt-XX/request-shapes.json`;
- the artifact is bounded and redacts sensitive headers such as
  `authorization`, API keys, tokens, secrets, and cookies;
- the checker matches redacted artifacts deterministically through
  `expected.request_shapes` in the task manifest.

Implemented tasks:

- `request_shape_http` in `codex-shim-native`;
- `request_shape_websocket` in `codex-shim-native-websocket`.

Boundary:

These tasks intentionally live outside the default `codex-core` and
`codex-real-upstream` gates. They prove what the current Codex CLI sends
through the shim, including implementation-specific details from the inspected
Codex source, and they should not be used as standalone evidence for a stronger
OpenAI hosted parity claim.

### 3. `apply_patch` Tool-Mode Profiles

Problem:

Codex can expose `apply_patch` in more than one shape depending on features and
model metadata. The stable real-upstream tasks prove practical file edits, but
they do not fully cover freeform-vs-function `apply_patch` contracts.

Required coverage:

- freeform `apply_patch` advertised and used;
- function-style `apply_patch` advertised and used, if still supported by the
  current Codex model metadata path;
- disabled `apply_patch` profile falls back to command/file-change behavior
  without claiming native patch coverage;
- invalid patch argument repair remains a shim-local compatibility layer and is
  not presented as exact hosted parity.

Required tests:

- generated Codex config/model catalog fixture for each profile;
- event checker proving a patch/file-change path was exercised;
- request-shape checker proving the intended tool shape was advertised.

### 4. WebSocket Incremental Continuation Profile

Status: request-shape path implemented; failure-state invalidation remains in
the broader V3 WebSocket track.

Problem:

The default HTTP eval path sends full request context. WebSocket mode can send
incremental inputs with `previous_response_id`, which is a different request
shape and a different shim state path.

Implemented coverage:

- WebSocket-enabled Codex profile uses WebSocket transport, not HTTP fallback;
- the second turn sends only incremental input plus `previous_response_id`;
- `store=false` behavior is handled as a connection-local cache path;

Remaining coverage:

- failed continuation invalidates the relevant cached state where applicable.

This profile belongs with V3 WebSocket coverage, but it is tracked here because
Codex is the caller whose request shape needs to be observed.

## Non-Goals

- Do not convert provider pseudo-tool markup such as `<read_file>` or
  `<tool_call>` text into real tool calls. Detecting and retrying obviously
  malformed model output is acceptable; inventing tool execution from plain text
  is not.
- Do not claim exact hosted Codex or OpenAI parity from these evals.
- Do not block the default deterministic `codex-core` gate on interactive,
  WebSocket, or metadata-profile tasks until they are stable.
- Do not fold benchmark-lite task import into this track; benchmark breadth
  remains Phase 6 of [v3-codex-eval-harness.md](v3-codex-eval-harness.md).

## Exit Criteria

This follow-up is done when:

- `write_stdin_pty` passes in a dedicated interactive profile without relying
  on model-visible luck; tracked in
  [v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md)
- request-shape artifacts are redacted, bounded, and checked deterministically
  (done);
- HTTP and WebSocket Codex request-shape profile tasks exist (done);
- freeform and function `apply_patch` profile checks exist or the unsupported
  mode is explicitly documented against the current Codex source;
- docs and run commands are linked from
  [docs/guides/codex-cli.md](guides/codex-cli.md) and
  [docs/v3-codex-eval-harness.md](v3-codex-eval-harness.md);
- `go test ./...`, `make lint`, and `git diff --check` pass.
