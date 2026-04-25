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

Run the CI-compatible smoke gate:

```bash
make devstack-ci-smoke
```

Run the full local smoke gate, including real Codex CLI checks:

```bash
make devstack-full-smoke
```

Run the focused V3 native coding-tools smoke path:

```bash
make v3-coding-tools-smoke
```

Run the real Codex CLI smoke path:

```bash
make codex-cli-devstack-smoke
```

Run the real Codex CLI fallback-shell smoke path:

```bash
make codex-cli-shell-tool-smoke
```

Run the real Codex CLI coding-task smoke path:

```bash
make codex-cli-coding-task-smoke
```

Run the real Codex CLI task matrix smoke path:

```bash
make codex-cli-task-matrix-smoke
```

Stop the stack:

```bash
make devstack-down
```

Equivalent raw Compose commands:

```bash
docker compose -f docker-compose.devstack.yml up -d --build
make devstack-ci-smoke
docker compose -f docker-compose.devstack.yml down --remove-orphans
```

Equivalent individual commands:

```bash
bash ./scripts/devstack-smoke.sh
bash ./scripts/v3-coding-tools-smoke.sh
bash ./scripts/codex-cli-devstack-smoke.sh
bash ./scripts/codex-cli-shell-tool-smoke.sh
bash ./scripts/codex-cli-coding-task-smoke.sh
bash ./scripts/codex-cli-task-matrix-smoke.sh
docker compose -f docker-compose.devstack.yml down --remove-orphans
```

## Ports

- shim: `http://127.0.0.1:18080`
- fixture backend: `http://127.0.0.1:18081`

The shim itself talks to the fixture backend over the Compose network as
`http://fixture:8081`.

## What The Smoke Path Verifies

`make devstack-ci-smoke` is the repo-owned CI-compatible smoke gate. It runs:

- `make devstack-smoke`
- `make responses-websocket-smoke`
- `make v3-coding-tools-smoke`

It intentionally does not require the real `codex` binary.

`make devstack-full-smoke` is the local heavy smoke gate. It runs the
CI-compatible gate plus real Codex CLI checks:

- `make devstack-smoke`
- `make responses-websocket-smoke`
- `make v3-coding-tools-smoke`
- `make codex-cli-devstack-smoke`
- `make codex-cli-shell-tool-smoke`
- `make codex-cli-task-matrix-smoke`

`scripts/devstack-smoke.sh` checks the following in one narrow run:

- fixture `GET /healthz`
- shim `GET /readyz`
- shim `GET /debug/capabilities`
- stored Chat Completions create/list/get/messages local-first surface
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

`cmd/responses-websocket-smoke` checks the focused V3 Responses WebSocket
transport:

- connects to `ws://127.0.0.1:18080/v1/responses`
- sends sequential `response.create` frames
- verifies `previous_response_id` continuation
- verifies native-local `shell` and `apply_patch` replay events over JSON text
  frames
- verifies WebSocket transport for local `file_search`, `web_search`,
  `image_generation`, remote MCP, cached MCP follow-up, hosted/server
  `tool_search`, and `tool_search` follow-up

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
- Responses WebSocket transport must be available; HTTP 405 from
  `ws://.../v1/responses` is now a failure
- the Codex request stays on the shim-local tool loop despite Codex CLI request
  metadata such as `prompt_cache_key` and empty `include`
- Codex executes one local `exec_command` and then receives a final `READY`
  assistant message

`scripts/codex-cli-shell-tool-smoke.sh` checks the same real Codex CLI bridge
with unified exec disabled:

- the real `codex exec` binary targets the shim through `openai_base_url`
- Codex runs with `features.unified_exec=false`
- the stored request includes the fallback Codex function tool named `shell`
- the stored request does not include `exec_command` or `write_stdin`
- Codex executes one local command and then receives a final `READY` assistant
  message

`scripts/codex-cli-coding-task-smoke.sh` checks the same real Codex CLI bridge
with a scratch coding task:

- the real `codex exec` binary targets the shim through `openai_base_url`
- Codex executes a deterministic local `exec_command`
- `smoke_target.txt` in `.tmp/codex-coding-task-smoke/workspace` changes from
  `status = TODO` to `status = patched-by-codex`
- Codex receives a final `PATCHED` assistant message and the turn completes

`scripts/codex-cli-task-matrix-smoke.sh` expands that same bridge check into a
small deterministic task matrix:

- `basic_patch`: updates one scratch text file and receives `PATCHED`
- `bugfix_go`: fixes a tiny Go package, then verifies `go test ./...`
- `plan_doc`: writes a deterministic `PLAN.md` checklist and receives
  `PLANNED`
- `multi_file`: updates two files under one scratch workspace and receives
  `MULTIFILE`

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
- [scripts/codex-cli-shell-tool-smoke.sh](../../scripts/codex-cli-shell-tool-smoke.sh):
  real Codex CLI fallback-shell smoke path
- [scripts/codex-cli-coding-task-smoke.sh](../../scripts/codex-cli-coding-task-smoke.sh):
  real Codex CLI coding-task smoke path
- [scripts/codex-cli-task-matrix-smoke.sh](../../scripts/codex-cli-task-matrix-smoke.sh):
  real Codex CLI task matrix smoke path
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
