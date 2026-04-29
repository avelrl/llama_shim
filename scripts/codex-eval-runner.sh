#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  SHIM_BASE_URL=http://127.0.0.1:18080 \
  CODEX_MODEL=devstack-model \
  ./scripts/codex-eval-runner.sh

  ./scripts/codex-eval-runner.sh matrix .tmp/codex-eval-runs

Common real-upstream usage:
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_MODEL=Qwen3.6-35B-A3B \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  GW_API_KEY=shim-dev-key \
  CODEX_EVAL_SUITE=codex-core \
  ./scripts/codex-eval-runner.sh

Optional:
  CODEX_BIN=codex
  CODEX_BASE_URL=$SHIM_BASE_URL/v1
  CODEX_API_KEY=shim-dev-key
  CODEX_EVAL_OUT=.tmp/codex-eval-runs/<run-id>
  CODEX_EVAL_ATTEMPTS=2
  CODEX_EVAL_REASONING_EFFORT=minimal
  CODEX_EVAL_REASONING_SUMMARY=none
  CODEX_EVAL_WEBSOCKETS=false
  CODEX_EVAL_UNIFIED_EXEC=true
  CODEX_EVAL_APPLY_PATCH_FREEFORM=true
  CODEX_EVAL_MATRIX_OUT=.tmp/codex-eval-runs/matrix.md
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

go run ./cmd/codex-eval-runner "$@"
