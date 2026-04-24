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
  ./scripts/v3-coding-tools-smoke.sh

Optional:
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'

This smoke path checks the shim-local V3 native coding-tools subset:
  1. /debug/capabilities exposes shell and apply_patch native-local flags
  2. non-stream shell_call and shell_call_output follow-up
  3. shell retrieve and input_items preservation
  4. non-stream apply_patch_call and apply_patch_call_output follow-up
  5. apply_patch retrieve and input_items preservation
  6. shell create-stream tool-specific replay
  7. shell retrieve-stream generic typed replay
  8. apply_patch create-stream tool-specific replay
  9. apply_patch retrieve-stream tool-specific replay
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

echo "==> checking native coding-tools capability flags"
capabilities_json="$(curl_shim "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq '{
  object,
  ready,
  responses_mode: .runtime.responses_mode,
  shell: .tools.shell,
  apply_patch: .tools.apply_patch
}'
printf '%s\n' "${capabilities_json}" | jq -e '
  .object == "shim.capabilities" and
  .tools.shell.enabled == true and
  .tools.shell.support == "native_local_subset" and
  .tools.apply_patch.enabled == true and
  .tools.apply_patch.support == "native_local_subset"
' >/dev/null

echo "==> creating shell_call"
first_shell="$(post_json "$(jq -nc --arg model "${model}" '{
  model: $model,
  store: true,
  tool_choice: {type: "shell"},
  input: [{role: "user", content: "Use the shell tool to run exactly this command: pwd"}],
  tools: [{type: "shell", environment: {type: "local"}}]
}')")"
shell_response_id="$(printf '%s\n' "${first_shell}" | jq -r '.id')"
shell_call_id="$(printf '%s\n' "${first_shell}" | jq -r '.output[0].call_id')"
response_ids+=("${shell_response_id}")
printf '%s\n' "${first_shell}" | jq '{id, status, output_type: .output[0].type, command: .output[0].action.commands[0]}'
printf '%s\n' "${first_shell}" | jq -e '
  .status == "completed" and
  .output[0].type == "shell_call" and
  (.output[0].call_id | type == "string" and length > 0) and
  (.output[0].action.commands | type == "array" and length > 0)
' >/dev/null

echo "==> sending shell_call_output"
second_shell="$(post_json "$(jq -nc \
  --arg model "${model}" \
  --arg previous_response_id "${shell_response_id}" \
  --arg call_id "${shell_call_id}" \
  '{
    model: $model,
    store: true,
    previous_response_id: $previous_response_id,
    input: [{
      type: "shell_call_output",
      call_id: $call_id,
      max_output_length: 12000,
      output: [{
        stdout: "tool says hi",
        stderr: "",
        outcome: {type: "exit", exit_code: 0}
      }]
    }],
    tools: [{type: "shell", environment: {type: "local"}}]
  }')")"
second_shell_response_id="$(printf '%s\n' "${second_shell}" | jq -r '.id')"
response_ids+=("${second_shell_response_id}")
printf '%s\n' "${second_shell}" | jq '{id, status, previous_response_id, output_text}'
printf '%s\n' "${second_shell}" | jq -e --arg previous_response_id "${shell_response_id}" '
  .status == "completed" and
  .previous_response_id == $previous_response_id and
  (.output_text | contains("tool says hi"))
' >/dev/null

echo "==> checking shell stored retrieve and input_items"
curl_shim "${shim_base_url}/v1/responses/${second_shell_response_id}" | jq -e --arg id "${second_shell_response_id}" --arg previous_response_id "${shell_response_id}" '
  .id == $id and
  .previous_response_id == $previous_response_id and
  (.output_text | contains("tool says hi"))
' >/dev/null
curl_shim "${shim_base_url}/v1/responses/${second_shell_response_id}/input_items" | jq -e --arg call_id "${shell_call_id}" '
  any(.data[]; .type == "shell_call_output" and .call_id == $call_id and .output[0].stdout == "tool says hi" and .output[0].outcome.exit_code == 0)
' >/dev/null

echo "==> creating apply_patch_call"
patch_prompt=$'The user has the following files:\n<BEGIN_FILES>\n===== game/main.go\npackage game\n\nconst answer = 1\n\nfunc Value() int {\n    return answer\n}\n<END_FILES>\n\nUse apply_patch to change answer from 1 to 2 in game/main.go. Emit patch operations only and do not explain the change yet.'
first_patch="$(post_json "$(jq -nc --arg model "${model}" --arg content "${patch_prompt}" '{
  model: $model,
  store: true,
  tool_choice: {type: "apply_patch"},
  input: [{role: "user", content: $content}],
  tools: [{type: "apply_patch"}]
}')")"
patch_response_id="$(printf '%s\n' "${first_patch}" | jq -r '.id')"
patch_call_id="$(printf '%s\n' "${first_patch}" | jq -r '.output[0].call_id')"
response_ids+=("${patch_response_id}")
printf '%s\n' "${first_patch}" | jq '{id, status, output_type: .output[0].type, operation: .output[0].operation}'
printf '%s\n' "${first_patch}" | jq -e '
  .status == "completed" and
  .output[0].type == "apply_patch_call" and
  (.output[0].call_id | type == "string" and length > 0) and
  (.output[0].operation.type | type == "string" and length > 0) and
  (.output[0].operation.path | type == "string" and length > 0)
' >/dev/null

echo "==> sending apply_patch_call_output"
second_patch="$(post_json "$(jq -nc \
  --arg model "${model}" \
  --arg previous_response_id "${patch_response_id}" \
  --arg call_id "${patch_call_id}" \
  '{
    model: $model,
    store: true,
    previous_response_id: $previous_response_id,
    input: [{
      type: "apply_patch_call_output",
      call_id: $call_id,
      status: "completed",
      output: "patched cleanly"
    }],
    tools: [{type: "apply_patch"}]
  }')")"
second_patch_response_id="$(printf '%s\n' "${second_patch}" | jq -r '.id')"
response_ids+=("${second_patch_response_id}")
printf '%s\n' "${second_patch}" | jq '{id, status, previous_response_id, output_text}'
printf '%s\n' "${second_patch}" | jq -e --arg previous_response_id "${patch_response_id}" '
  .status == "completed" and
  .previous_response_id == $previous_response_id and
  (.output_text | contains("patched cleanly"))
' >/dev/null

echo "==> checking apply_patch stored retrieve and input_items"
curl_shim "${shim_base_url}/v1/responses/${second_patch_response_id}" | jq -e --arg id "${second_patch_response_id}" --arg previous_response_id "${patch_response_id}" '
  .id == $id and
  .previous_response_id == $previous_response_id and
  (.output_text | contains("patched cleanly"))
' >/dev/null
curl_shim "${shim_base_url}/v1/responses/${second_patch_response_id}/input_items" | jq -e --arg call_id "${patch_call_id}" '
  any(.data[]; .type == "apply_patch_call_output" and .call_id == $call_id and .status == "completed" and .output == "patched cleanly")
' >/dev/null

echo "==> checking shell create-stream"
shell_create_stream_path="${tmp_dir}/shell-create-stream.sse"
curl_shim_stream "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg model "${model}" '{
    model: $model,
    store: true,
    stream: true,
    tool_choice: {type: "shell"},
    input: [{role: "user", content: "Use the shell tool to run exactly this command: pwd"}],
    tools: [{type: "shell", environment: {type: "local"}}]
  }')" > "${shell_create_stream_path}"
require_sse_event "${shell_create_stream_path}" "response.output_item.added"
require_sse_event "${shell_create_stream_path}" "response.shell_call_command.added"
require_sse_event "${shell_create_stream_path}" "response.shell_call_command.delta"
require_sse_event "${shell_create_stream_path}" "response.shell_call_command.done"
require_sse_event "${shell_create_stream_path}" "response.output_item.done"
require_sse_event "${shell_create_stream_path}" "response.completed"
shell_stream_response_id="$(sse_event_json "${shell_create_stream_path}" "response.completed" | jq -r '.response.id')"
response_ids+=("${shell_stream_response_id}")
sse_event_json "${shell_create_stream_path}" "response.output_item.done" | jq -e '
  .item.type == "shell_call" and
  (.item.action.commands | type == "array" and length > 0)
' >/dev/null

echo "==> checking shell retrieve-stream"
shell_retrieve_stream_path="${tmp_dir}/shell-retrieve-stream.sse"
curl_shim_stream "${shim_base_url}/v1/responses/${shell_stream_response_id}?stream=true" > "${shell_retrieve_stream_path}"
require_sse_event "${shell_retrieve_stream_path}" "response.output_item.added"
require_sse_event "${shell_retrieve_stream_path}" "response.output_item.done"
require_sse_event "${shell_retrieve_stream_path}" "response.completed"
sse_event_json "${shell_retrieve_stream_path}" "response.output_item.done" | jq -e '
  .item.type == "shell_call" and
  (.item.action.commands | type == "array" and length > 0)
' >/dev/null

echo "==> checking apply_patch create-stream"
patch_create_stream_path="${tmp_dir}/apply-patch-create-stream.sse"
curl_shim_stream "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg model "${model}" --arg content "${patch_prompt}" '{
    model: $model,
    store: true,
    stream: true,
    tool_choice: {type: "apply_patch"},
    input: [{role: "user", content: $content}],
    tools: [{type: "apply_patch"}]
  }')" > "${patch_create_stream_path}"
require_sse_event "${patch_create_stream_path}" "response.output_item.added"
require_sse_event "${patch_create_stream_path}" "response.apply_patch_call_operation_diff.done"
require_sse_event "${patch_create_stream_path}" "response.output_item.done"
require_sse_event "${patch_create_stream_path}" "response.completed"
patch_stream_response_id="$(sse_event_json "${patch_create_stream_path}" "response.completed" | jq -r '.response.id')"
response_ids+=("${patch_stream_response_id}")
sse_event_json "${patch_create_stream_path}" "response.output_item.done" | jq -e '
  .item.type == "apply_patch_call" and
  (.item.operation.type | type == "string" and length > 0) and
  (.item.operation.path | type == "string" and length > 0)
' >/dev/null

echo "==> checking apply_patch retrieve-stream"
patch_retrieve_stream_path="${tmp_dir}/apply-patch-retrieve-stream.sse"
curl_shim_stream "${shim_base_url}/v1/responses/${patch_stream_response_id}?stream=true" > "${patch_retrieve_stream_path}"
require_sse_event "${patch_retrieve_stream_path}" "response.output_item.added"
require_sse_event "${patch_retrieve_stream_path}" "response.apply_patch_call_operation_diff.done"
require_sse_event "${patch_retrieve_stream_path}" "response.output_item.done"
require_sse_event "${patch_retrieve_stream_path}" "response.completed"
sse_event_json "${patch_retrieve_stream_path}" "response.output_item.done" | jq -e '
  .item.type == "apply_patch_call" and
  (.item.operation.type | type == "string" and length > 0) and
  (.item.operation.path | type == "string" and length > 0)
' >/dev/null

echo "v3 coding tools smoke passed"
