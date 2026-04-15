# V2 Release Notes

Last updated: April 15, 2026.

`llama_shim` V2 is the first release framed as a broad compatibility facade for
the OpenAI surfaces the project already exposes.

This is not an “exact parity” release. It is a practical, local-first release
with explicit boundaries.

## Highlights

- `Responses` is now the primary surface, with local-first create, retrieve,
  cancel, delete, input-items, create-stream, and retrieve-stream support.
- `Conversations` is now a real durable state surface rather than a thin stub.
- Stored Chat Completions are available as a local-first shadow-store subset
  with optional upstream bridging where official stored-chat routes exist.
- Local files, vector stores, and retrieval are usable end-to-end.
- Built-in tool coverage now includes practical V2 subsets for:
  `file_search`, `web_search`, `image_generation`, `computer`,
  `code_interpreter`, remote `mcp`, and `tool_search`.
- Operator floor is in place: readiness, metrics, auth, rate limiting, quotas,
  maintenance cleanup, `shimctl`, Docker packaging, and Compose support.

## What This Release Optimizes For

- predictable local-first behavior
- clear compatibility boundaries
- practical agentic workflows
- self-hosted operation without pretending the upstream backend already
  understands the full `Responses` contract

## What This Release Does Not Claim

- exact hosted choreography for every built-in tool family
- exact hosted reranker quality or retrieval ranking behavior
- full hosted Code Interpreter, Computer Use, or connector parity
- true backend-native constrained decoding parity for `grammar` and `regex`

## Recommended Reading Order

- [Practical guides](guides/README.md)
- [Compatibility matrix](compatibility-matrix.md)
- [V2 scope](v2-scope.md)
- [V3 scope](v3-scope.md)

## Practical Summary

If you want to use the project today, the recommended path is:

1. Start with [Responses](guides/responses.md).
2. Add [Conversations](guides/conversations.md) if you need durable thread
   state.
3. Add [Retrieval and File Search](guides/retrieval.md) if you need document
   grounding.
4. Enable the tool runtimes you actually need with the corresponding config
   keys.
5. Use [Operations](guides/operations.md) for probes, cleanup, and backups.
