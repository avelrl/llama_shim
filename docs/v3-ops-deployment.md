# V3 Ops And Deployment Expansion

Status: partial; Phase 0 inventory and the first bounded observability slice
are implemented.

Last updated: May 5, 2026.

This document tracks V3 operations and deployment work that is useful for real
installations but should not be confused with OpenAI API parity.

Most items here depend on the storage/retrieval backend work being narrow and
well tested first. Avoid implementing tenanting, governance storage, or
multi-instance behavior on top of SQLite-specific paths that still need
interface hardening.

## Current State

The shim already exposes basic operational surfaces:

- `/healthz`
- `/readyz`
- `/metrics`
- `/debug/capabilities`
- local devstack and smoke targets
- SQLite-backed local persistence for the default durable subset
- Postgres/pgvector alpha persistence for files, vector stores, vector-store
  files, and vector-store chunks, with SQLite sidecar ownership for responses,
  conversations, stored Chat Completions, code-interpreter sessions, and
  maintenance commands
- package-level `make postgres-storage-test` and HTTP-level
  `make devstack-postgres-pgvector-smoke` coverage for the Postgres alpha path

The current default durable storage backend remains SQLite. Postgres is an
alpha retrieval-object storage backend only for the surfaces named in
[V3 Storage And Retrieval Backends](v3-storage-retrieval-backends.md).

## Goals

- Make production-style deployment modes explicit and testable.
- Add observability that helps operators distinguish shim, upstream, model,
  checker, and environment failures.
- Keep new operational limits internal-only unless they are documented OpenAI
  surface behavior.
- Stage Postgres and multi-instance deployment work through the storage
  backend plan.
- Keep tenanting and governance features clearly shim-owned.

## Non-Goals

- Exact OpenAI hosted retention, quota, billing, or tenancy semantics.
- Hidden request limits on OpenAI-compatible endpoints.
- A broad enterprise policy engine in V3.
- Treating ops features as proof of API parity.

## Candidate Work Areas

### 1. Multi-Instance Deployment

Depends on Postgres or another shared durable backend.

Potential work:

- shared state deployment docs
- advisory lock or ownership model for maintenance loops
- readiness checks that distinguish storage, upstream, and optional runtime
  readiness
- migration and backup guidance

### 2. Observability

Implemented first slice:

- `/readyz` and `/debug/capabilities` readiness probes emit bounded
  Prometheus counters and latency histograms:
  `shim_readiness_probe_total` and `shim_readiness_probe_duration_ms`.
- Labels are deliberately low-cardinality:
  `source` is `readyz` or `capabilities`, `component` is one of the fixed
  runtime components such as `storage`, `llama`, `retrieval_embedder`,
  `web_search_backend`, or `image_generation_backend`, and `outcome` is
  `ready` or `unready`.
- Existing upstream admission, queue wait, in-flight, queued, retrieval search,
  code-interpreter, auth, rate-limit, and HTTP metrics remain unchanged.

Potential future work:

- richer metrics labels for routing mode, upstream transport, tool family, and
  failure class
- dashboards for Codex eval and external tester runs
- structured log fields for raw-markup repair, transport retry, and backend
  fallback decisions
- alert examples that avoid high-cardinality labels

### 3. Tenant Isolation

Candidate only after shared storage is stable.

Potential work:

- internal tenant id propagation
- tenant-scoped storage keys and list filters
- tenant-aware metrics without leaking tenant identifiers into public response
  bodies
- tests that tenant isolation does not change OpenAI-compatible request or
  response shapes

### 4. Governance Storage

Candidate only after storage contracts are backend-neutral.

Potential work:

- encryption-at-rest configuration notes
- redaction policy hooks for internal capture or debug artifacts
- hard-delete controls for shim-owned persisted data
- audit-oriented retention docs

## Implementation Phases

### Phase 0: Ops Inventory

Status: implemented on May 5, 2026.

Inventory summary:

| Area | Current state | Backend boundary |
| --- | --- | --- |
| Liveness | `/healthz` checks process liveness only. | backend-neutral |
| Readiness | `/readyz` checks storage, upstream model backend, and optional retrieval, web-search, and image-generation backends. | backend-neutral API, component-specific probes |
| Capability manifest | `/debug/capabilities` reports active storage, retrieval, tool, runtime, compaction, metrics, and probe state. | backend-neutral manifest with backend-specific values |
| Metrics | `/metrics` exposes HTTP, auth, rate-limit, upstream admission/queue, in-flight/queued, retrieval, code-interpreter, and readiness-probe metrics. | backend-neutral, bounded labels |
| Storage maintenance | `shimctl cleanup`, `optimize`, `vacuum`, and `backup` currently operate on the SQLite store. | SQLite-specific |
| Default persistence | SQLite owns responses, conversations, chat completions, files, vector stores, code-interpreter state, and maintenance. | SQLite |
| Postgres alpha persistence | Postgres owns files, vector stores, vector-store files, and vector-store chunks; SQLite remains sidecar for other stores. | Postgres alpha plus SQLite sidecar |
| Devstack smokes | Default, sqlite_fts5, Postgres/pgvector, WebSocket, constrained decoding, coding-tools, Codex, and external tester smokes. | mixed, documented per smoke |

### Phase 1: Observability First Slice

Status: implemented for readiness-probe metrics on May 5, 2026.

- Add metrics/logging that helps classify real-upstream and eval failures:
  first slice adds readiness probe counters and duration histograms for
  `/readyz` and `/debug/capabilities`.
- Keep label cardinality bounded.
- Add tests for new metrics or structured fields where practical.

### Phase 2: Shared Storage Deployment

Status: ready to design against the current Postgres alpha boundary, but not
implemented.

- Document and test Postgres-backed deployment only after the storage track
  provides the backend.
- Add readiness and maintenance-loop behavior for multi-instance mode.

### Phase 3: Governance/Tenanting

Status: candidate.

- Split into a dedicated plan before implementation.
- Require explicit API-boundary review before adding any request-visible
  behavior.

## Done Criteria

This track should move from candidate to partial only when:

- the first ops slice is tied to a concrete operator problem
- new limits are internal-only or explicitly documented
- metrics/logs remain bounded and redacted
- storage dependencies are clear
- docs do not present shim-owned deployment features as OpenAI hosted parity
