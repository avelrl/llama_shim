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
  4. stored Chat Completions create/list/get/messages local-first surface
  5. stateful /v1/responses via previous_response_id
  6. model-assisted /v1/responses/compact canonical next-window flow
  7. server-side context_management compaction flow
  8. local /v1/responses file_search
  9. local /v1/responses web_search
  10. local /v1/responses image_generation
  11. local /v1/responses remote MCP via server_url
  12. local /v1/responses hosted/server tool_search with namespace follow-up
  13. local /v1/responses stream replay for MCP
  14. local /v1/responses generic stream replay for tool_search
  15. /debug/capabilities advertises Responses WebSocket local subset support
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
chat_completion_id=""
response_ids=()

cleanup() {
  if [[ -n "${chat_completion_id}" ]]; then
    curl -fsS -X DELETE "${shim_base_url}/v1/chat/completions/${chat_completion_id}" >/dev/null || true
  fi
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
  compaction: .runtime.compaction,
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
  .runtime.compaction.enabled == true and
  .runtime.compaction.support == "local_subset" and
  .runtime.compaction.backend == "model_assisted_text" and
  .runtime.compaction.capability_class == "model_assisted_text" and
  .runtime.compaction.model_configured == true and
  .runtime.compaction.retained_items == 2 and
  .runtime.compaction.max_input_chars == 32000 and
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

echo "==> checking stored Chat Completions local-first surface"
chat_completion_json="$(curl -fsS "${shim_base_url}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    store: true,
    metadata: {
      topic: "devstack-chat"
    },
    messages: [
      {
        role: "developer",
        content: "Be terse."
      },
      {
        role: "user",
        content: "Say OK and nothing else."
      }
    ]
  }')")"
chat_completion_id="$(printf '%s' "${chat_completion_json}" | jq -r '.id')"
printf '%s\n' "${chat_completion_json}" | jq '{id, object, model, content: .choices[0].message.content}'
printf '%s\n' "${chat_completion_json}" | jq -e '
  .object == "chat.completion" and
  .model == "devstack-model" and
  .choices[0].message.content == "OK"
' >/dev/null

chat_completion_list_json="$(curl -fsS -G "${shim_base_url}/v1/chat/completions" \
  --data-urlencode "metadata[topic]=devstack-chat" \
  --data-urlencode "limit=1" \
  --data-urlencode "order=asc")"
printf '%s\n' "${chat_completion_list_json}" | jq '{object, first_id, last_id, has_more, ids: [.data[].id]}'
printf '%s\n' "${chat_completion_list_json}" | jq -e --arg chat_completion_id "${chat_completion_id}" '
  .object == "list" and
  .data[0].id == $chat_completion_id and
  .first_id == $chat_completion_id and
  .last_id == $chat_completion_id and
  .has_more == false
' >/dev/null

chat_completion_get_json="$(curl -fsS "${shim_base_url}/v1/chat/completions/${chat_completion_id}")"
printf '%s\n' "${chat_completion_get_json}" | jq '{id, object, model, metadata}'
printf '%s\n' "${chat_completion_get_json}" | jq -e --arg chat_completion_id "${chat_completion_id}" '
  .id == $chat_completion_id and
  .object == "chat.completion" and
  .model == "devstack-model" and
  .metadata.topic == "devstack-chat"
' >/dev/null

chat_completion_messages_json="$(curl -fsS -G "${shim_base_url}/v1/chat/completions/${chat_completion_id}/messages" \
  --data-urlencode "limit=1" \
  --data-urlencode "order=desc")"
printf '%s\n' "${chat_completion_messages_json}" | jq '{object, first_id, last_id, has_more, first_message: .data[0]}'
printf '%s\n' "${chat_completion_messages_json}" | jq -e --arg chat_completion_id "${chat_completion_id}" '
  .object == "list" and
  .data[0].id == ($chat_completion_id + "-1") and
  .data[0].role == "user" and
  .data[0].content == "Say OK and nothing else." and
  .has_more == true
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

echo "==> checking standalone compaction canonical window"
compaction_json="$(curl -fsS "${shim_base_url}/v1/responses/compact" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    input: [
      {
        type: "message",
        role: "user",
        content: "Remember launch code 777 for compaction smoke."
      },
      {
        type: "message",
        role: "assistant",
        content: [
          {
            type: "output_text",
            text: "I will remember launch code 777."
          }
        ]
      },
      {
        type: "message",
        role: "user",
        content: "Keep deployment mode local."
      },
      {
        type: "message",
        role: "assistant",
        content: [
          {
            type: "output_text",
            text: "Acknowledged local mode."
          }
        ]
      }
    ]
  }')")"
compaction_output_path="${tmp_dir}/compaction-output.json"
printf '%s\n' "${compaction_json}" | jq '.output' > "${compaction_output_path}"
printf '%s\n' "${compaction_json}" | jq '{id, object, output_types: [.output[].type], output_count: (.output | length)}'
printf '%s\n' "${compaction_json}" | jq -e '
  .object == "response.compaction" and
  (.output | length) == 3 and
  .output[0].type == "message" and
  .output[1].type == "message" and
  .output[2].type == "compaction" and
  (.output[2].encrypted_content | startswith("llama_shim.compaction.v1:"))
' >/dev/null

compaction_next_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --slurpfile compacted "${compaction_output_path}" '{
    model: "devstack-model",
    store: true,
    input: ($compacted[0] + [
      {
        type: "message",
        role: "user",
        content: "What code did I ask you to remember? Reply digits only."
      }
    ])
  }')")"
compaction_next_response_id="$(printf '%s' "${compaction_next_json}" | jq -r '.id')"
response_ids+=("${compaction_next_response_id}")
printf '%s\n' "${compaction_next_json}" | jq '{id, status, output_text}'
printf '%s\n' "${compaction_next_json}" | jq -e '.status == "completed" and .output_text == "777"' >/dev/null

echo "==> checking server-side context_management compaction"
server_compaction_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg previous_response_id "${second_response_id}" '{
    model: "devstack-model",
    store: true,
    previous_response_id: $previous_response_id,
    context_management: [
      {
        type: "compaction",
        compact_threshold: 1
      }
    ],
    input: "What code did I ask you to remember? Reply digits only."
  }')")"
server_compaction_response_id="$(printf '%s' "${server_compaction_json}" | jq -r '.id')"
response_ids+=("${server_compaction_response_id}")
printf '%s\n' "${server_compaction_json}" | jq '{id, status, output_text, first_output_type: .output[0].type}'
printf '%s\n' "${server_compaction_json}" | jq -e '
  .status == "completed" and
  .output_text == "777" and
  .output[0].type == "compaction" and
  .output[1].type == "message"
' >/dev/null

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
