# Runtime Hardening

## What It Is

This guide is the place for shim-internal reliability and performance changes
that are worth documenting but do not belong in the `v*` scope ledgers and do
not justify new request preflight behavior.

Use it for operator-facing or engineering-facing notes about how the shim
stays stable when the upstream backend is slow, saturated, or slightly
inconsistent.

This guide is explicitly not:

- a new compatibility claim
- a replacement for the compatibility matrix
- a backlog ledger
- a reason to add new public validation on OpenAI-compatible routes

## What Belongs Here

- upstream backpressure, bounded queueing, and concurrency gating
- HTTP transport and connection reuse tuning
- shim-local continuation, replay, and state-reconstruction hardening
- internal-only storage or replay bounds that must not change the public API
- observability, metrics, and error-classification improvements
- pragmatic fast paths for shim-owned subsets that already fit the documented
  surface

## What Does Not Belong Here

- new API routes
- widening V2, V3, V4, or V5 scope claims
- undocumented request validation added to public OpenAI-compatible endpoints
- parity claims that still need fixture-backed or docs-backed validation
- work that belongs in the compatibility matrix or OpenAPI

## Safe Patterns

- add bounded internal admission control before slow upstream model calls
- tune keep-alive, idle connections, and transport behavior to reduce avoidable
  connection churn
- keep retries to idempotent read paths such as retrieve, list, or input-item
  lookup
- preserve shim-local state ownership for `previous_response_id` and
  `conversation`
- trim or compact local context only when the request semantics stay intact
- improve logging and metrics so upstream timeouts are distinguishable from
  contract failures

## Patterns To Avoid

- adding preflight rejection on hot create paths just because an upstream
  dependency is currently slow
- retrying side-effecting `POST /v1/responses` requests after timeout
- introducing undocumented public caps on otherwise valid OpenAI-compatible
  payloads
- silently degrading tool semantics while still claiming compatibility parity
- moving a runtime problem into a public API behavior change because that is
  easier to implement

## Current Notes

As of 2026-04-21:

- `responses.mode=prefer_local` means the shim owns routing, local state, and
  supported local tool/runtime subsets first; it does not guarantee fully local
  inference latency
- the common text-generation path may still call the configured upstream text
  backend even when the shim owns the outer `/v1/responses` surface
- under concurrency, a slow upstream is more likely to show up first as p95/p99
  latency growth and timeout storms than as deterministic contract bugs
- for that class of issue, internal backpressure and transport tuning are
  preferable to new public preflight or undocumented API limits

## Working Notes

These notes are allowed to document near-term hardening work before it is
implemented, but they must stay conservative:

- describe the symptom and intended invariant clearly
- avoid implying that a planned mechanism has already shipped
- avoid turning this guide into a scope ledger or feature roadmap

### 2026-04-21: Upstream admission control and transport tuning

- Status: implemented
- Symptom: when several agents or concurrent compatibility scenarios hit the
  shim at once, the upstream text backend may slow down sharply, pushing p95
  and p99 latency high enough to trigger timeout cascades
- Root cause: the shim currently has local concurrency gates for some
  shim-owned runtimes such as retrieval and code interpreter, but not for the
  main upstream model-call hot path; it also uses default HTTP client behavior
  without explicit upstream transport tuning
- Invariant: valid `POST /v1/responses` traffic must not gain a new
  undocumented compatibility preflight or a shim-only contract regression just
  to make overload easier to manage
- Change:
  add bounded internal admission control before slow upstream model calls and
  hold the slot until the proxied or streamed upstream body is closed; tune the
  upstream HTTP transport for explicit connection reuse and host-level caps
- Operator surface:
  new internal knobs live under `llama.max_concurrent_requests`,
  `llama.max_queue_wait`, and `llama.http.*` in the shim config; these are
  shim-only runtime controls, not OpenAI-surface request fields
- Observability:
  emit `shim_upstream_admission_total`, `shim_upstream_queue_wait_ms`,
  `shim_inflight`, and `shim_queued` metrics, and log slow or failed admission
  waits
- Non-goals:
  do not retry side-effecting create requests after timeout, do not add public
  undocumented payload caps, and do not reframe a runtime protection as a new
  OpenAI API behavior
- Verification used:
  targeted client tests for serialized admission, queue timeout, and proxy-body
  slot ownership; config parsing tests for the new runtime knobs; metrics
  integration coverage; `go test ./...`; `go vet ./...`; `git diff --check`

### 2026-04-21: Startup calibration and recommendation mode

- Status: implemented
- Symptom: operators can see that the upstream is alive via `/readyz`, but that
  does not tell them whether the hot path is merely alive or already too slow
  for the intended agent concurrency
- Root cause: the current readiness path checks basic backend reachability, but
  it does not measure representative startup latency for the common upstream
  text-generation path
- Invariant: startup probing must not become a new public compatibility
  preflight for normal `POST /v1/responses` traffic, and it must not mutate
  OpenAI-visible request or response semantics
- Intended change:
  add an operator-facing probe mode that performs a small number of short
  deterministic upstream probes, records observed latency, and emits
  conservative runtime recommendations through `shimctl probe`
- Current implementation:
  recommendation-only mode; `shimctl probe` reads `probe.*` from the shared
  `config.yaml`, checks `GET /v1/models`, runs a small number of short
  `POST /v1/chat/completions` probes, and prints a conservative recommendation
  for `llama.max_concurrent_requests` and queue slack without auto-changing
  runtime behavior
- Possible later stage:
  optional internal auto-tuning of shim-only upstream admission limits or queue
  budgets, with explicit opt-in and hard min/max clamps
- Non-goals:
  do not reject valid create traffic because calibration looks bad, do not
  auto-tune undocumented public API limits, and do not use calibration as a
  reason to add side-effecting request retries
- Verification plan:
  confirm that `shimctl probe` stays informational in recommendation mode,
  that it reflects the difference between simple readiness and real hot-path latency,
  and produces stable enough guidance to help operators size safe upstream
  concurrency without changing the external API contract
- Verification used:
  targeted client tests for successful and failed calibration runs, config
  parsing tests for the shared `config.yaml` `probe.*` knobs, `go test
  ./...`, `go vet ./...`, and `git diff --check`

### 2026-04-25: Responses proxy body buffering

- Status: implemented
- Symptom: several non-stream Responses proxy fallback paths needed to inspect
  an upstream body for shim-owned retry, normalization, canonical error, or
  shadow-store behavior before writing the client response
- Invariant: the shim must not add a public OpenAI-surface response-size cap;
  oversized upstream responses must still be proxied to the client unchanged
- Change:
  bound the internal non-stream `/v1/responses` proxy buffer with
  `shim.limits.responses_proxy_buffer_bytes`; when the upstream body exceeds
  that internal limit, the shim writes the captured prefix plus the remaining
  body to the client and skips only shim-owned normalization/local persistence
- Covered paths:
  create proxy/shadow-store, buffered Responses proxy helper, streamed
  non-SSE/error fallback, and cancel refresh fallback
- Operator surface:
  `shim.limits.responses_proxy_buffer_bytes` is a shim-only runtime knob, not
  an OpenAI API request field or compatibility limit
- Verification used:
  focused overflow tests for create, cancel, stream fallback, and prefix
  preservation; config parsing/default/env tests; full runtime smokes before
  and after implementation; `go test ./...`; `make lint`; `git diff --check`

### 2026-04-25: Responses stored lineage reconstruction

- Status: implemented
- Symptom: shim-owned `previous_response_id` context reconstruction and legacy
  `/v1/responses/{id}/input_items` fallback could walk an unbounded stored
  response chain, and each ancestor read used the full response row including
  `response_json`
- Invariant: the shim must not add a new public OpenAI request limit or reject
  otherwise valid Responses requests just to bound local storage work
- Change:
  bound stored response lineage reconstruction with
  `shim.limits.responses_stored_lineage_max_items` and read lineage ancestors
  through a metadata/input/output query that does not select `request_json` or
  `response_json`
- Covered paths:
  local create context from `previous_response_id`, legacy input-items
  reconstruction for rows without an effective-input snapshot, and
  `/v1/responses/{id}/input_items` pagination behavior for large local item
  snapshots
- Operator surface:
  `shim.limits.responses_stored_lineage_max_items` is a shim-only storage and
  context retention knob, not an OpenAI API request field
- Verification used:
  focused storage tests for bounded newest-ancestor lineage and lean row reads;
  service tests proving configured limit propagation; integration tests for
  large input-item pagination and bounded legacy lineage fallback; config
  parsing/default/env tests; `go test ./...`; `make lint`; `git diff --check`

### 2026-04-28: Responses local tool-output final-answer context

- Status: implemented
- Symptom: client-executed Codex tool outputs were dropped from shim-local
  final Responses generation because the generic local-text projector keeps
  only message items. Some upstreams then tried to call the same tool again and
  surfaced raw tool-call template markers as assistant text.
- Invariant: tool output may be used as local generation context, but it must
  remain an internal prompt aid, not a new OpenAI request field or unbounded
  prompt amplification path.
- Change:
  inject a bounded text summary of function/custom/shell/apply_patch output
  items into shim-local final generation, repair raw tool-call marker drafts
  once, and buffer post-tool streaming final answers until raw marker checks
  pass.
- Operator surface:
  `shim.limits.responses_local_tool_output_summary_bytes` caps the internal
  tool-output summary injected into local generation.
- Verification used:
  service tests for tool-output projection, summary truncation, raw marker
  repair, and streaming post-tool buffering; config parsing/default/env tests.

### 2026-04-25: Stored Chat Completions pagination

- Status: implemented
- Symptom: local stored Chat Completions list and messages routes could do more
  local storage work than needed before returning a bounded page
- Root cause: the list path scanned stored completion rows and filtered
  metadata in Go, and the messages path rebuilt every request message from the
  stored request JSON on each call
- Invariant: the shim must not add a public `limit` maximum or reject valid
  OpenAI-compatible stored-chat list/message requests to bound local work
- Change:
  use SQL keyset pagination for local stored-chat list, filter metadata in SQL,
  and persist a per-message snapshot for new shadow-stored rows so
  `GET /v1/chat/completions/{completion_id}/messages` can page through message
  rows without reading the full stored completion body
- Compatibility note:
  older database rows without message snapshots still fall back to the captured
  request JSON; that fallback is compatibility-only and does not widen the
  current stored-chat parity claim
- Operator surface:
  `shim.limits.chat_completions_shadow_store_bytes` remains an internal
  best-effort persistence budget; oversized upstream responses are still
  proxied to the client, and only local shadow-store persistence is skipped.
  `shim.limits.chat_completions_shadow_store_timeout` bounds the internal
  SQLite write after a successful upstream response; it is detached from client
  disconnect cancellation and does not change the public Chat Completions
  response contract
- Verification used:
  focused storage tests for SQL-paginated message snapshots, cascade deletion,
  and canceled request-context shadow-store persistence; integration tests for
  stored-chat list/message pagination and oversized non-stream shadow-store
  overflow; devstack smoke now covers stored Chat Completions list/get/messages;
  `go test ./...`; `make lint`; `git diff --check`

## Change Note Template

When a hardening change lands, add a short note with:

- symptom
- root cause
- invariant that must not change
- internal mechanism added
- verification used

Example:

```md
### 2026-04-21: Upstream admission control

- Symptom: concurrent agent turns caused timeout storms against a slow upstream.
- Root cause: the shim allowed unbounded concurrent model calls on the hot path.
- Invariant: valid `POST /v1/responses` traffic must not gain a new undocumented
  preflight rejection mode.
- Change: add bounded internal admission control and expose queue/in-flight
  metrics.
- Verification: targeted concurrent runs, full compatibility reruns, and
  contract regression tests.
```

## Related Docs

- [Operations](../guides/operations.md)
- [Responses](../guides/responses.md)
- [Tools Overview](../guides/tools.md)
- [Compatibility Matrix](../compatibility-matrix.md)
- [V2 Scope](../v2-scope.md)
