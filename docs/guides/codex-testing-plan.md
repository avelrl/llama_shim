# Codex Testing Plan

Use this plan when testing real Codex CLI against `llama_shim` and a
non-OpenAI or OpenAI-compatible upstream. The goal is to avoid one huge
ambiguous task and instead prove one capability layer at a time.

Docs checked on 2026-04-27:

- OpenAI Codex config reference: custom `model_provider`,
  `model_providers.<id>.base_url`, `env_key`, `wire_api`,
  `supports_websockets`, model context, skills, apps, and approval/sandbox
  settings.
- OpenAI Responses streaming guide: `stream=true` uses SSE semantic events,
  including `response.created`, deltas, `response.completed`, and `error`.
- OpenAI function-call streaming guide: streamed function calls arrive as
  `response.output_item.added`, argument deltas, and
  `response.function_call_arguments.done`.
- OpenAI local shell guide: local shell commands are client-executed; the API
  returns instructions, and the client/runtime executes and returns outputs.

## Rule

Do not advance to the next phase after an ambiguous failure. First classify the
failure as one of:

- Codex config/auth problem
- shim request/response bridge problem
- upstream HTTP/SSE stability problem
- upstream tool-following problem
- model quality/context-budget problem
- local Codex tool execution problem

If the cause is not clear, keep the task small and collect logs before changing
more than one variable.

## Environment

Use placeholders and keep local secrets out of notes:

```bash
export SHIM_BASE_URL=http://127.0.0.1:8080
export CODEX_MODEL=Kimi-K2.6
export CODEX_PROVIDER=gateway-shim
export GW_API_KEY=shim-dev-key
export CODEX_TEST_DIR=.codex-smoke-workspace
```

## Automated Real-Upstream Smoke

Use this before or alongside the manual phases when the shim is already running
against a real upstream. It creates an isolated `CODEX_HOME`, writes a temporary
custom Codex provider, runs `codex exec --json`, and verifies local workspace
results:

```bash
SHIM_BASE_URL="$SHIM_BASE_URL" \
CODEX_MODEL="$CODEX_MODEL" \
CODEX_PROVIDER="$CODEX_PROVIDER" \
CODEX_API_KEY_ENV=GW_API_KEY \
GW_API_KEY="$GW_API_KEY" \
make codex-cli-real-upstream-smoke
```

The smoke waits for shim process liveness with `/healthz`, then probes
`/v1/models` with the same bearer key that Codex will use. It intentionally does
not block on `/readyz`: real upstream gateways can require request auth while
`/readyz` is a terse unauthenticated operator probe.

Default cases:

- `boot`: plain Codex response through the custom provider.
- `read`: local command execution reads a seed file.
- `write`: local command execution updates one seed file.
- `bugfix`: fixes a tiny Go bug and verifies `go test ./...`.

To bisect a failure without changing prompts, narrow the case list:

```bash
CODEX_REAL_SMOKE_CASES=boot,read \
make codex-cli-real-upstream-smoke
```

Important knobs:

| Variable | Default | Purpose |
| --- | --- | --- |
| `CODEX_BASE_URL` | `$SHIM_BASE_URL/v1` | Codex provider base URL. |
| `CODEX_API_KEY_ENV` | `GW_API_KEY` | Environment variable name used by the generated Codex provider. |
| `CODEX_API_KEY` | unset | Optional direct key value if the named env var is not already set. |
| `CODEX_REAL_SMOKE_WORKDIR` | `.tmp/codex-real-upstream-smoke` | Disposable smoke workspace and logs. |
| `CODEX_REAL_SMOKE_CASE_ATTEMPTS` | `2` | Retry each selected real-upstream case with a fresh workspace. Set `1` for strict no-retry debugging. |
| `CODEX_REAL_SMOKE_REASONING_EFFORT` | `minimal` | Reasoning effort used by automated tiny tasks. Raise to `high` only when testing that model mode. |
| `CODEX_REAL_SMOKE_WEBSOCKETS` | `false` | Keep HTTP-first unless intentionally testing WS. |
| `CODEX_REAL_SMOKE_UNIFIED_EXEC` | `true` | Use Codex unified exec command tools. Set `false` to test fallback shell mode. |

Pass criteria:

- all selected cases emit Codex JSON events and complete a turn
- read/write/bugfix cases show local command execution events
- write and bugfix cases pass filesystem/test verification after Codex exits
- repeated raw tool-call markup or missing sentinel answers fail after the
  configured attempts

This smoke does not prove the full devstack matrix and does not claim exact
OpenAI hosted tool choreography. It is the practical real-upstream gate for
whether the current model/provider pair is useful enough for Codex coding work.

Create a disposable workspace:

```bash
mkdir -p "$CODEX_TEST_DIR"
cd "$CODEX_TEST_DIR"
git init
```

For normal manual debugging, start Codex over HTTP first:

```toml
model = "Kimi-K2.6"
model_provider = "gateway-shim"
approval_policy = "never"
sandbox_mode = "workspace-write"

[model_providers.gateway-shim]
name = "gateway-shim"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
env_key = "GW_API_KEY"
supports_websockets = false
```

Only enable `supports_websockets = true` after the same model passes the HTTP
phases.

For stuck-turn diagnostics, temporarily use:

```yaml
log:
  level: debug
  file_path: ./.data/shim.log
```

Return to `info` after capture. Debug mode can include request/response body
previews from generic HTTP middleware.

## Phase 0: Shim And Upstream Sanity

This phase proves that the shim is reachable and the upstream can answer through
the same route Codex will use.

Check the model list:

```bash
curl -fsS "$SHIM_BASE_URL/v1/models" \
  -H "Authorization: Bearer $GW_API_KEY" \
  | jq '.'
```

Check shim capabilities:

```bash
curl -fsS "$SHIM_BASE_URL/debug/capabilities" \
  -H "Authorization: Bearer $GW_API_KEY" \
  | jq '.surfaces.responses'
```

Direct text response:

```bash
curl -fsS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $GW_API_KEY" \
  -d "{
    \"model\": \"$CODEX_MODEL\",
    \"input\": \"Reply with OK only.\",
    \"stream\": false
  }" | jq '.output_text // .'
```

Direct required function-call response:

```bash
curl -fsS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $GW_API_KEY" \
  -d "{
    \"model\": \"$CODEX_MODEL\",
    \"tool_choice\": \"required\",
    \"input\": \"Call the run_command tool with {\\\"cmd\\\":\\\"pwd\\\"}.\",
    \"tools\": [
      {
        \"type\": \"function\",
        \"name\": \"run_command\",
        \"description\": \"Run one local command.\",
        \"parameters\": {
          \"type\": \"object\",
          \"properties\": {
            \"cmd\": {\"type\":\"string\"}
          },
          \"required\": [\"cmd\"],
          \"additionalProperties\": false
        }
      }
    ]
  }" | jq '.output'
```

Pass criteria:

- `/v1/models` works with the same key Codex will use.
- Direct text response completes.
- Required function-call response produces a function/tool call, not only text.

Stop criteria:

- `401` or `403`: fix shim ingress auth or Codex `env_key`.
- `400 Unsupported tool type`: add or adjust model-scoped disabled tool
  compatibility before testing Codex.
- No function/tool call when `tool_choice=required`: this model/upstream is not
  ready for Codex coding tasks yet.
- Streaming EOF before a terminal event: collect shim debug logs and retry once
  before changing task size.

## Phase 1: Codex Boot

This phase proves Codex can load the provider config, authenticate, and get a
plain answer.

```bash
GW_API_KEY="$GW_API_KEY" \
codex exec \
  --skip-git-repo-check \
  -C "$CODEX_TEST_DIR" \
  -m "$CODEX_MODEL" \
  -c "model_provider=\"$CODEX_PROVIDER\"" \
  -c 'model_reasoning_effort="high"' \
  -c 'model_reasoning_summary="none"' \
  'Reply with OK only.'
```

Pass criteria:

- Codex reaches the shim and prints a final answer.
- No `Model metadata not found` warning, or the warning is already understood
  and accepted for this run.
- No WebSocket errors when the provider is configured HTTP-first.

Stop criteria:

- Codex still connects to OpenAI sideband endpoints for apps/connectors but
  shim `/v1` traffic is absent: re-check custom provider config.
- `Model metadata not found`: add/update
  `responses.codex.model_metadata.models[]` for this model if context/tool
  decisions are unstable.
- Context-window errors on a tiny prompt: reduce enabled Codex features or use a
  larger-context upstream.

## Phase 2: Read-Only Local Tools

This phase proves Codex can request local command execution and continue after
tool output.

Prompt:

```text
Run pwd, then list the current directory, then reply with the two command
results in one short sentence.
```

Expected behavior:

- Codex runs `pwd`.
- Codex runs a directory listing command.
- Codex prints a final answer after command output.

Pass criteria:

- Shim logs show at least one tool/function event.
- Codex shows local command execution.
- The final answer reflects tool output.

Stop criteria:

- `saw_tool_event=false`: the upstream did not emit a tool call.
- Tool event is present in shim logs but Codex does not execute it: inspect
  Codex tool mode and provider metadata.
- Command executes but no final answer: check whether
  `force_tool_choice_required` is too aggressive for this model.

## Phase 3: Single-File Write

This is the first write test. Keep it intentionally trivial.

Prompt:

```text
Create a file named hello.txt in the current directory with exactly this
content:

Hello

Then read the file back and reply done.
```

Manual verification:

```bash
test "$(cat "$CODEX_TEST_DIR/hello.txt")" = "Hello"
git -C "$CODEX_TEST_DIR" diff -- hello.txt
```

Pass criteria:

- `hello.txt` exists.
- The file content is exactly `Hello`.
- Codex reads the file back or otherwise verifies the change.

Stop criteria:

- Codex says it is starting implementation but no file appears: inspect shim
  `responses stream summary` for `saw_tool_event`.
- File is created but wrong content: model quality issue; retry with a stricter
  prompt once, then compare another model.
- Codex cannot write due sandbox: fix Codex `sandbox_mode`, workspace trust, or
  working directory.

## Phase 4: One Existing File Edit

Create a small seed file:

```bash
printf 'alpha\n' > "$CODEX_TEST_DIR/notes.txt"
```

Prompt:

```text
Update notes.txt so it contains exactly two lines:
alpha
beta

After editing, run cat notes.txt and reply done.
```

Manual verification:

```bash
printf 'alpha\nbeta\n' | diff -u - "$CODEX_TEST_DIR/notes.txt"
```

Pass criteria:

- Codex edits an existing file instead of recreating unrelated structure.
- Codex verifies by reading the file or showing the diff.

## Phase 5: Tiny Deterministic Code Task

Use a tiny project with a focused test. Example Go task:

```bash
cat > "$CODEX_TEST_DIR/go.mod" <<'EOF'
module codexsmoke

go 1.24
EOF

cat > "$CODEX_TEST_DIR/mathutil.go" <<'EOF'
package codexsmoke

func Add(a, b int) int {
	return a - b
}
EOF

cat > "$CODEX_TEST_DIR/mathutil_test.go" <<'EOF'
package codexsmoke

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
EOF
```

Prompt:

```text
Fix the failing Go test in this directory. Keep the change minimal. Run
go test ./... and summarize the result.
```

Pass criteria:

- Codex inspects the files.
- Codex changes only the bug.
- `go test ./...` passes.
- The final answer mentions the focused test result.

Stop criteria:

- Codex rewrites unrelated files: reduce prompt scope and compare model.
- Codex does not run tests: prompt may be too weak or model tool persistence is
  poor.
- Test command hangs or requires network: change the task to an offline unit
  test.

## Phase 6: Two-File Task

Only after phases 0-5 pass, test a bounded multi-file task.

Prompt shape:

```text
Make this exact two-file change:

1. Add a small helper function in <file A>.
2. Add one unit test for it in <file B>.

Do not touch other files. Run the focused test command. If you cannot complete
it, stop and explain the blocker.
```

Pass criteria:

- Edits are limited to the named files.
- Codex uses tools repeatedly without losing state.
- The focused test passes.

## Phase 7: Negative Probes

Run these only after positive phases pass. They are for boundary classification,
not for proving usefulness.

Impossible task:

```text
Implement the missing service, but if required source files are absent, stop and
say exactly what is missing. Do not create a full replacement project.
```

Oversized task:

```text
Plan how you would implement this larger change, but do not edit files yet.
Limit the plan to five concrete steps and name the first file you would inspect.
```

Unsupported-tool probe:

```text
Use only local shell/file editing tools. Do not use image generation, web search,
or remote connectors.
```

Pass criteria:

- Codex stops or asks for missing context instead of fabricating a large system.
- Shim rejects or filters unsupported passive tools according to config.
- Failures are typed and explainable.

## Model Matrix

Use the same phases for every candidate model. Do not compare models using
different prompts.

| Model | Direct text | Required function call | Codex boot | Read-only tools | Write file | Tiny code+test | Failure reason |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `<model>` | pass/fail | pass/fail | pass/fail | pass/fail | pass/fail | pass/fail | classify |

Notes:

- A model that passes direct text but fails required function call is a chat
  model for this shim, not a Codex coding model.
- A model that passes direct required function call but fails Codex boot usually
  needs Codex metadata, disabled-tool compatibility, or context-budget tuning.
- A model that runs commands but cannot finish final answers may need
  `force_tool_choice_required` disabled for normal chat and enabled only for
  deterministic coding smokes.

## Log Triage

Use shim logs first:

```bash
rg 'responses upstream|responses stream|Unsupported tool|ContextWindow|tool_choice|saw_tool_event' .data/shim.log
```

Key fields:

- `responses upstream request started`: model, stream flag, tool choice, tool
  types, input shape, and body size.
- `responses upstream response headers`: upstream status and time to headers.
- `responses stream first upstream line`: whether SSE began at all.
- `responses stream event`: whether tool/function/text/completed events arrived.
- `responses stream summary`: `saw_tool_event`, tool event counters, text
  length, and terminal event status.
- `responses upstream request failed`: upstream connection/setup failure.

Common diagnosis:

| Symptom | Likely layer | First action |
| --- | --- | --- |
| `401` from shim | config/auth | Check `env_key`, bearer token, and shim auth mode. |
| WebSocket error while HTTP was intended | Codex provider | Set `supports_websockets=false` for the provider. |
| `Unsupported tool type: ImageGeneration` | upstream capability | Add model-scoped disabled tool compatibility. |
| `Unsupported tool type: NamespaceTool` | upstream capability | Disable `namespace` for that model unless intentionally testing it. |
| Context-window failure on tiny task | metadata/context | Fix `responses.codex.model_metadata` or switch models. |
| Preamble then no file | upstream tool-following/SSE | Check `saw_tool_event` and terminal stream event. |
| Tool event in shim but no local command | Codex bridge | Check Codex tool mode and model metadata. |
| Command ran but no final answer | model/tool loop | Check `force_tool_choice_required` and retry a read-only prompt. |

## When To Expand Task Size

Only expand after the previous phase passes twice or passes once and leaves a
clear, understood log trail.

Do not ask for broad tasks like:

```text
Implement an operating system.
```

Prefer bounded tasks:

```text
Inspect these two files. Make one minimal fix. Run this exact test command.
Stop if any required context is missing.
```

This keeps failures attributable. With OpenAI-compatible upstreams, Codex
success depends on both shim compatibility and upstream model behavior; a broad
task hides that distinction.
