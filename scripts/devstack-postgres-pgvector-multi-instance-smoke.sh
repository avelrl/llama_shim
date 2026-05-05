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
  make devstack-postgres-pgvector-multi-instance-up
  make devstack-postgres-pgvector-multi-instance-smoke

This smoke path checks the current Postgres/pgvector beta multi-instance
deployment boundary:
  1. primary and secondary shim instances become ready
  2. both advertise Postgres retrieval object storage and pgvector retrieval
  3. primary writes file/vector-store objects into Postgres
  4. secondary reads and searches those objects through the shared Postgres store
  5. secondary can run local Responses file_search over the primary-created store
  6. stateful Responses, conversations, and stored Chat Completions are shared
     across instances through Postgres

Code-interpreter runtime state remains SQLite sidecar owned in this profile.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd jq
require_cmd mktemp

primary_base_url="${SHIM_PRIMARY_BASE_URL:-http://127.0.0.1:18080}"
secondary_base_url="${SHIM_SECONDARY_BASE_URL:-http://127.0.0.1:18082}"
expected_responses_mode="${RESPONSES_MODE:-prefer_local}"

tmp_dir="$(mktemp -d)"
file_ids=()
vector_store_id=""
response_ids=()
chat_completion_id=""

cleanup() {
  for response_id in "${response_ids[@]:-}"; do
    if [[ -n "${response_id}" ]]; then
      curl -fsS -X DELETE "${secondary_base_url}/v1/responses/${response_id}" >/dev/null || true
    fi
  done
  if [[ -n "${vector_store_id}" ]]; then
    curl -fsS -X DELETE "${primary_base_url}/v1/vector_stores/${vector_store_id}" >/dev/null || true
  fi
  if [[ -n "${chat_completion_id}" ]]; then
    curl -fsS -X DELETE "${secondary_base_url}/v1/chat/completions/${chat_completion_id}" >/dev/null || true
  fi
  for file_id in "${file_ids[@]:-}"; do
    if [[ -n "${file_id}" ]]; then
      curl -fsS -X DELETE "${primary_base_url}/v1/files/${file_id}" >/dev/null || true
    fi
  done
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

wait_http_ok() {
  local label="$1"
  local url="$2"

  for _ in $(seq 1 90); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "${label} did not become ready: ${url}" >&2
  exit 1
}

assert_postgres_capabilities() {
  local label="$1"
  local base_url="$2"
  local capabilities_json

  capabilities_json="$(curl -fsS "${base_url}/debug/capabilities")"
  echo "==> ${label} capabilities"
  printf '%s\n' "${capabilities_json}" | jq '{
    ready,
    responses_mode: .runtime.responses_mode,
    persistence: .runtime.persistence,
    retrieval: .runtime.retrieval,
    probes: {
      storage: .probes.storage,
      postgres: .probes.postgres,
      retrieval_embedder: .probes.retrieval_embedder
    }
  }'
  printf '%s\n' "${capabilities_json}" | jq -e --arg expected_responses_mode "${expected_responses_mode}" '
    .ready == true and
    .runtime.responses_mode == $expected_responses_mode and
    .runtime.persistence.backend == "postgres" and
    .runtime.persistence.file_store == "postgres" and
    .runtime.persistence.vector_store == "postgres" and
    .runtime.persistence.response_store == "postgres" and
    .runtime.persistence.conversation_store == "postgres" and
    .runtime.persistence.chat_completion_store == "postgres" and
    .runtime.persistence.code_interpreter_store == "sqlite_sidecar" and
    .runtime.retrieval.storage_backend == "postgres" and
    .runtime.retrieval.index_backend == "pgvector" and
    .runtime.retrieval.embedder_backend == "openai_compatible" and
    .runtime.retrieval.semantic_search == true and
    .runtime.retrieval.hybrid_search == true and
    .probes.storage.ready == true and
    .probes.postgres.ready == true and
    .probes.retrieval_embedder.ready == true
  ' >/dev/null
}

upload_file_primary() {
  local path="$1"
  local upload_json
  local file_id

  upload_json="$(curl -fsS "${primary_base_url}/v1/files" \
    -F "purpose=assistants" \
    -F "file=@${path};type=text/plain")"
  file_id="$(printf '%s' "${upload_json}" | jq -r '.id')"
  if [[ -z "${file_id}" || "${file_id}" == "null" ]]; then
    echo "failed to parse uploaded file id for ${path}" >&2
    printf '%s\n' "${upload_json}" >&2
    exit 1
  fi
  file_ids+=("${file_id}")
  printf '%s\n' "${upload_json}" | jq '{id, filename, bytes, status}'
}

echo "==> waiting for primary shim readiness: ${primary_base_url}/readyz"
wait_http_ok "primary shim" "${primary_base_url}/readyz"
echo "==> waiting for secondary shim readiness: ${secondary_base_url}/readyz"
wait_http_ok "secondary shim" "${secondary_base_url}/readyz"

assert_postgres_capabilities "primary" "${primary_base_url}"
assert_postgres_capabilities "secondary" "${secondary_base_url}"

echo "==> uploading fixtures through primary"
needle_path="${tmp_dir}/shared-postgres-needle.txt"
decoy_path="${tmp_dir}/shared-postgres-decoy.txt"
printf '%s\n' \
  'Shared Postgres pgvector smoke target. Remember: code=777. Lookup keyword orionpepper.' \
  > "${needle_path}"
printf '%s\n' \
  'Shared Postgres pgvector decoy. This text is about ordinary notes.' \
  > "${decoy_path}"

upload_file_primary "${needle_path}"
needle_file_id="${file_ids[0]}"
upload_file_primary "${decoy_path}"
decoy_file_id="${file_ids[1]}"

echo "==> creating vector store through primary"
create_vector_store_json="$(curl -fsS "${primary_base_url}/v1/vector_stores" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg needle_file_id "${needle_file_id}" --arg decoy_file_id "${decoy_file_id}" '{
    name: "devstack-postgres-multi-instance-smoke",
    file_ids: [$needle_file_id, $decoy_file_id]
  }')")"
vector_store_id="$(printf '%s' "${create_vector_store_json}" | jq -r '.id')"
if [[ -z "${vector_store_id}" || "${vector_store_id}" == "null" ]]; then
  echo "failed to parse vector store id" >&2
  printf '%s\n' "${create_vector_store_json}" >&2
  exit 1
fi
printf '%s\n' "${create_vector_store_json}" | jq '{id, status, file_counts}'

echo "==> reading primary-created objects through secondary"
curl -fsS "${secondary_base_url}/v1/files/${needle_file_id}" | jq -e --arg needle_file_id "${needle_file_id}" '
  .id == $needle_file_id and .filename == "shared-postgres-needle.txt"
' >/dev/null
curl -fsS "${secondary_base_url}/v1/vector_stores/${vector_store_id}" | jq -e --arg vector_store_id "${vector_store_id}" '
  .id == $vector_store_id and .file_counts.completed == 2
' >/dev/null

echo "==> searching primary-created vector store through secondary"
search_json="$(curl -fsS "${secondary_base_url}/v1/vector_stores/${vector_store_id}/search" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{query: "What is the code?", max_num_results: 3}')")"
printf '%s\n' "${search_json}" | jq '{top_result: .data[0] | {file_id, filename, score, content}}'
printf '%s\n' "${search_json}" | jq -e --arg needle_file_id "${needle_file_id}" '
  (.data | length) >= 1 and
  .data[0].file_id == $needle_file_id and
  ([.data[0].content[]?.text] | join("\n") | contains("777"))
' >/dev/null

echo "==> running secondary local file_search over shared Postgres store"
file_search_json="$(curl -fsS "${secondary_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg vector_store_id "${vector_store_id}" '{
    model: "devstack-model",
    store: true,
    input: "What is the code?",
    tools: [
      {
        type: "file_search",
        vector_store_ids: [$vector_store_id],
        ranking_options: {
          hybrid_search: {
            embedding_weight: 1,
            text_weight: 1
          }
        }
      }
    ],
    tool_choice: "required"
  }')")"
file_search_response_id="$(printf '%s' "${file_search_json}" | jq -r '.id')"
response_ids+=("${file_search_response_id}")
printf '%s\n' "${file_search_json}" | jq '{id, status, output_text, file_search_calls: [.output[] | select(.type == "file_search_call") | {status, queries}]}'
printf '%s\n' "${file_search_json}" | jq -e '
  .status == "completed" and
  .output_text == "777" and
  ([.output[] | select(.type == "file_search_call")] | length) >= 1
' >/dev/null

echo "==> reading secondary-created response state through primary"
curl -fsS "${primary_base_url}/v1/responses/${file_search_response_id}" | jq -e --arg response_id "${file_search_response_id}" '
  .id == $response_id and .status == "completed" and .output_text == "777"
' >/dev/null
curl -fsS "${primary_base_url}/v1/responses/${file_search_response_id}/input_items?limit=10&order=asc" | jq -e '
  .object == "list" and (.data | length) >= 1
' >/dev/null

echo "==> sharing conversation state across primary and secondary"
conversation_json="$(curl -fsS "${primary_base_url}/v1/conversations" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    metadata: {smoke: "postgres-multi-instance"},
    items: [
      {
        type: "message",
        role: "user",
        content: [{type: "input_text", text: "shared conversation seed"}]
      }
    ]
  }')")"
conversation_id="$(printf '%s' "${conversation_json}" | jq -r '.id')"
if [[ -z "${conversation_id}" || "${conversation_id}" == "null" ]]; then
  echo "failed to parse conversation id" >&2
  printf '%s\n' "${conversation_json}" >&2
  exit 1
fi
printf '%s\n' "${conversation_json}" | jq '{id, metadata}'
curl -fsS "${secondary_base_url}/v1/conversations/${conversation_id}" | jq -e --arg conversation_id "${conversation_id}" '
  .id == $conversation_id and .metadata.smoke == "postgres-multi-instance"
' >/dev/null
curl -fsS "${secondary_base_url}/v1/conversations/${conversation_id}/items" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    item: {
      type: "message",
      role: "user",
      content: [{type: "input_text", text: "secondary append"}]
    }
  }')" | jq -e '(.data | length) == 1' >/dev/null
curl -fsS "${primary_base_url}/v1/conversations/${conversation_id}/items?limit=10&order=asc" | jq -e '
  .object == "list" and (.data | length) >= 2 and .data[1].role == "user"
' >/dev/null

echo "==> sharing stored chat completion state across primary and secondary"
chat_json="$(curl -fsS "${primary_base_url}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    model: "devstack-model",
    store: true,
    metadata: {smoke: "postgres-multi-instance"},
    messages: [
      {role: "user", content: "Say CHAT_POSTGRES_OK exactly."}
    ]
  }')")"
chat_completion_id="$(printf '%s' "${chat_json}" | jq -r '.id')"
if [[ -z "${chat_completion_id}" || "${chat_completion_id}" == "null" ]]; then
  echo "failed to parse chat completion id" >&2
  printf '%s\n' "${chat_json}" >&2
  exit 1
fi
printf '%s\n' "${chat_json}" | jq '{id, object, model, metadata}'
curl -fsS "${secondary_base_url}/v1/chat/completions/${chat_completion_id}" | jq -e --arg chat_completion_id "${chat_completion_id}" '
  .id == $chat_completion_id and .metadata.smoke == "postgres-multi-instance"
' >/dev/null
curl -fsS "${secondary_base_url}/v1/chat/completions/${chat_completion_id}/messages?limit=10&order=asc" | jq -e '
  .object == "list" and (.data | length) == 1 and .data[0].role == "user"
' >/dev/null

echo "devstack postgres pgvector multi-instance smoke passed"
