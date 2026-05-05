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
  ./scripts/v3-local-runtimes-smoke.sh

Optional:
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'

This smoke path checks the V3 shim-local computer runtime subset:
  1. /debug/capabilities exposes computer as enabled chat_completions runtime
  2. non-stream screenshot-first computer_call
  3. stored retrieve and input_items preservation
  4. follow-up computer_call_output screenshot loop
  5. create-stream and retrieve-stream generic replay
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd awk
require_cmd curl
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

  echo "${label} did not become ready: ${url}" >&2
  exit 1
}

echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
wait_http_ok "shim" "${shim_base_url}/readyz"

echo "==> checking local computer capability flags"
capabilities_json="$(curl_shim "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq '{
  object,
  ready,
  responses_mode: .runtime.responses_mode,
  computer: .tools.computer,
  computer_runtime: .probes.computer_runtime
}'
printf '%s\n' "${capabilities_json}" | jq -e '
  .object == "shim.capabilities" and
  .tools.computer.enabled == true and
  .tools.computer.support == "local_subset_when_configured" and
  .tools.computer.backend == "chat_completions" and
  .probes.computer_runtime.enabled == true and
  .probes.computer_runtime.checked == true and
  .probes.computer_runtime.ready == true
' >/dev/null

echo "==> creating screenshot-first computer_call"
first="$(post_json "$(jq -nc --arg model "${model}" '{
  model: $model,
  store: true,
  input: "Use the computer tool. First request a screenshot and do not take any other action until you receive it. After you receive the screenshot, if there is a clearly visible text input or search field, click it and type penguin.",
  tools: [{type: "computer"}],
  tool_choice: "required",
  include: ["computer_call_output.output.image_url"]
}')")"
first_response_id="$(printf '%s\n' "${first}" | jq -r '.id')"
first_call_id="$(printf '%s\n' "${first}" | jq -r '.output[0].call_id')"
response_ids+=("${first_response_id}")
printf '%s\n' "${first}" | jq '{id, status, output_type: .output[0].type, action: .output[0].actions[0]}'
printf '%s\n' "${first}" | jq -e '
  .status == "completed" and
  .output[0].type == "computer_call" and
  (.output[0].call_id | type == "string" and length > 0) and
  .output[0].actions == [{type: "screenshot"}]
' >/dev/null

echo "==> checking stored retrieve"
curl_shim "${shim_base_url}/v1/responses/${first_response_id}" | jq -e --arg response_id "${first_response_id}" --arg call_id "${first_call_id}" '
  .id == $response_id and
  .output[0].type == "computer_call" and
  .output[0].call_id == $call_id and
  .output[0].actions == [{type: "screenshot"}]
' >/dev/null

echo "==> sending computer_call_output screenshot follow-up"
second="$(post_json "$(jq -nc \
  --arg model "${model}" \
  --arg previous_response_id "${first_response_id}" \
  --arg call_id "${first_call_id}" \
  '{
    model: $model,
    store: true,
    previous_response_id: $previous_response_id,
    include: ["computer_call_output.output.image_url"],
    input: [{
      type: "computer_call_output",
      call_id: $call_id,
      output: {
        type: "computer_screenshot",
        image_url: "data:image/png;base64,ZmFrZS1zY3JlZW5zaG90",
        detail: "original"
      }
    }],
    tools: [{type: "computer"}],
    tool_choice: "required"
  }')")"
second_response_id="$(printf '%s\n' "${second}" | jq -r '.id')"
response_ids+=("${second_response_id}")
printf '%s\n' "${second}" | jq '{id, status, previous_response_id, actions: .output[0].actions}'
printf '%s\n' "${second}" | jq -e --arg previous_response_id "${first_response_id}" '
  .status == "completed" and
  .previous_response_id == $previous_response_id and
  .output[0].type == "computer_call" and
  .output[0].actions[0].type == "click" and
  .output[0].actions[1].type == "type" and
  .output[0].actions[1].text == "penguin"
' >/dev/null

echo "==> checking stored input_items"
curl_shim "${shim_base_url}/v1/responses/${second_response_id}/input_items?order=asc" | jq -e --arg call_id "${first_call_id}" '
  any(.data[]; .type == "computer_call_output" and .call_id == $call_id and .output.type == "computer_screenshot" and .output.detail == "original")
' >/dev/null

echo "==> creating streamed computer_call"
stream_sse="${tmp_dir}/computer.sse"
curl_shim_stream "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -d "$(jq -nc --arg model "${model}" '{
    model: $model,
    store: true,
    stream: true,
    input: "Use the computer tool. First request a screenshot and do not take any other action until you receive it.",
    tools: [{type: "computer"}],
    tool_choice: "required"
  }')" \
  > "${stream_sse}"
require_sse_event "${stream_sse}" "response.output_item.added"
require_sse_event "${stream_sse}" "response.output_item.done"
require_sse_event "${stream_sse}" "response.completed"
if grep -q '^event: response.computer_call' "${stream_sse}"; then
  echo "local computer stream unexpectedly emitted hosted-specific response.computer_call events" >&2
  exit 1
fi
done_json="$(sse_event_json "${stream_sse}" "response.output_item.done")"
printf '%s\n' "${done_json}" | jq -e '
  .item.type == "computer_call" and
  .item.actions == [{type: "screenshot"}]
' >/dev/null
completed_json="$(sse_event_json "${stream_sse}" "response.completed")"
stream_response_id="$(printf '%s\n' "${completed_json}" | jq -r '.response.id')"
response_ids+=("${stream_response_id}")

echo "==> checking retrieve-stream replay"
replay_sse="${tmp_dir}/computer_replay.sse"
curl_shim_stream "${shim_base_url}/v1/responses/${stream_response_id}?stream=true" \
  -H 'Accept: text/event-stream' \
  > "${replay_sse}"
require_sse_event "${replay_sse}" "response.output_item.done"
require_sse_event "${replay_sse}" "response.completed"
replay_done_json="$(sse_event_json "${replay_sse}" "response.output_item.done")"
printf '%s\n' "${replay_done_json}" | jq -e '
  .item.type == "computer_call" and
  .item.actions == [{type: "screenshot"}]
' >/dev/null

echo "v3 local runtimes smoke passed"
