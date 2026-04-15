# Practical Guides

These guides explain how to use `llama_shim` in practice.

They are intentionally shorter than the official OpenAI docs. The goal is to
answer three questions quickly:

- what this surface is for
- when to use it
- what the shim-specific boundary looks like

Assumptions:

- the shim is reachable at `http://127.0.0.1:8080`
- you already have a working upstream text backend
- examples use `<model>` as a placeholder for whatever model name your backend
  accepts

## Start Here

- [Responses](responses.md): primary API for new work
- [Conversations](conversations.md): durable conversation state
- [Chat Completions](chat-completions.md): legacy-compatible surface
- [Retrieval and File Search](retrieval.md): files, vector stores, and RAG

## Tool Guides

- [Tools Overview](tools.md): how tool routing works in the shim
- [Web Search](web-search.md): current-information lookups
- [Image Generation](image-generation.md): image creation and editing
- [Computer Use](computer.md): screenshot-first UI loop
- [Code Interpreter](code-interpreter.md): local Python execution

## Operations

- [Operations](operations.md): running, probing, backing up, and maintaining the shim

## Related Reference Docs

- [V2 Scope](../v2-scope.md)
- [Compatibility Matrix](../compatibility-matrix.md)
- [V3 Scope](../v3-scope.md)
- [V4 Extensions and Plugin Model](../v4-scope.md)
- [OpenAPI spec](../../openapi/openapi.yaml)
