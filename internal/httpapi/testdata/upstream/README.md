# Upstream SSE Fixtures

This directory is reserved for raw upstream Responses API SSE captures and the
normalized fixture JSON generated from them.

Recommended naming:

- `<label>.raw.sse` for the exact upstream body
- `<label>.fixture.json` for the parsed event stream plus capture metadata
- `<label>.request.json` for the request body used to reproduce the capture

Ready request files:

- `web_search_call.request.json` for a basic `search` action trace
- `web_search_call_open_page.request.json` for an `open_page`-leaning trace
- `web_search_call_find_in_page.request.json` for a `find_in_page`-leaning trace
- `file_search_call.request.json` for a basic `file_search_call` trace
- `file_search_call_include_results.request.json` for a `file_search_call`
  trace with `include=["file_search_call.results"]`
- `code_interpreter_call.request.json` for a basic `code_interpreter_call`
  trace using an auto-created container
- `code_interpreter_call_include_outputs.request.json` for a
  `code_interpreter_call` trace with
  `include=["code_interpreter_call.outputs"]`
- `code_interpreter_call_generated_file.request.json` for a
  `code_interpreter_call` trace that creates a small text artifact while
  `include=["code_interpreter_call.outputs"]` is enabled
- `code_interpreter_call_generated_image.request.json` for a
  `code_interpreter_call` trace that creates a small PNG artifact while
  `include=["code_interpreter_call.outputs"]` is enabled
- `code_interpreter_call_tool_error.request.json` for a
  `code_interpreter_call` trace that intentionally raises a Python error to
  clarify whether the hosted surface completes with logs or emits
  `response.failed`
- `computer_call_screenshot.request.json` for a first-turn `computer_call`
  trace that should request a screenshot before taking any other action
- `computer_call_output.request.json` for a follow-up
  `computer_call_output` trace after you have a `previous_response_id`,
  `call_id`, and a PNG screenshot encoded as base64
- `shell_call.request.json` for a first-turn local `shell` trace that should
  return a `shell_call`
- `shell_call_background.request.json` for a first-turn local `shell` trace
  created with `background=true` and `stream=true`, suitable for later
  `GET /v1/responses/{id}?stream=true`
- `shell_call_background_gpt5_1.request.json` for the same background local
  `shell` trace using `gpt-5.1`
- `shell_call_background_gpt5_1_codex.request.json` for the same background
  local `shell` trace using `gpt-5.1-codex`
- `shell_call_background_gpt5_3_codex.request.json` for the same background
  local `shell` trace using `gpt-5.3-codex`
- `shell_call_background_minimal.request.json` for a docs-literal background
  local `shell` trace that removes extra `instructions` and uses structured
  message input
- `shell_call_background_nostream.request.json` for a background local `shell`
  trace without `stream=true`, useful to separate a generic background-shell
  failure from a background-streaming failure
- `shell_call_output.request.json` for a follow-up `shell_call_output` trace
  after you have a `previous_response_id` and `OPENAI_SHELL_CALL_ID`
- `shell_call_output_background.request.json` for a follow-up
  `shell_call_output` trace created with `background=true` and `stream=true`,
  suitable for later retrieve-stream capture of the follow-up response
- `apply_patch_call.request.json` for a first-turn `apply_patch` trace that
  should return an `apply_patch_call`
- `apply_patch_call_background.request.json` for a first-turn `apply_patch`
  trace created with `background=true` and `stream=true`, suitable for later
  `GET /v1/responses/{id}?stream=true`
- `apply_patch_call_output.request.json` for a follow-up
  `apply_patch_call_output` trace after you have a `previous_response_id` and
  `OPENAI_APPLY_PATCH_CALL_ID`
- `apply_patch_call_output_background.request.json` for a follow-up
  `apply_patch_call_output` trace created with `background=true` and
  `stream=true`, suitable for later retrieve-stream capture of the follow-up
  response
- `image_generation_call.request.json` for a basic
  `image_generation_call` trace with a final image only
- `image_generation_call_partial_images.request.json` for an
  `image_generation_call` trace that should emit at least one
  `response.image_generation_call.partial_image` event

Use the capture helper:

```bash
go run ./cmd/upstream-sse-capture \
  -request-file internal/httpapi/testdata/upstream/web_search_call.request.json \
  -raw-out internal/httpapi/testdata/upstream/web_search_call.raw.sse \
  -fixture-out internal/httpapi/testdata/upstream/web_search_call.fixture.json \
  -label web_search_call
```

Guidelines:

- Do not commit secrets or bearer tokens.
- The generated fixture JSON keeps only a sanitized subset of response headers;
  if you captured traces before that change, scrub cookies, project IDs, org IDs,
  and request IDs before committing.
- `file_search_call*.request.json` templates require
  `OPENAI_VECTOR_STORE_ID` to be set; the capture helper expands `${VAR}`
  placeholders before sending the request.
- `code_interpreter_call*.request.json` templates use
  `container: {"type":"auto"}`, so they do not require any extra setup
  beyond `OPENAI_API_KEY`.
- the generated-file/image/tool-error `code_interpreter_call` templates exist
  specifically to capture docs-thin hosted behavior around
  `code_interpreter_call.outputs`, assistant-message annotations, and
  failure/status surface before we claim parity
- `image_generation_call*.request.json` templates only require
  `OPENAI_API_KEY`. They intentionally request a small image with
  `quality: "low"` and `partial_images: 1` in the streaming variant to keep
  future trace fixtures smaller.
- `computer_call_output.request.json` requires
  `OPENAI_PREVIOUS_RESPONSE_ID`, `OPENAI_COMPUTER_CALL_ID`, and
  `OPENAI_COMPUTER_SCREENSHOT_BASE64`. A convenient way to populate the last
  one is:
  `export OPENAI_COMPUTER_SCREENSHOT_BASE64="$(base64 < /path/to/screenshot.png | tr -d '\n')"`
- `shell_call_output.request.json` requires `OPENAI_PREVIOUS_RESPONSE_ID` and
  `OPENAI_SHELL_CALL_ID`. It also requires `OPENAI_SHELL_MAX_OUTPUT_LENGTH`
  from the first `shell_call` trace.
- `shell_call_output_background.request.json` requires the same variables as
  `shell_call_output.request.json`.
- `apply_patch_call_output.request.json` requires
  `OPENAI_PREVIOUS_RESPONSE_ID` and `OPENAI_APPLY_PATCH_CALL_ID`.
- `apply_patch_call_output_background.request.json` requires the same
  variables as `apply_patch_call_output.request.json`.
- Upstream retrieve-stream capture through
  `GET /v1/responses/{id}?stream=true` currently requires the original response
  to have been created with both `background=true` and `stream=true`. The
  plain request templates in this directory do not set `background=true`, so
  they are suitable for create-stream and follow-up create-stream captures, but
  not for retrieve-stream captures without a separate background-specific
  request.
- For the follow-up `computer_call_output` trace, use a PNG screenshot that
  actually shows a simple UI with a visible text input or search field;
  otherwise the model may legitimately stop without returning a useful action
  batch.
- Prefer short, deterministic prompts when capturing traces for tests.
- Treat the raw `.sse` file as the source of truth if the parsed fixture ever
  looks suspicious.
- If a background stream hits the client timeout after already emitting a
  terminal event such as `response.completed`, the capture helper preserves the
  partial raw body and fixture instead of discarding them.
- `cmd/upstream-sse-capture` only parses SSE responses. For diagnostics that
  intentionally omit `stream=true`, use `curl` or another plain HTTP client
  and save the JSON response separately.
- As of the April 23, 2026 shell diagnostics, both a minimal
  `background + stream + local shell` request and a non-streaming
  `background + local shell` request still end in `server_error`, so current
  shell background failures do not appear to be caused only by extra
  instructions or by the retrieve-stream lane.
