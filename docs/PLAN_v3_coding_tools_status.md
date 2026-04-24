# V3 Coding Tools Status Decision

Last updated: April 24, 2026.

This document records the completed decision for moving native local `shell`
and `apply_patch` out of a purely staged V3 label in
[compatibility-matrix.md](compatibility-matrix.md).

It is intentionally conservative. It does not change the V2 contract and does
not claim hosted shell/container parity.

## Current Status

Current label:

- `Broad subset`

Current wording says:

- native local `shell` and `apply_patch` are implemented only for the
  shim-local Responses subset
- hosted `container_auto`, `container_reference`, `network_policy`,
  `domain_secrets`, and exact hosted shell container behavior remain out of
  scope
- `shell_call` retrieve-stream remains generic through `response.output_item.*`
  until upstream background shell replay is fixture-backed
- `apply_patch_call` create/retrieve-stream emits
  `response.apply_patch_call_operation_diff.done`; `delta` is present only
  when the stored `operation.diff` is non-empty

## Closure Evidence

The matrix label was strengthened because all of these are true:

1. Official docs were rechecked on the day of the change:
   - [Shell](https://developers.openai.com/api/docs/guides/tools-shell)
   - [Apply Patch](https://developers.openai.com/api/docs/guides/tools-apply-patch)
   - [Streaming](https://developers.openai.com/api/docs/guides/streaming-responses)
   - current `POST /v1/responses` and streaming API references
2. Deterministic tests pass:
   - focused native coding-tools tests from
     [TEST_v3_coding_tools.md](TEST_v3_coding_tools.md)
   - `go test ./...`
   - `make lint`
   - `git diff --check`
3. `/debug/capabilities` exposes:
   - `.tools.shell.support == "native_local_subset"`
   - `.tools.apply_patch.support == "native_local_subset"`
4. Repo-owned smoke passes:
   - `make devstack-up`
   - `make v3-coding-tools-smoke`
   - `make codex-cli-devstack-smoke`
   - `make codex-cli-coding-task-smoke`
   - `make devstack-down`
5. A real Codex CLI smoke against the shim passes with `openai_base_url`
   pointing at the shim, including a coding-task smoke that changes a scratch
   workspace file.

## Codex CLI Smoke Acceptance

The Codex smoke should prove practical client compatibility, not just route
existence.

Minimum pass for this combined status gate:

- Direct Responses smoke covers at least one native local `shell` request and
  one native local `apply_patch` request.
- Codex CLI is configured to use the shim as its OpenAI-compatible base URL.
- Codex CLI can request at least one local command through the current
  compatibility bridge.
- Codex CLI can perform at least one file edit through native `apply_patch` if
  the public CLI emits that declaration, or through the current compatibility
  bridge if it has not switched yet.
- Stored follow-up still works through `previous_response_id`.
- The shim log shows successful `/v1/responses` requests without upstream
  fallback being mistaken for local native support.

If Codex still uses the older function/custom-tool bridge, the matrix may say
the native subset is implemented and smoke-tested through direct Responses
requests, but it must not claim that current Codex CLI uses native `shell` and
`apply_patch` declarations end to end.

## Latest Codex CLI Result

Last checked on April 24, 2026 with `codex-cli 0.124.0`.

Result: pass with the built-in `openai_base_url` setting pointed at the shim.

Observed details:

- At the time of this coding-tools status check, Codex CLI first attempted the
  Responses WebSocket transport, received HTTP 405 from
  `ws://127.0.0.1:18080/v1/responses`, then fell back to HTTP and completed
  the turn.
- The smoke exercised the current Codex compatibility bridge with
  `exec_command`, not native `shell` / `apply_patch` declarations from the CLI.
- The deterministic devstack fixture returned a planned `exec_command`; Codex
  executed `pwd` and then received final assistant text `READY`.
- The repo-owned coding-task smoke used the same real CLI path in a scratch
  workspace; Codex executed a deterministic `exec_command`, changed
  `smoke_target.txt`, and then received final assistant text `PATCHED`.

Status implication: this is enough evidence for practical Codex CLI bridge
compatibility and for closing the shim-local coding-tools status as
`Broad subset`, but it is not evidence that the current public Codex CLI build
uses the native `shell` and `apply_patch` tool declarations end to end.

Follow-up: WebSocket transport support is tracked in
[v3-websocket.md](v3-websocket.md). The later WebSocket work removes tolerated
HTTP 405 fallback from the Codex CLI smoke before making a WebSocket
compatibility claim.

## Status Decision

Decision: native local `shell` and native local `apply_patch` are closed as
`Broad subset` rows in the compatibility matrix.

The matrix wording answers these questions without ambiguity:

- What exact local subset is implemented?
- Which modes use the local subset?
- Which hosted/runtime features still fall through or fail validation?
- Which SSE families are fixture-backed?
- Which retrieve-stream paths remain generic?
- Did the real Codex CLI smoke exercise native tools or only the compatibility
  bridge?

Any future widening beyond this status requires new docs/fixture evidence and a
new status decision.

## Matrix Wording

The active wording is:

```text
native local `shell` tool contract | Broad subset | Keep local-only boundary
explicit | Shim-local Responses accepts `shell` with
`environment.type="local"`, preserves `shell_call`/`shell_call_output`, and
supports stored follow-up plus create-stream shell command events. Hosted
containers and shell retrieve-stream command events remain out of scope.
```

```text
native local `apply_patch` tool contract | Broad subset | Keep local-only
boundary explicit | Shim-local Responses accepts `apply_patch`, preserves
`apply_patch_call`/`apply_patch_call_output`, and supports stored follow-up
plus create/retrieve-stream `operation_diff.done`; `delta` depends on a
non-empty stored diff.
```
