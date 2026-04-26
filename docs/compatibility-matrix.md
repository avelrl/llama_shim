# V2 Compatibility Matrix

Last updated: April 26, 2026.

This document is the source of truth for the current V2-compatible surface of
`llama_shim`.

V2 is scoped as a broad compatibility facade over the current official OpenAI
surface already exposed by the shim. The goal is not exact hosted orchestration
for every tool family. The goal is a predictable, docs-aligned contract with
explicit boundaries.

Status legend:

- `Implemented`: shipped and not currently blocking V2
- `Broad subset`: usable and documented, but intentionally not full hosted parity
- `Shim-owned`: useful local surface, but not an OpenAI compatibility claim
- `V3`: intentionally moved out of the V2 ship bar

Primary official references reviewed for this matrix:

- [Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [Using tools](https://developers.openai.com/api/docs/guides/tools)
- [Shell](https://developers.openai.com/api/docs/guides/tools-shell)
- [Apply Patch](https://developers.openai.com/api/docs/guides/tools-apply-patch)
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Compaction](https://developers.openai.com/api/docs/guides/compaction)
- [Counting tokens](https://developers.openai.com/api/docs/guides/token-counting)
- [Data controls in the OpenAI platform](https://developers.openai.com/api/docs/guides/your-data)
- [Codex advanced config](https://developers.openai.com/codex/config-advanced)
- current official API endpoint list, including
  `/v1/responses`, `/v1/conversations`, `/v1/chat/completions`,
  `/v1/files`, and `/v1/vector_stores`

The frozen V2 release ledger lives in [v2-scope.md](v2-scope.md).

Practical usage guides live in [guides/README.md](guides/README.md).

Current external evidence:

- April 26, 2026: `openai-compatible-tester` real-upstream `strict` mode passed
  through the shim with profile `llama-shim-kimi-k2.6`; see
  [Responses Compatibility External Tester](engineering/responses-compatibility-external-tester.md#latest-real-upstream-ledger).
- April 26, 2026: `compat` mode exposed one broader Chat Completions
  `chat.tool_call` budget edge after tool-output submission
  (`finish_reason: "length"`, `message.content: null`). Responses remained
  green. This does not strengthen or weaken the Responses `Broad subset` claim;
  it is tracked as upstream/model behavior for the broader Chat profile.

## Responses And Conversations

| Surface | Current shim status | Freeze guidance | Notes |
| --- | --- | --- | --- |
| `POST /v1/responses` | Broad subset | Keep the local-first stateful contract explicit | Local-first path is real and stateful; the hosted/native tool contract is fixed for the V2 facade scope. April 26, 2026 real-upstream external `strict` tester passed without changing the boundary to exact hosted parity. |
| `GET /v1/responses/{id}` | Implemented | Keep docs and OpenAPI aligned | Stored response ownership is already in the shim |
| `DELETE /v1/responses/{id}` and `POST /v1/responses/{id}/cancel` | Implemented | Keep lifecycle semantics honest | No known V2 blocker beyond lifecycle wording |
| `GET /v1/responses/{id}/input_items` | Implemented | Keep item-history semantics stable | Effective input snapshot is already persisted |
| create-stream and retrieve-stream | Broad subset | Keep core SSE stable; do not overclaim exact hosted choreography | Generic replay is accepted where docs or fixtures do not lock down tool-specific event families |
| Responses WebSocket mode | Broad subset | Keep the local transport boundary explicit | `GET /v1/responses` now accepts WebSocket upgrades, processes sequential `response.create` messages for the current shim-local Responses subset, and emits existing Responses streaming events as JSON text frames. `/debug/capabilities` advertises the local subset. No multiplexing, no upstream WebSocket proxying, and no exact hosted close-code/quota/cache parity claimed; those remain in [v5-scope.md](v5-scope.md). |
| `responses.mode=prefer_local|prefer_upstream|local_only` | Broad subset | Keep the per-tool matrix aligned with implementation | Per-tool matrix and integration coverage now span the supported hosted/native tool families; exact hosted choreography stays out of scope |
| `POST /v1/conversations` | Implemented | Keep aligned with Responses state model | Durable conversation ids are already part of the baseline |
| `GET /v1/conversations/{id}` | Implemented | Keep read-model honest | |
| `GET/POST/DELETE /v1/conversations/{id}/items*` | Implemented | Keep canonical append/delete flow centralized | |
| `/v1/responses/input_tokens` | Broad subset | Keep “local deterministic estimate” wording explicit | V2 does not require exact upstream tokenization parity |
| `/v1/responses/compact` | Broad subset | Keep standalone compaction subset explicit | V3 adds `model_assisted_text` canonical next-window output with retained items plus a shim-owned opaque compaction item; exact hosted encrypted compaction state is not claimed |
| server-side compaction via `context_management.compact_threshold` | Broad subset | Keep the local subset explicit and conservative | The shim accepts the documented request policy shape and supports the core local text/stateful path for non-stream create plus generic create-stream and retrieve-stream replay; V3 adds model-assisted local state compression and capability visibility, while exact hosted encrypted payload parity and exact hosted choreography remain out of scope |

## Chat Completions

| Surface | Current shim status | Freeze guidance | Notes |
| --- | --- | --- | --- |
| `POST /v1/chat/completions` | Broad subset | Keep sanitization, streaming, and store policy explicit | Still a major compatibility surface even though Responses is primary. Real-upstream `compat` testing can expose model-budget-dependent tool follow-up failures that are outside the current Responses gate. |
| stored Chat Completions `list/get/update/delete/messages` | Broad subset | Keep the local-first compatibility contract explicit | Shim-owned shadow-stored resources cover the core surface even when upstream stored-chat routes are absent. Local list uses SQL keyset pagination and SQL metadata filtering; new message snapshots use SQL pagination, with legacy request-JSON fallback only for older rows. |
| stored Chat Completions upstream merge/fallback behavior | Broad subset | Keep implemented vs upstream-only behavior explicit in docs and tests | Upstream history remains an optional compatibility bridge; the shim does not imply full hosted stored-chat ownership |
| streamed shadow-store reconstruction | Broad subset | Keep current boundaries explicit | Current subset covers practical assistant-text and tool-call-heavy flows, not every possible hosted chunk shape. Oversized best-effort capture skips local persistence without changing the proxied client response. |

## Files, Vector Stores, And Retrieval

| Surface | Current shim status | Freeze guidance | Notes |
| --- | --- | --- | --- |
| `/v1/files` CRUD | Implemented | Keep retrieval/file-input contract explicit | Shim owns local storage |
| `/v1/vector_stores` CRUD | Implemented | Keep local retrieval subset explicit | |
| `/v1/vector_stores/{id}/files*` | Implemented | Keep failed-binary indexing behavior explicit | |
| `/v1/vector_stores/{id}/search` lexical + semantic + hybrid local subsets | Broad subset | Keep ranking semantics and filters explicit | Local substrate is already usable |
| retrieval ranking contract (`ranker`, `score_threshold`, `hybrid_search`) | Broad subset | Keep the docs-backed ranking knobs explicit | Local search supports the documented ranker names plus shim-local `none`; exact hosted reranker quality is not claimed |

## Tools In Responses

| Surface | Current shim status | Freeze guidance | Notes |
| --- | --- | --- | --- |
| custom functions / custom tools | Broad subset | Keep request/repair/fallback/error behavior explicit | This is part of the facade contract |
| constrained custom tools `grammar` / `regex` | Broad subset | Keep capability-backed subset wording | Default V3 constrained-runtime slice exposes shim-local validate/repair plus a Chat Completions JSON Schema hint through `/debug/capabilities` and reports `capability_class: none` / `native_available: false`. Optional `responses.constrained_decoding.backend: vllm` reports `grammar_native` for `grammar.syntax=regex` and the shim-supported Lark subset, using vLLM `structured_outputs.regex` / `structured_outputs.grammar` plus final local validation guardrails and shim validate/repair fallback for native invalid/timeout/upstream-error cases. Broader Lark parity is not claimed. |
| local `file_search` | Broad subset | Keep retrieval/result/citation subset explicit | Already usable end-to-end |
| local `web_search` | Broad subset | Keep hosted-vs-local boundary explicit | Exact hosted search parity is intentionally not part of the V2 promise |
| local `image_generation` | Broad subset | Keep separate image-backend contract explicit | Current subset is docs-aligned, not exact hosted planner parity |
| local `computer` | Broad subset | Keep external-loop contract explicit | Current subset is screenshot-first and intentionally generic on replay |
| local `code_interpreter` | Broad subset | Keep dev-only/local boundary explicit | Current contract is useful but not hosted-equivalent |
| native local `shell` tool contract | Broad subset | Keep the local-only boundary explicit and do not claim hosted/container parity | Shim-local Responses accepts native `shell` with `environment.type="local"`, preserves `shell_call` / `shell_call_output`, supports stored follow-up and input-items history, and exposes capability flags. Create-stream replays first-turn `response.shell_call_command.*`; retrieve-stream remains generic. Current Codex CLI 0.125 default smokes pass through the `exec_command` bridge; a separate `features.unified_exec=false` smoke verifies the Codex function tool named `shell`. Neither Codex path is evidence that the CLI emits native Responses `{"type":"shell"}` declarations. |
| native local `apply_patch` tool contract | Broad subset | Keep the local-only boundary explicit and do not claim hosted/container parity | Shim-local Responses accepts native `apply_patch`, preserves `apply_patch_call` / `apply_patch_call_output`, supports stored follow-up and input-items history, and exposes capability flags. Create-stream and retrieve-stream replay `response.apply_patch_call_operation_diff.done`, with `operation_diff.delta` only when the operation diff is non-empty. Current Codex CLI 0.125 smokes pass through the existing function-tool bridge, including a task matrix that edits scratch files and verifies a tiny Go bugfix, not through native `apply_patch` declarations emitted by the CLI. |
| remote MCP | Broad subset | Keep `server_url` vs `connector_id` boundary explicit | `server_url` subset is implemented; connectors remain a stricter compatibility boundary |
| `tool_search` | Broad subset | Keep runtime and passthrough boundaries explicit | Current subset is already docs-backed |
| hosted/native tool-specific SSE beyond current core traces | V3 | Only take on exact replay work when docs or fixtures make it necessary | Current V3 code now replays `response.shell_call_command.*` for first-turn shim-local `shell_call` create-stream and `response.apply_patch_call_operation_diff.done` for shim-local `apply_patch_call` create/retrieve replay, with `operation_diff.delta` only when `operation.diff` is non-empty. Shell retrieve-stream remains generic because upstream background shell replay is still blocked, and the narrower April 23 diagnostics suggest a broader upstream `background + local shell` blocker rather than a problem isolated to stream replay. The April 24 manual live smoke is recorded in [v3-coding-tools-test-runbook.md](engineering/v3-coding-tools-test-runbook.md). |

## Current Mode Matrix

This is the current V2-facing routing contract for `POST /v1/responses` as of
April 24, 2026. It is intentionally conservative: `prefer_upstream` remains a
proxy-first escape hatch for standalone hosted/native requests, while
`prefer_local` is the default mode for the shim facade.

| Tool or surface | `prefer_local` | `prefer_upstream` | `local_only` | Notes |
| --- | --- | --- | --- | --- |
| core local Responses subset | local first, fallback upstream on unsupported subset | proxy first for standalone requests; local-state follow-up still uses shim-owned state handling | reject unsupported fields | This covers the shim-owned stateful baseline. Stored `previous_response_id` lineage is reconstructed with a shim-owned internal ancestor cap; this is not exact hosted context-retention parity. |
| local `file_search` | local subset | proxy first | local subset or validation error | Current local subset is useful, but `prefer_upstream` does not silently replace hosted behavior |
| local `web_search` | local subset when backend is configured; fallback upstream on unsupported shape/runtime absence | proxy first | local subset or explicit local-only error | Covers `web_search` plus `web_search_preview`; preview ignores `external_web_access` and local preview filters are rejected explicitly |
| local `image_generation` | local subset when backend is configured; fallback upstream before local dispatch when runtime/subset is unavailable | proxy first | local subset or explicit disabled-runtime error | `prefer_upstream` does not silently reroute to local image generation if upstream rejects the tool type |
| local `computer` | local subset when backend is configured | proxy first | local subset or explicit disabled-runtime error | `prefer_upstream` stays raw-proxy; current local subset is screenshot-first external-loop planning |
| local `code_interpreter` | local subset when backend is configured | proxy first | local subset or explicit disabled-runtime error | `prefer_upstream` stays raw-proxy; current subset stays explicitly dev-only/local |
| native local `shell` | local subset for `environment.type="local"` | proxy first | local subset or validation error | Hosted containers, remote environments, and exact hosted orchestration are not claimed; retrieve-stream remains generic for shell calls |
| native local `apply_patch` | local subset | proxy first | local subset | Exact hosted orchestration is not claimed; typed operation-diff replay is limited to the shim-owned stored trace |
| remote MCP `server_url` | local subset | proxy first | local subset, or reject requests outside the local subset | Connector semantics remain separate from `server_url` semantics |
| remote MCP `connector_id` | proxy-only compatibility bridge | proxy-only compatibility bridge | reject with MCP-specific validation error | The shim validates/sanitizes connector-aware requests, but does not claim a local connector runtime |
| `tool_search` hosted/server subset | local subset | proxy first | local subset | Client execution remains proxy-only |
| `tool_search` client execution | proxy-only | proxy-only | reject with tool-search-specific validation error | The shim preserves typed items and replay, but does not run client tool search locally |

## Shim-Owned Operational Surface

| Surface | Current shim status | Freeze guidance | Notes |
| --- | --- | --- | --- |
| `/healthz`, `/readyz`, `/debug/capabilities`, `/metrics` | Shim-owned | Keep documented and stable | Useful operator surface, not OpenAI compatibility surface |
| ingress auth, rate limiting, quotas, structured logs | Shim-owned | Keep minimum operator floor stable | |
| retention cleanup, maintenance path, and local DX packaging | Implemented | Keep the operator workflow explicit | SQLite cleanup is limited to explicit `expires_at` resources; backup/restore/vacuum/optimize ship via `shimctl` |
| multi-tenant authz, shared rate limiting, richer exporters/admin tooling | V3 | Stage after V2 | Valuable, but not required for a broad compatibility facade |

## Known V2 Limitations

- Exact hosted tool choreography is not claimed where docs or fixtures do not
  pin down the wire contract.
- Official native local `shell` and `apply_patch` are implemented as
  shim-local broad subsets, not hosted parity. Current public Codex CLI smokes
  exercise Codex function-tool bridge paths: default `exec_command`, fallback
  function `shell` with `features.unified_exec=false`, and the repo-owned task
  matrix. They do not prove native Responses tool declarations from the CLI.
- `prefer_upstream` remains a proxy-first mode, not a hosted parity guarantee.
- Retrieval ranking is docs-backed and usable, but not presented as exact
  hosted reranker equivalence.
- Constrained custom tools are a supported shim subset. The V3 runtime slice is
  observable and smoke-tested. It reports no backend-native constrained decoding
  parity by default; the optional vLLM path reports `grammar_native` only for
  regex grammars and the shim-supported Lark subset, behind the constrained
  backend adapter registry.
- Operator cleanup currently targets only explicit local `expires_at`
  resources.

## V2 Ship Status

No remaining V2 ship blockers are currently tracked as of April 25, 2026.

## Staged For V3

Items intentionally staged for post-V2 expansion now live in
[docs/v3-scope.md](v3-scope.md).

Historical implementation detail now lives in Git history before the V2 freeze
refactor.
