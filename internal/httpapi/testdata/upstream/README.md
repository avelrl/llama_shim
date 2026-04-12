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
- `computer_call_screenshot.request.json` for a first-turn `computer_call`
  trace that should request a screenshot before taking any other action
- `computer_call_output.request.json` for a follow-up
  `computer_call_output` trace after you have a `previous_response_id`,
  `call_id`, and a PNG screenshot encoded as base64
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
- `image_generation_call*.request.json` templates only require
  `OPENAI_API_KEY`. They intentionally request a small image with
  `quality: "low"` and `partial_images: 1` in the streaming variant to keep
  future trace fixtures smaller.
- `computer_call_output.request.json` requires
  `OPENAI_PREVIOUS_RESPONSE_ID`, `OPENAI_COMPUTER_CALL_ID`, and
  `OPENAI_COMPUTER_SCREENSHOT_BASE64`. A convenient way to populate the last
  one is:
  `export OPENAI_COMPUTER_SCREENSHOT_BASE64="$(base64 < /path/to/screenshot.png | tr -d '\n')"`
- For the follow-up `computer_call_output` trace, use a PNG screenshot that
  actually shows a simple UI with a visible text input or search field;
  otherwise the model may legitimately stop without returning a useful action
  batch.
- Prefer short, deterministic prompts when capturing traces for tests.
- Treat the raw `.sse` file as the source of truth if the parsed fixture ever
  looks suspicious.
