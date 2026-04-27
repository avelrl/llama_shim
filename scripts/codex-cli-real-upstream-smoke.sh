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
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_MODEL=Kimi-K2.6 \
  GW_API_KEY=shim-dev-key \
  ./scripts/codex-cli-real-upstream-smoke.sh

Optional:
  CODEX_BIN=codex
  CODEX_PROVIDER=gateway-shim
  CODEX_BASE_URL=http://127.0.0.1:8080/v1
  CODEX_API_KEY_ENV=GW_API_KEY
  CODEX_API_KEY=shim-dev-key
  CODEX_HOME=.tmp/codex-real-upstream-smoke/codex-home
  CODEX_REAL_SMOKE_WORKDIR=.tmp/codex-real-upstream-smoke
  CODEX_REAL_SMOKE_CASES=boot,read,write,bugfix
  CODEX_REAL_SMOKE_WEBSOCKETS=false
  CODEX_REAL_SMOKE_UNIFIED_EXEC=true

This smoke runs the real Codex CLI against a running shim and real upstream
using a temporary custom Codex provider. It verifies small local results instead
of depending on exact model wording.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd go
require_cmd jq
require_cmd python3

codex_bin="${CODEX_BIN:-codex}"
require_cmd "${codex_bin}"

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
model="${CODEX_MODEL:-${MODEL:-Kimi-K2.6}}"
provider="${CODEX_PROVIDER:-gateway-shim}"
base_url="${CODEX_BASE_URL:-${shim_base_url%/}/v1}"
api_key_env="${CODEX_API_KEY_ENV:-GW_API_KEY}"
api_key_value="${CODEX_API_KEY:-${!api_key_env:-}}"
base_dir="${CODEX_REAL_SMOKE_WORKDIR:-.tmp/codex-real-upstream-smoke}"
codex_home="${CODEX_HOME:-${base_dir}/codex-home}"
case_list="${CODEX_REAL_SMOKE_CASES:-boot,read,write,bugfix}"
supports_websockets="${CODEX_REAL_SMOKE_WEBSOCKETS:-false}"
unified_exec="${CODEX_REAL_SMOKE_UNIFIED_EXEC:-true}"

toml_bool() {
  case "$1" in
    true|false) printf '%s' "$1" ;;
    1|yes|on) printf 'true' ;;
    0|no|off) printf 'false' ;;
    *)
      echo "invalid boolean value: $1" >&2
      exit 1
      ;;
  esac
}

supports_websockets="$(toml_bool "${supports_websockets}")"
unified_exec="$(toml_bool "${unified_exec}")"

if [[ -z "${api_key_value}" ]]; then
  echo "missing API key: set CODEX_API_KEY or ${api_key_env}" >&2
  exit 1
fi

mkdir -p "${base_dir}" "${codex_home}"

echo "==> waiting for shim health: ${shim_base_url%/}/healthz"
health_ready=false
for _ in $(seq 1 60); do
  if curl -fsS "${shim_base_url%/}/healthz" >/dev/null 2>&1; then
    health_ready=true
    break
  fi
  sleep 1
done
if [[ "${health_ready}" != true ]]; then
  echo "shim health probe failed: ${shim_base_url%/}/healthz" >&2
  exit 1
fi

echo "==> checking authorized upstream model path: ${shim_base_url%/}/v1/models"
if ! models_probe="$(curl -fsS "${shim_base_url%/}/v1/models" \
  -H "Authorization: Bearer ${api_key_value}")"; then
  echo "authorized /v1/models probe through shim failed" >&2
  echo "check SHIM_BASE_URL, llama.base_url, upstream auth, and ${api_key_env}" >&2
  exit 1
fi
if ! jq -e '.object == "list" and (.data | type == "array")' <<<"${models_probe}" >/dev/null; then
  echo "authorized /v1/models probe returned an unexpected shape" >&2
  printf '%s\n' "${models_probe}" >&2
  exit 1
fi

cat >"${codex_home}/config.toml" <<EOF
model = "${model}"
model_provider = "${provider}"
approval_policy = "never"
sandbox_mode = "workspace-write"
web_search = "disabled"

[history]
persistence = "none"

[features]
apps = false
memories = false
multi_agent = false
apply_patch_freeform = true
unified_exec = ${unified_exec}

[apps._default]
enabled = false
default_tools_enabled = false

[model_providers.${provider}]
name = "${provider} real upstream smoke"
base_url = "${base_url}"
wire_api = "responses"
env_key = "${api_key_env}"
supports_websockets = ${supports_websockets}
request_max_retries = 1
stream_max_retries = 0
stream_idle_timeout_ms = 180000
EOF

json_lines() {
  grep -a '^{' "$1" || true
}

require_json_events() {
  local output_file="$1"
  local lines
  lines="$(json_lines "${output_file}")"
  if [[ -z "${lines}" ]]; then
    echo "Codex did not emit JSON events for ${output_file}" >&2
    exit 1
  fi
  printf '%s\n' "${lines}" | jq -e 'select(.type == "turn.completed")' >/dev/null
}

require_command_event() {
  local output_file="$1"
  local lines
  lines="$(json_lines "${output_file}")"
  printf '%s\n' "${lines}" | jq -e 'select(.type == "item.started" and .item.type == "command_execution")' >/dev/null
}

require_no_unsupported_apply_patch() {
  local output_file="$1"
  if grep -a -q 'unsupported call: apply_patch' "${output_file}"; then
    echo "Codex reported unsupported apply_patch; check apply_patch_freeform/model metadata wiring" >&2
    cat "${output_file}" >&2
    exit 1
  fi
}

agent_text() {
  local output_file="$1"
  json_lines "${output_file}" \
    | jq -r 'select(.type == "item.completed" and .item.type == "agent_message") | .item.text // empty' \
    | tail -n 1
}

require_agent_text_contains() {
  local output_file="$1"
  local expected="$2"
  local text
  text="$(agent_text "${output_file}")"
  if [[ "${text}" != *"${expected}"* ]]; then
    echo "Codex final answer did not contain ${expected}" >&2
    echo "final answer: ${text}" >&2
    exit 1
  fi
}

prepare_case_dir() {
  local case_name="$1"
  local case_dir="${base_dir}/${case_name}"

  rm -rf "${case_dir}"
  mkdir -p "${case_dir}/workspace"
  cd "${case_dir}/workspace" && pwd
}

run_codex_case() {
  local case_name="$1"
  local workspace_abs="$2"
  local prompt="$3"
  local output_file="${base_dir}/${case_name}/codex.jsonl"

  echo "==> running Codex real-upstream case: ${case_name}"
  if ! env \
    CODEX_HOME="${codex_home}" \
    "${api_key_env}=${api_key_value}" \
    "${codex_bin}" exec \
      --ephemeral \
      --ignore-rules \
      --skip-git-repo-check \
      --json \
      -C "${workspace_abs}" \
      -m "${model}" \
      -c "model_provider=\"${provider}\"" \
      -c 'approval_policy="never"' \
      -c 'sandbox_mode="workspace-write"' \
      -c 'model_reasoning_effort="high"' \
      -c 'model_reasoning_summary="none"' \
      -c 'web_search="disabled"' \
      -c 'shell_environment_policy.inherit="all"' \
      "${prompt}" >"${output_file}" 2>&1; then
    cat "${output_file}" >&2
    exit 1
  fi

  cat "${output_file}"
  require_json_events "${output_file}"
  require_no_unsupported_apply_patch "${output_file}"
}

run_boot_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir boot)"

  run_codex_case \
    boot \
    "${workspace_abs}" \
    'Reply with exactly BOOT_OK and do not edit files.'

  require_agent_text_contains "${base_dir}/boot/codex.jsonl" "BOOT_OK"
}

run_read_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir read)"
  printf 'codex-smoke-token: llama-shim-42\n' >"${workspace_abs}/README.md"

  run_codex_case \
    read \
    "${workspace_abs}" \
    'Use local command execution to read README.md. If it contains token llama-shim-42, reply exactly READ_OK.'

  require_command_event "${base_dir}/read/codex.jsonl"
  require_agent_text_contains "${base_dir}/read/codex.jsonl" "READ_OK"
}

run_write_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir write)"
  printf 'name = llama_shim\nstatus = TODO\n' >"${workspace_abs}/smoke_target.txt"

  run_codex_case \
    write \
    "${workspace_abs}" \
    'Use local command execution to update smoke_target.txt by replacing `status = TODO` with `status = patched-by-codex`. Then read the file back and reply exactly WRITE_OK.'

  require_command_event "${base_dir}/write/codex.jsonl"
  if [[ "$(cat "${workspace_abs}/smoke_target.txt")" != $'name = llama_shim\nstatus = patched-by-codex' ]]; then
    echo "write case did not update smoke_target.txt as expected" >&2
    exit 1
  fi
}

run_bugfix_case() {
  local workspace_abs
  workspace_abs="$(prepare_case_dir bugfix)"
  cat >"${workspace_abs}/go.mod" <<'EOF'
module codexsmoke

go 1.22
EOF
  cat >"${workspace_abs}/mathutil.go" <<'EOF'
package codexsmoke

func Add(a, b int) int {
	return a - b
}
EOF
  cat >"${workspace_abs}/mathutil_test.go" <<'EOF'
package codexsmoke

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
EOF

  run_codex_case \
    bugfix \
    "${workspace_abs}" \
    'Use local command execution. First run go test ./.... Then inspect mathutil.go, fix Add with the smallest possible change, run go test ./... again, and reply exactly BUGFIX_OK.'

  require_command_event "${base_dir}/bugfix/codex.jsonl"
  if ! grep -q 'return a + b' "${workspace_abs}/mathutil.go"; then
    echo "bugfix case did not patch mathutil.go as expected" >&2
    exit 1
  fi
  (cd "${workspace_abs}" && GOCACHE="${workspace_abs}/.gocache" go test ./...)
}

IFS=',' read -r -a cases <<<"${case_list}"
for case_name in "${cases[@]}"; do
  case_name="${case_name//[[:space:]]/}"
  case "${case_name}" in
    boot) run_boot_case ;;
    read) run_read_case ;;
    write) run_write_case ;;
    bugfix) run_bugfix_case ;;
    "")
      ;;
    *)
      echo "unknown Codex real-upstream smoke case: ${case_name}" >&2
      exit 1
      ;;
  esac
done

echo "codex cli real upstream smoke passed"
