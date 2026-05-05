# V3 Hard Delete And Governance Boundary

Last updated: May 5, 2026.

This note defines the boundary between OpenAI-shaped resource delete routes and
future shim-owned governance purge features.

The short version: current `DELETE` routes delete the addressed resource in
the shim-owned local store. They are not tenant-wide erase workflows, legal-hold
controls, audit exports, upstream-provider deletion, or compliance evidence.

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

## Not Implemented As Of This Slice

The following are intentionally not part of V3 storage beta:

- tenant-wide or project-wide purge
- user/subject erase workflows
- redaction policy hooks for logs, debug captures, traces, or eval artifacts
- legal hold, audit ledger, or approval workflow
- encryption-at-rest policy controls
- cross-backup or PITR deletion guarantees
- upstream-provider delete propagation guarantees
- multi-instance code-interpreter sidecar cleanup beyond the addressed local
  runtime resource

Those are shim-owned governance features. They should not be described as
OpenAI API parity and should not be exposed through OpenAI-compatible resource
routes.

## Implementation Requirements Before Adding Governance Purge

Any future governance purge implementation needs a dedicated operator/admin
surface, not hidden behavior inside public OpenAI-shaped endpoints.

Minimum bar:

- explicit authz model and operator role requirements
- dry-run mode that reports affected resource counts without reading large BLOB
  content
- idempotent execution with bounded batches and resumable progress
- clear treatment of SQLite sidecars when Postgres is the primary store
- audit output that records what the shim attempted, what succeeded, and what
  was intentionally outside scope
- config docs for retention, redaction, and backup interaction
- tests for SQLite and Postgres sibling paths: responses, replay artifacts,
  conversations/items, stored Chat Completions, files, vector stores,
  vector-store files/chunks, and code-interpreter sidecar state
- `go test ./...`, `make lint`, and `git diff --check`

Until that exists, V3 storage should keep resource deletes narrow and the
compatibility matrix should call governance purge a deferred shim-owned
operator surface.
