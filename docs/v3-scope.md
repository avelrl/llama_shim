# V3 Expansion Staging

Last updated: May 5, 2026.

This document is the parking lot for work that did not make the V2 ship bar
and should not be reintroduced into the frozen V2 scope.

V2 is the broad compatibility facade release. V3 is where the project can
expand capabilities, add more backend diversity, and take on more expensive
runtime work without muddying the V2 release contract.

V3 now starts from the completed shim-owned automation and dev-stack substrate
documented in [v3-preflight.md](v3-preflight.md).

Current compatibility checkpoint:

- April 26, 2026: the real-upstream
  [`openai-compatible-tester`](engineering/responses-compatibility-external-tester.md)
  `strict` run passed through the shim with profile `llama-shim-kimi-k2.6`.
- The checkpoint supports keeping the current Responses status at
  `Broad subset`; it is not an exact hosted-parity claim.
- The same day, broader `compat` mode exposed one non-core Chat Completions
  tool follow-up budget edge after tool output. Responses stayed green, so this
  is not a V3 Responses blocker.

For work that goes beyond compatibility and into opinionated memory, plugin
architecture, or hardening, see [v4-scope.md](v4-scope.md).

For exact hosted-parity and advanced transport behavior that should not slow
down practical V3 rollout, see [v5-scope.md](v5-scope.md).

## V3 Entry Criteria

V3 starts from a frozen V2 release ledger and a current compatibility matrix:

- the per-surface status in [docs/compatibility-matrix.md](compatibility-matrix.md)
  is current
- the frozen release framing in [v2-scope.md](v2-scope.md) remains
  truthful
- OpenAPI, README, and backlog no longer imply exact hosted parity where the
  shim only offers a documented subset
- detailed historical notes remain recoverable from Git history before the V2
  freeze refactor

## Already Moved Out Of V2

These items are useful, but they are no longer part of the V2 ship bar:

- exact hosted/native tool-specific SSE replay beyond the current
  docs-backed and trace-backed core shim families
- exact hosted/native tool choreography and failure/status fidelity where docs
  alone do not pin the wire behavior down
- exact hosted parity for server-side compaction via
  `context_management.compact_threshold`, including encrypted payload fidelity
  and hosted stream choreography
- true constrained decoder/runtime for `grammar` / `regex` custom tools
- multi-tenant authz / tenanting / shared rate limiting
- richer exporters, dashboards, admin tooling, and governance-heavy storage work

## Candidate V3 Tracks

The tracks below assume the small preflight substrate in
[v3-preflight.md](v3-preflight.md) is already in place.

Status labels in this section are intentionally coarse:

- `Implemented`: the V3 slice is closed for the current practical scope.
- `Partial`: implementation has started, but material V3 phases remain.
- `Planned`: the track is accepted for V3, but still needs a first
  implementation slice.
- `Candidate`: useful V3 work, but not yet prioritized into a concrete
  implementation phase.

### 1. Alternative image backends

Status: `Planned`. See [v3-image-backends.md](v3-image-backends.md).

- Stable Diffusion / ComfyUI / other image-generation backends
- provider-specific image pipelines that are useful locally but are not part of
  the core OpenAI compatibility promise
- capability, config, and replay boundaries for additional shim-local
  `image_generation` providers without claiming hosted image-generation parity

### 2. More retrieval and storage backends

Status: `Partial`; SQLite storage contracts, the retrieval-index contract,
`sqlite_fts5`, `sqlite_vec`, the Postgres/pgvector path for retrieval objects,
responses, conversations, stored Chat Completions, Postgres hardening coverage,
backend-aware Postgres maintenance, and the first multi-instance shared-state
smoke are implemented. SQLite-to-Postgres migration tooling is implemented for
the current Postgres-owned beta tables, and code-interpreter state ownership is
implemented as an explicit per-instance SQLite sidecar boundary. Shim-owned
response replay-artifact retention is implemented for standalone responses.
Cluster-native Postgres backup guidance is documented as a runbook; backup
scheduling and retention remain operator-owned.
See
[v3-storage-retrieval-backends.md](v3-storage-retrieval-backends.md).

- ANN indexing beyond the current exact local subset
- broader Postgres / multi-instance storage modes beyond the current
  responses/conversations/stored-chat/file/vector boundary
- code-interpreter store ownership stays sidecar-local unless a future shared
  runtime design exists
- hard-delete/governance hooks and ANN index management
- more embedders and rerankers beyond the current compatibility-driven set

### 3. Richer local-only runtimes

Status: `Planned`. See [v3-local-runtimes.md](v3-local-runtimes.md).

- additional local tools that do not map cleanly to current OpenAI surface area
- more ambitious local shell / browser / multimodal runtime loops after the V2
  contract is stable
- deterministic fixture and capability-gated runtime slices before any
  production-grade runtime claims

### 4. Native coding tools for local execution

Status: `Implemented` as a `Broad subset` in
[compatibility-matrix.md](compatibility-matrix.md), with remaining exact hosted
choreography deferred to V5.

Implemented local scope:

- native local `shell` subset for `/v1/responses`
- native local `apply_patch` subset for `/v1/responses`
- typed item persistence for `shell_call`, `shell_call_output`,
  `apply_patch_call`, and `apply_patch_call_output`
- focused create/retrieve replay for the shim-owned traces documented in
  [v3-coding-tools.md](v3-coding-tools.md)
- real `openai/codex` smoke coverage against the shim via `openai_base_url`,
  including default `exec_command` bridge, fallback Codex function `shell`
  bridge, and deterministic task matrix coverage

See [v3-coding-tools.md](v3-coding-tools.md) for the design starting point and
implemented status.

This is a compatibility-quality and runtime-expansion track, not a reason to
reopen the frozen V2 contract before code, tests, and capabilities exist.

### 5. Deeper constrained decoding work

Status: `Partial`; first conservative runtime slice implemented as a
`Broad subset` in
[compatibility-matrix.md](compatibility-matrix.md). The default path still
does not claim backend-native constrained sampling. An optional vLLM adapter can
now claim `grammar_native` for `grammar.syntax=regex` and the shim-supported
Lark subset only when `responses.constrained_decoding.backend: vllm` is
configured and verified.

Implemented local scope:

- shared constrained custom tool runtime abstraction
- Chat Completions JSON Schema hint path for direct constrained custom tool
  generation
- final shim-local regex validation remains authoritative
- `/debug/capabilities` reports `support: shim_validate_repair`,
  `capability_class: none`, and `native_available: false`
- focused devstack smoke coverage through `make v3-constrained-decoding-smoke`
- optional vLLM `structured_outputs.regex` adapter for regex grammar custom
  tools and `structured_outputs.grammar` adapter for the shim-supported Lark
  subset
- backend adapter registry with explicit native-to-`shim_validate_repair`
  fallback for invalid native output, native timeouts, and native upstream
  errors
- `/debug/capabilities` reports `support:
  grammar_native_with_validate_repair_fallback`, `capability_class:
  grammar_native`, `native_formats: ["grammar.regex",
  "grammar.lark_subset"]`, and `native_available: true` only for the configured
  vLLM backend
- live vLLM smoke coverage through `make v3-vllm-constrained-smoke`

Remaining valid expansion areas:

- additional backend adapters beyond the current vLLM regex and Lark-subset
  grammar slice
- embedded constrained decoder/runtime libraries
- lower-level sampler/logits integrations
- SGLang and llama.cpp adapters after the vLLM grammar path is proven
- `json_schema_native` or broader `grammar_native` capability upgrades only
  after concrete enforcement is wired and tested

See [v3-constrained-decoding.md](v3-constrained-decoding.md) for the design
starting point and implemented status.

This is valuable work, but it is a runtime-expansion track, not a V2 facade
requirement.

### 6. Higher-fidelity compaction runtime

Status: `Implemented` as a `Broad subset` in
[compatibility-matrix.md](compatibility-matrix.md).

The closed slice covers model-assisted text compaction, retained-window
standalone output, automatic `context_management` compaction over local state,
capability visibility, and devstack smoke coverage. See
[v3-compaction.md](v3-compaction.md) for the exact scope and non-goals.

Tool-aware stateful compaction, multimodal-aware compaction, exact hosted
encrypted payload parity, and exact hosted stream choreography remain deferred.

### 7. Responses WebSocket mode

Status: `Implemented` as a `Broad subset` in
[compatibility-matrix.md](compatibility-matrix.md), with exact hosted close
codes, quotas, cache edges, and upstream WebSocket proxying deferred to V5.

Implemented local scope:

- WebSocket upgrade support on `/v1/responses`
- sequential `response.create` messages over one persistent socket
- Responses streaming events emitted as WebSocket JSON messages
- `previous_response_id` continuation over the socket, including a
  connection-local cache for the most recent `store=false` response
- WebSocket support for the full current shim-local Responses subset already
  supported through HTTP/SSE
- real Codex CLI smoke without HTTP fallback when WebSocket support is enabled

See [v3-websocket.md](v3-websocket.md) for the implementation status and
remaining parity boundaries.

This is a transport-quality track, not a reason to reopen the frozen V2 HTTP
contract. Exact hosted close codes, upstream WebSocket proxying, hosted cache
edge cases, and Realtime API WebSocket compatibility are deferred to
[v5-scope.md](v5-scope.md).

### 8. Codex eval harness and auto-regression loop

Status: `Implemented` for the current automated V3 scope. Phase 6
benchmark-lite and automated profile gates are implemented. See
[v3-codex-eval-harness.md](v3-codex-eval-harness.md).

The existing Codex CLI smokes are useful canaries, but manual Codex sessions
are not a scalable compatibility strategy. V3 now has a repo-owned eval
harness that runs real `codex exec --json` through the shim, captures Codex
JSONL, workspace snapshots, diffs, deterministic checker output, and
machine-readable failure buckets.

The goal is a practical auto-regression loop for local and OpenAI-compatible
upstreams such as Qwen 3.6:

- run a curated task suite through Codex and the shim
- classify failures as shim, transport, Codex tool registration, upstream
  model/tool-following, checker, or harness bugs
- use frontier-model review only for root-cause analysis and patch proposals
- convert manual failures into permanent deterministic tasks
- keep pass/fail owned by task checkers, not by an LLM judge

Implemented scope:

- `cmd/codex-eval-runner`
- manifest-backed task definitions under `internal/codexeval/testdata/tasks`
- isolated workspace and `CODEX_HOME` per task attempt
- generated Codex custom-provider config
- deterministic file, command, Codex event, and forbidden-output checkers
- `summary.json`, `summary.md`, `environment.json`, `checker.json`,
  `failure.md`, workspace snapshots, and `git.diff` artifacts
- `make codex-eval-smoke`, `make codex-eval-core`, and
  `make codex-eval-real-upstream`
- `make codex-eval-core-profiles`, `make codex-eval-shim-native-profiles`,
  `make codex-eval-compat`, and `make codex-eval-automated-profiles`
- `codex-real-upstream` and `codex-real-upstream-expanded` profiles
- `codex-eval-runner matrix`, `codex-eval-runner compare`, and
  `codex-eval-runner failure-bundle`
- `codex-eval-runner import-failure` for failed-run to regression-task
  skeleton import
- `make codex-eval-loop` for automated devstack-control versus real-upstream
  model orchestration
- `codex-bench-lite` profile plus `make codex-eval-bench-lite` and
  `make codex-eval-loop-bench-lite` for longer repo-owned benchmark-lite
  stability checks

Remaining V3 work in this track is result curation, trend reporting across
multiple run directories, and future imported benchmark tasks when they are
worth sanitizing. Manual TUI/app-server features stay in their own plan until
they have deterministic reductions. Shim-native Codex request-shape/profile
coverage is split into
[v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md).

This is a quality and automation track. It does not strengthen any hosted
OpenAI parity claim by itself.

### 9. Codex shim-native request-shape and profile coverage

Status: `Implemented` for automated profile coverage; manual feature
exploration remains separate. See
[v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md).

This track covers Codex-through-shim behavior that is not benchmark breadth:

- `write_stdin`/PTY continuation through the Chat-backed bridge
  ([v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md))
- redacted Codex HTTP Responses request-shape checks
- WebSocket `response.create` and `previous_response_id` continuation checks
- `apply_patch` freeform/function/disabled model-metadata profiles
- shell-tool profile variants that should stay out of the default core gate
  until stable
- manual Codex TUI and app-server feature exploration, tracked separately in
  [v3-codex-interactive-features-manual-plan.md](v3-codex-interactive-features-manual-plan.md)

This is a V3 compatibility-quality track. It should not claim exact hosted
parity and should not promote flaky profile tasks into `codex-core` or
`codex-real-upstream` until the profile has deterministic evidence.

### 10. Ops and deployment expansion

Status: `Partial`; Phase 0 inventory, the first bounded readiness-probe
metrics slice, the Postgres/pgvector beta multi-instance devstack profile, and
backend-aware storage maintenance are implemented. SQLite-to-Postgres migration
for the Postgres-owned beta tables is implemented in the storage track. See
[v3-ops-deployment.md](v3-ops-deployment.md).

- multi-tenant authz / tenant isolation
- richer exporters and dashboards
- governance-heavy storage features such as encryption-at-rest options,
  redaction policy, and hard-delete controls
- cluster-native Postgres backup scheduling/retention and remaining
  shared-state deployment modes

This track should stay behind storage-backend interface hardening. It must not
add hidden OpenAI-surface limits or tenant-specific request behavior that makes
the public compatibility story less truthful.

## Current V3 Implementation Queue

The next practical implementation work should be selected from this queue:

1. Continue [V3 Storage And Retrieval Backends](v3-storage-retrieval-backends.md)
   for the remaining Postgres beta work: hard-delete/governance hooks, ANN
   indexing, or additional embedders/rerankers.
2. Continue [V3 Ops And Deployment Expansion](v3-ops-deployment.md) only for
   governance/tenanting after the shared storage beta boundary is stable.
3. Turn [V3 Alternative Image Backends](v3-image-backends.md) into a first
   concrete backend slice only after selecting one reproducible provider path.
4. Turn [V3 Richer Local-Only Runtimes](v3-local-runtimes.md) into one focused
   runtime slice with deterministic fixtures and capability gates.

## V3 Result-Curation Work

The automated Codex eval work is now useful enough that V3 needs a standing
curation loop, not only one-off run inspection. The operational runbook is
[V3 Codex Eval Curation](v3-codex-eval-curation.md):

- summarize each `codex-eval-auto` run into the model matrix only after
  checking profile summaries and relevant shim logs
- classify failures as shim, transport, Codex tool registration, upstream
  model/tool-following, checker, harness, or environment
- keep retry-dependent passes visible instead of treating them as clean passes
- import manual or real-upstream failures as deterministic regression tasks
  only when they reduce to a stable repo-owned scenario
- prefer trend summaries across multiple run directories before changing a
  model's documented quality rating

## V3 Anti-Scope For Now

These items should not jump ahead of keeping the frozen V2 contract honest:

- new novelty backends just because they are easy to prototype
- new local-only features that force OpenAPI/backlog wording to become less
  honest
- exact hosted choreography work without a docs-backed or fixture-backed reason

## Working Rule

If a task mainly improves correctness, predictability, or explicit contract
boundaries for an official OpenAI surface the shim already exposes, it is
probably still V2.

If a task mainly adds backend diversity, local-only capability, or expensive
runtime sophistication beyond the V2 contract, it is probably V3.
