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
  routing classes, runtime config, and dependency probe state
- `/metrics`: Prometheus-style metrics endpoint when enabled

Important distinction:

- `/readyz` is a terse public probe and returns `503` when a required
  dependency is unavailable
- if the upstream `/v1/models` readiness check requires auth, configure
  `llama.readiness_bearer_token` or `LLAMA_READINESS_BEARER_TOKEN`; this token
  is used only for the `/readyz` upstream probe
- `/debug/capabilities` remains a normal shim route, so it shares shim ingress
  auth and request rate limiting, and reports degraded dependencies inside
  `ready` and `probes.*` instead of failing the route itself
- `shimctl probe` is separate from `/readyz`: it is recommendation-only,
  runs on demand from the shared `config.yaml`, can use the shared `.env` for
  `SHIMCTL_PROBE_*` overrides, prints per-request progress to `stderr`
  including the full successful assistant content for each probe, and reports
  operator-facing latency observations as JSON without changing the running
  HTTP server

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
| Check live upstream provider routing | `make upstream-provider-routing-smoke` | No | Verifies one public `provider/model` alias through capabilities, live `/v1/models`, routed Responses, routed Chat Completions, derived endpoint probes, and fail-closed routing boundaries. Writes artifacts under `.tmp/upstream-provider-routing-smoke`. |
| Curate Codex eval artifacts | `make codex-eval-curate` | No | Reads `.tmp/codex-eval-*` artifacts and writes a cross-run interpretation report under `.tmp/codex-eval-curation`. |
| Preview disposable run-artifact cleanup | `make clean-artifacts-dry-run` | No | Lists allowlisted `.tmp` artifact directories that would be removed. |
| Remove disposable run artifacts | `make clean-artifacts` | No | Removes Codex eval runs, Codex smoke workdirs, browser harness runs, governance smoke runs, and Playwright daemon sockets/sessions under `.tmp`. |
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
