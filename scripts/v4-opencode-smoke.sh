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
  OPENCODE_SMOKE_MODEL=gpu/qwen3-coder30b-q5km \
  ./scripts/v4-opencode-smoke.sh

Optional:
  MODEL=provider/model
  OPENCODE_BIN=opencode
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'
  SHIM_API_KEY=<token>
  OPENCODE_SMOKE_ARTIFACT_DIR=.tmp/v4-opencode-smoke
  OPENCODE_SMOKE_RUN_ID=manual
  OPENCODE_SMOKE_READY_ATTEMPTS=60

This smoke runs a real local OpenCode CLI against the shim using an isolated
generated config and a small Go bugfix workspace.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd date
require_cmd jq
require_cmd mkdir
require_cmd python3
require_cmd tr

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
opencode_bin="${OPENCODE_BIN:-opencode}"
model="${OPENCODE_SMOKE_MODEL:-${MODEL:-gpu/qwen3-coder30b-q5km}}"
scenario="${OPENCODE_SMOKE_SCENARIO:-bugfix_go}"
artifact_root="${OPENCODE_SMOKE_ARTIFACT_DIR:-.tmp/v4-opencode-smoke}"
run_id="${OPENCODE_SMOKE_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
ready_attempts="${OPENCODE_SMOKE_READY_ATTEMPTS:-60}"

if [[ "${scenario}" != "bugfix_go" ]]; then
  echo "unsupported OPENCODE_SMOKE_SCENARIO: ${scenario}" >&2
  exit 1
fi
require_cmd "${opencode_bin}"

slugify() {
  local value="$1"
  value="$(printf '%s' "${value}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9._-' '-')"
  value="${value#-}"
  value="${value%-}"
  if [[ -z "${value}" ]]; then
    value="value"
  fi
  printf '%s' "${value}"
}

derive_api_key() {
  if [[ -n "${SHIM_API_KEY:-}" ]]; then
    printf '%s' "${SHIM_API_KEY}"
  elif [[ -n "${GW_API_KEY:-}" ]]; then
    printf '%s' "${GW_API_KEY}"
  elif [[ -n "${OPENAI_API_KEY:-}" ]]; then
    printf '%s' "${OPENAI_API_KEY}"
  elif [[ -n "${SHIM_AUTH_HEADER:-}" && "${SHIM_AUTH_HEADER}" == Authorization:\ Bearer\ * ]]; then
    printf '%s' "${SHIM_AUTH_HEADER#Authorization: Bearer }"
  else
    printf 'local-smoke-token'
  fi
}

header_name=""
header_value=""
if [[ -n "${SHIM_AUTH_HEADER:-}" && "${SHIM_AUTH_HEADER}" != Authorization:\ Bearer\ * && "${SHIM_AUTH_HEADER}" == *:* ]]; then
  header_name="${SHIM_AUTH_HEADER%%:*}"
  header_value="${SHIM_AUTH_HEADER#*:}"
  header_value="${header_value# }"
fi

run_dir="${artifact_root%/}/$(slugify "${model}")_${run_id}"
workspace="${run_dir}/workspace"
config_dir="${run_dir}/opencode-config"
config_file="${run_dir}/opencode.json"
stdout_file="${run_dir}/opencode.stdout.jsonl"
stderr_file="${run_dir}/opencode.stderr.log"
verify_file="${run_dir}/verify.log"
summary_file="${run_dir}/summary.json"
provider_id="llama-shim"
model_name="${model}"
opencode_model="${provider_id}/${model_name}"
shim_api_key="$(derive_api_key)"

mkdir -p "${run_dir}" "${workspace}" "${config_dir}"

echo "==> v4 OpenCode smoke: ${model}"
echo "==> checking OpenCode CLI"
"${opencode_bin}" --version >"${run_dir}/opencode.version.txt"

echo "==> waiting for shim readiness: ${shim_base_url%/}/readyz"
ready=0
for _ in $(seq 1 "${ready_attempts}"); do
  if curl -fsS "${shim_base_url%/}/readyz" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [[ "${ready}" -ne 1 ]]; then
  jq -cn \
    --arg model "${model}" \
    --arg artifact_dir "${run_dir}" \
    '{status:"failed",failure_class:"shim_not_ready",model:$model,artifact_dir:$artifact_dir,error:"shim readiness endpoint did not become ready"}' >"${summary_file}"
  echo "shim readiness endpoint did not become ready: ${shim_base_url%/}/readyz" >&2
  echo "artifacts: ${run_dir}" >&2
  exit 1
fi
curl -fsS "${shim_base_url%/}/readyz" >"${run_dir}/readyz.json"

cat >"${workspace}/go.mod" <<'EOF'
module opencodesmoke

go 1.22
EOF

cat >"${workspace}/calc.go" <<'EOF'
package opencodesmoke

func Add(a, b int) int {
	return a - b
}
EOF

cat >"${workspace}/calc_test.go" <<'EOF'
package opencodesmoke

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
EOF
cp "${workspace}/calc_test.go" "${run_dir}/calc_test.go.before"

jq -cn \
  --arg provider "${provider_id}" \
  --arg model "${model_name}" \
  --arg selected "${opencode_model}" \
  --arg base_url "${shim_base_url%/}/v1" \
  --arg header_name "${header_name}" \
  --arg header_value "${header_value}" \
  '{
    "$schema":"https://opencode.ai/config.json",
    enabled_providers:[$provider],
    model:$selected,
    small_model:$selected,
    provider:{
      ($provider):{
        npm:"@ai-sdk/openai-compatible",
        name:"llama_shim",
        options:{
          baseURL:$base_url,
          apiKey:"{env:OPENCODE_SHIM_API_KEY}"
        },
        models:{
          ($model):{
            name:$model,
            limit:{context:32768,output:4096}
          }
        }
      }
    }
  } | if $header_name != "" then .provider[$provider].options.headers = {($header_name):$header_value} else . end' >"${config_file}"

prompt='Fix the failing Go test in this workspace. Inspect the files, edit the code, run go test ./..., and stop when the test passes. Do not change the test.'
printf '%s\n' "${prompt}" >"${run_dir}/prompt.txt"

echo "==> running OpenCode"
set +e
OPENCODE_CONFIG="${config_file}" \
OPENCODE_CONFIG_DIR="${config_dir}" \
OPENCODE_SHIM_API_KEY="${shim_api_key}" \
OPENCODE_DISABLE_MODELS_FETCH=1 \
OPENCODE_DISABLE_DEFAULT_PLUGINS=1 \
OPENCODE_DISABLE_CLAUDE_CODE=1 \
OPENCODE_DISABLE_PRUNE=1 \
"${opencode_bin}" run \
  --dir "${workspace}" \
  --model "${opencode_model}" \
  --format json \
  --dangerously-skip-permissions \
  "${prompt}" >"${stdout_file}" 2>"${stderr_file}"
opencode_status=$?
set -e
printf '%s' "${opencode_status}" >"${run_dir}/opencode.status"

echo "==> verifying workspace"
verify_status=0
{
  echo "opencode_exit=${opencode_status}"
  echo "calc.go:"
  cat "${workspace}/calc.go"
  echo
  echo "go test ./...:"
  (cd "${workspace}" && go test ./...)
} >"${verify_file}" 2>&1 || verify_status=$?
printf '%s' "${verify_status}" >"${run_dir}/verify.status"

if [[ "${opencode_status}" -ne 0 ]]; then
  jq -cn \
    --arg model "${model}" \
    --arg artifact_dir "${run_dir}" \
    --argjson opencode_status "${opencode_status}" \
    --argjson verify_status "${verify_status}" \
    '{status:"failed",failure_class:"opencode_transport_error",model:$model,artifact_dir:$artifact_dir,opencode_exit:$opencode_status,verify_exit:$verify_status}' >"${summary_file}"
  echo "OpenCode failed with exit ${opencode_status}" >&2
  echo "artifacts: ${run_dir}" >&2
  exit 1
fi

if ! grep -q 'return a + b' "${workspace}/calc.go"; then
  jq -cn \
    --arg model "${model}" \
    --arg artifact_dir "${run_dir}" \
    --argjson opencode_status "${opencode_status}" \
    --argjson verify_status "${verify_status}" \
    '{status:"failed",failure_class:"workspace_verification_error",model:$model,artifact_dir:$artifact_dir,opencode_exit:$opencode_status,verify_exit:$verify_status,error:"calc.go did not contain return a + b"}' >"${summary_file}"
  echo "OpenCode did not patch calc.go as expected" >&2
  echo "artifacts: ${run_dir}" >&2
  exit 1
fi

if ! cmp -s "${run_dir}/calc_test.go.before" "${workspace}/calc_test.go"; then
  jq -cn \
    --arg model "${model}" \
    --arg artifact_dir "${run_dir}" \
    --argjson opencode_status "${opencode_status}" \
    --argjson verify_status "${verify_status}" \
    '{status:"failed",failure_class:"workspace_verification_error",model:$model,artifact_dir:$artifact_dir,opencode_exit:$opencode_status,verify_exit:$verify_status,error:"calc_test.go was modified"}' >"${summary_file}"
  echo "OpenCode modified calc_test.go unexpectedly" >&2
  echo "artifacts: ${run_dir}" >&2
  exit 1
fi

if [[ "${verify_status}" -ne 0 ]]; then
  jq -cn \
    --arg model "${model}" \
    --arg artifact_dir "${run_dir}" \
    --argjson opencode_status "${opencode_status}" \
    --argjson verify_status "${verify_status}" \
    '{status:"failed",failure_class:"workspace_verification_error",model:$model,artifact_dir:$artifact_dir,opencode_exit:$opencode_status,verify_exit:$verify_status,error:"go test ./... failed"}' >"${summary_file}"
  echo "OpenCode workspace verification failed" >&2
  echo "artifacts: ${run_dir}" >&2
  exit 1
fi

jq -cn \
  --arg model "${model}" \
  --arg opencode_model "${opencode_model}" \
  --arg artifact_dir "${run_dir}" \
  --arg scenario "${scenario}" \
  '{status:"passed",model:$model,opencode_model:$opencode_model,scenario:$scenario,artifact_dir:$artifact_dir}' >"${summary_file}"

cat >"${run_dir}/summary.md" <<EOF
# V4 OpenCode Smoke

- status: passed
- model: ${model}
- opencode_model: ${opencode_model}
- scenario: ${scenario}
- artifact_dir: ${run_dir}
EOF

echo "v4 OpenCode smoke passed"
echo "artifacts: ${run_dir}"
