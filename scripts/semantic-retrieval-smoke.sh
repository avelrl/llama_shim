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
  SHIM_BASE_URL=http://127.0.0.1:8083 \
  EMBEDDER_BASE_URL=http://127.0.0.1:8080 \
  ./scripts/semantic-retrieval-smoke.sh

Environment:
  SHIM_BASE_URL       Shim base URL. Default: http://127.0.0.1:8083
  EMBEDDER_BASE_URL   EmbedAnything base URL. Default: http://127.0.0.1:8080
  SEARCH_QUERY        Search query. Default: banana smoothie
  FILE_TEXT           Uploaded fixture text.

This smoke test checks:
  1. EmbedAnything /health_check
  2. Shim /readyz
  3. /v1/files upload
  4. /v1/vector_stores create
  5. /v1/vector_stores/{id}/search semantic result
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd jq
require_cmd mktemp

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8083}"
embedder_base_url="${EMBEDDER_BASE_URL:-http://127.0.0.1:8080}"
search_query="${SEARCH_QUERY:-banana smoothie}"
file_text="${FILE_TEXT:-Banana smoothie recipe and ripe banana notes.}"

tmp_dir="$(mktemp -d)"
file_id=""
vector_store_id=""

cleanup() {
  if [[ -n "${vector_store_id}" ]]; then
    curl -fsS -X DELETE "${shim_base_url}/v1/vector_stores/${vector_store_id}" >/dev/null || true
  fi
  if [[ -n "${file_id}" ]]; then
    curl -fsS -X DELETE "${shim_base_url}/v1/files/${file_id}" >/dev/null || true
  fi
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

fixture_path="${tmp_dir}/semantic-smoke.txt"
printf '%s\n' "${file_text}" > "${fixture_path}"

echo "==> checking EmbedAnything health: ${embedder_base_url}/health_check"
curl -fsS "${embedder_base_url}/health_check" >/dev/null

echo "==> checking shim readiness: ${shim_base_url}/readyz"
curl -fsS "${shim_base_url}/readyz" | jq .

echo "==> uploading local file"
upload_json="$(curl -fsS "${shim_base_url}/v1/files" \
  -F "purpose=assistants" \
  -F "file=@${fixture_path};type=text/plain")"
file_id="$(printf '%s' "${upload_json}" | jq -r '.id')"
if [[ -z "${file_id}" || "${file_id}" == "null" ]]; then
  echo "failed to parse uploaded file id" >&2
  printf '%s\n' "${upload_json}" >&2
  exit 1
fi
printf '%s\n' "${upload_json}" | jq '{id, object, filename, bytes, status}'

echo "==> creating vector store"
create_json="$(curl -fsS "${shim_base_url}/v1/vector_stores" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg file_id "${file_id}" '{name:"semantic-smoke", file_ids:[$file_id]}')")"
vector_store_id="$(printf '%s' "${create_json}" | jq -r '.id')"
if [[ -z "${vector_store_id}" || "${vector_store_id}" == "null" ]]; then
  echo "failed to parse vector store id" >&2
  printf '%s\n' "${create_json}" >&2
  exit 1
fi
printf '%s\n' "${create_json}" | jq '{id, object, status, file_counts}'

echo "==> searching vector store"
search_json="$(curl -fsS "${shim_base_url}/v1/vector_stores/${vector_store_id}/search" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg query "${search_query}" '{query:$query,max_num_results:3}')")"

result_count="$(printf '%s' "${search_json}" | jq '.data | length')"
if [[ "${result_count}" -lt 1 ]]; then
  echo "semantic search returned no results" >&2
  printf '%s\n' "${search_json}" >&2
  exit 1
fi

top_file_id="$(printf '%s' "${search_json}" | jq -r '.data[0].file_id')"
if [[ "${top_file_id}" != "${file_id}" ]]; then
  echo "unexpected top file: expected ${file_id}, got ${top_file_id}" >&2
  printf '%s\n' "${search_json}" >&2
  exit 1
fi

printf '%s\n' "${search_json}" | jq '{
  object,
  search_query,
  top_result: .data[0] | {file_id, filename, score, content}
}'

echo "semantic retrieval smoke passed"
