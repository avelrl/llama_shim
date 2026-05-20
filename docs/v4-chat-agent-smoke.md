# V4 Chat Agent Smoke

Last updated: May 20, 2026.

Status: implemented first slice. This is a shim-owned operator smoke for
OpenAI-compatible `/v1/chat/completions`; it is not an OpenAI parity claim and
not a replacement for Codex eval.

## Purpose

Many practical coding agents are Chat-first rather than Responses-first. Aider,
Cline, Qwen Code, OpenCode/OpenCoder-style clients, and local IDE integrations
usually exercise `/v1/chat/completions` with streaming, function tool calls, and
`role=tool` follow-up messages.

Codex certification is still the right gate for Codex compatibility, but it
does not answer whether a model is usable for Chat-first coding tools. This
smoke covers that separate workflow.

The next integration layer is [V4 OpenCode Smoke](v4-opencode-smoke.md), which
will run the real local `opencode` CLI through the shim after this deterministic
harness is green.

Compatibility hardening that applies beyond this smoke is tracked in
[V4 Chat Compatibility Layer](v4-chat-compatibility-layer.md). That document is
where Responses-era repairs are classified before being adapted to
Chat Completions.

## What It Checks

`make v4-chat-agent-smoke` runs a small local harness against a live shim:

- `stream_text`: streamed Chat response must reconstruct exactly `HELLO`.
- `read_file`: the model must call `read_file`, consume the tool result, and
  finish with the expected marker.
- `basic_patch`: the model must edit one file through Chat function tools.
- `bugfix_go`: the model must inspect Go files, fix a failing test, call
  `run_command` with `go test ./...`, and finish only after the test passes.
- `multi_file`: the model must coordinate edits across two files.

The harness executes only its local allowlisted tools:

- `list_files`
- `read_file`
- `write_file`
- `replace_text`
- `run_command` for `go test ./...` and `go test ./... -v`

## Run

Start the shim first, then run:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  MODEL=gpu/qwen3-30b-instruct \
  SHIM_AUTH_HEADER="Authorization: Bearer $GW_API_KEY" \
  make v4-chat-agent-smoke
```

To run a smaller set:

```bash
CHAT_AGENT_SMOKE_SCENARIOS=stream_text,read_file,basic_patch \
  MODEL=gpu/qwen3-30b-instruct \
  make v4-chat-agent-smoke
```

## Inputs

| Variable | Default | Meaning |
| --- | --- | --- |
| `SHIM_BASE_URL` | `http://127.0.0.1:8080` | Live shim base URL. |
| `MODEL` / `CHAT_AGENT_MODEL` | `devstack-model` | Public model alias sent to Chat Completions. |
| `SHIM_AUTH_HEADER` | unset | Full ingress auth header, if the shim requires one. |
| `GW_API_KEY` / `OPENAI_API_KEY` | unset | Fallback bearer token sources when `SHIM_AUTH_HEADER` is unset. |
| `CHAT_AGENT_SMOKE_SCENARIOS` | `all` | Comma-separated scenario list or `all`. |
| `CHAT_AGENT_SMOKE_ARTIFACT_DIR` | `.tmp/v4-chat-agent-smoke` | Artifact root. |
| `CHAT_AGENT_SMOKE_RUN_ID` | UTC timestamp | Optional deterministic run id. |

## Artifacts

Each run writes:

```text
.tmp/v4-chat-agent-smoke/<model>_<timestamp>/
```

The directory contains request and response JSON for each turn, tool outputs,
scenario workspaces, final text markers, and `summary.json`.

## Current Evidence

Operator runs on May 20, 2026:

| Model | Artifact | Result | Notes |
| --- | --- | --- | --- |
| `gpu/qwen3-coder30b-q5km` | `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder30b-q5km_20260520T134047Z` | Passed | Covered stream text, file read, single-file edit, Go bugfix with real `go test ./...`, and multi-file edit. |
| `gpu/qwen3-coder30b-q5km` | `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder30b-q5km_20260520T175840Z` | Passed | Post V4 Chat Compatibility Layer run; full scenario set passed after trace/structured-output/provider-form hardening. |
| `gpu/qwen3-coder-30b` | `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder-30b_20260520T194113Z` | Passed | Full Chat-agent scenario set passed; keep separate from its retry-dependent Codex certification status. |
| `gpu/qwen3-coder-next` | `.tmp/v4-chat-agent-smoke/gpu-qwen3-coder-next_20260520T210116Z` | Passed | Full Chat-agent scenario set passed; Codex certification evidence is still pending. |
| `gpu/glm47-flash-opus-reasoning` | `.tmp/v4-chat-agent-smoke/gpu-glm47-flash-opus-reasoning_20260520T194326Z` | Passed | Full Chat-agent scenario set passed; keep separate from its blocked Codex baseline status. |
| `gpu/qwen3-30b-instruct` | `.tmp/v4-chat-agent-smoke/gpu-qwen3-30b-instruct_20260520T134315Z` | Passed | Same full scenario set passed. |
| `gpu/qwen3-next-instruct` | `.tmp/v4-chat-agent-smoke/gpu-qwen3-next-instruct_20260520T213404Z` | Passed | Full Chat-agent scenario set passed; OpenCode smoke failed separately. |

The first Qwen Coder run exposed raw pseudo-tool markup in Chat assistant
content (`<function=...>`). The shim now applies the existing raw-markup repair
path to non-stream Chat Completions requests with tools, so that output is
re-asked instead of being returned as normal assistant text.

## Interpretation

A pass means the model and shim can complete a small Chat-first coding-agent
tool loop. It does not mean the model is Codex-ready; Codex uses the
Responses-native tool loop and stricter patch behavior.

Common failures:

- `stream_text` mismatch: native Chat streaming or exact short-answer behavior
  is not stable enough for this smoke.
- final text instead of a tool call: the model is weak for Chat-first coding
  tools, even if plain chat works.
- missing required command: the model edited files but skipped the test command
  and claimed completion from reasoning alone.
- malformed tool arguments: the model or compatibility cleanup needs
  provider/model-specific hardening before it should be recommended.
- `bugfix_go` command failure: inspect the scenario workspace and
  `turn-*.tool-*.json` artifacts to distinguish a bad edit from a bad command
  call.

## Relationship To Other Gates

- External OpenAI-compatible tester: validates API surface behavior. It can be
  green while this smoke fails on real coding-agent workflow.
- Codex eval and model certification: validate Codex/Responses suitability.
  A model can pass this Chat smoke and still fail Codex tool-loop requirements.
- Provider matrix smoke: validates configured provider aliases and operator
  readiness. It does not test coding-agent file editing.

Use this smoke when deciding whether a model is worth trying in Chat-first
coding clients before spending time on heavier Codex certification.
