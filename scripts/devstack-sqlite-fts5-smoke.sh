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
  RETRIEVAL_INDEX_BACKEND=sqlite_fts5 make devstack-up
  SHIM_BASE_URL=http://127.0.0.1:18080 make devstack-sqlite-fts5-smoke

This smoke path checks:
  1. shim /readyz
  2. /debug/capabilities reports sqlite storage and sqlite_fts5 retrieval index
  3. /v1/files upload
  4. /v1/vector_stores create with files
  5. /v1/vector_stores/{id}/search returns the FTS5-indexed fixture
  6. local /v1/responses file_search returns the same fixture answer
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

  for _ in $(seq 1 60); do
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

echo "==> checking sqlite_fts5 capability manifest"
capabilities_json="$(curl -fsS "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq '{
  ready,
  retrieval: .runtime.retrieval
}'
printf '%s\n' "${capabilities_json}" | jq -e '
  .ready == true and
  .runtime.retrieval.storage_backend == "sqlite" and
  .runtime.retrieval.index_backend == "sqlite_fts5" and
  .runtime.retrieval.embedder_backend == "disabled" and
  .runtime.retrieval.semantic_search == false and
  .runtime.retrieval.hybrid_search == false and
  .runtime.retrieval.local_rerank == false and
  .runtime.retrieval.lazy_repair == true
' >/dev/null

echo "==> uploading FTS5 retrieval fixtures"
needle_path="${tmp_dir}/fts5-needle.txt"
decoy_path="${tmp_dir}/fts5-decoy.txt"
printf '%s\n' \
  'FTS5 devstack smoke target. Remember: code=777. Reply OK. Lookup keyword orionpepper.' \
  > "${needle_path}"
printf '%s\n' \
  'Unrelated retrieval decoy. This text is about ordinary notes.' \
  > "${decoy_path}"

upload_file "${needle_path}"
needle_file_id="${file_ids[0]}"
upload_file "${decoy_path}"
decoy_file_id="${file_ids[1]}"

echo "==> creating vector store with FTS5 fixtures"
create_vector_store_json="$(curl -fsS "${shim_base_url}/v1/vector_stores" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg needle_file_id "${needle_file_id}" --arg decoy_file_id "${decoy_file_id}" '{
    name: "devstack-sqlite-fts5-smoke",
    file_ids: [$needle_file_id, $decoy_file_id]
  }')")"
vector_store_id="$(printf '%s' "${create_vector_store_json}" | jq -r '.id')"
if [[ -z "${vector_store_id}" || "${vector_store_id}" == "null" ]]; then
  echo "failed to parse vector store id" >&2
  printf '%s\n' "${create_vector_store_json}" >&2
  exit 1
fi
printf '%s\n' "${create_vector_store_json}" | jq '{id, status, file_counts}'

echo "==> searching vector store through sqlite_fts5"
search_json="$(curl -fsS "${shim_base_url}/v1/vector_stores/${vector_store_id}/search" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc '{query: "orionpepper", max_num_results: 3}')")"
printf '%s\n' "${search_json}" | jq '{
  object,
  top_result: .data[0] | {file_id, filename, score, content}
}'
printf '%s\n' "${search_json}" | jq -e --arg needle_file_id "${needle_file_id}" '
  (.data | length) >= 1 and
  .data[0].file_id == $needle_file_id and
  ([.data[0].content[]?.text] | join("\n") | contains("777"))
' >/dev/null

echo "==> checking local file_search flow through sqlite_fts5"
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
printf '%s\n' "${file_search_json}" | jq '{id, status, output_text, file_search_calls: [.output[] | select(.type == "file_search_call") | {status, queries}]}'
printf '%s\n' "${file_search_json}" | jq -e '
  .status == "completed" and
  .output_text == "777" and
  ([.output[] | select(.type == "file_search_call")] | length) >= 1
' >/dev/null

echo "devstack sqlite_fts5 retrieval smoke passed"
