# V3 Ops And Deployment Expansion

Status: candidate design stub.

Last updated: May 4, 2026.

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
- SQLite-backed local persistence for the current supported storage subset

The current supported durable storage backend remains SQLite unless a more
specific V3 storage document says otherwise.

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

Potential work:

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

Status: not started.

- Inventory current probes, metrics, log fields, storage maintenance loops, and
  backup/restore commands.
- Identify which are SQLite-specific and which are backend-neutral.

### Phase 1: Observability First Slice

Status: not started.

- Add metrics/logging that helps classify real-upstream and eval failures.
- Keep label cardinality bounded.
- Add tests for new metrics or structured fields where practical.

### Phase 2: Shared Storage Deployment

Status: blocked on storage backend phases.

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
