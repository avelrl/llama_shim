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
[web_search_call.request.json](../internal/httpapi/testdata/upstream/web_search_call.request.json).

Keep prompts short and deterministic. The goal is not to benchmark model
behavior, but to capture the SSE event sequence and payload shape.

## Suggested request shape for `file_search_call`

The repository also includes ready-to-run templates at
[file_search_call.request.json](../internal/httpapi/testdata/upstream/file_search_call.request.json)
and
[file_search_call_include_results.request.json](../internal/httpapi/testdata/upstream/file_search_call_include_results.request.json).

They require `OPENAI_VECTOR_STORE_ID` to point at a vector store that already
contains at least one indexed file.

## Suggested request shape for `code_interpreter_call`

The repository also includes ready-to-run templates at
[code_interpreter_call.request.json](../internal/httpapi/testdata/upstream/code_interpreter_call.request.json)
and
[code_interpreter_call_include_outputs.request.json](../internal/httpapi/testdata/upstream/code_interpreter_call_include_outputs.request.json).

These use `container: {"type":"auto"}`, so they do not require any setup
beyond `OPENAI_API_KEY`. The prompt asks the model to use the "python tool"
explicitly, matching the wording in the official Code Interpreter guide.
The `include=["code_interpreter_call.outputs"]` variant is intended to verify
the live upstream behavior for outputs retrieval before we claim parity.

For docs-thin artifact and failure cases, the repository also includes:

- [code_interpreter_call_generated_file.request.json](../internal/httpapi/testdata/upstream/code_interpreter_call_generated_file.request.json)
- [code_interpreter_call_generated_image.request.json](../internal/httpapi/testdata/upstream/code_interpreter_call_generated_image.request.json)
- [code_interpreter_call_tool_error.request.json](../internal/httpapi/testdata/upstream/code_interpreter_call_tool_error.request.json)

These are intended to answer three specific parity questions that are not
fully pinned down by the public docs alone:

- whether generated non-image files ever appear in
  `code_interpreter_call.outputs`, or remain assistant-message annotations plus
  container files
- whether generated images appear in `code_interpreter_call.outputs`, or stay
  assistant-message annotations plus container files
- whether ordinary tool/runtime errors produce a completed response with logs
  or a terminal `response.failed`

Suggested capture flow:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/code_interpreter_call_generated_file.request.json \
  -raw-out internal/httpapi/testdata/upstream/code_interpreter_call_generated_file.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/code_interpreter_call_generated_file.fixture.json \
  -label code_interpreter_call_generated_file
```

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/code_interpreter_call_generated_image.request.json \
  -raw-out internal/httpapi/testdata/upstream/code_interpreter_call_generated_image.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/code_interpreter_call_generated_image.fixture.json \
  -label code_interpreter_call_generated_image
```

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/code_interpreter_call_tool_error.request.json \
  -raw-out internal/httpapi/testdata/upstream/code_interpreter_call_tool_error.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/code_interpreter_call_tool_error.fixture.json \
  -label code_interpreter_call_tool_error
```

## Suggested request shape for `computer_call`

The repository includes ready-to-run templates at
[computer_call_screenshot.request.json](../internal/httpapi/testdata/upstream/computer_call_screenshot.request.json)
and
[computer_call_output.request.json](../internal/httpapi/testdata/upstream/computer_call_output.request.json).

The first request is intended to capture the screenshot-first turn described in
the official Computer use guide. The follow-up request replays a
`computer_call_output` item using:

- `OPENAI_PREVIOUS_RESPONSE_ID` from the first trace
- `OPENAI_COMPUTER_CALL_ID` from the first `computer_call`
- `OPENAI_COMPUTER_SCREENSHOT_BASE64` containing a PNG screenshot, encoded as
  a single-line base64 string

Example flow:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/computer_call_screenshot.request.json \
  -raw-out internal/httpapi/testdata/upstream/computer_call_screenshot.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/computer_call_screenshot.fixture.json \
  -label computer_call_screenshot
```

```bash
export OPENAI_PREVIOUS_RESPONSE_ID="$(jq -r 'first(.stream.events[] | select(.event == \"response.completed\") | .json.response.id)' internal/httpapi/testdata/upstream/computer_call_screenshot.fixture.json)"
export OPENAI_COMPUTER_CALL_ID="$(jq -r 'first(.stream.events[] | select(.json.item.call_id != null) | .json.item.call_id)' internal/httpapi/testdata/upstream/computer_call_screenshot.fixture.json)"
export OPENAI_COMPUTER_SCREENSHOT_BASE64="$(base64 < /path/to/screenshot.png | tr -d '\n')"
```

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/computer_call_output.request.json \
  -raw-out internal/httpapi/testdata/upstream/computer_call_output.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/computer_call_output.fixture.json \
  -label computer_call_output
```

On the docs side, the contract is clear enough for request payloads:

- the built-in tool name is `computer`
- the first loop turn can return a `computer_call`
- follow-up input uses `computer_call_output`
- the API reference currently describes `computer_call_output.output.type` as
  `computer_screenshot` with `image_url` or `file_id`

What is still missing from docs is the exact Responses SSE family for
`computer_call`, so replay work should stay trace-backed and conservative.
For the second trace, use a screenshot that actually contains a visible text
input or search field. The template asks the model to click and type only if
the UI makes that possible; otherwise it may legitimately stop without
producing a richer action trace.

## Suggested request shape for `image_generation_call`

The repository includes ready-to-run templates at
[image_generation_call.request.json](../internal/httpapi/testdata/upstream/image_generation_call.request.json)
and
[image_generation_call_partial_images.request.json](../internal/httpapi/testdata/upstream/image_generation_call_partial_images.request.json).

These only require `OPENAI_API_KEY`. The partial-images variant requests a
single `response.image_generation_call.partial_image` event and uses
`quality: "low"` to keep future committed trace fixtures smaller.

Example flow:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/image_generation_call.request.json \
  -raw-out internal/httpapi/testdata/upstream/image_generation_call.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/image_generation_call.fixture.json \
  -label image_generation_call
```

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/image_generation_call_partial_images.request.json \
  -raw-out internal/httpapi/testdata/upstream/image_generation_call_partial_images.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/image_generation_call_partial_images.fixture.json \
  -label image_generation_call_partial_images
```

At the docs level, the contract is clear enough for request payloads and the
existence of `response.image_generation_call.partial_image`, but not for the
full stored replay choreography. Replay work should therefore stay
conservative until a live upstream trace is captured.

## Why this exists

Official docs currently confirm hosted output item families such as
`web_search_call`, `file_search_call`, `code_interpreter_call`, and `mcp_call`,
but they do not fully specify every Responses SSE event family and payload shape
needed for replay parity. Real upstream traces are the safest tie-breaker.
