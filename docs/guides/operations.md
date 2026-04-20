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

Build binaries:

```bash
make build
```

Run in Docker:

```bash
docker compose up --build
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
- `/debug/capabilities` remains a normal shim route, so it shares shim ingress
  auth and request rate limiting, and reports degraded dependencies inside
  `ready` and `probes.*` instead of failing the route itself

## Maintenance

Background cleanup:

- `sqlite.maintenance.cleanup_interval` sweeps local resources with explicit
  `expires_at`
- `responses.code_interpreter.cleanup_interval` handles expired local code
  interpreter containers separately

One-shot maintenance:

```bash
go run ./cmd/shimctl -config ./config.yaml cleanup
go run ./cmd/shimctl -config ./config.yaml optimize
go run ./cmd/shimctl -config ./config.yaml vacuum
go run ./cmd/shimctl -config ./config.yaml backup -out ./.data/shim-backup.db
go run ./cmd/shimctl -config ./config.yaml restore -from ./.data/shim-backup.db
```

## Auth, Limits, And Logging

The shim also supports:

- optional static bearer auth
- in-memory request rate limiting
- request, upload, retrieval, and local runtime quotas
- structured JSON logs

## Gotchas

- `restore` is intentionally an offline-oriented operation; stop the running
  shim before replacing the SQLite file.
- Cleanup is intentionally conservative and currently targets only explicit
  local expiry-managed resources.

## Related Docs

- [README](../../README.md)
- [Runtime Hardening](runtime-hardening.md)
- [Dev Stack](devstack.md)
- [V2 Scope](../v2-scope.md)
