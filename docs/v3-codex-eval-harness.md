# V3 Codex Eval Harness

Last updated: May 4, 2026.

Task id: `v3-codex-eval-harness`

Status: Phase 5 regression import workflow implemented; Phase 3 deterministic
core suite and profile gates implemented; Phase 6 benchmark-lite implemented
and validated against the current DeepSeek control-vs-real loops. Shim-native
Codex request-shape, `apply_patch` tool-mode profiles, and interactive
`exec_command -> session_id -> write_stdin` coverage are implemented in
dedicated profiles. Manual TUI feature exploration remains split into
[v3-codex-interactive-features-manual-plan.md](v3-codex-interactive-features-manual-plan.md).
Human review and baseline-promotion rules for generated auto-run artifacts are
kept in [v3-codex-eval-curation.md](v3-codex-eval-curation.md).

This task defines a repeatable evaluation and regression loop for running the
real Codex CLI through `llama_shim` against local or OpenAI-compatible upstream
models. The goal is to stop relying on one-off manual Codex sessions as the
primary compatibility signal.

Implemented slice through the current Phase 6 work:

- `cmd/codex-eval-runner`
- `internal/codexeval`
- manifest-backed task definitions under
  `internal/codexeval/testdata/tasks`
- `scripts/codex-eval-runner.sh`
- Make targets:
  - `make codex-eval-smoke`
  - `make codex-eval-core`
  - `make codex-eval-core-shell`
  - `make codex-eval-core-websocket`
  - `make codex-eval-core-interactive`
  - `make codex-eval-core-profiles`
  - `make codex-eval-compat`
  - `make codex-eval-automated-profiles`
  - `make codex-eval-bench-lite`
  - `make codex-eval-loop-bench-lite`
  - `make codex-eval-shim-native`
  - `make codex-eval-shim-native-websocket`
  - `make codex-eval-shim-native-apply-patch-freeform`
  - `make codex-eval-shim-native-apply-patch-function`
  - `make codex-eval-shim-native-apply-patch-disabled`
  - `make codex-eval-shim-native-apply-patch-profiles`
  - `make codex-eval-shim-native-profiles`
  - `make codex-eval-real-upstream`
  - `make codex-eval-real-upstream-expanded`
  - `make codex-eval-loop`
  - `make codex-eval-auto`
  - `make codex-eval-prune`
  - `make codex-eval-clean`
- isolated task workspace and `CODEX_HOME` per attempt
- generated Codex custom-provider config
- deterministic file, command, Codex event, request-shape, forbidden-event,
  and forbidden-output checkers
- local artifacts under `.tmp/codex-eval-runs/<run-id>/`
- task-id filtering and failed-task rerun from a previous `summary.json`
- failure bundle generation for frontier-model review:
  `go run ./cmd/codex-eval-runner failure-bundle .tmp/codex-eval-runs/<run-id>`
- markdown matrix generation from one or more `summary.json` files:
  `go run ./cmd/codex-eval-runner matrix .tmp/codex-eval-runs`

The implemented `codex-smoke` suite currently covers `boot`, `read_file`,
`basic_patch`, `bugfix_go`, `command_recovery`, `plan_doc`, and `multi_file`.
The `codex-core` suite now contains 20 deterministic tasks: the smoke set plus
`no_edit`, `stderr_handling`, `long_stdout`, `command_timeout`,
`command_pipeline`, `js_bugfix`, `python_bugfix`, `json_config_edit`,
`env_var_command`, `workdir_nested`, `patch_after_context`, `no_delete`, and
`shell_script_fix`.
Profile gates cover tool/transport axes that should not be mixed into the
normal real-upstream model comparison by default: `codex-core-shell` runs
fallback shell mode with `unified_exec=false`, `codex-core-websocket` runs
WebSocket transport tasks with `supports_websockets=true`, and
`codex-shim-native` / `codex-shim-native-websocket` verify redacted Codex
request shapes for HTTP and WebSocket transports. The
`write_stdin_pty` task is kept in `codex-core-interactive` and `codex-compat`
instead of the default core gate because it is a long-running interactive
process profile, not a normal non-interactive pre-commit core task. Focused
shim tests prove model-visible `session_id` preservation; the profile checks
raw Codex output for `READY_FOR_STDIN` and `STDIN_DONE codex-stdin-token`, so it
does not rely on final model text alone.
`make codex-eval-automated-profiles` runs the current automated non-manual
profile gates in one command. `make codex-eval-compat` is the broader
compatibility-suite entrypoint; today it intentionally overlaps with the
interactive profile, and future deterministic reductions from manual findings
should land there before any promotion discussion.

The `codex-real-upstream` suite tracks the current real-upstream-safe stable
subset plus the first mixed text-plus-file-change regression task,
`bugfix_mixed`, because that task requires real Codex file-change behavior
rather than the devstack command fixture. The
`codex-real-upstream-expanded` suite is an explicit diagnostic profile: it
starts from the stable real-upstream subset and adds practical command/edit/code
tasks (`command_pipeline`, `env_var_command`, `json_config_edit`, `js_bugfix`,
`python_bugfix`, `shell_script_fix`, and `workdir_nested`). Keep
high-noise or deliberately adversarial tasks such as `command_timeout`,
`no_delete`, and `patch_after_context` out of the expanded profile until a real
provider proves them stable enough to be useful.

Current DeepSeek V4 Pro evidence from May 4, 2026:

- `deepseek-v4-pro_baseline_20260504T063358Z`: `codex-core` control passed
  20/20 and `codex-real-upstream` candidate passed 11/11 with no retries.
- `deepseek-v4-pro_codex-real-upstream-expanded_20260504T065057Z`:
  `codex-core` control passed 20/20 and expanded candidate passed 18/18; two
  candidate tasks were retry-dependent.
- `deepseek-v4-pro_codex-bench-lite_20260504T081412Z`: `codex-bench-lite`
  control and candidate both passed 20/20; the candidate had one
  retry-dependent task, `patch_after_context`, caused by model formatting
  whitespace on the first attempt.

These are scratch artifact ids under `.tmp/codex-eval-loops/`, not committed
fixtures. They are recorded here only as the latest validation evidence for the
harness and should be regenerated when comparing future shim or model changes.

This is a V3 quality and automation track. It does not change the frozen V2
compatibility contract and must not strengthen any hosted OpenAI parity claim
until the implementation, fixtures, and tests prove that stronger claim.

## Why This Exists

The shim already has useful Codex coverage:

- deterministic devstack smoke coverage through `make devstack-full-smoke`
- real Codex CLI boot and command-tool smoke coverage
- real Codex CLI fallback shell-tool coverage
- a small deterministic task matrix in `scripts/codex-cli-task-matrix-smoke.sh`
- real-upstream smoke coverage in `scripts/codex-cli-real-upstream-smoke.sh`
- a manual phase-by-phase plan in `docs/guides/codex-testing-plan.md`

That coverage is enough to catch obvious breakage, but it is not enough to
evaluate the broader Codex workflow reliably. Manual use will keep finding
failures, but manual use alone does not produce:

- comparable pass/fail results across shim commits
- reproducible workspaces for failed tasks
- durable request, stream, tool, diff, and checker artifacts
- failure buckets that separate shim bugs from model-quality failures
- regression fixtures that can be rerun after a fix

The practical problem is not whether Codex can do one tiny edit once. The
problem is whether a model/provider/shim combination can repeatedly survive the
real Codex loop: model metadata, Responses transport, streamed tool calls,
local command execution, file edits, follow-up generation, state carry-over,
failure recovery, and output formatting.

## Official References Reviewed

This task was checked on April 29, 2026, re-checked on May 1, 2026, and
spot-checked again on May 4, 2026 for the Codex config and WebSocket
continuation sections against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Evaluation best practices](https://developers.openai.com/api/docs/guides/evaluation-best-practices)
  - [Evaluate agent workflows](https://developers.openai.com/api/docs/guides/agent-evals)
  - [Evaluate external models](https://developers.openai.com/api/docs/guides/external-models)
  - [Codex configuration reference](https://developers.openai.com/codex/config-reference)
  - [Codex app-server API overview](https://developers.openai.com/codex/app-server)
  - [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
  - [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
  - [WebSocket Mode](https://developers.openai.com/api/docs/guides/websocket-mode)
  - [Local shell](https://developers.openai.com/api/docs/guides/tools-local-shell)
  - [Function calling](https://developers.openai.com/api/docs/guides/function-calling)
- official public repo:
  - [OpenAI Codex](https://github.com/openai/codex)

Relevant docs-backed constraints:

- OpenAI evaluation guidance recommends task-specific evals, logging everything,
  automation where possible, and continuous evaluation.
- OpenAI external-model evals are useful for model comparison, but they do not
  currently cover tool-call evals. Codex-through-shim needs a local runner for
  tool-loop fidelity.
- Codex configuration supports OpenAI-compatible custom providers via
  `model_providers.<id>.base_url`, `wire_api = "responses"`, and
  provider-level `supports_websockets`.
- Current Codex documentation and source no longer support Chat Completions as a
  Codex provider wire API. Chat-only upstream compatibility remains a
  shim-owned bridge concern and is outside native Codex provider parity.
- Responses WebSocket mode uses repeated `response.create` events, optional
  `generate: false` warmup, and `previous_response_id` continuation with
  incremental inputs.
- Codex configuration supports `developer_instructions` as additional
  instructions, but this harness does not currently inject model-specific
  developer instructions by default. A Qwen-specific experiment on April 30,
  2026 made the run less stable by prompting the model to discuss protocol
  details in final text.
- The public `openai/codex` repo is the right implementation reference for the
  CLI tool registry and local execution behavior, but it is not the source of
  truth for OpenAI wire-contract claims.

## Codex Upstream Reference Inspected

An ignored local checkout of `openai/codex` was inspected at commit:

```text
9121132c8f5412ae99c36363409759baa7e004f9
```

Relevant implementation points observed:

- `codex-rs/model-provider-info/src/lib.rs` only accepts
  `wire_api = "responses"`; `wire_api = "chat"` is rejected by current Codex.
- `codex-rs/codex-api/src/common.rs` and `codex-rs/core/src/client.rs` show
  the current Responses request shape Codex sends: `model`, `instructions`,
  `input`, `tools`, `tool_choice = "auto"`, `parallel_tool_calls`,
  optional `reasoning`, `store`, `stream = true`, `include`,
  `service_tier`, `prompt_cache_key`, optional `text`, and
  `client_metadata`.
- The HTTP path adds `x-client-request-id` and `session_id` headers from the
  Codex conversation id. The WebSocket path can send `previous_response_id`
  with an incremental input delta after a reusable prior response.
- `codex-rs/codex-api/src/sse/responses.rs` maps a concrete subset of
  Responses stream events into Codex events, including output items, output
  text deltas, custom tool-call input deltas, reasoning summary/text deltas,
  `response.failed`, `response.incomplete`, and `response.completed`.
- `codex-rs/tools/src/tool_registry_plan.rs` builds the active tool registry
  from config and model metadata.
- Codex can expose several command-tool modes, including `unified_exec`,
  `shell_command`, `shell`, `local_shell`, and fallback function-tool variants.
- In `unified_exec` mode, Codex exposes both `exec_command` and `write_stdin`.
  `exec_command` can return a live session id for PTY or long-running command
  interaction, and `write_stdin` resumes that session.
- The registry can also expose `apply_patch` as either freeform or JSON
  function tool, `update_plan`, `request_permissions`, `request_user_input`,
  MCP resource tools, deferred tool search, dynamic tools, `view_image`,
  `web_search`, subagent tools, and agent-job tools depending on configuration.
- `codex-rs/protocol/src/openai_models.rs` confirms model metadata affects
  shell tool type, apply-patch tool type, web-search tool type, context window,
  auto-compaction threshold, truncation policy, parallel tool calls, image
  detail support, reasoning summary support, verbosity support, input
  modalities, and experimental supported tools.
- `docs/exec.md` points non-interactive execution at the public Codex
  non-interactive docs; local smoke scripts should keep using `codex exec
  --json` because that is the most stable automation surface for this repo.

These findings mean the harness must evaluate concrete Codex tool-mode
combinations rather than a generic "agent benchmark" only.

Source-informed gaps that should be represented in future task or profile
work are tracked in
[v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md):

- `write_stdin`/PTY interaction, because current timeout coverage proves
  command recovery but not interactive process continuation.
- WebSocket incremental continuation with `previous_response_id`, because this
  is a different Codex request shape than the normal HTTP full-context request.
- `apply_patch` as both freeform and function tool, because model metadata can
  switch the advertised tool contract. This is now covered by shim-native
  request-shape profiles rather than model-quality tasks.
- fallback shell variants (`shell`, `shell_command`, `local_shell`) as separate
  profiles, because they produce different tool schemas and handlers.
- request-permissions and request-user-input behavior as non-default profiles,
  because the normal non-interactive eval loop should avoid blocking on client
  approval or user-choice UI.
- MCP resource, deferred tool-search, dynamic-tool, and subagent registries as
  later compatibility profiles, not as part of the first real-upstream model
  quality comparison.
- model-metadata edge profiles for reasoning summaries, verbosity, truncation,
  context-window/auto-compaction, parallel tool calls, image input/detail, and
  experimental supported tools.

Manual TUI and app-server feature exploration is tracked separately in
[v3-codex-interactive-features-manual-plan.md](v3-codex-interactive-features-manual-plan.md).
That document covers slash commands such as `/goal`, `/review`, `/plan`, and
`/compact`, queued follow-ups, approval prompts, multi-agent tools, MCP
resources, apps, plugins, and external-agent import. Those checks should become
automated tasks only after a failure can be reduced to a deterministic runner
or shim-native profile case.

## Goal

Build a repo-owned eval harness that can:

1. Run real `codex exec --json` against a running shim.
2. Use isolated task workspaces and isolated `CODEX_HOME` directories.
3. Configure Codex provider/model flags per run.
4. Execute a curated task suite with deterministic checkers.
5. Capture enough artifacts to debug without rerunning immediately.
6. Produce machine-readable summaries for trend comparison.
7. Convert new manual failures into permanent regression tasks.
8. Support local free/cheap model loops such as Qwen 3.6 while preserving
   apples-to-apples comparison against other providers.

The first milestone should make daily local Codex regression runs practical.
It should not try to be a complete SWE-bench replacement.

## Non-Goals

Do not include in this V3 task:

- exact hosted Codex or OpenAI parity claims
- changing public OpenAI request/response contracts to make tests easier
- using an LLM judge as the authoritative pass/fail signal
- depending on OpenAI Platform Evals for Codex tool-call execution
- running untrusted benchmark repositories without sandbox and cleanup rules
- committing third-party benchmark checkouts, generated workspaces, raw secrets,
  or local absolute paths
- moving this into the frozen V2 release ledger

OpenAI Platform Evals can be used later for supplementary model comparison, but
the first useful gate must be a local deterministic runner because the core
thing under test is Codex tool execution through the shim.

## Design Principle

The harness should be layered:

- smoke tests catch obvious transport/tool-loop failures quickly
- the Codex eval harness catches broader workflow regressions
- manual use discovers new cases, then those cases become automated tasks
- frontier-model review helps classify failures and propose fixes, but never
  replaces deterministic task checkers

The checker owns pass/fail. A frontier model can explain why a failure happened.

## Proposed Repository Shape

Use repo-owned paths and keep generated artifacts under ignored directories:

```text
cmd/codex-eval-runner/
internal/codexeval/
internal/codexeval/testdata/tasks/
scripts/codex-eval-runner.sh
scripts/codex-eval-loop.sh
docs/v3-codex-eval-harness.md
.tmp/codex-eval-runs/
.tmp/codex-eval-loops/
```

The exact Go package split can change during implementation, but the important
boundary is:

- task definitions are committed
- reusable checkers are committed
- run artifacts are ignored
- real upstream secrets stay in the environment
- benchmark imports are pinned and sanitized before use

## Task Manifest

Each task should be a small directory with a manifest and optional fixture
files:

```text
internal/codexeval/testdata/tasks/basic_patch/
  task.yaml
  workspace/
    smoke_target.txt
```

Initial manifest shape:

```yaml
id: basic_patch
title: Single file deterministic patch
category: edit
timeout: 180s
attempts: 2
requires:
  codex_features:
    unified_exec: true
    apply_patch_freeform: true
  shim:
    websocket: optional
prompt: |
  Update smoke_target.txt by replacing `status = TODO` with
  `status = patched-by-codex`. Then reply PATCHED.
expected:
  final_text_contains:
    - PATCHED
  files:
    - path: smoke_target.txt
      equals: |
        name = llama_shim
        status = patched-by-codex
  codex_events:
    - item.started:command_execution
    - item.completed:agent_message
  forbidden_output:
    - "<|tool_call"
```

The schema should support:

- exact file content checks
- regex file checks
- file existence and non-existence checks
- JSON file checks
- command checkers such as `go test ./...`
- expected Codex JSON event presence
- forbidden Codex JSON event presence
- forbidden Codex JSON event or text markers
- expected shim log markers when debug logging is enabled
- minimum command-execution count for recovery tasks
- maximum tool-call count
- maximum wall-clock duration
- per-task retry count
- per-task model/provider tags
- task quarantine metadata for known flaky upstream/model combinations

## Runner Inputs

The runner should accept at least:

```bash
codex-eval-runner \
  --shim-base-url http://127.0.0.1:8080 \
  --codex-bin codex \
  --model Qwen3.6-35B-A3B \
  --provider gateway-shim \
  --api-key-env GW_API_KEY \
  --suite codex-core \
  --out .tmp/codex-eval-runs/qwen36-20260429
```

Environment and flags should cover:

- `SHIM_BASE_URL`
- `CODEX_BIN`
- `CODEX_MODEL`
- `CODEX_PROVIDER`
- `CODEX_BASE_URL`
- `CODEX_API_KEY_ENV`
- `CODEX_API_KEY`
- `CODEX_EVAL_SUITE`
- `CODEX_EVAL_TASKS`
- `CODEX_EVAL_RERUN_FAILED_FROM`
- `CODEX_EVAL_OUT`
- `CODEX_EVAL_PARALLELISM`
- `CODEX_EVAL_ATTEMPTS`
- `CODEX_EVAL_REASONING_EFFORT`
- `CODEX_EVAL_REASONING_SUMMARY`
- `CODEX_EVAL_WEBSOCKETS`
- `CODEX_EVAL_UNIFIED_EXEC`
- `CODEX_EVAL_APPLY_PATCH_FREEFORM`
- `CODEX_EVAL_SHELL_TOOL_TYPE`
- `CODEX_EVAL_APPLY_PATCH_TOOL_TYPE`
- `CODEX_EVAL_MODEL_METADATA_PROFILE`
- `CODEX_EVAL_STREAM_IDLE_TIMEOUT_MS`
- `CODEX_EVAL_STREAM_MAX_RETRIES`
- `CODEX_EVAL_REQUEST_MAX_RETRIES`
- `CODEX_EVAL_WEBSOCKET_CONNECT_TIMEOUT_MS`
- `CODEX_EVAL_KEEP_WORKSPACES`
- `CODEX_EVAL_LOOP_OUT`
- `CODEX_EVAL_LOOP_STRICT_REAL_UPSTREAM`
- `CODEX_EVAL_CONTROL_RUN`
- `CODEX_EVAL_MODELS`
- `CODEX_EVAL_CONTROL_SHIM_BASE_URL`
- `CODEX_EVAL_CONTROL_MODEL`
- `CODEX_EVAL_CONTROL_SUITE`
- `CODEX_EVAL_CANDIDATE_SUITE`
- `CODEX_EVAL_AUTO_OUT`
- `CODEX_EVAL_AUTO_PROFILES`
- `CODEX_EVAL_AUTO_STRICT`
- `CODEX_EVAL_NOTIFY`
- `CODEX_EVAL_SHIM_LOG`

Default to serial execution for the first version. Codex tasks mutate
workspaces, produce logs, and can stress one upstream model; parallelism should
be explicit.

Run an explicit subset by task id:

```bash
CODEX_EVAL_TASKS=basic_patch,bugfix_mixed \
  CODEX_EVAL_SUITE=codex-real-upstream \
  bash ./scripts/codex-eval-runner.sh
```

Run the expanded real-upstream profile directly:

```bash
CODEX_EVAL_SUITE=codex-real-upstream-expanded \
  bash ./scripts/codex-eval-runner.sh
```

Rerun only tasks that failed in an earlier run. The value can be either a run
directory or the exact `summary.json` path:

```bash
CODEX_EVAL_RERUN_FAILED_FROM=.tmp/codex-eval-runs/run-20260430T215102Z \
  CODEX_EVAL_SUITE=codex-real-upstream \
  bash ./scripts/codex-eval-runner.sh
```

When explicit task ids or failed-task rerun are used, the runner resolves task
ids across all committed task manifests rather than only the named suite. The
suite still records the intended run profile in the output environment.

## Runner Outputs

For every run:

```text
.tmp/codex-eval-runs/<run-id>/
  summary.json
  summary.md
  environment.json
  tasks/
    basic_patch/
      task.yaml
      codex-config.toml
      codex.jsonl
      codex.stderr.log
      shim.log.slice.jsonl
      workspace-before/
      workspace-after/
      git.diff
      checker.json
      failure.md
```

To summarize multiple runs after testing several models:

```bash
go run ./cmd/codex-eval-runner matrix .tmp/codex-eval-runs
```

Or write the generated markdown to a local artifact:

```bash
go run ./cmd/codex-eval-runner matrix \
  --out .tmp/codex-eval-runs/matrix.md \
  .tmp/codex-eval-runs
```

The generated matrix is mechanical: date, run id, model, suite, suite scope,
pass count, retry-dependent task count, failure buckets, and failed tasks. Keep
the human-written interpretation in
`docs/engineering/codex-upstream-model-matrix.md`.

To compare a deterministic control run against one or more real-upstream runs:

```bash
go run ./cmd/codex-eval-runner compare \
  --control .tmp/codex-eval-loops/<loop-id>/control \
  --out .tmp/codex-eval-loops/<loop-id>/compare.md \
  --json-out .tmp/codex-eval-loops/<loop-id>/summary.json \
  .tmp/codex-eval-loops/<loop-id>/candidate-*
```

The compare report classifies each task with a mechanical diagnosis:

- `control_failed`: the deterministic control failed, so the issue is in the
  harness, local shim path, fixture, or environment before model quality should
  be considered;
- `candidate_transport`: the control passed and the real-upstream run failed
  in auth, shim transport, upstream HTTP/streaming, or timeout handling;
- `candidate_tool_contract`: the control passed and the real-upstream run
  failed with bad tool arguments, raw provider-native tool markup, or a missing
  Codex tool contract;
- `candidate_model_behavior`: the control passed and the real-upstream run
  reached Codex but failed task semantics, final text, or model persistence;
- `retry_dependent`: the real-upstream task passed only after a failed earlier
  attempt;
- `ok`: the control and real-upstream task both passed without retry.

Tasks that exist in only one compared suite are reported as coverage
differences, not quality diagnoses. The report also labels each run with a
suite scope such as `control`, `real-stable`, or `real-expanded`, so a stable
11-task real-upstream run is not mistaken for the same coverage claim as the
20-task deterministic control. For example, a `codex-core`-only timeout task
and a `codex-real-upstream`-only mixed text/tool task should not make an
otherwise green candidate look like a tool or transport regression.

For a full local model qualification pass, use the auto wrapper. It runs the
normal baseline, expanded diagnostic, and benchmark-lite profile in one command,
captures a shim log slice for each profile, then writes one top-level
`summary.md` and `summary.json`:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
CODEX_PROVIDER=gateway-shim \
CODEX_API_KEY_ENV=GW_API_KEY \
GW_API_KEY=sk-... \
CODEX_EVAL_MODELS="deepseek-v4-pro" \
CODEX_EVAL_ATTEMPTS=2 \
make codex-eval-auto
```

By default, `CODEX_EVAL_AUTO_PROFILES` is
`baseline,expanded,bench-lite`:

- `baseline`: `codex-core` control vs `codex-real-upstream` candidate;
- `expanded`: `codex-core` control vs `codex-real-upstream-expanded`
  candidate;
- `bench-lite`: `codex-bench-lite` control vs `codex-bench-lite` candidate.

The default exit policy is `CODEX_EVAL_AUTO_STRICT=baseline`: the command exits
non-zero when the baseline profile fails, while expanded and benchmark-lite
failures are still reported as diagnostics. Set `CODEX_EVAL_AUTO_STRICT=all`
when every profile must be green for a zero exit code, or `none` when collecting
diagnostics should never fail the command.

At completion the wrapper sends a small local notification. The default is
`CODEX_EVAL_NOTIFY=bell`, which prints a terminal bell character. Use
`CODEX_EVAL_NOTIFY=macos` to also attempt a best-effort macOS notification via
`osascript`, or `CODEX_EVAL_NOTIFY=off` to disable notifications.

Auto artifacts live under `.tmp/codex-eval-auto/<auto-id>/`:

```text
.tmp/codex-eval-auto/<auto-id>/
  summary.md
  summary.json
  profiles/
    baseline/
      compare.md
      matrix.md
      summary.json
      failure-bundle.md
      shim.log.slice
      shim-log-diagnostics.md
      loop.log
    expanded/
    bench-lite/
```

Use `summary.md` first. It shows profile pass counts, retry-dependent tasks,
failed tasks, coverage differences, and links to each profile's compare report
and shim-log diagnostics. The generated report is mechanical; follow
[v3-codex-eval-curation.md](v3-codex-eval-curation.md) before copying only the
interpreted facts into `docs/engineering/codex-upstream-model-matrix.md`.

The auto wrapper reuses a previously completed control run when multiple
profiles use the same control suite. In the default profile set this means the
`expanded` profile reuses the `baseline` profile's `codex-core` control instead
of running the same 20 deterministic control tasks again. Candidate runs are not
deduplicated, because they intentionally test different real-upstream profiles
and retry behavior.

For a lower-level control-vs-real loop over one suite, use `make
codex-eval-loop`. It first runs the deterministic control, then each
real-upstream model, then generates matrix, compare, JSON summary, and
failure-bundle artifacts:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
CODEX_PROVIDER=gateway-shim \
CODEX_API_KEY_ENV=GW_API_KEY \
GW_API_KEY=sk-... \
CODEX_EVAL_MODELS="deepseek-v4-pro,kimi-k2,Qwen3.6-35B-A3B" \
CODEX_EVAL_ATTEMPTS=2 \
make codex-eval-loop
```

To reuse an existing control run in a focused lower-level loop, set
`CODEX_EVAL_CONTROL_RUN` to a run directory containing `summary.json`.

For a larger real-upstream diagnostic pass, switch only the candidate suite:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
CODEX_PROVIDER=gateway-shim \
CODEX_API_KEY_ENV=GW_API_KEY \
GW_API_KEY=sk-... \
CODEX_EVAL_MODELS="deepseek-v4-pro" \
CODEX_EVAL_CANDIDATE_SUITE=codex-real-upstream-expanded \
CODEX_EVAL_ATTEMPTS=2 \
make codex-eval-loop
```

Loop artifacts live under `.tmp/codex-eval-loops/<loop-id>/`. Single-model
`codex-real-upstream` loops default to
`.tmp/codex-eval-loops/<model>_baseline_<timestamp>/`; expanded or custom
suites use `<model>_<suite>_<timestamp>` unless `CODEX_EVAL_LOOP_OUT` is set:

```text
.tmp/codex-eval-loops/<loop-id>/
  control/
    summary.json
  candidate-<model>/
    summary.json
  matrix.md
  compare.md
  summary.json
  failure-bundle.md
  failure-bundles/
```

To clear local eval artifacts after extracting the reports you need:

```bash
make codex-eval-clean
```

To keep summaries, compare reports, Codex JSONL, diffs, and workspace
snapshots while dropping transient Codex plugin clones and similar
`CODEX_HOME` temp payloads:

```bash
make codex-eval-prune
```

By default, candidate failures do not stop the loop after their own run because
failed real-upstream candidates are the data being collected. Set
`CODEX_EVAL_LOOP_STRICT_REAL_UPSTREAM=true` when the loop should return a
non-zero exit code if any real-upstream candidate fails.

To package failed-task artifacts for review:

```bash
go run ./cmd/codex-eval-runner failure-bundle \
  --out .tmp/codex-eval-runs/failure-bundle.md \
  .tmp/codex-eval-runs/<run-id>
```

The failure bundle includes failed task ids, status and failure buckets, attempt
metadata, Codex event summaries, checker failures, final text, copied
`task.yaml`, `git.diff`, and `failure.md` when those artifacts exist. It is a
local debug artifact and should not be committed.

To turn a failed task attempt into a committed regression-task skeleton:

```bash
go run ./cmd/codex-eval-runner import-failure \
  --task basic_patch \
  --attempt 1 \
  --out imported_basic_patch_regression \
  .tmp/codex-eval-runs/<run-id>
```

The import command rejects tasks whose final status is `passed`, `skipped`, or
`quarantined`; it is meant for failed outcomes, not retry-dependent green runs.
If `--attempt` is omitted, the last failed attempt is imported.

Generated import layout:

```text
internal/codexeval/testdata/tasks/<new-task-id>/
  task.yaml
  workspace/
  import_artifacts/
    README.md
    source.json
    source_task.yaml
    git.diff
    final_text.txt
    checker_failures.md
    workspace-before/
    workspace-after/
```

The generated `task.yaml` is intentionally valid but isolated in the
`codex-regression-import` suite with TODO prompt/checker fields. It should not
be moved into `codex-core`, `codex-smoke`, or `codex-real-upstream` until the
task is minimized and deterministic.

Before committing an imported regression:

- replace the TODO prompt with the smallest task that reproduces the failure;
- replace the TODO checker with deterministic file, command, and Codex-event
  checks;
- remove provider chatter, secrets, raw `.tmp` paths, local absolute paths, and
  unrelated generated files;
- keep only the minimal `workspace/` fixture needed to reproduce the issue;
- delete or trim `import_artifacts/` if the copied diagnostics are no longer
  needed for review;
- run `go test ./internal/codexeval` and the target eval suite.

That split is intentional:

- generated matrix output is an audit trail and quick comparison view copied
  directly from `summary.json`;
- the model matrix document is where a human records interpretation: whether a
  retry is acceptable, whether a failure was shim transport, model behavior, or
  task/checker wording, and which model should be used for the next gate;
- do not edit generated counts by hand; rerun the matrix generator instead;
- do not paste every historical generated row into the model matrix. Keep only
  meaningful baselines and explain why they matter.

Manual matrix-doc updates should therefore carry interpretation only: why a
retry-dependent green result is acceptable or not, whether a failure points at
shim transport, task wording, model tool discipline, or Codex config, and which
run should be treated as the current baseline. Counts, buckets, and failed task
ids come from generated `summary.json`/matrix output.

`summary.json` should include:

- run id
- timestamp
- shim git commit
- Codex binary path and version
- Codex upstream reference SHA if available
- model slug
- provider id
- base URL with secrets redacted
- WebSocket setting
- unified exec setting
- reasoning setting
- task counts by status
- pass rate
- failure buckets
- task duration statistics
- path to every task artifact directory

Task status values:

- `passed`
- `failed_checker`
- `failed_codex_exit`
- `failed_transport`
- `failed_no_tool_event`
- `failed_no_final_answer`
- `failed_raw_tool_markup`
- `failed_timeout`
- `failed_setup`
- `skipped`
- `quarantined`

## Artifact Rules

Artifacts must be useful for automated analysis but safe to keep locally:

- redact bearer tokens, API keys, cookies, and authorization headers
- store request ids and client request ids when present
- keep Codex JSONL exactly enough to replay event classification
- keep shim log slices bounded by request id or run window
- keep before/after workspace snapshots for small committed fixture tasks
- do not retain generated dependency/cache directories in saved workspaces or
  workspace snapshots (`.cache`, `.gocache`, `.pytest_cache`, `__pycache__`,
  `node_modules`)
- do not retain Codex plugin catalog downloads under per-attempt `codex-home`
- for large tasks, store a git diff plus checker output instead of full copies
- never commit run artifacts
- never write local absolute paths into committed task manifests or docs

## Initial Suites

### `codex-smoke`

Purpose: fast local gate, similar to the current task matrix but running through
the new runner.

Tasks:

- `boot`
- `read_file`
- `basic_patch`
- `bugfix_go`
- `command_recovery`
- `plan_doc`
- `multi_file`

Target runtime: under 10 minutes for one model/provider.

### `codex-core`

Purpose: daily local regression signal for the current practical Codex subset.

Task families:

- boot and provider metadata
- read-only shell command
- single-file edit
- multi-file edit
- tiny Go bugfix with tests
- tiny TypeScript or JavaScript bugfix with tests
- command failure recovery
- command timeout recovery
- shell pipeline command behavior
- long stdout truncation
- stderr handling
- no-op task where Codex should not edit files
- plan document generation with required semantic markers
- mixed natural-language preamble plus tool edit
- patch after reading context from multiple files
- apply-patch/freeform path
- apply-patch/function path
- fallback shell path with `unified_exec=false`
- local-shell path when Codex exposes `local_shell`
- WebSocket-enabled path
- WebSocket incremental continuation after a previous response id
- HTTP-first path
- raw tool-call markup rejection

Target runtime: under 45 minutes for one model/provider.

### `codex-real-upstream`

Purpose: stable real-provider gate for the subset that is cheap enough and
predictable enough to run repeatedly while comparing models.

Tasks:

- stable smoke/core tasks: `boot`, `read_file`, `basic_patch`, `bugfix_go`,
  `command_recovery`, `plan_doc`, `multi_file`, `long_stdout`, `no_edit`, and
  `stderr_handling`
- real-only regression task: `bugfix_mixed`

Target runtime: under 20 minutes for one model/provider in normal conditions.

### `codex-real-upstream-expanded`

Purpose: larger real-provider diagnostic pass. This is not the default gate; use
it when evaluating a model before updating the model matrix or promoting more
tasks into the stable real-upstream suite.

Additional tasks beyond `codex-real-upstream`:

- `command_pipeline`
- `env_var_command`
- `json_config_edit`
- `js_bugfix`
- `python_bugfix`
- `shell_script_fix`
- `workdir_nested`

Excluded for now: `command_timeout`, `no_delete`, and `patch_after_context`.
Those remain deterministic-control coverage until they are proven stable enough
across real providers.

### `codex-compat`

Purpose: broader deterministic compatibility and regression discovery outside
the default core gate.

Current automated coverage:

- `write_stdin_pty`, shared with `codex-core-interactive`, verifies
  `exec_command -> session_id -> write_stdin` on a live PTY process and checks
  raw Codex output for both the pre-stdin and post-stdin markers.

Run command:

```bash
make codex-eval-compat
```

Future additions must start as small deterministic reductions from observed
manual failures or shim-native profile gaps. Candidate families include
same-session continuity, transport/retry variants, model-metadata variants,
tool-availability variants, response-formatting variants, and imported manual
regressions. API-level compaction remains covered by devstack and external
Responses compatibility smokes; Codex slash-command `/compact` belongs to the
manual interactive feature plan until it has a deterministic non-TUI reduction.

Target runtime: allowed to be long; not a normal pre-commit gate.

### `codex-bench-lite`

Purpose: longer-running, repo-owned benchmark-lite breadth after the in-repo
harness is stable.

Rules:

- pin benchmark-source inspiration in manifest provenance metadata
- copy only small sanitized task definitions into repo-owned fixtures when a
  third-party source is actually imported
- keep source attribution in task metadata
- prefer tasks with deterministic local checkers
- do not include tasks that require network during execution unless the task is
  explicitly tagged as network-required
- avoid large third-party repos in the first milestone

Candidate sources can include agent/coding benchmark tasks, but they must be
adapted into this repo's task manifest and checker model before becoming a gate.
The current first milestone uses 20 repo-owned synthetic tasks with local
fixtures only. They are inspired by the public shape of
[SWE-bench](https://www.swebench.com/SWE-bench/) and
[Terminal-Bench](https://www.tbench.ai/benchmarks), but no third-party task
files are imported.
`bugfix_mixed` stays out of `codex-bench-lite` because its useful signal is the
real Codex `file_change` event after a natural-language preamble. The devstack
fixture can patch that workspace through command execution, but it cannot
honestly stand in for a real model choosing Codex apply-patch/file-change
behavior.

Commands:

```bash
make codex-eval-bench-lite
```

For control-vs-real stability checks, run the same bench-lite suite on both
the devstack control and the candidate upstream:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
CODEX_MODEL=<model> \
CODEX_PROVIDER=gateway-shim \
CODEX_API_KEY_ENV=GW_API_KEY \
make codex-eval-loop-bench-lite
```

## Failure Buckets

The runner should classify failures before any LLM-assisted analysis:

- `codex_config`: Codex cannot load provider/config/model metadata.
- `shim_auth`: shim rejects the Codex request before model execution.
- `shim_transport`: HTTP/SSE/WebSocket request fails or wrong status appears.
- `upstream_http`: upstream request returns non-2xx or malformed payload.
- `upstream_stream`: upstream stream disconnects or never completes.
- `upstream_incomplete`: upstream emits `response.incomplete`.
- `upstream_response_failed`: upstream emits `response.failed`.
- `model_no_tool`: model answers text but never calls the required tool.
- `model_bad_tool_args`: model calls a tool with invalid or empty arguments.
- `codex_tool_missing`: Codex receives a call but reports unsupported tool.
- `codex_tool_exec`: local command or patch handler fails unexpectedly.
- `checker_diff`: Codex completed but final workspace is wrong.
- `checker_tests`: Codex completed but repository tests fail.
- `raw_tool_markup`: model leaked provider-native tool markup to Codex text.
- `context_leak`: model quoted Codex prompt/context blocks such as
  `<environment_context>` or `<permissions instructions>` in final text.
- `timeout`: task exceeded configured wall-clock limit.
- `harness_bug`: setup/checker/artifact capture failed.

These buckets should be machine-readable in `summary.json` and human-readable
in `summary.md`.

## Frontier Review Loop

The harness should make LLM-assisted triage cheap, but keep it separate from
pass/fail:

1. Run `codex-eval-runner`.
2. Collect failed task artifacts.
3. Generate a compact failure bundle:
   - task prompt
   - manifest
   - checker result
   - Codex JSON event summary
   - shim log slice summary
   - final diff
4. Ask a frontier coding model to classify likely root cause and propose a fix.
5. Human or Codex implements the shim/script/test change.
6. Add or update a deterministic regression task.
7. Re-run the failed task, then the suite.

The frontier model should answer questions like:

- Is this likely shim transport, upstream model behavior, Codex local tool
  registration, or task/checker design?
- Which request or event first diverged?
- Is the fix in shim compatibility handling, Codex model metadata, task prompt,
  or the checker?
- Should this become a new permanent regression task?

The frontier model should not decide that a failed deterministic checker
actually passed.

## Implementation Phases

Current status on May 4, 2026:

- Phase 0 is complete: the previous smoke scripts remain documented and
  available.
- Phase 1 is implemented: the runner, manifests, isolated workspaces,
  `CODEX_HOME`, summaries, artifacts, and deterministic checkers exist.
- Phase 2 is implemented for the eval harness. Older smoke scripts remain as
  compatibility canaries instead of being silently rewritten into the runner.
- Phase 3 is implemented for deterministic core coverage and first
  tool/transport profile gates: `codex-core` has 20 deterministic tasks,
  `codex-core-shell` covers fallback shell mode, and
  `codex-core-websocket` covers WebSocket mode plus a tool-follow-up
  continuation path. `codex-core-interactive` covers
  `exec_command -> session_id -> write_stdin` PTY continuation without
  promoting it into the default core gate. The shim-native request-shape
  profile is implemented and
  tracked in
  [v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md),
  including HTTP, WebSocket, and `apply_patch` model-metadata tool-mode
  profiles. The detailed interactive bridge evidence is tracked in
  [v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md).
  API-level compaction is covered by devstack and external Responses
  compatibility smokes. Codex slash-command `/compact` remains a manual TUI
  feature until a deterministic non-TUI reduction exists.
- Phase 4 daily-loop tooling is implemented: real-upstream runs, manifest
  quarantine, task-id filtering, failed-task rerun, matrix generation, and
  packaged failure review bundles exist.
- A Qwen-specific `developer_instructions` injection experiment was tried and
  removed. It reduced the April 30 Qwen eval result from the prior 7/8 and 8/8
  baselines to 5/8 by increasing protocol-shaped final text such as
  `<resolve_conflicts>` and `<toolCall::apply_patch>`.
- Phase 5 regression import workflow is implemented.
- Phase 6 benchmark-lite is implemented as `codex-bench-lite`: 20 repo-owned
  deterministic tasks with manifest provenance metadata and Make targets for
  local and control-vs-real runs.

The automatable eval-harness work is closed as of this status update. Remaining
work is intentionally outside the main automated phases: manual TUI feature
exploration and future MCP/multi-agent/app-server tasks only after they have
small deterministic reductions.

### Phase 0: Preserve Current Smoke Behavior

Deliverables:

- document the existing smoke scripts as the baseline
- add no behavioral changes
- confirm `make codex-cli-task-matrix-smoke` still covers the existing four
  deterministic tasks
- confirm `make codex-cli-real-upstream-smoke` remains the current manual gate
  for local upstreams such as Qwen 3.6

Exit criteria:

- this document is linked from `docs/v3-scope.md`
- no compatibility wording is widened

### Phase 1: Manifest And Runner Skeleton

Deliverables:

- `cmd/codex-eval-runner`
- manifest parser and validation
- isolated workspace creation
- isolated `CODEX_HOME` creation
- generated Codex provider config
- serial task execution
- captured `codex exec --json` output
- deterministic file and command checkers
- `summary.json` and `summary.md`

Initial tasks:

- `boot`
- `read_file`
- `basic_patch`
- `bugfix_go`
- `command_recovery`
- `plan_doc`
- `multi_file`

Exit criteria:

- `codex-eval-runner --suite codex-smoke` passes against devstack fixture mode
- generated artifacts are ignored and bounded
- existing smoke scripts still pass

### Phase 2: Replace Script Duplication With Shared Runner Logic

Deliverables:

- keep old Make targets stable
- route new eval Make target through `codex-eval-runner`
- optionally adapt current smoke scripts to call the runner for common setup
- preserve exact current script behavior where CI depends on it

New Make targets:

```make
codex-eval-smoke
codex-eval-core
codex-eval-real-upstream
```

Exit criteria:

- `make codex-eval-smoke` passes locally against devstack
- `make devstack-full-smoke` remains green
- no old documented command breaks

### Phase 3: Core Codex Workflow Coverage

Deliverables:

- command failure recovery task
- no-edit safety task
- stderr handling task
- long stdout handling task
- mixed text plus file-change task
- raw tool markup detection
- per-task failure bucket classification
- command timeout task
- command pipeline task
- `write_stdin`/PTY interaction task in `codex-core-interactive` and
  `codex-compat`
- fallback shell mode task through the `codex-core-shell` profile
- WebSocket mode task through the `codex-core-websocket` profile
- WebSocket tool-follow-up continuation task through the
  `codex-core-websocket` profile
- API compaction covered by devstack/external Responses smokes, with
  slash-command `/compact` left in the manual TUI plan

Exit criteria:

- `codex-core` has at least 20 deterministic tasks
- every failure has a bucket
- failed task artifacts are enough to debug without re-running the whole suite

Implemented Phase 3 profile commands:

```bash
make codex-eval-core
make codex-eval-core-shell
make codex-eval-core-websocket
make codex-eval-core-interactive
make codex-eval-core-profiles
make codex-eval-compat
make codex-eval-automated-profiles
make codex-eval-shim-native
make codex-eval-shim-native-websocket
make codex-eval-shim-native-profiles
```

### Phase 4: Real-Upstream Daily Loop

Deliverables:

- documented Qwen 3.6 run profile
- documented Kimi or DeepSeek profile if still useful
- per-model expected-quarantine support
- suite comparison summary across models/providers
- one-command "run failed tasks only" mode

Exit criteria:

- local Qwen 3.6 run produces a stable `summary.json`
- failures can be rerun by id
- failures can be packed for frontier-model review

Implemented so far:

- `CODEX_EVAL_TASKS` and `--tasks` run explicit task ids.
- `CODEX_EVAL_RERUN_FAILED_FROM` and `--rerun-failed-from` rerun tasks whose
  previous `summary.json` status was not `passed`, `skipped`, or
  `quarantined`.
- The matrix generator summarizes multiple local run directories.
- The compare generator classifies deterministic control versus real-upstream
  candidate differences.
- `make codex-eval-loop` runs the control, candidate models, matrix, compare,
  JSON summary, and failure-bundle generation in one command.
- The `failure-bundle` subcommand packages failed-task artifacts into one
  markdown file for frontier-model review.
- The `import-failure` subcommand turns a failed task attempt into a sanitized
  regression-task skeleton under the committed task tree.
- Current per-model baselines are recorded in
  `docs/engineering/codex-upstream-model-matrix.md`.

### Phase 5: Regression Import Workflow

Deliverables:

- command to import a failed manual run into a new task skeleton: implemented
- task minimization checklist: implemented
- fixture sanitization checklist: implemented
- reviewer template for failure analysis: covered by `failure-bundle`
- docs update explaining how manual sessions become automated regression cases:
  implemented

Exit criteria:

- at least three historical/manual Codex failures are converted into permanent
  deterministic tasks
- each converted task has a checker and failure bucket

### Phase 6: Benchmark-Lite Expansion

Deliverables:

- identify candidate external coding-agent benchmark sources: implemented with
  SWE-bench and Terminal-Bench as first source-shape references
- choose only small deterministic tasks: implemented with 20 repo-owned tasks
- pin provenance in task metadata: implemented through manifest `provenance`
- adapt tasks into the repo-owned manifest/checker model: implemented; no
  third-party fixtures are imported
- keep third-party source imports out of normal CI unless explicitly enabled:
  implemented; `codex-bench-lite` is an explicit target only

Exit criteria:

- `codex-bench-lite` exists with at least 10 tasks: implemented with 20 tasks
- every task has deterministic local pass/fail: implemented with file, command,
  event, and forbidden-output checkers
- no network-required or long-running task is in the default local gate:
  implemented; the suite is not part of the default local gate

## Checker Requirements

A task is valid only if it has at least one deterministic checker.

Preferred checkers:

- exact file content
- regex file content
- `git diff --exit-code` style no-change check
- targeted `go test ./...` or package-specific tests
- targeted `npm test` or equivalent when fixture dependencies are local
- JSON shape checks through `jq`
- Codex JSON event checks
- shim log marker checks for transport-specific tasks

Avoid:

- final assistant text as the only success criterion
- model-judge-only scoring
- prompts whose success depends on unstated style preferences
- tasks that pass even when no tool was called
- tasks that pass if Codex edits the wrong file but says the right sentinel

## Minimum Regression Coverage Split

Before marking this task complete, the harness should cover the default and
real-upstream-safe items below. Shim-native profile items are tracked in
[v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md) so they do
not block closing the eval-harness phases that are already implemented.

- Codex boot through custom provider
- authorized `/v1/models` probe through shim
- HTTP-first Responses path
- WebSocket-enabled Responses path
- `unified_exec=true`
- `unified_exec=false`
- local command execution
- command stdout, stderr, non-zero exit, and timeout
- long-running command continuation through `write_stdin`
  (`codex-core-interactive`)
- single-file edit
- multi-file edit
- patch-style file change
- freeform, function, and disabled `apply_patch` tool modes
- tiny code bugfix plus test run
- deterministic documentation writing
- no-edit safety task
- mixed text/tool stream
- raw provider tool markup rejection
- final answer after tool output
- HTTP Responses request shape and headers used by Codex
- WebSocket Responses request shape, including incremental continuation with
  `previous_response_id`
- at least one real-upstream Qwen 3.6 profile run

## Guardrails

The harness must not introduce hidden OpenAI-surface regressions:

- do not add public request limits just to make tasks pass
- do not reject official Responses fields only because a local upstream is weak
- do not change `/v1/responses` public behavior for harness convenience
- do not claim exact hosted parity from Codex task success alone
- do not treat a model-quality failure as a shim compatibility success
- do not treat a shim transport failure as a model-quality failure
- keep all operational limits internal and documented if new limits are needed

If a task exposes a resource-bound issue, apply the repo's existing security
rules:

- fix sibling paths, not only the one task
- avoid full materialization on hot paths
- add focused tests for the bound/helper
- run `go test ./...`, `make lint`, and `git diff --check` before closing

## Documentation Updates Required

When this task is implemented, update:

- `docs/v3-scope.md`
- `docs/guides/codex-cli.md`
- `docs/guides/codex-testing-plan.md` if the manual workflow changes
- `docs/engineering/responses-compatibility-external-tester.md` if the real
  upstream gate changes
- `docs/compatibility-matrix.md` only if an implementation change affects an
  existing compatibility row
- `docs/engineering/openai-api-choreography-atlas.md` if the implementation
  changes Responses state flow, SSE replay, WebSocket transport, tool routing,
  Codex behavior, compaction, or routing-mode semantics

## Done Criteria

This V3 task is done when:

- the runner exists and is documented
- `codex-smoke`, `codex-core`, `codex-real-upstream`, and
  `codex-real-upstream-expanded` suites exist
- `codex-core` contains at least 20 deterministic tasks
- `codex-real-upstream-expanded` is documented as diagnostic coverage, not the
  default stable real-upstream gate
- failed runs produce enough artifacts for offline diagnosis
- task manifests validate before execution
- generated artifacts are ignored and redacted
- the old smoke targets still work
- at least one local real-upstream model profile such as Qwen 3.6 is documented
- at least three prior manual failure modes are permanent regression tasks
- shim-native request-shape, `write_stdin`, and `apply_patch` profile coverage
  are implemented in dedicated profiles and documented in
  [v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md) and
  [v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md)
- `go test ./...` passes
- `make lint` passes
- `git diff --check` passes
- no V2 or OpenAI parity wording overclaims what the harness proves
