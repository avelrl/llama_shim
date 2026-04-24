# V3 Coding Tools

Last updated: April 24, 2026.

This document records the V3 coding-tools design and the implemented
shim-local status for the current HTTP/SSE track.

It does not change the frozen V2 contract.
It does not claim new OpenAI-surface parity before code, tests, and
capabilities exist.

## Why This Exists

The shim already has a useful Codex-oriented bridge path:

- stateful `/v1/responses`
- stored follow-up through `previous_response_id`
- typed item preservation for the current shipped tool families
- Codex compatibility mode that injects shim-local guidance for
  `exec_command` and `apply_patch`

That is valuable, but it is still a compatibility bridge around function/custom
tool flows rather than the current official Responses coding-tool contract.

The current official OpenAI docs now define coding-oriented built-in tools that
matter directly for Codex and similar clients:

- `shell` for hosted or local shell execution
- `apply_patch` for structured file edits

The official Codex docs also explicitly support pointing the built-in OpenAI
provider at a proxy or router with `openai_base_url`.

That makes a new V3 track worthwhile:

- keep the existing bridge path as the current working baseline
- add a narrow shim-local subset of the official Responses-native coding tools
- validate the result against a real `openai/codex` smoke path before widening
  compatibility wording

This is a runtime-expansion and compatibility-quality track, not a reason to
reopen the frozen V2 ledger.

## Official References Reviewed

This design note was re-checked on April 24, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Shell](https://developers.openai.com/api/docs/guides/tools-shell)
  - [Apply Patch](https://developers.openai.com/api/docs/guides/tools-apply-patch)
  - [Code generation](https://developers.openai.com/api/docs/guides/code-generation)
  - [Codex advanced config](https://developers.openai.com/codex/config-advanced)
- current official API reference for `POST /v1/responses`

The practical takeaway from the current official docs is:

- `shell` is a current Responses tool and supports a local execution mode
- local shell uses `shell_call` and `shell_call_output`
- `apply_patch` is a current Responses tool for structured diffs
- apply-patch follow-up uses `apply_patch_call_output`
- Codex can target an OpenAI-compatible proxy by setting `openai_base_url`

## Current Fixture Findings

The upstream captures recorded on April 23, 2026 narrowed the open questions
for this V3 track:

- first-turn `shell_call` create-stream uses shell-specific SSE:
  - `response.shell_call_command.added`
  - `response.shell_call_command.delta`
  - `response.shell_call_command.done`
- first-turn `apply_patch_call` create-stream uses patch-specific SSE:
  - `response.apply_patch_call_operation_diff.delta`
  - `response.apply_patch_call_operation_diff.done`
- background-created `apply_patch_call` retrieve-stream replays the same
  patch-specific SSE family rather than collapsing to generic
  `response.output_item.*` only
- both first-turn traces start with an incomplete item in
  `response.output_item.added` and only expose the finalized item in
  `response.output_item.done`
- follow-up traces for `shell_call_output` and `apply_patch_call_output`
  currently behave like ordinary assistant-message streams rather than
  introducing a second tool-specific SSE family
- upstream validates `shell_call_output.max_output_length` against the original
  `shell_call.action.max_output_length`
- upstream `GET /v1/responses/{id}?stream=true` currently rejects non-background
  responses, so retrieve-stream parity for these tool families requires a
  separate background-created fixture lane
- current background `shell_call` attempts are still blocked by upstream
  `response.failed` / `server_error` before any `shell_call` item is emitted;
  this has now been reproduced on both `gpt-5.4` and `gpt-5.3-codex`, so shell
  retrieve-stream parity remains unresolved and should be treated as an
  upstream blocker rather than a single-model mismatch
- the follow-up diagnostics narrowed that further: a docs-literal minimal
  `background + stream + local shell` request still fails with the same
  `server_error`, and a non-streaming `background + local shell` request moves
  from `queued` to `failed` with the same `server_error`; this no longer looks
  like an issue with our extra `instructions` field or with background
  streaming alone

That means exact create-stream choreography for the first tool turn is no
longer docs-thin; it is now fixture-backed. Retrieve-stream parity is now
fixture-backed for `apply_patch_call`, while `shell_call` retrieve parity
remains a separate capture problem.

## Current Code Status

As of April 24, 2026, the repo has closed the shim-local HTTP/SSE
implementation for this track as a `Broad subset`:

- shim-local `/v1/responses` accepts official local `shell` and `apply_patch`
  tool declarations
- `/debug/capabilities` exposes `shell` and `apply_patch` as
  `native_local_subset`
- the local tool loop accepts current Codex CLI no-op request metadata such as
  empty `include`, `prompt_cache_key`, and `client_metadata` without falling
  through to upstream proxy
- Codex tool-output follow-up can finish with a final assistant message instead
  of being forced into another required tool call
- first-turn create-stream replay for local `shell_call` now emits:
  - `response.shell_call_command.added`
  - `response.shell_call_command.delta`
  - `response.shell_call_command.done`
- create-stream and stored retrieve-stream replay for local
  `apply_patch_call` now emit:
  - `response.apply_patch_call_operation_diff.delta`
  - `response.apply_patch_call_operation_diff.done`
- synthetic `response.output_item.added` snapshots match the current
  fixture-backed local subset:
  - `shell_call` starts with empty `action.commands` plus `null`
    `timeout_ms` / `max_output_length`
  - `apply_patch_call` starts with empty `operation.diff`
- `shell_call` stored retrieve-stream remains conservative and generic through
  `response.output_item.*` because upstream background shell replay is still
  blocked
- the repo-owned real Codex CLI task matrix verifies actual scratch workspace
  edits and a tiny Go bugfix through the current `exec_command` compatibility
  bridge

This is enough to mark the native local tool rows in the compatibility matrix
as implemented broad subsets. It is not a full hosted parity claim, and the
current public Codex CLI smoke still exercises the compatibility bridge rather
than native `shell` / `apply_patch` declarations end to end.

## Manual Live Smoke

The shim-local V3 coding-tools runbook was manually exercised on April 24,
2026 with a Qwen-compatible upstream model behind the shim. The run covered:

- non-stream `shell_call` creation and `shell_call_output` follow-up
- stored shell retrieve and `/input_items`
- non-stream `apply_patch_call` creation and `apply_patch_call_output`
  follow-up
- stored apply-patch retrieve and `/input_items`
- shell create-stream replay with `response.shell_call_command.*`
- shell retrieve-stream preserving `shell_call` through generic
  `response.output_item.*`
- apply-patch create-stream and retrieve-stream replay with
  `response.apply_patch_call_operation_diff.done`

The Qwen-compatible live run also confirmed two practical boundaries:

- prompts for the local shell smoke should ask for an explicit command; vague
  forced-tool wording can stall or time out on non-OpenAI upstreams even when
  the upstream supports tool calls
- `response.apply_patch_call_operation_diff.delta` is only expected when the
  stored `operation.diff` is non-empty; a structured operation with an empty
  diff should still emit `response.apply_patch_call_operation_diff.done`

This live smoke improves confidence in the shim-local subset, but it does not
replace the conservative status decision in
[PLAN_v3_coding_tools_status.md](PLAN_v3_coding_tools_status.md).

## Codex CLI Smoke

The real Codex CLI smoke was exercised on April 24, 2026 with
`codex-cli 0.125.0`, the built-in `openai_base_url` setting pointed at the
shim, and the deterministic devstack fixture behind the shim.

Observed result:

- The Codex CLI smoke now treats HTTP 405 from `ws://.../v1/responses` as a
  failure; the devstack run completed with the WebSocket-capable shim path.
- The turn exercised the current Codex function-tool bridge with
  `exec_command`; Codex executed `pwd` and then received final assistant text
  `READY`.
- A separate fallback-shell smoke runs Codex with
  `features.unified_exec=false` and verifies that the stored request uses the
  Codex function tool named `shell`, without `exec_command` or `write_stdin`.
- A follow-up repo-owned task matrix smoke uses the same real CLI path in
  scratch workspaces and verifies a single-file patch, a tiny Go bugfix,
  deterministic `PLAN.md` creation, and a two-file update.

This proves practical compatibility for the current Codex CLI bridge path with
the Responses WebSocket-capable shim. The Responses WebSocket track is recorded
in [v3-websocket.md](v3-websocket.md); this coding-tools note still does not prove
that the public Codex CLI is sending native `shell` or `apply_patch` tool
declarations.

## Current V2 Baseline

The frozen V2 truth remains:

- `POST /v1/responses` is a `Broad subset` in
  [compatibility-matrix.md](compatibility-matrix.md)
- create-stream and retrieve-stream intentionally avoid claims of exact hosted
  tool choreography where docs or fixtures do not pin it down
- the current Codex path is a compatibility bridge, not a shipped claim that
  the shim already supports the official native `shell` or `apply_patch` tool
  families

This document does not reopen those claims.

## Implemented Local Scope

The closed V3 coding-tools rollout is intentionally narrow.

### `shell`

The shim supports the local subset of the official Responses shell contract:

- accept `tools: [{"type":"shell","environment":{"type":"local"}}]`
- preserve `shell_call` typed output items
- accept `shell_call_output` follow-up items
- keep the loop compatible with stored responses and `previous_response_id`

### `apply_patch`

The shim supports the local subset of the official Responses apply-patch
contract:

- accept `tools: [{"type":"apply_patch"}]`
- preserve `apply_patch_call` typed output items
- accept `apply_patch_call_output` follow-up items
- keep the loop compatible with stored responses and `previous_response_id`

### Stateful Surfaces Included In Scope

The implemented local subset covers all of:

- non-stream `POST /v1/responses`
- create-stream
- `GET /v1/responses/{id}`
- retrieve-stream
- `GET /v1/responses/{id}/input_items`
- stored follow-up through `previous_response_id`

The existing shim-owned conversation/state substrate is reused, and the
compatibility claim stays anchored to the Responses surfaces above.

## Working Assumptions

The coding-tools track starts from the following assumptions:

- the shim should target the current official Responses tool names rather than
  inventing a second local naming scheme
- the first rollout should implement only the narrow local subset the shim can
  own honestly
- hosted container semantics are a separate problem from local shell execution
- typed item preservation matters more than clever prompt-based normalization
- exact hosted SSE choreography should stay out of scope until docs or fixtures
  pin it down; the first-turn `shell` and `apply_patch` create-stream families
  are now fixture-backed, while retrieve-stream still needs background traces

## Routing Policy

The existing `responses.mode` contract stays in force.
V3 should refine runtime routing without rewriting the public mode model.

### `prefer_local`

- use the shim-local native coding-tools subset when the request fits the
  supported local shape
- keep upstream fallback explicit when the request asks for unsupported hosted
  or wider runtime behavior
- do not silently widen local claims beyond the implemented subset

### `prefer_upstream`

- remain proxy-first
- do not rewrite the public docs story into "full native coding tools" unless
  the shim actually owns the local path end to end
- keep hosted-only shell features as upstream territory unless the shim later
  implements them explicitly

### `local_only`

- require the local subset to be available
- reject unsupported hosted-only shell fields or unsupported apply-patch shapes
  explicitly
- do not fall back to the current Codex bridge path in a way that makes the
  public contract ambiguous

## Output And Replay Policy

The implemented local subset keeps the replay story conservative and
observable:

- store `shell_call`, `shell_call_output`, `apply_patch_call`, and
  `apply_patch_call_output` as typed items
- preserve those item families in retrieve and input-items reads
- first-turn create-stream for local `shell_call` replays the dedicated
  `response.shell_call_command.*` family with collapsed deltas from stored
  final state
- create-stream and stored retrieve-stream for local `apply_patch_call`
  replay the dedicated `response.apply_patch_call_operation_diff.*` family
  with collapsed deltas from stored final state
- stored `shell_call` replay stays generic through `response.output_item.*`
  until upstream background shell replay is fixture-backed
- avoid claims of exact hosted chunk boundaries or full hosted retrieve-stream
  choreography beyond the fixture-backed local subset

## `/debug/capabilities` Direction

The capability manifest should grow beyond the current Codex compatibility
toggle and answer:

- whether native local `shell` is available
- whether native local `apply_patch` is available
- whether the process is still relying only on the older Codex compatibility
  bridge
- which routing modes can use the local subset

This keeps the V3 work visible to operators, testers, and autonomous clients.

The current manifest exposes this under:

- `.tools.shell`
- `.tools.apply_patch`

Both entries use `support: "native_local_subset"` and
`backend: "chat_completions_tool_loop"` to make the bridge-vs-native boundary
visible without claiming hosted container parity.

## Closure Evidence

The local slice is considered closed because coverage includes:

- request-shape and validation tests for `shell` and `apply_patch`
- integration tests for non-stream local tool loops
- integration tests for create-stream and retrieve-stream replay
- stored follow-up coverage through `previous_response_id`
- `/input_items` coverage for the new typed item families
- mode coverage for `prefer_local`, `prefer_upstream`, and `local_only`
- `/debug/capabilities` coverage for `.tools.shell` and `.tools.apply_patch`
- `make v3-coding-tools-smoke` against the deterministic dev stack
- a repo-owned real Codex CLI smoke path that points Codex at the shim with
  `openai_base_url`
- `make codex-cli-shell-tool-smoke`, which verifies the Codex fallback function
  tool named `shell` when `features.unified_exec=false`
- `make codex-cli-task-matrix-smoke`, which verifies real scratch workspace
  edits, a tiny Go bugfix, and deterministic planning output through Codex CLI

The current Codex bridge tests remain part of the baseline because current
Codex CLI still uses the bridge instead of emitting native `shell` /
`apply_patch` tool declarations end to end.

## Non-Goals For The Local Subset

The closed V3 coding-tools local subset does not try to do all of the
following at once:

- implement hosted shell container parity
- implement `container_auto` or `container_reference`
- implement hosted shell `network_policy` or `domain_secrets`
- claim full hosted chunk-for-chunk SSE choreography for shell or apply patch
- replace the current Codex bridge before the native path is proven
- widen compatibility wording before code, tests, and capabilities are aligned

## Completed Rollout Shape

The implemented rollout is:

1. kept the current bridge path and V2 wording intact
2. added local `shell` request parsing and typed item storage
3. added local `apply_patch` request parsing and typed item storage
4. added follow-up handling for `shell_call_output` and
   `apply_patch_call_output`
5. exposed the new local-vs-bridge state in `/debug/capabilities`
6. added the focused repo-owned smoke path
7. added real Codex CLI smoke paths, including a task matrix smoke that
   changes scratch workspace files and verifies a tiny Go bugfix

That is the narrowest practical closure from the current Codex bridge to a real
V3 native coding-tools broad-subset status.
