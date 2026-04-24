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
  FIXTURE_BASE_URL=http://127.0.0.1:18081 \
  FIXTURE_INTERNAL_MCP_URL=http://fixture:8081/mcp \
  ./scripts/devstack-smoke.sh

This smoke path checks:
  1. deterministic fixture health
  2. shim /readyz
  3. shim /debug/capabilities
  4. stateful /v1/responses via previous_response_id
  5. local /v1/responses file_search
  6. local /v1/responses web_search
  7. local /v1/responses image_generation
  8. local /v1/responses remote MCP via server_url
  9. local /v1/responses hosted/server tool_search with namespace follow-up
  10. local /v1/responses stream replay for MCP
  11. local /v1/responses generic stream replay for tool_search
  12. /debug/capabilities advertises Responses WebSocket local subset support
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd bash
require_cmd curl
require_cmd jq
require_cmd mktemp

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
fixture_base_url="${FIXTURE_BASE_URL:-http://127.0.0.1:18081}"
fixture_internal_mcp_url="${FIXTURE_INTERNAL_MCP_URL:-http://fixture:8081/mcp}"

tmp_dir="$(mktemp -d)"
file_id=""
vector_store_id=""
response_ids=()

cleanup() {
  for response_id in "${response_ids[@]:-}"; do
    if [[ -n "${response_id}" ]]; then
      curl -fsS -X DELETE "${shim_base_url}/v1/responses/${response_id}" >/dev/null || true
    fi
  done
  if [[ -n "${vector_store_id}" ]]; then
    curl -fsS -X DELETE "${shim_base_url}/v1/vector_stores/${vector_store_id}" >/dev/null || true
  fi
  if [[ -n "${file_id}" ]]; then
    curl -fsS -X DELETE "${shim_base_url}/v1/files/${file_id}" >/dev/null || true
  fi
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

wait_http_ok() {
  local label="$1"
  local url="$2"

  for _ in $(seq 1 60); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "${label} did not become ready: ${url}" >&2
  exit 1
}

echo "==> waiting for deterministic fixture: ${fixture_base_url}/healthz"
wait_http_ok "fixture" "${fixture_base_url}/healthz"

echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
wait_http_ok "shim" "${shim_base_url}/readyz"

echo "==> checking shim capability manifest"
capabilities_json="$(curl -fsS "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq '{
  object,
  ready,
  responses_mode: .runtime.responses_mode,
  responses_websocket: .surfaces.responses.websocket,
  tools: {
    web_search: .tools.web_search.enabled,
    image_generation: .tools.image_generation.enabled,
    shell: .tools.shell.enabled,
    apply_patch: .tools.apply_patch.enabled,
    mcp_server_url: .tools.mcp_server_url.enabled,
    tool_search_hosted: .tools.tool_search_hosted.enabled
  },
  probes: {
    web_search_backend: .probes.web_search_backend.ready,
    image_generation_backend: .probes.image_generation_backend.ready
  }
}'
printf '%s\n' "${capabilities_json}" | jq -e '
  .object == "shim.capabilities" and
  .ready == true and
  .runtime.responses_mode == "prefer_local" and
  .surfaces.responses.websocket.enabled == true and
  .surfaces.responses.websocket.support == "local_subset" and
  .surfaces.responses.websocket.endpoint == "/v1/responses" and
  .surfaces.responses.websocket.sequential == true and
  .surfaces.responses.websocket.multiplexing == false and
  .tools.web_search.enabled == true and
  .tools.image_generation.enabled == true and
  .tools.shell.enabled == true and
  .tools.apply_patch.enabled == true and
  .tools.shell.support == "native_local_subset" and
  .tools.apply_patch.support == "native_local_subset" and
  .tools.mcp_server_url.enabled == true and
  .tools.tool_search_hosted.enabled == true and
  .probes.web_search_backend.ready == true and
  .probes.image_generation_backend.ready == true
' >/dev/null

echo "==> checking stateful previous_response_id flow"
stateful_first_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    store: true,
    input: "Remember code 777. Reply READY."
  }')")"
first_response_id="$(printf '%s' "${stateful_first_json}" | jq -r '.id')"
response_ids+=("${first_response_id}")
printf '%s\n' "${stateful_first_json}" | jq '{id, status, output_text}'
printf '%s\n' "${stateful_first_json}" | jq -e '.status == "completed" and .output_text == "READY"' >/dev/null

stateful_second_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg previous_response_id "${first_response_id}" '{
    model: "devstack-model",
    store: true,
    previous_response_id: $previous_response_id,
    input: "What code did I ask you to remember? Reply digits only."
  }')")"
second_response_id="$(printf '%s' "${stateful_second_json}" | jq -r '.id')"
response_ids+=("${second_response_id}")
printf '%s\n' "${stateful_second_json}" | jq '{id, status, output_text}'
printf '%s\n' "${stateful_second_json}" | jq -e '.status == "completed" and .output_text == "777"' >/dev/null

echo "==> seeding retrieval fixture"
fixture_path="${tmp_dir}/codes.txt"
printf '%s\n' 'Remember: code=777. Reply OK.' > "${fixture_path}"

upload_json="$(curl -fsS "${shim_base_url}/v1/files" \
  -F "purpose=assistants" \
  -F "file=@${fixture_path};type=text/plain")"
file_id="$(printf '%s' "${upload_json}" | jq -r '.id')"
printf '%s\n' "${upload_json}" | jq '{id, filename, bytes, status}'

create_vector_store_json="$(curl -fsS "${shim_base_url}/v1/vector_stores" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg file_id "${file_id}" '{
    name: "devstack-smoke",
    file_ids: [$file_id]
  }')")"
vector_store_id="$(printf '%s' "${create_vector_store_json}" | jq -r '.id')"
printf '%s\n' "${create_vector_store_json}" | jq '{id, status, file_counts}'

echo "==> checking local file_search flow"
file_search_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg vector_store_id "${vector_store_id}" '{
    model: "devstack-model",
    store: true,
    input: "What is the code?",
    tools: [
      {
        type: "file_search",
        vector_store_ids: [$vector_store_id]
      }
    ],
    tool_choice: "required"
  }')")"
file_search_response_id="$(printf '%s' "${file_search_json}" | jq -r '.id')"
response_ids+=("${file_search_response_id}")
printf '%s\n' "${file_search_json}" | jq '{id, status, output_text, first_output_type: .output[0].type}'
printf '%s\n' "${file_search_json}" | jq -e '
  .status == "completed" and
  .output_text == "777" and
  .output[0].type == "file_search_call"
' >/dev/null

echo "==> checking local web_search flow"
web_search_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    store: true,
    input: "Open the fixture guide page and find \"SUPPORTED FIXTURE PHRASE\" in that page. Reply with the exact phrase only.",
    include: ["web_search_call.action.sources"],
    tools: [
      {
        type: "web_search",
        search_context_size: "medium"
      }
    ],
    tool_choice: "required"
  }')")"
web_search_response_id="$(printf '%s' "${web_search_json}" | jq -r '.id')"
response_ids+=("${web_search_response_id}")
printf '%s\n' "${web_search_json}" | jq '{
  id,
  status,
  output_text,
  web_search_items: [.output[] | select(.type == "web_search_call") | .action.type],
  first_source_url: .output[0].action.sources[0].url
}'
printf '%s\n' "${web_search_json}" | jq -e '
  .status == "completed" and
  (.output_text | startswith("SUPPORTED FIXTURE PHRASE")) and
  ([.output[] | select(.type == "web_search_call" and .action.type == "search")] | length) >= 1 and
  (.output[0].action.sources[0].url | contains("/pages/web-search-guide"))
' >/dev/null

echo "==> checking local image_generation flow"
image_generation_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    store: true,
    input: "Generate a tiny orange cat in a teacup.",
    tools: [
      {
        type: "image_generation",
        output_format: "png",
        quality: "low",
        size: "1024x1024"
      }
    ],
    tool_choice: {
      type: "image_generation"
    }
  }')")"
image_generation_response_id="$(printf '%s' "${image_generation_json}" | jq -r '.id')"
response_ids+=("${image_generation_response_id}")
printf '%s\n' "${image_generation_json}" | jq '{
  id,
  status,
  first_output_type: .output[0].type,
  revised_prompt: .output[0].revised_prompt
}'
printf '%s\n' "${image_generation_json}" | jq -e '
  .status == "completed" and
  .output[0].type == "image_generation_call" and
  .output[0].status == "completed" and
  (.output[0].revised_prompt | type == "string" and length > 0) and
  (.output[0].result | type == "string" and length > 0)
' >/dev/null

echo "==> checking local remote MCP flow"
mcp_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg fixture_mcp_url "${fixture_internal_mcp_url}" '{
    model: "devstack-model",
    store: true,
    input: "Roll 2d4+1 and return only the numeric result.",
    tools: [
      {
        type: "mcp",
        server_label: "dmcp",
        server_url: $fixture_mcp_url,
        require_approval: "never"
      }
    ],
    tool_choice: "required"
  }')")"
mcp_response_id="$(printf '%s' "${mcp_json}" | jq -r '.id')"
response_ids+=("${mcp_response_id}")
printf '%s\n' "${mcp_json}" | jq '{
  id,
  status,
  output_text,
  output_types: [.output[].type],
  tool_name: .output[1].name
}'
printf '%s\n' "${mcp_json}" | jq -e '
  .status == "completed" and
  .output_text == "4" and
  .output[0].type == "mcp_list_tools" and
  .output[1].type == "mcp_call" and
  .output[1].name == "roll" and
  .output[1].output == "4"
' >/dev/null

echo "==> checking cached MCP follow-up flow"
mcp_follow_up_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg previous_response_id "${mcp_response_id}" '{
    model: "devstack-model",
    store: true,
    previous_response_id: $previous_response_id,
    input: "Roll again and return only the numeric result."
  }')")"
mcp_follow_up_response_id="$(printf '%s' "${mcp_follow_up_json}" | jq -r '.id')"
response_ids+=("${mcp_follow_up_response_id}")
printf '%s\n' "${mcp_follow_up_json}" | jq '{
  id,
  status,
  output_text,
  first_output_type: .output[0].type
}'
printf '%s\n' "${mcp_follow_up_json}" | jq -e '
  .status == "completed" and
  .output_text == "4" and
  .output[0].type == "mcp_call" and
  .output[0].name == "roll"
' >/dev/null

echo "==> checking streamed MCP replay"
mcp_stream_path="${tmp_dir}/mcp-stream.sse"
curl -fsS -N "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg fixture_mcp_url "${fixture_internal_mcp_url}" '{
    model: "devstack-model",
    stream: true,
    store: true,
    input: "Roll 2d4+1 and return only the numeric result.",
    tools: [
      {
        type: "mcp",
        server_label: "dmcp",
        server_url: $fixture_mcp_url,
        require_approval: "never"
      }
    ],
    tool_choice: "required"
  }')" > "${mcp_stream_path}"
grep -q '^event: response.mcp_call_arguments.done$' "${mcp_stream_path}"
grep -q '^event: response.mcp_call.in_progress$' "${mcp_stream_path}"
grep -q '^event: response.completed$' "${mcp_stream_path}"
mcp_completed_json="$(awk '
  $0 == "event: response.completed" {
    getline
    if ($0 ~ /^data: /) {
      sub(/^data: /, "", $0)
      print
      exit
    }
  }
' "${mcp_stream_path}")"
printf '%s\n' "${mcp_completed_json}" | jq '{
  type,
  output_text: .response.output_text,
  output_types: [.response.output[].type]
}'
printf '%s\n' "${mcp_completed_json}" | jq -e '
  .type == "response.completed" and
  .response.output_text == "4" and
  .response.output[0].type == "mcp_list_tools" and
  .response.output[1].type == "mcp_call" and
  .response.output[2].type == "message"
' >/dev/null

echo "==> checking hosted/server tool_search namespace flow"
tool_search_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    store: true,
    input: "Find the shipping ETA namespace tool and use it for order_42.",
    tools: [
      {
        type: "tool_search",
        description: "Search deferred project tools."
      },
      {
        type: "namespace",
        name: "shipping_ops",
        description: "Tools for shipping ETA and tracking lookups.",
        tools: [
          {
            type: "function",
            name: "get_shipping_eta",
            description: "Look up shipping ETA details for an order.",
            defer_loading: true,
            parameters: {
              type: "object",
              properties: {
                order_id: {
                  type: "string"
                }
              },
              required: ["order_id"],
              additionalProperties: false
            }
          },
          {
            type: "function",
            name: "get_tracking_events",
            description: "List tracking events for an order.",
            defer_loading: true,
            parameters: {
              type: "object",
              properties: {
                order_id: {
                  type: "string"
                }
              },
              required: ["order_id"],
              additionalProperties: false
            }
          }
        ]
      }
    ],
    tool_choice: {
      type: "tool_search"
    }
  }')")"
tool_search_response_id="$(printf '%s' "${tool_search_json}" | jq -r '.id')"
tool_search_call_id="$(printf '%s' "${tool_search_json}" | jq -r '.output[2].call_id')"
response_ids+=("${tool_search_response_id}")
printf '%s\n' "${tool_search_json}" | jq '{
  id,
  status,
  output_types: [.output[].type],
  selected_namespace: .output[1].tools[0].name,
  planned_function: .output[2].name
}'
printf '%s\n' "${tool_search_json}" | jq -e '
  .status == "completed" and
  .output_text == "" and
  .output[0].type == "tool_search_call" and
  .output[0].execution == "server" and
  .output[0].call_id == null and
  .output[1].type == "tool_search_output" and
  .output[1].execution == "server" and
  .output[1].tools[0].type == "namespace" and
  .output[1].tools[0].name == "shipping_ops" and
  .output[1].tools[0].tools[0].name == "get_shipping_eta" and
  .output[2].type == "function_call" and
  .output[2].name == "get_shipping_eta" and
  .output[2].namespace == "shipping_ops" and
  .output[2].arguments == "{\"order_id\":\"order_42\"}"
' >/dev/null

echo "==> checking tool_search follow-up completion"
tool_search_follow_up_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg previous_response_id "${tool_search_response_id}" --arg call_id "${tool_search_call_id}" '{
    model: "devstack-model",
    store: true,
    previous_response_id: $previous_response_id,
    input: [
      {
        type: "function_call_output",
        call_id: $call_id,
        output: "ETA for order_42 is 2026-04-20."
      }
    ]
  }')")"
tool_search_follow_up_response_id="$(printf '%s' "${tool_search_follow_up_json}" | jq -r '.id')"
response_ids+=("${tool_search_follow_up_response_id}")
printf '%s\n' "${tool_search_follow_up_json}" | jq '{
  id,
  status,
  output_text,
  first_output_type: .output[0].type
}'
printf '%s\n' "${tool_search_follow_up_json}" | jq -e '
  .status == "completed" and
  .output_text == "ETA for order_42 is 2026-04-20." and
  .output[0].type == "message"
' >/dev/null

echo "==> checking streamed tool_search replay"
tool_search_stream_path="${tmp_dir}/tool-search-stream.sse"
curl -fsS -N "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    stream: true,
    store: true,
    input: "Find the shipping ETA namespace tool and use it for order_42.",
    tools: [
      {
        type: "tool_search",
        description: "Search deferred project tools."
      },
      {
        type: "namespace",
        name: "shipping_ops",
        description: "Tools for shipping ETA and tracking lookups.",
        tools: [
          {
            type: "function",
            name: "get_shipping_eta",
            description: "Look up shipping ETA details for an order.",
            defer_loading: true,
            parameters: {
              type: "object",
              properties: {
                order_id: {
                  type: "string"
                }
              },
              required: ["order_id"],
              additionalProperties: false
            }
          }
        ]
      }
    ],
    tool_choice: {
      type: "tool_search"
    }
  }')" > "${tool_search_stream_path}"
grep -q '^event: response.output_item.added$' "${tool_search_stream_path}"
grep -q '^event: response.output_item.done$' "${tool_search_stream_path}"
grep -q '^event: response.function_call_arguments.done$' "${tool_search_stream_path}"
grep -q '^event: response.completed$' "${tool_search_stream_path}"
if grep -q '^event: response.tool_search' "${tool_search_stream_path}"; then
  echo "unexpected exact tool_search SSE family in generic replay" >&2
  exit 1
fi
tool_search_completed_json="$(awk '
  $0 == "event: response.completed" {
    getline
    if ($0 ~ /^data: /) {
      sub(/^data: /, "", $0)
      print
      exit
    }
  }
' "${tool_search_stream_path}")"
printf '%s\n' "${tool_search_completed_json}" | jq '{
  type,
  output_text: .response.output_text,
  output_types: [.response.output[].type]
}'
printf '%s\n' "${tool_search_completed_json}" | jq -e '
  .type == "response.completed" and
  .response.output_text == "" and
  .response.output[0].type == "tool_search_call" and
  .response.output[1].type == "tool_search_output" and
  .response.output[2].type == "function_call"
' >/dev/null

echo "devstack smoke passed"
