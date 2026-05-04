#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  SHIM_BASE_URL=http://127.0.0.1:18080 \
  CODEX_MODEL=devstack-model \
  ./scripts/codex-eval-runner.sh

  ./scripts/codex-eval-runner.sh matrix .tmp/codex-eval-runs

  ./scripts/codex-eval-runner.sh failure-bundle \
    --out .tmp/codex-eval-runs/failure-bundle.md \
    .tmp/codex-eval-runs/<run-id>

  ./scripts/codex-eval-runner.sh compare \
    --control .tmp/codex-eval-loops/<loop-id>/control \
    --out .tmp/codex-eval-loops/<loop-id>/compare.md \
    --json-out .tmp/codex-eval-loops/<loop-id>/summary.json \
    .tmp/codex-eval-loops/<loop-id>/candidate-*

  ./scripts/codex-eval-runner.sh import-failure \
    --task basic_patch \
    --attempt 1 \
    --out imported_basic_patch_regression \
    .tmp/codex-eval-runs/<run-id>

  ./scripts/codex-eval-runner.sh auto-report \
    --out .tmp/codex-eval-auto/<auto-id>/summary.md \
    --json-out .tmp/codex-eval-auto/<auto-id>/summary.json \
    .tmp/codex-eval-auto/<auto-id>/profiles/*

Common real-upstream usage:
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_MODEL=Qwen3.6-35B-A3B \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  GW_API_KEY=shim-dev-key \
  CODEX_EVAL_SUITE=codex-real-upstream \
  ./scripts/codex-eval-runner.sh

Expanded real-upstream diagnostic:
  CODEX_EVAL_SUITE=codex-real-upstream-expanded \
    ./scripts/codex-eval-runner.sh

Optional:
  CODEX_BIN=codex
  CODEX_BASE_URL=$SHIM_BASE_URL/v1
  CODEX_API_KEY=shim-dev-key
  CODEX_EVAL_OUT=.tmp/codex-eval-runs/<run-id>
  CODEX_EVAL_TASKS=basic_patch,bugfix_mixed
  CODEX_EVAL_RERUN_FAILED_FROM=.tmp/codex-eval-runs/<previous-run-id>
  CODEX_EVAL_ATTEMPTS=2
  CODEX_EVAL_REASONING_EFFORT=minimal
  CODEX_EVAL_REASONING_SUMMARY=none
  CODEX_EVAL_WEBSOCKETS=false
  CODEX_EVAL_UNIFIED_EXEC=true
  CODEX_EVAL_APPLY_PATCH_FREEFORM=true
  CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=freeform|function|disabled
  CODEX_EVAL_MATRIX_OUT=.tmp/codex-eval-runs/matrix.md
  CODEX_EVAL_FAILURE_BUNDLE_OUT=.tmp/codex-eval-runs/failure-bundle.md
  CODEX_EVAL_COMPARE_CONTROL=.tmp/codex-eval-loops/<loop-id>/control
  CODEX_EVAL_COMPARE_OUT=.tmp/codex-eval-loops/<loop-id>/compare.md
  CODEX_EVAL_COMPARE_JSON_OUT=.tmp/codex-eval-loops/<loop-id>/summary.json
  CODEX_EVAL_IMPORT_TASK=basic_patch
  CODEX_EVAL_IMPORT_OUT=imported_basic_patch_regression
  CODEX_EVAL_IMPORT_ATTEMPT=1
  CODEX_EVAL_AUTO_REPORT_OUT=.tmp/codex-eval-auto/<auto-id>/summary.md
  CODEX_EVAL_AUTO_REPORT_JSON_OUT=.tmp/codex-eval-auto/<auto-id>/summary.json
  CODEX_EVAL_AUTO_STRICT=baseline|all|none

Profile gates:
  CODEX_EVAL_SUITE=codex-core-shell CODEX_EVAL_UNIFIED_EXEC=false \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-core-websocket CODEX_EVAL_WEBSOCKETS=true \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-core-interactive \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-compat \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-shim-native \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-shim-native-websocket CODEX_EVAL_WEBSOCKETS=true \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-shim-native-apply-patch-freeform \
  CODEX_EVAL_APPLY_PATCH_FREEFORM=true \
  CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=freeform \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-shim-native-apply-patch-function \
  CODEX_EVAL_APPLY_PATCH_FREEFORM=false \
  CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=function \
    ./scripts/codex-eval-runner.sh

  CODEX_EVAL_SUITE=codex-shim-native-apply-patch-disabled \
  CODEX_EVAL_APPLY_PATCH_FREEFORM=false \
  CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=disabled \
    ./scripts/codex-eval-runner.sh
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

go run ./cmd/codex-eval-runner "$@"
