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
- Prefer short, deterministic prompts when capturing traces for tests.
- Treat the raw `.sse` file as the source of truth if the parsed fixture ever
  looks suspicious.
