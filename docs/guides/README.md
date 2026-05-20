# Practical Guides

These guides explain how to use `llama_shim` in practice.

They are intentionally shorter than the official OpenAI docs. The goal is to
answer three questions quickly:

- what this surface is for
- when to use it
- what the shim-specific boundary looks like

Assumptions:

- the shim is reachable at `http://127.0.0.1:8080`
- you already have a working upstream text backend
- examples use `<model>` as a placeholder for whatever model name your backend
  accepts

## Start Here

- [Responses](responses.md): primary API for new work
- [Codex CLI](codex-cli.md): using Codex through the shim, including WebSocket
  mode
- [Codex Testing Plan](codex-testing-plan.md): phased manual testing for real
  Codex CLI and model/upstream triage
- [Conversations](conversations.md): durable conversation state
- [Chat Completions](chat-completions.md): legacy-compatible surface
- [Retrieval and File Search](retrieval.md): files, vector stores, and RAG

## Tool Guides

- [Tools Overview](tools.md): how tool routing works in the shim
- [Web Search](web-search.md): current-information lookups
- [Image Generation](image-generation.md): image creation and editing
- [Computer Use](computer.md): screenshot-first UI loop
- [Code Interpreter](code-interpreter.md): local Python execution

## Operations

- [Operations](operations.md): running, probing, backing up, and maintaining the shim
- [Add Provider/Model Alias](add-provider-model.md): add and validate a new
  routed upstream model without swapping endpoints by hand
- [Model Certification Runner](model-certification.md): batch-certify
  provider/model candidates through isolated shim, external tester, and Codex
  phases
- [Work Queue](../work-queue.md): current next-work list and parked items
- [Script Inventory](../script-inventory.md): short map of Make targets and
  script-owned artifact families
- [V4 Preflight Runbook](operations.md#v4-preflight-runbook): aggregate V4
  health, capability, debug-trace, provider-routing, and Codex config checks
- [V4 Provider Ops Runbook](../v4-provider-ops-runbook.md): final
  provider/model evidence workflow and ops verdict interpretation
- [V4 Chat Compatibility Layer](../v4-chat-compatibility-layer.md): planned
  Chat Completions hardening track for Chat-first coding clients
- [V4 OpenCode Smoke](../v4-opencode-smoke.md): planned real-client OpenCode
  CLI smoke through the shim
- [Postgres Backup and Restore](postgres-backup.md): cluster-native backup
  guidance for the Postgres beta backend
- [Dev Stack](devstack.md): deterministic local stack and smoke path
- [Responses Compatibility External Tester](../engineering/responses-compatibility-external-tester.md):
  Broad subset external tester profile and runner contract

## Related Reference Docs

- [Engineering Notes](../engineering/README.md)
- [OpenAI API Choreography Atlas](../engineering/openai-api-choreography-atlas.md)
- [V2 Scope](../v2-scope.md)
- [Compatibility Matrix](../compatibility-matrix.md)
- [Work Queue](../work-queue.md)
- [Script Inventory](../script-inventory.md)
- [V3 Preflight](../v3-preflight.md)
- [V3 Scope](../v3-scope.md)
- [V3 Codex Eval Curation](../v3-codex-eval-curation.md)
- [V3 Alternative Image Backends](../v3-image-backends.md)
- [V3 Storage and Retrieval Backends](../v3-storage-retrieval-backends.md)
- [V3 Richer Local-Only Runtimes](../v3-local-runtimes.md)
- [V3 Computer Browser Harness](../v3-computer-browser-harness.md)
- [V3 Ops and Deployment Expansion](../v3-ops-deployment.md)
- [V4 Preflight](../v4-preflight.md)
- [V4 Provider Ops Runbook](../v4-provider-ops-runbook.md)
- [V4 Chat Compatibility Layer](../v4-chat-compatibility-layer.md)
- [V4 OpenCode Smoke](../v4-opencode-smoke.md)
- [Model Certification Runner](model-certification.md)
- [Add Provider/Model Alias](add-provider-model.md)
- [V4 Model/Provider Operational Matrix](../engineering/v4-model-provider-operational-matrix.md)
- [V4 Extensions and Plugin Model](../v4-scope.md)
- [V5 Hosted Parity and Advanced Transports](../v5-scope.md)
- [OpenAPI spec](../../openapi/openapi.yaml)
