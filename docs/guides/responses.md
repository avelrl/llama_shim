# Responses

## What It Is

`/v1/responses` is the primary API surface in `llama_shim`.

Use it when you want:

- multi-turn state with `previous_response_id`
- durable state through `conversation`
- built-in tools such as `file_search`, `web_search`, `image_generation`,
  `computer`, `code_interpreter`, `mcp`, or `tool_search`
- the shim's local-first compatibility layer instead of a raw upstream chat proxy

## When To Use It

Use `Responses` for new integrations unless you specifically need the legacy
`/v1/chat/completions` shape.

Good fits:

- agentic workflows
- tool calling
- stateful follow-up turns
- retrieval-heavy applications
- any integration that wants one surface for text, tools, and state

## Minimal Request

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Say OK and nothing else."
  }'
```

## Common Patterns

### 1. One-shot response

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "store": true,
    "input": "Summarize what this service does in one sentence."
  }'
```

### 2. Continue with `previous_response_id`

First turn:

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "store": true,
    "input": "Remember that my project code is ALPHA-7."
  }'
```

Follow-up turn:

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "store": true,
    "previous_response_id": "resp_...",
    "input": "What project code did I just give you?"
  }'
```

### 3. Attach to a conversation

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "conversation": "conv_...",
    "input": "Continue the support thread."
  }'
```

### 4. Stream the response

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "stream": true,
    "input": "Stream a short answer."
  }'
```

## Shim-Specific Notes

- `responses.mode=prefer_local` is the default. The shim uses its local subset
  first and falls back upstream only for unsupported shapes.
- `responses.mode=prefer_upstream` is a proxy-first escape hatch, not the main
  V2 behavior.
- `responses.mode=local_only` never calls upstream.
- `GET /v1/responses/{id}/input_items` returns the effective item history used
  for generation, not just the current turn payload.
- `/v1/responses/input_tokens` and `/v1/responses/compact` are pragmatic
  shim-owned subsets, useful but not exact hosted parity claims.

## Gotchas

- Exact hosted SSE choreography is not promised for every tool family.
- Tool-heavy flows are intentionally documented as broad subsets, not exact
  hosted orchestration.
- `store=false` changes outward retention behavior, but the shim may still keep
  hidden internal rows needed for local replay and state reconstruction.

## Related Docs

- [Conversations](conversations.md)
- [Tools Overview](tools.md)
- [Official migration guide](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [Official conversation-state guide](https://developers.openai.com/api/docs/guides/conversation-state)
