# llama_shim

`llama_shim` is a small Go 1.26 HTTP service that exposes a minimal OpenAI-compatible subset for Responses + Conversations while keeping `llama.cpp` as an unchanged stateless backend.

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

An example file is provided in [config.yaml.example](/Users/avel/Projects/llama_shim/config.yaml.example).

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

responses:
  custom_tools:
    mode: bridge
  codex:
    force_tool_choice_required: false
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
- `LLAMA_BASE_URL` overrides `llama.base_url`
- `SQLITE_PATH` overrides `sqlite.path`
- `SHIM_ADDR` overrides `shim.addr`
- `RESPONSES_CUSTOM_TOOLS_MODE` overrides `responses.custom_tools.mode`; supported values: `bridge`, `auto`, `passthrough`
- `RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED` overrides `responses.codex.force_tool_choice_required`; when enabled, Codex-like requests with `tool_choice: "auto"` are rewritten to `required`

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
