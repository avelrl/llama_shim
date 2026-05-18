# Script Inventory

Last updated: May 10, 2026.

Status: short operator map for the current command surface. The `Makefile`
remains the stable entrypoint; scripts own implementation details and artifact
contracts.

## Rule

Prefer `make <target>` in docs and day-to-day use. Call `scripts/*.sh`
directly only when debugging that script or passing a script-specific flag that
the make target intentionally does not expose.

## Core Development

| Command | Purpose | Writes artifacts |
| --- | --- | --- |
| `make test` | Run Go tests. | No |
| `make lint` | Run repository lint checks. | No |
| `make build` | Build shim binaries. | No committed output |
| `make ci-check` | Local build/lint/test/diff-check gate. | No committed output |
| `make operator-ui-build` | Build embedded Operator UI assets. | Yes, committed bundle under `internal/httpapi/operator_ui_dist/` |

## Devstack And Local Smoke

| Command | Purpose | Main artifacts |
| --- | --- | --- |
| `make devstack-up` / `make devstack-down` | Start or stop deterministic local stack. | Docker state |
| `make devstack-doctor` | Strict local environment and readiness diagnosis. | `.tmp/devstack-doctor/` |
| `make devstack-doctor-advisory` | Same diagnosis without failing on readiness. | `.tmp/devstack-doctor/` |
| `make preflight-local` | Local state, doctor, dry-run cleanup, build/lint preflight. | `.tmp/preflight-local/` |
| `make devstack-ci-smoke` | Default deterministic V3/V4 local smoke path. | `.tmp/*` smoke directories |
| `make devstack-full-smoke` | CI smoke plus Codex CLI task matrix. | `.tmp/*` smoke directories |

## Provider And V4 Ops

| Command | Purpose | Main artifacts |
| --- | --- | --- |
| `make upstream-provider-routing-smoke` | Live check for one public `provider/model` alias. | `.tmp/upstream-provider-routing-smoke/` |
| `make v4-preflight-smoke` | Aggregate V4 capability, trace, plugin, and optional provider-routing gate. | `.tmp/v4-preflight-smoke/` |
| `make v4-provider-config-doctor` | Static provider config, env, matrix, and Codex metadata checks. | `.tmp/v4-provider-config-doctor/` |
| `make v4-provider-matrix-smoke` | Run provider-routing and V4 preflight over configured matrix aliases. | `.tmp/v4-provider-matrix-smoke/` |
| `make v4-provider-matrix-curate` | Summarize provider-matrix smoke artifacts. | `.tmp/v4-provider-matrix-curation/` |
| `make v4-provider-ops-report` | Final read-only provider/model ops verdict. | `.tmp/v4-provider-ops/` |
| `make model-certify` | Heavy candidate-model certification runner: isolated shim, external tester gate, Codex profiles, diagnostics, and fix candidates. See [Model Certification Runner](guides/model-certification.md). | `.tmp/model-certification/` |
| `make codex-config` | Generate Codex config for the current shim/provider/model settings. | stdout |
| `make codex-config-doctor` | Diagnose Codex config, local Codex binary, shim health, and one Responses smoke. | `.tmp/shimctl-codex/` |

Use [V4 Provider Ops Runbook](v4-provider-ops-runbook.md) for the full evidence
sequence and [Operations](guides/operations.md) for interpretation.

## Codex Eval

| Command | Purpose | Main artifacts |
| --- | --- | --- |
| `make codex-eval-core` | Deterministic devstack control suite. | `.tmp/codex-eval-runs/` |
| `make codex-eval-real-upstream` | One real-upstream candidate suite. | `.tmp/codex-eval-runs/` |
| `make codex-eval-real-upstream-expanded` | Larger real-upstream candidate suite. | `.tmp/codex-eval-runs/` |
| `make codex-eval-loop` | Control versus candidate loop with matrix and compare reports. | `.tmp/codex-eval-loops/` |
| `make codex-eval-auto` | Baseline, expanded, and bench-lite orchestration. | `.tmp/codex-eval-auto/` |
| `make codex-eval-curate` | Cross-run curation and model-quality summary. | `.tmp/codex-eval-curation/` |
| `make codex-eval-automated-profiles` | Core plus shim-native profile coverage. | `.tmp/codex-eval-runs/` |
| `make codex-eval-clean` | Remove Codex eval artifacts only. | Deletes allowlisted `.tmp/codex-eval-*` |

Use [V3 Codex Eval Curation](v3-codex-eval-curation.md) and
[Codex CLI](guides/codex-cli.md) for the detailed flow.

## V3 Runtime And Backend Smokes

| Command | Purpose | Main artifacts |
| --- | --- | --- |
| `make v3-image-backends-smoke` | Fixture and configured image-generation backend smoke. | `.tmp/v3-image-backends-smoke/` |
| `make v3-local-runtimes-smoke` | Deterministic local runtime capability smoke. | `.tmp/v3-local-runtimes-smoke/` |
| `make v3-computer-browser-harness-smoke` | Optional real Playwright executor for the `computer` loop. | `.tmp/v3-computer-browser-harness-runs/` |
| `make v3-coding-tools-smoke` | Native local shell/apply_patch subset smoke. | `.tmp/v3-coding-tools-smoke/` |
| `make v3-constrained-decoding-smoke` | Default constrained custom-tool runtime smoke. | `.tmp/v3-constrained-decoding-smoke/` |
| `make v3-vllm-constrained-smoke` | Optional live vLLM constrained backend smoke. | `.tmp/v3-vllm-constrained-smoke/` |
| `make responses-websocket-smoke` | Responses WebSocket shim-local subset smoke. | command output plus local state |

## Storage, Governance, And Maintenance

| Command | Purpose | Main artifacts |
| --- | --- | --- |
| `make postgres-storage-test` | Package-level Postgres storage/retrieval beta tests. | No committed output |
| `make devstack-sqlite-fts5-smoke` | SQLite FTS5 retrieval smoke. | `.tmp/devstack-sqlite-fts5-smoke/` |
| `make devstack-postgres-pgvector-smoke` | Postgres/pgvector retrieval smoke. | `.tmp/devstack-postgres-pgvector-smoke/` |
| `make devstack-postgres-pgvector-ann-smoke` | pgvector ANN index smoke. | `.tmp/devstack-postgres-pgvector-smoke/` |
| `make devstack-postgres-pgvector-multi-instance-smoke` | Shared Postgres multi-instance smoke. | `.tmp/devstack-postgres-pgvector-multi-instance-smoke/` |
| `make maint-migrate-sqlite-to-postgres` | SQLite-to-Postgres beta table migration. | Operator-selected DBs |
| `make governance-purge-smoke` | Local-state purge dry-run/apply smoke. | `.tmp/governance-purge-smoke/` |
| `make local-state-report` | Read-only local state size/count report. | `.tmp/local-state-report/` |

## External Compatibility

| Command | Purpose | Main artifacts |
| --- | --- | --- |
| `make responses-compat-external-smoke` | Run operator-provided external Responses tester command against the shim. | `.data/responses-compat-external/` |
| `make responses-compat-external-real-smoke` | Same runner in real-upstream mode. | `.data/responses-compat-external/` |

External tester commands are intentionally operator-owned because local paths,
model choices, and tester repos are not part of this repository.

## Cleanup

| Command | Purpose | Deletes |
| --- | --- | --- |
| `make clean-artifacts-dry-run` | Preview disposable run-artifact cleanup. | Nothing |
| `make clean-artifacts` | Remove allowlisted `.tmp` run artifacts. | `.tmp` smoke/eval/browser artifacts |
| `make clean-dev-artifacts-dry-run` | Preview broader local dev cleanup. | Nothing |
| `make clean-dev-artifacts` | Remove run artifacts plus local tool caches. | allowlisted `.tmp`, `.cache`, local Playwright/build caches |
| `make devstack-reset-dry-run` | Preview Compose reset. | Nothing |
| `make devstack-reset` | Stop/reset devstack containers. | Compose containers, no volumes |
| `make devstack-reset-volumes-dry-run` | Preview Compose volume reset. | Nothing |
| `make devstack-reset-volumes` | Stop/reset devstack containers and volumes. | Compose-managed volumes only |

Cleanup commands deliberately do not remove `.data` by habit. Logs, external
tester artifacts, backups, and local databases are operator-owned.

## When To Add A New Script

Add a new script only when at least one of these is true:

- the flow creates a reusable artifact contract
- the flow needs substantial error handling or cleanup traps
- the flow must be callable both by `make` and manually
- the flow has environment variables worth documenting as a stable interface

Otherwise, prefer a Make target that calls existing tools directly.
