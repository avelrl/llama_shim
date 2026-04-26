# Engineering Notes

These notes track internal implementation guardrails and change ledgers that are
not practical user guides and are not release scope documents.

- [Runtime Hardening](runtime-hardening.md): storage, replay, pagination, and
  runtime resource-bound work that must not silently change the public
  OpenAI-compatible contract.
- [OpenAI API Choreography Atlas](openai-api-choreography-atlas.md):
  diagram-first map of Responses, state, SSE, WebSocket, tools, Codex, and the
  current shim-local boundaries.
- [Upstream SSE Capture](upstream-sse-capture.md): procedure for capturing
  real upstream Responses traces and sanitized fixtures for parity work.
- [Responses Compatibility External Tester](responses-compatibility-external-tester.md):
  Broad subset tester profile, capability-gating rules, and repo-owned runner
  contract for external API-surface compatibility tests.
- [V3 Storage and Retrieval Backends](../v3-storage-retrieval-backends.md):
  backend-expansion plan, storage contracts, capability reporting, and
  Postgres/pgvector staging.
- [V3 Coding Tools Test Runbook](v3-coding-tools-test-runbook.md): manual and
  deterministic checks for the shim-local native coding-tools subset.
- [V3 Coding Tools Status Decision](v3-coding-tools-status-decision.md):
  evidence ledger for the current coding-tools compatibility label.
