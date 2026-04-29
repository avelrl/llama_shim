# V3 Codex Eval Harness

Last updated: April 29, 2026.

Task id: `v3-codex-eval-harness`

Status: Phase 1 implemented, Phase 2+ pending.

This task defines a repeatable evaluation and regression loop for running the
real Codex CLI through `llama_shim` against local or OpenAI-compatible upstream
models. The goal is to stop relying on one-off manual Codex sessions as the
primary compatibility signal.

Implemented Phase 1 slice:

- `cmd/codex-eval-runner`
- `internal/codexeval`
- manifest-backed task definitions under
  `internal/codexeval/testdata/tasks`
- `scripts/codex-eval-runner.sh`
- Make targets:
  - `make codex-eval-smoke`
  - `make codex-eval-core`
  - `make codex-eval-real-upstream`
- isolated task workspace and `CODEX_HOME` per attempt
- generated Codex custom-provider config
- deterministic file, command, Codex event, and forbidden-output checkers
- local artifacts under `.tmp/codex-eval-runs/<run-id>/`
- markdown matrix generation from one or more `summary.json` files:
  `go run ./cmd/codex-eval-runner matrix .tmp/codex-eval-runs`

The implemented `codex-smoke` suite currently covers `boot`, `read_file`,
`basic_patch`, `bugfix_go`, `command_recovery`, `plan_doc`, and `multi_file`.
The initial `codex-core` suite currently reuses that deterministic set. The
`codex-real-upstream` suite includes those tasks plus the first mixed
text-plus-file-change regression task, `bugfix_mixed`, because that task
requires real Codex file-change behavior rather than the devstack command
fixture.

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

This task was checked on April 29, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Evaluation best practices](https://developers.openai.com/api/docs/guides/evaluation-best-practices)
  - [Evaluate agent workflows](https://developers.openai.com/api/docs/guides/agent-evals)
  - [Evaluate external models](https://developers.openai.com/api/docs/guides/external-models)
  - [Codex configuration reference](https://developers.openai.com/codex/config-reference)
  - [Codex app-server API overview](https://developers.openai.com/codex/app-server)
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
- The public `openai/codex` repo is the right implementation reference for the
  CLI tool registry and local execution behavior, but it is not the source of
  truth for OpenAI wire-contract claims.

## Codex Upstream Reference Inspected

The local ignored checkout at `.tmp/codex-upstream/` was inspected at commit:

```text
87bc72408c5ef08f8d21f2cdd00c55451c3be33f
```

Relevant implementation points observed:

- `codex-rs/tools/src/tool_registry_plan.rs` builds the active tool registry
  from config and model metadata.
- Codex can expose several command-tool modes, including `unified_exec`,
  `shell_command`, `shell`, `local_shell`, and fallback function-tool variants.
- The registry can also expose `write_stdin`, `apply_patch`, `update_plan`,
  `view_image`, `web_search`, dynamic tool discovery, MCP resources/tools,
  request-user-input, and subagent tools depending on configuration.
- `codex-rs/model-provider/src/provider.rs` confirms provider metadata is
  adapted into the API client and model-manager path.
- `docs/exec.md` points non-interactive execution at the public Codex
  non-interactive docs; local smoke scripts should keep using `codex exec
  --json` because that is the most stable automation surface for this repo.

These findings mean the harness must evaluate concrete Codex tool-mode
combinations rather than a generic "agent benchmark" only.

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
docs/v3-codex-eval-harness.md
.tmp/codex-eval-runs/
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
- forbidden Codex JSON event or text markers
- expected shim log markers when debug logging is enabled
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
- `CODEX_EVAL_OUT`
- `CODEX_EVAL_PARALLELISM`
- `CODEX_EVAL_ATTEMPTS`
- `CODEX_EVAL_REASONING_EFFORT`
- `CODEX_EVAL_REASONING_SUMMARY`
- `CODEX_EVAL_WEBSOCKETS`
- `CODEX_EVAL_UNIFIED_EXEC`
- `CODEX_EVAL_APPLY_PATCH_FREEFORM`
- `CODEX_EVAL_KEEP_WORKSPACES`

Default to serial execution for the first version. Codex tasks mutate
workspaces, produce logs, and can stress one upstream model; parallelism should
be explicit.

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

The generated matrix is mechanical: date, run id, model, suite, pass count,
retry-dependent task count, failure buckets, and failed tasks. Keep the
human-written interpretation in
`docs/engineering/codex-upstream-model-matrix.md`.

That split is intentional:

- generated matrix output is an audit trail and quick comparison view copied
  directly from `summary.json`;
- the model matrix document is where a human records interpretation: whether a
  retry is acceptable, whether a failure was shim transport, model behavior, or
  task/checker wording, and which model should be used for the next gate;
- do not edit generated counts by hand; rerun the matrix generator instead;
- do not paste every historical generated row into the model matrix. Keep only
  meaningful baselines and explain why they matter.

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
- long stdout truncation
- stderr handling
- no-op task where Codex should not edit files
- plan document generation with required semantic markers
- mixed natural-language preamble plus tool edit
- patch after reading context from multiple files
- apply-patch/freeform path
- fallback shell path with `unified_exec=false`
- WebSocket-enabled path
- HTTP-first path
- raw tool-call markup rejection

Target runtime: under 45 minutes for one model/provider.

### `codex-compat`

Purpose: broader compatibility and regression discovery.

Task families:

- `previous_response_id` continuity over multi-turn Codex sessions
- `store=false` and same-session continuation where Codex exposes it
- create-stream and WebSocket variants for the same small tasks
- model metadata variants:
  - context-window present
  - context-window absent
  - apply-patch freeform enabled
  - apply-patch disabled
  - WebSocket enabled
  - WebSocket disabled
- tool availability variants:
  - unified exec
  - fallback shell
  - no shell
  - web search disabled
  - app/connectors disabled
  - tool search disabled
- response formatting variants:
  - sentinel-only final answer
  - final answer after file change
  - final answer after failed command
  - final answer after large command output
- regression cases imported from manual failures

Target runtime: allowed to be long; not a normal pre-commit gate.

### `codex-bench-lite`

Purpose: small external benchmark subset after the in-repo harness is stable.

Rules:

- pin benchmark sources
- copy only small sanitized task definitions into repo-owned fixtures
- keep source attribution in task metadata
- prefer tasks with deterministic local checkers
- do not include tasks that require network during execution unless the task is
  explicitly tagged as network-required
- avoid large third-party repos in the first milestone

Candidate sources can include agent/coding benchmark tasks, but they must be
adapted into this repo's task manifest and checker model before becoming a gate.

## Failure Buckets

The runner should classify failures before any LLM-assisted analysis:

- `codex_config`: Codex cannot load provider/config/model metadata.
- `shim_auth`: shim rejects the Codex request before model execution.
- `shim_transport`: HTTP/SSE/WebSocket request fails or wrong status appears.
- `upstream_http`: upstream request returns non-2xx or malformed payload.
- `upstream_stream`: upstream stream disconnects or never completes.
- `model_no_tool`: model answers text but never calls the required tool.
- `model_bad_tool_args`: model calls a tool with invalid or empty arguments.
- `codex_tool_missing`: Codex receives a call but reports unsupported tool.
- `codex_tool_exec`: local command or patch handler fails unexpectedly.
- `checker_diff`: Codex completed but final workspace is wrong.
- `checker_tests`: Codex completed but repository tests fail.
- `raw_tool_markup`: model leaked provider-native tool markup to Codex text.
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

Current status on April 29, 2026:

- Phase 0 is complete: the previous smoke scripts remain documented and
  available.
- Phase 1 is implemented: the runner, manifests, isolated workspaces,
  `CODEX_HOME`, summaries, artifacts, and deterministic checkers exist.
- Phase 2 is partially implemented: Make targets exist and use the runner, but
  older smoke scripts have not been deduplicated into shared runner logic.
- Phase 3 has started: `command_recovery`, `bugfix_mixed`, raw-tool-markup
  detection, and failure buckets exist, but `codex-core` is still a small suite
  and does not yet contain the planned timeout, long-stdout, stderr, no-edit,
  fallback-shell, and WebSocket variants.
- Phase 4 has started: real-upstream runs and matrix generation exist, but
  failed-task rerun, expected-quarantine, and packaged failure review are still
  pending.
- Phases 5 and 6 are still pending.

Next practical milestone: finish Phase 4 enough for daily use by adding
failed-task rerun and expected-quarantine support, then grow `codex-core`
toward the Phase 3 task list.

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
- command timeout task
- long stdout task
- stderr task
- no-op/no-edit task
- mixed text plus file-change task
- fallback shell mode task
- WebSocket mode task
- raw tool markup task
- per-task failure bucket classification

Exit criteria:

- `codex-core` has at least 20 deterministic tasks
- every failure has a bucket
- failed task artifacts are enough to debug without re-running the whole suite

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

### Phase 5: Regression Import Workflow

Deliverables:

- command to import a failed manual run into a new task skeleton
- task minimization checklist
- fixture sanitization checklist
- reviewer template for failure analysis
- docs update explaining how manual sessions become automated regression cases

Exit criteria:

- at least three historical/manual Codex failures are converted into permanent
  deterministic tasks
- each converted task has a checker and failure bucket

### Phase 6: Benchmark-Lite Expansion

Deliverables:

- identify candidate external coding-agent benchmark sources
- choose only small deterministic tasks
- pin provenance in task metadata
- adapt tasks into the repo-owned manifest/checker model
- keep third-party source imports out of normal CI unless explicitly enabled

Exit criteria:

- `codex-bench-lite` exists with at least 10 tasks
- every task has deterministic local pass/fail
- no network-required or long-running task is in the default local gate

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

## Minimum Regression Coverage

Before marking this task complete, the harness should cover:

- Codex boot through custom provider
- authorized `/v1/models` probe through shim
- HTTP-first Responses path
- WebSocket-enabled Responses path
- `unified_exec=true`
- `unified_exec=false`
- local command execution
- command stdout, stderr, non-zero exit, and timeout
- single-file edit
- multi-file edit
- patch-style file change
- tiny code bugfix plus test run
- deterministic documentation writing
- no-edit safety task
- mixed text/tool stream
- raw provider tool markup rejection
- final answer after tool output
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
- `codex-smoke`, `codex-core`, and `codex-real-upstream` suites exist
- `codex-core` contains at least 20 deterministic tasks
- failed runs produce enough artifacts for offline diagnosis
- task manifests validate before execution
- generated artifacts are ignored and redacted
- the old smoke targets still work
- at least one local real-upstream model profile such as Qwen 3.6 is documented
- at least three prior manual failure modes are permanent regression tasks
- `go test ./...` passes
- `make lint` passes
- `git diff --check` passes
- no V2 or OpenAI parity wording overclaims what the harness proves
