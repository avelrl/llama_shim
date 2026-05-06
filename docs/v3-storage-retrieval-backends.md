# V3 Storage And Retrieval Backends

Last updated: May 5, 2026.

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
- OpenAI Conversation state guide:
  <https://developers.openai.com/api/docs/guides/conversation-state>
- OpenAI Migrate to Responses guide:
  <https://developers.openai.com/api/docs/guides/migrate-to-responses>
- OpenAI Code Interpreter guide:
  <https://developers.openai.com/api/docs/guides/tools-code-interpreter>
- OpenAI Data controls guide:
  <https://developers.openai.com/api/docs/guides/your-data>
- OpenAI API reference delete pages for responses, conversations/items, files,
  vector stores, and vector-store files; these confirm resource-scoped delete
  response shapes but do not define shim-local governance purge.
- OpenAI OpenAPI spec via the OpenAI docs MCP for
  `/responses/{response_id}/input_items`,
  `/conversations/{conversation_id}/items`, and
  `/chat/completions/{completion_id}/messages`; the MCP endpoint list also
  confirms `/responses`, `/conversations`, `/chat/completions`,
  `/chat/completions/{completion_id}`, `/containers`,
  `/containers/{container_id}/files`, and
  `/containers/{container_id}/files/{file_id}/content`.

## Current State

The shim already exposes a practical local subset for:

- `/v1/files`
- `/v1/vector_stores`
- `/v1/vector_stores/{id}/files`
- `/v1/vector_stores/{id}/search`
- `/v1/responses` `file_search`

Current runtime ownership:

- default durable object state is SQLite
- optional Postgres beta object storage uses Postgres for responses, response
  replay artifacts, conversations, stored chat completions, files, vector
  stores, vector-store files, and vector-store chunks while keeping SQLite as a
  sidecar for code-interpreter state; backend-aware `shimctl` maintenance now
  covers the Postgres-owned tables
- default retrieval index is local lexical search
- optional indexed lexical retrieval uses `retrieval.index.backend=sqlite_fts5`
- optional semantic retrieval uses `retrieval.index.backend=sqlite_vec` plus a
  configured embedder
- optional pgvector retrieval uses `storage.backend=postgres`,
  `retrieval.index.backend=pgvector`, and a configured retrieval embedder
- local `file_search` uses the same vector-store substrate and injects bounded
  grounding context before final model generation
- `shimctl migrate sqlite-to-postgres` can move the current Postgres-owned
  beta tables from an existing SQLite database into an explicitly configured
  Postgres target

The OpenAI docs frame File Search as a hosted Responses tool over vector stores
and frame Retrieval as semantic search over uploaded data. The shim should keep
that public shape, but it must not claim hosted ranking quality, hosted
retention, hosted billing/quota semantics, or exact hosted planner behavior.

```mermaid
flowchart LR
  client["Client"]
  http["OpenAI-shaped HTTP routes"]
  sqlite["SQLite store"]
  postgres["Postgres durable store beta"]
  lexical["Lexical scan"]
  fts5["sqlite_fts5 index"]
  sqlitevec["sqlite_vec index"]
  pgvector["pgvector dense index"]
  embedder["Optional embedder backend"]
  model["Upstream text model"]

  client --> http
  http --> sqlite
  http --> postgres
  http --> lexical
  http --> fts5
  http --> sqlitevec
  http --> pgvector
  sqlitevec --> embedder
  pgvector --> embedder
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
  pgStore["postgres.Store beta"]
  retrievalIndex["retrieval index contract"]
  lexical["lexical scan"]
  fts5["sqlite_fts5"]
  sqliteVec["sqlite_vec"]
  pgVector["pgvector"]
  embedder["embedder provider"]

  routes --> services
  routes --> storageContracts
  services --> storageContracts
  storageContracts --> sqliteStore
  storageContracts --> pgStore

  routes --> retrievalIndex
  retrievalIndex --> lexical
  retrievalIndex --> fts5
  retrievalIndex --> sqliteVec
  retrievalIndex --> pgVector
  sqliteVec --> embedder
  pgVector --> embedder
```

The first code slices now add `internal/storage` contracts, a composite
`storage.Store` boundary, compile-time SQLite conformance checks, and HTTP
handler wiring that depends on storage interfaces for router health,
chat-completion shadow storage, retrieval routes, vector-store search, and
code-interpreter file/session stores. The retrieval index is now a separate
generic internal contract with SQLite implementations for lexical scan,
`sqlite_fts5`, and `sqlite_vec` indexing. The Postgres beta adds a second
durable backend for the stateful local stores that are safe to share across
instances: responses, replay artifacts, conversations, stored Chat
Completions, files, vector stores, vector-store files, and vector-store
chunks. It intentionally keeps SQLite as a sidecar for code-interpreter
runtime state because shim-local Docker sessions and container files are
instance-local runtime state, not safe multi-instance state that can be shared
by moving metadata alone. The
maintenance contract is now backend-aware: SQLite uses native SQLite
maintenance, while Postgres uses shim-owned cleanup and logical COPY
backup/restore for the Postgres-owned tables.

## Configuration

Supported storage configuration:

```yaml
storage:
  backend: sqlite
  retention:
    response_replay_artifacts:
      max_age: 0s
      max_responses: 0

postgres:
  dsn: ""
```

Environment override:

```bash
STORAGE_BACKEND=sqlite
POSTGRES_DSN=postgres://llama_shim:llama_shim@127.0.0.1:15432/llama_shim?sslmode=disable
```

`sqlite` remains the default. `postgres` is accepted for the beta durable
state/object-storage slice and requires `postgres.dsn`. Unsupported values
fail during config loading, before any HTTP route starts.
Replay-artifact retention is disabled by default and is a shim-local operator
policy, not an OpenAI API retention claim.

Retrieval indexing remains configured separately:

```yaml
retrieval:
  index:
    backend: lexical   # lexical, sqlite_fts5, sqlite_vec, or pgvector
    pgvector:
      ann:
        enabled: false
        method: hnsw        # hnsw or ivfflat
        metric: cosine      # currently cosine only
        dimensions: 0       # required when enabled
        hnsw_m: 16
        hnsw_ef_construction: 64
        ivfflat_lists: 100
  embedder:
    backend: disabled
```

`retrieval.index.backend=pgvector` requires `storage.backend=postgres` and a
configured retrieval embedder. By default it uses exact pgvector distance
ordering plus the shim's existing lexical fallback/hybrid fusion. Optional
ANN indexing is enabled only when `retrieval.index.pgvector.ann.enabled=true`
and `retrieval.index.pgvector.ann.dimensions` matches the configured embedder.
This is a shim-owned local index acceleration path, not hosted OpenAI ranking
parity.

When `storage.backend=postgres` is active, `retrieval.index.backend` must be
`lexical` or `pgvector`; `sqlite_fts5` and `sqlite_vec` are SQLite-specific
index backends.

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

When Postgres/pgvector beta is active, the manifest reports Postgres for the
stateful stores that are shared across instances and SQLite sidecar ownership
for the stores that have not moved yet:

```json
{
  "runtime": {
    "persistence": {
      "backend": "postgres",
      "response_store": "postgres",
      "conversation_store": "postgres",
      "chat_completion_store": "postgres",
      "file_store": "postgres",
      "vector_store": "postgres",
      "code_interpreter_store": "sqlite_sidecar",
      "expected_durable": true
    },
    "retrieval": {
      "storage_backend": "postgres",
      "index_backend": "pgvector",
      "embedder_backend": "openai_compatible",
      "semantic_search": true,
      "hybrid_search": true,
      "local_rerank": false,
      "lazy_repair": false,
      "ann_index": {
        "enabled": true,
        "method": "hnsw",
        "metric": "cosine",
        "dimensions": 4
      }
    }
  }
}
```

`ann_index` is omitted when pgvector ANN is disabled.

## Implementation Phases

### 0. Foundation

Status: implemented for the SQLite-only foundation.

- Add `storage.backend`, initially with `sqlite` as the only supported value.
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

This keeps `sqlite_fts5`, `sqlite_vec`, and `pgvector` behind the
storage/retrieval boundary. Higher-level handlers still see the same
OpenAI-shaped vector-store and `file_search` surface. The current
Postgres/pgvector beta validates that boundary for durable state and retrieval
objects without changing HTTP handlers.

### 3. Postgres Durable Storage Beta

Status: implemented as a beta hybrid store.

The `postgres` durable backend owns the shareable local state surfaces:

- responses
- response replay artifacts used by retrieve-stream replay
- conversations and conversation items
- stored Chat Completions and normalized stored messages
- files metadata and content
- vector stores
- vector-store files
- vector-store chunk metadata
- direct vector-store search when backed by `retrieval.index.backend=pgvector`

Implementation boundary:

- `internal/storage/postgres.Store` satisfies the shared `storage.Store`
  contract.
- Postgres stores responses, conversations, stored Chat Completions,
  file/vector-store data, and replay artifacts.
- SQLite remains a sidecar for code-interpreter sessions/generated files.
- `shimctl cleanup`, `optimize`, `vacuum`, `backup`, and `restore` operate on
  the active storage backend. In Postgres mode, backup/restore use a
  shim-owned logical COPY format for the Postgres-owned tables and keep file
  content mirrored into the SQLite sidecar after restore.
- Cluster-native Postgres backup guidance is documented in
  [Postgres Backup and Restore](guides/postgres-backup.md).
- The schema creates the pgvector extension because this slice is paired with
  pgvector retrieval in devstack.
- List endpoints use SQL-side pagination and do not fetch file content just to
  build pages.

Out of current beta scope:

- Postgres-backed code-interpreter runtime/session ownership
- mixed SQLite/Postgres cross-store transactions
- automatic external `pg_dump`/`pg_restore` orchestration or cluster-level
  backup scheduling

### 4. pgvector Retrieval Alpha

Status: implemented as an alpha pgvector backend with exact search by default
and optional explicit ANN indexing.

Implemented behavior:

- semantic search through pgvector exact vector distance ordering
- optional HNSW/IVFFlat ANN indexes over a fixed embedding dimension when
  explicitly configured
- lexical fallback when semantic search returns no usable results
- weighted hybrid dense+text ranking when `ranking_options.hybrid_search` is
  configured
- same public `/v1/vector_stores/{id}/search` response shape
- same local `file_search` grounding contract
- deterministic query rewrite and multi-query planning stay in the local
  HTTP/service layer, not in the pgvector index itself

The quality bar is practical local RAG behavior, not hosted OpenAI reranker
equivalence.

### 5. Devstack And Smokes

Status: implemented for the Postgres/pgvector beta path.

Repo-owned smoke coverage:

- Docker Compose service for Postgres with pgvector
- optional Docker Compose `multi-instance` profile with a secondary shim
- upload file
- create vector store
- attach file
- search vector store
- run `/v1/responses` with `file_search`
- delete file/vector-store state
- verify `/debug/capabilities`
- verify primary-created Postgres retrieval objects can be read, searched, and
  used by secondary through local Responses `file_search`
- verify secondary-created response state is readable through primary
- verify primary-created conversations are readable and appendable through
  secondary
- verify primary-created stored Chat Completions and normalized messages are
  readable through secondary
- verify backend-aware `shimctl` maintenance for Postgres cleanup, optimize,
  vacuum, and logical backup generation

Run:

```bash
STORAGE_BACKEND=postgres \
RETRIEVAL_INDEX_BACKEND=pgvector \
RETRIEVAL_EMBEDDER_BACKEND=openai_compatible \
RETRIEVAL_EMBEDDER_BASE_URL=http://fixture:8081 \
RETRIEVAL_EMBEDDER_MODEL=devstack-embedding \
RESPONSES_MODE=prefer_local \
make devstack-up

RESPONSES_MODE=prefer_local make devstack-postgres-pgvector-smoke
```

Run the focused ANN variant after starting the ANN-enabled devstack:

```bash
make devstack-postgres-pgvector-ann-up
make devstack-postgres-pgvector-ann-smoke
```

Run the focused multi-instance deployment smoke:

```bash
make devstack-postgres-pgvector-multi-instance-up
make devstack-postgres-pgvector-multi-instance-smoke
```

The multi-instance smoke covers the current beta boundary: responses, response
input-items, conversations, stored Chat Completions, files, vector stores,
vector-store files, and vector-store chunks are shared through Postgres.
Code-interpreter state remains SQLite sidecar owned and is not a shared-state
claim.

The smoke checks `prefer_local`, because it exercises the shim-owned local
Responses `file_search` path. `local_only` and `prefer_upstream` remain covered
by the existing Responses mode tests.

Focused Postgres storage hardening coverage:

```bash
make devstack-up
make postgres-storage-test
```

`postgres-storage-test` uses `POSTGRES_TEST_DSN`, defaulting to the devstack
Postgres port. It opens each test case in an isolated schema and checks:

- Postgres response CRUD, lineage, replay artifacts, and multi-instance reads
- Postgres conversation create/read/list/append/delete-item behavior and
  multi-instance reads/appends
- Postgres stored Chat Completion CRUD, metadata filtering, normalized message
  pagination, and multi-instance reads
- Postgres file CRUD, keyset pagination, and SQLite sidecar mirroring
- vector-store CRUD, file pagination, delete behavior, and lexical search
- binary/unsupported file attachment failure state
- pgvector semantic search, hybrid ranking, and capability reporting
- rejection of SQLite-only retrieval indexes when `storage.backend=postgres`
- Postgres maintenance cleanup, optimize/vacuum SQL, and logical backup/restore
  round-trip for the current beta tables

Postgres beta hardening now also covers:

- app-owned DDL serialization with a Postgres advisory lock, so concurrent
  shim startup does not race schema creation or migration recording
- physical cleanup of `vector_store_chunks` when a vector-store file is deleted
  from a vector store, not only hiding those chunks from search joins
- focused tests for concurrent `OpenWithOptions` migration and chunk cleanup

### 6. Broader Storage Expansion

Broader storage expansion slices:

#### 6.1 SQLite-to-Postgres migration tooling

Status: implemented for the current Postgres-owned beta tables.

The operator workflow is intentionally conservative:

- `shimctl migrate sqlite-to-postgres` copies only the Postgres-owned beta
  tables: responses, replay artifacts,
  conversations/items, stored Chat Completions/messages, files, vector stores,
  vector-store files, and vector-store chunks
- code-interpreter sessions/generated files stay in the SQLite sidecar and are
  not part of this migration claim
- `-dry-run` reports source and target row counts without writes
- writes fail by default when target Postgres migration tables are not empty;
  `-replace` is the only supported overwrite mode and truncates the
  Postgres-owned beta tables before copying
- the tool uses a separate target SQLite sidecar while opening the Postgres
  store, so it does not write through to the source SQLite database
- when the target is opened with `retrieval.index.backend=pgvector`, copied
  completed chunks with missing pgvector embeddings are re-indexed with the
  configured retrieval embedder during migration; otherwise copied chunks remain
  searchable through the Postgres lexical path
- focused Postgres storage tests cover SQLite fixture migration into an
  isolated Postgres schema, including file content, searchability, response
  replay artifacts, conversations, stored chat messages, dry-run reporting,
  non-empty-target refusal, explicit replace, and sidecar file mirroring

Run:

```bash
STORAGE_BACKEND=postgres \
POSTGRES_DSN=postgres://llama_shim:llama_shim@127.0.0.1:15432/llama_shim?sslmode=disable \
go run ./cmd/shimctl -config ./config.yaml migrate sqlite-to-postgres \
  -sqlite ./.data/shim.db \
  -dry-run

STORAGE_BACKEND=postgres \
POSTGRES_DSN=postgres://llama_shim:llama_shim@127.0.0.1:15432/llama_shim?sslmode=disable \
go run ./cmd/shimctl -config ./config.yaml migrate sqlite-to-postgres \
  -sqlite ./.data/shim.db \
  -replace
```

#### 6.2 Code-interpreter state ownership

Status: implemented as an explicit sidecar-local ownership boundary.

Code-interpreter sessions and container-file membership remain owned by the
per-instance SQLite sidecar in Postgres mode. This is an intentional runtime
ownership decision, not a missing migration table.

Rationale:

- Official Code Interpreter is container-scoped: files are uploaded to or
  downloaded from a container, generated files appear as container-file
  citations, and hosted container state is temporary.
- The shim's local execution boundary is a Docker container managed by one
  shim instance. Its workdir, process lifetime, cleanup, and generated
  artifacts are not cluster-shared just because object metadata moves to
  Postgres.
- Postgres mode shares responses, conversations, stored Chat Completions,
  files, vector stores, and vector-store chunks. It does not share active
  code-interpreter session reuse or `/v1/containers` membership across shim
  instances.
- Generated artifacts that are mirrored into shim `/v1/files` follow the
  normal active file store and can be Postgres-owned, but the
  code-interpreter container relationship that produced them stays sidecar
  local.
- SQLite-to-Postgres migration and Postgres logical backup/restore
  intentionally exclude code-interpreter session/container-file tables and
  report `code_interpreter_migrated=false`.

Focused Postgres storage tests cover the boundary in both directions:

- primary-created code-interpreter sessions/container files are not visible
  through a secondary Postgres-backed shim instance
- SQLite migration copies the Postgres-owned beta tables while leaving
  source code-interpreter state out of the target sidecar

Future Postgres-backed code-interpreter state would need a separate shared
runtime design covering scheduler ownership, container placement, generated
artifact cleanup, and failure semantics. It should not be treated as a storage
table extension.

#### 6.3 Replay/artifact retention policy

Status: implemented.

Current cleanup targets explicit `expires_at` resources and optional
shim-owned response replay artifacts. Replay-artifact retention is disabled by
default and configured under
`storage.retention.response_replay_artifacts`:

- `max_age` prunes artifacts for standalone responses older than the configured
  age
- `max_responses` keeps replay artifacts for only the newest standalone
  responses by `created_at`
- stored `responses` rows are not deleted by this policy
- responses attached to `/v1/conversations` are preserved to avoid implying an
  OpenAI hosted 30-day TTL for conversation state

This is a shim-local operator policy, not hosted OpenAI retention parity.
Storage tests cover SQLite and Postgres artifact pruning; retrieve-stream
tests cover replay behavior after artifacts are pruned.

#### 6.4 Cluster-native Postgres backup guidance

Status: implemented docs/runbook.

[Postgres Backup and Restore](guides/postgres-backup.md) documents when to use
the shim-owned logical COPY backup versus Postgres-native backup/restore. The
current logical backup is useful for devstack, regression tests, and small
operator exports. Production deployments still need a cluster-level policy
such as `pg_dump`, PITR, or a managed database backup; the shim does not
schedule or retain those backups.

#### 6.5 Hard-delete and governance hooks

Status: implemented operator purge surface for local shim-owned state.

Resource-scoped OpenAI-shaped delete routes already exist for responses,
conversation items, stored Chat Completions, files, vector stores,
vector-store files, containers, and container files. They remain compatibility
routes for the addressed resource only.

[V3 Hard Delete And Governance Boundary](v3-hard-delete-governance.md)
documents the dedicated shim-owned `shimctl governance purge` surface. The
current scope is `all_local_state` only and defaults to dry-run. Applying the
purge requires `-apply -confirm purge-all-local-state`; it deletes in bounded
table batches and emits a JSON audit report to stdout and optionally to
`-audit-out`.

Implemented boundary:

- SQLite purges responses, replay artifacts, conversations/items, stored Chat
  Completions, files, vector stores, vector-store files/chunks, and
  code-interpreter sidecar state.
- Postgres purges the current Postgres-owned beta tables and also purges the
  configured SQLite sidecar, because file mirrors and code-interpreter runtime
  state remain sidecar-owned in Postgres mode.
- Dry-run reports counts without reading stored file/blob content.
- The audit report includes explicit out-of-scope notes for debug logs,
  request/response capture files, eval/smoke artifacts, operator-created
  backups/PITR archives, and upstream-provider state already transmitted
  outside the shim.

Tenant/project purge, legal hold, redaction policy, encryption policy, approval
workflow, upstream delete propagation, and backup/PITR deletion guarantees
remain future governance work, not OpenAI API parity.

#### 6.6 ANN indexing

Status: implemented for explicit pgvector HNSW/IVFFlat indexes.

The pgvector implementation remains exact search by default. ANN is an
operator opt-in because pgvector ANN indexes require a fixed embedding
dimension and can trade recall for latency. The shim does not infer dimensions
from the first document or silently change retrieval behavior.

Implemented boundary:

- `retrieval.index.pgvector.ann.enabled=true` enables shim-managed ANN index
  creation.
- `retrieval.index.pgvector.ann.method` supports `hnsw` and `ivfflat`.
- `retrieval.index.pgvector.ann.metric` supports `cosine`, matching the
  existing pgvector `<=>` distance query.
- `retrieval.index.pgvector.ann.dimensions` is required when enabled and must
  match the retrieval embedder output dimension.
- HNSW creation parameters are `hnsw_m` and `hnsw_ef_construction`.
- IVFFlat creation parameter is `ivfflat_lists`.
- Startup and `shimctl optimize` create the desired managed index and drop
  stale shim-managed ANN indexes with the `idx_vsc_ann_` prefix after the new
  index exists.
- Search casts `embedding` and query vectors to `vector(<dimensions>)` only in
  ANN mode, and filters chunks by `embedding_dimensions`.
- Attach/migration/search fail fast on dimension mismatch instead of mixing
  incompatible embedding spaces.
- `/debug/capabilities.runtime.retrieval.ann_index` reports method, metric,
  and dimensions when the ANN path is active.

Non-goals:

- hosted OpenAI reranker equivalence
- automatic dimension inference
- automatic ANN enablement for existing pgvector deployments
- per-query ANN search knobs such as `hnsw.ef_search` or `ivfflat.probes`
  exposed as public API fields

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
status label beyond the current broad-subset wording. Keep
`make postgres-storage-test` green when changing the Postgres beta store
itself; it is package-level coverage, while
`make devstack-postgres-pgvector-smoke` is the HTTP/service smoke.
