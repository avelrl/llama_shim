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
  RESPONSES_COMPAT_TESTER_CMD='<external tester command>' \
  ./scripts/responses-compat-external-smoke.sh

Optional:
  OPENAI_BASE_URL=http://127.0.0.1:18080/v1
  OPENAI_API_KEY=shim-test-key
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'
  RESPONSES_COMPAT_PROFILE=responses-broad-subset
  RESPONSES_COMPAT_ARTIFACT_DIR=.data/responses-compat-external
  RESPONSES_COMPAT_RUN_ID=manual
  RESPONSES_COMPAT_REQUIRE_TESTER=1

If RESPONSES_COMPAT_TESTER_CMD is unset, the script performs a capture-only
preflight: it probes /readyz and /debug/capabilities, writes artifacts, and
exits successfully unless RESPONSES_COMPAT_REQUIRE_TESTER=1.

The external command receives:
  OPENAI_BASE_URL
  OPENAI_API_KEY
  SHIM_BASE_URL
  SHIM_AUTH_HEADER
  SHIM_CAPABILITIES_FILE
  RESPONSES_COMPAT_PROFILE
  RESPONSES_COMPAT_ARTIFACT_DIR
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd bash
require_cmd curl
require_cmd date
require_cmd mkdir
require_cmd tail

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
shim_base_url="${shim_base_url%/}"
openai_base_url="${OPENAI_BASE_URL:-${shim_base_url}/v1}"
openai_base_url="${openai_base_url%/}"
openai_api_key="${OPENAI_API_KEY:-shim-test-key}"
auth_header="${SHIM_AUTH_HEADER:-}"

profile="${RESPONSES_COMPAT_PROFILE:-responses-broad-subset}"
artifact_root="${RESPONSES_COMPAT_ARTIFACT_DIR:-.data/responses-compat-external}"
case "${artifact_root}" in
  /*) ;;
  *) artifact_root="$(pwd)/${artifact_root}" ;;
esac
run_id="${RESPONSES_COMPAT_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
artifact_dir="${artifact_root%/}/${run_id}"
tester_cmd="${RESPONSES_COMPAT_TESTER_CMD:-${OPENAI_COMPAT_TESTER_CMD:-}}"
require_tester="${RESPONSES_COMPAT_REQUIRE_TESTER:-0}"

mkdir -p "${artifact_dir}"

curl_shim() {
  if [[ -n "${auth_header}" ]]; then
    curl -fsS -H "${auth_header}" "$@"
  else
    curl -fsS "$@"
  fi
}

wait_http_ok() {
  local label="$1"
  local url="$2"

  for _ in $(seq 1 60); do
    if curl_shim "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "${label} did not become ready: ${url}" >&2
  exit 1
}

write_run_env() {
  cat >"${artifact_dir}/run.env" <<EOF
SHIM_BASE_URL=${shim_base_url}
OPENAI_BASE_URL=${openai_base_url}
OPENAI_API_KEY_SET=$([[ -n "${openai_api_key}" ]] && echo true || echo false)
SHIM_AUTH_HEADER_SET=$([[ -n "${auth_header}" ]] && echo true || echo false)
RESPONSES_COMPAT_PROFILE=${profile}
RESPONSES_COMPAT_REQUIRE_TESTER=${require_tester}
EOF
}

echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
wait_http_ok "shim" "${shim_base_url}/readyz"

readyz_path="${artifact_dir}/readyz.json"
capabilities_path="${artifact_dir}/capabilities.json"
summary_path="${artifact_dir}/capabilities-summary.json"

echo "==> capturing readiness and capabilities into ${artifact_dir}"
curl_shim "${shim_base_url}/readyz" >"${readyz_path}"
curl_shim "${shim_base_url}/debug/capabilities" >"${capabilities_path}"
write_run_env

if command -v jq >/dev/null 2>&1; then
  jq '{
    object,
    ready,
    runtime: {
      responses_mode: .runtime.responses_mode,
      compaction: .runtime.compaction,
      constrained_decoding: .runtime.constrained_decoding
    },
    surfaces: {
      responses: .surfaces.responses,
      conversations: .surfaces.conversations,
      chat_completions: .surfaces.chat_completions
    },
    tools: .tools
  }' "${capabilities_path}" >"${summary_path}"
  jq '{ready, runtime, surfaces, tools}' "${summary_path}"
else
  cp "${capabilities_path}" "${summary_path}"
  echo "jq not found; wrote raw capabilities only"
fi

if [[ -z "${tester_cmd}" ]]; then
  echo "==> RESPONSES_COMPAT_TESTER_CMD is unset; capture-only preflight complete"
  echo "artifacts: ${artifact_dir}"
  if [[ "${require_tester}" == "1" || "${require_tester}" == "true" ]]; then
    echo "RESPONSES_COMPAT_REQUIRE_TESTER is set; failing because no tester command was provided" >&2
    exit 2
  fi
  exit 0
fi

export OPENAI_BASE_URL="${openai_base_url}"
export OPENAI_API_KEY="${openai_api_key}"
export SHIM_BASE_URL="${shim_base_url}"
export SHIM_AUTH_HEADER="${auth_header}"
export SHIM_CAPABILITIES_FILE="${capabilities_path}"
export RESPONSES_COMPAT_PROFILE="${profile}"
export RESPONSES_COMPAT_ARTIFACT_DIR="${artifact_dir}"

stdout_path="${artifact_dir}/tester.stdout.log"
stderr_path="${artifact_dir}/tester.stderr.log"
exitcode_path="${artifact_dir}/tester.exitcode"

echo "==> running external Responses compatibility tester"
echo "${tester_cmd}" >"${artifact_dir}/tester.command"

set +e
bash -lc "${tester_cmd}" >"${stdout_path}" 2>"${stderr_path}"
tester_status=$?
set -e

printf '%s\n' "${tester_status}" >"${exitcode_path}"

if [[ "${tester_status}" -ne 0 ]]; then
  echo "external tester failed with exit code ${tester_status}" >&2
  echo "stdout tail:" >&2
  tail -n 80 "${stdout_path}" >&2 || true
  echo "stderr tail:" >&2
  tail -n 80 "${stderr_path}" >&2 || true
  echo "artifacts: ${artifact_dir}" >&2
  exit "${tester_status}"
fi

echo "==> external Responses compatibility tester passed"
echo "artifacts: ${artifact_dir}"
