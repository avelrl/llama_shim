# Web Search

## What It Is

The shim supports a practical local `web_search` / `web_search_preview` subset
inside `/v1/responses`.

Use it when the model needs current information from the web.

## When To Use It

Good fits:

- current events
- up-to-date company or product info
- web lookup with source links
- questions where model memory alone is not enough

## Minimal Request

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Find the latest OpenAI API docs for web search.",
    "tools": [{"type": "web_search"}]
  }'
```

## What You Get Back

A successful tool-using response usually includes:

- a `web_search_call` output item
- a final assistant `message`
- `url_citation` annotations on the output text

## Shim-Specific Notes

- The local runtime is enabled with `responses.web_search.backend=searxng`.
- The shim supports a practical `filters` subset for `web_search`.
- `include=["web_search_call.action.sources"]` is accepted in the local subset.
- `web_search_preview` is supported, but it behaves as a preview-compatible
  subset rather than the preferred long-term path.
- `web_search_preview` ignores `external_web_access`, matching the public tool
  guidance.

## Gotchas

- This is not a claim of exact hosted search parity.
- `prefer_upstream` stays proxy-first and does not silently reroute an upstream
  failure into local search.
- Preview-specific unsupported shapes, such as local `filters` on
  `web_search_preview`, fail explicitly in the local validation path.

## Related Docs

- [Tools Overview](tools.md)
- [Official web-search guide](https://developers.openai.com/api/docs/guides/tools-web-search)
