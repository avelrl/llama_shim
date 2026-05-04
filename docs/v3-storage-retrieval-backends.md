# V3 Storage And Retrieval Backends

Last updated: May 4, 2026.

This is the V3 plan for expanding durable storage and retrieval backends
without changing the public OpenAI-shaped HTTP surface.

Official docs checked for this pass:

- local official-docs index: [openapi/llms.txt](../openapi/llms.txt)
- OpenAI File Search guide:
  <https://developers.openai.com/api/docs/guides/tools-file-search>
- OpenAI Retrieval guide:
  <https://developers.openai.com/api/docs/guides/retrieval>
- OpenAI API endpoint list via the OpenAI docs MCP, including `/files`,
  `/vector_stores`, vector-store files, and vector-store search endpoints

## Current State

The shim already exposes a practical local subset for:

- `/v1/files`
- `/v1/vector_stores`
- `/v1/vector_stores/{id}/files`
- `/v1/vector_stores/{id}/search`
- `/v1/responses` `file_search`

Current runtime ownership:

- durable object state is SQLite
- default retrieval index is local lexical search
- optional indexed lexical retrieval uses `retrieval.index.backend=sqlite_fts5`
- optional semantic retrieval uses `retrieval.index.backend=sqlite_vec` plus a
  configured embedder
- local `file_search` uses the same vector-store substrate and injects bounded
  grounding context before final model generation

The OpenAI docs frame File Search as a hosted Responses tool over vector stores
and frame Retrieval as semantic search over uploaded data. The shim should keep
that public shape, but it must not claim hosted ranking quality, hosted
retention, hosted billing/quota semantics, or exact hosted planner behavior.

```mermaid
flowchart LR
  client["Client"]
  http["OpenAI-shaped HTTP routes"]
  sqlite["SQLite store"]
  lexical["Lexical scan"]
  fts5["sqlite_fts5 index"]
  sqlitevec["sqlite_vec index"]
  embedder["Optional embedder backend"]
  model["Upstream text model"]

  client --> http
  http --> sqlite
  http --> lexical
  http --> fts5
  http --> sqlitevec
  sqlitevec --> embedder
  http --> model
```

## Goal

Make storage and retrieval backend expansion boring:

- one explicit storage selector: `storage.backend`
- one stable internal storage contract package
- clear separation between durable object storage and retrieval indexing
- no hidden OpenAI-surface request limits to make backend work easier
- `/debug/capabilities` tells operators what is active
- existing SQLite behavior remains the default and remains compatible

## Non-Goals

This V3 track does not claim:

- exact hosted OpenAI File Search or Retrieval ranking parity
- hosted storage retention, quota, billing, or cache semantics
- multi-tenant authorization, governance, or encryption-at-rest policy
- first-pass migration from existing SQLite state into another backend
- a new public OpenAI request field for backend selection

Multi-tenant governance remains V4. Exact hosted choreography and parity claims
remain V5 unless a docs-backed or fixture-backed reason moves them earlier.

## Backend Split

Durable object storage and retrieval indexing are related but separate.

```mermaid
flowchart TB
  routes["HTTP handlers"]
  services["Services"]
  storageContracts["internal/storage contracts"]
  sqliteStore["sqlite.Store"]
  futurePg["future postgres.Store"]
  retrievalIndex["retrieval index contract"]
  lexical["lexical scan"]
  fts5["sqlite_fts5"]
  sqliteVec["sqlite_vec"]
  futurePgVector["future pgvector"]
  embedder["embedder provider"]

  routes --> services
  routes --> storageContracts
  services --> storageContracts
  storageContracts --> sqliteStore
  storageContracts -. future .-> futurePg

  routes --> retrievalIndex
  retrievalIndex --> lexical
  retrievalIndex --> fts5
  retrievalIndex --> sqliteVec
  retrievalIndex -. future .-> futurePgVector
  sqliteVec --> embedder
  futurePgVector -. future .-> embedder
```

The first code slices now add `internal/storage` contracts, a composite
`storage.Store` boundary, compile-time SQLite conformance checks, and HTTP
handler wiring that depends on storage interfaces for router health,
chat-completion shadow storage, retrieval routes, vector-store search, and
code-interpreter file/session stores. The retrieval index is now a separate
generic internal contract with SQLite implementations for lexical scan,
`sqlite_fts5`, and `sqlite_vec` indexing. These slices do not introduce a
second durable object-storage runtime backend yet.

## Configuration

Current supported storage configuration:

```yaml
storage:
  backend: sqlite
```

Environment override:

```bash
STORAGE_BACKEND=sqlite
```

Only `sqlite` is accepted today. Unsupported values fail during config loading,
before any HTTP route starts.

Retrieval indexing remains configured separately:

```yaml
retrieval:
  index:
    backend: lexical   # lexical, sqlite_fts5, or sqlite_vec
  embedder:
    backend: disabled
```

## Capability Manifest

`GET /debug/capabilities` must keep exposing backend boundaries. The relevant
runtime section is:

```json
{
  "runtime": {
    "persistence": {
      "backend": "sqlite",
      "response_store": "sqlite",
      "conversation_store": "sqlite",
      "chat_completion_store": "sqlite",
      "file_store": "sqlite",
      "vector_store": "sqlite",
      "code_interpreter_store": "sqlite",
      "expected_durable": true
    },
    "retrieval": {
      "storage_backend": "sqlite",
      "index_backend": "lexical",
      "embedder_backend": "disabled",
      "semantic_search": false,
      "hybrid_search": false,
      "local_rerank": false,
      "lazy_repair": false
    }
  }
}
```

When `sqlite_fts5` is active, `index_backend` reports `sqlite_fts5` while
semantic/hybrid/rerank flags remain `false`; it reports `lazy_repair=true`
because search can repair a missing or stale FTS5 index for the queried vector
store. It is an indexed lexical backend, not a semantic backend. When
`sqlite_vec` and an embedder are active, `semantic_search`, `hybrid_search`,
`local_rerank`, and `lazy_repair` can become `true`. That is a local
capability claim, not a hosted OpenAI ranking claim.

## Implementation Phases

### 0. Foundation

Status: implemented for the SQLite-only foundation.

- Add `storage.backend` with `sqlite` as the only supported value.
- Add `internal/storage` contracts for the existing durable surfaces.
- Reuse shared storage errors across SQLite and services.
- Add compile-time SQLite conformance checks.
- Expand `/debug/capabilities` and OpenAPI schemas for storage/retrieval
  backend visibility.

### 1. Interface Boundary Hardening

Status: implemented for the current SQLite-only V3 slice.

Move route and service dependencies gradually from concrete `*sqlite.Store` to
the narrowest `internal/storage` interface each path needs.

Rules:

- do not move all handlers at once
- avoid adapter code that just hides the concrete type without reducing
  coupling
- keep ready checks and maintenance paths explicit
- keep tests focused on unchanged HTTP behavior

Current completed slice:

- `RouterDeps.Store` is `storage.Store`, not `*sqlite.Store`
- proxy/chat paths use storage contracts and shared storage errors
- retrieval routes use `storage.FileStore` plus `storage.VectorStore`
- local code-interpreter container/file paths use storage contracts and shared
  storage errors

Still intentionally SQLite-specific after this phase:

- startup store opening and maintenance loops
- SQLite migrations, backup/restore, optimize, and vacuum operations
- concrete SQLite SQL/FTS5/vec0 operations inside the lexical, `sqlite_fts5`,
  and `sqlite_vec` retrieval index implementations

### 2. Retrieval Index Contract

Status: implemented for the current SQLite lexical scan, `sqlite_fts5`, and
`sqlite_vec` backends.

Define a retrieval-index contract that is separate from vector-store object
storage.

The contract should cover:

- indexing vector-store file chunks
- deleting stale chunks
- searching by one or more planned queries
- reporting whether semantic search, hybrid search, and local rerank are
  active
- lazy repair or reindex hooks where a backend supports them

Implemented boundary:

- `internal/retrieval.Index[Mutation, Corpus]` defines the index operations.
- SQLite lexical scan, `sqlite_fts5`, and `sqlite_vec` implementations satisfy
  that contract.
- `internal/retrieval.IndexCapabilities` reports backend, semantic, hybrid,
  rerank, and lazy-repair capability bits.
- `storage.RetrievalIndexReporter` lets `/debug/capabilities` read the actual
  active store/index capabilities instead of reconstructing them only from
  router config.

The first concrete added backend is `retrieval.index.backend=sqlite_fts5`.
It keeps a maintained FTS5 chunk index in SQLite and uses the same public
`/v1/vector_stores/{id}/search` and local `file_search` response shapes as
the default lexical scan backend. It does not change hosted-parity wording and
does not make semantic-search claims. Search lazily repairs missing FTS5 rows
for the queried vector store, which covers enabling this backend on an existing
SQLite database.

This keeps `sqlite_fts5` and `sqlite_vec` behind the storage/retrieval
boundary. Higher-level handlers still see the same OpenAI-shaped vector-store
and `file_search` surface, while future Postgres/pgvector work can implement
the same index contract without changing HTTP handlers.

### 3. Postgres Object Storage Alpha

Add a `postgres` durable backend only after the interface boundary is narrow.

First alpha scope:

- files metadata and content
- vector stores
- vector-store files
- vector-store chunk metadata
- direct vector-store search only if backed by a retrieval index from phase 4

Out of first alpha scope:

- responses and conversations migration
- stored chat completions migration
- code-interpreter artifact storage migration
- mixed SQLite/Postgres cross-store transactions

### 4. pgvector Retrieval Alpha

Add `retrieval.index.backend=pgvector` after Postgres object storage is stable.

Expected behavior:

- semantic search through pgvector
- lexical fallback or hybrid fusion when configured
- same public `/v1/vector_stores/{id}/search` response shape
- same local `file_search` grounding contract

The quality bar is practical local RAG behavior, not hosted OpenAI reranker
equivalence.

### 5. Devstack And Smokes

Add repo-owned smoke coverage before upgrading compatibility wording:

- Docker Compose service for Postgres with pgvector
- upload file
- create vector store
- attach file
- search vector store
- run `/v1/responses` with `file_search`
- delete file/vector-store state
- verify `/debug/capabilities`
- verify `local_only`, `prefer_local`, and `prefer_upstream` do not regress

### 6. Broader Storage Expansion

Only after vector-store storage and retrieval are proven:

- responses/conversations
- stored chat completions
- code-interpreter sessions and generated files
- backup/restore and maintenance operations
- optional migration tools

## Test Requirements

Minimum for each implementation slice:

- config tests for accepted and rejected backend names
- compile-time conformance checks for each backend implementation
- focused storage tests for happy path and main edge cases
- endpoint tests proving HTTP response shapes did not change
- `/debug/capabilities` tests for backend reporting
- `go test ./...`
- `make lint`
- `git diff --check`

For Postgres/pgvector slices, add devstack smoke tests before changing any
status label beyond the current broad-subset wording.
