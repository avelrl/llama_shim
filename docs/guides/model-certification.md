# Model Certification Runner

This guide explains how to use the V4 model certification runner. The design
and artifact contract live in [V4 Model Certification Runner](../v4-model-certification-runner.md).

Use this workflow when you want to evaluate one or more provider/model aliases
through the shim without manually editing endpoints, restarting the shim,
running an external tester, then running Codex evals by hand.

## What It Does

`make model-certify` runs a repeatable certification pipeline for each selected
model:

1. Reads the model from `configs/model-certification.yaml`.
2. Generates an isolated one-model shim config under `.tmp/model-certification/`.
3. Starts a dedicated shim on a loopback port.
4. Captures `/healthz`, `/readyz`, and `/debug/capabilities`.
5. Runs the external OpenAI-compatible tester through the shim.
6. Runs Codex profiles only if the external tester gate passes.
7. Writes summaries, logs, traces, failure notes, and fix candidates.

The runner is not a model benchmark. It is an operator gate for answering:

- is the provider/model alias wired correctly?
- is the model API-compatible enough to be worth deeper testing?
- if API compatibility passes, is the model useful for Codex tasks?
- when it fails, does the evidence point to shim work, provider/runtime work,
  or model behavior?

## Prerequisites

Before a live run:

- `config.yaml` contains the provider, model alias, upstream model id, token env
  name, and Codex metadata.
- `configs/model-certification.yaml` contains the candidate model.
- Provider token environment variables are present in repo-local `.env` or in
  the shell that starts the runner.
- The external tester checkout exists if you are running the API compatibility
  gate.
- Codex eval dependencies work if you are running the Codex phase.

Do not put real tokens into `configs/model-certification.yaml` or committed
docs. Keep tokens in environment variables.

The runner loads the same repo-local `.env` as the shim before parsing
`MODEL_CERT_*` options. With the common sibling checkout layout, no tester
command needs to be supplied:

For the isolated shim process itself, runner keeps provider tokens from the
environment but strips shim-local config overrides such as `SHIM_ADDR`,
`SQLITE_PATH`, and `LOG_FILE_PATH`; the generated per-model config owns those
values.

```text
../openai-compatible-tester/
  configs/models_llama_shim.yaml
  configs/suite_llama_shim.yaml
  configs/capabilities_llama_shim.yaml
  configs/suite_responses_extended.yaml
  configs/capabilities_responses_extended.yaml
```

If the tester checkout lives elsewhere, set this once in `.env`:

```bash
MODEL_CERT_EXTERNAL_TESTER_DIR=../openai-compatible-tester
```

## Quick Dry Run

Use this first after editing config or the manifest. It renders artifacts and
generated config, but does not start a shim or call upstreams:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-dry-run
```

Expected result:

- command exits `0`
- top-level verdict is `configured`
- artifacts appear under `.tmp/model-certification/cert-<timestamp>/`

This validates local wiring only. It does not prove the upstream is reachable
or that the model works.

## External Tester Gate Only

Use this when adding a new model or debugging API compatibility. Codex is
skipped until the simpler compatibility surface is green.

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-api
```

This uses `configs/models_llama_shim.yaml`, `configs/suite_llama_shim.yaml`,
and `configs/capabilities_llama_shim.yaml` from the external tester checkout.
The runner intentionally does not patch or relax the tester per model; the shim
should adapt where adaptation is shim-owned.

To run the optional Responses extended block:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-api-extended
```

That target swaps the tester suite and capabilities to
`configs/suite_responses_extended.yaml` and
`configs/capabilities_responses_extended.yaml`.

If this gate fails, inspect the model artifact before running Codex:

- `models/<model-slug>/summary.md`
- `models/<model-slug>/failure-notes.md`
- `models/<model-slug>/shim-log-diagnostics.md`
- `models/<model-slug>/external-tester/`
- `models/<model-slug>/traces-summary.json`

## Full Single-Model Run

After the external tester gate is green, run Codex without repeating the
external tester:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify-codex
```

Codex profiles come from `configs/model-certification.yaml` by default. Use a
narrow profile list such as `[baseline]` for new local candidates, and broader
profiles for stronger hosted models. To force a one-off profile without editing
the manifest, set `MODEL_CERT_CODEX_PROFILES`, for example:

```bash
MODEL=gpu/qwen3-coder-30b MODEL_CERT_CODEX_PROFILES=expanded make model-certify-codex
```

Certification runs these profiles as candidate-only Codex suites against the
isolated shim; it does not require a separate devstack control shim.

For a full single command that runs the tester gate and then Codex:

```bash
MODEL=gpu/qwen3-coder-30b make model-certify
```

## Batch Run

Use a comma-separated or space-separated model list:

```bash
MODEL_CERT_MODELS="deepseek/deepseek-v4-pro,xiaomi/mimo-v2.5-pro" make model-certify-api
```

Empty `MODEL_CERT_MODELS` means every model in
`configs/model-certification.yaml`. Avoid that until single-model runs are
known to work.

## Common Options

| Option | Purpose |
| --- | --- |
| `MODEL` | Makefile shortcut for one public `provider/model` alias. |
| `MODEL_CERT_MODELS` | Selected public `provider/model` aliases. Empty means all manifest models. |
| `MODEL_CERT_PHASE` | `full`, `dry-run`, `api`, or `codex`. Usually set by the Make targets. |
| `MODEL_CERT_MANIFEST` | Manifest path. Default: `configs/model-certification.yaml`. |
| `CONFIG` | Base shim config used to fill provider and Codex metadata. Default: `config.yaml`. |
| `MODEL_CERT_OUT` | Artifact output directory. Default: `.tmp/model-certification/cert-<timestamp>`. |
| `MODEL_CERT_RUN_ID` | Stable run id for reproducible artifact paths. |
| `MODEL_CERT_EXTERNAL_TESTER_DIR` | External tester checkout. Default: `../openai-compatible-tester`. |
| `MODEL_CERT_TESTER_MODELS_CONFIG` | Tester models config relative to the checkout. Default: `configs/models_llama_shim.yaml`. |
| `MODEL_CERT_TESTER_SUITE_CONFIG` | Tester suite config relative to the checkout. Default: `configs/suite_llama_shim.yaml`. |
| `MODEL_CERT_TESTER_CAPABILITIES_CONFIG` | Tester capabilities config relative to the checkout. Default: `configs/capabilities_llama_shim.yaml`. |
| `MODEL_CERT_TESTER_CMD` | Optional exact shell command for unusual tester layouts. |
| `MODEL_CERT_CODEX_PROFILES` | Comma- or space-separated Codex profiles overriding the manifest for this run. |
| `MODEL_CERT_CODEX_RUNNER_CMD` | Override the candidate-only Codex runner command. Default: `bash ./scripts/codex-eval-runner.sh`. |
| `MODEL_CERT_SKIP_SHIM` | Render artifacts without starting a shim. |
| `MODEL_CERT_SKIP_TESTER` | Skip the external API compatibility gate. |
| `MODEL_CERT_SKIP_CODEX` | Skip Codex profiles. |
| `MODEL_CERT_REQUIRE_TESTER` | Fail the model if no tester command is configured. Default: `true`. |

## Artifact Map

Each run writes:

```text
.tmp/model-certification/<run-id>/
  summary.md
  summary.json
  fix-candidates.md
  fix-candidates.json
  models/<model-slug>/
    summary.md
    summary.json
    model.env
    healthz.json
    readyz.json
    capabilities.json
    traces.json
    traces-summary.json
    failure-notes.md
    shim-log-diagnostics.md
    external-tester/
    codex/
    shim/
      config.yaml
      shim.log
      shim.stdout.log
      shim.stderr.log
```

Start with the top-level `summary.md`. If it reports attention or failure,
open the model-level `failure-notes.md` and `fix-candidates.md`.

## Verdicts

| Verdict | Interpretation |
| --- | --- |
| `configured` | Config and artifacts were rendered; live checks were skipped. |
| `shim_start_failed` | The isolated shim did not start or did not satisfy readiness. |
| `api_compat_passed` | External tester passed, and Codex was skipped or not configured. |
| `api_compat_failed` | External tester failed; Codex was intentionally skipped. |
| `codex_clean` | Selected Codex profiles passed without retry-dependent classification or failed shim debug traces. |
| `codex_retry_dependent` | Codex passed, but retry dependence, internal failed traces, or curation notes should stay visible. |
| `codex_failed` | One or more required Codex profiles failed. |
| `needs_operator_review` | Evidence exists, but automated ownership is inconclusive. |

Recommended interpretation:

- `configured`: dry-run only, not a certification result.
- `api_compat_failed`: fix API compatibility first; do not spend time on Codex.
- `codex_retry_dependent`: usable signal, but do not promote as a strict-clean
  model until the retry reason or internal failed trace is understood.
- `codex_clean`: good candidate for provider matrix or release-gate promotion.

## Troubleshooting

If the runner fails before the tester:

- inspect `models/<model-slug>/shim/shim.stderr.log`
- inspect `models/<model-slug>/readyz.json`
- check provider token env vars from `config.yaml`
- run `make v4-provider-config-doctor` for static config/env checks

If the external tester fails:

- inspect `external-tester/` artifacts
- inspect `failure-notes.md`
- inspect `traces-summary.json`
- look for request-shape, JSON/schema, tool-call, auth, quota, timeout, or
  readiness signals in `shim-log-diagnostics.md`

If Codex fails:

- inspect `models/<model-slug>/codex/summary.md`
- inspect `models/<model-slug>/codex/profiles/<profile>/summary.json`
- run `make codex-eval-curate` separately if you need cross-run context
- treat raw tool markup, invalid tool args, and transport errors differently;
  those may have different owners

## Cleanup

Certification artifacts are disposable and live under `.tmp/model-certification/`.

Preview cleanup:

```bash
make clean-artifacts-dry-run
```

Apply cleanup:

```bash
make clean-artifacts
```

Do not commit `.tmp/model-certification/` artifacts. Commit only manifest,
config, code, and docs changes.
