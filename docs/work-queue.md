# Work Queue

Last updated: May 12, 2026.

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

The best next implementation slice is still V4 local/operator hardening, not
more V3 backend breadth.

Recommended order:

1. V4 local access boundaries for `/debug/*`, `/ui/`, and operator-only data.
2. Provider/model candidate expansion for DeepSeek Flash, MiMo non-Pro, and one
   local Gemma-family model.
3. V4 state/session memory hardening: promotion, deduplication, and redaction
   guardrails.
4. Documentation/script consolidation only where it removes operator confusion.

The model-candidate expansion should stay evidence-driven. Use
[Add Provider/Model Alias](guides/add-provider-model.md): add config and
metadata first, run provider-matrix smokes, then decide whether each model is
worth a Codex auto run.

Recently completed:

- V4 OpenTelemetry foundation: optional metadata-only OTLP trace export via
  `shim.telemetry.*`, with Phoenix as the recommended first local pilot backend.
  The guide includes local Phoenix and Laminar backend startup paths. See
  [Operations](guides/operations.md#opentelemetry-trace-export).

## Now

| Item | Status | Why it matters | Next slice | Validation |
| --- | --- | --- | --- | --- |
| V4 local access boundaries | Not started | The shim now has useful operator surfaces: `/debug/capabilities`, `/debug/traces`, `/debug/evidence`, and `/ui/`. Even for local use, these need a clear access model before more control-plane features grow. | Add a focused design/update for static bearer/local-only policy, then implement route grouping, config, tests, and guide updates. | `go test ./internal/httpapi ./internal/config`, `make v4-preflight-smoke`, `make lint` |
| Provider/model candidate expansion | Not started | The existing matrix is useful, but adding candidate rows will show whether DeepSeek Flash, MiMo non-Pro, or a local Gemma-family model should become practical gates. | Follow [Add Provider/Model Alias](guides/add-provider-model.md): add provider/model aliases, Codex metadata, `.env.example` placeholders if needed, and matrix docs. Start with provider-routing smoke before Codex eval. | `make v4-provider-config-doctor`, `make v4-provider-matrix-smoke`, `make v4-provider-matrix-curate`, `make v4-provider-ops-report` |
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
- [Add Provider/Model Alias](guides/add-provider-model.md)
- [V4 Model/Provider Operational Matrix](engineering/v4-model-provider-operational-matrix.md)
- [Operations Guide](guides/operations.md)
- [Script Inventory](script-inventory.md)
