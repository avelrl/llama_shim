# Retrieval And File Search

## What It Is

`llama_shim` ships a local retrieval substrate behind OpenAI-shaped routes:

- `/v1/files`
- `/v1/vector_stores`
- `/v1/vector_stores/{id}/files`
- `/v1/vector_stores/{id}/search`
- local `file_search` inside `/v1/responses`

This is the main way to build RAG-style flows over your own documents.

## When To Use It

Use retrieval when you want the model to answer from your files instead of only
from model memory.

Typical cases:

- internal docs assistants
- product manuals and runbooks
- ticket or policy search
- local knowledge bases for tool-using agents

## Typical Flow

### 1. Upload a file

```bash
curl http://127.0.0.1:8080/v1/files \
  -H "Content-Type: multipart/form-data" \
  -F purpose=assistants \
  -F file=@./docs/manual.txt
```

### 2. Create a vector store

```bash
curl http://127.0.0.1:8080/v1/vector_stores \
  -H "Content-Type: application/json" \
  -d '{"name": "manuals"}'
```

### 3. Attach the file to the vector store

```bash
curl http://127.0.0.1:8080/v1/vector_stores/vs_.../files \
  -H "Content-Type: application/json" \
  -d '{"file_id": "file_..."}'
```

### 4. Query the store directly

```bash
curl http://127.0.0.1:8080/v1/vector_stores/vs_.../search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "What is the retention policy?",
    "max_num_results": 5
  }'
```

### 5. Let `Responses` use `file_search`

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Answer from the uploaded docs: what is the retention policy?",
    "tools": [
      {
        "type": "file_search",
        "vector_store_ids": ["vs_..."]
      }
    ]
  }'
```

## Shim-Specific Notes

- Durable retrieval objects are stored through `storage.backend`. `sqlite` is
  the default. `postgres` is available as an alpha backend for files, vector
  stores, vector-store files, and vector-store chunks; responses,
  conversations, stored Chat Completions, and code-interpreter state remain in
  the SQLite sidecar for that alpha path.
- Lexical search works in the default local setup.
- `retrieval.index.backend=sqlite_fts5` enables an indexed SQLite FTS5 lexical
  backend while keeping the same local vector-store search response shape. The
  first search can lazily repair missing FTS5 rows for an existing vector
  store.
- Semantic, hybrid, and local rerank subsets become available when
  `retrieval.index.backend=sqlite_vec` and a retrieval embedder are configured.
- `retrieval.index.backend=pgvector` enables alpha exact pgvector semantic
  search when `storage.backend=postgres` and a retrieval embedder are
  configured. It supports the same direct search and local `file_search`
  response shapes, plus lexical fallback and weighted hybrid ranking, but it
  does not claim hosted OpenAI ranking parity.
- With `storage.backend=postgres`, the retrieval index must be `lexical` or
  `pgvector`; `sqlite_fts5` and `sqlite_vec` remain SQLite-specific.
- `/debug/capabilities.runtime.retrieval` reports the active storage backend,
  retrieval index backend, embedder backend, semantic/hybrid/rerank support,
  and whether the active local index can lazily repair stale chunks.
- Devstack has a focused `sqlite_fts5` smoke path:
  `RETRIEVAL_INDEX_BACKEND=sqlite_fts5 make devstack-up` followed by
  `make devstack-sqlite-fts5-smoke`.
- Devstack has a focused Postgres/pgvector alpha smoke path:
  `STORAGE_BACKEND=postgres RETRIEVAL_INDEX_BACKEND=pgvector
  RETRIEVAL_EMBEDDER_BACKEND=openai_compatible
  RETRIEVAL_EMBEDDER_BASE_URL=http://fixture:8081
  RETRIEVAL_EMBEDDER_MODEL=devstack-embedding make devstack-up` followed by
  `make devstack-postgres-pgvector-smoke`.
- `make postgres-storage-test` runs the package-level Postgres alpha hardening
  suite against `POSTGRES_TEST_DSN`, defaulting to the devstack Postgres port.
  It creates isolated schemas and verifies persistence, pagination, sidecar
  mirroring, failure states, lexical search, and pgvector semantic/hybrid
  search.
- Canonical ranking values are `auto` and `default-2024-08-21`; shim-local
  `none` disables the local rerank stage.
- `include=["file_search_call.results"]` returns the practical local result
  subset that was retrieved.
- Local `file_search` injects a bounded 20-chunk grounding context before the
  final answer stage.

## Gotchas

- Binary and unsupported attachments are not silently accepted. They can fail
  with explicit file/vector-store status errors.
- Exact hosted retrieval ranking quality is not claimed.
- `file_search` is usable and practical, but it is still documented as a broad
  subset rather than exact hosted planner parity.

## Related Docs

- [Tools Overview](tools.md)
- [Local semantic retrieval setup](../semantic-retrieval-embedanything.md)
- [V3 storage and retrieval backend plan](../v3-storage-retrieval-backends.md)
- [Official file-search guide](https://developers.openai.com/api/docs/guides/tools-file-search)
- [Official retrieval guide](https://developers.openai.com/api/docs/guides/retrieval)
