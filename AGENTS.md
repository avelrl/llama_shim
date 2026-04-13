# AGENTS

## Scope

This repository is an OpenAI-compatible shim. Any task that touches `/v1/responses`, `/v1/conversations`, `/v1/chat/completions`, OpenAPI, backlog status, or compatibility claims must be checked against current official OpenAI docs before it is marked done.

## Required verification workflow

Use this order every time:

1. Start with the local docs index at [`openapi/llms.txt`](openapi/llms.txt).
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

## When fixtures are mandatory

Use a real upstream fixture before closing parity work when any of the
following is true:

- the task depends on exact SSE choreography rather than only final JSON shape
- the public docs confirm an item family exists but do not fully specify event
  order or event payload fields
- the task changes stored replay or retrieve streaming fidelity for hosted or
  native tools
- the task depends on exact artifact placement, annotation placement, or
  intermediate tool-progress events
- the task depends on exact failure/status semantics and the docs do not pin
  down whether the upstream surface completes, fails, or returns partial data
- MCP/OpenAI docs search, API reference, and the live public site disagree or
  leave a material ambiguity about observable wire behavior

In those cases:

1. Add or reuse a request template under
   `internal/httpapi/testdata/upstream/`.
2. Capture a real upstream trace with `cmd/upstream-sse-capture`.
3. Commit the sanitized request, raw SSE, and parsed fixture when they are
   needed to justify the implementation.
4. Keep backlog/spec wording conservative until the fixture-backed behavior is
   implemented and tested.

Typical examples where fixtures are expected:

- tool-specific `response.*` SSE families
- hosted/native tool replay parity
- `code_interpreter_call.outputs` or artifact/citation behavior
- `response.failed` vs `response.completed` ambiguity
- image-generation partial events

Fixtures are not mandatory when the public docs and reference already define
the relevant contract well enough and the shim is only implementing a
generic/docs-backed subset without claiming exact hosted choreography.

## Path hygiene

- In committed repo files, use repo-relative paths only.
- Do not add absolute local filesystem paths to docs, backlog items, comments,
  or examples.
- When linking to files from repo markdown, prefer relative markdown links.

## Backlog rules

When updating [`backlog-v2.md`](backlog-v2.md):

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
- [`openapi/openapi.yaml`](openapi/openapi.yaml) matches the implementation
- integration tests cover the happy path and the main contract edges
- `go test ./...` passes
- backlog wording does not overclaim beyond what the code and docs actually support
