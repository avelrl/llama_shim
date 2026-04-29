# Codex Upstream Model Matrix

Last updated: April 29, 2026.

Status: practical Codex-through-shim model notes. This is not a general model
benchmark and not an OpenAI API parity claim. Scores below reflect only the
observed shim/Codex smoke and external-tester behavior captured in this repo.

## Source References

Official and implementation references used for this matrix:

- [OpenAI Codex](https://github.com/openai/codex): client behavior, Responses
  provider config, model metadata, and local tool execution.
- [Kimi CLI](https://github.com/MoonshotAI/kimi-cli): Kimi/Moonshot request
  shaping, tool schema handling, thinking behavior, and coding-session defaults.
- [Qwen Code](https://github.com/QwenLM/qwen-code): Qwen/DashScope provider
  shape, `extra_body.enable_thinking`, and background thinking behavior.
- [OpenCode](https://github.com/anomalyco/opencode): provider-specific defaults
  used as a second implementation reference for Qwen-like and other
  OpenAI-compatible providers.
- [Responses Compatibility External Tester](responses-compatibility-external-tester.md):
  real-upstream API-surface ledger for DeepSeek, Kimi, and Qwen.
- [Codex Testing Plan](../guides/codex-testing-plan.md): manual and automated
  Codex smoke procedure.
- Hugging Face model cards:
  [MiMo-V2.5-Pro](https://huggingface.co/XiaomiMiMo/MiMo-V2.5-Pro),
  [DeepSeek-V4-Pro](https://huggingface.co/deepseek-ai/DeepSeek-V4-Pro),
  and [Qwen3.6-35B-A3B](https://huggingface.co/Qwen/Qwen3.6-35B-A3B).

Provider documentation:

- DeepSeek:
  [First API Call](https://api-docs.deepseek.com/),
  [Chat Completion](https://api-docs.deepseek.com/api/create-chat-completion/),
  [Tool Calls](https://api-docs.deepseek.com/guides/tool_calls),
  [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode),
  [JSON Output](https://api-docs.deepseek.com/guides/json_mode),
  [Models and Pricing](https://api-docs.deepseek.com/quick_start/pricing).
- Kimi/Moonshot:
  [API Overview](https://platform.kimi.ai/docs/overview),
  [Chat Completion](https://platform.kimi.ai/docs/api/chat),
  [Kimi K2.6 Quickstart](https://platform.kimi.ai/docs/guide/kimi-k2-6-quickstart),
  [Thinking Model Guide](https://platform.kimi.ai/docs/guide/use-kimi-k2-thinking-model),
  [Tool Calls](https://platform.kimi.ai/docs/guide/use-kimi-api-to-complete-tool-calls),
  [Agent Support](https://platform.kimi.ai/docs/guide/agent-support).
- Qwen:
  [Qwen Code Architecture](https://qwenlm.github.io/qwen-code-docs/en/developers/architecture/),
  [Qwen Code Model Providers](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/model-providers/),
  [Qwen Code Configuration](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/).

## Rating Key

Ratings are intentionally coarse:

- `5`: reliable in the current repo-owned checks.
- `4`: good enough for practical use, with known warnings or retries.
- `3`: useful, but requires provider-specific config and manual smoke before
  larger tasks.
- `2`: narrow or flaky; use only for targeted diagnosis.
- `1`: not recommended for this path yet.

## Current Matrix

| Model / upstream | Codex context metadata | API compatibility through shim | Codex coding smoke | Tool-call discipline | Config complexity | Best current use | Main risks |
| --- | --- | ---: | ---: | ---: | --- | --- | --- |
| DeepSeek V4 Pro | `1000000` | 5 | 4 | 4 | Medium | External compatibility gate, structured API checks, Codex smoke baseline after config. | Reasoning/tool-choice interactions can fail on some variants; Codex can still print reasoning-delta warnings. |
| Qwen3.6-35B-A3B | `262144` conservative tested default | 4 | 4 | 3 | Low | Practical Codex coding smoke and manual Codex task testing. | Needs Chat `json_schema` downgrade for shim-local helpers; can make Unix/macOS command-shape mistakes such as GNU-only flags. |
| Kimi K2.6 | `262144` | 4 | 3 | 3 | High | Long-context Codex experiments after model-specific config; useful for agent behavior comparison. | Most provider-specific workarounds: schema sanitization, larger output budget, invalid-tool-argument retry/final-text fallback, and careful thinking handling. |
| MiMo v2.5 Pro | `1048576` | 5 via `chat_completions` transport | 4 | 3 | Medium | Chat-only upstream compatibility gate and Codex eval candidate for Responses-over-Chat. | Eval is green with retry, but first attempts can still skip required verification tools; this does not prove native upstream Responses parity. |

## Automated Codex Eval Baselines

These rows are preliminary real-upstream Codex eval harness results, not stable
benchmark scores. Run artifacts live under `.tmp/codex-eval-runs/` and are not
committed.

Use the eval runner to generate the mechanical table from local run artifacts:

```bash
make codex-eval-matrix
```

The generated table is intentionally not the source of interpretation. It
copies facts from `summary.json`: date, run id, model, suite, pass count,
retry-dependent task count, failure buckets, and failed tasks. This document is
the human-maintained layer on top: keep only meaningful baselines here and use
the notes column to explain what the generated numbers mean, for example
whether a retry is acceptable, whether the failure was shim transport or model
tool discipline, and whether the task set changed since the previous run.

| Date | Model | Suite | Attempts | Result | Failure buckets | Notes |
| --- | --- | --- | ---: | --- | --- | --- |
| 2026-04-29 | Kimi K2.6 | `codex-real-upstream` | 2 | 5/7 tasks passed | `checker_diff`: 2 | Exploratory run `run-20260429T125724Z`. `boot`, `read_file`, `basic_patch`, `bugfix_go`, and `bugfix_mixed` passed. `bugfix_mixed` needed retry after first-attempt raw Kimi tool markup. `multi_file` and `plan_doc` failed earlier checker/task wording that was tightened afterward; rerun before treating this as the stable Kimi baseline. |
| 2026-04-29 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 4/7 tasks passed | `checker_diff`: 1, `harness_bug`: 1, `model_no_tool`: 1 | Exploratory run `run-20260429T143815Z`. `boot`, `read_file`, `basic_patch`, and `bugfix_go` passed; `bugfix_go` needed retry after first-attempt raw pseudo tool text. `bugfix_mixed` failed by emitting a plan/patch as text instead of executing a file change. `multi_file` wrote the exact target files on retry but missed the required final sentinel and printed `<patch>` markup. `plan_doc` wrote a reasonable checklist but missed the required final sentinel and a narrow marker. Raw marker detection and final-text classification were tightened afterward; rerun before comparing this score to other models. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 4/7 tasks passed | `raw_tool_markup`: 1, `upstream_http`: 2 | Exploratory run `run-20260429T145829Z`. `boot`, `read_file`, `basic_patch`, and `multi_file` passed. `bugfix_go` and `bugfix_mixed` exposed a shim Chat-history bridge bug for parallel tool calls: consecutive Codex tool calls were serialized as separate assistant messages, and DeepSeek rejected the next request with missing `tool_call_id` tool responses. That bridge bug was fixed after this run, so rerun before scoring DeepSeek coding quality. `plan_doc` also showed raw provider tool markup on retry; the task prompt was tightened afterward to make the checked plan markers explicit. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 6/7 tasks passed | `checker_diff`: 1 | Post bridge-fix, pre-`<bash>` detector run `run-20260429T151357Z`. `boot`, `read_file`, `basic_patch`, `bugfix_go`, `multi_file`, and `plan_doc` passed. The previous parallel-tool-call `upstream_http` failures disappeared. The only failure was `bugfix_mixed`: DeepSeek emitted pseudo-tool text (`<tool_call ...>` then `<bash>...`) instead of executing a file change, so this is model/tool-discipline behavior rather than shim transport failure. The raw-markup detector was extended for `<bash>` after this run. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 6/7 tasks passed | `raw_tool_markup`: 1 | Current DeepSeek baseline `run-20260429T173134Z`. `boot`, `read_file`, `basic_patch`, `bugfix_go`, `multi_file`, and `plan_doc` passed. No upstream transport errors were present in shim logs. `bugfix_mixed` failed twice by printing pseudo shell tool markup (`<bash>...`) instead of executing the file change; the harness now classifies this as provider raw tool markup. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 7/7 tasks passed | none | Current best DeepSeek baseline `run-20260429T174957Z` after runtime pseudo-tool-markup repair detection. No upstream transport errors were present in shim logs. `bugfix_mixed` and `plan_doc` passed on the second harness attempt after first-attempt checker misses, so this is green but still retry-dependent. |
| 2026-04-29 | MiMo v2.5 Pro | `codex-real-upstream` | 2 | 7/7 tasks passed | none | Current MiMo baseline `run-20260429T202049Z` after XML-style raw tool-call marker repair. Earlier run `run-20260429T195801Z` leaked `<tool_call>...` text in `multi_file`; the post-tool raw-markup detector now catches and repairs that class. This run still needed retry for `multi_file`: the first attempt edited files but skipped the required command event, so treat it as green with model-discipline warning, not strict-clean. |

After adding or changing eval tasks, rerun every model before comparing rows.
Run one model at a time against a shim configured for that model:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_MODEL=<model> \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  CODEX_EVAL_SUITE=codex-real-upstream \
  CODEX_EVAL_ATTEMPTS=2 \
  make codex-eval-real-upstream
```

Do not compare old 7-task rows directly with new runs after the task set grows;
keep the run id in the notes and interpret only like-for-like suites.

## Interpretation

DeepSeek V4 Pro is the strongest current API compatibility gate. It passed the
strict external tester profile after the Chat compatibility fixes, so use it
when the question is whether the shim's broad OpenAI-compatible surface still
works.

MiMo v2.5 Pro is now a green API-surface and Codex-eval candidate for chat-only
gateways when `responses.upstream_transport: chat_completions` is enabled. It
still sits behind DeepSeek as the API compatibility gate because its Codex run
needed retry for a model-discipline miss and it does not prove native upstream
Responses parity.

Qwen3.6-35B-A3B is currently a good practical Codex smoke model. It passed
boot/read/write/bugfix through `/v1/responses`, including local file changes
and Go test repair. Its failures are more often model-command quality or
Codex-side reasoning warnings than shim transport failures.

Kimi K2.6 works, but it is the least plug-and-play of the three for Codex task
loops. It benefits from the richest compatibility block and should be treated
as a tuned-provider path, not as the default first model for manual smoke.

Context metadata is Codex-facing budgeting data served by the shim model catalog,
not a new OpenAI API claim. DeepSeek is set to the Hugging Face 1M context line.
MiMo is set to `1048576`, matching the model card's 1M claim and deployment
example. Kimi K2.6 remains at 262144 tokens. Qwen3.6 remains at the conservative
262144-token native path that has been smoked through this gateway; the Qwen
card says the model is extensible up to 1010000 tokens, but raise that only
after the exact upstream deployment proves it end to end.

For chat-only gateways, set `responses.upstream_transport: chat_completions`.
That keeps the Codex-facing `/v1/responses` surface on the shim while routing
model generation through upstream `/v1/chat/completions`; do not interpret that
as native upstream Responses parity.

## Recommended Order

Use this order when qualifying a new shim change:

1. Run deterministic repo tests and devstack checks.
2. Run the strict external tester against DeepSeek V4 Pro or the current
   strongest API-compatibility upstream.
3. Run `make codex-cli-real-upstream-smoke` against Qwen3.6-35B-A3B for a
   practical Codex coding loop.
4. Run `make codex-eval-real-upstream` for MiMo when the change touches
   Responses-over-Chat or chat-only gateway behavior.
5. Run Kimi K2.6 only after the same change is green on the simpler model path,
   or when the change is specifically about Kimi/Moonshot behavior.
6. Move to manual Codex testing only after the automated smoke is green.

## Manual Smoke Scope

Manual smoke is now the right next step for model quality. Keep the tasks small:

- ask for one plain answer;
- read one file;
- create one tiny file;
- patch one known one-line bug;
- run one bounded test command;
- stop and inspect logs after the first ambiguous failure.

Do not use manual smoke to upgrade compatibility labels by itself. It is an
operator-confidence check. Compatibility labels still require docs-backed,
test-backed, and, where needed, fixture-backed evidence.
