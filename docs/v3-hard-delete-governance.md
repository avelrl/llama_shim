# V3 Hard Delete And Governance Boundary

Last updated: May 6, 2026.

This note defines the boundary between OpenAI-shaped resource delete routes and
shim-owned governance purge features.

The short version: current `DELETE` routes delete the addressed resource in
the shim-owned local store. They are not tenant-wide erase workflows,
legal-hold controls, audit exports, upstream-provider deletion, or compliance
evidence. The V3 governance slice adds an explicit operator CLI for purging
shim-local state; it does not widen any OpenAI-compatible route.

## Official Docs Checked

- local official-docs index: [openapi/llms.txt](../openapi/llms.txt)
- OpenAI docs MCP endpoint list for `/responses/{response_id}`,
  `/conversations/{conversation_id}`, `/conversations/{conversation_id}/items`,
  `/files/{file_id}`, `/vector_stores/{vector_store_id}`, and
  `/vector_stores/{vector_store_id}/files/{file_id}`
- OpenAI API reference:
  - <https://developers.openai.com/api/reference/resources/responses/methods/delete>
  - <https://developers.openai.com/api/reference/resources/conversations/methods/delete>
  - <https://developers.openai.com/api/reference/resources/conversations/subresources/items/methods/delete>
  - <https://developers.openai.com/api/reference/resources/files/methods/delete>
  - <https://developers.openai.com/api/reference/resources/vector_stores/methods/delete>
  - <https://developers.openai.com/api/reference/resources/vector_stores/subresources/files>
- OpenAI Data controls guide:
  <https://developers.openai.com/api/docs/guides/your-data>

## Current Resource-Scoped Deletes

These routes are OpenAI-shaped compatibility routes. Their response shape should
track the public API reference, but their storage side effects are local shim
behavior unless a request is explicitly proxied upstream.

| Route | Current shim-local behavior | Governance boundary |
| --- | --- | --- |
| `DELETE /v1/responses/{response_id}` | Deletes a stored response. Response replay artifacts are removed through the store schema/invariant for that response. | Does not erase unrelated conversation items, upstream provider state, debug logs, traces, or backups. |
| `DELETE /v1/conversations/{conversation_id}/items/{item_id}` | Deletes one item and returns the updated conversation. | Does not implement whole-conversation purge. |
| `DELETE /v1/chat/completions/{completion_id}` | Deletes a shim shadow-stored chat completion and message snapshots. | Does not guarantee upstream stored-chat deletion when the upstream surface is absent or best-effort forwarding fails. |
| `DELETE /v1/files/{file_id}` | Deletes the stored file and removes its vector-store attachments/chunks from the local retrieval substrate. | Does not erase copies already exported, logged, backed up, or sent to an upstream or third-party service. |
| `DELETE /v1/vector_stores/{vector_store_id}` | Deletes the vector store and owned vector-store file/chunk rows. File objects remain separate resources. | Does not delete file objects unless the file delete endpoint is called. |
| `DELETE /v1/vector_stores/{vector_store_id}/files/{file_id}` | Removes a file from one vector store and removes that store's chunks for the file. | Does not delete the underlying file object. |
| `DELETE /v1/containers/{container_id}` and container-file deletes | Delete local container or container-file state where the local runtime owns it. | Does not claim hosted container lifecycle parity or cross-instance sidecar purge. |

## Operator Purge Surface

The implemented V3 purge surface is `shimctl governance purge`. It is an
operator/admin command, not an HTTP compatibility endpoint.

Dry-run is the default:

```bash
go run ./cmd/shimctl -config config.yaml governance purge -all \
  -audit-out .data/governance-purge-dry-run.json
```

Apply requires an explicit confirmation token:

```bash
go run ./cmd/shimctl -config config.yaml governance purge -all \
  -apply \
  -confirm purge-all-local-state \
  -batch-size 500 \
  -audit-out .data/governance-purge-apply.json
```

Current scope is `all_local_state` only. The command:

- reports affected row counts without reading stored file/blob content
- deletes in bounded table batches when `-apply` is used
- covers SQLite-owned local state
- covers Postgres-owned beta tables when `storage.backend=postgres`
- also purges the configured SQLite sidecar in Postgres mode, because file
  mirrors and code-interpreter runtime state are sidecar-owned there
- emits a JSON audit report to stdout and optionally to `-audit-out`

The audit report is evidence of shim-local action only. It does not prove that
external providers, backups, debug logs, eval artifacts, or operator-created
exports were deleted.

## Use Scenarios

Use `shimctl governance purge` only when the operator explicitly wants to erase
the configured shim-local durable state. It is intentionally heavier than the
OpenAI-shaped resource delete routes.

### Pre-commit or local-development reset

Use a dry-run first to inspect what will be removed:

```bash
go run ./cmd/shimctl -config config.yaml governance purge -all \
  -audit-out .data/governance-purge-dev-dry-run.json
```

If the counts match the intended local reset, apply with an audit artifact:

```bash
go run ./cmd/shimctl -config config.yaml governance purge -all \
  -apply \
  -confirm purge-all-local-state \
  -audit-out .data/governance-purge-dev-apply.json
```

### Postgres beta environment reset

When `storage.backend=postgres`, the command purges the Postgres-owned beta
tables and the configured SQLite sidecar. This is the right reset path for a
single configured shim deployment because code-interpreter runtime state and
file mirrors can live in the sidecar.

This is not a cluster-wide governance workflow. Other shim instances with
different sidecar paths must be handled by their own operator process.

### Incident triage or support cleanup

Use dry-run output as a count-level inventory before deleting anything. The
report deliberately avoids reading stored file/blob content, so it can be used
without amplifying large local data.

Do not treat the audit report as evidence that logs, request/response capture
files, eval artifacts, backups, PITR archives, exported files, or upstream
provider state were deleted. Those remain separate operator responsibilities.

### Not A Replacement For Resource Deletes

For normal client behavior, keep using the resource-scoped delete route for the
specific object:

- `DELETE /v1/responses/{response_id}`
- `DELETE /v1/files/{file_id}`
- `DELETE /v1/vector_stores/{vector_store_id}`
- `DELETE /v1/conversations/{conversation_id}/items/{item_id}`

Use the governance purge only for an operator-owned full local-state reset.

## Still Out Of Scope

The following remain intentionally outside the V3 storage beta:

- tenant-wide or project-wide purge
- user/subject erase workflows
- redaction policy hooks for logs, debug captures, traces, or eval artifacts
- legal hold, audit ledger, or approval workflow
- encryption-at-rest policy controls
- cross-backup or PITR deletion guarantees
- upstream-provider delete propagation guarantees
- multi-instance code-interpreter state coordination beyond the configured
  sidecar opened by the operator command

Those are shim-owned governance features. They should not be described as
OpenAI API parity and should not be exposed through OpenAI-compatible resource
routes.

## Future Governance Expansion

Future governance expansion still needs a dedicated operator/admin surface,
not hidden behavior inside public OpenAI-shaped endpoints.

Minimum bar:

- explicit authz model and operator role requirements
- dry-run mode that reports affected resource counts without reading large
  BLOB content
- idempotent execution with bounded batches and resumable progress
- clear treatment of SQLite sidecars when Postgres is the primary store
- audit output that records what the shim attempted, what succeeded, and what
  was intentionally outside scope
- config docs for retention, redaction, and backup interaction
- tests for SQLite and Postgres sibling paths: responses, replay artifacts,
  conversations/items, stored Chat Completions, files, vector stores,
  vector-store files/chunks, and code-interpreter sidecar state
- `go test ./...`, `make lint`, and `git diff --check`

The current `shimctl governance purge` slice satisfies the dry-run, bounded
batch, sidecar, audit, SQLite, and Postgres storage-test requirements for the
single `all_local_state` scope. Authz, approval workflow, tenant/project
selection, redaction, legal hold, backup/PITR coordination, and upstream delete
propagation remain future governance work.
