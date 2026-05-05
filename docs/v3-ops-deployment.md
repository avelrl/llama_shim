# V3 Ops And Deployment Expansion

Status: partial; Phase 0 inventory, the first bounded observability slice, the
Postgres/pgvector beta multi-instance devstack profile, and backend-aware
storage maintenance are implemented.

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
- Postgres/pgvector beta persistence for responses, response replay artifacts,
  conversations, stored Chat Completions, files, vector stores, vector-store
  files, and vector-store chunks, with SQLite sidecar ownership for
  code-interpreter sessions
- backend-aware `shimctl` cleanup, optimize, vacuum, backup, and restore for
  SQLite and the Postgres-owned beta tables
- package-level `make postgres-storage-test` and HTTP-level
  `make devstack-postgres-pgvector-smoke` coverage for the Postgres beta path
- a focused Postgres/pgvector multi-instance devstack smoke path that runs two
  shim instances with separate SQLite sidecars against one shared Postgres
  durable-state store

The current default durable storage backend remains SQLite. Postgres is an
beta durable storage backend for the surfaces named in
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
| Storage maintenance | `shimctl cleanup`, `optimize`, `vacuum`, `backup`, and `restore` operate on the active storage backend. | SQLite or Postgres beta tables |
| Default persistence | SQLite owns responses, conversations, chat completions, files, vector stores, code-interpreter state, and maintenance. | SQLite |
| Postgres beta persistence | Postgres owns responses, response replay artifacts, conversations, stored Chat Completions, files, vector stores, vector-store files, and vector-store chunks; SQLite remains sidecar for code-interpreter state. | Postgres beta plus SQLite sidecar |
| Multi-instance devstack | Optional Compose `multi-instance` profile starts a secondary shim on the same Postgres store with a separate SQLite sidecar and log file. | Postgres state/retrieval objects shared; code-interpreter sidecar state instance-local |
| Devstack smokes | Default, sqlite_fts5, Postgres/pgvector, Postgres/pgvector multi-instance, WebSocket, constrained decoding, coding-tools, Codex, and external tester smokes. | mixed, documented per smoke |

### Phase 1: Observability First Slice

Status: implemented for readiness-probe metrics on May 5, 2026.

- Add metrics/logging that helps classify real-upstream and eval failures:
  first slice adds readiness probe counters and duration histograms for
  `/readyz` and `/debug/capabilities`.
- Keep label cardinality bounded.
- Add tests for new metrics or structured fields where practical.

### Phase 2: Shared Storage Deployment

Status: implemented for the current Postgres/pgvector beta boundary on May 5,
2026.

Implemented boundary:

- `docker-compose.devstack.yml` has an optional `multi-instance` profile with
  `shim_secondary` on `http://127.0.0.1:18082`.
- `make devstack-postgres-pgvector-multi-instance-up` starts the fixture,
  Postgres, primary shim, and secondary shim with the Postgres/pgvector beta
  profile enabled.
- `make devstack-postgres-pgvector-multi-instance-smoke` verifies that primary
  writes file/vector-store objects into Postgres and secondary reads, searches,
  and uses them through local Responses `file_search`; it also verifies shared
  response retrieval/input-items, conversation read/append, and stored Chat
  Completion retrieval/messages across the two instances.
- Primary and secondary use separate SQLite sidecar paths and log files. This
  keeps the test honest: shared state for the covered surfaces must come from
  Postgres, not a shared SQLite sidecar.
- Readiness and `/debug/capabilities` are checked on both instances and must
  report `storage`, `postgres`, and `retrieval_embedder` ready.

Maintenance boundary:

- Runtime storage cleanup now targets the active storage backend. In Postgres
  mode it removes expired Postgres files and vector stores; code-interpreter
  cleanup remains a separate per-instance SQLite sidecar/runtime sweep.
- `shimctl cleanup`, `optimize`, `vacuum`, `backup`, and `restore` are
  backend-aware. Postgres backup/restore use a shim-owned logical COPY file for
  the current beta tables, not cluster-level `pg_dump`/`pg_restore`.
- Postgres object-storage migrations still run at process startup and are
  idempotent for the current beta schema.
- There is still no shared leader election for automatic cleanup loops,
  SQLite-to-Postgres migration tool, cluster-native backup policy, or
  code-interpreter state migration. Those belong to later Postgres
  beta/hardening slices, not the multi-instance deployment smoke.

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
