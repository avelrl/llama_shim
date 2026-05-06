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
make devstack-ci-smoke
make devstack-down
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
