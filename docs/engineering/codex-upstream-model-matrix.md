# Codex Upstream Model Matrix

Last updated: May 20, 2026.

Status: practical Codex-through-shim model notes. This is not a general model
benchmark and not an OpenAI API parity claim. Scores below reflect only the
observed shim/Codex smoke and external-tester behavior captured in this repo.

For day-to-day provider/model operating choices, promotion rules, and commands,
use [V4 Model/Provider Operational Matrix](v4-model-provider-operational-matrix.md).
This document remains the longer historical evidence ledger.

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
| DeepSeek V4 Pro | `1000000` | 5 | 4 | 4 | Medium | Broad external compatibility gate and default real-upstream Codex baseline candidate. | Latest full auto baseline passed with one retry-dependent task; reasoning/tool-choice interactions can still fail on some variants. |
| Qwen3 Coder 30B local GPU | `32768` | 4 | 3 | 3 | Medium | Fast local coding assistant for supervised edits and candidate-model experiments. | Bench-lite is 19/20 with repeated `patch_after_context` checker miss; raw-markup failures are now classified as `502 malformed_backend_response`, but the model is not unattended release-gate clean. |
| Qwen3 Coder 30B Q5_K_M local GPU | `32768` | 4 | 4 | 3 | Medium | Best current local Codex candidate for supervised coding and larger local diagnostics. | Codex certification `cert-20260519T220117Z` passed baseline, expanded, and bench-lite 49/49 final tasks, but only with retries. External API tester still shows weak Chat exactness on `chat.basic`; Codex traces include raw markup, malformed backend response, transport, and timeout signals. |
| Gemma 4 E4B local GPU | `32768` | 4 | 2 | 1 | Low | Local chat/API model after external tester compatibility passes. | API certification `cert-20260520T071938Z` passed, but Codex baseline `cert-20260520T072422Z` failed because the model printed a correct patch as final text instead of invoking the edit tool. Shim logs also show llama.cpp/Gemma parser errors on Codex tool transcripts, so do not use it as a Codex gate. |
| Omnicoder 9B local GPU | `32768` | 3 | 1 | 2 | Low | Targeted continuation diagnostics only. | Partial external tester run `llama_shim_omnicoder_9b_20260520_142219` showed core Responses paths working, but `previous_response_id` follow-ups hit upstream `context canceled` and returned `502 transport_error`. Codex was not run and should remain blocked until focused API certification is stable. |
| GLM 4.7 Flash Opus Reasoning local GPU | `32768` | 4 | 2 | 2 | Low | API/chat diagnostics and reasoning-content stress. | API certification `cert-20260520T115344Z` was 27/28 with only `chat.stream` stopping at `H` instead of `HELLO`. Codex baseline `cert-20260520T120358Z` was 8/11; the failed coding tasks show checker drift and malformed `apply_patch` contract, so do not use as a Codex gate. |
| Qwen3.6-35B-A3B | `262144` conservative tested default | 4 | 2 | 2 | Low | Experimental/manual Codex smoke and raw-markup regression probe. | Latest full auto baseline is not promotable; failures include missed final sentinels, pseudo-tool markup, and task-quality drift even after bounded shim repairs. |
| Kimi K2.6 | `262144` | 4 | 4 | 4 | High | Tuned-provider Codex candidate, long-context experiments, and provider-specific comparison. | Good latest Codex evidence, but still retry-dependent on larger profiles and noisy through the gateway/LiteLLM path. |
| MiMo v2.5 Pro | `1048576` | 5 via `chat_completions` transport | 5 | 4 | Medium | Strict chat-only Responses-over-Chat gate and Codex eval candidate. | Latest auto run is strict-clean across baseline, expanded, and bench-lite; this still does not prove native upstream Responses parity. |

## Automated Codex Eval Baselines

These rows are preliminary real-upstream Codex eval harness results, not stable
benchmark scores. Eval artifacts live under `.tmp/codex-eval-runs/` or
`.tmp/codex-eval-loops/`; they are scratch audit artifacts, not committed
baseline evidence. When a run is promoted, copy only the interpreted facts into
this document. For future single-model `codex-real-upstream` loop runs, the
default artifact directory is named `<model>_baseline_<timestamp>` to make the
scratch run easier to identify before it is cleaned.

Use the eval runner to generate the mechanical table from local run artifacts:

```bash
make codex-eval-matrix
```

For a fresh full pass, use the auto wrapper. It runs baseline, expanded, and
benchmark-lite profiles, captures per-profile shim-log slices, and writes a
single top-level report:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
CODEX_PROVIDER=gateway-shim \
CODEX_API_KEY_ENV=GW_API_KEY \
CODEX_EVAL_MODELS="deepseek-v4-pro" \
make codex-eval-auto
```

The auto wrapper writes generated artifacts under
`.tmp/codex-eval-auto/<auto-id>/`. Use `summary.md` first, then the linked
profile `compare.md` files to separate control failures, real-upstream
transport failures, tool-contract failures, model-behavior failures, and
retry-dependent passes before updating this matrix by hand. Use the lower-level
`make codex-eval-loop` target only for a focused single-suite rerun.

The generated table is intentionally not the source of interpretation. It
copies facts from `summary.json`: date, run id, model, suite, pass count,
retry-dependent task count, failure buckets, and failed tasks. This document is
the human-maintained layer on top: keep only meaningful baselines here and use
the notes column to explain what the generated numbers mean, for example
whether a retry is acceptable, whether the failure was shim transport or model
tool discipline, and whether the task set changed since the previous run.
Use [V3 Codex Eval Curation](../v3-codex-eval-curation.md) as the review
procedure before promoting a run or importing a regression.

Current promoted per-model baseline set:

| Model | Baseline id | Result | Retries | Interpretation |
| --- | --- | ---: | ---: | --- |
| MiMo v2.5 Pro | `mimo-v2.5-pro_full_20260507T070500Z` | 11/11 | 0 | Current strict-clean chat-transport baseline; same auto run also produced strict-clean expanded 18/18 and bench-lite 20/20. |
| DeepSeek V4 Pro | `deepseek-v4-pro_full_20260506T181012Z` | 11/11 | 1 | Current green auto baseline, but retry-dependent on `basic_patch`; expanded was strict-clean 18/18 and bench-lite was green 20/20 with one retry-dependent task. |
| Qwen3 Coder 30B Q5_K_M local GPU | `cert-20260519T220117Z` | 49/49 across baseline, expanded, and bench-lite | 6 failed attempts recovered | Best current local Codex evidence. All final tasks passed across focused profiles, but verdict is `codex_retry_dependent`; use with supervision and monitor raw-markup/malformed-response retries. |
| Qwen3.6-35B-A3B | `run-20260430T182633Z` | 8/8 | 3 | Last promoted green baseline remains retry-dependent; May 7 full auto was not promotable, so treat Qwen as experimental/manual until a fresh baseline is green. |
| Kimi K2.6 | `kimi-k2.6_full_20260507T135213Z` | 11/11 | 0 | Current strict-clean baseline. May 7 expanded and bench-lite profiles are green but retry-dependent, so Kimi is practical but not strict-clean on larger diagnostics. |

| Date | Model | Suite | Attempts | Result | Failure buckets | Notes |
| --- | --- | --- | ---: | --- | --- | --- |
| 2026-05-20 | GLM 4.7 Flash Opus Reasoning local GPU | model certification API and Codex baseline | 1 API, 1 Codex baseline profile | API 27/28; Codex baseline 8/11 | API: `chat.stream` exact-text short output; Codex: `checker_diff`, `malformed_backend_response` on constrained `apply_patch` | API run `cert-20260520T115344Z` passed core Responses, structured output, tools, custom tools, `store=true`, conversations, and `previous_response_id`; only native Chat stream emitted `H` rather than `HELLO`. Codex run `cert-20260520T120358Z` failed `basic_patch`, `bugfix_go`, and `bugfix_mixed`. The Go failures reached the right conceptual patch but did not satisfy the constrained patch contract after retries, so treat this as model/tool-loop instability, not a shim transport regression. |
| 2026-05-20 | Omnicoder 9B local GPU | partial external tester API run | 1 | Core Responses paths mostly worked; continuation blocked | `upstream_transport_failed` on `previous_response_id`; tester timeout/spec-violation classification | Partial run `llama_shim_omnicoder_9b_20260520_142219` passed basic Responses, stream, structured output, tools, custom tools, and conversations. The shim log shows first-turn response creation succeeded, then `previous_response_id` follow-ups failed in upstream Chat transport with `context canceled` and shim returned `502 transport_error`. `store=true` and conversations worked independently, so this is a model/upstream continuation blocker rather than a broad storage-surface failure. Do not run Codex profiles for this 9B model unless a focused API run later proves stable continuation. |
| 2026-05-20 | Gemma 4 E4B local GPU | model certification API and Codex baseline | 2 | API compatibility passed; Codex baseline 10/11 | `checker_diff`; shim log parser errors on raw tool transcript markers | API run `cert-20260520T071938Z` passed all external tester rows and is acceptable for chat/API experiments. Codex run `cert-20260520T072422Z` failed `bugfix_go`: both attempts produced a valid-looking apply-patch body as assistant final text, but no edit tool call or file change occurred. The shim log also showed upstream llama.cpp/Gemma parser errors on histories containing `<|tool_call>...`, `assistant{selection:assistant}`, and similar Codex tool transcript markers. Treat Gemma as chat/API-only until a Gemma-specific transcript cleanup/stringify strategy is intentionally tested. |
| 2026-05-19 | Qwen3 Coder 30B Q5_K_M local GPU | model certification Codex `baseline`, `expanded`, and `bench-lite` profiles | 2 | 49/49 final tasks passed | Retry-dependent: 4 checker failures, 1 transport failure, 1 timeout; traces include `raw_tool_markup`, `malformed_backend_response`, `transport_error`, and `server_error` signals | Certification run `cert-20260519T220117Z` completed all three Codex profiles: baseline 11/11, expanded 18/18, bench-lite 20/20. The run took about 62 minutes and recovered 6 failed attempts by retry. Treat this as the strongest local Codex candidate so far, but not strict-clean. Keep external Chat exactness caveat separate: API tester can still fail `chat.basic` by answering `pong` instead of exact `OK`, while Responses/Codex paths are practical. |
| 2026-05-19 | GPT-OSS 120B local GPU | model certification API and Codex baseline | 2 | API compatibility passed; Codex baseline 7/11 | `model_no_tool`: 1, `timeout`: 3; trace noise included `transport_error` 502 and upstream context cancellation | API run `cert-20260519T201957Z` passed all external tester rows with Chat and Responses ready, including native Chat stream. Codex run `cert-20260519T202427Z` failed `bugfix_go`, `bugfix_mixed`, `command_recovery`, and `multi_file`. This is better than GPT-OSS 20B but still not promotable: failed tasks show missing or incomplete file changes, missing final markers, and workspace state described as updated before checkers agree. |
| 2026-05-19 | GPT-OSS 20B local GPU | model certification API and Codex baseline | 2 | API/Responses usable with native Chat stream gap; Codex baseline 6/11 | `checker_diff`, `model_no_tool`, `timeout`; trace noise included `transport_error`, `upstream_timeout`, and rate/quota-like signals | API run `cert-20260519T182616Z` showed one important gap: `chat.stream` returned role/no-op chunks and `finish_reason=length` without text, while Responses stream and other core API paths were usable. Codex run `cert-20260519T193742Z` failed `basic_patch`, `bugfix_go`, `bugfix_mixed`, `command_recovery`, and `multi_file`. The model often planned or described commands instead of completing the tool/file loop, so keep it as an API/assistant candidate rather than a Codex candidate. |
| 2026-05-19 | Qwen3 Coder 30B local GPU | model certification `baseline`, `expanded`, and `bench-lite` focused Codex profiles | 2 | baseline passed in recent certification runs; expanded failed 17/18 on `multi_file`; bench-lite 19/20 | `checker_diff`: repeated `patch_after_context`; transient `transport_error`/`malformed_backend_response` classified in traces | Current local candidate evidence spans `cert-20260519T144415Z`, `cert-20260519T154024Z`, and `cert-20260519T162448Z`. The Codex-compat assistant-content suppression fixed `command_recovery`; raw tool markup now maps to classified `502 malformed_backend_response` instead of hidden `500`. Remaining bench-lite failure is model behavior: the model reads context and reports success, but leaves a required line unchanged. Treat as usable with diff/test supervision, not a release gate. |
| 2026-05-06 | DeepSeek V4 Pro | auto `baseline` + `expanded` + `bench-lite` profiles | 2 | baseline 11/11 candidate and 20/20 control; expanded 18/18 candidate; bench-lite 20/20 candidate | none | Current DeepSeek auto evidence `deepseek-v4-pro_full_20260506T181012Z`. Baseline was green but retry-dependent on `basic_patch`; expanded was strict-clean; bench-lite was green with one retry-dependent task, `multi_file`. Keep the retry visible rather than calling the latest baseline strict-clean. |
| 2026-05-07 | MiMo v2.5 Pro | auto `baseline` + `expanded` + `bench-lite` profiles | 2 | baseline 11/11, expanded 18/18, bench-lite 20/20 | none | Current MiMo auto evidence `mimo-v2.5-pro_full_20260507T070500Z`. All three profiles were strict-clean with no retry-dependent tasks. This is strong evidence for the shim-owned Responses-over-Chat path, not native upstream `/v1/responses` parity. |
| 2026-05-07 | Qwen3.6-35B-A3B | auto `baseline` + `expanded` + `bench-lite` profiles | 2 | baseline 10/11, expanded 16/18, bench-lite 18/20 | `candidate_model_behavior`, `candidate_tool_contract` | Latest Qwen auto evidence `qwen3.6-35b-a3b_full_20260507T124107Z` is not promotable. Bounded raw-markup detection was extended for Qwen forms such as `<chatcmpl-tool>`, `<function.chatcmpl.tool>`, `<tools>`, shell-command blocks, fenced patch/code blocks, and `<tool_code_exec>` / `<tool_code_interpreter>` markers, but the remaining failures still show model discipline and task-quality issues rather than a clean unattended Codex path. |
| 2026-05-07 | Kimi K2.6 | auto `baseline` + `expanded` + `bench-lite` profiles | 2 | baseline 11/11, expanded 18/18, bench-lite 20/20 | none | Current Kimi auto evidence spans `kimi-k2.6_full_20260507T135213Z`, `kimi-k2.6_full_20260507T152016Z`, and the focused bench-lite rerun `kimi-k2.6_full_20260507T165956Z`. Baseline is strict-clean. Expanded passed with two retry-dependent tasks, and bench-lite passed with three retry-dependent tasks after key-value checker normalization. Shim diagnostics still showed gateway/LiteLLM `400 Expecting value` and one `context canceled`, so classify Kimi as green but retry/gateway-noise aware. |
| 2026-04-29 | Kimi K2.6 | `codex-real-upstream` | 2 | 5/7 tasks passed | `checker_diff`: 2 | Exploratory run `run-20260429T125724Z`. `boot`, `read_file`, `basic_patch`, `bugfix_go`, and `bugfix_mixed` passed. `bugfix_mixed` needed retry after first-attempt raw Kimi tool markup. `multi_file` and `plan_doc` failed earlier checker/task wording that was tightened afterward; rerun before treating this as the stable Kimi baseline. |
| 2026-04-30 | Kimi K2.6 | `codex-real-upstream` | 2 | 8/8 tasks passed | none | Former Kimi baseline `run-20260430T190648Z` after bounded final-text raw-markup repair. All tasks passed on the first harness attempt and no summary failure buckets were reported. The shim log had no request-level `502` or `ERROR`; it showed one successful empty-assistant final-text fallback in `bugfix_go` that completed with HTTP 200 and `BUGFIXED`. Superseded by the May 7 auto baseline. |
| 2026-04-29 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 4/7 tasks passed | `checker_diff`: 1, `harness_bug`: 1, `model_no_tool`: 1 | Exploratory run `run-20260429T143815Z`. `boot`, `read_file`, `basic_patch`, and `bugfix_go` passed; `bugfix_go` needed retry after first-attempt raw pseudo tool text. `bugfix_mixed` failed by emitting a plan/patch as text instead of executing a file change. `multi_file` wrote the exact target files on retry but missed the required final sentinel and printed `<patch>` markup. `plan_doc` wrote a reasonable checklist but missed the required final sentinel and a narrow marker. Raw marker detection and final-text classification were tightened afterward; rerun before comparing this score to other models. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 4/7 tasks passed | `raw_tool_markup`: 1, `upstream_http`: 2 | Exploratory run `run-20260429T145829Z`. `boot`, `read_file`, `basic_patch`, and `multi_file` passed. `bugfix_go` and `bugfix_mixed` exposed a shim Chat-history bridge bug for parallel tool calls: consecutive Codex tool calls were serialized as separate assistant messages, and DeepSeek rejected the next request with missing `tool_call_id` tool responses. That bridge bug was fixed after this run, so rerun before scoring DeepSeek coding quality. `plan_doc` also showed raw provider tool markup on retry; the task prompt was tightened afterward to make the checked plan markers explicit. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 6/7 tasks passed | `checker_diff`: 1 | Post bridge-fix, pre-`<bash>` detector run `run-20260429T151357Z`. `boot`, `read_file`, `basic_patch`, `bugfix_go`, `multi_file`, and `plan_doc` passed. The previous parallel-tool-call `upstream_http` failures disappeared. The only failure was `bugfix_mixed`: DeepSeek emitted pseudo-tool text (`<tool_call ...>` then `<bash>...`) instead of executing a file change, so this is model/tool-discipline behavior rather than shim transport failure. The raw-markup detector was extended for `<bash>` after this run. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 6/7 tasks passed | `raw_tool_markup`: 1 | Earlier DeepSeek baseline `run-20260429T173134Z`. `boot`, `read_file`, `basic_patch`, `bugfix_go`, `multi_file`, and `plan_doc` passed. No upstream transport errors were present in shim logs. `bugfix_mixed` failed twice by printing pseudo shell tool markup (`<bash>...`) instead of executing the file change; the harness now classifies this as provider raw tool markup. |
| 2026-04-29 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 7/7 tasks passed | none | Former best DeepSeek baseline `run-20260429T174957Z` after runtime pseudo-tool-markup repair detection. No upstream transport errors were present in shim logs. `bugfix_mixed` and `plan_doc` passed on the second harness attempt after first-attempt checker misses, so this was green but retry-dependent. Superseded by the 8-task `run-20260430T132430Z` baseline. |
| 2026-04-29 | MiMo v2.5 Pro | `codex-real-upstream` | 2 | 7/7 tasks passed | none | Former MiMo baseline `run-20260429T202049Z` after XML-style raw tool-call marker repair. Earlier run `run-20260429T195801Z` leaked `<tool_call>...` text in `multi_file`; the post-tool raw-markup detector now catches and repairs that class. This run still needed retry for `multi_file`, so it is superseded by the strict-clean 8-task `run-20260429T225025Z` baseline. |
| 2026-04-29 | MiMo v2.5 Pro | `codex-real-upstream` | 2 | 8/8 tasks passed | none | Former MiMo baseline `run-20260429T225025Z`. The generated matrix reports 0 retries and `strict-clean`. Superseded by the May 7 auto baseline, while still not claiming native upstream Responses parity. |
| 2026-04-30 | DeepSeek V4 Pro | `codex-real-upstream` | 2 | 8/8 tasks passed | none | Former DeepSeek baseline `run-20260430T132430Z`. The generated matrix reports 0 retries and `strict-clean`, superseding the previous 7-task rows after the suite expansion. |
| 2026-05-01 | DeepSeek V4 Pro | `codex-real-upstream` + `codex-core` control | 2 | 11/11 candidate tasks passed, 20/20 control tasks passed | none | Former DeepSeek baseline `deepseek-v4-pro_baseline_20260501T200951Z`. The generated loop matrix reported 0 retries and `strict-clean` for both the devstack control and DeepSeek candidate. Superseded by the May 4 baseline after the raw-markup detector and checker refinements. |
| 2026-05-04 | DeepSeek V4 Pro | `codex-real-upstream` + `codex-core` control | 2 | 11/11 candidate tasks passed, 20/20 control tasks passed | none | Former DeepSeek strict-clean baseline `deepseek-v4-pro_baseline_20260504T063358Z`. The generated loop matrix reports 0 retries and `strict-clean` for both the devstack control and DeepSeek candidate. The shim log spot-check for the run window found no structured `ERROR`/`WARN`, no `4xx`/`5xx`, no raw-tool repair, and all relevant `/v1/responses` request entries completed with HTTP 200. |
| 2026-05-04 | DeepSeek V4 Pro | `codex-real-upstream-expanded` + `codex-core` control | 2 | 18/18 candidate tasks passed, 20/20 control tasks passed | none | Expanded diagnostic run `deepseek-v4-pro_codex-real-upstream-expanded_20260504T065057Z`. It was green but not strict-clean: `bugfix_mixed` and `command_recovery` were retry-dependent. The shim log spot-check found no transport or request-level errors. Keep this separate from the stable baseline because expanded coverage intentionally includes more model-discipline-sensitive tasks. |
| 2026-05-04 | DeepSeek V4 Pro | `codex-bench-lite` + `codex-bench-lite` control | 2 | 20/20 candidate tasks passed, 20/20 control tasks passed | none | Benchmark-lite loop `deepseek-v4-pro_codex-bench-lite_20260504T081412Z`. It was green with one retry-dependent task: `patch_after_context` failed the first checker because the model inserted leading spaces into config-like lines, then passed on retry. The shim log spot-check found no `WARN`/`ERROR`, `502`, failed stream event, raw-tool repair, or upstream transport issue. |
| 2026-05-04 | DeepSeek V4 Pro | auto `baseline` + `bench-lite` profiles | 2 | baseline 11/11 candidate and 20/20 control; bench-lite 20/20 candidate and 20/20 control | none | Repeat auto run `deepseek-v4-pro_full_20260504T185826Z` after a clean shim restart and fresh DB. `baseline` was strict-clean. `bench-lite` was green with one retry-dependent task, `patch_after_context`. Both profile shim diagnostics reported no high-signal matches. This superseded the earlier same-day failed auto attempt and is now superseded by the May 6 full auto evidence. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 6/8 tasks passed | `checker_diff`: 1, `timeout`: 1 | Earlier Qwen eval run `run-20260430T133543Z` after the eight-task suite expansion. `boot`, `read_file`, `basic_patch`, `bugfix_go`, `command_recovery`, and `multi_file` passed. `bugfix_mixed` failed by emitting Qwen template/function-output text instead of completing the required final marker, and `plan_doc` first emitted pseudo function-output text then timed out with no events. The shim log also showed one recovered invalid `apply_patch` 502 during `bugfix_go`; it did not fail the task. After this run, raw-markup detection was extended for Qwen `<|mask_start|>`, `<|mask_end|>`, and `<function_call_output>` forms, so rerun before treating this as the stable Qwen baseline. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 6/8 tasks passed | `checker_diff`: 2 | Follow-up Qwen run `run-20260430T140247Z`. The previous timeout disappeared and `command_recovery` passed cleanly, but `bugfix_mixed` still missed the required final marker after doing partial work, and `plan_doc` printed pseudo patch markup (`<apply_patch><command>...`) instead of executing a tool call. After this run, raw-markup detection was extended again for `<prelude>`, `<apply_patch>`, and `<command>` forms. This remains a Qwen tool-discipline issue rather than a shim transport failure. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 7/8 tasks passed | `upstream_http`: 1 | Follow-up Qwen run `run-20260430T142106Z`. `boot`, `basic_patch`, `bugfix_go`, `command_recovery`, `multi_file`, and `plan_doc` passed on the first attempt; `read_file` passed on retry after first-attempt context leakage instead of the required final marker. `bugfix_mixed` first emitted raw `<command>` markup, which the harness now classifies correctly, then failed with a shim-local constrained `apply_patch` 502 caused by an otherwise valid patch hunk whose unchanged `}` context line missed the required leading space. After this run, apply_patch input repair was extended for that formal grammar case. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 7/8 tasks passed | `checker_diff`: 1 | Follow-up Qwen run `run-20260430T144313Z`. The previous `bugfix_mixed` apply_patch repair worked: `bugfix_mixed` passed on the first attempt. The remaining failure is `plan_doc`; both attempts printed provider pseudo-tool markup (`<function_call>...` and `<tool_code_call>...`) instead of producing `PLAN.md` plus `PLANNED`. After this run, raw-markup detection and Codex compatibility hints were extended for those two Qwen forms, so rerun before treating this as the stable Qwen baseline. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 6/8 tasks passed | `checker_diff`: 2 | Follow-up Qwen run `run-20260430T161903Z`. No shim transport failures occurred and the previous `bugfix_mixed` repair remained green. `multi_file` changed the right values but omitted the trailing newline in `app/status.txt`; after this run, that file expectation was relaxed to ignore leading/trailing whitespace. `plan_doc` created `PLAN.md` on one attempt, but did not send the required final `PLANNED` marker in either attempt, so this remains a model finalization-discipline failure rather than a shim repair candidate. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 8/8 tasks passed | none | Former Qwen green baseline `run-20260430T165416Z`. No shim transport failures appeared in the run log. `basic_patch`, `command_recovery`, `multi_file`, `plan_doc`, and `read_file` passed on first attempt; `bugfix_go` and `bugfix_mixed` passed on retry after first-attempt context/prompt leakage (`<permissions instructions>` / `<environment_context>`) instead of tool use. Treat this as functionally green but still retry-dependent and less disciplined than the best DeepSeek/MiMo runs. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 7/8 tasks passed | `checker_diff`: 1 | Follow-up Qwen run `run-20260430T172447Z`. No shim transport failure was present; the only failed task was `plan_doc`, where the model created a useful `PLAN.md` but missed the required final `PLANNED` marker. The run confirmed that shim model metadata alone was not visible in this custom-provider Codex eval path. A runner-level `developer_instructions` experiment was tried afterward and then removed after the next run regressed. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 5/8 tasks passed | `checker_diff`: 3 | Qwen discipline-instruction experiment `run-20260430T175456Z`. The generated Codex config carried `instructions_preset: qwen-codex-eval-discipline`, but the run regressed: `read_file` timed out then emitted `<resolve_conflicts>`, `multi_file` printed `<toolCall::apply_patch>` after editing files, and `bugfix_mixed` missed the required final marker. This suggests the extra instruction made the model focus on protocol text rather than improving structured tool discipline, so the harness-level injection was removed. |
| 2026-04-30 | Qwen3.6-35B-A3B | `codex-real-upstream` | 2 | 8/8 tasks passed | none | Last promoted Qwen baseline `run-20260430T182633Z` after removing the runner-level discipline instruction and clearing Qwen `base_instructions`. All tasks passed. It is still retry-dependent: `bugfix_mixed`, `multi_file`, and `plan_doc` needed second attempts; first attempts included `<antThinking>`, invalid `apply_patch` arguments, and missing final markers. The May 7 full auto run was not promotable, so keep this as historical green evidence rather than a current unattended gate. |

After the Qwen `run-20260430T165416Z` baseline, the Qwen model metadata gained
additional Codex-facing base instructions that forbid reproducing internal
context blocks and require structured tool calls for workspace state. The
`run-20260430T172447Z` check showed that the generated custom-provider Codex
eval config did not consume that metadata reliably. A runner-level additive
`developer_instructions` experiment made the next Qwen run worse and is not
retained. The follow-up run returned to 8/8 after removing the extra prompt.
The eval harness reports exact internal-context leaks under `context_leak`, so
future Qwen rows should distinguish prompt/context leakage from ordinary
checker diffs and provider-native raw tool markup rather than trying to steer
Qwen through extra global instructions.

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

DeepSeek V4 Pro remains the strongest broad API compatibility gate. It passed
the strict external tester profile after the Chat compatibility fixes and the
current control-vs-real Codex auto profiles. The latest baseline is green but
retry-dependent, so use it as the broad default gate while preferring a fresh
strict-clean rerun when a release needs a zero-retry Codex signal.

MiMo v2.5 Pro is now a green API-surface and Codex-eval candidate for chat-only
gateways when `responses.upstream_transport: chat_completions` is enabled. The
latest auto run passed baseline, expanded, and bench-lite without retries. It
still sits behind DeepSeek as the default broad API compatibility gate because
it does not prove native upstream Responses parity.

Qwen3.6-35B-A3B is currently not a promotable unattended Codex regression gate.
It remains useful as an experimental/manual smoke model and as a raw-markup
regression source because it exposes many pseudo-tool dialects. Its current
failures are more often model-command quality, sentinel discipline, or
provider-native raw markup than shim transport failures.

Kimi K2.6 is now a practical tuned-provider Codex candidate. The latest
baseline is strict-clean and the larger profiles are green, but retry-dependent
and affected by gateway noise. Treat it as a useful second model after the
default gate, not as proof that the provider path is transport-clean.

Qwen3 Coder 30B on the local GPU backend is promising as a fast supervised
assistant, but not as an unattended Codex gate. The current bench-lite evidence
shows high pass coverage, working `command_recovery`, and no hidden raw-markup
500s, but the repeated `patch_after_context` miss is exactly the kind of
partial-edit risk that must be caught by diff review and tests before merge.

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
3. Run `make codex-eval-auto` for the current Codex baseline model before
   promoting Codex-through-shim behavior.
4. Run MiMo when the change touches Responses-over-Chat or chat-only gateway
   behavior.
5. Run Kimi K2.6 as a second tuned-provider Codex check, especially for larger
   diagnostic or benchmark-lite profiles.
6. Run Qwen3.6-35B-A3B only for manual smoke, experimental coverage, or
   raw-markup/tool-discipline regression checks until a fresh auto baseline is
   promotable.
7. Move to manual Codex testing only after the automated smoke is green.

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
