#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

usage() {
  cat <<'EOF'
Usage:
  make governance-purge-smoke
  ./scripts/governance-purge-smoke.sh

This smoke creates an isolated temporary SQLite database, seeds representative
shim-owned local state, runs the real shimctl governance purge CLI in dry-run
and apply modes, validates JSON audit reports, and verifies that apply removes
the seeded state.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd jq
require_cmd mktemp

go_cmd="${GO:-go}"
require_cmd "${go_cmd}"

root_dir="${GOVERNANCE_PURGE_SMOKE_ROOT:-.tmp/governance-purge-smoke}"
mkdir -p "${root_dir}"
workdir="$(mktemp -d "${root_dir}/run-XXXXXX")"
db_path="${workdir}/shim.db"
config_path="${workdir}/config.yaml"
dry_run_path="${workdir}/dry-run.json"
dry_run_audit_path="${workdir}/dry-run-audit.json"
apply_path="${workdir}/apply.json"
apply_audit_path="${workdir}/apply-audit.json"
missing_confirm_stdout="${workdir}/missing-confirm.stdout"
missing_confirm_stderr="${workdir}/missing-confirm.stderr"

cleanup() {
  if [[ "${GOVERNANCE_PURGE_SMOKE_KEEP:-1}" != "1" ]]; then
    rm -rf "${workdir}"
  fi
}
trap cleanup EXIT

cat >"${config_path}" <<EOF
sqlite:
  path: ${db_path}
storage:
  backend: sqlite
EOF

run_helper() {
  "${go_cmd}" run ./cmd/governance-purge-smoke "$@"
}

run_shimctl() {
  SHIM_DOTENV="${workdir}/missing.env" \
    SQLITE_PATH="${db_path}" \
    STORAGE_BACKEND=sqlite \
    "${go_cmd}" run ./cmd/shimctl -config "${config_path}" "$@"
}

echo "==> governance purge smoke: seed SQLite fixture"
run_helper seed -sqlite "${db_path}"
run_helper verify-present -sqlite "${db_path}"

echo "==> governance purge smoke: dry-run"
run_shimctl governance purge -all -batch-size 1 -audit-out "${dry_run_audit_path}" >"${dry_run_path}"
jq -e '
  .object == "governance.purge_report" and
  .backend == "sqlite" and
  .scope == "all_local_state" and
  .dry_run == true and
  .applied == false and
  .batch_size == 1 and
  .primary.matched_total > 0 and
  .primary.deleted_total == 0 and
  (.primary.tables | length) > 0 and
  (.out_of_scope | length) > 0
' "${dry_run_path}" >/dev/null
jq -e '.dry_run == true and .primary.matched_total > 0' "${dry_run_audit_path}" >/dev/null
run_helper verify-present -sqlite "${db_path}"

echo "==> governance purge smoke: missing confirmation must fail"
if run_shimctl governance purge -all -apply >"${missing_confirm_stdout}" 2>"${missing_confirm_stderr}"; then
  echo "governance purge -apply unexpectedly succeeded without confirmation" >&2
  exit 1
fi
if ! grep -q 'requires -confirm purge-all-local-state' "${missing_confirm_stderr}"; then
  echo "missing confirmation failure did not explain required confirmation token" >&2
  cat "${missing_confirm_stderr}" >&2
  exit 1
fi
run_helper verify-present -sqlite "${db_path}"

echo "==> governance purge smoke: apply"
run_shimctl governance purge -all -apply -confirm purge-all-local-state -batch-size 1 -audit-out "${apply_audit_path}" >"${apply_path}"
jq -e '
  .object == "governance.purge_report" and
  .backend == "sqlite" and
  .scope == "all_local_state" and
  .dry_run == false and
  .applied == true and
  .batch_size == 1 and
  .primary.matched_total > 0 and
  .primary.deleted_total > 0 and
  (.primary.tables | length) > 0 and
  (.out_of_scope | length) > 0
' "${apply_path}" >/dev/null
jq -e '.dry_run == false and .applied == true and .primary.deleted_total > 0' "${apply_audit_path}" >/dev/null
run_helper verify-purged -sqlite "${db_path}"

echo "governance purge smoke passed"
echo "artifacts: ${workdir}"
