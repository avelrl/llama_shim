# V3 Expansion Staging

Last updated: April 14, 2026.

This document is the parking lot for work that should not block V2.

V2 is the broad compatibility facade release. V3 is where the project can
expand capabilities, add more backend diversity, and take on more expensive
runtime work without muddying the V2 release contract.

## V3 Entry Criteria

V3 should start only after the repo can honestly ship V2 as a broad
compatibility facade:

- the per-surface status in [docs/compatibility-matrix.md](compatibility-matrix.md)
  is current
- remaining V2 blockers in [backlog-v2.md](../backlog-v2.md) are either closed
  or explicitly downgraded
- OpenAPI, README, and backlog no longer imply exact hosted parity where the
  shim only offers a documented subset

## Already Moved Out Of V2

These items are useful, but they are no longer part of the V2 ship bar:

- exact hosted/native tool-specific SSE replay beyond the current
  docs-backed and trace-backed core shim families
- true constrained decoder/runtime for `grammar` / `regex` custom tools
- multi-tenant authz / tenanting / shared rate limiting
- richer exporters, dashboards, admin tooling, and governance-heavy storage work

## Candidate V3 Tracks

### 1. Alternative image backends

- Stable Diffusion / ComfyUI / other image-generation backends
- provider-specific image pipelines that are useful locally but are not part of
  the core OpenAI compatibility promise

### 2. More retrieval and storage backends

- ANN indexing beyond the current exact local subset
- Postgres / pgvector / multi-instance storage modes
- more embedders and rerankers beyond the current compatibility-driven set

### 3. Richer local-only runtimes

- additional local tools that do not map cleanly to current OpenAI surface area
- more ambitious local shell / browser / multimodal runtime loops after the V2
  contract is stable

### 4. Deeper constrained decoding work

- backend-native constrained decoding hooks
- embedded constrained decoder/runtime libraries
- lower-level sampler/logits integrations

This is valuable work, but it is a runtime-expansion track, not a V2 facade
requirement.

### 5. Ops and deployment expansion

- multi-tenant authz / tenant isolation
- richer exporters and dashboards
- governance-heavy storage features such as encryption-at-rest options,
  redaction policy, and hard-delete controls
- Postgres / multi-instance / shared-state deployment modes

## V3 Anti-Scope For Now

These items should not jump ahead of unfinished V2 compatibility work:

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
