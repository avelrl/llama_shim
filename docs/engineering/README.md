# Engineering Notes

These notes track internal implementation guardrails and change ledgers that are
not practical user guides and are not release scope documents.

- [Runtime Hardening](runtime-hardening.md): storage, replay, pagination, and
  runtime resource-bound work that must not silently change the public
  OpenAI-compatible contract.
- [Work Queue](../work-queue.md): current practical implementation queue,
  parked tracks, and model-candidate backlog.
- [Script Inventory](../script-inventory.md): short operator map for Make
  targets, script entrypoints, and artifact families.
- [OpenAI API Choreography Atlas](openai-api-choreography-atlas.md):
  diagram-first map of Responses, state, SSE, WebSocket, tools, Codex, and the
  current shim-local boundaries.
- [Upstream SSE Capture](upstream-sse-capture.md): procedure for capturing
  real upstream Responses traces and sanitized fixtures for parity work.
- [Responses Compatibility External Tester](responses-compatibility-external-tester.md):
  Broad subset tester profile, capability-gating rules, and repo-owned runner
  contract for external API-surface compatibility tests.
- [V4 Model/Provider Operational Matrix](v4-model-provider-operational-matrix.md):
  current provider/model operating choices, promotion rules, and commands for
  V4 preflight, provider routing, and Codex auto runs.
- [V4 Provider Ops Runbook](../v4-provider-ops-runbook.md): compact
  provider/model evidence flow and final ops verdict interpretation.
- [V4 Chat Compatibility Layer](../v4-chat-compatibility-layer.md):
  planned Chat Completions hardening track that classifies Responses-era fixes
  as portable, adapted, or not applicable before implementation.
- [V4 OpenCode Smoke](../v4-opencode-smoke.md): design/runbook for the planned
  real-client OpenCode CLI smoke through the shim.
- [Codex Upstream Model Matrix](codex-upstream-model-matrix.md): practical
  historical DeepSeek, MiMo, Qwen, and Kimi comparison for
  Codex-through-shim smoke testing.
- [V3 Codex Eval Curation](../v3-codex-eval-curation.md): human review
  procedure for generated Codex auto-run artifacts and baseline promotion.
- [V3 Storage and Retrieval Backends](../v3-storage-retrieval-backends.md):
  backend-expansion plan, storage contracts, capability reporting, and
  Postgres/pgvector staging.
- [V3 Alternative Image Backends](../v3-image-backends.md):
  planned local image-generation backend expansion without hosted parity
  overclaims.
- [V3 Richer Local-Only Runtimes](../v3-local-runtimes.md):
  local runtime expansion slices and capability-gating requirements; the first
  implemented slice covers the shim-local computer loop.
- [V3 Computer Browser Harness](../v3-computer-browser-harness.md): optional
  real Playwright executor smoke for the shim-local `computer` loop.
- [V3 Ops and Deployment Expansion](../v3-ops-deployment.md):
  deployment, readiness observability, tenanting, and governance staging.
- [V3 Coding Tools Test Runbook](v3-coding-tools-test-runbook.md): manual and
  deterministic checks for the shim-local native coding-tools subset.
- [V3 Coding Tools Status Decision](v3-coding-tools-status-decision.md):
  evidence ledger for the current coding-tools compatibility label.
