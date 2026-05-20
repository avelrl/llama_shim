# Work Queue

Last updated: May 20, 2026.

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

The best next practical slice is to validate V4 Chat Compatibility Layer with
live Chat clients and selected model certification runs. Slices 1-3 now
name/trace existing Chat repairs, lock conservative structured-output/tool-call
guardrails, and add the first evidence-backed streamed `<chatcmpl-tool>` form.

Recommended order:

1. Run [V4 Chat Compatibility Layer](v4-chat-compatibility-layer.md) live
   validation: `v4-chat-agent-smoke`, `v4-opencode-smoke`, and external Chat
   tester rows on one or two real candidates.
2. Run V4 model certification on one real candidate, then on a small batch.
3. V4 local access boundaries for `/debug/*`, `/ui/`, and operator-only data.
4. Provider/model candidate expansion through the certification runner.
5. V4 state/session memory hardening: promotion, deduplication, and redaction
   guardrails.
6. Documentation/script consolidation only where it removes operator confusion.

The model-candidate expansion should stay evidence-driven. Use
[Add Provider/Model Alias](guides/add-provider-model.md): add config and
metadata first, then use
[V4 Model Certification Runner](v4-model-certification-runner.md) once it is
implemented to run the external tester gate and Codex profiles consistently.

Recently completed:

- V4 model certification runner: `.env`-aware short API/Codex phases,
  isolated per-model shim startup, candidate-only Codex eval profiles,
  generated tester model configs, model-owned diagnostics for constrained
  `apply_patch` failures, stronger `apply_patch` format hints, and
  unified-diff hunk header repair for Codex tool-loop compatibility.
- V4 Chat-first coding-agent smoke: `/v1/chat/completions` streaming and
  function-tool workflow coverage for OpenCode/Aider-style local coding
  agents. See [V4 Chat Agent Smoke](v4-chat-agent-smoke.md).
- V4 OpenCode smoke: first real-client proof for
  `gpu/qwen3-coder30b-q5km` at
  `.tmp/v4-opencode-smoke/gpu-qwen3-coder30b-q5km_20260520T145203Z`,
  with post-Chat-Compatibility validation at
  `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder30b-q5km_20260520T175840Z` and
  `.tmp/v4-opencode-smoke/gpu-qwen3-coder30b-q5km_20260520T180029Z`,
  including streamed Chat tool calls, file edit, and `go test ./...`.
  `gpu/qwen3-coder-30b` also passed Chat-agent and OpenCode smokes at
  `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder-30b_20260520T194113Z` and
  `.tmp/v4-opencode-smoke/gpu-qwen3-coder-30b_20260520T194141Z`; keep that as
  supervised Chat/OpenCode evidence, not a Codex gate promotion.
  `gpu/glm47-flash-opus-reasoning` passed the same Chat/OpenCode path at
  `.tmp/v4-chat-agent-smoke/gpu-glm47-flash-opus-reasoning_20260520T194326Z`
  and `.tmp/v4-opencode-smoke/gpu-glm47-flash-opus-reasoning_20260520T194446Z`;
  keep it as reasoning/API diagnostics evidence because Codex baseline remains
  blocked.
- V4 OpenTelemetry foundation: optional metadata-only OTLP trace export via
  `shim.telemetry.*`, with Phoenix as the recommended first local pilot backend.
  The guide includes local Phoenix and Laminar backend startup paths. See
  [Operations](guides/operations.md#opentelemetry-trace-export).

## Now

| Item | Status | Why it matters | Next slice | Validation |
| --- | --- | --- | --- | --- |
| V4 Chat Compatibility Layer | Slices 1-3 implemented | Chat-first clients use `/v1/chat/completions` streaming, function tools, `response_format`, and `role=tool` loops. Several Responses-era repairs are useful here, but only after a `portable/adapt/no` classification. | Validate with live Chat clients and add future provider forms only from new real artifacts. | focused `internal/httpapi` tests, `make v4-chat-agent-smoke`, `make v4-opencode-smoke`, external Chat tester rows, `go test ./...`, `make lint` |
| V4 model certification runner | Implemented; first real candidate evidence captured | Model testing was too manual: endpoints, tokens, shim restarts, external tester reports, Codex profiles, and log interpretation were spread across separate commands. | Run [V4 Model Certification Runner](v4-model-certification-runner.md) on a small batch; harden only evidence-backed gaps in tester parsing, trace summaries, prompt repair, or retry policy. | `make model-certify-api`, `make model-certify-codex`, focused runner tests, `go test ./...`, `make lint` |
| Chat-first coding-agent smoke | Implemented first slice; OpenCode client smoke green for one model | Most non-Codex coding agents use OpenAI-compatible Chat Completions, so Codex-only evidence misses practical Aider/OpenCode/Qwen Code/Cline-style workflows. | Keep [V4 OpenCode Smoke](v4-opencode-smoke.md) as the real-client regression check for Chat Compatibility Layer changes. Add scenarios only after repeated real failures justify them. | `make v4-chat-agent-smoke`, `make v4-opencode-smoke`, `bash -n scripts/v4-chat-agent-smoke.sh scripts/v4-opencode-smoke.sh`, `make lint` |
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
| `gpu/qwen3-coder-30b` | Configured candidate; usable with supervision, not strict-clean; Chat-client green | Local GPU alias resolves to upstream runtime id `coder30b`; Codex metadata uses a 32768-token context window; `coder30b` has a Chat compatibility rule that downgrades JSON Schema to JSON mode plus schema instruction for llama.cpp sampler compatibility. Baseline certification passes after Codex-compat fixes. Bench-lite evidence `cert-20260519T162448Z` is 19/20: `command_recovery` stays fixed, raw tool markup is classified as `502 malformed_backend_response` instead of hidden `500`, but `patch_after_context` repeatedly misses the required edit. V4 Chat-first evidence passed: `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder-30b_20260520T194113Z` and `.tmp/v4-opencode-smoke/gpu-qwen3-coder-30b_20260520T194141Z`. | Keep as a fast local coding assistant for supervised Chat/OpenCode work. Do not promote as an unattended release gate unless a future bench-lite run is clean or an operator explicitly accepts the repeated `patch_after_context` miss. |
| `gpu/qwen3-coder30b-q5km` | Configured candidate; best current local Codex evidence, retry-dependent; Chat-client green | Local GPU alias resolves to upstream runtime id `coder30b-q5km`; Codex metadata uses a 32768-token context window. Certification run `cert-20260519T220117Z` passed all focused Codex profiles: baseline 11/11, expanded 18/18, bench-lite 20/20, for 49/49 final tasks. The run recovered 6 failed attempts and exposed raw-markup, malformed-backend, transport, and timeout signals. Post V4 Chat Compatibility Layer smokes passed: `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder30b-q5km_20260520T175840Z` and `.tmp/v4-opencode-smoke/gpu-qwen3-coder30b-q5km_20260520T180029Z`. API certification `cert-20260520T180503Z` stopped on external tester `chat.basic`: expected exact `OK`, model answered `pong`; invalid-shape `400` checks were expected validation behavior. | Prefer over `gpu/qwen3-coder-30b` for supervised local Codex and Chat-first coding-agent work. Do not promote as strict-clean or unattended release gate until retry rate and malformed-response noise are lower; do not treat the `pong` exactness miss as a shim regression. |
| `gpu/gemma4-e4b` | Configured chat/API candidate; Codex promotion blocked | API certification `cert-20260520T071938Z` passed all external tester rows. Codex baseline `cert-20260520T072422Z` failed 10/11 because the model printed a valid apply-patch body as final text instead of calling the edit tool. Shim logs also showed llama.cpp/Gemma parser errors on Codex tool transcript markers such as `<|tool_call>...`. | Keep as a local chat/API model. Do not promote for Codex unless a future Gemma-specific transcript cleanup/stringify strategy is implemented and a fresh baseline passes. |
| `gpu/omnicoder-9b` | Configured experimental; Codex blocked | Partial external tester evidence `llama_shim_omnicoder_9b_20260520_142219` showed core Responses paths working, but `previous_response_id` follow-ups returned `502 transport_error` after upstream `context canceled`. The same shim log shows `store=true` and conversations working, so this is not a broad storage/conversation regression. | Do not spend broader Codex time on this 9B model unless a focused API certification later shows stable `previous_response_id`. Keep it available for targeted continuation diagnostics only. |
| `gpu/glm47-flash-opus-reasoning` | Configured API/chat diagnostic; Chat-client green; Codex blocked | API certification `cert-20260520T115344Z` was 27/28 with only native `chat.stream` returning `H` instead of `HELLO`. Codex baseline `cert-20260520T120358Z` failed 8/11: `basic_patch` checker diff plus malformed constrained `apply_patch` responses on `bugfix_go` and `bugfix_mixed`. V4 Chat-first evidence passed: `.tmp/v4-chat-agent-smoke/gpu-glm47-flash-opus-reasoning_20260520T194326Z` and `.tmp/v4-opencode-smoke/gpu-glm47-flash-opus-reasoning_20260520T194446Z`. | Keep for reasoning-content/API diagnostics and supervised Chat/OpenCode checks. Do not promote as Codex gate unless a future baseline proves stable patch/tool-loop behavior. |
| local GPU batch (`qwen3_6-35b-a3b`, `glm4_7-flash`, `qwen35-27b-opus-reasoning`, `qwen3-coder-next`, `qwen3-30b-instruct`, `qwen3-next-instruct`, `gpt-oss-20b`, `gpt-oss-120b`) | Configured candidates; GPT-OSS Codex promotion blocked | Aliases are configured under the `gpu` provider with conservative Codex metadata unless a prior tested model already established a larger context. GPT-OSS candidates start at 32768 tokens and use Chat upstream compatibility cleanup for `developer` role and default thinking. Evidence on May 19, 2026: `gpt-oss-20b` is API/Responses usable but fails Codex baseline 6/11 and has native Chat stream issues; `gpt-oss-120b` is API/Chat/Responses ready but fails Codex baseline 7/11. `gpu/qwen3-30b-instruct` now has Chat-agent and OpenCode evidence: `.tmp/v4-chat-agent-smoke/gpu-qwen3-30b-instruct_20260520T134315Z` and `.tmp/v4-opencode-smoke/gpu-qwen3-30b-instruct_20260520T192306Z`. `qwen35-27b-opus-reasoning` is newly configured and still evidence-pending. | Keep GPT-OSS as API/assistant candidates, not unattended Codex gates. Next useful local Codex checks should prioritize stronger coder-specific models such as `gpu/qwen3-coder-30b` variants or `gpu/qwen3-30b-instruct`. |
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
