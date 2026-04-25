# Tools Overview

## What It Is

The shim supports a practical subset of the OpenAI tool model inside
`/v1/responses`.

This is where the V2 facade becomes useful: the shim can own local state,
preserve typed tool items, and execute a subset of built-in tools locally when
configured to do so.

## Tool Routing Modes

`responses.mode` controls how `/v1/responses` behaves:

- `prefer_local`: default. Use the shim-local subset first, fall back upstream
  for unsupported shapes.
- `prefer_upstream`: proxy-first escape hatch.
- `local_only`: never call upstream.

## Current Practical Tool Surface

| Tool family | Practical status |
| --- | --- |
| `file_search` | local subset |
| `web_search` / `web_search_preview` | local subset when configured |
| `image_generation` | local subset when configured |
| `computer` | local screenshot-first subset when configured |
| `code_interpreter` | local dev-oriented subset when configured |
| native local `shell` | broad local subset in shim-local mode |
| native local `apply_patch` | broad local subset in shim-local mode |
| remote `mcp` with `server_url` | local subset |
| `mcp` with `connector_id` | proxy-only compatibility bridge |
| `tool_search` hosted/server subset | local subset |
| `tool_search` client execution | proxy-only |

For the exact contract, see the [compatibility matrix](../compatibility-matrix.md).

## Minimal Patterns

### Local tool call

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Use retrieval to answer this question.",
    "tools": [
      {"type": "file_search", "vector_store_ids": ["vs_..."]}
    ]
  }'
```

### Remote MCP server

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Roll 2d4+1.",
    "tools": [
      {
        "type": "mcp",
        "server_label": "dmcp",
        "server_url": "https://dmcp-server.deno.dev/sse",
        "require_approval": "never"
      }
    ]
  }'
```

### Hosted/server tool search

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Find the billing tool and use it.",
    "tools": [
      {"type": "tool_search"},
      {
        "type": "namespace",
        "name": "billing_tools",
        "description": "Billing and invoice tools.",
        "tools": [
          {
            "type": "function",
            "name": "get_invoice",
            "description": "Fetch one invoice.",
            "defer_loading": true,
            "parameters": {
              "type": "object",
              "properties": {"invoice_id": {"type": "string"}},
              "required": ["invoice_id"],
              "additionalProperties": false
            }
          }
        ]
      }
    ]
  }'
```

## Shim-Specific Notes

- The shim preserves typed output items and does not flatten everything into a
  generic text-only contract.
- The repo dev stack exposes a deterministic remote MCP target at
  `http://127.0.0.1:18081/mcp` on the host or `http://fixture:8081/mcp`
  inside Compose, which is useful for smoke tests and CI.
- The repo dev stack also smoke-tests hosted/server `tool_search` with a
  namespace-based deferred tool example and a stored `function_call_output`
  follow-up.
- `connector_id` is not a local runtime in V2. It remains a proxy-only bridge.
- Client `tool_search` is also proxy-only in V2; hosted/server `tool_search`
  is the local practical subset.
- The V3 coding-tools lane now has a narrower replay contract:
  - `/debug/capabilities` reports native-local `shell` and `apply_patch`
    support under `.tools.shell` and `.tools.apply_patch`
  - shim-local `shell_call` create-stream emits
    `response.shell_call_command.*`
  - shim-local `apply_patch_call` create-stream and retrieve-stream emit
    `response.apply_patch_call_operation_diff.done`; a
    `response.apply_patch_call_operation_diff.delta` event is expected only
    when the stored `operation.diff` is non-empty
  - shim-local `shell_call` retrieve-stream still stays generic through
    `response.output_item.*` because upstream background shell replay is
    currently blocked

## Gotchas

- `prefer_upstream` should be treated as an escape hatch, not the normal mode.
- If a local runtime is disabled and you use `local_only`, you will get an
  explicit validation error rather than an upstream fallback.

## Related Docs

- [Web Search](web-search.md)
- [Image Generation](image-generation.md)
- [Computer Use](computer.md)
- [Code Interpreter](code-interpreter.md)
- [Official tools guide](https://developers.openai.com/api/docs/guides/tools)
