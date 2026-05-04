# V3 Codex Eval Curation

Last updated: May 4, 2026.

Status: implemented runbook for interpreting generated Codex eval artifacts.

This document defines the human review loop for `codex-eval-auto` results.
Generated reports provide counts and links; this runbook decides what those
counts mean for model quality, shim regressions, and follow-up work.

This is not a new OpenAI API compatibility claim. It is an operational process
for curating Codex-through-shim evidence.

## Inputs

Start from the auto summary:

```bash
CODEX_EVAL_AUTO_PROFILES=baseline,expanded,bench-lite \
CODEX_EVAL_ATTEMPTS=2 \
make codex-eval-auto
```

For long manual runs, notifications are useful:

```bash
CODEX_EVAL_AUTO_PROFILES=baseline,bench-lite \
CODEX_EVAL_ATTEMPTS=2 \
CODEX_EVAL_NOTIFY=macos \
make codex-eval-auto
```

The primary artifact is:

```text
.tmp/codex-eval-auto/<auto-id>/summary.md
```

Each profile also writes:

```text
.tmp/codex-eval-auto/<auto-id>/profiles/<profile>/compare.md
.tmp/codex-eval-auto/<auto-id>/profiles/<profile>/summary.json
.tmp/codex-eval-auto/<auto-id>/profiles/<profile>/shim-log-diagnostics.md
.tmp/codex-eval-auto/<auto-id>/profiles/<profile>/shim.log.slice
```

`.tmp` artifacts are local run artifacts and are not committed. The durable
record is the interpreted summary copied into
[docs/engineering/codex-upstream-model-matrix.md](engineering/codex-upstream-model-matrix.md)
or a permanent regression task imported into the repo.

## First-Pass Review

Read files in this order:

1. `summary.md`
2. each failed or retry-dependent profile's `compare.md`
3. each relevant `shim-log-diagnostics.md`
4. task-level `checker.json`, `failure.md`, `codex.jsonl`, and `git.diff` only
   for failed or retry-dependent tasks

Do not start by reading the full `shim.log.slice`. Use diagnostics first, then
open the slice only when the summary points at transport, raw-tool, or runtime
behavior.

## Profile Meaning

`baseline` is the stable model gate:

- control suite: `codex-core`
- candidate suite: `codex-real-upstream`
- expected use: current model baseline and model-matrix promotion

`expanded` is diagnostic coverage:

- control suite: `codex-core`
- candidate suite: `codex-real-upstream-expanded`
- expected use: tool-discipline, extra command, and broader coding behavior
  checks
- do not treat it as identical to the stable baseline, because the task set is
  intentionally broader

`bench-lite` is longer-run stability coverage:

- control suite: `codex-bench-lite`
- candidate suite: `codex-bench-lite`
- expected use: compare candidate behavior against the same deterministic
  suite under control and real upstream

Coverage differences are not failures. They mean the profile compares suites
with intentionally different task sets.

## Classification Rules

Use the generated diagnosis first, then verify with artifacts.

| Diagnosis | Meaning | Usual next step |
| --- | --- | --- |
| `ok` | Control and candidate passed. | Candidate evidence can be summarized. |
| `retry_dependent` | Candidate passed only after an earlier failed attempt. | Keep visible in notes; do not call it strict-clean. |
| `candidate_model_behavior` | The model performed the action incorrectly or missed a required final marker. | Rerun once if the behavior looks flaky; otherwise record as model behavior. |
| `candidate_tool_contract` | The model printed pseudo-tool markup or violated structured tool-use expectations. | Check whether shim diagnostics show repair/transport issues; otherwise record as model tool discipline. |
| `candidate_transport` | HTTP, stream, upstream status, or timeout issue on candidate path. | Inspect shim diagnostics and `.data/shim.log`; do not blame model first. |
| `control_failed` | Deterministic control failed. | Treat as harness/devstack/regression issue before scoring the candidate. |
| `harness_bug` | Checker, task wording, fixture, or runner issue. | Fix harness before rerunning or updating the matrix. |

When the generated diagnosis and artifacts disagree, keep the matrix wording
conservative and describe what was actually observed.

## Shim Log Triage

High-signal shim findings:

- `WARN` or `ERROR`
- HTTP `5xx`
- `response.failed` or `turn.failed`
- upstream request failure
- stream EOF before terminal event
- raw tool-call repair or raw markup leakage
- panic or process restart

If none of these appear in the profile diagnostics and the task artifacts show
wrong final text, missing sentinel, bad file content, or pseudo-tool text, the
default classification is model behavior or model tool discipline, not shim
transport.

If high-signal shim findings appear, inspect:

- matching `request_id`
- request path, especially `/v1/responses`
- whether the run used native Responses proxy or Chat-backed local tool loop
- whether a response had already started streaming before the failure
- whether a retry happened inside shim, Codex, or the eval harness

Do not add broad shim repair logic for model text unless the path can be safely
re-asked before streaming a terminal response.

## Rerun Policy

Use one repeat when the failure looks model-flaky and shim diagnostics are
clean.

For failed auto profiles:

```bash
CODEX_EVAL_AUTO_PROFILES=baseline,bench-lite \
CODEX_EVAL_ATTEMPTS=2 \
make codex-eval-auto
```

For one focused task:

```bash
CODEX_EVAL_CONTROL_SUITE=codex-core \
CODEX_EVAL_CANDIDATE_SUITE=codex-real-upstream \
CODEX_EVAL_TASKS=multi_file \
bash ./scripts/codex-eval-loop.sh
```

For benchmark-lite task reruns:

```bash
CODEX_EVAL_CONTROL_SUITE=codex-bench-lite \
CODEX_EVAL_CANDIDATE_SUITE=codex-bench-lite \
CODEX_EVAL_TASKS=command_pipeline \
bash ./scripts/codex-eval-loop.sh
```

Interpret repeat runs this way:

- first run failed, repeat passed, shim diagnostics clean: record the first run
  as model flake if it is worth mentioning; use the passing repeat as current
  evidence
- same task fails twice with the same bucket: investigate before updating the
  matrix
- control fails on repeat: stop candidate scoring and fix control or harness
- transport/high-signal shim logs repeat: treat as shim or upstream-route issue
- retry-dependent pass repeats: keep it visible as stability weakness

## Baseline Promotion

Promote a run in the model matrix only when:

- `baseline` passes
- shim diagnostics for the profile have no relevant high-signal entries
- control passed
- candidate pass count and retry count are copied from generated artifacts
- notes clearly distinguish strict-clean from retry-dependent green

Strict-clean baseline:

- all candidate tasks pass on first attempt
- no high-signal shim diagnostics
- preferred baseline when available

Retry-dependent baseline:

- acceptable only when no strict-clean baseline exists or the task set is more
  valuable than the older strict-clean set
- notes must list the retry-dependent tasks and the first-attempt failure class

Diagnostic profiles:

- `expanded` and `bench-lite` can update evidence rows
- they should not replace the stable baseline unless the matrix says the scope
  changed deliberately

## Matrix Update Rules

Generated files own counts. The model matrix owns interpretation.

Copy from generated artifacts:

- date
- model id
- suite/profile
- attempts
- pass count
- retry-dependent task count
- failure buckets
- failed task ids

Write by hand:

- whether this supersedes an earlier baseline
- whether failures look like shim transport, model behavior, tool discipline,
  task wording, or harness issue
- whether rerun evidence changes the conclusion
- why a retry-dependent pass is acceptable or not

Do not paste every historical run into the matrix. Keep meaningful baselines,
regressions, and explanatory evidence rows.

## Regression Import Decision

Import a permanent regression only when the failure reduces to a deterministic
repo-owned scenario.

Good import candidates:

- repeated checker failure caused by task or shim behavior
- stable raw-tool leakage form not covered by detector tests
- reproducible command/session bridge failure
- repeated transport failure with a minimal request shape
- Codex request-shape regression

Poor import candidates:

- one-off model missed final sentinel and passed on rerun
- provider outage or transient `5xx`
- model wrote harmless leading whitespace unless the task contract requires
  exact bytes
- broad manual session failure that cannot be minimized

Use the regression import workflow in
[docs/v3-codex-eval-harness.md](v3-codex-eval-harness.md) when importing a
task. The imported task starts quarantined and must be minimized before it is
promoted into a normal suite.

## Current Example

The May 4, 2026 DeepSeek repeat illustrates the intended process:

- an earlier full auto run had a `baseline` `multi_file` raw-tool-markup
  failure and a `bench-lite` `command_pipeline` checker failure
- focused inspection showed clean shim diagnostics and model-visible behavior
  issues
- the repeat run with `baseline,bench-lite` passed:
  - `baseline`: 11/11, strict-clean
  - `bench-lite`: 20/20, with one retry-dependent `patch_after_context`
- conclusion: no shim regression; treat the earlier failed run as model flake
  and keep the repeat as current evidence

