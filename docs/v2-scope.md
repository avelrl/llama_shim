# V2 Scope

Last updated: April 15, 2026.

`llama_shim` V2 is framed as a broad compatibility facade over the current
official OpenAI surface already exposed by the shim. This file is the frozen V2
release ledger and scope definition.

For the live per-surface contract, use
[compatibility-matrix.md](compatibility-matrix.md).

For post-V2 work, use [v3-scope.md](v3-scope.md), [v4-scope.md](v4-scope.md),
and [v5-scope.md](v5-scope.md).

## Official References Reviewed At Freeze

The freeze wording was re-checked on April 15, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI Docs MCP on `developers.openai.com` / `platform.openai.com`
- official OpenAI docs pages:
  - [Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
  - [Using tools](https://developers.openai.com/api/docs/guides/tools)
  - [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
  - [Compaction](https://developers.openai.com/api/docs/guides/compaction)
  - [Counting tokens](https://developers.openai.com/api/docs/guides/token-counting)
  - [Data controls in the OpenAI platform](https://developers.openai.com/api/docs/guides/your-data)

## Release Status

- The V2 blocker list is empty as of April 15, 2026.
- `go test ./...` is green at the current freeze point.
- OpenAPI, config examples, README, and the compatibility matrix are aligned to
  the implemented V2 scope closely enough for a broad compatibility release.

## What V2 Ships

- A local-first `/v1/responses` surface with create, retrieve, delete, cancel,
  input-items, create-stream, and retrieve-stream support.
- A local-first `/v1/conversations` surface with create, retrieve, item list,
  append, item retrieve, and delete flows.
- Stored Chat Completions compatibility as a local-first shadow-store subset
  plus an optional upstream bridge where official stored-chat routes exist.
- Local files, vector stores, and retrieval substrate behind OpenAI-shaped
  routes, including lexical, semantic, hybrid, and local rerank subsets.
- Pragmatic built-in tool support for `file_search`, `web_search`,
  `image_generation`, `computer`, `code_interpreter`, remote `mcp`, and
  `tool_search`, with explicit `prefer_local`, `prefer_upstream`, and
  `local_only` behavior.
- A shim-owned operator floor: readiness, metrics, ingress auth, rate
  limiting, quotas, structured logs, SQLite maintenance cleanup, `shimctl`,
  and minimal local packaging via `Makefile`, `Dockerfile`, and
  `docker-compose.yml`.

## Conscious V2 Boundaries

- V2 does not claim exact hosted choreography for every native or hosted tool
  family.
- Tool-specific SSE beyond the current docs-backed and fixture-backed families
  is not part of the V2 promise.
- Retrieval ranking is docs-aligned and usable, but not claimed to be exact
  hosted reranker parity.
- Constrained custom tools are a useful supported subset, not a claim of true
  backend-native constrained decoding parity.
- Retention cleanup is intentionally conservative and currently sweeps only
  local resources with explicit `expires_at`; it does not invent new TTL rules
  for stored Responses or Conversations.
- `prefer_upstream` remains a proxy-first escape hatch, not a promise that the
  upstream backend natively understands the full Responses tool surface.

## Documentation Map After Freeze

- Practical usage guides: [guides/README.md](guides/README.md)
- V2 release notes: [release-notes-v2.md](release-notes-v2.md)
- Current surface truth: [compatibility-matrix.md](compatibility-matrix.md)
- V2 release ledger: [v2-scope.md](v2-scope.md)
- V3 preflight substrate: [v3-preflight.md](v3-preflight.md)
- Completed narrow V2 follow-up ledger: [v2-followups.md](v2-followups.md)
- Post-V2 expansion parking lot: [v3-scope.md](v3-scope.md)
- V4 extensions and plugin model: [v4-scope.md](v4-scope.md)
- V5 hosted parity and advanced transports: [v5-scope.md](v5-scope.md)
- Historical implementation detail: Git history before the V2 freeze refactor
