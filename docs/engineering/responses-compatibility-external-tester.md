# Responses Compatibility External Tester

Last updated: April 26, 2026.

Status: repo-owned runner and Broad subset profile are in place. This is an
engineering runbook, not a stronger hosted-parity claim.

## Source Check

This runbook was checked against the local docs index in
[`openapi/llms.txt`](../../openapi/llms.txt), the OpenAI Docs MCP, and the
official OpenAI docs on April 26, 2026.

Relevant official pages:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Responses API reference](https://platform.openai.com/docs/api-reference/responses)
- [Chat Completions API reference](https://platform.openai.com/docs/api-reference/chat/create)

The docs-backed baseline is:

- `POST /v1/responses` creates a Response object.
- Response objects are stored by default upstream and can be retrieved; upstream
  storage can be disabled with `store: false`.
- `previous_response_id` and `conversation` are the state-carrying mechanisms
  for follow-up turns.
- `stream: true` uses semantic SSE events such as `response.created`,
  `response.output_text.delta`, and `response.completed`.
- `/v1/responses/{response_id}/input_items` is the API surface for listing the
  items used to generate a stored response.

## Latest Real-Upstream Ledger

Last real-upstream check: April 26, 2026.

Configuration:

- harness mode: `real-upstream`
- tester mode: `strict`
- tester profile: `llama-shim-kimi-k2.6`
- tester config paths supplied through `TESTER_MODELS`, `TESTER_SUITE`,
  `TESTER_CAPABILITIES`, and `TESTER_PROFILE`
- artifact root: `.data/responses-compat-external/20260426T150735Z`

Result:

- `strict` passed through the shim against the configured real upstream
- Responses, Conversations, stored state, streaming, structured output,
  function/custom tools, constrained custom tools, and compaction cases passed
- `/readyz` was `503` during the run, but this was capture-only by design in
  `real-upstream` mode; `/healthz` and ordinary `/v1/*` request paths worked

The same day, `compat` mode completed without timeout but reported one
non-core Chat Completions failure in `chat.tool_call`. The first forced
tool-call turn produced a tool call, while the follow-up after the tool result
returned `finish_reason: "length"` with `message.content: null`. Treat this as
an upstream/model budget edge for the broader Chat profile, not as a failure of
the V3 Responses `Broad subset` gate. Use `strict` as the current gate for the
Responses compatibility claim.

## Goal

The external tester should validate observable OpenAI-compatible behavior for
the shim's current `Broad subset` Responses claim. It should not test internal
backend contracts, fixture implementation details, or exact hosted behavior
that the shim explicitly does not claim.

The repo-owned entrypoint is:

```bash
make responses-compat-external-smoke
```

This command runs [`scripts/responses-compat-external-smoke.sh`](../../scripts/responses-compat-external-smoke.sh).
It captures `/readyz` and `/debug/capabilities` every time, then optionally
runs an external tester command.

## Run Modes

The runner has two explicit harness modes:

- `devstack-fixture`: deterministic local fixture mode. This is the default
  for `make responses-compat-external-smoke` and is useful for transport,
  storage, replay, SSE, and capability-manifest checks.
- `real-upstream`: operator-owned mode for a shim that is already connected to
  a real OpenAI-compatible backend such as vLLM, SGLang, llama.cpp, or OpenAI.
  Use `make responses-compat-external-real-smoke` for this path.

The runner cannot infer `llama.base_url` from public probes. In `real-upstream`
mode, set `RESPONSES_COMPAT_EXPECTED_UPSTREAM` to record the intended upstream
base URL as an operator assertion in the run artifacts.

`devstack-fixture` mode requires `/readyz` to return 2xx. `real-upstream` mode
waits only for `/healthz` and captures `/readyz` as evidence by default because
many OpenAI-compatible gateways do not expose a health probe that satisfies the
shim's backend readiness check while ordinary `/v1/*` requests still work.
Override this with `RESPONSES_COMPAT_REQUIRE_READYZ=1` when a real-upstream run
must be gated on backend readiness.

Devstack fixture capture-only preflight:

```bash
make devstack-up
make responses-compat-external-smoke
make devstack-down
```

Devstack fixture strict external tester run:

```bash
make devstack-up
RESPONSES_COMPAT_REQUIRE_TESTER=1 \
RESPONSES_COMPAT_TESTER_CMD='<external tester command>' \
make responses-compat-external-smoke
make devstack-down
```

Real-upstream capture-only preflight against an already running shim:

```bash
RESPONSES_COMPAT_EXPECTED_UPSTREAM=http://127.0.0.1:8000 \
make responses-compat-external-real-smoke
```

Real-upstream strict external tester run:

```bash
RESPONSES_COMPAT_EXPECTED_UPSTREAM=http://127.0.0.1:8000 \
RESPONSES_COMPAT_REQUIRE_TESTER=1 \
RESPONSES_COMPAT_TESTER_CMD='<external tester command>' \
make responses-compat-external-real-smoke
```

The command string is intentionally owned by the operator or CI job because
external tester CLIs differ. The runner provides stable environment variables
instead of baking in one third-party CLI contract.

In `real-upstream` mode, the runner does not synthesize an `OPENAI_API_KEY`.
Leave `OPENAI_API_KEY` unset when the external tester should load credentials
from its own `.env`; set it explicitly only when the harness should pass a
specific key through the process environment. `devstack-fixture` mode keeps the
`shim-test-key` default for unauthenticated local smoke runs.

## Running openai-compatible-tester

Use this flow when validating a real shim process against the external
`openai-compatible-tester`.

1. Start the shim in one terminal:

```bash
export SHIM_CONFIG=<shim-config.yaml>
export SHIM_BASE_URL=http://127.0.0.1:8080

CONFIG="$SHIM_CONFIG" make run
```

Equivalent direct command:

```bash
go run ./cmd/shim -config "$SHIM_CONFIG"
```

Keep this process running while the external tester runs. For the default
local config, the shim base URL is usually `http://127.0.0.1:8080`; for the
devstack fixture it is usually `http://127.0.0.1:18080`.

Before running the tester, check the shim from another terminal:

```bash
curl -fsS "$SHIM_BASE_URL/healthz"
curl -sS "$SHIM_BASE_URL/readyz"
curl -sS "$SHIM_BASE_URL/debug/capabilities"
```

If the shim config uses a real upstream gateway, record that expected upstream
URL separately; the harness cannot infer it from public shim probes.

2. In another terminal, define only local runtime paths and URLs:

```bash
export TESTER_DIR=<path-to-openai-compatible-tester>
export SHIM_BASE_URL=http://127.0.0.1:8080
export RESPONSES_COMPAT_EXPECTED_UPSTREAM=<upstream-base-url>
export TESTER_MODELS=configs/models_llama_shim.yaml
export TESTER_SUITE=configs/suite_llama_shim.yaml
export TESTER_CAPABILITIES=configs/capabilities_llama_shim.yaml
export TESTER_PROFILE=llama-shim-kimi-k2.6
```

Do not commit real keys or absolute local paths. In `real-upstream` mode the
harness leaves `OPENAI_API_KEY` unset, so the tester can load credentials from
its own `.env`. If a run must use an explicit process-level key, export
`OPENAI_API_KEY` locally before the command and keep it out of artifacts and
docs.

3. Run the strict tester profile through the harness:

```bash
RESPONSES_COMPAT_REQUIRE_TESTER=1 \
RESPONSES_COMPAT_TESTER_CMD='cd "$TESTER_DIR" && go run . --no-tui --models "$TESTER_MODELS" --suite "$TESTER_SUITE" --capabilities "$TESTER_CAPABILITIES" --profile "$TESTER_PROFILE" --mode strict --out-dir "$RESPONSES_COMPAT_ARTIFACT_DIR/openai-compatible-tester" --json' \
make responses-compat-external-real-smoke
```

The harness exports `SHIM_BASE_URL`, `OPENAI_BASE_URL`, capability paths, and
artifact paths to the tester command. The tester's `--out-dir` should stay
under `$RESPONSES_COMPAT_ARTIFACT_DIR` so each run keeps the shim probes,
tester logs, and tester report together.

4. Read the result:

- `==> external Responses compatibility tester passed` means the harness and
  tester command both exited successfully.
- `tester.stdout.log` and `tester.stderr.log` contain the external tester
  output.
- `tester.exitcode` contains the tester process exit code.
- `readyz.status`, `readyz.json`, `capabilities.status`, and
  `capabilities.json` show what the shim advertised before the tester ran.
- The tester report lives under
  `.data/responses-compat-external/<run-id>/openai-compatible-tester...`.

If `readyz.status` is non-2xx in `real-upstream` mode, do not treat that alone
as a failed tester verdict. This mode only requires `/healthz` by default
because gateway readiness probes can be stricter than the ordinary `/v1/*`
request path. Set `RESPONSES_COMPAT_REQUIRE_READYZ=1` when readiness must be a
hard gate.

Do not interpret a `devstack-fixture` run with a real-model profile as a real
Qwen, GPT, vLLM, SGLang, llama.cpp, or OpenAI compatibility verdict. The runner
writes `harness-warnings.txt` when the profile or command appears
real-model-specific while the mode is still `devstack-fixture`.

## Environment Contract

| Variable | Default | Meaning |
| --- | --- | --- |
| `SHIM_BASE_URL` | `http://127.0.0.1:18080` | Shim root used for `/readyz` and `/debug/capabilities`. |
| `OPENAI_BASE_URL` | `$SHIM_BASE_URL/v1` | OpenAI-compatible base URL passed to the external tester. |
| `OPENAI_API_KEY` | `shim-test-key` in devstack mode, unset in real-upstream mode | API key passed to SDK-style testers when explicitly configured. |
| `SHIM_AUTH_HEADER` | empty | Optional auth header for shim-owned probe endpoints. |
| `RESPONSES_COMPAT_RUN_MODE` | `devstack-fixture` | Harness mode: `devstack-fixture` or `real-upstream`. |
| `RESPONSES_COMPAT_EXPECTED_UPSTREAM` | `devstack-fixture` in devstack mode, empty otherwise | Operator assertion for the upstream behind the shim. |
| `RESPONSES_COMPAT_PROFILE` | `responses-broad-subset` | Profile name for tester-side filtering. |
| `RESPONSES_COMPAT_TESTER_CMD` | empty | Shell command used to run the external tester. |
| `OPENAI_COMPAT_TESTER_CMD` | empty | Backward-compatible alias for the tester command. |
| `RESPONSES_COMPAT_REQUIRE_READYZ` | `1` in devstack mode, `0` in real-upstream mode | Whether non-2xx `/readyz` blocks the run. |
| `RESPONSES_COMPAT_REQUIRE_TESTER` | `0` | When `1` or `true`, missing tester command is a failure. |
| `RESPONSES_COMPAT_ARTIFACT_DIR` | `.data/responses-compat-external` | Artifact root. |
| `RESPONSES_COMPAT_RUN_ID` | UTC timestamp | Artifact subdirectory name. |

The external command receives these exported variables:

- `OPENAI_BASE_URL`
- `OPENAI_API_KEY` when configured
- `SHIM_BASE_URL`
- `SHIM_AUTH_HEADER`
- `SHIM_CAPABILITIES_FILE`
- `RESPONSES_COMPAT_RUN_MODE`
- `RESPONSES_COMPAT_EXPECTED_UPSTREAM`
- `RESPONSES_COMPAT_PROFILE`
- `RESPONSES_COMPAT_ARTIFACT_DIR`

## Artifact Contract

Every run writes:

- `readyz.json`
- `readyz.status`
- `capabilities.json`
- `capabilities.status`
- `capabilities-summary.json`
- `run.env`
- `harness-warnings.txt`

When an external tester command is provided, the runner also writes:

- `tester.command`
- `tester.stdout.log`
- `tester.stderr.log`
- `tester.exitcode`

Artifacts live under `.data/`, which is intentionally ignored by git.

## Broad Subset Profile

The `responses-broad-subset` profile should include the observable behavior
below. Tester cases must be capability-gated by `/debug/capabilities` before
they assert optional tool or transport behavior.

| Area | External test expectation | Current claim boundary |
| --- | --- | --- |
| `POST /v1/responses` | Create text response, object shape, status, output text, usage object when available. | Broad subset, not exact model-quality parity. |
| `GET /v1/responses/{id}` | Retrieve stored responses created with `store: true`. | Broad subset. |
| `DELETE /v1/responses/{id}` | Delete stored local responses and return stable OpenAI-like delete shape. | Broad subset. |
| `/v1/responses/{id}/input_items` | List current request plus state items used to generate the response. | Broad subset; exact hosted hidden context is not claimed. |
| `previous_response_id` | Follow-up turn can use stored previous response state. | Broad subset. |
| `conversation` | Conversation-backed response state persists across turns. | Broad subset. |
| `store: false` | Response is returned to the client without relying on local stored replay. | Broad subset; tester should not require later retrieve unless docs require storage. |
| `stream: true` create | Generic semantic SSE lifecycle and text deltas. | Broad subset; exact hosted tool-specific choreography needs fixtures before stronger claims. |
| retrieve streaming | Stored response replay emits stable semantic events. | Broad subset; generic replay is acceptable where hosted choreography is unspecified. |
| `/v1/responses/input_tokens` | Returns `response.input_tokens` object and deterministic local count. | Broad subset; exact upstream tokenizer parity is not claimed. |
| `responses.mode` | `prefer_local`, `prefer_upstream`, and `local_only` keep documented fallback behavior. | Broad subset. |
| Function/custom tools | Function/custom tool call shape, tool output follow-up, validation/repair boundaries. | Broad subset. |
| Constrained decoding | Use capability flags to distinguish `shim_validate_repair` from `grammar_native`. | Broad subset, backend-specific. |
| Local tool families | Test only families enabled in `/debug/capabilities`: `file_search`, `web_search`, `image_generation`, `computer`, `code_interpreter`, `mcp.server_url`, `tool_search`, native local `shell`, native local `apply_patch`. | Broad subset, local/runtime-specific. |
| WebSocket | Test only when `/debug/capabilities` advertises the local Responses WebSocket subset. | Broad subset; HTTP remains the baseline transport. |

## Non-Goals

Do not use this runner to claim:

- exact hosted planner behavior
- exact hosted/native tool SSE choreography without upstream fixtures
- exact hosted storage retention, quota, cache, or billing behavior
- exact model output quality
- internal fixture or backend adapter contracts
- local-only shim extensions as OpenAI API parity

## Hardening Loop

Use this order when the external tester finds a failure:

1. Check the failing case against `/debug/capabilities`.
2. Decide whether it is a real `Broad subset` regression, a tester profile
   mismatch, or an overclaim in docs.
3. If the failure depends on exact SSE choreography or ambiguous hosted tool
   behavior, capture a real upstream fixture before changing the claim.
4. Fix the smallest implementation gap that preserves the public OpenAI
   contract.
5. Update this runbook, [Compatibility Matrix](../compatibility-matrix.md), or
   the tester profile only when the evidence changes.
6. Run `make devstack-ci-smoke`, the external tester runner, `go test ./...`,
   `make lint`, and `git diff --check`.

## Relationship To Existing Smokes

`make devstack-ci-smoke` remains the repo-owned deterministic gate.
`make devstack-full-smoke` remains the local heavy gate with real Codex CLI
coverage. The external tester runner adds a stable bridge for API-surface
compatibility testing against the same running shim and the same capability
manifest.
