# Chat Completions

## What It Is

`/v1/chat/completions` is the legacy-compatible surface.

In `llama_shim`, it is still useful, but it is no longer the main product
surface. V2 treats `Responses` as primary and `Chat Completions` as a broad
compatibility layer.

## When To Use It

Use `Chat Completions` when:

- you already have a legacy client
- you only need the classic `messages[]` shape
- you do not want to migrate a simple integration yet

Use `Responses` when you need tools, richer state, or a more future-proof
surface.

## Minimal Request

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "messages": [
      {"role": "user", "content": "Say OK and nothing else."}
    ]
  }'
```

## Stored Chat Flow

Create a stored chat:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "store": true,
    "messages": [
      {"role": "user", "content": "Remember ticket 42."}
    ]
  }'
```

Then use the stored-resource routes:

- `GET /v1/chat/completions`
- `GET /v1/chat/completions/{completion_id}`
- `POST /v1/chat/completions/{completion_id}`
- `DELETE /v1/chat/completions/{completion_id}`
- `GET /v1/chat/completions/{completion_id}/messages`

## Shim-Specific Notes

- Stored chats are local-first. The shim shadow-stores successful completions
  and serves the stored CRUD surface from that local ownership model.
- Upstream stored-chat routes are only an optional compatibility bridge.
- Omitted `store` can still result in storage when
  `chat_completions.default_store_when_omitted=true`.
- Streamed completions are reconstructed into the local shadow-store using a
  practical subset, not every possible hosted chunk variant.
- Local stored-chat list and messages routes use SQL pagination for new
  shadow-stored rows. Older database rows without the message snapshot still
  fall back to the captured request JSON for compatibility.
- Shadow-store capture is best-effort. If an upstream response exceeds the
  internal `shim.limits.chat_completions_shadow_store_bytes` budget, the client
  response is still proxied and only local persistence is skipped.

## Gotchas

- This is not a promise of full hosted stored-chat ownership parity.
- If you need the main tool surface, use [Responses](responses.md) instead.

## Related Docs

- [Responses](responses.md)
- [Official migration guide](https://developers.openai.com/api/docs/guides/migrate-to-responses)
