# AGENTS

## Scope

This repository is an OpenAI-compatible shim. Any task that touches `/v1/responses`, `/v1/conversations`, `/v1/chat/completions`, OpenAPI, backlog status, or compatibility claims must be checked against current official OpenAI docs before it is marked done.

## Required verification workflow

Use this order every time:

1. Start with the local docs index at [`openapi/llms.txt`](openapi/llms.txt).
   Use it as a map of relevant official pages only.
   It is not the source of truth for exact request or response contracts.
2. Use OpenAI Docs MCP tools against `developers.openai.com` and `platform.openai.com`.
   Search first, then fetch the exact page or section you need.
3. Do a final spot-check on the official site directly for the exact page or endpoint you are validating.
   Prefer only official OpenAI domains.
4. If MCP and the official site disagree, or MCP is thin, ambiguous, or missing exact schema details, treat the current official site page as the tie-breaker and update the backlog/spec conservatively.

Do not close a compatibility task from memory alone.

## What must be checked

For any OpenAI-surface task, verify all of these that apply:

- endpoint existence and HTTP method
- path params, query params, and defaults
- request body shape and limits
- response shape, including nested object form
- pagination semantics
- streaming event flow and event payload fields
- retention and storage semantics
- error behavior and unsupported-field behavior

Do not mark a task done if only the route exists but the documented semantics still differ.

## When fixtures are mandatory

Use a real upstream fixture before closing parity work when any of the
following is true:

- the task depends on exact SSE choreography rather than only final JSON shape
- the public docs confirm an item family exists but do not fully specify event
  order or event payload fields
- the task changes stored replay or retrieve streaming fidelity for hosted or
  native tools
- the task depends on exact artifact placement, annotation placement, or
  intermediate tool-progress events
- the task depends on exact failure/status semantics and the docs do not pin
  down whether the upstream surface completes, fails, or returns partial data
- MCP/OpenAI docs search, API reference, and the live public site disagree or
  leave a material ambiguity about observable wire behavior

In those cases:

1. Add or reuse a request template under
   `internal/httpapi/testdata/upstream/`.
2. Capture a real upstream trace with `cmd/upstream-sse-capture`.
3. Commit the sanitized request, raw SSE, and parsed fixture when they are
   needed to justify the implementation.
4. Keep backlog/spec wording conservative until the fixture-backed behavior is
   implemented and tested.

Typical examples where fixtures are expected:

- tool-specific `response.*` SSE families
- hosted/native tool replay parity
- `code_interpreter_call.outputs` or artifact/citation behavior
- `response.failed` vs `response.completed` ambiguity
- image-generation partial events

Fixtures are not mandatory when the public docs and reference already define
the relevant contract well enough and the shim is only implementing a
generic/docs-backed subset without claiming exact hosted choreography.

## Path hygiene

- In committed repo files, use repo-relative paths only.
- Do not add absolute local filesystem paths to docs, backlog items, comments,
  or examples.
- When linking to files from repo markdown, prefer relative markdown links.

## V2 scope rules

When updating [`docs/v2-scope.md`](docs/v2-scope.md):

- Treat it as the frozen V2 release ledger, not as a live scratch backlog.
- Keep shipped scope and non-goals aligned with the actual router, handlers,
  tests, and OpenAPI.
- If parity is only partial, document that boundary in
  [`docs/compatibility-matrix.md`](docs/compatibility-matrix.md) instead of
  silently widening the V2 claim.
- Use exact dates when re-validating the scope wording against docs.

## Scope triage rules

- Do not move work out of V2 merely because exact hosted parity is unavailable.
  If public docs define a core behavior for a surface already claimed by V2,
  and the missing behavior materially affects practical functionality, keep it
  in V2 as a conservative shim-local subset.
- Use [`docs/compatibility-matrix.md`](docs/compatibility-matrix.md) as the
  live source of truth for status labels such as `Implemented`, `Broad subset`,
  `Shim-owned`, and `V3`.
- Use [`docs/v3-scope.md`](docs/v3-scope.md) for parity-expansion or backend-
  expansion work that is useful but not required for the current V2 contract.
- Use [`docs/v4-scope.md`](docs/v4-scope.md) for shim-owned extensions and
  plugin/backend architecture that should not be framed as OpenAI API parity.

## Implementation rules for this repo

- For Responses and Conversations work, check both guide pages and API reference pages.
  Guides explain semantics; reference pages define contract details.
- Treat `include` support carefully.
  “Accepted as compatibility no-op” is not the same as fully implemented semantics.
- Treat retrieve streaming separately from create streaming.
  Route parity is not enough; replay event fidelity matters.
- Treat `input_items` as “items used to generate the response”, not just the current request body.
- For constrained custom tools, distinguish between:
  shim-local supported subset,
  repair/validation compatibility layer,
  and true constrained decoding/runtime parity.
- For `responses.mode`, verify behavior in `prefer_local`, `prefer_upstream`, and `local_only` when the task changes fallback behavior.
- For compaction or other cross-turn state-carrying features, verify all of:
  non-stream create, create-stream, retrieve-stream, `previous_response_id`,
  `conversation`, `/v1/responses/{id}/input_items`, and `responses.mode`.
- For tool-routing work, distinguish between:
  shim-local subset,
  proxy-only compatibility bridge,
  and exact hosted parity.
  In particular, keep `mcp.server_url` separate from `mcp.connector_id`, and
  keep hosted/server `tool_search` separate from client-executed
  `tool_search_output` flows.
- For stored Chat Completions work, treat these as separate scopes:
  local shadow-store ownership,
  streamed shadow-store reconstruction,
  and optional upstream history merge/bridge behavior.

## Mandatory security and regression guardrails

The commit range re-validated on 2026-04-19 exposed a repeating set of
mistakes. Treat the following as mandatory rules for new work on this repo.

### 1. Do not fix DoS by adding hidden OpenAI-surface regressions

- Do not add undocumented public limits to OpenAI-compatible endpoints just to
  stop memory growth.
- In particular, do not add shim-only caps such as:
  undocumented `limit` maxima,
  undocumented response-size ceilings,
  or new `502`/validation failures for payloads that are still valid per the
  official OpenAI surface.
- If a limit is only needed for optional shim-owned side effects
  (shadow-store, replay artifact persistence, debug capture, local snapshots),
  keep it internal-only, configurable, and non-fatal for the main client
  response path.

### 2. Avoid full materialization on hot request, replay, list, and rebuild paths

- Do not use `io.ReadAll`, unbounded `strings.Builder`, full-table slices, or
  per-item event accumulation on public or attacker-reachable paths unless the
  data is already hard-bounded by contract and tests prove it.
- Prefer:
  streaming transforms over buffer-then-rewrite,
  visitor/callback emission over `[]event` construction,
  metadata-first scans over full BLOB/file reads,
  SQL/keyset pagination over load-all-then-slice,
  incremental updates over full rebuilds.
- If a list endpoint does not return file or blob content, do not read content
  from storage just to build the page.
- If a replay path writes SSE incrementally, do not prebuild the full replay
  event list in memory.

### 3. Fix every reachable sibling path, not just the first hot path you saw

- When a bug is caused by full buffering, replay synthesis, or list/rebuild
  logic, review all sibling entrypoints that share the helper or pattern.
- Do not mark a fix done if one path is bounded but another equivalent path
  still materializes the same data.
- Typical sibling pairs to check in this repo:
  create-stream vs retrieve-stream,
  stored replay vs synthetic replay,
  local list path vs merged upstream bridge path,
  Docker snapshot path vs unsafe-host/local snapshot path,
  attach/update path vs delete/rebuild path.

### 4. Do not rely on fake language-level sandboxes as a security boundary

- Do not present Python monkeypatching, builtin wrapping, substring blacklists,
  or planner hints as real sandboxing.
- For code interpreter security, the real boundary is the runtime/container
  isolation layer. Prefer Docker hardening and explicit docs over fragile
  Python-level wrappers.
- If a shim-local safety layer is only advisory or best-effort, document it as
  such and do not overclaim filesystem/module isolation.

### 5. Keep limits centralized and docs-aware

- New operational limits belong in normalized config/service limit structs, not
  scattered `const` values hidden inside handlers.
- Wire new limits through config defaults, runtime normalization, tests, and
  example config together.
- If the limit affects only internal behavior, document it in repo docs/config,
  not as an OpenAI contract change.

### 6. Prefer conservative architecture over narrow patches

- For storage/listing:
  use SQL-side filtering/pagination where possible,
  use cursor/keyset pagination instead of load-all pagination,
  avoid reading unused columns.
- For retrieval/vector indexing:
  prefer incremental row updates or coalesced repair/rebuild paths over full
  rebuild-on-every-mutation.
- For snapshots and generated-artifact diffing:
  compare metadata first and fetch content lazily only for bounded candidates.
- For sanitization:
  prefer streaming/token-based rewriting over buffer -> unmarshal -> marshal of
  the whole payload.

### 7. Do not close partial mitigations as complete fixes

- If a patch removes only one amplification factor but leaves the same attack
  path reachable through another buffer, scan, rebuild, or replay path, keep
  the task open or split the remaining work into an explicit follow-up.
- Backlog, OpenAPI, README, and guides must not claim full closure if the code
  only implements a bounded subset or best-effort mitigation.

### 8. Mandatory tests for this class of changes

For any security, replay, pagination, storage, sandbox, or resource-bound fix,
the minimum bar is:

- focused unit tests for the new bound/streaming/helper behavior
- integration tests for the public endpoint happy path
- integration tests for the main oversized/adversarial path
- tests that the client-visible OpenAI contract did not regress
- tests for all sibling paths that share the same helper/pattern
- `go test ./...`
- `make lint`
- `git diff --check`

Add the following where applicable:

- If changing list pagination:
  test `after`, `order`, `has_more`, and that large unused fields are not read.
- If changing replay/SSE:
  test create-stream, retrieve-stream, and any synthetic replay helper.
- If changing shadow-store/capture behavior:
  test that overflow skips local persistence without breaking the client
  response.
- If changing code interpreter/container behavior:
  test owner isolation, adversarial input paths, and both metadata and content
  handling.
- If changing vector store indexing:
  test attach, update, delete, and rebuild/repair behavior separately.

### 9. Review checklist before merge

Before merging work in these areas, explicitly ask and answer:

- Did this fix remove the root cause, or only cap one manifestation?
- Did we accidentally change the public OpenAI contract to make the fix easier?
- Did we move the same bug to a sibling path?
- Is the bound internal-only and best-effort, or is it a real API behavior?
- Are docs, OpenAPI, config defaults, and tests aligned with that answer?

## Before calling a task done

Confirm all of the following:

- router/handler/service/storage behavior matches the intended scope
- [`openapi/openapi.yaml`](openapi/openapi.yaml) matches the implementation
- integration tests cover the happy path and the main contract edges
- `go test ./...` passes
- backlog wording does not overclaim beyond what the code and docs actually support
