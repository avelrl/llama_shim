# Conversations

## What It Is

`/v1/conversations` gives you a durable conversation object that stores items
across turns.

Use it when you want a stable conversation ID that can outlive any single
response chain.

## When To Use It

Use `conversation` when you want:

- a long-lived thread identifier
- a persistent support or assistant session
- state shared across jobs, devices, or resumptions
- explicit item append and item inspection APIs

Use `previous_response_id` when you only need a lightweight chain of turns and
do not need a durable conversation object.

## Minimal Flow

### 1. Create a conversation

```bash
curl http://127.0.0.1:8080/v1/conversations \
  -H "Content-Type: application/json" \
  -d '{
    "items": [
      {"type": "message", "role": "user", "content": "Start a new thread."}
    ]
  }'
```

### 2. Use the conversation in `Responses`

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "conversation": "conv_...",
    "input": "Continue the thread."
  }'
```

### 3. Read or append items directly

List items:

```bash
curl http://127.0.0.1:8080/v1/conversations/conv_.../items
```

Append items:

```bash
curl http://127.0.0.1:8080/v1/conversations/conv_.../items \
  -H "Content-Type: application/json" \
  -d '{
    "items": [
      {"type": "message", "role": "user", "content": "Add this note to the thread."}
    ]
  }'
```

## Shim-Specific Notes

- The shim owns conversation IDs and item history locally.
- Conversation items are the durable state model in V2; they are not just a
  thin wrapper around `previous_response_id`.
- Conversation-attached items follow the conversation lifecycle rather than the
  standalone stored-response TTL model.

## Gotchas

- Conversations are for durable thread state, not for bypassing all context
  management concerns.
- If you only need a simple follow-up chain, `previous_response_id` is still
  the lighter option.

## Related Docs

- [Responses](responses.md)
- [Official conversation-state guide](https://developers.openai.com/api/docs/guides/conversation-state)
