# V4 Chat Compatibility Layer

Last updated: May 20, 2026.

Status: planned. This is a shim-owned compatibility layer for practical
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

- Introduce a small internal "chat compatibility" boundary around existing
  sanitization, request cleanup hooks, raw-markup repair, and stream
  pseudo-tool conversion.
- Add tests that cover both stream and non-stream behavior under one naming
  scheme.
- Add debug-trace transform labels for Chat repairs so OpenCode/model
  certification failures are easier to classify.

Validation:

- focused `internal/httpapi` tests
- `make v4-chat-agent-smoke`
- `make v4-opencode-smoke`
- `go test ./...`
- `make lint`

### Slice 2: Structured Output And Tool-Call Content Hardening

- Add Chat-specific tests for `response_format: json_object` and
  `response_format: json_schema` across non-stream and stream paths.
- Normalize markdown-fenced JSON and common provider preambles only under a
  structured-output profile.
- Normalize assistant content next to tool calls only for profiles where the
  target client rejects that shape.

Validation:

- focused JSON/profile tests
- external tester Chat rows for selected models
- `v4-chat-agent-smoke` on at least one model that previously exposed the
  failure

### Slice 3: Evidence-Backed Provider Forms

- Add raw markup parsers only for forms observed in real artifacts.
- Keep each form paired with a fixture or test case that shows the upstream
  text and expected Chat shape.
- Avoid broad XML/markdown parsing that could consume ordinary assistant text.

Validation:

- fixture-backed unit tests
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

The first known real-client proof is:

- `.tmp/v4-opencode-smoke/gpu-qwen3-coder30b-q5km_20260520T145203Z`

It shows OpenCode receiving real Chat tools through the shim, editing
`calc.go`, and passing `go test ./...` after stream pseudo-tool normalization.
