# llama_shim

`llama_shim` is a small Go 1.26 HTTP service that exposes a minimal OpenAI-compatible subset for Responses + Conversations while keeping `llama.cpp` as an unchanged stateless backend.

For a Russian translation, see [README.ru.md](README.ru.md).

v1 supports:

- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `POST /v1/conversations`
- `POST /v1/responses` with `stream: true` over SSE
- stored Chat Completions list/get/update/delete/messages local-first surface;
  shim-owned shadow storage is the core path, and upstream-owned historical
  resources are only an optional compatibility bridge when the upstream backend
  supports stored-chat routes; local shadow storage follows a shim-owned
  omitted-`store` policy and includes streamed reconstruction:
  `GET /v1/chat/completions`,
  `GET/POST/DELETE /v1/chat/completions/{completion_id}`,
  `GET /v1/chat/completions/{completion_id}/messages`
- SQLite-backed state reconstruction for `previous_response_id`
- SQLite-backed conversation history for `conversation`
- fallback proxying for non-shim routes directly to the upstream backend

## Compatibility Roadmap

- V2 release framing: a broad compatibility facade over the current official
  OpenAI surface already exposed by the shim
- per-surface status: [docs/compatibility-matrix.md](docs/compatibility-matrix.md)
- frozen V2 release ledger: [docs/v2-scope.md](docs/v2-scope.md)
- completed V3 preflight substrate: [docs/v3-preflight.md](docs/v3-preflight.md)
- post-V2 capability expansion parking lot: [docs/v3-scope.md](docs/v3-scope.md)
- extension and plugin directions after the compatibility core:
  [docs/v4-scope.md](docs/v4-scope.md)
- exact hosted-parity and advanced transport backlog:
  [docs/v5-scope.md](docs/v5-scope.md)

## Documentation

- practical guides: [docs/guides/README.md](docs/guides/README.md)
- engineering notes: [docs/engineering/README.md](docs/engineering/README.md)
- runtime hardening notes: [docs/engineering/runtime-hardening.md](docs/engineering/runtime-hardening.md)
- V2 release notes: [docs/release-notes-v2.md](docs/release-notes-v2.md)
- API contract and boundaries: [docs/compatibility-matrix.md](docs/compatibility-matrix.md)
- V2 release scope: [docs/v2-scope.md](docs/v2-scope.md)
- completed V3 preflight substrate: [docs/v3-preflight.md](docs/v3-preflight.md)
- deterministic dev stack and smoke path: [docs/guides/devstack.md](docs/guides/devstack.md)
- Responses compatibility external tester:
  [docs/engineering/responses-compatibility-external-tester.md](docs/engineering/responses-compatibility-external-tester.md)
- V3 expansion staging: [docs/v3-scope.md](docs/v3-scope.md)
- V4 extensions and plugin model: [docs/v4-scope.md](docs/v4-scope.md)
- V5 hosted parity and advanced transports: [docs/v5-scope.md](docs/v5-scope.md)
- OpenAPI spec: [openapi/openapi.yaml](openapi/openapi.yaml)

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

You can run the shim with plain environment variables, with a YAML config file, or with both. Environment variables override values from YAML. If a repo-local `.env` file exists, the shim loads it first as a convenience layer and then lets real process environment variables win. A starter template is provided in [.env.example](.env.example).

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
  auth:
    mode: disabled
    bearer_tokens: []
  rate_limit:
    enabled: false
    requests_per_minute: 120
    burst: 60
  metrics:
    enabled: true
    path: /metrics
  limits:
    json_body_bytes: 1MiB
    retrieval_file_upload_bytes: 64MiB
    chat_completions_shadow_store_timeout: 5s
    retrieval_max_concurrent_searches: 8
    retrieval_max_search_queries: 4
    retrieval_max_grounding_chunks: 20
    code_interpreter_max_concurrent_runs: 2

sqlite:
  path: ./data/shim.db
  maintenance:
    cleanup_interval: 15m

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

chat_completions:
  default_store_when_omitted: true

responses:
  mode: prefer_local
  web_search:
    backend: disabled
    base_url: ""
    timeout: 10s
    max_results: 10
  image_generation:
    backend: disabled
    base_url: ""
    timeout: 60s
  computer:
    backend: disabled
  custom_tools:
    mode: auto
  codex:
    enable_compatibility: true
    force_tool_choice_required: true
  code_interpreter:
    backend: disabled
    execution_timeout: 20s
    docker:
      binary: docker
      image: python:3.12-slim
      memory_limit: 1g
      cpu_limit: "0.5"
      pids_limit: 64
    input_file_url_policy: disabled
    input_file_url_allow_hosts: []
    cleanup_interval: 1m
    limits:
      generated_files: 8
      generated_file_bytes: 2MiB
      generated_total_bytes: 8MiB
      remote_input_file_bytes: 50MiB
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
- `SQLITE_MAINTENANCE_CLEANUP_INTERVAL` overrides `sqlite.maintenance.cleanup_interval`
- `SHIM_ADDR` overrides `shim.addr`
- `SHIM_AUTH_MODE` overrides `shim.auth.mode`; supported values: `disabled`, `static_bearer`
- `SHIM_AUTH_BEARER_TOKENS` overrides `shim.auth.bearer_tokens` as a comma-separated list
- `SHIM_RATE_LIMIT_ENABLED` overrides `shim.rate_limit.enabled`
- `SHIM_RATE_LIMIT_REQUESTS_PER_MINUTE` overrides `shim.rate_limit.requests_per_minute`
- `SHIM_RATE_LIMIT_BURST` overrides `shim.rate_limit.burst`
- `SHIM_METRICS_ENABLED` overrides `shim.metrics.enabled`
- `SHIM_METRICS_PATH` overrides `shim.metrics.path`
- `SHIM_LIMITS_JSON_BODY_BYTES` overrides `shim.limits.json_body_bytes`
- `SHIM_LIMITS_RETRIEVAL_FILE_UPLOAD_BYTES` overrides `shim.limits.retrieval_file_upload_bytes`
- `SHIM_LIMITS_CHAT_COMPLETIONS_SHADOW_STORE_TIMEOUT` overrides `shim.limits.chat_completions_shadow_store_timeout`
- `SHIM_LIMITS_RETRIEVAL_MAX_CONCURRENT_SEARCHES` overrides `shim.limits.retrieval_max_concurrent_searches`
- `SHIM_LIMITS_RETRIEVAL_MAX_SEARCH_QUERIES` overrides `shim.limits.retrieval_max_search_queries`
- `SHIM_LIMITS_RETRIEVAL_MAX_GROUNDING_CHUNKS` overrides `shim.limits.retrieval_max_grounding_chunks`
- `SHIM_LIMITS_CODE_INTERPRETER_MAX_CONCURRENT_RUNS` overrides `shim.limits.code_interpreter_max_concurrent_runs`
- `RETRIEVAL_INDEX_BACKEND` overrides `retrieval.index.backend`; supported values: `lexical`, `sqlite_vec`
- `RETRIEVAL_EMBEDDER_BACKEND` overrides `retrieval.embedder.backend`; supported values: `disabled`, `openai_compatible`, `embedanything`
- `RETRIEVAL_EMBEDDER_BASE_URL` overrides `retrieval.embedder.base_url`
- `RETRIEVAL_EMBEDDER_MODEL` overrides `retrieval.embedder.model`
- `CHAT_COMPLETIONS_DEFAULT_STORE_WHEN_OMITTED` overrides `chat_completions.default_store_when_omitted`
- `RESPONSES_MODE` overrides `responses.mode`; supported values: `prefer_local`, `prefer_upstream`, `local_only`
  `prefer_local` is the default: the shim owns `/v1/responses` whenever the request fits the locally-supported subset, and falls back to upstream `/v1/responses` only for unsupported features.
- `RESPONSES_WEB_SEARCH_BACKEND` overrides `responses.web_search.backend`; supported values: `disabled`, `searxng`
- `RESPONSES_WEB_SEARCH_BASE_URL` overrides `responses.web_search.base_url`
- `RESPONSES_WEB_SEARCH_TIMEOUT` overrides `responses.web_search.timeout`
- `RESPONSES_WEB_SEARCH_MAX_RESULTS` overrides `responses.web_search.max_results`
- `RESPONSES_IMAGE_GENERATION_BACKEND` overrides `responses.image_generation.backend`; supported values: `disabled`, `responses`
- `RESPONSES_IMAGE_GENERATION_BASE_URL` overrides `responses.image_generation.base_url`
- `RESPONSES_IMAGE_GENERATION_TIMEOUT` overrides `responses.image_generation.timeout`
- `RESPONSES_COMPACTION_BACKEND` overrides `responses.compaction.backend`; supported values: `heuristic`, `model_assisted_text`
- `RESPONSES_COMPACTION_BASE_URL` overrides `responses.compaction.base_url`; when omitted with `model_assisted_text`, the shim reuses `llama.base_url`
- `RESPONSES_COMPACTION_MODEL` overrides `responses.compaction.model`
- `RESPONSES_COMPACTION_TIMEOUT` overrides `responses.compaction.timeout`
- `RESPONSES_COMPACTION_MAX_OUTPUT_TOKENS` overrides `responses.compaction.max_output_tokens`
- `RESPONSES_COMPACTION_RETAINED_ITEMS` overrides `responses.compaction.retained_items`
- `RESPONSES_COMPACTION_MAX_INPUT_CHARS` overrides `responses.compaction.max_input_chars`
- `RESPONSES_COMPUTER_BACKEND` overrides `responses.computer.backend`; supported values: `disabled`, `chat_completions`
- `RESPONSES_CUSTOM_TOOLS_MODE` overrides `responses.custom_tools.mode`; supported values: `bridge`, `auto`, `passthrough`
  Use `auto` for the default path: it keeps bridge behavior for plain-text custom tools, routes supported `grammar` / `regex` custom tools into the shim-local constrained path, uses backend-native structured generation of raw `input` for named constrained custom tools and `tool_choice: "required"` with a single constrained tool, and in broader auto/mixed cases runs a shim-local tool selector before backend-native constrained generation for the selected custom tool. Shim-local `tool_choice.type=allowed_tools` is supported for function/custom subsets. The old validation/repair loop remains only as an error fallback, not the happy path.
- `RESPONSES_CODEX_ENABLE_COMPATIBILITY` overrides `responses.codex.enable_compatibility`; when disabled, the shim stops injecting Codex-specific instructions/context and skips Codex-specific response normalization
- `RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED` overrides `responses.codex.force_tool_choice_required`; when enabled, Codex-like requests with `tool_choice: "auto"` are rewritten to `required`
- `RESPONSES_CODE_INTERPRETER_BACKEND` overrides `responses.code_interpreter.backend`; supported values: `disabled`, `docker`
- `RESPONSES_CODE_INTERPRETER_DOCKER_BINARY` overrides `responses.code_interpreter.docker.binary`
- `RESPONSES_CODE_INTERPRETER_DOCKER_IMAGE` overrides `responses.code_interpreter.docker.image`
- `RESPONSES_CODE_INTERPRETER_DOCKER_MEMORY_LIMIT` overrides `responses.code_interpreter.docker.memory_limit`
- `RESPONSES_CODE_INTERPRETER_DOCKER_CPU_LIMIT` overrides `responses.code_interpreter.docker.cpu_limit`
- `RESPONSES_CODE_INTERPRETER_DOCKER_PIDS_LIMIT` overrides `responses.code_interpreter.docker.pids_limit`
- `RESPONSES_CODE_INTERPRETER_EXECUTION_TIMEOUT` overrides `responses.code_interpreter.execution_timeout`
- `RESPONSES_CODE_INTERPRETER_INPUT_FILE_URL_POLICY` overrides `responses.code_interpreter.input_file_url_policy`
- `RESPONSES_CODE_INTERPRETER_INPUT_FILE_URL_ALLOW_HOSTS` overrides `responses.code_interpreter.input_file_url_allow_hosts`
- `RESPONSES_CODE_INTERPRETER_CLEANUP_INTERVAL` overrides `responses.code_interpreter.cleanup_interval`
- `RESPONSES_CODE_INTERPRETER_LIMITS_GENERATED_FILES` overrides `responses.code_interpreter.limits.generated_files`
- `RESPONSES_CODE_INTERPRETER_LIMITS_GENERATED_FILE_BYTES` overrides `responses.code_interpreter.limits.generated_file_bytes`
- `RESPONSES_CODE_INTERPRETER_LIMITS_GENERATED_TOTAL_BYTES` overrides `responses.code_interpreter.limits.generated_total_bytes`
- `RESPONSES_CODE_INTERPRETER_LIMITS_REMOTE_INPUT_FILE_BYTES` overrides `responses.code_interpreter.limits.remote_input_file_bytes`

Response retention notes:

- standalone `/v1/responses` objects follow the outward `store` contract returned on the response object
- conversation-attached items follow the conversation lifecycle instead of standalone response retention
- the shim may keep hidden response rows needed for local `previous_response_id` replay even when the outward response reports `store=false`

## Maintenance

The shim now includes a minimal operator maintenance path:

- background SQLite retention cleanup via `sqlite.maintenance.cleanup_interval`
- one-shot maintenance commands via `./cmd/shimctl`
- local DX packaging via `Makefile`, `Dockerfile`, and `docker-compose.yml`

`sqlite.maintenance.cleanup_interval` currently sweeps only local resources
with explicit `expires_at` retention:

- expired `/v1/files`
- expired `/v1/vector_stores`

`code_interpreter` container expiry remains controlled separately by
`responses.code_interpreter.cleanup_interval`.

Examples:

```bash
go run ./cmd/shimctl -config ./config.yaml cleanup
go run ./cmd/shimctl -config ./config.yaml optimize
go run ./cmd/shimctl -config ./config.yaml vacuum
go run ./cmd/shimctl -config ./config.yaml probe
go run ./cmd/shimctl -config ./config.yaml probe -probe-count 3 -request-timeout 8s -model unsloth/Kimi-K2.5
go run ./cmd/shimctl -config ./config.yaml backup -out ./.data/shim-backup.db
go run ./cmd/shimctl -config ./config.yaml restore -from ./.data/shim-backup.db
```

The restore path is intentionally offline-oriented: stop the running shim
before replacing the SQLite file.

`shimctl` uses the same `config.yaml` and the same repo-local `.env` as the
running shim, but it only reads the subset of fields it needs. For probe-only
overrides in `.env`, use `SHIMCTL_PROBE_BEARER_TOKEN` and
`SHIMCTL_PROBE_MODEL`. `shimctl probe` runs the same calibration logic on
demand, without starting the HTTP server, prints live per-request progress to
`stderr` including the full successful assistant content for each probe, and
writes the structured result snapshot as JSON to `stdout`.

## Local DX

Minimal local packaging is now checked into the repo:

- `make run`, `make test`, `make build`
- `make maint-cleanup`, `make maint-optimize`, `make maint-vacuum`, `make maint-backup`
- `docker build -t llama-shim:local .`
- `docker compose up --build`
- `make devstack-up`, `make devstack-smoke`, `make devstack-down`

The compose setup mounts `./config.yaml` into the container and keeps SQLite
state in `./.data`.

For a deterministic two-process dev stack with a repo-owned smoke path, see
[docs/guides/devstack.md](docs/guides/devstack.md).

## Ops hardening

The shim now has a shim-owned operational layer that is separate from route-contract parity:

- optional ingress bearer auth via `shim.auth.mode=static_bearer`
- optional in-memory per-client request rate limiting via `shim.rate_limit.*`
- optional shim-owned capability manifest at `/debug/capabilities`
- optional on-demand upstream sizing probe via `shimctl probe` and `probe.*` in `config.yaml`
- optional Prometheus-text metrics at `shim.metrics.path` (default `/metrics`)
- configurable request, upload, retrieval, and local `code_interpreter` limits
- structured JSON logs with `request_id`, optional `client_request_id`, stable route labels, auth subject fingerprints, and retrieval/runtime events

Important behavior:

- `/healthz` and `/readyz` stay unauthenticated and unthrottled so external probes keep working
- `/debug/capabilities` is a shim-owned operator/debug route that reports current surfaces, routing classes, runtime config, and dependency probe state; it returns `200` even when some dependencies are degraded, and shares normal shim auth and request rate limiting
- `shimctl probe` remains shim-owned and separate from the running HTTP server: it reads `probe.*` from the shared `config.yaml`, probes documented upstream `/v1/models` and `/v1/chat/completions` endpoints on demand, and prints a structured JSON snapshot with conservative sizing guidance
- the optional calibration token is scoped only to `shimctl probe`; `/readyz`, normal request handling, proxy/backend traffic, and `/debug/capabilities` never borrow it
- `/metrics` is skipped by the request rate limiter but still shares ingress auth when shim auth is enabled
- when shim ingress auth is enabled, the ingress `Authorization` header is consumed by the shim and is not forwarded to the upstream text-generation backend; `X-Client-Request-Id` still propagates upstream
- request rate limiting is currently a shim-owned in-memory subset with request-based headers:
  `X-RateLimit-Limit-Requests`,
  `X-RateLimit-Remaining-Requests`,
  `X-RateLimit-Reset-Requests`
- retrieval and local runtime limits are shim-owned operational controls, not claims about hosted OpenAI quotas

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

When `responses.web_search.backend=searxng` is enabled, `/readyz` also checks that the configured web search backend answers before returning `ready`.

When `responses.image_generation.backend=responses` is enabled, `/readyz` also checks that the configured image-generation `/v1/responses` backend answers before returning `ready`.

For a step-by-step local setup and a smoke test script, see [docs/semantic-retrieval-embedanything.md](docs/semantic-retrieval-embedanything.md).

## Local web search

The shim now has a pragmatic local `web_search` / `web_search_preview` subset inside `/v1/responses`:

- one `web_search` or `web_search_preview` tool in `responses.mode=prefer_local|local_only`
- deterministic query planning and rewrite through the same local planner family used by retrieval
- a configurable `searxng` backend for the search step
- heuristic `open_page` and `find_in_page` follow-up when the prompt clearly asks to inspect a page or find an exact phrase
- final assistant messages carry a pragmatic `url_citation` subset with visible source links
- `include=["web_search_call.action.sources"]` is accepted in the local subset

This is intentionally not a claim of full hosted browsing parity. Exact hosted planner behavior, broader live-web semantics, and full hosted failure choreography remain separate follow-up work.

## Local image generation

The shim now has a pragmatic local `image_generation` subset inside
`/v1/responses`:

- one `image_generation` tool in `responses.mode=prefer_local|local_only`
- a separate OpenAI-compatible `/v1/responses` image backend selected via
  `responses.image_generation.backend=responses`
- non-stream and stream create paths, with stored shadow-state and retrieve
  replay handled by the shim
- current-turn `input_image` parts and local `previous_response_id` edit
  lineage are forwarded to the image backend through the flattened Responses
  `input` owned by the shim
- when the backend stream emits
  `response.image_generation_call.partial_image`, the shim persists those
  irrecoverable artifacts and replays them on stored
  `GET /v1/responses/{id}?stream=true`

This is intentionally not a claim of exact hosted live-stream timing or full
hosted planner/failure choreography. The current local subset delegates image
tool execution to a dedicated Responses-compatible backend and then replays the
stored result through the shim-owned Responses surface.

## Local computer use

The shim now has a pragmatic local `computer` subset inside `/v1/responses`:

- one `computer` tool in `responses.mode=prefer_local|local_only`
- explicit enablement via `responses.computer.backend=chat_completions`
- a screenshot-first external loop through stored `computer_call` and
  follow-up `computer_call_output` items
- multimodal planner turns: current-turn `computer_call_output` screenshot
  inputs are projected into the shim-owned planner request as text plus image
  context, and `previous_response_id` lineage keeps the latest loop state
- non-stream create, stream create, stored retrieve, and stored
  `/v1/responses/{id}/input_items` preserve the typed
  `computer_call` / `computer_call_output` subset
- create-stream and retrieve-stream stay generic through
  `response.output_item.*`; the shim does not invent a
  `response.computer_call.*` SSE family

This is intentionally not a claim of exact hosted planner behavior or full
hosted computer-use choreography. The current local subset is a docs-aligned
external loop over the existing `/v1/chat/completions` backend, with exact
hosted action-shape edge cases and broader failure/status parity left open.

## Remote MCP in local mode

The shim now supports a pragmatic local-first remote MCP subset inside
`/v1/responses`:

- request-declared `mcp` tools that use `server_url`
- import into stored `mcp_list_tools`, with cached reuse across
  `previous_response_id`
- approvals via `mcp_approval_request` and follow-up
  `mcp_approval_response`
- real `mcp_call` execution and same-turn assistant completion
- both legacy HTTP/SSE MCP endpoints such as `https://.../sse` and basic
  streamable HTTP MCP endpoints such as `https://.../mcp`
- generic create/retrieve replay for `mcp_list_tools` /
  `mcp_approval_request` and existing `mcp_call` replay semantics

Boundaries of the current local subset:

- shim-local remote `mcp` rejects request-supplied `authorization` and
  `headers`; upstream or a trusted proxy must own outbound credentials
- connectors (`connector_id`) remain an upstream-only compatibility bridge,
  not a shim-local runtime; the shim now validates connector-aware MCP tool
  definitions and sanitizes `authorization`, `headers`, and `server_url`
  from visible Response request surfaces on both create and retrieve
- broader hosted failure/status parity remains open

This keeps the local runtime useful without overclaiming parity for MCP
surfaces that have different trust, auth, or transport semantics.

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
