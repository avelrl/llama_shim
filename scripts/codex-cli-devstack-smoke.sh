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
  ./scripts/codex-cli-devstack-smoke.sh

Optional:
  CODEX_BIN=codex
  CODEX_HOME=.tmp/codex-smoke
  OPENAI_API_KEY=shim-dev-key
  OPENAI_BASE_URL=http://127.0.0.1:18080/v1

This smoke runs the real Codex CLI against the shim using the built-in
openai_base_url provider setting. WebSocket support is expected to be
available; HTTP 405 from ws://.../v1/responses is treated as a failure.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd jq
require_cmd mktemp

codex_bin="${CODEX_BIN:-codex}"
require_cmd "${codex_bin}"

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
model="${MODEL:-devstack-model}"
codex_home="${CODEX_HOME:-.tmp/codex-smoke}"
api_key="${OPENAI_API_KEY:-shim-dev-key}"

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

mkdir -p "${codex_home}"

echo "==> running Codex CLI through openai_base_url: ${openai_base_url}"
if ! env CODEX_HOME="${codex_home}" OPENAI_API_KEY="${api_key}" "${codex_bin}" exec \
  --ephemeral \
  --ignore-user-config \
  --ignore-rules \
  --json \
  -C "$(pwd)" \
  -m "${model}" \
  -c "openai_base_url=\"${openai_base_url}\"" \
  -c 'model_reasoning_effort="minimal"' \
  -c 'model_reasoning_summary="none"' \
  'Use exec_command to run pwd, then reply READY.' >"${tmp_output}" 2>&1; then
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
  echo "Codex CLI smoke did not emit JSON events" >&2
  exit 1
fi

printf '%s\n' "${json_lines}" | jq -e 'select(.type == "item.started" and .item.type == "command_execution")' >/dev/null
printf '%s\n' "${json_lines}" | jq -e 'select(.type == "item.completed" and .item.type == "agent_message" and .item.text == "READY")' >/dev/null
printf '%s\n' "${json_lines}" | jq -e 'select(.type == "turn.completed")' >/dev/null

echo "codex cli devstack smoke passed"
