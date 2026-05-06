#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/preflight-local.sh

Environment:
  PREFLIGHT_REQUIRE_DEVSTACK   Default: 1. Set 0 to run doctor in advisory mode.
  MAKE                         Default: make.

Runs the local preflight sequence without deleting files, stopping containers,
or resetting volumes. It combines state inspection, devstack diagnostics,
cleanup/reset previews, and the local build/lint/diff gate.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

make_cmd="${MAKE:-make}"
require_devstack="${PREFLIGHT_REQUIRE_DEVSTACK:-1}"

run_step() {
  echo
  echo "==> preflight-local: $*"
  "$@"
}

echo "==> preflight-local"
echo "require_devstack: ${require_devstack}"

run_step "${make_cmd}" local-state-report

if [[ "${require_devstack}" == "0" ]]; then
  run_step env DEVSTACK_DOCTOR_REQUIRE_READY=0 "${make_cmd}" devstack-doctor
else
  run_step "${make_cmd}" devstack-doctor
fi

run_step "${make_cmd}" clean-artifacts-dry-run
run_step "${make_cmd}" clean-dev-artifacts-dry-run
run_step "${make_cmd}" devstack-reset-dry-run
run_step "${make_cmd}" devstack-reset-volumes-dry-run
run_step "${make_cmd}" build
run_step "${make_cmd}" lint
run_step "${make_cmd}" diff-check

echo
echo "preflight-local passed"
