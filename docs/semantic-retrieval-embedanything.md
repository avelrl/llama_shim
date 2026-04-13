# Semantic Retrieval with EmbedAnything

This repo can use `EmbedAnything` as a local embeddings sidecar while keeping
`sqlite_vec` as the vector index/search backend inside the shim's SQLite
database.

Architecture:

- `EmbedAnything` generates embeddings over an OpenAI-compatible
  `/v1/embeddings` endpoint
- `llama_shim` chunks and stores files locally
- `sqlite_vec` stores chunk embeddings and runs exact cosine search inside
  SQLite

## 1. Pick a local port layout

The official EmbedAnything Actix server starts on `http://0.0.0.0:8080`.
To avoid colliding with the shim default port, the simplest local setup is:

- `EmbedAnything`: `http://127.0.0.1:8080`
- `llama.cpp`: `http://127.0.0.1:8081`
- `llama_shim`: `http://127.0.0.1:8083`

## 2. Start EmbedAnything

The project documents an OpenAI-compatible Actix server and a health endpoint.
Follow the official setup instructions from Starlight Search:

- https://embed-anything.com/
- https://embed-anything.com/guides/actix_server/

If you already have an EmbedAnything checkout locally, this repo ships a helper
that follows the documented `cargo run -p server --release` flow:

```bash
git clone --depth=1 https://github.com/StarlightSearch/EmbedAnything ../EmbedAnything
EMBEDANYTHING_DIR=../EmbedAnything ./scripts/embedanything-actix-local.sh
```

The shim expects the EmbedAnything base URL, for example:

```text
http://127.0.0.1:8080
```

## 3. Configure the shim

Example YAML:

```yaml
shim:
  addr: ":8083"

retrieval:
  index:
    backend: sqlite_vec
  embedder:
    backend: embedanything
    base_url: http://127.0.0.1:8080
    model: BAAI/bge-small-en-v1.5
```

Environment-variable form:

```bash
SHIM_ADDR=:8083
RETRIEVAL_INDEX_BACKEND=sqlite_vec
RETRIEVAL_EMBEDDER_BACKEND=embedanything
RETRIEVAL_EMBEDDER_BASE_URL=http://127.0.0.1:8080
RETRIEVAL_EMBEDDER_MODEL=BAAI/bge-small-en-v1.5
```

## 4. Run the smoke test

This repo also ships a smoke script that validates the whole semantic retrieval
path through the shim:

```bash
SHIM_BASE_URL=http://127.0.0.1:8083 \
EMBEDDER_BASE_URL=http://127.0.0.1:8080 \
./scripts/semantic-retrieval-smoke.sh
```

It checks:

- `EmbedAnything` `GET /health_check`
- shim `GET /readyz`
- local file upload
- vector store creation
- semantic search via `/v1/vector_stores/{id}/search`

## 5. Readiness behavior

When `retrieval.index.backend=sqlite_vec` is active, `/readyz` does more than
check SQLite and the upstream llama-compatible backend:

- for `embedanything`, the shim also probes `GET /health_check`
- if the sidecar is down, `/readyz` returns `503`

This is intentional. Semantic retrieval should not report ready if the embedder
process is unavailable.

## 6. Scope of the current semantic subset

What is implemented now:

- persisted chunk embeddings
- exact dense cosine retrieval via `sqlite_vec`
- pluggable embedder backend
- local `/v1/vector_stores/{id}/search`
- local `file_search` can use the same retrieval engine through the existing
  contract

What is still open:

- ANN indexing beyond exact dense scan
- hybrid lexical+dense retrieval
- reranking
- hosted `file_citation` parity in final assistant messages
