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

Good fits:

- automating a browser or desktop flow when the target has no useful API
- testing a UI as a user would see it, including visual state after each step
- driving a deterministic internal admin page, fixture, or QA workflow
- letting a model decide the next UI action after each screenshot
- bridging an external browser/VM executor to the Responses state loop

Poor fits:

- data retrieval from systems that have stable APIs; use a normal tool or MCP
  connector instead
- high-volume scraping or background jobs; use direct HTTP/data pipelines
- flows that require strong security isolation unless the executor is already
  sandboxed outside the shim
- compatibility tests where you only need to prove JSON shape; use fixture
  tests or `make v3-local-runtimes-smoke`
- model visual-grounding evaluation; keep that as a separate eval profile
  because it measures model quality, not only shim protocol behavior

For this shim, `computer_call` is mainly useful as a protocol bridge: the shim
keeps Responses state, typed `computer_call` / `computer_call_output` items,
and replay surfaces, while your application owns the actual browser or VM.

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
          "image_url": "data:image/png;base64,<base64>",
          "detail": "original"
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
  multimodal text-plus-image input. `computer_call_output.output.detail:
  "original"` is accepted and stored on the Responses item, then mapped to
  `high` for the Chat-backed planner projection because Chat-compatible image
  inputs commonly accept only `auto`, `low`, or `high`.
- Planner actions are allow-listed to the current local action family:
  `screenshot`, `click`, `double_click`, `scroll`, `type`, `wait`,
  `keypress`, `drag`, and `move`.
- Planner output is accepted as strict `decision/actions` JSON or a
  single-action `action`/`args` JSON object for known actions. The shim
  normalizes these forms, including `action_type` as a `type` alias; it does
  not infer missing coordinates, text, or actions.
- Stored retrieve and `/v1/responses/{id}/input_items` keep the typed
  `computer_call` / `computer_call_output` items.
- Stream replay stays generic through `response.output_item.*`.
- Run `make v3-local-runtimes-smoke` against devstack to exercise the
  deterministic screenshot-first loop.
- Run `make v3-computer-browser-harness-smoke` when you want the optional
  Playwright-backed check that executes returned actions against a real local
  browser fixture.

## Gotchas

- The shim does not claim exact hosted `response.computer_call.*` SSE behavior.
- This is a practical external loop, not a hosted browser runtime.
- The browser harness is a developer/release smoke, not a portable CI gate.

## Related Docs

- [Tools Overview](tools.md)
- [V3 Computer Browser Harness](../v3-computer-browser-harness.md)
- [Official computer-use guide](https://developers.openai.com/api/docs/guides/tools-computer-use)
