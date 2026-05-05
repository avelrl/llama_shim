# Dev Stack

This guide describes the smallest reproducible local stack for `llama_shim`
after the V2 freeze.

It is meant for fast operator sanity checks, CI smoke runs, and future
external-tester integration. It is not a replacement for the full Go
integration suite.

## What It Starts

The dev stack consists of three Compose services:

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
- `postgres`: a pgvector-enabled Postgres service used by the optional
  Postgres/pgvector storage and retrieval smoke path
- `shim_secondary`: an optional second shim service in the Compose
  `multi-instance` profile, exposed at `http://127.0.0.1:18082`, used to prove
  Postgres shared-state behavior across shim instances

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

Capture the Responses external compatibility tester preflight artifacts:

```bash
make responses-compat-external-smoke
```

Run a strict external Responses compatibility tester command against the
deterministic devstack fixture:

```bash
RESPONSES_COMPAT_REQUIRE_TESTER=1 \
RESPONSES_COMPAT_TESTER_CMD='<external tester command>' \
make responses-compat-external-smoke
```

Run a strict external Responses compatibility tester command against a shim
that is already connected to a real upstream backend:

```bash
RESPONSES_COMPAT_EXPECTED_UPSTREAM=http://127.0.0.1:8000 \
RESPONSES_COMPAT_REQUIRE_TESTER=1 \
RESPONSES_COMPAT_TESTER_CMD='<external tester command>' \
make responses-compat-external-real-smoke
```

For the copy-paste `openai-compatible-tester` command, including the
`TESTER_DIR` and artifact layout conventions, see
[Responses Compatibility External Tester](../engineering/responses-compatibility-external-tester.md#running-openai-compatible-tester).

Run the focused V3 native coding-tools smoke path:

```bash
make v3-coding-tools-smoke
```

Run the focused V3 constrained-decoding smoke path:

```bash
make v3-constrained-decoding-smoke
```

Run the focused Postgres/pgvector retrieval smoke path:

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

Run the focused Postgres/pgvector ANN retrieval smoke path:

```bash
make devstack-postgres-pgvector-ann-up
make devstack-postgres-pgvector-ann-smoke
```

Run the focused Postgres/pgvector multi-instance smoke path:

```bash
make devstack-postgres-pgvector-multi-instance-up
make devstack-postgres-pgvector-multi-instance-smoke
```

This starts a secondary shim instance with a separate SQLite sidecar and log
file. The smoke writes files and vector stores through the primary shim and
then reads, searches, and runs local Responses `file_search` through the
secondary shim. It also verifies shared response retrieval/input-items,
conversation read/append, and stored Chat Completion retrieval/messages across
the two instances. Code-interpreter state is still SQLite sidecar owned.
Override primary
sidecar/log paths with `SHIM_SQLITE_PATH` and `SHIM_LOG_FILE_PATH`; override
secondary paths with `SHIM_SECONDARY_SQLITE_PATH` and
`SHIM_SECONDARY_LOG_FILE_PATH`.

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
RETRIEVAL_INDEX_BACKEND=sqlite_fts5 docker compose -f docker-compose.devstack.yml up -d --build
bash ./scripts/devstack-sqlite-fts5-smoke.sh
bash ./scripts/responses-compat-external-smoke.sh
RESPONSES_COMPAT_RUN_MODE=real-upstream RESPONSES_COMPAT_EXPECTED_UPSTREAM=<upstream-base-url> bash ./scripts/responses-compat-external-smoke.sh
STORAGE_BACKEND=postgres RETRIEVAL_INDEX_BACKEND=pgvector RETRIEVAL_EMBEDDER_BACKEND=openai_compatible RETRIEVAL_EMBEDDER_BASE_URL=http://fixture:8081 RETRIEVAL_EMBEDDER_MODEL=devstack-embedding docker compose -f docker-compose.devstack.yml up -d --build
bash ./scripts/devstack-postgres-pgvector-smoke.sh
STORAGE_BACKEND=postgres RETRIEVAL_INDEX_BACKEND=pgvector RETRIEVAL_EMBEDDER_BACKEND=openai_compatible RETRIEVAL_EMBEDDER_BASE_URL=http://fixture:8081 RETRIEVAL_EMBEDDER_MODEL=devstack-embedding RETRIEVAL_INDEX_PGVECTOR_ANN_ENABLED=true RETRIEVAL_INDEX_PGVECTOR_ANN_DIMENSIONS=4 docker compose -f docker-compose.devstack.yml up -d --build
RETRIEVAL_INDEX_PGVECTOR_ANN_ENABLED=true RETRIEVAL_INDEX_PGVECTOR_ANN_DIMENSIONS=4 bash ./scripts/devstack-postgres-pgvector-smoke.sh
COMPOSE_PROFILES=multi-instance STORAGE_BACKEND=postgres RETRIEVAL_INDEX_BACKEND=pgvector RETRIEVAL_EMBEDDER_BACKEND=openai_compatible RETRIEVAL_EMBEDDER_BASE_URL=http://fixture:8081 RETRIEVAL_EMBEDDER_MODEL=devstack-embedding RESPONSES_MODE=prefer_local docker compose -f docker-compose.devstack.yml up -d --build
bash ./scripts/devstack-postgres-pgvector-multi-instance-smoke.sh
bash ./scripts/v3-coding-tools-smoke.sh
bash ./scripts/v3-constrained-decoding-smoke.sh
bash ./scripts/codex-cli-devstack-smoke.sh
bash ./scripts/codex-cli-shell-tool-smoke.sh
bash ./scripts/codex-cli-coding-task-smoke.sh
bash ./scripts/codex-cli-task-matrix-smoke.sh
docker compose -f docker-compose.devstack.yml down --remove-orphans
```

## Ports

- shim: `http://127.0.0.1:18080`
- secondary shim in the `multi-instance` profile: `http://127.0.0.1:18082`
- fixture backend: `http://127.0.0.1:18081`
- postgres: `127.0.0.1:15432`

The shim itself talks to the fixture backend over the Compose network as
`http://fixture:8081`. In Postgres/pgvector mode it talks to Postgres over the
Compose network as `postgres:5432`.

## Operator Metrics

`/metrics` exposes Prometheus text when metrics are enabled. The devstack path
uses it for low-cardinality operational checks, not for OpenAI API parity
claims.

Useful readiness metrics:

- `shim_readiness_probe_total{source,component,outcome}`
- `shim_readiness_probe_duration_ms{source,component,outcome}`

`source` is `readyz` or `capabilities`. `component` is a fixed runtime
component such as `storage`, `llama`, `retrieval_embedder`,
`web_search_backend`, or `image_generation_backend`. `outcome` is `ready` or
`unready`.

## What The Smoke Path Verifies

`make devstack-ci-smoke` is the repo-owned CI-compatible smoke gate. It runs:

- `make devstack-smoke`
- `make responses-websocket-smoke`
- `make v3-coding-tools-smoke`
- `make v3-constrained-decoding-smoke`

It intentionally does not require the real `codex` binary.

`make responses-compat-external-smoke` is the repo-owned bridge for external
Responses API compatibility testers in `devstack-fixture` mode. With no tester
command configured, it captures `/readyz`, `/debug/capabilities`, and a Broad
subset profile summary under `.data/responses-compat-external`. With
`RESPONSES_COMPAT_TESTER_CMD`, it exports `OPENAI_BASE_URL`,
`OPENAI_API_KEY`, `SHIM_CAPABILITIES_FILE`,
`RESPONSES_COMPAT_PROFILE=responses-broad-subset`, and artifact paths to the
external tester command. Set `RESPONSES_COMPAT_REQUIRE_TESTER=1` when CI should
fail if the command is missing. The profile and gap ledger are documented in
[Responses Compatibility External Tester](../engineering/responses-compatibility-external-tester.md).

`make responses-compat-external-real-smoke` runs the same harness with
`RESPONSES_COMPAT_RUN_MODE=real-upstream`. Use it only when the shim is already
running against the intended upstream backend. Set
`RESPONSES_COMPAT_EXPECTED_UPSTREAM` so the artifact ledger records that
operator assertion. The harness cannot read `llama.base_url` from public shim
probes, so the assertion is explicit rather than inferred. This mode waits for
`/healthz` and captures `/readyz` without requiring it to be 2xx by default;
set `RESPONSES_COMPAT_REQUIRE_READYZ=1` if the real-upstream run must be
blocked on the shim backend readiness probe. For auth-required upstream
`/v1/models` checks, configure `llama.readiness_bearer_token` or
`LLAMA_READINESS_BEARER_TOKEN`. The harness also leaves `OPENAI_API_KEY` unset
by default so external testers can load their own `.env` credentials.

`make devstack-full-smoke` is the local heavy smoke gate. It runs the
CI-compatible gate plus real Codex CLI checks:

- `make devstack-smoke`
- `make responses-websocket-smoke`
- `make v3-coding-tools-smoke`
- `make v3-constrained-decoding-smoke`
- `make codex-cli-devstack-smoke`
- `make codex-cli-shell-tool-smoke`
- `make codex-cli-task-matrix-smoke`

`scripts/devstack-smoke.sh` checks the following in one narrow run:

- fixture `GET /healthz`
- shim `GET /readyz`
- shim `GET /debug/capabilities`
- stored Chat Completions create/list/get/messages local-first surface
- stateful `POST /v1/responses` with `previous_response_id`
- model-assisted `POST /v1/responses/compact` through the deterministic
  fixture backend, including canonical next-window reuse
- server-side `context_management` compaction over a stored state chain
- local `file_search` over shim-owned `files` and `vector_stores`
- local `web_search` over the deterministic fixture backend
- local `image_generation` through the deterministic fixture backend
- local remote `mcp` via `server_url`
- cached remote `mcp` follow-up without repeating tools
- streamed generic replay for remote `mcp`
- hosted/server `tool_search` with namespace loading
- stored `tool_search` follow-up through `function_call_output`
- streamed generic replay for `tool_search`
- `/debug/capabilities` exposes the active compaction backend and retained
  window limits

`scripts/devstack-sqlite-fts5-smoke.sh` is the focused retrieval-index smoke.
Run the stack with `RETRIEVAL_INDEX_BACKEND=sqlite_fts5` first, then run
`make devstack-sqlite-fts5-smoke`. It checks that `/debug/capabilities`
reports SQLite storage plus the `sqlite_fts5` index backend, uploads text
files, creates a vector store, verifies direct vector-store search, and then
verifies local Responses `file_search` over the same store.

`scripts/devstack-postgres-pgvector-smoke.sh` is the focused Postgres/pgvector
storage and retrieval smoke. Run the stack with `STORAGE_BACKEND=postgres`,
`RETRIEVAL_INDEX_BACKEND=pgvector`, and the fixture OpenAI-compatible embedder
environment first, then run `make devstack-postgres-pgvector-smoke`. It checks
that `/debug/capabilities` reports Postgres for responses, conversations,
stored Chat Completions, files, and vector stores, `sqlite_sidecar` for
code-interpreter state, pgvector semantic/hybrid retrieval, direct vector-store
search, local Responses `file_search`, cleanup, optimize/vacuum, and logical
Postgres backup generation.

`make devstack-postgres-pgvector-ann-up` starts the same stack with explicit
pgvector HNSW ANN indexing enabled for the devstack fixture's 4-dimensional
embeddings. `make devstack-postgres-pgvector-ann-smoke` reuses the same smoke
script and additionally verifies the `ann_index` capability manifest block.

`make postgres-storage-test` is the focused package-level Postgres beta test.
It uses `POSTGRES_TEST_DSN`, defaulting to the devstack Postgres port, creates
an isolated schema per test, and checks response/conversation/stored-chat
persistence, replay artifacts, multi-instance shared reads/appends,
file/vector-store persistence, pagination, SQLite sidecar mirroring, binary
attachment failure, lexical retrieval, pgvector semantic/hybrid retrieval, and
Postgres logical backup/restore round-trip coverage. It also verifies that
code-interpreter session/container-file membership remains SQLite-sidecar
local in Postgres mode and is not copied by SQLite-to-Postgres migration.
Run it after `make
devstack-up`; it complements the HTTP smoke above instead of replacing it.

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

`scripts/v3-constrained-decoding-smoke.sh` checks the focused V3 constrained
runtime subset:

- `/debug/capabilities` reports `shim_validate_repair`,
  `capability_class=none`, and `native_available=false`
- non-stream direct grammar custom tool generation returns a validated
  `custom_tool_call`
- create-stream emits typed `response.custom_tool_call_input.*` events for the
  same validated path

`scripts/v3-vllm-constrained-smoke.sh` checks the optional vLLM native
constrained runtime slice outside the default devstack CI path:

- direct vLLM `/v1/chat/completions` enforces `structured_outputs.regex`
- direct vLLM `/v1/chat/completions` enforces `structured_outputs.grammar`
- when `SHIM_BASE_URL` is set, `/debug/capabilities` reports `grammar_native`
  for the configured vLLM backend
- when `SHIM_BASE_URL` is set, `/v1/responses` returns a regex
  `custom_tool_call` generated through the vLLM adapter
- when `SHIM_BASE_URL` is set, `/v1/responses` returns a Lark-subset grammar
  `custom_tool_call` generated through the vLLM adapter

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
- `plan_doc`: writes `PLAN.md` with required planning markers and receives
  `PLANNED`
- `multi_file`: updates two files under one scratch workspace and receives
  `MULTIFILE`

The goal is not to benchmark model quality. The goal is to prove that the
stack is runnable, probeable, and reproducible.

`make codex-eval-smoke` runs the manifest-backed V3 Codex eval harness against
the same devstack fixture path. It is heavier than the shell smoke scripts but
keeps local scratch artifacts under `.tmp/codex-eval-runs/<run-id>/`, including
Codex JSONL, workspace snapshots, diffs, checker results, and a machine-readable
summary. Use it when a failure needs to become an automated regression task:

```bash
make codex-eval-smoke
```

`make codex-eval-loop` adds real-upstream orchestration on top of that runner:
it runs the devstack control first, runs each model from `CODEX_EVAL_MODELS`,
and writes matrix, compare, JSON summary, and failure-bundle artifacts under
`.tmp/codex-eval-loops/<loop-id>/`. Single-model `codex-real-upstream` loops
default to `<model>_baseline_<timestamp>` so promoted baseline candidates are
recognizable before `.tmp` is cleaned.

`make codex-eval-bench-lite` runs the longer repo-owned benchmark-lite profile
against the configured local target. `make codex-eval-loop-bench-lite` runs the
same `codex-bench-lite` suite against both the devstack control and the real
candidate model, which is the preferred stability check before promoting a
bench-lite result into the model matrix.

## Files

- [config.devstack.yaml](../../config.devstack.yaml): shim config used by the stack
- [docker-compose.devstack.yml](../../docker-compose.devstack.yml): Compose wiring
- [scripts/devstack-smoke.sh](../../scripts/devstack-smoke.sh): repo-owned smoke path
- [scripts/responses-compat-external-smoke.sh](../../scripts/responses-compat-external-smoke.sh):
  repo-owned bridge for external Responses compatibility tester runs
- [scripts/v3-coding-tools-smoke.sh](../../scripts/v3-coding-tools-smoke.sh):
  focused native coding-tools smoke path
- [scripts/v3-constrained-decoding-smoke.sh](../../scripts/v3-constrained-decoding-smoke.sh):
  focused constrained-runtime smoke path
- [scripts/codex-cli-devstack-smoke.sh](../../scripts/codex-cli-devstack-smoke.sh):
  real Codex CLI smoke path
- [scripts/codex-cli-shell-tool-smoke.sh](../../scripts/codex-cli-shell-tool-smoke.sh):
  real Codex CLI fallback-shell smoke path
- [scripts/codex-cli-coding-task-smoke.sh](../../scripts/codex-cli-coding-task-smoke.sh):
  real Codex CLI coding-task smoke path
- [scripts/codex-cli-task-matrix-smoke.sh](../../scripts/codex-cli-task-matrix-smoke.sh):
  real Codex CLI task matrix smoke path
- [scripts/codex-eval-runner.sh](../../scripts/codex-eval-runner.sh):
  manifest-backed Codex eval runner wrapper
- [scripts/codex-eval-loop.sh](../../scripts/codex-eval-loop.sh):
  control-vs-real Codex eval orchestration wrapper
- [cmd/codex-eval-runner/main.go](../../cmd/codex-eval-runner/main.go):
  Codex eval runner, matrix, compare, and failure-bundle CLI
- [internal/codexeval/testdata/tasks](../../internal/codexeval/testdata/tasks):
  committed Codex eval task manifests and fixture workspaces
- [cmd/devstack-fixture/main.go](../../cmd/devstack-fixture/main.go): deterministic fixture service
- [internal/devstackfixture/mcp.go](../../internal/devstackfixture/mcp.go): deterministic MCP fixture transport

## Notes

- The default dev stack uses lexical retrieval, not `sqlite_vec`, to avoid
  extra local embedder requirements. The Postgres/pgvector smoke opts into the
  deterministic fixture embedder explicitly.
- The fixture backend is deterministic by design. If the smoke path fails, the
  failure should usually be actionable as a shim or environment issue rather
  than a model-quality fluctuation.
- The remote MCP target for local smoke is `http://127.0.0.1:18081/mcp` on the
  host and `http://fixture:8081/mcp` inside Compose.
- The hosted/server `tool_search` smoke path uses a namespace-based deferred
  tool example, matching the current OpenAI docs guidance to prefer
  namespaces where practical.
