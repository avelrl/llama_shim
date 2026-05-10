# V4 Provider Ops Runbook

Last updated: May 10, 2026.

Status: implemented read-only operator workflow for configured provider/model
aliases. This is shim-owned operational evidence, not an OpenAI API parity
claim.

## Goal

Use this runbook when deciding whether a configured `provider/model` alias is
ready for local shim work, Codex eval work, or release-gate use.

The final answer should come from `make v4-provider-ops-report`. Earlier steps
produce the evidence that report consumes.

## Full Flow

```bash
V4_PROVIDER_CONFIG_DOCTOR_FLAGS="-strict-env -require-matrix -strict-codex-metadata" \
  make v4-provider-config-doctor

make v4-provider-matrix-smoke
make v4-provider-matrix-curate

make codex-eval-auto
make codex-eval-curate

make v4-provider-ops-report
```

Use a focused flow while iterating on one or two aliases:

```bash
V4_PROVIDER_MATRIX_MODELS="deepseek/deepseek-v4-pro xiaomi/mimo-v2.5-pro" \
  make v4-provider-matrix-smoke

V4_PROVIDER_MATRIX_CURATE_MODEL=deepseek/deepseek-v4-pro \
  make v4-provider-matrix-curate

CODEX_EVAL_CURATE_MODEL=deepseek/deepseek-v4-pro \
  make codex-eval-curate

V4_PROVIDER_OPS_MODEL=deepseek/deepseek-v4-pro \
  make v4-provider-ops-report
```

## Alias Normalization

Codex eval artifacts can contain either the public alias sent through the shim
or the provider's raw upstream model id. `make codex-eval-curate` now uses
`config.yaml` by default when it exists and records canonical alias metadata:

- `model`: the raw model id from the artifact
- `public_model`: the configured `provider/model` alias when known
- `canonical_model`: the model id used for curation grouping
- `raw_models`: raw ids that contributed to a grouped summary

Override the mapping when needed:

```bash
CODEX_EVAL_CURATE_MODEL_ALIASES="Kimi-K2.6=svgun/kimi-k2.6,Qwen3.6-35B-A3B=svgun/qwen-3.6" \
  make codex-eval-curate
```

Disable config-based normalization only when you explicitly want historical
raw-id reporting:

```bash
CODEX_EVAL_CURATE_PROVIDER_CONFIG=disabled make codex-eval-curate
```

## Reading Decisions

`make v4-provider-ops-report` reads local summaries only. It does not call
upstreams.

| Decision | Meaning |
| --- | --- |
| `release_gate_strict_clean` | Matrix evidence is green and latest Codex baseline is strict-clean. |
| `release_gate_retry_dependent` | Matrix evidence is green and Codex baseline passed with retries; record it as retry-dependent. |
| `release_gate_matrix_only` | Matrix evidence is green but Codex evidence is missing or not required. |
| `partial_gate` | Partial matrix smoke passed; run the full matrix before promotion. |
| `missing_matrix_evidence` | Alias is configured but current matrix curation has no row. |
| `codex_attention` | Codex evidence is required and missing or not promotable. |
| `config_attention` | Provider config doctor found blocking config issues. |
| `needs_attention` | Nested evidence needs manual review. |

Set `V4_PROVIDER_OPS_REQUIRE_CODEX=1` when missing Codex evidence should fail
the final ops report:

```bash
V4_PROVIDER_OPS_REQUIRE_CODEX=1 make v4-provider-ops-report
```

## UI And Evidence

The Operator UI Evidence tab highlights the latest `v4_provider_ops` report
when present. The same report is available through `/debug/evidence` because
the evidence registry scans `.tmp/v4-provider-ops/*/summary.json`.

The registry reads only known `summary.json` files. It does not read raw logs,
prompts, headers, request bodies, generated files, or screenshots.
