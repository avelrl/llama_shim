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

Run the focused V3 native coding-tools smoke path:

```bash
make v3-coding-tools-smoke
```

Run the real Codex CLI smoke path:

```bash
make codex-cli-devstack-smoke
```

Stop the stack:

```bash
make devstack-down
```

Equivalent raw Compose commands:

```bash
docker compose -f docker-compose.devstack.yml up -d --build
bash ./scripts/devstack-smoke.sh
bash ./scripts/v3-coding-tools-smoke.sh
bash ./scripts/codex-cli-devstack-smoke.sh
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

`scripts/v3-coding-tools-smoke.sh` checks the focused V3 native coding-tools
subset:

- `/debug/capabilities` exposes native-local `shell` and `apply_patch` flags
- non-stream `shell_call` plus `shell_call_output` follow-up
- non-stream `apply_patch_call` plus `apply_patch_call_output` follow-up
- stored retrieve and `/input_items` for both families
- shell create-stream emits `response.shell_call_command.*`
- shell retrieve-stream preserves `shell_call` through generic
  `response.output_item.*`
- apply-patch create/retrieve-stream emit
  `response.apply_patch_call_operation_diff.done`

`scripts/codex-cli-devstack-smoke.sh` checks practical Codex CLI compatibility:

- the real `codex exec` binary targets the shim through `openai_base_url`
- the Codex request stays on the shim-local tool loop despite Codex CLI request
  metadata such as `prompt_cache_key` and empty `include`
- Codex executes one local `exec_command` and then receives a final `READY`
  assistant message
- Codex CLI 0.124 WebSocket 405 logs are tolerated only if HTTP fallback
  completes successfully

The goal is not to benchmark model quality. The goal is to prove that the
stack is runnable, probeable, and reproducible.

## Files

- [config.devstack.yaml](../../config.devstack.yaml): shim config used by the stack
- [docker-compose.devstack.yml](../../docker-compose.devstack.yml): Compose wiring
- [scripts/devstack-smoke.sh](../../scripts/devstack-smoke.sh): repo-owned smoke path
- [scripts/v3-coding-tools-smoke.sh](../../scripts/v3-coding-tools-smoke.sh):
  focused native coding-tools smoke path
- [scripts/codex-cli-devstack-smoke.sh](../../scripts/codex-cli-devstack-smoke.sh):
  real Codex CLI smoke path
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
