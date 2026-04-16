# Dev Stack

This guide describes the smallest reproducible local stack for `llama_shim`
after the V2 freeze.

It is meant for fast operator sanity checks, CI smoke runs, and future
external-tester integration. It is not a replacement for the full Go
integration suite.

## What It Starts

The dev stack consists of two processes:

- `shim`: the normal `llama_shim` server
- `devstack-fixture`: a deterministic helper backend that provides:
  - a `llama`-compatible text backend for local generation
  - deterministic chat-completions planning for hosted/server `tool_search`
    follow-up
  - a `searxng`-compatible search backend for local `web_search`
  - a deterministic OpenAI-compatible `/v1/responses` image backend for local
    `image_generation`
  - a deterministic remote MCP server on `/mcp` and legacy `/sse` for
    shim-local `mcp.server_url`
  - fixed HTML pages linked from deterministic search results for targeted
    debugging and explicit tests

## Quick Start

Bring the stack up:

```bash
make devstack-up
```

Run the smoke path:

```bash
make devstack-smoke
```

Stop the stack:

```bash
make devstack-down
```

Equivalent raw Compose commands:

```bash
docker compose -f docker-compose.devstack.yml up -d --build
bash ./scripts/devstack-smoke.sh
docker compose -f docker-compose.devstack.yml down --remove-orphans
```

## Ports

- shim: `http://127.0.0.1:18080`
- fixture backend: `http://127.0.0.1:18081`

The shim itself talks to the fixture backend over the Compose network as
`http://fixture:8081`.

## What The Smoke Path Verifies

`scripts/devstack-smoke.sh` checks the following in one narrow run:

- fixture `GET /healthz`
- shim `GET /readyz`
- shim `GET /debug/capabilities`
- stateful `POST /v1/responses` with `previous_response_id`
- local `file_search` over shim-owned `files` and `vector_stores`
- local `web_search` over the deterministic fixture backend
- local `image_generation` through the deterministic fixture backend
- local remote `mcp` via `server_url`
- cached remote `mcp` follow-up without repeating tools
- streamed generic replay for remote `mcp`
- hosted/server `tool_search` with namespace loading
- stored `tool_search` follow-up through `function_call_output`
- streamed generic replay for `tool_search`

The goal is not to benchmark model quality. The goal is to prove that the
stack is runnable, probeable, and reproducible.

## Files

- [config.devstack.yaml](../../config.devstack.yaml): shim config used by the stack
- [docker-compose.devstack.yml](../../docker-compose.devstack.yml): Compose wiring
- [scripts/devstack-smoke.sh](../../scripts/devstack-smoke.sh): repo-owned smoke path
- [cmd/devstack-fixture/main.go](../../cmd/devstack-fixture/main.go): deterministic fixture service
- [internal/devstackfixture/mcp.go](../../internal/devstackfixture/mcp.go): deterministic MCP fixture transport

## Notes

- The dev stack uses lexical retrieval, not `sqlite_vec`, to avoid extra local
  embedder requirements.
- The fixture backend is deterministic by design. If the smoke path fails, the
  failure should usually be actionable as a shim or environment issue rather
  than a model-quality fluctuation.
- The remote MCP target for local smoke is `http://127.0.0.1:18081/mcp` on the
  host and `http://fixture:8081/mcp` inside Compose.
- The hosted/server `tool_search` smoke path uses a namespace-based deferred
  tool example, matching the current OpenAI docs guidance to prefer
  namespaces where practical.
