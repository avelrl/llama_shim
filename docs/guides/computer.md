# Computer Use

## What It Is

The shim supports a screenshot-first local `computer` subset inside
`/v1/responses`.

It follows the docs-aligned external loop model:

1. request a screenshot
2. return a `computer_call`
3. execute actions outside the model
4. send back `computer_call_output`
5. continue until the model stops asking for `computer_call`

## When To Use It

Use it when you want a model to work through a UI workflow and you already have
code that can execute mouse, keyboard, or screenshot actions.

## Minimal First Turn

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Use the computer tool. First request a screenshot.",
    "tools": [{"type": "computer"}]
  }'
```

The first turn will often return a `computer_call` with a `screenshot` action.

## Follow-Up Turn

After your runtime captures the screenshot and executes any returned actions,
send a `computer_call_output` item:

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "previous_response_id": "resp_...",
    "input": [
      {
        "type": "computer_call_output",
        "call_id": "call_...",
        "output": {
          "type": "computer_screenshot",
          "image_url": "data:image/png;base64,<base64>"
        }
      }
    ],
    "tools": [{"type": "computer"}]
  }'
```

## Shim-Specific Notes

- Enable the local runtime with `responses.computer.backend=chat_completions`.
- The planner runs over the existing `/v1/chat/completions` backend.
- Current-turn screenshots are projected into the local planner context as
  multimodal text-plus-image input.
- Stored retrieve and `/v1/responses/{id}/input_items` keep the typed
  `computer_call` / `computer_call_output` items.
- Stream replay stays generic through `response.output_item.*`.

## Gotchas

- The shim does not claim exact hosted `response.computer_call.*` SSE behavior.
- This is a practical external loop, not a hosted browser runtime.

## Related Docs

- [Tools Overview](tools.md)
- [Official computer-use guide](https://developers.openai.com/api/docs/guides/tools-computer-use)
