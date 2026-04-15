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
  ./scripts/devstack-smoke.sh

This smoke path checks:
  1. deterministic fixture health
  2. shim /readyz
  3. shim /debug/capabilities
  4. stateful /v1/responses via previous_response_id
  5. local /v1/responses file_search
  6. local /v1/responses web_search
  7. local /v1/responses image_generation
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
  tools: {
    web_search: .tools.web_search.enabled,
    image_generation: .tools.image_generation.enabled
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
  .tools.web_search.enabled == true and
  .tools.image_generation.enabled == true and
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
  ([.output[] | select(.type == "web_search_call")] | length) >= 3 and
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

echo "devstack smoke passed"
