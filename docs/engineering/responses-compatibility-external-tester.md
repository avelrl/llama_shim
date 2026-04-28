# Responses Compatibility External Tester

Last updated: April 28, 2026.

Status: repo-owned runner and Broad subset profile are in place. This is an
engineering runbook, not a stronger hosted-parity claim.

## Source Check

This runbook was checked against the local docs index in
[`openapi/llms.txt`](../../openapi/llms.txt), the OpenAI Docs MCP, and the
official OpenAI docs on April 27, 2026.
Official DeepSeek upstream dialect notes were checked on April 27, 2026.
Official Kimi/Moonshot upstream dialect notes were checked on April 27, 2026.
Official Qwen Code upstream dialect notes were checked on April 28, 2026.

Relevant official pages:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Responses API reference](https://platform.openai.com/docs/api-reference/responses)
- [Chat Completions API reference](https://platform.openai.com/docs/api-reference/chat/create)
- [DeepSeek Tool Calls](https://api-docs.deepseek.com/guides/tool_calls)
- [DeepSeek Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode)
- [DeepSeek Chat Completion](https://api-docs.deepseek.com/api/create-chat-completion/)
- [DeepSeek JSON Output](https://api-docs.deepseek.com/guides/json_mode)
- [DeepSeek Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing)
- [DeepSeek First API Call](https://api-docs.deepseek.com/)
- [Kimi API Overview](https://platform.kimi.ai/docs/overview)
- [Kimi K2.6 Quickstart](https://platform.kimi.ai/docs/guide/kimi-k2-6-quickstart)
- [Kimi Thinking Model Guide](https://platform.kimi.ai/docs/guide/use-kimi-k2-thinking-model)
- [Kimi Tool Calls](https://platform.kimi.ai/docs/guide/use-kimi-api-to-complete-tool-calls)
- [Kimi Chat Completion](https://platform.kimi.ai/docs/api/chat)
- [Kimi Agent Support](https://platform.kimi.ai/docs/guide/agent-support)
- [Qwen Code Architecture](https://qwenlm.github.io/qwen-code-docs/en/developers/architecture/)
- [Qwen Code Model Providers](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/model-providers/)

Implementation references:

- [OpenAI Codex](https://github.com/openai/codex)
- [Kimi CLI](https://github.com/MoonshotAI/kimi-cli)
- [Qwen Code](https://github.com/QwenLM/qwen-code)
- [OpenCode](https://github.com/anomalyco/opencode)

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

Last real-upstream check: April 28, 2026.

### April 27, 2026 DeepSeek Diagnostic Run

Configuration:

- harness mode: operator-run real upstream through the shim
- tester mode: `strict`
- tester profile: DeepSeek V4 Pro profile
- shim command: `go run ./cmd/shim -config ./config.yaml`

Result:

- Responses core cases passed through the shim, including basic create,
  storage/retrieve, streaming, structured output, previous-response state,
  conversations, input-items, and compaction cases.
- Direct Chat Completions exposed upstream-dialect mismatches:
  DeepSeek-compatible gateways accepted OpenAI-shaped Responses traffic through
  the shim but rejected some raw Chat Completions details, including the
  OpenAI `developer` role, OpenAI `response_format.type=json_schema`, and a
  thinking/tool-choice combination that returned an upstream
  `deepseek-reasoner does not support this tool_choice` error.

Shim-owned follow-up:

- Direct `/v1/chat/completions` proxy requests can normalize OpenAI
  `developer` messages to upstream `system` messages before forwarding when a
  matching `chat_completions.upstream_compatibility.models[]` rule enables
  `remap_developer_role`.
- Shim-generated Chat Completions requests, including local Responses tool and
  constrained-output helper paths, use the same upstream compatibility
  normalizer.
- A DeepSeek-style rule can add `thinking: {"type":"disabled"}` when the caller
  did not provide an explicit `thinking` value.
- A DeepSeek-style rule can downgrade upstream Chat Completions
  `response_format.type=json_schema` to JSON mode
  (`response_format.type=json_object`) and prepend the schema as a system
  instruction. This is an upstream transport compatibility bridge only; it is
  not a native strict JSON Schema guarantee.
- Generic upstream failures now report `upstream request failed` /
  `upstream request timed out` instead of llama.cpp-specific wording.

Example config:

```yaml
chat_completions:
  upstream_compatibility:
    models:
      - model: deepseek-*
        remap_developer_role: true
        default_thinking: disabled
        json_schema_mode: json_object_instruction
```

Remaining boundary:

- DeepSeek JSON mode can produce valid JSON, but it does not provide OpenAI
  Structured Outputs parity for `json_schema`.
- Tool-choice behavior can still fail when the configured gateway/model rejects
  forced tool calls or tool calls while thinking is enabled. Treat those as
  real-upstream/model constraints until a rerun proves otherwise.

Follow-up strict rerun:

- A later strict rerun improved the Responses side but exposed two shim-local
  bugs: direct Chat proxy responses could arrive gzip-compressed while the shim
  tried to sanitize them as raw JSON, and Responses function-call replay used a
  response item `id` as the Chat `tool_calls[].id` instead of the Responses
  `call_id` that `function_call_output` references.
- The fix keeps `Accept-Encoding` under the Go transport's control so upstream
  responses are transparently decoded before sanitize/shadow-store work, and it
  maps Responses function calls to Chat tool calls with the same `call_id` that
  subsequent tool output messages reference.

### April 27, 2026 DeepSeek V4 Pro Green Rerun

Configuration:

- harness mode: operator-run real upstream through the shim
- tester mode: `strict`
- tester profile: DeepSeek V4 Pro profile
- shim command: `go run ./cmd/shim -config ./config.yaml`
- operator report name: `llama_shim_deepseek_4_pro_20260427_204544`

Result:

- `chat`: `READY`
- `responses`: `READY`
- flaky cases: `0`
- unsupported cases: `0`
- incompatibilities: `0`
- Direct Chat Completions passed basic text, streaming, memory, tool-call,
  forced-tool-call, JSON Schema compatibility, and JSON object cases.
- Responses passed basic create, store/retrieve, streaming, structured output,
  function/custom tools, forced tool calls, grammar/custom-tool paths,
  conversations, compaction, input-items, and previous-response cases.

Evidence from shim logs:

- The configured DeepSeek compatibility rule was loaded.
- `developer` roles were remapped for direct Chat requests.
- `thinking` was disabled when the caller did not provide an explicit value.
- Chat `json_schema` requests were bridged to upstream JSON mode plus a schema
  instruction.
- Direct Chat proxy responses no longer hit gzip/sanitizer decode failures.
- Responses function-call replay used matching tool-call IDs and completed the
  tool-result follow-up path.

Current interpretation:

- This is a green real-upstream compatibility gate for the shim's current
  strict external tester profile against DeepSeek V4 Pro.
- It does not upgrade DeepSeek JSON mode to native OpenAI Structured Outputs
  parity. The `json_schema` behavior remains a compatibility bridge.
- It does not prove Codex task reliability. Codex adds a harder agent loop:
  longer prompts, repeated shell/file operations, model planning quality,
  timeout behavior, and CLI-specific configuration.
- DeepSeek's public model table currently lists `deepseek-v4-flash` and
  `deepseek-v4-pro` with the same relevant API feature flags for this profile:
  JSON Output and Tool Calls. Treat `flash` vs `pro` Codex smoke differences as
  model quality, latency, cost, or timeout behavior until a run proves an API
  contract difference.

### April 26, 2026 Kimi K2.6 Green Gate

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

### April 27, 2026 Kimi Dialect Notes

Inputs checked:

- Official Kimi/Moonshot docs listed in the source check.
- Local Kimi CLI provider implementation from the operator-owned checkout.

Useful Kimi-specific observations:

- Kimi Chat Completions is OpenAI-compatible enough to use the standard
  `/v1/chat/completions` path, but Kimi CLI still performs provider-specific
  request shaping.
- Kimi CLI defaults agent Chat calls to `max_tokens: 32000`. This is relevant
  for Codex-like and tester tool loops because short upstream output budgets can
  surface as `finish_reason: "length"` instead of a final answer after a tool
  result.
- Kimi thinking mode can use `reasoning_effort` plus a Moonshot-specific
  `thinking` body object on compatible upstreams. LiteLLM/OpenAI-provider
  Kimi model groups can reject that field, so the shim should not inject a
  default `thinking` value for generic Kimi/Codex smoke runs.
- Kimi streams and non-stream responses can carry `reasoning_content`; this is
  useful to Kimi clients but should not be overclaimed as OpenAI Chat parity.
- Kimi CLI normalizes function tool schemas by adding missing `type` values to
  nested parameter properties. This covers JSON Schema-valid enum-only fields
  that some OpenAI-compatible upstream validators reject.
- Kimi CLI omits assistant `content` entirely for assistant tool-call messages
  when visible content is empty. This avoids upstream rejection of empty content
  in coding/tool loops.

Shim-owned compatibility now available:

```yaml
chat_completions:
  upstream_compatibility:
    models:
      - model: Kimi-*
        default_thinking: passthrough
        json_schema_mode: json_object_instruction
        default_max_tokens: 32000
        ensure_tool_parameter_property_types: true
        sanitize_moonshot_tool_schema: true
        omit_empty_assistant_tool_content: true
        retry_invalid_tool_arguments: true
        invalid_tool_arguments_fallback: final_text
```

Current boundary:

- This is request-shape compatibility for upstream Chat calls. It does not prove
  Kimi/Codex agent-task reliability.
- `sanitize_moonshot_tool_schema` is a Kimi/Moonshot transport workaround for
  schemas with `$ref` siblings or tuple-style array `items`; it is model-scoped
  and does not change the shim's OpenAI-facing request shape.
- `retry_invalid_tool_arguments` is a narrow shim-local tool-loop workaround for
  Kimi/LiteLLM `Expecting value: line 1 column 1` failures after malformed
  generated tool-call arguments. It retries once with an explicit JSON-arguments
  repair instruction and does not silently drop tool calls.
- `invalid_tool_arguments_fallback: final_text` is a Kimi-scoped continuation
  path for the same failure after local tool outputs already exist. It asks
  upstream for final plain text without tools instead of surfacing a 502.
- Kimi `reasoning_content` round-tripping is not currently a stronger OpenAI
  Chat compatibility claim. For tool-heavy smoke runs, keep Kimi thinking
  passthrough unless preserved-thinking behavior is the subject of the test and
  the exact upstream is known to accept the `thinking` body field.

### April 28, 2026 Qwen 3.6 Codex Smoke

Inputs:

- model: `Qwen3.6-35B-A3B`
- shim route: Codex CLI through `/v1/responses`
- smoke command: `scripts/codex-cli-real-upstream-smoke.sh`
- local source check: operator-owned Qwen Code checkout plus the official Qwen
  Code architecture and model-provider docs listed above.

Observed result:

- The Codex real-upstream smoke passed boot, read, write, and bugfix.
- Qwen emitted visible assistant text with leading newlines before the expected
  markers; the smoke already treats marker matching whitespace-tolerantly.
- Codex printed `ReasoningRawContentDelta without active item` during the
  bugfix case. This matched the DeepSeek warning class and did not break the
  final response.
- The shim log showed repeated upstream Chat 400s from the local constrained
  helper path: the upstream rejected `response_format.type=json_schema` when no
  nested `response_format.json_schema` field was present.
- After the Qwen compatibility rule was added and the shim was restarted, a
  rerun passed boot, read, write, and bugfix again. The shim log showed
  `json_schema_downgraded=true` and no repeated upstream 400s for that
  `response_format` shape.
- Remaining visible smoke noise was not a shim transport failure:
  `ReasoningRawContentDelta without active item` came from Codex CLI handling
  reasoning deltas, `cat -A` was a GNU/BSD command mismatch on macOS, and
  `go test ... | tail` masked the command exit code through shell pipeline
  semantics.

Useful Qwen-specific observations:

- Qwen Code configures OpenAI-compatible providers with `extra_body`.
  Thinking-capable Qwen deployments use provider-specific
  `extra_body.enable_thinking`, not the Kimi/DeepSeek `thinking` request shape.
- Qwen Code explicitly disables thinking for background/forked paths by
  overriding `enable_thinking: false` and removing generic reasoning config.
- OpenCode uses Qwen-specific sampling defaults (`temperature` around `0.55`,
  `top_p: 1`) and enables DashScope `enable_thinking` only for
  reasoning-capable Alibaba/Qwen-style providers.

Recommended shim config:

```yaml
chat_completions:
  upstream_compatibility:
    models:
      - model: Qwen*
        default_thinking: passthrough
        json_schema_mode: json_object_instruction
```

This is a scoped transport workaround for Qwen/LiteLLM/DashScope-like
Chat Completions backends. It preserves the OpenAI-shaped public request at the
shim boundary, avoids a noisy upstream retry in shim-local constrained helpers,
and does not claim native OpenAI Structured Outputs parity for Qwen.

For practical model ranking and manual-smoke order, see
[Codex Upstream Model Matrix](codex-upstream-model-matrix.md).

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
