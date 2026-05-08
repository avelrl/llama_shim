# Operations

## What It Is

This guide covers the shim-owned operational surface:

- `/healthz`
- `/readyz`
- `/debug/capabilities`
- `/metrics`
- `shimctl`
- Docker and Compose packaging
- SQLite maintenance cleanup

## Minimal Daily Commands

Run locally:

```bash
make run
```

Run tests:

```bash
make test
```

Run the local fast gate:

```bash
make ci-check
```

Build binaries:

```bash
make build
```

Run in Docker:

```bash
docker compose up --build
```

Run the deterministic devstack CI-compatible smoke gate:

```bash
make devstack-up
make devstack-doctor
make devstack-ci-smoke
make devstack-down
```

Run the nondestructive local preflight gate:

```bash
make preflight-local
```

Run the full local smoke gate when the Codex CLI is installed:

```bash
make devstack-up
make devstack-full-smoke
make devstack-down
```

## Probes

- `/healthz`: process liveness
- `/readyz`: readiness of SQLite, upstream text backend, and any configured
  local retrieval or tool backends
- `/debug/capabilities`: shim-owned capability manifest for operators, testers,
  and autonomous agents; always returns a JSON manifest with current surfaces,
  routing classes, runtime config, backend/plugin registries, and dependency
  probe state
- `/debug/traces` and `/debug/traces/{request_id}`: shim-owned bounded
  in-memory request traces for the latest requests; these are metadata-only
  and intentionally omit prompts, bearer tokens, private provider headers,
  tool outputs, and file contents
- `/metrics`: Prometheus-style metrics endpoint when enabled

Important distinction:

- `/readyz` is a terse public probe and returns `503` when a required
  dependency is unavailable
- if the upstream `/v1/models` readiness check requires auth, configure
  `llama.readiness_bearer_token` or `LLAMA_READINESS_BEARER_TOKEN`; this token
  is used only for the `/readyz` upstream probe
- `/debug/capabilities` remains a normal shim route, so it shares shim ingress
  auth and request rate limiting, and reports degraded dependencies inside
  `ready` and `probes.*` instead of failing the route itself. Its
  `plugins.plugins[]` entries are metadata-only plugin contracts cross-linked
  to `backends.components[]`: they expose ids, versions, config namespaces,
  required env secret names, surfaces, backend projections, timeout labels,
  named backend request cleanup hooks, and error classes, but never secret
  values.
- `/debug/traces` remains a normal shim route as well. Configure
  `shim.debug_traces.enabled` / `SHIM_DEBUG_TRACES_ENABLED` and
  `shim.debug_traces.max_entries` / `SHIM_DEBUG_TRACES_MAX_ENTRIES`.
  Use `X-Request-Id` from a client response to retrieve one trace:
  `curl "$SHIM_BASE_URL/debug/traces/$REQUEST_ID"`.
  Request cleanup appears only as metadata-only `transforms[]` entries with
  `stage=request_cleanup`; traces do not include prompts, bearer tokens, or
  provider header values.
- `shimctl probe` is separate from `/readyz`: it is recommendation-only,
  runs on demand from the shared `config.yaml`, can use the shared `.env` for
  `SHIMCTL_PROBE_*` overrides, prints per-request progress to `stderr`
  including the full successful assistant content for each probe, and reports
  operator-facing latency observations as JSON without changing the running
  HTTP server

## V4 Preflight Runbook

Use V4 preflight as the operator gate for the shim-owned V4 substrate: backend
contracts, plugin descriptors, request cleanup metadata, debug traces, provider
routing, and optional Codex config probing. It is intentionally broader than
`/readyz`, but narrower than a full model-quality benchmark.

Run it after changes to provider routing, backend/plugin registration, debug
trace capture, request cleanup hooks, Codex config generation, or preflight
scripts:

```bash
make v4-preflight-smoke
```

Run the same gate for a live routed provider/model alias:

```bash
V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> make v4-preflight-smoke
```

If the shim itself requires bearer auth:

```bash
SHIM_AUTH_HEADER="Authorization: Bearer $GW_API_KEY" \
  V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> \
  make v4-preflight-smoke
```

Add the isolated Codex config doctor only when validating Codex CLI wiring:

```bash
V4_PREFLIGHT_RUN_CODEX_DOCTOR=1 \
  V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> \
  make v4-preflight-smoke
```

When validating all configured provider/model rows in the V4 operator matrix,
use the aggregate smoke instead of launching one model at a time:

```bash
make v4-provider-matrix-smoke
make v4-provider-matrix-curate
```

Limit the smoke during iteration, or focus the curation report on one model:

```bash
V4_PROVIDER_MATRIX_MODELS="deepseek/deepseek-v4-pro xiaomi/mimo-v2.5-pro" \
  make v4-provider-matrix-smoke
V4_PROVIDER_MATRIX_CURATE_MODEL=deepseek/deepseek-v4-pro \
  make v4-provider-matrix-curate
```

What a passing run means:

- `/healthz`, `/readyz`, and `/v1/models` were reachable
- `/debug/capabilities` advertised valid V4 backend and plugin contract
  schemas
- backend `plugin_id` links matched plugin descriptor ids and versions
- plugin `request_cleanup_hooks` stayed metadata-only named hooks
- one direct Responses request produced a retrievable metadata-only debug trace
- the trace contained V4 tool-classifier metadata for a function tool
- when `V4_PREFLIGHT_PROVIDER_MODEL` was set, the nested provider-routing
  smoke proved the same alias through Responses, Chat Completions, helper
  endpoints, live `/v1/models`, and fail-closed unknown-provider behavior
- when `V4_PREFLIGHT_RUN_CODEX_DOCTOR=1` was set, `shimctl codex doctor`
  verified config generation and direct probe wiring

Read artifacts in this order:

1. `summary.json`: machine-readable status, request ids, nested smoke status,
   warnings, and failures.
2. `summary.md`: human-readable short report.
3. `responses_trace_debug_trace.response.json`: selected backend, projection,
   plugin id, tool-classifier decision, and request-cleanup transform metadata.
4. `provider-routing/<model>/summary.json`: nested routed-provider result when
   `V4_PREFLIGHT_PROVIDER_MODEL` was set.
5. `codex-doctor/summary.json`: Codex config doctor result when enabled.
6. `warnings.txt`, `failures.txt`, and `shim.log.slice`: first stop for
   diagnosis.

Common failure shapes:

- `/healthz` returns `000` or times out: the shim is not running, or
  `SHIM_BASE_URL` points at the wrong process.
- `/readyz` returns `503`: a configured required dependency is unavailable.
  Inspect `/debug/capabilities.probes`. If the upstream `/v1/models`
  readiness probe requires auth, configure `LLAMA_READINESS_BEARER_TOKEN`.
- capability validation fails: backend/plugin registry metadata drifted, a
  plugin cross-link is broken, or debug traces are disabled while
  `V4_PREFLIGHT_REQUIRE_DEBUG_TRACE` still requires them.
- the direct Responses trace fails: the request path, selected backend, tool
  classifier, or trace capture path is broken before model-quality questions
  matter.
- `/debug/traces/{request_id}` returns `404`: debug traces are disabled,
  evicted by the bounded trace store, or the request failed before trace
  metadata was recorded.
- nested provider routing fails: check the `provider/model` alias, provider
  auth env, live `/v1/models`, and provider-specific cleanup hook metadata.
- Codex doctor fails: treat it as Codex config/auth/probe wiring evidence, not
  as evidence that the model cannot pass Codex tasks.

Boundaries:

- V4 preflight does not measure coding quality, instruction following, or
  long-run stability. Use `make codex-eval-auto` and
  `make codex-eval-curate` for that.
- V4 preflight does not turn `/debug/*` into OpenAI-compatible API surface.
  Debug capabilities and traces remain shim-owned operator routes.
- V4 preflight does not delete `.data` and does not clean artifacts. Its output
  under `.tmp/v4-preflight-smoke` is disposable.

Plugin, model, and capability settings map:

| Need | Configure | Inspect | Verify |
| --- | --- | --- | --- |
| Route one public model alias to one upstream provider | `llama.providers[]` plus provider token env names such as `DEEPSEEK_API_KEY` | `/debug/capabilities.backends.components[]`, `/debug/capabilities.plugins.plugins[]`, live `/v1/models` | `V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> make v4-preflight-smoke` or `make upstream-provider-routing-smoke` |
| Make Codex know model capabilities and tool shapes | `responses.codex.model_metadata.models[]` using the same public model id Codex launches with | Codex-specific `/v1/models?client_version=...` or `/api/codex/models`; normal `/v1/models` stays OpenAI-shaped | `make codex-config-doctor`, then `make codex-eval-auto` |
| Adapt request shape for a provider without changing public input | `chat_completions.upstream_compatibility.models[]`, `responses.codex.upstream_input_compatibility.models[]`, and `responses.upstream_tool_compatibility.models[]` | `/debug/traces/{request_id}.transforms[]` and plugin `request_cleanup_hooks` | V4 preflight trace check, provider-routing smoke, and focused Codex evals |
| Enable or disable local tool/runtime backends | `responses.web_search`, `responses.image_generation`, `responses.computer`, `responses.code_interpreter`, `responses.compaction`, `retrieval.*` | backend components and plugin descriptors with enabled/ready state | the matching domain smoke, plus V4 preflight for registry consistency |
| Tune debug trace availability | `shim.debug_traces.*` or `SHIM_DEBUG_TRACES_*` | `/debug/capabilities.runtime.ops.debug_traces` and `/debug/traces` | `make v4-preflight-smoke` |

Use these rules when adding a model or plugin:

- Use the same public model id everywhere a client sees the model, for example
  `deepseek/deepseek-v4-pro`.
- Match provider compatibility rules against the resolved upstream model when
  `llama.providers` is enabled, not the public `provider/model` alias.
- Add new provider token names to `.env.example`, but never commit token
  values.
- Keep plugin descriptors descriptive, not executable: they advertise
  `config_namespace`, `required_secrets`, surfaces, projections, timeouts,
  limits, error classes, and named cleanup hooks.
- Confirm `/debug/capabilities.plugins.issues` has no `error` entries before
  treating the plugin registry as healthy.
- Do not use Codex model metadata to fake ordinary OpenAI `/v1/models` data.
  Codex metadata is a Codex client profile surface only.
- Do not expose provider-specific knobs as public OpenAI request fields. Keep
  them in config and make any backend projection visible through debug traces.

Useful inspection snippets:

```bash
curl -fsS "$SHIM_BASE_URL/debug/capabilities" \
  | jq '.backends.components[] | {id, category, kind, capability_class, enabled, ready, plugin_id}'

curl -fsS "$SHIM_BASE_URL/debug/capabilities" \
  | jq '.plugins.plugins[] | {id, kind, config_namespace, required_secrets, request_cleanup_hooks}'

curl -fsS "$SHIM_BASE_URL/debug/capabilities" \
  | jq '.plugins.issues // []'
```

Recommended operator flow:

```bash
make preflight-local
V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> make v4-preflight-smoke
make codex-eval-auto
make codex-eval-curate
```

## Maintenance

Background cleanup:

- `sqlite.maintenance.cleanup_interval` controls the storage-maintenance
  sweep interval for the active storage backend. In SQLite mode it sweeps
  SQLite resources with explicit `expires_at`; in Postgres mode it sweeps the
  Postgres-owned files and vector stores with explicit `expires_at`.
- `storage.retention.response_replay_artifacts.max_age` and
  `storage.retention.response_replay_artifacts.max_responses` are disabled by
  default. When configured, the same maintenance sweep prunes only shim-local
  replay artifacts for standalone responses; response rows and
  conversation-attached response artifacts are preserved.
- `responses.code_interpreter.cleanup_interval` handles expired local code
  interpreter containers separately
- In Postgres mode, code-interpreter sessions and container-file membership
  remain in the per-instance SQLite sidecar. Back up the sidecar only if you
  need that local ephemeral metadata; active Docker containers are not
  cluster-shared by Postgres backup/restore.

One-shot maintenance for the configured backend:

```bash
go run ./cmd/shimctl -config ./config.yaml cleanup
go run ./cmd/shimctl -config ./config.yaml optimize
go run ./cmd/shimctl -config ./config.yaml vacuum
go run ./cmd/shimctl -config ./config.yaml backup -out ./.data/shim-backup.db
go run ./cmd/shimctl -config ./config.yaml restore -from ./.data/shim-backup.db
```

`shimctl cleanup` uses the same replay-artifact retention settings as the
background sweep and reports both expired-resource deletes and replay-artifact
prunes.

Governance purge is the operator-owned full local-state reset. It is not an
OpenAI-compatible HTTP route and is intentionally explicit:

```bash
go run ./cmd/shimctl -config ./config.yaml governance purge -all

go run ./cmd/shimctl -config ./config.yaml governance purge -all \
  -apply \
  -confirm purge-all-local-state \
  -audit-out ./.data/governance-purge-apply.json
```

Dry-run is the default. Use `-apply -confirm purge-all-local-state` only after
reviewing the dry-run counts. In Postgres mode this purges the current
Postgres-owned beta tables plus the configured SQLite sidecar, because file
mirrors and code-interpreter runtime state remain sidecar-owned there.

Run the isolated SQLite governance smoke before changing this workflow:

```bash
make governance-purge-smoke
```

The smoke creates a temporary database, seeds representative local state, runs
the real `shimctl governance purge` CLI in dry-run and apply modes, verifies the
audit JSON, and verifies that the seeded state is gone after apply.

## Artifact And Data Cleanup

The repository separates disposable run artifacts from durable local data.
Default cleanup targets intentionally do not remove `.data`.

| Goal | Command | Removes `.data`? | Notes |
| --- | --- | --- | --- |
| Inspect local state | `make local-state-report` | No | Read-only size/count report for `.tmp`, `.cache`, `.data`, common smoke artifacts, and devstack Compose status when Docker is available. |
| Diagnose devstack health | `make devstack-doctor` | No | Read-only strict checks for required commands, Compose status, fixture health, shim health, `/readyz`, and `/debug/capabilities`. |
| Diagnose devstack without failing on readiness | `make devstack-doctor-advisory` | No | Same report, but unavailable dependencies are warnings. Useful before starting or after stopping the stack. |
| Run local preflight | `make preflight-local` | No | Runs local state report, strict devstack doctor, cleanup/reset dry-runs, build, lint, and `git diff --check`. |
| Run V4 preflight smoke | `make v4-preflight-smoke` | No | Aggregates health/readiness, `/v1/models`, V4 capabilities/plugin contract validation, one Responses debug-trace/tool-classifier probe, optional provider-routing smoke, and optional Codex config doctor. Writes artifacts under `.tmp/v4-preflight-smoke`. |
| Run V4 provider matrix smoke | `make v4-provider-matrix-smoke` | No | Runs provider-routing smoke and V4 preflight for each configured matrix model, then writes one aggregate JSON/Markdown report under `.tmp/v4-provider-matrix-smoke`. |
| Curate V4 provider matrix artifacts | `make v4-provider-matrix-curate` | No | Reads existing matrix-smoke artifacts, groups routing/preflight/Codex doctor/readiness failures, and writes a local operator verdict under `.tmp/v4-provider-matrix-curation`. |
| Check live upstream provider routing | `make upstream-provider-routing-smoke` | No | Verifies one public `provider/model` alias through capabilities, live `/v1/models`, routed Responses, routed Chat Completions, derived endpoint probes, and fail-closed routing boundaries. Writes artifacts under `.tmp/upstream-provider-routing-smoke`. |
| Diagnose Codex provider config | `make codex-config-doctor` | No | Generates an isolated Codex config, checks the local Codex binary, verifies shim auth env, health/readiness, capabilities, `/v1/models`, and one direct `/v1/responses` smoke. Writes artifacts under `.tmp/shimctl-codex`. |
| Curate Codex eval artifacts | `make codex-eval-curate` | No | Reads `.tmp/codex-eval-*` artifacts and writes a cross-run interpretation report under `.tmp/codex-eval-curation`. |
| Preview disposable run-artifact cleanup | `make clean-artifacts-dry-run` | No | Lists allowlisted `.tmp` artifact directories that would be removed. |
| Remove disposable run artifacts | `make clean-artifacts` | No | Removes Codex eval runs, Codex smoke workdirs, browser harness runs, governance/V4 smoke runs, and Playwright daemon sockets/sessions under `.tmp`. |
| Preview broader local dev cleanup | `make clean-dev-artifacts-dry-run` | No | Adds local tool caches to the artifact preview. |
| Remove broader local dev artifacts | `make clean-dev-artifacts` | No | Removes the allowlisted `.tmp` artifacts plus `.cache`, `.playwright-cli`, `.tmp/go-build`, and `.tmp/go-tmp`. |
| Remove only Codex eval runs | `make codex-eval-clean` | No | Removes `.tmp/codex-eval-runs`, `.tmp/codex-eval-loops`, `.tmp/codex-eval-auto`, and `.tmp/codex-eval-curation`. |
| Preview devstack reset | `make devstack-reset-dry-run` | No | Prints the Compose reset command; keeps Docker volumes. |
| Stop/reset devstack containers | `make devstack-reset` | No | Runs `docker compose -f docker-compose.devstack.yml down --remove-orphans`; keeps Docker volumes. |
| Preview devstack volume reset | `make devstack-reset-volumes-dry-run` | No | Prints the Compose reset command with `-v`. |
| Stop/reset devstack containers and volumes | `make devstack-reset-volumes` | No | Removes Compose-managed devstack volumes only; still does not touch repo `.data`. |
| Reset shim-local durable state | `shimctl governance purge` | Yes, for configured store rows only | Use dry-run first. Does not delete logs, backups, eval artifacts, or external upstream state. |

Safe cleanup target boundaries:

- `make clean-artifacts` and `make clean-dev-artifacts` use an allowlist in
  `scripts/clean-artifacts.sh`
- they do not accept arbitrary paths
- they never delete `.data`, `.env`, `config.yaml`, backups, response
  compatibility artifacts, or `shim.log`
- they are useful before re-running Codex evals, browser harness smokes, or
  governance smoke tests
- `devstack-reset*` targets affect Docker Compose state only; they do not clean
  repo files or `.data`

Treat `.data` as durable local state:

- `.data/shim.db` and Postgres sidecars contain local API state
- `.data/shim.log` can contain operational evidence
- `.data/responses-compat-external` contains external compatibility tester
  artifacts
- backup files under `.data` are operator-owned

To clear local API state, use `shimctl governance purge` instead of deleting
`.data` by habit. To clear logs, external tester artifacts, or backups, make a
separate explicit operator decision after copying anything that must be kept.

Practical reset playbook:

```bash
make local-state-report
make devstack-doctor-advisory
make clean-artifacts-dry-run
make devstack-reset-dry-run

make clean-artifacts
make devstack-reset
make devstack-up
make devstack-ci-smoke
```

If the devstack Postgres volume itself must be reset, preview the destructive
Docker-volume command first:

```bash
make devstack-reset-volumes-dry-run
make devstack-reset-volumes
```

For Postgres mode, configure `storage.backend=postgres` and `postgres.dsn` in
`config.yaml`, or provide matching environment overrides:

```bash
STORAGE_BACKEND=postgres \
POSTGRES_DSN=postgres://llama_shim:llama_shim@127.0.0.1:15432/llama_shim?sslmode=disable \
SQLITE_PATH=./.data/shim-postgres-sidecar.db \
go run ./cmd/shimctl -config ./config.yaml backup -out ./.data/shim-postgres-backup.sql
```

Postgres backup/restore is a shim-owned logical COPY format for the current
Postgres-owned state and retrieval tables. It is useful for local/devstack
operations and regression checks, but it is not a replacement for a
cluster-level `pg_dump`/`pg_restore`, PITR, or managed-database backup policy.
Use [Postgres Backup and Restore](postgres-backup.md) for the cluster-native
runbook and restore checklist.

Move current SQLite state into the Postgres beta store:

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

The migration copies only the Postgres-owned beta tables: responses, response
replay artifacts, conversations/items, stored Chat Completions/messages, files,
vector stores, vector-store files, and vector-store chunks. It leaves
code-interpreter sessions/generated files in SQLite sidecar ownership. Without
`-replace`, writes fail when any target migration table is already populated.
The command prints a JSON report with source counts, target counts, copied
counts, and whether an explicit replace is required.

Equivalent Make wrapper:

```bash
STORAGE_BACKEND=postgres \
POSTGRES_DSN=postgres://llama_shim:llama_shim@127.0.0.1:15432/llama_shim?sslmode=disable \
make maint-migrate-sqlite-to-postgres

STORAGE_BACKEND=postgres \
POSTGRES_DSN=postgres://llama_shim:llama_shim@127.0.0.1:15432/llama_shim?sslmode=disable \
MIGRATE_FLAGS=-replace \
make maint-migrate-sqlite-to-postgres
```

## Auth, Limits, And Logging

The shim also supports:

- optional static bearer auth
- in-memory request rate limiting
- request, upload, retrieval, and local runtime quotas
- structured JSON logs

## Gotchas

- `restore` is intentionally an offline-oriented operation; stop the running
  shim before replacing the SQLite file or truncating/restoring the
  Postgres-owned shim tables.
- `migrate sqlite-to-postgres` is also offline-oriented; stop the running shim
  while copying into Postgres, and review `-dry-run` output before using
  `-replace`.
- Cleanup is intentionally conservative and currently targets only explicit
  local expiry-managed resources.

## Related Docs

- [README](../../README.md)
- [Runtime Hardening](../engineering/runtime-hardening.md)
- [Dev Stack](devstack.md)
- [V2 Scope](../v2-scope.md)
