# Upstream Responses SSE Capture

This repository uses synthetic replay for stored `/v1/responses/{id}?stream=true`
when the original upstream SSE log is not available. For true hosted-tool
parity work, capture a real upstream stream first and then implement replay from
that evidence.

## Capture command

Use the local helper command:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/web_search_call.request.json \
  -raw-out internal/httpapi/testdata/upstream/web_search_call.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/web_search_call.fixture.json \
  -label web_search_call
```

Defaults:

- `OPENAI_API_KEY` is used for authentication
- `OPENAI_BASE_URL` defaults to `https://api.openai.com`
- request target defaults to `POST /v1/responses`
- `${VAR}` placeholders inside request JSON are expanded from the current
  environment before the request is sent

The command writes:

- raw upstream SSE body
- parsed fixture JSON with event list, status code, request body, and a
  sanitized subset of response headers

## Suggested request shape for `web_search_call`

The repository includes a ready-to-run example request at
[web_search_call.request.json](/Users/avel/Projects/llama_shim/internal/httpapi/testdata/upstream/web_search_call.request.json).

Keep prompts short and deterministic. The goal is not to benchmark model
behavior, but to capture the SSE event sequence and payload shape.

## Suggested request shape for `file_search_call`

The repository also includes ready-to-run templates at
[file_search_call.request.json](/Users/avel/Projects/llama_shim/internal/httpapi/testdata/upstream/file_search_call.request.json)
and
[file_search_call_include_results.request.json](/Users/avel/Projects/llama_shim/internal/httpapi/testdata/upstream/file_search_call_include_results.request.json).

They require `OPENAI_VECTOR_STORE_ID` to point at a vector store that already
contains at least one indexed file.

## Why this exists

Official docs currently confirm hosted output item families such as
`web_search_call`, `file_search_call`, `code_interpreter_call`, and `mcp_call`,
but they do not fully specify every Responses SSE event family and payload shape
needed for replay parity. Real upstream traces are the safest tie-breaker.
