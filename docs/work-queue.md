# Work Queue

Last updated: May 18, 2026.

Status: operator-maintained queue for choosing the next practical work item.
This is not a compatibility matrix and not an OpenAI API parity claim. Detailed
scope boundaries remain in the linked V2/V3/V4/V5/V6 documents.

## How To Use This Queue

Use this document before starting a new large slice. It answers three questions:

- what is most useful next
- which work is parked until there is a concrete trigger
- which command family should validate the result

Keep each row short. If a row needs design detail, create or update the linked
scope/runbook document instead of expanding this queue.

## Current Recommendation

The best next implementation slice is V4 model certification automation. The
provider/model surface now has enough routing, preflight, Codex eval, curation,
and evidence tooling that the manual model loop should be replaced by one
repeatable runner before adding more candidate rows.

Recommended order:

1. V4 model certification runner for external tester plus Codex eval batches.
2. V4 local access boundaries for `/debug/*`, `/ui/`, and operator-only data.
3. Provider/model candidate expansion through the certification runner.
4. V4 state/session memory hardening: promotion, deduplication, and redaction
   guardrails.
5. Documentation/script consolidation only where it removes operator confusion.

The model-candidate expansion should stay evidence-driven. Use
[Add Provider/Model Alias](guides/add-provider-model.md): add config and
metadata first, then use
[V4 Model Certification Runner](v4-model-certification-runner.md) once it is
implemented to run the external tester gate and Codex profiles consistently.

Recently completed:

- V4 OpenTelemetry foundation: optional metadata-only OTLP trace export via
  `shim.telemetry.*`, with Phoenix as the recommended first local pilot backend.
  The guide includes local Phoenix and Laminar backend startup paths. See
  [Operations](guides/operations.md#opentelemetry-trace-export).

## Now

| Item | Status | Why it matters | Next slice | Validation |
| --- | --- | --- | --- | --- |
| V4 model certification runner | Designed, not started | Model testing is still too manual: endpoints, tokens, shim restarts, external tester reports, Codex profiles, and log interpretation are spread across separate commands. | Implement [V4 Model Certification Runner](v4-model-certification-runner.md): manifest, isolated shim lifecycle, external tester compat gate, Codex phase, artifacts, and fix-candidate summaries. | focused runner tests, `go test ./...`, `make lint`, single-model dry run |
| V4 local access boundaries | Not started | The shim now has useful operator surfaces: `/debug/capabilities`, `/debug/traces`, `/debug/evidence`, and `/ui/`. Even for local use, these need a clear access model before more control-plane features grow. | Add a focused design/update for static bearer/local-only policy, then implement route grouping, config, tests, and guide updates. | `go test ./internal/httpapi ./internal/config`, `make v4-preflight-smoke`, `make lint` |
| Provider/model candidate expansion | Blocked on runner | The existing matrix is useful, but candidate rows are noisy to evaluate by hand. | Use the certification manifest as the candidate queue, then promote models only after external tester and Codex evidence exist. | `make model-certify`, then existing provider ops reports |
| Documentation and script inventory | Baseline implemented | There are many scripts and scope docs. Operators need a small map so new work does not require rediscovering the repo every time. | Keep this queue plus [Script Inventory](script-inventory.md) current. Consolidate script docs only after repeated confusion. | `git diff --check`; docs-only review |
| V4 memory hardening | Not started | The memory baseline exists, but durable state gets more useful after explicit promotion, deduplication, conflict, and redaction rules. | Implement promotion rules from session to global memory plus deterministic dedup/conflict behavior. Keep it shim-owned metadata, not an OpenAI memory claim. | focused memory tests, `go test ./...`, `make v4-preflight-smoke` |

## Next

| Item | Status | Trigger | Notes |
| --- | --- | --- | --- |
| V4 retrieval-backed knowledge extension | Not started | A real workflow needs richer ingestion, reranking, or external vector-store adapters. | Keep separate from state/session memory. Retrieval is for corpora and source-grounded knowledge, not stable user/task facts. |
| V4 hybrid memory orchestration | Not started | Memory, retrieval, and compaction need one policy decision point. | Do after memory hardening, otherwise the policy has too many unstable inputs. |
| V4 opaque state encryption | Not started | Compaction or shim-owned continuation artifacts leave trusted local development and move into less trusted storage. | Keep client-visible `encrypted_content` semantics unchanged. This is local confidentiality hardening, not hosted encrypted-state parity. |
| V4 local execution hardening | Not started | Code-interpreter or local runtime usage becomes production-like. | Prefer runtime/container isolation over language-level restrictions. |
| Operator UI evidence polish | Implemented baseline, future polish open | Operators need faster triage in the UI. | Useful later: trace filters, refresh/pause, capability diff, doc route, live readiness polling. Keep read-only unless a separate admin surface is approved. |

## Parked

| Item | Status | Why parked |
| --- | --- | --- |
| V5 exact Responses WebSocket hosted parity | Parked | Requires fixture-backed hosted behavior for close codes, cache eviction, quota, exact errors, and tool choreography. V3 already has the practical shim-local subset. |
| V5 upstream WebSocket proxying | Parked | Useful only if provider deployments need raw upstream `/v1/responses` WebSocket passthrough. Current shim owns local WebSocket behavior. |
| V5 Realtime API compatibility | Parked | Realtime is a different WebSocket surface from Responses WebSocket mode. Do not mix it into V3/V4. |
| V6 routing runtime Stage 0 | Designed, not started | Valuable but large: alias gate, private run log, worker registry, routing policy, no-leak tests, continuation and idempotency. Start only when there is time for a real vertical slice. |
| V3 constrained decoding new adapters | Candidate | Add SGLang, llama.cpp, or `json_schema_native` only with adapter-level tests proving native enforcement. |
| V3 storage/retrieval breadth | Candidate | Postgres/pgvector beta is no longer blocking. Add embedders, rerankers, or shared-storage modes only for a concrete operator/runtime need. |

## Model Candidate Queue

| Candidate | Status | First check | Promotion rule |
| --- | --- | --- | --- |
| `deepseek/deepseek-v4-flash` | Candidate | Verify the live provider catalog and add a configured alias only if the upstream accepts it. | Promote only after provider matrix smoke and at least one curated Codex auto run. |
| `xiaomi/mimo-v2.5` | Candidate | Verify whether the Xiaomi endpoint exposes this exact model id and whether it has the same request-shape needs as `mimo-v2.5-pro`. | Promote only if it adds value over the existing MiMo Pro row. |
| `gpu/qwen3-coder-30b` | Configured candidate | Local GPU alias resolves to upstream runtime id `coder30b`; Codex metadata uses a 32768-token context window. | Promote only after provider-routing/preflight smoke and a small Codex eval profile prove tool-call discipline. |
| local GPU batch (`gemma4-e4b`, `qwen3_6-35b-a3b`, `glm4_7-flash`, `qwen3-coder-next`, `qwen3-30b-instruct`, `qwen3-next-instruct`) | Configured candidates | Aliases are configured under the `gpu` provider with conservative Codex metadata unless a prior tested model already established a larger context. | First check live `/v1/models`, then run provider-routing/preflight for each served alias before any Codex eval promotion. |
| local Gemma-family model | Candidate | Choose the served model id from the local runtime, such as Ollama or LM Studio, then add a local provider alias. | Start with provider-routing/preflight only. Run Codex eval only after basic tool-call discipline is proven. |

Do not promote candidate model names from memory. Check the live upstream model
catalog, then record the exact public alias, upstream model id, and evidence
path in the provider matrix.

## Documentation And Script Hygiene

Use [Script Inventory](script-inventory.md) for the command map. The target is
not fewer files at any cost; it is fewer unclear entrypoints.

Useful cleanup rules:

- keep `Makefile` as the stable operator entrypoint
- keep scripts when they own non-trivial orchestration or artifact contracts
- prefer one runbook per operator workflow over inline command blocks in many
  unrelated docs
- remove stale raw model ids only after alias-normalized evidence exists
- keep generated run artifacts out of committed docs unless the artifact is a
  sanitized fixture needed for a compatibility claim

## Source Documents

- [V3 Scope](v3-scope.md)
- [V4 Scope](v4-scope.md)
- [V5 Scope](v5-scope.md)
- [V6 Model Routing Runtime](v6-routing-runtime.md)
- [V4 Provider Ops Runbook](v4-provider-ops-runbook.md)
- [V4 Model Certification Runner](v4-model-certification-runner.md)
- [Add Provider/Model Alias](guides/add-provider-model.md)
- [V4 Model/Provider Operational Matrix](engineering/v4-model-provider-operational-matrix.md)
- [Operations Guide](guides/operations.md)
- [Script Inventory](script-inventory.md)
