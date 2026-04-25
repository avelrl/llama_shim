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
  ./scripts/v3-constrained-decoding-smoke.sh

Optional:
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'

This smoke path checks the V3 constrained-decoding shim slice:
  1. /debug/capabilities reports shim_validate_repair and no native parity claim
  2. non-stream direct grammar custom_tool_call is locally generated and validated
  3. create-stream emits typed custom_tool_call input events for the same path
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd awk
require_cmd curl
require_cmd grep
require_cmd jq
require_cmd mktemp

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
model="${MODEL:-devstack-model}"
auth_header="${SHIM_AUTH_HEADER:-}"

tmp_dir="$(mktemp -d)"
response_ids=()

cleanup() {
  for response_id in "${response_ids[@]:-}"; do
    if [[ -n "${response_id}" && "${response_id}" != "null" ]]; then
      curl_shim -X DELETE "${shim_base_url}/v1/responses/${response_id}" >/dev/null || true
    fi
  done
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

curl_shim() {
  if [[ -n "${auth_header}" ]]; then
    curl -fsS -H "${auth_header}" "$@"
  else
    curl -fsS "$@"
  fi
}

curl_shim_stream() {
  if [[ -n "${auth_header}" ]]; then
    curl -fsS -N -H "${auth_header}" "$@"
  else
    curl -fsS -N "$@"
  fi
}

post_json() {
  local body="$1"
  curl_shim "${shim_base_url}/v1/responses" \
    -H 'Content-Type: application/json' \
    -d "${body}"
}

sse_event_json() {
  local file="$1"
  local event="$2"

  awk -v wanted="event: ${event}" '
    $0 == wanted {
      getline
      if ($0 ~ /^data: /) {
        sub(/^data: /, "", $0)
        print
        exit
      }
    }
  ' "${file}"
}

require_sse_event() {
  local file="$1"
  local event="$2"

  if ! grep -q "^event: ${event}$" "${file}"; then
    echo "missing SSE event ${event} in ${file}" >&2
    sed -n '1,120p' "${file}" >&2
    exit 1
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

  echo "timed out waiting for ${label}: ${url}" >&2
  exit 1
}

math_tool_json() {
  jq -nc '{
    type: "custom",
    name: "math_exp",
    format: {
      type: "grammar",
      syntax: "lark",
      definition: "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT"
    }
  }'
}

wait_http_ok "shim readiness" "${shim_base_url}/readyz"

echo "==> checking constrained decoding capability flags"
capabilities_json="$(curl_shim "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq '{
  ready,
  constrained_decoding: .runtime.constrained_decoding
}'
printf '%s\n' "${capabilities_json}" | jq -e '
  .runtime.constrained_decoding.enabled == true and
  .runtime.constrained_decoding.support == "shim_validate_repair" and
  .runtime.constrained_decoding.runtime == "chat_completions_json_schema_hint" and
  .runtime.constrained_decoding.capability_class == "none" and
  .runtime.constrained_decoding.native_available == false and
  .runtime.constrained_decoding.native_backend == "none" and
  (.runtime.constrained_decoding.custom_tools.formats | index("grammar.regex")) and
  (.runtime.constrained_decoding.custom_tools.formats | index("grammar.lark_subset"))
' >/dev/null

echo "==> checking non-stream direct grammar custom tool"
math_tool="$(math_tool_json)"
non_stream_json="$(post_json "$(jq -nc --arg model "${model}" --argjson tool "${math_tool}" '{
  model: $model,
  store: true,
  tool_choice: {type: "custom", name: "math_exp"},
  input: "Use grammar tool.",
  tools: [$tool]
}')")"
non_stream_response_id="$(printf '%s' "${non_stream_json}" | jq -r '.id')"
response_ids+=("${non_stream_response_id}")
printf '%s\n' "${non_stream_json}" | jq '{
  id,
  status,
  output_type: .output[0].type,
  tool_name: .output[0].name,
  tool_input: .output[0].input
}'
printf '%s\n' "${non_stream_json}" | jq -e '
  .status == "completed" and
  .output[0].type == "custom_tool_call" and
  .output[0].name == "math_exp" and
  .output[0].input == "4 + 4"
' >/dev/null

echo "==> checking streamed direct grammar custom tool"
stream_path="${tmp_dir}/constrained-create-stream.sse"
curl_shim_stream "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg model "${model}" --argjson tool "${math_tool}" '{
    model: $model,
    stream: true,
    store: true,
    tool_choice: {type: "custom", name: "math_exp"},
    input: "Use grammar tool.",
    tools: [$tool]
  }')" > "${stream_path}"
require_sse_event "${stream_path}" "response.custom_tool_call_input.delta"
require_sse_event "${stream_path}" "response.custom_tool_call_input.done"
require_sse_event "${stream_path}" "response.completed"
stream_done_json="$(sse_event_json "${stream_path}" "response.custom_tool_call_input.done")"
printf '%s\n' "${stream_done_json}" | jq '{
  item_type: .item.type,
  tool_name: .item.name,
  tool_input: .item.input
}'
printf '%s\n' "${stream_done_json}" | jq -e '
  .item.type == "custom_tool_call" and
  .item.name == "math_exp" and
  .item.input == "4 + 4"
' >/dev/null

stream_completed_json="$(sse_event_json "${stream_path}" "response.completed")"
stream_response_id="$(printf '%s\n' "${stream_completed_json}" | jq -r '.response.id')"
response_ids+=("${stream_response_id}")

echo "v3 constrained decoding smoke passed"
