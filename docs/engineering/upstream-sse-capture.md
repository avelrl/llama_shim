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

If the server keeps the streaming connection open past the client timeout, the
helper now preserves any partial SSE body it already received. When the partial
body already contains a terminal response event such as `response.completed`,
the raw stream and parsed fixture are still written so background traces can be
kept instead of being discarded.

## Suggested request shape for `web_search_call`

The repository includes a ready-to-run example request at
[web_search_call.request.json](../../internal/httpapi/testdata/upstream/web_search_call.request.json).

Keep prompts short and deterministic. The goal is not to benchmark model
behavior, but to capture the SSE event sequence and payload shape.

## Suggested request shape for `file_search_call`

The repository also includes ready-to-run templates at
[file_search_call.request.json](../../internal/httpapi/testdata/upstream/file_search_call.request.json)
and
[file_search_call_include_results.request.json](../../internal/httpapi/testdata/upstream/file_search_call_include_results.request.json).

They require `OPENAI_VECTOR_STORE_ID` to point at a vector store that already
contains at least one indexed file.

## Suggested request shape for `code_interpreter_call`

The repository also includes ready-to-run templates at
[code_interpreter_call.request.json](../../internal/httpapi/testdata/upstream/code_interpreter_call.request.json)
and
[code_interpreter_call_include_outputs.request.json](../../internal/httpapi/testdata/upstream/code_interpreter_call_include_outputs.request.json).

These use `container: {"type":"auto"}`, so they do not require any setup
beyond `OPENAI_API_KEY`. The prompt asks the model to use the "python tool"
explicitly, matching the wording in the official Code Interpreter guide.
The `include=["code_interpreter_call.outputs"]` variant is intended to verify
the live upstream behavior for outputs retrieval before we claim parity.

For docs-thin artifact and failure cases, the repository also includes:

- [code_interpreter_call_generated_file.request.json](../../internal/httpapi/testdata/upstream/code_interpreter_call_generated_file.request.json)
- [code_interpreter_call_generated_image.request.json](../../internal/httpapi/testdata/upstream/code_interpreter_call_generated_image.request.json)
- [code_interpreter_call_tool_error.request.json](../../internal/httpapi/testdata/upstream/code_interpreter_call_tool_error.request.json)

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
[computer_call_screenshot.request.json](../../internal/httpapi/testdata/upstream/computer_call_screenshot.request.json)
and
[computer_call_output.request.json](../../internal/httpapi/testdata/upstream/computer_call_output.request.json).

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

## Suggested request shapes for `shell_call` and `apply_patch_call`

The repository includes ready-to-run templates at
[shell_call.request.json](../../internal/httpapi/testdata/upstream/shell_call.request.json),
[shell_call_background.request.json](../../internal/httpapi/testdata/upstream/shell_call_background.request.json),
[shell_call_output.request.json](../../internal/httpapi/testdata/upstream/shell_call_output.request.json),
[shell_call_output_background.request.json](../../internal/httpapi/testdata/upstream/shell_call_output_background.request.json),
[apply_patch_call.request.json](../../internal/httpapi/testdata/upstream/apply_patch_call.request.json),
and
[apply_patch_call_output.request.json](../../internal/httpapi/testdata/upstream/apply_patch_call_output.request.json),
[apply_patch_call_background.request.json](../../internal/httpapi/testdata/upstream/apply_patch_call_background.request.json),
and
[apply_patch_call_output_background.request.json](../../internal/httpapi/testdata/upstream/apply_patch_call_output_background.request.json).

These only require `OPENAI_API_KEY`.
The first-turn captures ask the model to emit one local-tool call.
The follow-up captures replay one client-executed result back through
`previous_response_id`.

Suggested capture flow for `shell_call`:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/shell_call.request.json \
  -raw-out internal/httpapi/testdata/upstream/shell_call.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/shell_call.fixture.json \
  -label shell_call
```

```bash
export OPENAI_PREVIOUS_RESPONSE_ID="$(jq -r 'first(.stream.events[] | select(.event == \"response.completed\") | .json.response.id)' internal/httpapi/testdata/upstream/shell_call.fixture.json)"
export OPENAI_SHELL_CALL_ID="$(jq -r 'first(.stream.events[] | select(.json.item.type == \"shell_call\") | .json.item.call_id)' internal/httpapi/testdata/upstream/shell_call.fixture.json)"
export OPENAI_SHELL_MAX_OUTPUT_LENGTH="$(jq -r 'first(.stream.events[] | select(.json.item.type == \"shell_call\") | .json.item.action.max_output_length)' internal/httpapi/testdata/upstream/shell_call.fixture.json)"
```

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/shell_call_output.request.json \
  -raw-out internal/httpapi/testdata/upstream/shell_call_output.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/shell_call_output.fixture.json \
  -label shell_call_output
```

Suggested capture flow for `apply_patch_call`:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/apply_patch_call.request.json \
  -raw-out internal/httpapi/testdata/upstream/apply_patch_call.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/apply_patch_call.fixture.json \
  -label apply_patch_call
```

```bash
export OPENAI_PREVIOUS_RESPONSE_ID="$(jq -r 'first(.stream.events[] | select(.event == \"response.completed\") | .json.response.id)' internal/httpapi/testdata/upstream/apply_patch_call.fixture.json)"
export OPENAI_APPLY_PATCH_CALL_ID="$(jq -r 'first(.stream.events[] | select(.json.item.type == \"apply_patch_call\") | .json.item.call_id)' internal/httpapi/testdata/upstream/apply_patch_call.fixture.json)"
```

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/apply_patch_call_output.request.json \
  -raw-out internal/httpapi/testdata/upstream/apply_patch_call_output.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/apply_patch_call_output.fixture.json \
  -label apply_patch_call_output
```

The public docs are clear enough about the request and follow-up item shapes:

- local shell uses `tools: [{"type":"shell","environment":{"type":"local"}}]`
- shell follow-up uses `shell_call_output`
- apply patch uses `tools: [{"type":"apply_patch"}]`
- patch follow-up uses `apply_patch_call_output`

What the docs still do not pin down is exact stream choreography for these
tool families, especially stored retrieve replay. That is why these traces are
worth capturing before we claim exact upstream parity beyond generic
`response.output_item.*` replay.

Current fixture-backed findings from captures recorded on April 23, 2026:

- first-turn local `shell_call` create-stream emits shell-specific events:
  - `response.shell_call_command.added`
  - `response.shell_call_command.delta`
  - `response.shell_call_command.done`
- first-turn local `apply_patch_call` create-stream emits patch-specific events:
  - `response.apply_patch_call_operation_diff.delta`
  - `response.apply_patch_call_operation_diff.done`
- background-created `apply_patch_call` retrieve-stream replays the same
  patch-specific event family, including
  `response.apply_patch_call_operation_diff.*` and
  `response.output_item.done`
- for both tool families, `response.output_item.added` starts with an incomplete
  item and the finalized payload appears only by `response.output_item.done`
- follow-up requests using `shell_call_output` and `apply_patch_call_output`
  currently replay as ordinary assistant-message streams; no extra tool-specific
  SSE family was observed in the follow-up traces
- upstream validates `shell_call_output.max_output_length` against the original
  `shell_call.action.max_output_length`
- current background `shell_call` attempts can fail before emitting any
  `shell_call` item, returning `response.failed` with `server_error` instead;
  this has now been reproduced on both `gpt-5.4` and `gpt-5.3-codex`
- the narrower diagnostics now also fail:
  - a docs-literal minimal `background + stream + local shell` request still
    returns `response.failed` with `server_error`
  - a non-streaming `background + local shell` request is accepted, starts in
    `queued`, and later retrieves as `failed` with the same `server_error`

Important current upstream limitation:

- `GET /v1/responses/{id}?stream=true` is rejected unless the response was
  created with `background=true`
- the official Background mode guide currently states: "You can only start a
  new stream from a background response if you created it with `stream=true`."

That means the request templates in this directory are valid for:

- first-turn create-stream captures
- follow-up create-stream captures

But they are not sufficient for upstream retrieve-stream capture.
If you want a retrieve-stream fixture for `shell` or `apply_patch`, first
capture a separate background response with both:

- `"background": true`
- `"stream": true`

Then use `GET /v1/responses/{id}?stream=true` against that background-created
response.

Suggested background capture flow for `shell_call` retrieve replay:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/shell_call_background.request.json \
  -raw-out internal/httpapi/testdata/upstream/shell_call_background.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/shell_call_background.fixture.json \
  -label shell_call_background
```

```bash
export OPENAI_BACKGROUND_RESPONSE_ID="$(jq -r 'first(.stream.events[] | select(.event == \"response.completed\") | .json.response.id)' internal/httpapi/testdata/upstream/shell_call_background.fixture.json)"
```

```bash
go run ./cmd/upstream-sse-capture \
  -method GET \
  -path "/v1/responses/${OPENAI_BACKGROUND_RESPONSE_ID}?stream=true" \
  -raw-out internal/httpapi/testdata/upstream/shell_call_background_retrieve.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/shell_call_background_retrieve.fixture.json \
  -label shell_call_background_retrieve
```

As of the captures recorded on April 23, 2026, this shell background lane is
not yet reliable: the captured background `shell_call` attempts failed with
`response.failed` / `server_error` before any `shell_call` item was emitted.
This was reproduced on both `gpt-5.4` and `gpt-5.3-codex`, so keep shell
retrieve claims conservative until a successful background shell trace exists.

Two useful follow-up diagnostics are now templated in the repository:

- [shell_call_background_minimal.request.json](../../internal/httpapi/testdata/upstream/shell_call_background_minimal.request.json)
  removes the extra `instructions` field and uses structured message input
- [shell_call_background_nostream.request.json](../../internal/httpapi/testdata/upstream/shell_call_background_nostream.request.json)
  removes `stream=true` to test whether the failure is tied to streaming or to
  background local shell more generally

The first variant is still an SSE capture:

```bash
go run ./cmd/upstream-sse-capture \
  -timeout 180s \
  -request-file internal/httpapi/testdata/upstream/shell_call_background_minimal.request.json \
  -raw-out internal/httpapi/testdata/upstream/shell_call_background_minimal.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/shell_call_background_minimal.fixture.json \
  -label shell_call_background_minimal
```

The second variant is intentionally non-streaming, so `cmd/upstream-sse-capture`
is not the right tool. Use plain JSON capture instead:

```bash
curl https://api.openai.com/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d @internal/httpapi/testdata/upstream/shell_call_background_nostream.request.json \
  > internal/httpapi/testdata/upstream/shell_call_background_nostream.response.json
```

Then inspect the created response id and status:

```bash
jq '{id,status,error}' internal/httpapi/testdata/upstream/shell_call_background_nostream.response.json
```

If the response is queued or in progress, retrieve the final JSON shape later:

```bash
curl "https://api.openai.com/v1/responses/$(jq -r '.id' internal/httpapi/testdata/upstream/shell_call_background_nostream.response.json)" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  > internal/httpapi/testdata/upstream/shell_call_background_nostream.retrieve.json
```

As of the April 23, 2026 diagnostics, this non-streaming background request
also ends in `status: "failed"` with `error.code: "server_error"`, so the
current evidence points to a broader upstream issue with `background + local
shell`, not just a retrieve-stream or extra-instructions problem.

Suggested background capture flow for `apply_patch_call` retrieve replay:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/apply_patch_call_background.request.json \
  -raw-out internal/httpapi/testdata/upstream/apply_patch_call_background.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/apply_patch_call_background.fixture.json \
  -label apply_patch_call_background
```

```bash
export OPENAI_BACKGROUND_RESPONSE_ID="$(jq -r 'first(.stream.events[] | select(.event == \"response.completed\") | .json.response.id)' internal/httpapi/testdata/upstream/apply_patch_call_background.fixture.json)"
```

```bash
go run ./cmd/upstream-sse-capture \
  -method GET \
  -path "/v1/responses/${OPENAI_BACKGROUND_RESPONSE_ID}?stream=true" \
  -raw-out internal/httpapi/testdata/upstream/apply_patch_call_background_retrieve.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/apply_patch_call_background_retrieve.fixture.json \
  -label apply_patch_call_background_retrieve
```

The April 23, 2026 captures confirm that this apply-patch retrieve stream
replays the same patch-specific diff events observed during create-stream, so
retrieve parity for this tool family is no longer docs-thin.

If you also want retrieve-stream fixtures for the follow-up background-created
responses, repeat the same pattern with
`shell_call_output_background.request.json` or
`apply_patch_call_output_background.request.json`, then retrieve the resulting
background response id with `GET /v1/responses/{id}?stream=true`.

## Suggested request shape for `image_generation_call`

The repository includes ready-to-run templates at
[image_generation_call.request.json](../../internal/httpapi/testdata/upstream/image_generation_call.request.json)
and
[image_generation_call_partial_images.request.json](../../internal/httpapi/testdata/upstream/image_generation_call_partial_images.request.json).

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
