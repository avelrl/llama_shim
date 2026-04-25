# Responses Compatibility External Tester

Last updated: April 25, 2026.

Status: repo-owned runner and Broad subset profile are in place. This is an
engineering runbook, not a stronger hosted-parity claim.

## Source Check

This runbook was checked against the local docs index in
[`openapi/llms.txt`](../../openapi/llms.txt), the OpenAI Docs MCP, and the
official OpenAI docs on April 25, 2026.

Relevant official pages:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Responses API reference](https://platform.openai.com/docs/api-reference/responses)

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

Do not interpret a `devstack-fixture` run with a real-model profile as a real
Qwen, GPT, vLLM, SGLang, llama.cpp, or OpenAI compatibility verdict. The runner
writes `harness-warnings.txt` when the profile or command appears
real-model-specific while the mode is still `devstack-fixture`.

## Environment Contract

| Variable | Default | Meaning |
| --- | --- | --- |
| `SHIM_BASE_URL` | `http://127.0.0.1:18080` | Shim root used for `/readyz` and `/debug/capabilities`. |
| `OPENAI_BASE_URL` | `$SHIM_BASE_URL/v1` | OpenAI-compatible base URL passed to the external tester. |
| `OPENAI_API_KEY` | `shim-test-key` | API key passed to SDK-style testers. |
| `SHIM_AUTH_HEADER` | empty | Optional auth header for shim-owned probe endpoints. |
| `RESPONSES_COMPAT_RUN_MODE` | `devstack-fixture` | Harness mode: `devstack-fixture` or `real-upstream`. |
| `RESPONSES_COMPAT_EXPECTED_UPSTREAM` | `devstack-fixture` in devstack mode, empty otherwise | Operator assertion for the upstream behind the shim. |
| `RESPONSES_COMPAT_PROFILE` | `responses-broad-subset` | Profile name for tester-side filtering. |
| `RESPONSES_COMPAT_TESTER_CMD` | empty | Shell command used to run the external tester. |
| `OPENAI_COMPAT_TESTER_CMD` | empty | Backward-compatible alias for the tester command. |
| `RESPONSES_COMPAT_REQUIRE_TESTER` | `0` | When `1` or `true`, missing tester command is a failure. |
| `RESPONSES_COMPAT_ARTIFACT_DIR` | `.data/responses-compat-external` | Artifact root. |
| `RESPONSES_COMPAT_RUN_ID` | UTC timestamp | Artifact subdirectory name. |

The external command receives these exported variables:

- `OPENAI_BASE_URL`
- `OPENAI_API_KEY`
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
- `capabilities.json`
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
