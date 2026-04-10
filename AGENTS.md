# AGENTS

## Scope

This repository is an OpenAI-compatible shim. Any task that touches `/v1/responses`, `/v1/conversations`, `/v1/chat/completions`, OpenAPI, backlog status, or compatibility claims must be checked against current official OpenAI docs before it is marked done.

## Required verification workflow

Use this order every time:

1. Start with the local docs index at [`openapi/llms.txt`](/Users/avel/Projects/llama_shim/openapi/llms.txt).
   Use it as a map of relevant official pages only.
   It is not the source of truth for exact request or response contracts.
2. Use OpenAI Docs MCP tools against `developers.openai.com` and `platform.openai.com`.
   Search first, then fetch the exact page or section you need.
3. Do a final spot-check on the official site directly for the exact page or endpoint you are validating.
   Prefer only official OpenAI domains.
4. If MCP and the official site disagree, or MCP is thin, ambiguous, or missing exact schema details, treat the current official site page as the tie-breaker and update the backlog/spec conservatively.

Do not close a compatibility task from memory alone.

## What must be checked

For any OpenAI-surface task, verify all of these that apply:

- endpoint existence and HTTP method
- path params, query params, and defaults
- request body shape and limits
- response shape, including nested object form
- pagination semantics
- streaming event flow and event payload fields
- retention and storage semantics
- error behavior and unsupported-field behavior

Do not mark a task done if only the route exists but the documented semantics still differ.

## Backlog rules

When updating [`backlog-v2.md`](/Users/avel/Projects/llama_shim/backlog-v2.md):

- Keep the top-level baseline and “current patch” sections in sync with the actual router and handlers.
- A checked box means the implementation, OpenAPI, tests, and docs-aware behavior are aligned closely enough for that task’s stated scope.
- If parity is only partial, keep the item open or split the remaining gap into an explicit follow-up task.
- Use exact dates when re-validating the backlog against docs.

## Implementation rules for this repo

- For Responses and Conversations work, check both guide pages and API reference pages.
  Guides explain semantics; reference pages define contract details.
- Treat `include` support carefully.
  “Accepted as compatibility no-op” is not the same as fully implemented semantics.
- Treat retrieve streaming separately from create streaming.
  Route parity is not enough; replay event fidelity matters.
- Treat `input_items` as “items used to generate the response”, not just the current request body.
- For constrained custom tools, distinguish between:
  shim-local supported subset,
  repair/validation compatibility layer,
  and true constrained decoding/runtime parity.
- For `responses.mode`, verify behavior in `prefer_local`, `prefer_upstream`, and `local_only` when the task changes fallback behavior.

## Before calling a task done

Confirm all of the following:

- router/handler/service/storage behavior matches the intended scope
- [`openapi/openapi.yaml`](/Users/avel/Projects/llama_shim/openapi/openapi.yaml) matches the implementation
- integration tests cover the happy path and the main contract edges
- `go test ./...` passes
- backlog wording does not overclaim beyond what the code and docs actually support
