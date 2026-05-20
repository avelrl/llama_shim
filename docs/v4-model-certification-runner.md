# V4 Model Certification Runner

Last updated: May 18, 2026.

Status: first runner implemented as `make model-certify`.

This document defines a V4 operator workflow for automatically certifying
provider/model aliases through the shim. It is shim-owned automation and does
not widen any OpenAI API compatibility claim.

For command-oriented usage, see
[Model Certification Runner](guides/model-certification.md).

## Goal

The runner should replace the current manual loop of editing provider config,
restarting the shim, running external compatibility tests, running Codex evals,
and then hand-reading logs.

The useful outcome is one artifact bundle per run:

- every candidate model is tested through the same shim entrypoints
- the shim is isolated per model with its own DB, logs, and debug traces
- external API compatibility failures stop Codex runs before they waste time
- Codex results are captured by profile from simplest to hardest
- likely shim-fix candidates are summarized in a separate operator file

This is not a model benchmark and not a strict OpenAI parity proof. It is an
operator gate for deciding whether a model is usable behind this shim.

## Why This Is Useful

The shim now has enough provider routing, compatibility repair, debug traces,
Codex eval profiles, and curation tooling that manual model testing has become
the bottleneck. A model can fail for several different reasons:

- the provider endpoint or token is wrong
- the upstream catalog or readiness probe is inconsistent
- the model/API gateway is not OpenAI-compatible enough for basic clients
- the shim needs a request-shape or tool-call repair
- the model is API-compatible but too flaky for Codex tasks

Running those checks by hand makes results hard to compare and easy to
misclassify. The certification runner should turn that into a repeatable
evidence package. The external tester is the first gate because there is no
value in running slow Codex profiles when the model already breaks the simpler
OpenAI-compatible surface. Codex is the deeper follow-up gate for models that
clear that basic API check. Operator-readable fix candidates should be emitted
only when the evidence points to shim-owned work.

## Pipeline

The runner should use this order for each selected model:

1. Read the model entry from a repo-owned certification manifest.
2. Generate a temporary shim config and env for that one provider/model alias.
3. Start a dedicated shim process on a free loopback port.
4. Wait for `/healthz`, then capture `/readyz` and `/debug/capabilities`.
5. Run `openai-compatible-tester` through the shim without changing tester
   behavior per model.
6. If the tester compat verdict is not green, skip Codex for that model.
7. If the tester is green, run Codex eval profiles in order:
   `baseline`, `expanded`, then `bench-lite`.
8. Slice shim logs, collect debug traces, and write model-level notes.
9. Add any actionable shim-fix candidates to the run-level fix file.

The runner should continue to the next model after a model fails. A single bad
model must not stop the whole batch unless the runner itself cannot proceed.

## Relationship To Existing Tools

This runner should orchestrate existing tools instead of replacing their
semantics:

- `v4-provider-config-doctor` remains the static config/env/metadata check.
- `v4-provider-matrix-smoke` remains the fast live routing and preflight smoke.
- `openai-compatible-tester` remains the cheap first API-compatibility gate.
- `codex-eval-runner` remains the candidate-only Codex task runner.
- `codex-eval-auto`, `codex-eval-curate`, and `v4-provider-ops-report` remain
  the broader comparison, curation, and promotion views.

The certification runner is the heavy batch workflow for candidate models. It
should be used when the operator wants an apples-to-apples package of evidence
across many models, not for every small config edit.

## Manifest

The repo-owned manifest lives at `configs/model-certification.yaml`.

The manifest should be the source of truth for candidates that are not yet
release gates. It should not store secrets. It should refer to token env vars
and public aliases only.

Minimum model entry:

```yaml
models:
  - model: gpu/qwen3_6-35b-a3b
    provider:
      id: gpu
      base_url: http://192.168.1.130:8000
      bearer_token_env: ""
      upstream_model: qwen3_6-35b-a3b
    tester:
      mode: compat
      gate: compat
    codex:
      profiles: [baseline, expanded, bench-lite]
      context_window: 32768
      apply_patch_tool_type: freeform
```

The manifest should allow these per-model knobs:

- public alias
- provider id, base URL, token env, upstream model id
- readiness policy
- Responses upstream transport preference
- Codex metadata needed for config generation
- tester suite/profile/capability config paths
- Codex profile list and attempts

Defaults should be conservative. Missing Codex metadata should make Codex
phase fail before the long eval starts.

## Self-Managed Shim Runtime

The default mode should be self-managed. For each model, create:

```text
.tmp/model-certification/<run-id>/models/<model-slug>/shim/
  config.yaml
  shim.db
  shim.log
  shim.stdout.log
  shim.stderr.log
```

The generated shim config should contain one provider and one model alias. It
should preserve the compatibility rules from the repo config that match the
resolved upstream model, but it should not copy unrelated provider credentials.

Required runtime defaults:

- bind on a free `127.0.0.1` port
- use SQLite under the model artifact directory
- write logs under the model artifact directory
- enable metadata-only debug traces
- set the evidence root to the current model artifact directory
- keep shim ingress auth explicit and separate from provider tokens

The runner should shut the shim down with `SIGTERM` and rely on the existing
graceful shutdown path. If the process does not exit before the runner timeout,
mark the model with a runtime failure and clean up the process.

An attach mode can be added later for debugging already-running shims, but it
must not be the default certification path.

## External Tester Gate

The runner should call an operator-supplied checkout of
`openai-compatible-tester` through the shim. The tester itself should not be
patched or relaxed per model.

Default command shape:

```bash
cd "$MODEL_CERT_EXTERNAL_TESTER_DIR"
go run . \
  --no-tui \
  --models configs/models_llama_shim.yaml \
  --suite configs/suite_llama_shim.yaml \
  --capabilities configs/capabilities_llama_shim.yaml \
  --profile "$MODEL_CERT_TESTER_PROFILE" \
  --mode compat \
  --out-dir "$MODEL_ARTIFACT_DIR/external-tester" \
  --json
```

This is a pre-Codex gate, not a model-quality benchmark. The gate should be
based on the tester's practical compatibility verdict and agent-readiness
result. Strict OpenAI-spec conformance should be saved as advisory evidence
unless the manifest explicitly asks for a strict gate.

If the tester fails, write:

- tester stdout/stderr
- tester report directory
- shim log slice
- request trace summary
- `failure-notes.md`

Then skip Codex for that model.

## Verdicts

Use stable verdict names in `summary.json` so curation and operator UI can read
the output without parsing prose:

| Verdict | Meaning |
| --- | --- |
| `configured` | Manifest and generated config were valid, but no live checks ran. |
| `shim_start_failed` | The isolated shim failed to start or never reached required readiness. |
| `api_compat_passed` | External tester compat gate passed. |
| `api_compat_failed` | External tester compat gate failed; Codex was skipped. |
| `codex_clean` | Codex profiles selected by policy passed without retry-dependent interpretation or failed shim debug traces. |
| `codex_retry_dependent` | Codex passed, but retries, failed shim debug traces, or curation notes must remain visible. |
| `codex_failed` | One or more required Codex profiles failed. |
| `needs_operator_review` | Evidence exists but automated classification is inconclusive. |

Do not collapse these into a boolean. A model that is API-compatible but
retry-dependent in Codex is still useful, but it should not be promoted as a
strict-clean gate.

## Codex Eval Phase

Codex should run only after the external tester gate is green.

Default profiles:

- `baseline`: candidate-only `codex-real-upstream`
- `expanded`: candidate-only `codex-real-upstream-expanded`
- `bench-lite`: candidate-only `codex-bench-lite`

The runner should call `codex-eval-runner` directly for certification. It
should not require the devstack control shim on `127.0.0.1:18080`; comparison
and cross-run curation stay in `codex-eval-auto` and `codex-eval-curate`.

Codex environment should use the public `provider/model` alias:

```bash
SHIM_BASE_URL=<model-shim-url>
CODEX_PROVIDER=gateway-shim
CODEX_API_KEY_ENV=GW_API_KEY
CODEX_MODEL=<provider/model>
CODEX_EVAL_MODELS=<provider/model>
```

The model-level verdict should preserve retry-dependent results instead of
hiding them behind a single pass/fail field.

## Artifacts And Fix Candidates

Each run should write:

```text
.tmp/model-certification/<run-id>/
  summary.md
  summary.json
  fix-candidates.md
  fix-candidates.json
  models/<model-slug>/
    model.env
    capabilities.json
    readyz.json
    traces.json
    failure-notes.md
    external-tester/
    codex/
    shim/
```

The top-level summary should show:

- final verdict per model
- tester status
- Codex status by profile
- whether failures look model-owned, provider-owned, or shim-owned
- links to the most useful artifact paths

The fix-candidate analyzer should classify common signals:

- request-shape incompatibility
- raw tool markup
- invalid or lossy tool-call arguments
- JSON schema or structured-output mismatch
- upstream timeout or transport failure
- readiness/catalog mismatch
- shim-local bug candidate
- model behavior/checker failure

The analyzer should be conservative. It should record "possible shim fix" only
when the evidence points to request shaping, routing, parsing, retry, or
compatibility repair that the shim owns.

## LLM-Friendly Review Notes

The runner should produce bounded summaries that are easy for a human or LLM to
review without loading raw logs first.

Per model, `failure-notes.md` should include:

- final verdict and first failing stage
- the exact command family that failed
- high-signal tester rows or Codex task names
- bounded shim-log matches around warnings, 4xx/5xx, raw markup, retries, and
  upstream timeouts
- trace ids for failed requests when available
- a short "possible owner" field: `shim`, `provider`, `model`, `tester`, or
  `environment`

Raw logs should still be preserved under the artifact directory, but summaries
must redact bearer tokens, provider tokens, request bodies that may contain
prompts, and generated files. The first implementation can use regex-based
classification; a separate research task can later improve the classifier if
the summaries are not good enough.

## Diagnostics Requirements

The first implementation should improve diagnosis through orchestration and
artifact shape before adding more verbose shim logs. The shim already exposes
metadata-only debug traces and accepts `X-Client-Request-Id`; the runner should
make those features deterministic and easy to consume.

Required for the first implementation:

- Set `X-Client-Request-Id` on every probe, tester request when possible, and
  runner-owned shim request. Use a stable form such as
  `modelcert:<run-id>:<model-slug>:<stage>:<case>`.
- Render isolated shim configs with debug traces enabled and a higher
  stage-safe retention limit, for example `shim.debug_traces.max_entries: 4096`.
- Fetch `/debug/traces` after every major stage before the bounded in-memory
  trace store can evict useful entries.
- Save `traces.json` plus a compact `traces-summary.json` grouped by surface,
  final status, provider, public model, upstream model, selected backend,
  backend failure class, request cleanup transforms, and tool decisions.
- Save individual trace detail for failed, slow, retry-dependent, and
  non-2xx requests when the request id is known.
- Slice shim logs by model and by stage. The high-signal matcher should include
  4xx/5xx statuses, upstream timeouts, transport errors, raw tool markup,
  invalid tool arguments, schema or JSON-structure failures, readiness/catalog
  mismatches, provider auth failures, rate limits, and panics.
- Prefer debug trace metadata over raw log text when writing
  `failure-notes.md`; raw logs remain evidence, not the primary summary.

Useful later, but not required to start:

- Add upstream request id metadata to debug traces when a provider returns one.
- Add metadata-only response outcome fields such as finish reason, tool-call
  count, output item types, and stream terminal event class.
- Add a durable private trace store only if stage-level trace snapshots and the
  higher in-memory retention limit are not enough for large batches.

The runner must keep these artifacts metadata-only. It should not persist
prompts, completions, generated files, bearer tokens, or upstream authorization
headers in diagnostic summaries.

## Implementation Plan

Implemented first slice:

- manifest parser and validation
- temporary isolated shim config rendering
- shim process lifecycle and readiness waits
- external tester invocation by explicit command or configured tester checkout
- Codex phase orchestration through candidate-only `codex-eval-runner`
- debug trace, shim-log diagnostic, failure-note, summary, and fix-candidate
  artifacts
- `make model-certify`

Future refinements can add richer tester report parsing and durable trace
storage if the current artifact snapshots are not enough.

Do not implement automatic code fixes. The runner should produce evidence and
fix-candidate notes only.

## Running

Use a single model first:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-api
MODEL=gpu/qwen3-coder-30b make model-certify-codex
```

Codex profiles are read from `configs/model-certification.yaml` unless the run
sets `MODEL_CERT_CODEX_PROFILES`. Use that override for one-off stronger runs,
for example:

```bash
MODEL=gpu/qwen3-coder-30b MODEL_CERT_CODEX_PROFILES=expanded make model-certify-codex
```

The common external tester layout is encoded as defaults:
`../openai-compatible-tester`, `configs/models_llama_shim.yaml`,
`configs/suite_llama_shim.yaml`, and
`configs/capabilities_llama_shim.yaml`. Use `MODEL_CERT_TESTER_CMD` only for an
unusual local tester checkout.

The runner exports:

- `OPENAI_BASE_URL`
- `OPENAI_API_KEY`
- `SHIM_BASE_URL`
- `MODEL_CERT_MODEL`
- `TESTER_MODEL`
- `MODEL_CERT_ARTIFACT_DIR`
- `MODEL_ARTIFACT_DIR`
- `MODEL_CERT_EXTERNAL_TESTER_DIR`

For a dry artifact/config check without live upstream calls:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-dry-run
```

Artifacts are written under `.tmp/model-certification/<run-id>/`.

## Validation Plan

Focused tests:

- manifest parsing and validation
- secret redaction in rendered reports
- generated config contains one provider/model alias
- tester failure skips Codex
- green tester starts Codex phase
- failure classification for fixture snippets
- artifacts are written in the documented layout

Manual live validation still owns the real shim process lifecycle and upstream
network behavior because those depend on local ports, provider credentials, and
the operator's external tester checkout.

Manual dry-run command:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-dry-run
```

Manual live first-run command:

```bash
MODEL=deepseek/deepseek-v4-pro make model-certify-api
```

Final repo checks:

```bash
go test ./...
make lint
git diff --check
```

## Boundaries

- Do not change `openai-compatible-tester` to make model runs pass.
- Do not commit generated `.tmp` artifacts.
- Do not write real provider tokens into configs or reports.
- Do not treat strict tester failures as release blockers unless the manifest
  explicitly asks for strict mode.
- Do not promote a model based on one green API tester run. Promotion still
  needs Codex evidence or an explicit operator decision.
