# llama_shim

`llama_shim` is a small Go 1.26 HTTP service that exposes a minimal OpenAI-compatible subset for Responses + Conversations while keeping `llama.cpp` as an unchanged stateless backend.

For a Russian translation, see [README.ru.md](README.ru.md).

v1 supports:

- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `POST /v1/conversations`
- `POST /v1/responses` with `stream: true` over SSE
- SQLite-backed state reconstruction for `previous_response_id`
- SQLite-backed conversation history for `conversation`
- fallback proxying for non-shim routes directly to the upstream backend

## Architecture

- `cmd/shim`: process bootstrap and HTTP server startup
- `internal/httpapi`: thin handlers, JSON error mapping, request ID and request logging middleware
- `internal/service`: orchestration for response generation and conversation creation
- `internal/storage/sqlite`: explicit SQL, migrations, WAL mode, foreign keys, busy timeout
- `internal/llama`: adapter for `llama.cpp` `POST /v1/chat/completions`
- `internal/domain`: input normalization, context reconstruction, ID generation, response normalization

Key design choices:

- `llama.cpp` remains stateless; the shim owns all state semantics
- SQLite is the only persistent store in v1
- write transactions stay short; the service never keeps a DB transaction open while waiting for generation
- response and conversation objects use a compact, stable JSON shape
- non-shim upstream routes can stream through the shim via SSE passthrough

## Requirements

- Go 1.26+
- a running `llama.cpp` server that exposes `POST /v1/chat/completions`

## Running llama.cpp locally

One possible local setup is:

```bash
./llama-server \
  -m /path/to/model.gguf \
  --host 127.0.0.1 \
  --port 8081
```

This README assumes `llama.cpp` is already running separately and reachable at:

```text
POST /v1/chat/completions
```

## Running the shim

You can run the shim with plain environment variables, with a YAML config file, or with both. Environment variables override values from YAML.

```bash
LLAMA_BASE_URL=http://127.0.0.1:8081 \
SQLITE_PATH=./data/shim.db \
SHIM_ADDR=:8080 \
go run ./cmd/shim
```

### YAML config

An example file is provided in [config.yaml.example](config.yaml.example).

Example:

```yaml
shim:
  addr: ":8080"
  read_timeout: 15s
  write_timeout: 90s
  idle_timeout: 60s

sqlite:
  path: ./data/shim.db

llama:
  base_url: http://127.0.0.1:8081
  timeout: 60s

log:
  level: info
  file_path: ./.data/shim.log

retrieval:
  index:
    backend: lexical
  embedder:
    backend: disabled
    base_url: ""
    model: ""

responses:
  mode: prefer_local
  custom_tools:
    mode: auto
  codex:
    enable_compatibility: true
    force_tool_choice_required: true
```

Run with an explicit config file:

```bash
go run ./cmd/shim -config ./config.yaml
```

Or via environment:

```bash
SHIM_CONFIG=./config.yaml go run ./cmd/shim
```

If `-config` and `SHIM_CONFIG` are not set, the service will also try to auto-load `./config.yaml` or `./config.yml` when present.

Supported environment overrides:

- `LLAMA_TIMEOUT` default `60s`
- `SHIM_READ_TIMEOUT` default `15s`
- `SHIM_WRITE_TIMEOUT` default `90s`
- `SHIM_IDLE_TIMEOUT` default `60s`
- `LOG_LEVEL` default `info`; set `debug` to emit an additional debug log line with request and response bodies
- `LOG_FILE_PATH` overrides `log.file_path`; when set, logs are duplicated to stdout and the configured file
- `LLAMA_BASE_URL` overrides `llama.base_url`
- `SQLITE_PATH` overrides `sqlite.path`
- `SHIM_ADDR` overrides `shim.addr`
- `RETRIEVAL_INDEX_BACKEND` overrides `retrieval.index.backend`; supported values: `lexical`, `sqlite_vec`
- `RETRIEVAL_EMBEDDER_BACKEND` overrides `retrieval.embedder.backend`; supported values: `disabled`, `openai_compatible`, `embedanything`
- `RETRIEVAL_EMBEDDER_BASE_URL` overrides `retrieval.embedder.base_url`
- `RETRIEVAL_EMBEDDER_MODEL` overrides `retrieval.embedder.model`
- `RESPONSES_MODE` overrides `responses.mode`; supported values: `prefer_local`, `prefer_upstream`, `local_only`
  `prefer_local` is the default: the shim owns `/v1/responses` whenever the request fits the locally-supported subset, and falls back to upstream `/v1/responses` only for unsupported features.
- `RESPONSES_CUSTOM_TOOLS_MODE` overrides `responses.custom_tools.mode`; supported values: `bridge`, `auto`, `passthrough`
  Use `auto` for the default path: it keeps bridge behavior for plain-text custom tools, routes supported `grammar` / `regex` custom tools into the shim-local constrained path, and for named constrained custom tools first tries backend-native structured generation of raw `input` before falling back to the legacy validation/repair loop.
- `RESPONSES_CODEX_ENABLE_COMPATIBILITY` overrides `responses.codex.enable_compatibility`; when disabled, the shim stops injecting Codex-specific instructions/context and skips Codex-specific response normalization
- `RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED` overrides `responses.codex.force_tool_choice_required`; when enabled, Codex-like requests with `tool_choice: "auto"` are rewritten to `required`

Response retention notes:

- standalone `/v1/responses` objects follow the outward `store` contract returned on the response object
- conversation-attached items follow the conversation lifecycle instead of standalone response retention
- the shim may keep hidden response rows needed for local `previous_response_id` replay even when the outward response reports `store=false`

## Semantic retrieval with sqlite_vec + EmbedAnything

The shim can run local semantic retrieval without an external OpenAI embeddings API:

- `sqlite_vec` stores and searches embeddings inside the same SQLite database
- exact semantic search uses per-store `sqlite-vec` `vec0` KNN tables; this is
  still exact/brute-force today, not ANN
- `EmbedAnything` runs as a local OpenAI-compatible `/v1/embeddings` sidecar
- `ranking_options.hybrid_search` can blend dense semantic matches and lexical
  keyword matches when `sqlite_vec` is enabled
- when `sqlite_vec` is enabled, omitted `ranking_options.ranker` defaults to
  shim-local `auto` reranking; `ranker=none` disables that local rerank stage
- raw `/v1/vector_stores/{id}/search` honors `rewrite_query=true` with a small
  deterministic local rewrite pass and returns the rewritten query in
  `search_query`
- local `/v1/responses` `file_search` reuses that rewrite core and can fan a
  complex prompt out into several rewritten search queries before retrieval;
  this is a pragmatic local subset, not exact hosted planner parity
- raw search results now retain a small per-file multi-snippet subset instead
  of collapsing every file to one best chunk immediately, and local
  `/v1/responses` `file_search` injects only a bounded 20-chunk grounding
  subset before final answer generation
- when `include=["file_search_call.results"]` is used, local result entries
  now expose snippet `content[]` arrays instead of only a flattened text blob
- final local assistant messages now carry a pragmatic shim-local
  `file_citation` subset using `{type,index,file_id,filename}` for
  top-ranked retrieved files; exact hosted file-citation placement/selection
  parity remains open
- when the configured embedder model or embedding dimensions change, the
  `sqlite_vec` path lazily reindexes stale chunks in the queried vector store
  before semantic search so the shim does not mix incompatible embedding spaces

The official EmbedAnything Actix server starts on `http://0.0.0.0:8080`.
The simplest local layout is:

- `EmbedAnything`: `http://127.0.0.1:8080`
- `llama.cpp`: `http://127.0.0.1:8081`
- `llama_shim`: `http://127.0.0.1:8083`

Example config:

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

Then start the shim:

```bash
go run ./cmd/shim -config ./config.yaml
```

If you have an EmbedAnything checkout locally, this repo ships a small helper that
follows the official Actix server guide:

```bash
git clone --depth=1 https://github.com/StarlightSearch/EmbedAnything ../EmbedAnything
EMBEDANYTHING_DIR=../EmbedAnything ./scripts/embedanything-actix-local.sh
```

When `retrieval.index.backend=sqlite_vec` is enabled, `/readyz` also checks the retrieval embedder. For `embedanything`, the shim probes the sidecar `GET /health_check` endpoint before returning `ready`.

For a step-by-step local setup and a smoke test script, see [docs/semantic-retrieval-embedanything.md](docs/semantic-retrieval-embedanything.md).

## curl examples

### POST `/v1/responses`

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"test-model","store":true,"input":"Say OK and nothing else"}'
```

### GET `/v1/responses/{id}`

```bash
curl -s http://127.0.0.1:8080/v1/responses/resp_your_id_here
```

### POST `/v1/conversations`

```bash
curl -s http://127.0.0.1:8080/v1/conversations \
  -H 'Content-Type: application/json' \
  -d '{
    "items": [
      {"type":"message","role":"system","content":"You are a test assistant."},
      {"type":"message","role":"user","content":"Remember: code=777. Reply OK."}
    ]
  }'
```

### POST `/v1/responses` with `previous_response_id`

First request:

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"test-model","store":true,"input":"Remember: my code = 123. Reply OK"}'
```

Then follow up using the returned response ID:

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"test-model",
    "store":true,
    "previous_response_id":"resp_previous_id_here",
    "input":"What was my code? Reply with just the number."
  }'
```

### POST `/v1/responses` with `conversation`

After creating a conversation:

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"test-model",
    "store":true,
    "conversation":"conv_your_id_here",
    "input":"What is the code? Reply with just the number."
  }'
```

### POST `/v1/responses` with `stream: true`

```bash
curl -N http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"test-model",
    "store":true,
    "stream":true,
    "input":"Say OK and nothing else"
  }'
```

The shim emits SSE events including:

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.output_item.done`
- `response.completed`

## API notes

- versioned OpenAPI spec for the current shim-owned surface lives in [openapi/openapi.yaml](openapi/openapi.yaml)
- operations in the spec are marked with `x-shim-status: implemented|partial|proxy` so it is clear where the shim owns the contract and where it only forwards to upstream
- `previous_response_id` and `conversation` are mutually exclusive
- all API errors are returned as JSON
- `output_text` is always present on successful responses
- conversation creation normalizes text content into canonical `input_text` items

## Running tests

```bash
go test ./...
```

The integration tests use:

- a temp SQLite database
- a fake `llama.cpp` server built with `httptest.Server`

Covered acceptance flows:

- store + GET
- `previous_response_id` chain reconstruction
- `conversation` state reconstruction
- missing response and conversation 404s
- 4xx validation for mutually exclusive state fields
