# V3 Expansion Staging

Last updated: April 26, 2026.

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

### 1. Alternative image backends

- Stable Diffusion / ComfyUI / other image-generation backends
- provider-specific image pipelines that are useful locally but are not part of
  the core OpenAI compatibility promise

### 2. More retrieval and storage backends

Status: foundation started. See
[v3-storage-retrieval-backends.md](v3-storage-retrieval-backends.md).

- ANN indexing beyond the current exact local subset
- Postgres / pgvector / multi-instance storage modes
- more embedders and rerankers beyond the current compatibility-driven set

### 3. Richer local-only runtimes

- additional local tools that do not map cleanly to current OpenAI surface area
- more ambitious local shell / browser / multimodal runtime loops after the V2
  contract is stable

### 4. Native coding tools for local execution

Status: closed as a `Broad subset` in
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

Status: first conservative runtime slice implemented as a `Broad subset` in
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

Status: closed as a `Broad subset` in
[compatibility-matrix.md](compatibility-matrix.md).

The closed slice covers model-assisted text compaction, retained-window
standalone output, automatic `context_management` compaction over local state,
capability visibility, and devstack smoke coverage. See
[v3-compaction.md](v3-compaction.md) for the exact scope and non-goals.

Tool-aware stateful compaction, multimodal-aware compaction, exact hosted
encrypted payload parity, and exact hosted stream choreography remain deferred.

### 7. Responses WebSocket mode

Status: closed as a `Broad subset` in
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

### 8. Ops and deployment expansion

- multi-tenant authz / tenant isolation
- richer exporters and dashboards
- governance-heavy storage features such as encryption-at-rest options,
  redaction policy, and hard-delete controls
- Postgres / multi-instance / shared-state deployment modes

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
