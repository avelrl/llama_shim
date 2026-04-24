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
  SHIM_BASE_URL=http://127.0.0.1:18080 \
  MODEL=devstack-model \
  ./scripts/codex-cli-task-matrix-smoke.sh

Optional:
  CODEX_BIN=codex
  CODEX_HOME=.tmp/codex-task-matrix-smoke/codex-home
  CODEX_TASK_MATRIX_WORKDIR=.tmp/codex-task-matrix-smoke
  OPENAI_API_KEY=shim-dev-key
  OPENAI_BASE_URL=http://127.0.0.1:18080/v1

This smoke runs the real Codex CLI against the shim using openai_base_url and
executes a deterministic matrix of small coding tasks. Each case verifies both
Codex JSON events and local filesystem results.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd go
require_cmd jq
require_cmd mktemp
require_cmd python3

codex_bin="${CODEX_BIN:-codex}"
require_cmd "${codex_bin}"

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
model="${MODEL:-devstack-model}"
base_dir="${CODEX_TASK_MATRIX_WORKDIR:-.tmp/codex-task-matrix-smoke}"
codex_home="${CODEX_HOME:-${base_dir}/codex-home}"
api_key="${OPENAI_API_KEY:-shim-dev-key}"

if [[ -n "${OPENAI_BASE_URL:-}" ]]; then
  openai_base_url="${OPENAI_BASE_URL%/}"
else
  openai_base_url="${shim_base_url%/}/v1"
fi

echo "==> waiting for shim readiness: ${shim_base_url%/}/readyz"
for _ in $(seq 1 60); do
  if curl -fsS "${shim_base_url%/}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "${shim_base_url%/}/readyz" >/dev/null

mkdir -p "${base_dir}" "${codex_home}"

verify_codex_events() {
  local output_file="$1"
  local expected_final="$2"
  local json_lines

  if grep -Eiq 'ws://[^[:space:]]*/v1/responses.*405|405 Method Not Allowed' "${output_file}"; then
    echo "Codex CLI hit WebSocket HTTP 405; Responses WebSocket transport is expected to work" >&2
    exit 1
  fi

  json_lines="$(grep '^{' "${output_file}" || true)"
  if [[ -z "${json_lines}" ]]; then
    echo "Codex CLI task matrix case did not emit JSON events" >&2
    exit 1
  fi

  printf '%s\n' "${json_lines}" | jq -e 'select(.type == "item.started" and .item.type == "command_execution")' >/dev/null
  printf '%s\n' "${json_lines}" | jq -e --arg expected_final "${expected_final}" 'select(.type == "item.completed" and .item.type == "agent_message" and .item.text == $expected_final)' >/dev/null
  printf '%s\n' "${json_lines}" | jq -e 'select(.type == "turn.completed")' >/dev/null
}

run_codex_case() {
  local case_name="$1"
  local workspace_abs="$2"
  local expected_final="$3"
  local prompt="$4"
  local output_file="${base_dir}/${case_name}/codex.jsonl"
  local smoke_target="${workspace_abs}/smoke_target.txt"

  echo "==> running Codex CLI task matrix case: ${case_name}"
  if ! env \
    CODEX_HOME="${codex_home}" \
    OPENAI_API_KEY="${api_key}" \
    LLAMA_SHIM_CODEX_MATRIX_WORKDIR="${workspace_abs}" \
    LLAMA_SHIM_CODEX_SMOKE_TARGET="${smoke_target}" \
    "${codex_bin}" exec \
      --ephemeral \
      --ignore-user-config \
      --ignore-rules \
      --json \
      -C "${workspace_abs}" \
      -m "${model}" \
      -c "openai_base_url=\"${openai_base_url}\"" \
      -c 'approval_policy="never"' \
      -c 'sandbox_mode="workspace-write"' \
      -c 'model_reasoning_effort="minimal"' \
      -c 'model_reasoning_summary="none"' \
      -c 'shell_environment_policy.inherit="all"' \
      "${prompt}" >"${output_file}" 2>&1; then
    cat "${output_file}" >&2
    exit 1
  fi

  cat "${output_file}"
  verify_codex_events "${output_file}" "${expected_final}"
}

prepare_case_dir() {
  local case_name="$1"
  local case_dir="${base_dir}/${case_name}"

  rm -rf "${case_dir}"
  mkdir -p "${case_dir}/workspace"
  cd "${case_dir}/workspace" && pwd
}

run_basic_patch_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir basic_patch)"
  printf 'name = llama_shim\nstatus = TODO\n' >"${workspace_abs}/smoke_target.txt"

  run_codex_case \
    basic_patch \
    "${workspace_abs}" \
    PATCHED \
    'This is the Codex coding task smoke. Use exec_command to update smoke_target.txt by replacing `status = TODO` with `status = patched-by-codex`. Then reply PATCHED.'

  local actual_content
  local expected_content
  actual_content="$(cat "${workspace_abs}/smoke_target.txt")"
  expected_content=$'name = llama_shim\nstatus = patched-by-codex'
  if [[ "${actual_content}" != "${expected_content}" ]]; then
    echo "basic_patch did not update smoke_target.txt as expected" >&2
    exit 1
  fi
}

run_bugfix_go_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir bugfix_go)"
  cat >"${workspace_abs}/go.mod" <<'EOF'
module codexmatrix

go 1.22
EOF
  cat >"${workspace_abs}/calc.go" <<'EOF'
package codexmatrix

func Add(a, b int) int {
	return a - b
}
EOF
  cat >"${workspace_abs}/calc_test.go" <<'EOF'
package codexmatrix

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
EOF

  run_codex_case \
    bugfix_go \
    "${workspace_abs}" \
    BUGFIXED \
    'This is the Codex task matrix bugfix go case. Use exec_command to fix calc.go so go test ./... passes. Then reply BUGFIXED.'

  if ! grep -q 'return a + b' "${workspace_abs}/calc.go"; then
    echo "bugfix_go did not patch calc.go as expected" >&2
    exit 1
  fi
  (cd "${workspace_abs}" && GOCACHE="${workspace_abs}/.gocache" go test ./...)
}

run_plan_doc_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir plan_doc)"
  cat >"${workspace_abs}/REQUIREMENTS.md" <<'EOF'
# Requirements

- Read the existing API notes.
- Identify the smallest compatible change.
- Add a regression test before shipping.
EOF

  run_codex_case \
    plan_doc \
    "${workspace_abs}" \
    PLANNED \
    'This is the Codex task matrix plan doc case. Use exec_command to write PLAN.md with the implementation checklist. Then reply PLANNED.'

  local actual_content
  local expected_content
  actual_content="$(cat "${workspace_abs}/PLAN.md")"
  expected_content=$'# Implementation Plan\n\n- [x] Read requirements\n- [x] Identify API change\n- [x] Add regression test'
  if [[ "${actual_content}" != "${expected_content}" ]]; then
    echo "plan_doc did not write PLAN.md as expected" >&2
    exit 1
  fi
}

run_multi_file_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir multi_file)"
  mkdir -p "${workspace_abs}/app"
  printf 'mode=initial\nfeature=disabled\n' >"${workspace_abs}/app/config.txt"
  printf 'status=stale\n' >"${workspace_abs}/app/status.txt"

  run_codex_case \
    multi_file \
    "${workspace_abs}" \
    MULTIFILE \
    'This is the Codex task matrix multi file case. Use exec_command to update app/config.txt and app/status.txt. Then reply MULTIFILE.'

  if [[ "$(cat "${workspace_abs}/app/config.txt")" != $'mode=matrix\nfeature=enabled' ]]; then
    echo "multi_file did not update app/config.txt as expected" >&2
    exit 1
  fi
  if [[ "$(cat "${workspace_abs}/app/status.txt")" != "status=updated" ]]; then
    echo "multi_file did not update app/status.txt as expected" >&2
    exit 1
  fi
}

run_basic_patch_case
run_bugfix_go_case
run_plan_doc_case
run_multi_file_case

echo "codex cli task matrix smoke passed"
