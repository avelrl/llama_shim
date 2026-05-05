# Postgres Backup And Restore

This guide covers the Postgres beta storage backend. It separates two backup
paths that serve different jobs:

- `shimctl backup` / `shimctl restore`: shim-owned logical COPY export for the
  current Postgres-owned beta tables
- Postgres-native backup: `pg_dump` / `pg_restore`, physical backup, PITR, or a
  managed database backup for production-like deployments

This is operational guidance, not an OpenAI API retention or storage parity
claim.

## What Postgres Owns

When `storage.backend=postgres` is active, Postgres owns:

- responses and response replay artifacts
- conversations and conversation items
- stored Chat Completions and stored messages
- files, vector stores, vector-store files, and vector-store chunks
- pgvector embeddings when `retrieval.index.backend=pgvector`

SQLite remains a per-instance sidecar for code-interpreter sessions and
container-file membership. Active Docker containers, process state, `.env`
files, logs, and `.tmp` eval artifacts are not part of Postgres backup.

## Which Backup To Use

| Need | Use | Notes |
| --- | --- | --- |
| Devstack export, regression fixture, small table-level move | `shimctl backup` | Portable logical COPY for the shim-owned beta tables only |
| Production logical backup | `pg_dump` | Captures database-native metadata and extension state better than `shimctl` |
| Disaster recovery / point-in-time restore | PITR or managed database backup | `shimctl` cannot provide WAL-level recovery |
| Major Postgres upgrade or provider migration | Provider tooling or `pg_dump`/`pg_restore` | Test on a restored database before switching traffic |
| Per-instance code-interpreter sidecar metadata | SQLite sidecar backup or instance snapshot | Usually ephemeral; not shared by Postgres |

## Shim-Owned Logical Backup

Use this for local/devstack workflows and focused regression checks:

```bash
STORAGE_BACKEND=postgres \
POSTGRES_DSN='postgres://user:pass@host:5432/dbname?sslmode=disable' \
SQLITE_PATH=./.data/shim-postgres-sidecar.db \
go run ./cmd/shimctl -config ./config.yaml backup \
  -out ./.data/shim-postgres-backup.sql
```

Restore should be treated as offline-oriented. Stop active shim writers, restore
into an empty or disposable database/schema, then start the shim and verify
readiness:

```bash
STORAGE_BACKEND=postgres \
POSTGRES_DSN='postgres://user:pass@host:5432/dbname?sslmode=disable' \
SQLITE_PATH=./.data/shim-postgres-sidecar.db \
go run ./cmd/shimctl -config ./config.yaml restore \
  -from ./.data/shim-postgres-backup.sql
```

The shim-owned format is intentionally narrow. It does not replace a database
cluster backup policy, and it only covers the tables known to the current beta
schema.

## Postgres-Native Logical Backup

For production-style logical backups, prefer a custom-format dump:

```bash
POSTGRES_DSN='postgres://user:pass@host:5432/dbname?sslmode=require'

pg_dump \
  --format=custom \
  --no-owner \
  --no-privileges \
  --file ./.data/postgres-native.dump \
  "$POSTGRES_DSN"
```

If the shim uses a non-default schema through `search_path`, either dump the
whole database or pass the matching `--schema=<schema>` value. The pgvector
extension must be available on the restore target when pgvector retrieval is
enabled.

Restore into a stopped or disposable target first:

```bash
RESTORE_DSN='postgres://user:pass@host:5432/restored_db?sslmode=require'

pg_restore \
  --clean \
  --if-exists \
  --single-transaction \
  --dbname "$RESTORE_DSN" \
  ./.data/postgres-native.dump
```

After restore, start the shim against the restored database and verify:

```bash
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/debug/capabilities
```

For a local devstack restore, also run:

```bash
make postgres-storage-test
make devstack-postgres-pgvector-smoke
```

## Managed Or Physical Backup

For real disaster recovery, use the managed database or cluster-native backup
path your Postgres deployment provides:

- automated snapshots
- WAL archiving and PITR
- cross-region replica or provider-native restore
- scheduled restore drills into a non-production target

Define RPO/RTO outside the shim. The shim can verify readiness after restore,
but it does not schedule, retain, encrypt, or validate cluster backups.

## Sidecar Handling

In Postgres mode, each shim instance still has a SQLite sidecar. Treat it as
instance-local runtime state:

- do not rely on it for shared Postgres durable objects
- do not expect it to move active Docker containers between nodes
- back it up only if you intentionally need local code-interpreter session
  metadata from that specific instance

For most clustered deployments, restore Postgres first and let each shim
instance recreate local sidecar/runtime state as needed.

## Restore Checklist

Before switching traffic to a restored database:

- stop or drain shim writers during restore
- verify the restored database has the `vector` extension if pgvector is used
- start one shim instance and check `/readyz`
- inspect `/debug/capabilities` for `storage.backend=postgres`
- retrieve a known stored response and, if applicable, a known vector-store
  search result
- run the relevant smoke target in staging before production traffic
