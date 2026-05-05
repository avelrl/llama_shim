#!/usr/bin/env bash
set -euo pipefail

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
model="${MODEL:-devstack-model}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd curl
require_cmd jq
require_cmd mktemp

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

echo "==> checking image_generation fixture capabilities"
curl -fsS "${shim_base_url}/debug/capabilities" \
  | jq -e '
    .tools.image_generation.enabled == true and
    .tools.image_generation.backend == "fixture" and
    .probes.image_generation_backend.ready == true
  ' >/dev/null

echo "==> checking image_generation fixture non-stream create"
create_json="$(curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg model "${model}" '{
    model: $model,
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
create_response_id="$(printf '%s' "${create_json}" | jq -r '.id')"
printf '%s\n' "${create_json}" | jq -e '
  .status == "completed" and
  .output[0].type == "image_generation_call" and
  .output[0].status == "completed" and
  .output[0].result == "ZmFrZS1pbWFnZQ==" and
  .output[0].revised_prompt == "Generate a tiny orange cat in a teacup."
' >/dev/null

echo "==> checking image_generation fixture stream create"
stream_sse="${tmp_dir}/image_generation.sse"
curl -fsS "${shim_base_url}/v1/responses" \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -d "$(jq -nc --arg model "${model}" '{
    model: $model,
    store: true,
    stream: true,
    input: "Generate a fixture stream image.",
    tools: [
      {
        type: "image_generation",
        partial_images: 2,
        output_format: "png",
        quality: "low",
        size: "1024x1024"
      }
    ],
    tool_choice: {
      type: "image_generation"
    }
  }')" \
  > "${stream_sse}"

grep -q 'event: response.image_generation_call.partial_image' "${stream_sse}"
grep -q '"partial_image_b64":"cGFydGlhbC0w"' "${stream_sse}"
grep -q '"partial_image_b64":"cGFydGlhbC0x"' "${stream_sse}"
grep -q 'event: response.completed' "${stream_sse}"
stream_response_id="$(
  awk '/^data: /{sub(/^data: /, ""); if ($0 != "[DONE]") print}' "${stream_sse}" \
    | jq -r 'select(.type == "response.completed") | .response.id' \
    | tail -1
)"
if [[ -z "${stream_response_id}" || "${stream_response_id}" == "null" ]]; then
  echo "could not extract streamed response id" >&2
  exit 1
fi

echo "==> checking image_generation fixture retrieve-stream replay"
replay_sse="${tmp_dir}/image_generation_replay.sse"
curl -fsS "${shim_base_url}/v1/responses/${stream_response_id}?stream=true" \
  -H 'Accept: text/event-stream' \
  > "${replay_sse}"
grep -q 'event: response.image_generation_call.partial_image' "${replay_sse}"
grep -q '"partial_image_b64":"cGFydGlhbC0w"' "${replay_sse}"
grep -q '"partial_image_b64":"cGFydGlhbC0x"' "${replay_sse}"
grep -q 'event: response.completed' "${replay_sse}"

echo "==> cleaning image_generation fixture smoke responses"
curl -fsS -X DELETE "${shim_base_url}/v1/responses/${create_response_id}" >/dev/null || true
curl -fsS -X DELETE "${shim_base_url}/v1/responses/${stream_response_id}" >/dev/null || true

echo "v3 image backends smoke passed"
