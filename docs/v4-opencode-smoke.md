# V4 OpenCode Smoke

Last updated: May 20, 2026.

Status: first slice implemented. This is a V4 client-integration smoke for a
locally installed `opencode` CLI talking to the shim. It is not an
OpenAI API parity claim, not a model benchmark, and not a replacement for
[V4 Chat Agent Smoke](v4-chat-agent-smoke.md) or Codex eval.

## Source Check

Checked against OpenCode docs on May 20, 2026:

- <https://opencode.ai/docs/cli/>
- <https://opencode.ai/docs/config/>
- <https://opencode.ai/docs/providers/>

The implementation plan uses these documented OpenCode surfaces:

- `opencode run [message..]` for non-interactive execution.
- `opencode run --model <provider/model>` to choose a model.
- `opencode run --dir <workspace>` to run against a specific workspace.
- `opencode run --format json` for raw JSON event output.
- `opencode run --dangerously-skip-permissions` only inside an isolated smoke
  workspace.
- `OPENCODE_CONFIG` / `OPENCODE_CONFIG_DIR` / `OPENCODE_CONFIG_CONTENT` to keep
  the smoke away from the operator's global OpenCode config.
- Custom providers with `@ai-sdk/openai-compatible`, `options.baseURL`,
  `options.apiKey`, optional headers, and explicit model entries.
- `OPENCODE_DISABLE_MODELS_FETCH`, `OPENCODE_DISABLE_DEFAULT_PLUGINS`, and
  `OPENCODE_DISABLE_CLAUDE_CODE` as useful isolation toggles.

## Purpose

[V4 Chat Agent Smoke](v4-chat-agent-smoke.md) proves the shim can support the
Chat Completions tool loop in a controlled harness. The OpenCode smoke should
prove the next layer: a real Chat-first coding-agent client can use the shim to
edit files and run tests.

The first slice should answer one practical question:

> Can local `opencode run` use the shim as an OpenAI-compatible provider,
> modify a small project, and leave the workspace in a verified passing state?

## Scope

First implementation slice:

- assumes `opencode` is already installed locally
- assumes a live shim is reachable at `SHIM_BASE_URL`
- creates an isolated artifact directory under `.tmp/v4-opencode-smoke/`
- writes a temporary OpenCode config
- writes a temporary workspace with a small Go bug
- runs `opencode run` non-interactively
- verifies files and `go test ./...` locally after OpenCode exits
- stores stdout, stderr, OpenCode JSON output, generated config, workspace
  before/after state, local verification output, and `summary.json`

Out of scope for the first slice:

- installing or upgrading OpenCode
- using the operator's global OpenCode config or auth store
- benchmarking model quality
- testing every OpenCode tool
- matching Codex eval profiles
- testing OpenCode server, web UI, ACP, GitHub, GitLab, or share flows

## Model Alias Shape

OpenCode's user-facing model selector is documented as `provider/model`.
The shim also uses public aliases like `gpu/qwen3-coder30b-q5km`.

The first runner keeps this visible instead of hiding it:

- generated OpenCode provider id: `llama-shim`
- generated OpenCode model key: the requested shim public alias
- generated OpenCode selected model: `llama-shim/<shim-public-alias>`

If OpenCode or the AI SDK rewrites that selected model differently before
sending `/v1/chat/completions`, the run artifacts and shim logs should be used
to decide whether a temporary no-slash shim alias or a different generated
provider layout is needed.

## Planned Command

Run:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  OPENCODE_SMOKE_MODEL=gpu/qwen3-coder30b-q5km \
  make v4-opencode-smoke
```

Optional inputs:

| Variable | Default | Meaning |
| --- | --- | --- |
| `SHIM_BASE_URL` | `http://127.0.0.1:8080` | Live shim URL. |
| `SHIM_AUTH_HEADER` | unset | Full shim ingress auth header, if enabled. |
| `SHIM_API_KEY` / `GW_API_KEY` / `OPENAI_API_KEY` | unset | Fallback bearer token sources for generated OpenCode config. |
| `OPENCODE_BIN` | `opencode` | Local OpenCode binary. |
| `OPENCODE_SMOKE_MODEL` / `MODEL` | `gpu/qwen3-coder30b-q5km` | Shim public model alias to test. |
| `OPENCODE_SMOKE_SCENARIO` | `bugfix_go` | Scenario name for the first slice. |
| `OPENCODE_SMOKE_ARTIFACT_DIR` | `.tmp/v4-opencode-smoke` | Artifact root. |
| `OPENCODE_SMOKE_RUN_ID` | UTC timestamp | Optional deterministic run id. |
| `OPENCODE_SMOKE_READY_ATTEMPTS` | `60` | Number of one-second shim readiness attempts. |

## Generated OpenCode Config

The runner generates a config similar to this, then passes it through
`OPENCODE_CONFIG`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "enabled_providers": ["llama-shim"],
  "model": "llama-shim/<model>",
  "small_model": "llama-shim/<model>",
  "provider": {
    "llama-shim": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "llama_shim",
      "options": {
        "baseURL": "http://127.0.0.1:8080/v1",
        "apiKey": "{env:OPENCODE_SHIM_API_KEY}"
      },
      "models": {
        "<model>": {
          "name": "<model>",
          "limit": {
            "context": 32768,
            "output": 4096
          }
        }
      }
    }
  }
}
```

The current runner uses bearer-style API key config. If shim ingress auth is
configured as a non-bearer custom header, extend the generated config with
`options.headers`.

## Scenario: `bugfix_go`

Workspace fixture:

```text
go.mod
calc.go
calc_test.go
```

Initial bug:

```go
func Add(a, b int) int {
	return a - b
}
```

OpenCode prompt:

```text
Fix the failing Go test in this workspace. Inspect the files, edit the code,
run go test ./..., and stop when the test passes. Do not change the test.
```

Verification after `opencode run`:

- `calc.go` contains `return a + b`
- `go test ./...` exits 0 in the workspace
- `calc_test.go` was not modified, unless a future scenario explicitly allows
  test edits
- summary records the OpenCode exit code and local verification result

## Shim Compatibility Dependency

OpenCode uses streaming Chat Completions for its agent loop. Some local and
OpenAI-compatible coding models print pseudo tool markup as assistant text,
for example:

```text
<function=bash>
<parameter=command>
find . -name "*.go" -type f
</parameter>
</function>
</tool_call>
```

For requests that include Chat `tools`, the shim normalizes this streamed
markup into `delta.tool_calls` before forwarding the SSE chunk to OpenCode.
The normalizer is intentionally scoped to tool-enabled Chat streams; ordinary
text-only streams are not converted.

## Runner Flow

1. Check `opencode --version`.
2. Check shim `/readyz`.
3. Create `.tmp/v4-opencode-smoke/<model>_<timestamp>/`.
4. Generate OpenCode config and isolated workspace.
5. Run:

```bash
OPENCODE_CONFIG=<run-dir>/opencode.json \
OPENCODE_CONFIG_DIR=<run-dir>/opencode-config \
OPENCODE_DISABLE_MODELS_FETCH=1 \
OPENCODE_DISABLE_DEFAULT_PLUGINS=1 \
OPENCODE_DISABLE_CLAUDE_CODE=1 \
opencode run \
  --dir <workspace> \
  --model <generated-provider/model> \
  --format json \
  --dangerously-skip-permissions \
  "Fix the failing Go test..."
```

6. Capture stdout, stderr, exit status, generated config, and workspace state.
7. Run local verification commands independent of OpenCode's final text.
8. Write `summary.json` and a short `summary.md`.

## Interpretation

Passing this smoke means:

- OpenCode can load the generated shim provider config.
- The shim's Chat Completions projection is compatible enough for OpenCode's
  real client behavior.
- The selected model can complete one real coding-agent workflow through
  OpenCode.

Failing this smoke needs classification:

- `opencode_config_error`: generated config or provider/model naming issue.
- `opencode_transport_error`: OpenCode could not call the shim or upstream.
- `shim_chat_contract_error`: shim rejected or repaired upstream Chat output.
- `model_tool_loop_error`: model did not use tools/edits/tests correctly.
- `workspace_verification_error`: OpenCode exited but the files or tests did
  not match the scenario contract.

## Relationship To Existing Gates

- [V4 Chat Agent Smoke](v4-chat-agent-smoke.md) is the deterministic unit-style
  harness. Keep it as the first debugging step.
- V4 OpenCode smoke is the real-client integration layer.
- Codex eval remains the Codex-specific gate and should not be inferred from
  OpenCode results.
- The external OpenAI-compatible tester remains the API-surface gate and does
  not prove coding-agent usefulness.

Implementation entrypoints:

- `make v4-opencode-smoke`
- `scripts/v4-opencode-smoke.sh`
