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
  VLLM_BASE_URL=http://127.0.0.1:8000 \
  MODEL=qwen3-8b \
  ./scripts/v3-vllm-constrained-smoke.sh

Optional shim check:
  SHIM_BASE_URL=http://127.0.0.1:18080 \
  SHIM_AUTH_HEADER='Authorization: Bearer <token>' \
  ./scripts/v3-vllm-constrained-smoke.sh

The shim must be started with:
  LLAMA_BASE_URL=$VLLM_BASE_URL
  RESPONSES_CONSTRAINED_DECODING_BACKEND=vllm

This smoke path verifies the vLLM-backed V3 regex-native slice:
  1. direct vLLM /v1/chat/completions accepts structured_outputs.regex
  2. optional shim /debug/capabilities reports regex_native
  3. optional shim /v1/responses emits a regex custom_tool_call generated
     through the vLLM structured_outputs.regex adapter
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd jq

vllm_base_url="${VLLM_BASE_URL:-http://127.0.0.1:8000}"
shim_base_url="${SHIM_BASE_URL:-}"
model="${MODEL:-qwen3-8b}"
auth_header="${SHIM_AUTH_HEADER:-}"

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
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "timed out waiting for ${label}: ${url}" >&2
  exit 1
}

echo "==> checking vLLM models endpoint"
wait_http_ok "vLLM models" "${vllm_base_url}/v1/models"

echo "==> checking direct vLLM structured_outputs.regex"
vllm_json="$(curl -fsS "${vllm_base_url}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg model "${model}" '{
    model: $model,
    messages: [
      {
        role: "user",
        content: "Return exactly hello followed by a space and two digits. No prose."
      }
    ],
    max_tokens: 24,
    temperature: 0,
    structured_outputs: {
      regex: "^(?:hello [0-9]{2})$"
    }
  }')")"
vllm_text="$(printf '%s\n' "${vllm_json}" | jq -r '.choices[0].message.content')"
printf '%s\n' "${vllm_json}" | jq '{id, model, content: .choices[0].message.content}'
if ! [[ "${vllm_text}" =~ ^hello\ [0-9]{2}$ ]]; then
  echo "vLLM structured_outputs.regex did not constrain output: ${vllm_text}" >&2
  exit 1
fi

if [[ -z "${shim_base_url}" ]]; then
  echo "v3 vLLM constrained direct smoke passed"
  exit 0
fi

response_id=""
cleanup() {
  if [[ -n "${response_id}" && "${response_id}" != "null" ]]; then
    curl_shim -X DELETE "${shim_base_url}/v1/responses/${response_id}" >/dev/null || true
  fi
}
trap cleanup EXIT

echo "==> checking shim readiness"
for _ in $(seq 1 60); do
  if curl_shim "${shim_base_url}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl_shim "${shim_base_url}/readyz" >/dev/null

echo "==> checking shim vLLM constrained capability flags"
capabilities_json="$(curl_shim "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq '{ready, constrained_decoding: .runtime.constrained_decoding}'
printf '%s\n' "${capabilities_json}" | jq -e '
  .runtime.constrained_decoding.enabled == true and
  .runtime.constrained_decoding.support == "regex_native_with_validate_repair_fallback" and
  .runtime.constrained_decoding.runtime == "vllm_structured_outputs_regex" and
  .runtime.constrained_decoding.backend == "vllm" and
  .runtime.constrained_decoding.capability_class == "regex_native" and
  .runtime.constrained_decoding.native_available == true and
  .runtime.constrained_decoding.native_backend == "vllm" and
  (.runtime.constrained_decoding.native_formats | index("grammar.regex"))
' >/dev/null

echo "==> checking shim regex custom tool through vLLM adapter"
response_json="$(curl_shim "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg model "${model}" '{
    model: $model,
    store: true,
    tool_choice: {type: "custom", name: "exact_text"},
    input: "Use regex tool.",
    tools: [
      {
        type: "custom",
        name: "exact_text",
        format: {
          type: "grammar",
          syntax: "regex",
          definition: "hello [0-9]{2}"
        }
      }
    ]
  }')")"
response_id="$(printf '%s\n' "${response_json}" | jq -r '.id')"
printf '%s\n' "${response_json}" | jq '{id, status, output_type: .output[0].type, tool_name: .output[0].name, tool_input: .output[0].input}'
printf '%s\n' "${response_json}" | jq -e '
  .status == "completed" and
  .output[0].type == "custom_tool_call" and
  .output[0].name == "exact_text" and
  (.output[0].input | test("^hello [0-9]{2}$"))
' >/dev/null

echo "v3 vLLM constrained smoke passed"
