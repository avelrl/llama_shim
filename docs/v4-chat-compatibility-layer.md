# V4 Chat Compatibility Layer

Last updated: May 20, 2026.

Status: Slices 1-3 implemented. This is a shim-owned compatibility layer for practical
Chat-first coding clients that use `/v1/chat/completions`. It is not a claim
that the Chat Completions API should behave like the Responses API, and it is
not an OpenAI API parity expansion.

## Source Check

Checked against official OpenAI documentation on May 20, 2026:

- Chat Completions create reference, via OpenAI docs search: `tools`,
  `tool_choice`, `stream`, `response_format`, `json_schema`, and
  `json_object` are documented Chat Completions request concepts.
- [Function Calling](https://developers.openai.com/api/docs/guides/function-calling):
  function/tool calling is a multi-step application loop, with tool calls as
  structured model outputs and tool outputs sent back by the application.
- [Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs):
  JSON Schema and JSON mode are available on Chat Completions and Responses,
  but schema adherence and edge cases remain model/backend dependent.

This plan uses those facts only as boundaries. Repairs below are shim-local
provider/client compatibility work for non-OpenAI backends, not new public
OpenAI contract claims.

## Purpose

The shim already has a broad Responses compatibility layer and a growing set of
provider-specific cleanup hooks. Chat-first coding clients such as OpenCode,
Aider-style agents, Cline-style IDE integrations, and Qwen Code-style tools
exercise a different surface:

- `POST /v1/chat/completions`
- streaming SSE chunks
- `tools` and `tool_choice`
- `delta.tool_calls`
- assistant messages followed by `role=tool` messages
- `response_format` for JSON output

Some fixes first built for Responses are useful here, but only after adapting
them to Chat's object shapes and streaming rules. The goal is to make Chat
clients reliable without quietly turning Chat into a lossy Responses emulator.

## Design Rule

Classify each candidate fix before implementation:

- **Portable:** same failure class, same public contract idea, only object paths
  differ. Implement for Chat.
- **Adapt:** useful, but Chat has different state, stream, or tool-output
  semantics. Implement a Chat-specific version.
- **No:** Responses-only state, hosted-tool choreography, or item graph logic.
  Do not move to Chat.

The layer must stay optically conservative:

- never invent fake OpenAI fields in client-visible Chat responses
- never convert ordinary text-only streams unless the request includes tools or
  a structured-output contract that justifies the repair
- keep repairs visible in debug traces and tests
- keep model/provider cleanup hooks configured by public model alias or
  upstream model pattern, not by hidden one-off branches

## Candidate Fix Matrix

| Candidate | Classification | Chat behavior | Notes |
| --- | --- | --- | --- |
| Remove provider-only fields such as `reasoning_content` from Chat responses and chunks | Portable | Keep current sanitization and empty-chunk suppression. | Already partially implemented. Extend tests when new provider fields appear. |
| Suppress empty streamed chunks after cleanup | Portable | Drop chunks that have no useful `delta`, no terminal `finish_reason`, and no `usage`. | Needed for reasoning-only providers and GPT-OSS-like streams. |
| Raw pseudo-tool markup detection in assistant text | Portable | Reject or repair non-stream text with `<function=...>`, `<tool_call>`, fenced command blocks, or provider-native tool markers when request has tools. | Existing non-stream repair should become part of the named layer. |
| Raw pseudo-tool markup in Chat streaming | Adapt | For tool-enabled streams, normalize known pseudo function markup into `delta.tool_calls`; otherwise leave ordinary text alone. | First OpenCode fix implemented this for `<function=...><parameter=...>`. Future work should expand only evidence-backed forms. |
| Assistant content next to tool calls | Portable | Suppress or preserve according to configured Codex/Chat profile; default should avoid breaking clients that reject non-empty content with tool calls. | Must not erase useful final text when no tool call exists. |
| `response_format` JSON fence cleanup | Portable | For `json_object` and `json_schema`, unwrap markdown fences and provider preambles in `message.content` and `delta.content`. | Do not claim schema enforcement unless backend actually enforces it. |
| JSON Schema downgrade to JSON mode for weak upstreams | Adapt | Apply provider cleanup hooks before proxying Chat requests when a backend cannot initialize samplers for schema grammar. | Keep the original user contract visible in trace/evidence, not in client response. |
| Tool schema normalization | Adapt | Ensure function parameter schemas use backend-compatible shapes where configured. | Avoid silently making optional fields mandatory unless the backend rule is explicit. |
| Forced/required `tool_choice` compatibility retry | Portable | Retry once when upstream rejects a supported Chat `tool_choice` shape and a safer compatible shape exists. | Existing non-stream retry can be formalized; streaming retry needs separate design because headers may already be committed. |
| Responses conversation/store replay | No | Do not port. | Chat has its own shadow-store work and does not expose Responses item state. |
| Responses item graph and hosted-tool SSE families | No | Do not port. | `computer_call`, `code_interpreter_call`, hosted MCP/search/image item choreography is Responses-specific. |
| Responses local tool loop state machine | No | Do not port as-is. | Chat clients own the tool execution loop; shim should repair wire shape, not execute arbitrary Chat client tools. |

## Implementation Slices

### Slice 1: Name And Consolidate Existing Chat Repairs

- Status: implemented on May 20, 2026.
- Introduce a small internal "chat compatibility" boundary around existing
  sanitization, request cleanup hooks, raw-markup repair, and stream
  pseudo-tool conversion.
- Add tests that cover both stream and non-stream behavior under one naming
  scheme.
- Add debug-trace transform labels for Chat repairs so OpenCode/model
  certification failures are easier to classify.

Debug trace transforms use `stage=chat_compatibility` and
`class=chat_completions`. Current hook names are:

- `chat_completions.structured_json_normalize`
- `chat_completions.raw_tool_markup_repair`
- `chat_completions.stream_pseudo_tool_markup_to_tool_calls`
- `chat_completions.tool_choice_retry`
- `chat_completions.minimum_retry_tokens`

Validation:

- focused `internal/httpapi` tests: `go test ./internal/httpapi`
- `make v4-chat-agent-smoke`
- `make v4-opencode-smoke`
- `go test ./...`
- `make lint`

### Slice 2: Structured Output And Tool-Call Content Hardening

- Status: conservative guardrails implemented on May 20, 2026.
- Add Chat-specific tests for `response_format: json_object` and
  `response_format: json_schema` across non-stream and stream paths.
- Normalize markdown-fenced JSON and common provider preambles only under a
  structured-output profile.
- Normalize assistant content next to tool calls only for profiles where the
  target client rejects that shape.

The default behavior deliberately preserves non-empty assistant `content` next
to `tool_calls`, because many Chat-first clients tolerate or depend on that
shape. The current implemented guardrail is narrower:

- structured-output normalization touches only `choices[].message.content` and
  `choices[].delta.content`
- `tool_calls[].function.arguments` are not normalized as structured-output
  final text
- configured request cleanup may omit empty assistant `content` next to
  `tool_calls`, but it preserves non-empty assistant content

Validation:

- focused JSON/profile tests: `go test ./internal/httpapi ./internal/upstreamcompat`
- external tester Chat rows for selected models
- `v4-chat-agent-smoke` on at least one model that previously exposed the
  failure

### Slice 3: Evidence-Backed Provider Forms

- Status: first evidence-backed form implemented on May 20, 2026.
- Add raw markup parsers only for forms observed in real artifacts.
- Keep each form paired with a fixture or test case that shows the upstream
  text and expected Chat shape.
- Avoid broad XML/markdown parsing that could consume ordinary assistant text.

Current stream conversion support:

- `<function=NAME><parameter=...>...</parameter></function>` is converted to
  `delta.tool_calls` when the request has tools and raw-markup repair is active.
- `<chatcmpl-tool>{"name":"NAME","arguments":{...}}</chatcmpl-tool>` is also
  converted when `name` is explicit and `arguments` is valid JSON.

Ambiguous forms such as `<tools>...</tools>` remain buffered and are released
as text at the terminal chunk if they cannot be safely converted. They stay in
the raw-markup detector and repair prompt for non-stream retry paths, but the
streaming path does not invent a tool name or arguments shape.

Validation:

- fixture-backed unit tests: `go test ./internal/httpapi ./internal/upstreamcompat`
- one real-client smoke artifact, preferably OpenCode
- work-queue/model evidence update if a model status changes

## Explicit Non-Goals

- Do not implement Responses item replay on Chat.
- Do not make Chat clients depend on shim-owned hidden state.
- Do not run local shell/edit tools for arbitrary Chat clients inside the shim.
- Do not claim exact OpenAI hosted tool parity for non-OpenAI backends.
- Do not add provider-specific hacks without a config hook, test, and evidence.

## Evidence To Keep

Each implemented repair should point at at least one of:

- focused unit/integration test
- `v4-chat-agent-smoke` artifact
- `v4-opencode-smoke` artifact
- model certification artifact
- external OpenAI-compatible tester report

Current known Chat Compatibility Layer evidence for `gpu/qwen3-coder30b-q5km`:

- `.tmp/v4-opencode-smoke/gpu-qwen3-coder30b-q5km_20260520T145203Z`
- `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder30b-q5km_20260520T175840Z`
- `.tmp/v4-opencode-smoke/gpu-qwen3-coder30b-q5km_20260520T180029Z`
- `.tmp/model-certification/cert-20260520T180503Z`

The Chat-agent and OpenCode runs show streamed Chat tools through the shim,
file edits, and `go test ./...` completion. The certification run is not a
Chat-layer failure: `model-certify-api` stopped at the external tester because
`chat.basic` expected exact `OK` and the model answered `pong` to a `ping`
user message. Invalid-shape negative checks returned expected `400`
validation errors (`input` and `messages` shape). Treat this as model exactness
limitation, not a shim transport or V4 Chat Compatibility regression.

`gpu/qwen3-30b-instruct` also has Chat-first evidence:

- `.tmp/v4-chat-agent-smoke/gpu-qwen3-30b-instruct_20260520T134315Z`
- `.tmp/v4-opencode-smoke/gpu-qwen3-30b-instruct_20260520T192306Z`

This makes it a useful second local Chat-first candidate, separate from Codex
promotion decisions.

`gpu/qwen3-coder-30b` now has Chat-first evidence too:

- `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder-30b_20260520T194113Z`
- `.tmp/v4-opencode-smoke/gpu-qwen3-coder-30b_20260520T194141Z`

This supports supervised Chat/OpenCode usage. It does not override the Codex
bench-lite finding where `patch_after_context` repeatedly missed the required
edit.

`gpu/qwen3-coder-next` has Chat-first evidence:

- `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder-next_20260520T210116Z`
- `.tmp/v4-opencode-smoke/gpu-qwen3-coder-next_20260520T210229Z`

This makes it a promising Chat/OpenCode candidate; keep Codex promotion
separate until model certification evidence exists.

`gpu/qwen3-next-instruct` has mixed Chat-first evidence:

- `.tmp/v4-chat-agent-smoke/gpu-qwen3-next-instruct_20260520T213404Z`
- `.tmp/v4-opencode-smoke/gpu-qwen3-next-instruct_20260520T213441Z`

The shim-owned Chat-agent smoke passed, but the real OpenCode smoke did not
patch `calc.go`. Treat it as Chat-agent capable but not yet real-client green.

`gpu/glm47-flash-opus-reasoning` has Chat-first evidence despite blocked Codex
promotion:

- `.tmp/v4-chat-agent-smoke/gpu-glm47-flash-opus-reasoning_20260520T194326Z`
- `.tmp/v4-opencode-smoke/gpu-glm47-flash-opus-reasoning_20260520T194446Z`

This keeps it useful for Chat/OpenCode diagnostics and reasoning-content stress
without treating it as a Codex gate.
