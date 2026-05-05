#!/usr/bin/env bash
set -euo pipefail

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
model="${MODEL:-devstack-model}"
expected_backend="${EXPECTED_IMAGE_BACKEND:-comfyui}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

extract_completed_response_id() {
  awk '/^data: /{sub(/^data: /, ""); if ($0 != "[DONE]") print}' "$1" \
    | jq -r 'select(.type == "response.completed") | .response.id' \
    | tail -1
}

require_cmd curl
require_cmd jq
require_cmd mktemp

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

echo "==> checking image_generation ${expected_backend} capabilities"
capabilities_json="$(curl -fsS "${shim_base_url}/debug/capabilities")"
backend="$(printf '%s\n' "${capabilities_json}" | jq -r '.tools.image_generation.backend')"
if [[ "${backend}" != "${expected_backend}" ]]; then
  echo "unexpected image_generation backend: got ${backend}, want ${expected_backend}" >&2
  exit 1
fi
printf '%s\n' "${capabilities_json}" \
  | jq -e '
    .tools.image_generation.enabled == true and
    .probes.image_generation_backend.ready == true
  ' >/dev/null

echo "==> checking image_generation ${backend} non-stream create"
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

echo "==> checking image_generation ${backend} stream create"
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

grep -q 'event: response.completed' "${stream_sse}"
stream_response_id="$(extract_completed_response_id "${stream_sse}")"
if [[ -z "${stream_response_id}" || "${stream_response_id}" == "null" ]]; then
  echo "could not extract streamed response id" >&2
  exit 1
fi

if [[ "${backend}" == "fixture" ]]; then
  grep -q 'event: response.image_generation_call.partial_image' "${stream_sse}"
  grep -q '"partial_image_b64":"cGFydGlhbC0w"' "${stream_sse}"
  grep -q '"partial_image_b64":"cGFydGlhbC0x"' "${stream_sse}"
else
  if grep -q 'event: response.image_generation_call.partial_image' "${stream_sse}"; then
    echo "backend ${backend} unexpectedly emitted fixture partial-image events" >&2
    exit 1
  fi
fi

echo "==> checking image_generation ${backend} retrieve-stream replay"
replay_sse="${tmp_dir}/image_generation_replay.sse"
curl -fsS "${shim_base_url}/v1/responses/${stream_response_id}?stream=true" \
  -H 'Accept: text/event-stream' \
  > "${replay_sse}"
grep -q 'event: response.completed' "${replay_sse}"
if [[ "${backend}" == "fixture" ]]; then
  grep -q 'event: response.image_generation_call.partial_image' "${replay_sse}"
  grep -q '"partial_image_b64":"cGFydGlhbC0w"' "${replay_sse}"
  grep -q '"partial_image_b64":"cGFydGlhbC0x"' "${replay_sse}"
fi

echo "==> cleaning image_generation ${backend} smoke responses"
curl -fsS -X DELETE "${shim_base_url}/v1/responses/${create_response_id}" >/dev/null || true
curl -fsS -X DELETE "${shim_base_url}/v1/responses/${stream_response_id}" >/dev/null || true

echo "v3 image backends smoke passed"
