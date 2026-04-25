# Engineering Notes

These notes track internal implementation guardrails and change ledgers that are
not practical user guides and are not release scope documents.

- [Runtime Hardening](runtime-hardening.md): storage, replay, pagination, and
  runtime resource-bound work that must not silently change the public
  OpenAI-compatible contract.
- [Upstream SSE Capture](upstream-sse-capture.md): procedure for capturing
  real upstream Responses traces and sanitized fixtures for parity work.
- [V3 Coding Tools Test Runbook](v3-coding-tools-test-runbook.md): manual and
  deterministic checks for the shim-local native coding-tools subset.
- [V3 Coding Tools Status Decision](v3-coding-tools-status-decision.md):
  evidence ledger for the current coding-tools compatibility label.
