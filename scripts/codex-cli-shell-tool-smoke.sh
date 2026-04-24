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
  ./scripts/codex-cli-shell-tool-smoke.sh

Optional:
  CODEX_BIN=codex
  CODEX_HOME=.tmp/codex-shell-tool-smoke/codex-home
  CODEX_SHELL_TOOL_SMOKE_WORKDIR=.tmp/codex-shell-tool-smoke/workspace
  OPENAI_API_KEY=shim-dev-key
  OPENAI_BASE_URL=http://127.0.0.1:18080/v1
  SHIM_SQLITE_PATH=.data/devstack/shim.db

This smoke runs the real Codex CLI with features.unified_exec=false and
verifies that Codex sent the fallback function tool named shell rather than
the default unified exec_command/write_stdin tools.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd jq
require_cmd mktemp
require_cmd sqlite3

codex_bin="${CODEX_BIN:-codex}"
require_cmd "${codex_bin}"

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
model="${MODEL:-devstack-model}"
workspace="${CODEX_SHELL_TOOL_SMOKE_WORKDIR:-.tmp/codex-shell-tool-smoke/workspace}"
codex_home="${CODEX_HOME:-.tmp/codex-shell-tool-smoke/codex-home}"
api_key="${OPENAI_API_KEY:-shim-dev-key}"
sqlite_path="${SHIM_SQLITE_PATH:-.data/devstack/shim.db}"
smoke_marker="LLAMA_SHIM_CODEX_SHELL_TOOL_SMOKE"

if [[ -n "${OPENAI_BASE_URL:-}" ]]; then
  openai_base_url="${OPENAI_BASE_URL%/}"
else
  openai_base_url="${shim_base_url%/}/v1"
fi

tmp_output="$(mktemp)"
trap 'rm -f "${tmp_output}"' EXIT

echo "==> waiting for shim readiness: ${shim_base_url%/}/readyz"
for _ in $(seq 1 60); do
  if curl -fsS "${shim_base_url%/}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "${shim_base_url%/}/readyz" >/dev/null

if [[ ! -f "${sqlite_path}" ]]; then
  echo "missing shim sqlite database: ${sqlite_path}" >&2
  exit 1
fi

mkdir -p "${workspace}" "${codex_home}"
workspace_abs="$(cd "${workspace}" && pwd)"

echo "==> running Codex CLI shell-tool smoke through openai_base_url: ${openai_base_url}"
if ! env CODEX_HOME="${codex_home}" OPENAI_API_KEY="${api_key}" "${codex_bin}" exec \
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
  -c 'features.unified_exec=false' \
  "This is ${smoke_marker}. Use the shell tool to run pwd, then reply READY." >"${tmp_output}" 2>&1; then
  cat "${tmp_output}" >&2
  exit 1
fi

cat "${tmp_output}"

if grep -Eiq 'ws://[^[:space:]]*/v1/responses.*405|405 Method Not Allowed' "${tmp_output}"; then
  echo "Codex CLI hit WebSocket HTTP 405; Responses WebSocket transport is expected to work" >&2
  exit 1
fi

json_lines="$(grep '^{' "${tmp_output}" || true)"
if [[ -z "${json_lines}" ]]; then
  echo "Codex CLI shell-tool smoke did not emit JSON events" >&2
  exit 1
fi

printf '%s\n' "${json_lines}" | jq -e 'select(.type == "item.started" and .item.type == "command_execution")' >/dev/null
printf '%s\n' "${json_lines}" | jq -e 'select(.type == "item.completed" and .item.type == "agent_message" and .item.text == "READY")' >/dev/null
printf '%s\n' "${json_lines}" | jq -e 'select(.type == "turn.completed")' >/dev/null

latest_id="$(
  sqlite3 -noheader "${sqlite_path}" \
    "select id from responses where request_json like '%${smoke_marker}%' order by created_at desc limit 1;"
)"
if [[ -z "${latest_id}" ]]; then
  echo "could not find stored smoke request containing ${smoke_marker}" >&2
  exit 1
fi

tool_names="$(
  sqlite3 -noheader "${sqlite_path}" \
    "select coalesce(json_extract(tool.value, '$.name'), json_extract(tool.value, '$.function.name'), json_extract(tool.value, '$.type')) from responses, json_each(responses.request_json, '$.tools') as tool where responses.id = '${latest_id}';"
)"

if ! printf '%s\n' "${tool_names}" | grep -qx 'shell'; then
  echo "stored Codex request did not include function tool shell" >&2
  printf 'tools seen:\n%s\n' "${tool_names}" >&2
  exit 1
fi

if printf '%s\n' "${tool_names}" | grep -Eqx 'exec_command|write_stdin'; then
  echo "stored Codex request unexpectedly used unified exec tools" >&2
  printf 'tools seen:\n%s\n' "${tool_names}" >&2
  exit 1
fi

echo "codex cli shell-tool smoke passed"
