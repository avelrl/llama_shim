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
  STORAGE_BACKEND=postgres \
  RETRIEVAL_INDEX_BACKEND=pgvector \
  RETRIEVAL_EMBEDDER_BACKEND=openai_compatible \
  RETRIEVAL_EMBEDDER_BASE_URL=http://fixture:8081 \
  RETRIEVAL_EMBEDDER_MODEL=devstack-embedding \
  make devstack-up

  SHIM_BASE_URL=http://127.0.0.1:18080 \
  RESPONSES_MODE=prefer_local \
  make devstack-postgres-pgvector-smoke

This smoke path checks:
  1. shim /readyz
  2. /debug/capabilities reports Postgres object storage and pgvector retrieval
  3. /v1/files upload through the Postgres-backed object store
  4. /v1/vector_stores create and attach files
  5. /v1/vector_stores/{id}/search returns pgvector-ranked results
  6. local /v1/responses file_search uses the same pgvector-backed store
  7. vector-store and file deletes complete
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd jq
require_cmd mktemp

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
expected_responses_mode="${RESPONSES_MODE:-prefer_local}"

tmp_dir="$(mktemp -d)"
file_ids=()
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
  for file_id in "${file_ids[@]:-}"; do
    if [[ -n "${file_id}" ]]; then
      curl -fsS -X DELETE "${shim_base_url}/v1/files/${file_id}" >/dev/null || true
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

upload_file() {
  local path="$1"
  local upload_json
  local file_id

  upload_json="$(curl -fsS "${shim_base_url}/v1/files" \
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

echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
wait_http_ok "shim" "${shim_base_url}/readyz"

echo "==> checking postgres/pgvector capability manifest"
capabilities_json="$(curl -fsS "${shim_base_url}/debug/capabilities")"
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
  .runtime.retrieval.local_rerank == false and
  .runtime.retrieval.lazy_repair == false and
  .probes.storage.ready == true and
  .probes.postgres.ready == true and
  .probes.retrieval_embedder.ready == true
' >/dev/null

echo "==> uploading pgvector retrieval fixtures"
needle_path="${tmp_dir}/pgvector-needle.txt"
decoy_path="${tmp_dir}/pgvector-decoy.txt"
printf '%s\n' \
  'Postgres pgvector devstack smoke target. Remember: code=777. Lookup keyword orionpepper.' \
  > "${needle_path}"
printf '%s\n' \
  'Unrelated retrieval decoy. This text is about ordinary notes and generic reminders.' \
  > "${decoy_path}"

upload_file "${needle_path}"
needle_file_id="${file_ids[0]}"
upload_file "${decoy_path}"
decoy_file_id="${file_ids[1]}"

echo "==> creating vector store with pgvector fixtures"
create_vector_store_json="$(curl -fsS "${shim_base_url}/v1/vector_stores" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg needle_file_id "${needle_file_id}" --arg decoy_file_id "${decoy_file_id}" '{
    name: "devstack-postgres-pgvector-smoke",
    file_ids: [$needle_file_id, $decoy_file_id]
  }')")"
vector_store_id="$(printf '%s' "${create_vector_store_json}" | jq -r '.id')"
if [[ -z "${vector_store_id}" || "${vector_store_id}" == "null" ]]; then
  echo "failed to parse vector store id" >&2
  printf '%s\n' "${create_vector_store_json}" >&2
  exit 1
fi
printf '%s\n' "${create_vector_store_json}" | jq '{id, status, file_counts}'

echo "==> searching vector store through pgvector"
search_json="$(curl -fsS "${shim_base_url}/v1/vector_stores/${vector_store_id}/search" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{query: "What is the code?", max_num_results: 3}')")"
printf '%s\n' "${search_json}" | jq '{
  object,
  search_query,
  top_result: .data[0] | {file_id, filename, score, content}
}'
printf '%s\n' "${search_json}" | jq -e --arg needle_file_id "${needle_file_id}" '
  (.data | length) >= 1 and
  .data[0].file_id == $needle_file_id and
  ([.data[0].content[]?.text] | join("\n") | contains("777"))
' >/dev/null

echo "==> checking hybrid pgvector/text ranking options"
hybrid_json="$(curl -fsS "${shim_base_url}/v1/vector_stores/${vector_store_id}/search" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{
    query: "orionpepper",
    max_num_results: 3,
    ranking_options: {
      hybrid_search: {
        embedding_weight: 1,
        text_weight: 1
      }
    }
  }')")"
printf '%s\n' "${hybrid_json}" | jq '{top_result: .data[0] | {file_id, filename, score}}'
printf '%s\n' "${hybrid_json}" | jq -e --arg needle_file_id "${needle_file_id}" '
  (.data | length) >= 1 and .data[0].file_id == $needle_file_id
' >/dev/null

echo "==> checking local file_search flow through pgvector"
file_search_json="$(curl -fsS "${shim_base_url}/v1/responses" \
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

echo "devstack postgres pgvector retrieval smoke passed"
