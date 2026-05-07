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
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  UPSTREAM_PROVIDER_ROUTING_MODEL=deepseek/deepseek-v4-pro \
  GW_API_KEY=shim-dev-key \
  ./scripts/upstream-provider-routing-smoke.sh

Optional:
  MODEL=provider/model
  CODEX_MODEL=provider/model
  TESTER_MODEL=provider/model
  CODEX_EVAL_MODELS=provider/model
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'
  UPSTREAM_PROVIDER_ROUTING_ARTIFACT_DIR=.tmp/upstream-provider-routing-smoke
  UPSTREAM_PROVIDER_ROUTING_RUN_ID=manual
  UPSTREAM_PROVIDER_ROUTING_REQUIRE_READYZ=1
  UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED=0
  UPSTREAM_PROVIDER_ROUTING_TIMEOUT_SECONDS=180
  UPSTREAM_PROVIDER_ROUTING_SHIM_LOG=.data/shim.log

The smoke validates the configured public provider/model alias through:
  - /healthz and /readyz capture
  - /debug/capabilities provider-routing manifest
  - GET /v1/models live provider catalog
  - POST /v1/responses
  - POST /v1/chat/completions
  - POST /v1/responses/input_tokens
  - POST /v1/responses/compact
  - unknown provider/model fail-closed behavior
  - model-less derived request boundary

Derived endpoint failures are warnings by default because many OpenAI-compatible
providers do not implement /responses/input_tokens or /responses/compact. Set
UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED=1 to make them strict.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd date
require_cmd jq
require_cmd mkdir
require_cmd tail
require_cmd tr
require_cmd wc

slugify() {
  local value="$1"
  value="$(printf '%s' "${value}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9._-' '-')"
  value="${value#-}"
  value="${value%-}"
  if [[ -z "${value}" ]]; then
    value="value"
  fi
  printf '%s' "${value}"
}

csv_to_words() {
  local value="$1"
  value="${value//,/ }"
  printf '%s' "${value}"
}

bool_enabled() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    1 | true | yes | on) return 0 ;;
    *) return 1 ;;
  esac
}

is_2xx() {
  [[ "$1" == 2* ]]
}

shim_log_size() {
  local log_path="$1"
  if [[ ! -f "${log_path}" ]]; then
    printf '0'
    return
  fi
  wc -c <"${log_path}" | tr -d '[:space:]'
}

capture_shim_log() {
  local log_path="$1"
  local start_bytes="$2"
  local out_dir="$3"
  local slice="${out_dir}/shim.log.slice"
  local diagnostics="${out_dir}/shim-log-diagnostics.md"
  local matches="${out_dir}/shim-log-diagnostics.matches"
  local end_bytes

  if [[ ! -f "${log_path}" ]]; then
    {
      echo "# Shim Log Diagnostics"
      echo
      echo "Shim log was not found at \`${log_path}\`."
    } >"${diagnostics}"
    : >"${slice}"
    return
  fi

  end_bytes="$(shim_log_size "${log_path}")"
  if [[ "${end_bytes}" =~ ^[0-9]+$ && "${start_bytes}" =~ ^[0-9]+$ && "${end_bytes}" -ge "${start_bytes}" ]]; then
    if [[ "${start_bytes}" -eq 0 ]]; then
      cp "${log_path}" "${slice}"
    else
      tail -c +"$((start_bytes + 1))" "${log_path}" >"${slice}"
    fi
  else
    cp "${log_path}" "${slice}"
  fi

  {
    echo "# Shim Log Diagnostics"
    echo
    echo "- Source: \`${log_path}\`"
    echo "- Slice: \`${slice}\`"
    echo "- Start bytes: \`${start_bytes}\`"
    echo "- End bytes: \`${end_bytes}\`"
    echo
  } >"${diagnostics}"

  if grep -nE '"level":"(WARN|ERROR)"|"status":5[0-9][0-9]|upstream provider|provider/model|model catalog|upstream request failed|upstream request timed out|panic' "${slice}" >"${matches}"; then
    {
      echo "## High-Signal Matches"
      echo
      echo '```text'
      cat "${matches}"
      echo '```'
    } >>"${diagnostics}"
  else
    {
      echo "## High-Signal Matches"
      echo
      echo "No high-signal diagnostics matched."
    } >>"${diagnostics}"
  fi
  rm -f "${matches}"
}

model_from_eval_list() {
  local raw="$1"
  local models=()
  for value in $(csv_to_words "${raw}"); do
    [[ -n "${value}" ]] && models+=("${value}")
  done
  if [[ "${#models[@]}" -eq 1 ]]; then
    printf '%s' "${models[0]}"
  fi
}

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
shim_base_url="${shim_base_url%/}"
model="${UPSTREAM_PROVIDER_ROUTING_MODEL:-${MODEL:-${CODEX_MODEL:-${TESTER_MODEL:-}}}}"
if [[ -z "${model}" && -n "${CODEX_EVAL_MODELS:-}" ]]; then
  model="$(model_from_eval_list "${CODEX_EVAL_MODELS}")"
fi
if [[ -z "${model}" ]]; then
  echo "upstream provider routing smoke failed: set UPSTREAM_PROVIDER_ROUTING_MODEL, MODEL, CODEX_MODEL, TESTER_MODEL, or a single CODEX_EVAL_MODELS value" >&2
  usage >&2
  exit 2
fi
if [[ "${model}" != */* || "${model}" == */ || "${model}" == /* ]]; then
  echo "upstream provider routing smoke failed: model must use provider/model form: ${model}" >&2
  exit 2
fi

auth_header="${SHIM_AUTH_HEADER:-}"
if [[ -z "${auth_header}" && -n "${GW_API_KEY:-}" ]]; then
  auth_header="Authorization: Bearer ${GW_API_KEY}"
elif [[ -z "${auth_header}" && -n "${OPENAI_API_KEY:-}" ]]; then
  auth_header="Authorization: Bearer ${OPENAI_API_KEY}"
fi

require_readyz="${UPSTREAM_PROVIDER_ROUTING_REQUIRE_READYZ:-1}"
require_derived="${UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED:-0}"
timeout_seconds="${UPSTREAM_PROVIDER_ROUTING_TIMEOUT_SECONDS:-180}"
if ! [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid UPSTREAM_PROVIDER_ROUTING_TIMEOUT_SECONDS: ${timeout_seconds}" >&2
  exit 2
fi

artifact_root="${UPSTREAM_PROVIDER_ROUTING_ARTIFACT_DIR:-.tmp/upstream-provider-routing-smoke}"
run_id="${UPSTREAM_PROVIDER_ROUTING_RUN_ID:-$(slugify "${model}")_$(date -u +%Y%m%dT%H%M%SZ)}"
artifact_dir="${artifact_root%/}/${run_id}"
shim_log="${UPSTREAM_PROVIDER_ROUTING_SHIM_LOG:-.data/shim.log}"
warnings_path="${artifact_dir}/warnings.txt"
failures_path="${artifact_dir}/failures.txt"
log_captured=0

mkdir -p "${artifact_dir}"
: >"${warnings_path}"
: >"${failures_path}"

warn_harness() {
  local message="$1"
  echo "warning: ${message}" >&2
  printf 'warning: %s\n' "${message}" >>"${warnings_path}"
}

fail_harness() {
  local message="$1"
  echo "${message}" >&2
  printf 'failure: %s\n' "${message}" >>"${failures_path}"
  exit 1
}

finish_run() {
  local exit_code=$?
  if [[ -d "${artifact_dir}" && "${log_captured}" -eq 0 ]]; then
    capture_shim_log "${shim_log}" "${log_start:-0}" "${artifact_dir}" || true
    log_captured=1
  fi
  if [[ "${exit_code}" -ne 0 && -d "${artifact_dir}" ]]; then
    jq -n \
      --arg model "${model}" \
      --arg artifact_dir "${artifact_dir}" \
      --argjson exit_code "${exit_code}" \
      '{object:"upstream_provider_routing_smoke.summary",status:"failed",model:$model,artifact_dir:$artifact_dir,exit_code:$exit_code}' \
      >"${artifact_dir}/summary.json" || true
    {
      echo "# Upstream Provider Routing Smoke"
      echo
      echo "- Status: failed"
      echo "- Model: \`${model}\`"
      echo "- Artifacts: \`${artifact_dir}\`"
      echo "- Exit code: \`${exit_code}\`"
      echo
      echo "## Failures"
      echo
      if [[ -s "${failures_path}" ]]; then
        sed 's/^/- /' "${failures_path}"
      else
        echo "No structured failure was recorded before exit."
      fi
      echo
      echo "## Warnings"
      echo
      if [[ -s "${warnings_path}" ]]; then
        sed 's/^/- /' "${warnings_path}"
      else
        echo "None."
      fi
    } >"${artifact_dir}/summary.md" || true
    echo "artifacts: ${artifact_dir}" >&2
  fi
  exit "${exit_code}"
}

curl_capture() {
  local method="$1"
  local url="$2"
  local body_path="$3"
  local output_path="$4"
  local status_path="$5"
  local curl_err_path="${output_path}.curl.err"
  local status
  local curl_exit
  local args=(-sS -X "${method}" -o "${output_path}" -w "%{http_code}")

  if [[ -n "${auth_header}" ]]; then
    args+=(-H "${auth_header}")
  fi
  if [[ -n "${body_path}" ]]; then
    args+=(-H "Content-Type: application/json" --data-binary "@${body_path}")
  fi
  args+=("${url}")

  set +e
  status="$(curl "${args[@]}" 2>"${curl_err_path}")"
  curl_exit=$?
  set -e

  printf '%s\n' "${status:-000}" >"${status_path}"
  if [[ "${curl_exit}" -ne 0 ]]; then
    jq -n --arg message "curl failed before writing a response body" \
      --arg exit_code "${curl_exit}" \
      --arg stderr "$(cat "${curl_err_path}")" \
      '{error:{message:$message,type:"transport_error",curl_exit:($exit_code|tonumber),stderr:$stderr}}' >"${output_path}"
    return "${curl_exit}"
  fi
  return 0
}

request_json() {
  local name="$1"
  local jq_program="$2"
  shift 2
  jq -nc "$@" "${jq_program}" >"${artifact_dir}/${name}.request.json"
}

run_json_request() {
  local name="$1"
  local method="$2"
  local path="$3"
  local request_path="${artifact_dir}/${name}.request.json"
  local response_path="${artifact_dir}/${name}.response.json"
  local status_path="${artifact_dir}/${name}.status"
  curl_capture "${method}" "${shim_base_url}${path}" "${request_path}" "${response_path}" "${status_path}" >/dev/null || true
}

run_get() {
  local name="$1"
  local path="$2"
  local response_path="${artifact_dir}/${name}.response.json"
  local status_path="${artifact_dir}/${name}.status"
  curl_capture "GET" "${shim_base_url}${path}" "" "${response_path}" "${status_path}" >/dev/null || true
}

status_of() {
  cat "${artifact_dir}/$1.status"
}

response_of() {
  printf '%s' "${artifact_dir}/$1.response.json"
}

require_success_status() {
  local name="$1"
  local label="$2"
  local status
  status="$(status_of "${name}")"
  if ! is_2xx "${status}"; then
    echo "${label} returned HTTP ${status}" >&2
    cat "$(response_of "${name}")" >&2
    fail_harness "${label} returned HTTP ${status}"
  fi
}

allow_derived_status() {
  local name="$1"
  local label="$2"
  local status
  status="$(status_of "${name}")"
  if is_2xx "${status}"; then
    return 0
  fi
  if bool_enabled "${require_derived}"; then
    echo "${label} returned HTTP ${status}" >&2
    cat "$(response_of "${name}")" >&2
    fail_harness "${label} returned HTTP ${status}"
  fi
  case "${status}" in
    400 | 404 | 405 | 501 | 502 | 503 | 504)
      warn_harness "${label} returned HTTP ${status}; treating as provider capability gap because UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED=${require_derived}"
      return 0
      ;;
    *)
      echo "${label} returned unexpected HTTP ${status}" >&2
      cat "$(response_of "${name}")" >&2
      fail_harness "${label} returned unexpected HTTP ${status}"
      ;;
  esac
}

write_run_env() {
  cat >"${artifact_dir}/run.env" <<EOF
SHIM_BASE_URL=${shim_base_url}
UPSTREAM_PROVIDER_ROUTING_MODEL=${model}
SHIM_AUTH_HEADER_SET=$([[ -n "${auth_header}" ]] && echo true || echo false)
UPSTREAM_PROVIDER_ROUTING_REQUIRE_READYZ=${require_readyz}
UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED=${require_derived}
UPSTREAM_PROVIDER_ROUTING_TIMEOUT_SECONDS=${timeout_seconds}
UPSTREAM_PROVIDER_ROUTING_SHIM_LOG=${shim_log}
EOF
}

wait_http_ok() {
  local label="$1"
  local path="$2"
  local deadline=$((SECONDS + timeout_seconds))
  while [[ "${SECONDS}" -le "${deadline}" ]]; do
    if [[ -n "${auth_header}" ]]; then
      if curl -fsS -H "${auth_header}" "${shim_base_url}${path}" >/dev/null 2>&1; then
        return 0
      fi
    else
      if curl -fsS "${shim_base_url}${path}" >/dev/null 2>&1; then
        return 0
      fi
    fi
    sleep 1
  done
  fail_harness "${label} did not become ready: ${shim_base_url}${path}"
}

provider_prefix="${model%%/*}"
unknown_model="${provider_prefix}/__routing_smoke_missing_model__"
log_start="$(shim_log_size "${shim_log}")"
trap finish_run EXIT
write_run_env

echo "==> upstream provider routing smoke: ${model}"
echo "==> waiting for shim health: ${shim_base_url}/healthz"
wait_http_ok "shim health" "/healthz"

if bool_enabled "${require_readyz}"; then
  echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
  wait_http_ok "shim readiness" "/readyz"
else
  echo "==> shim readiness is capture-only"
fi

echo "==> capturing readiness and capability state"
run_get "healthz" "/healthz"
run_get "readyz" "/readyz"
run_get "capabilities" "/debug/capabilities"
require_success_status "healthz" "/healthz"
if bool_enabled "${require_readyz}"; then
  require_success_status "readyz" "/readyz"
elif ! is_2xx "$(status_of "readyz")"; then
  warn_harness "/readyz returned HTTP $(status_of "readyz")"
fi
require_success_status "capabilities" "/debug/capabilities"

jq -e --arg model "${model}" '
  (.upstream_provider_routing // .runtime.upstream_provider_routing) as $routing |
  .object == "shim.capabilities" and
  $routing.enabled == true and
  ([$routing.providers[].models[]?] | index($model)) != null
' "$(response_of "capabilities")" >/dev/null || {
  jq '{object, upstream_provider_routing: (.upstream_provider_routing // .runtime.upstream_provider_routing)}' "$(response_of "capabilities")" >&2
  fail_harness "/debug/capabilities does not advertise upstream provider routing for ${model}"
}

echo "==> checking live /v1/models catalog"
run_get "models" "/v1/models"
require_success_status "models" "GET /v1/models"
jq -e --arg model "${model}" '
  .object == "list" and
  ([.data[].id] | index($model)) != null
' "$(response_of "models")" >/dev/null || {
  jq '{object, models: [.data[].id]}' "$(response_of "models")" >&2
  fail_harness "GET /v1/models did not list ${model}"
}

echo "==> checking fail-closed unknown provider/model"
request_json "unknown_model_responses" \
  '{model:$model, store:false, input:"This request should fail before upstream routing."}' \
  --arg model "${unknown_model}"
run_json_request "unknown_model_responses" "POST" "/v1/responses"
unknown_status="$(status_of "unknown_model_responses")"
if [[ "${unknown_status}" != "400" ]]; then
  cat "$(response_of "unknown_model_responses")" >&2
  fail_harness "unknown provider/model returned HTTP ${unknown_status}, expected 400"
fi
jq -e '.error.param == "model"' "$(response_of "unknown_model_responses")" >/dev/null || {
  cat "$(response_of "unknown_model_responses")" >&2
  fail_harness "unknown provider/model error did not point at model"
}

echo "==> checking POST /v1/responses"
request_json "responses_create" \
  '{model:$model, store:false, input:"Reply with the exact token ROUTE_OK."}' \
  --arg model "${model}"
run_json_request "responses_create" "POST" "/v1/responses"
require_success_status "responses_create" "POST /v1/responses"
jq -e --arg model "${model}" '
  (.object == "response" or (.id | type == "string")) and
  .model == $model and
  ((.status // "completed") as $status | ($status == "completed" or $status == "in_progress" or $status == "queued"))
' "$(response_of "responses_create")" >/dev/null || {
  jq '{id, object, status, model, output_text}' "$(response_of "responses_create")" >&2
  fail_harness "POST /v1/responses returned unexpected routed response shape"
}

echo "==> checking POST /v1/chat/completions"
request_json "chat_completions_create" \
  '{model:$model, messages:[{role:"user",content:"Reply with the exact token CHAT_ROUTE_OK."}]}' \
  --arg model "${model}"
run_json_request "chat_completions_create" "POST" "/v1/chat/completions"
require_success_status "chat_completions_create" "POST /v1/chat/completions"
jq -e --arg model "${model}" '
  .object == "chat.completion" and
  .model == $model and
  (.choices | type == "array" and length > 0)
' "$(response_of "chat_completions_create")" >/dev/null || {
  jq '{id, object, model, choice0: .choices[0]}' "$(response_of "chat_completions_create")" >&2
  fail_harness "POST /v1/chat/completions returned unexpected routed response shape"
}

echo "==> checking POST /v1/responses/input_tokens"
request_json "input_tokens" \
  '{model:$model, input:"Count this provider-routed request."}' \
  --arg model "${model}"
run_json_request "input_tokens" "POST" "/v1/responses/input_tokens"
allow_derived_status "input_tokens" "POST /v1/responses/input_tokens"
if is_2xx "$(status_of "input_tokens")"; then
  jq -e '.object == "response.input_tokens" and (.input_tokens | type == "number")' "$(response_of "input_tokens")" >/dev/null || {
    cat "$(response_of "input_tokens")" >&2
    fail_harness "POST /v1/responses/input_tokens returned unexpected shape"
  }
fi

echo "==> checking POST /v1/responses/compact"
request_json "compact" \
  '{model:$model, input:[{type:"message",role:"user",content:[{type:"input_text",text:"Remember provider routing smoke."}]}]}' \
  --arg model "${model}"
run_json_request "compact" "POST" "/v1/responses/compact"
allow_derived_status "compact" "POST /v1/responses/compact"
if is_2xx "$(status_of "compact")"; then
  jq -e '.object == "response.compaction" and (.output | type == "array")' "$(response_of "compact")" >/dev/null || {
    cat "$(response_of "compact")" >&2
    fail_harness "POST /v1/responses/compact returned unexpected shape"
  }
fi

echo "==> checking model-less derived request boundary"
request_json "input_tokens_model_less" \
  '{input:"Count this local model-less request."}'
run_json_request "input_tokens_model_less" "POST" "/v1/responses/input_tokens"
require_success_status "input_tokens_model_less" "model-less POST /v1/responses/input_tokens"
jq -e '.object == "response.input_tokens" and (.input_tokens | type == "number")' "$(response_of "input_tokens_model_less")" >/dev/null || {
  cat "$(response_of "input_tokens_model_less")" >&2
  fail_harness "model-less POST /v1/responses/input_tokens returned unexpected shape"
}

request_json "compact_model_less" \
  '{input:[{type:"message",role:"user",content:[{type:"input_text",text:"Model-less compact should not choose a hidden provider."}]}]}'
run_json_request "compact_model_less" "POST" "/v1/responses/compact"
compact_model_less_status="$(status_of "compact_model_less")"
if [[ "${compact_model_less_status}" != "400" ]]; then
  cat "$(response_of "compact_model_less")" >&2
  fail_harness "model-less POST /v1/responses/compact returned HTTP ${compact_model_less_status}, expected local 400"
fi
jq -e '.error.param == "model"' "$(response_of "compact_model_less")" >/dev/null || {
  cat "$(response_of "compact_model_less")" >&2
  fail_harness "model-less POST /v1/responses/compact error did not point at model"
}

capture_shim_log "${shim_log}" "${log_start}" "${artifact_dir}"
log_captured=1

jq -n \
  --arg model "${model}" \
  --arg artifact_dir "${artifact_dir}" \
  --arg healthz_status "$(status_of "healthz")" \
  --arg readyz_status "$(status_of "readyz")" \
  --arg models_status "$(status_of "models")" \
  --arg responses_status "$(status_of "responses_create")" \
  --arg chat_status "$(status_of "chat_completions_create")" \
  --arg input_tokens_status "$(status_of "input_tokens")" \
  --arg compact_status "$(status_of "compact")" \
  --arg warnings_file "${warnings_path}" \
  '{
    object: "upstream_provider_routing_smoke.summary",
    status: "passed",
    model: $model,
    artifact_dir: $artifact_dir,
    statuses: {
      healthz: $healthz_status,
      readyz: $readyz_status,
      models: $models_status,
      responses: $responses_status,
      chat_completions: $chat_status,
      input_tokens: $input_tokens_status,
      compact: $compact_status
    },
    warnings_file: $warnings_file
  }' >"${artifact_dir}/summary.json"

{
  echo "# Upstream Provider Routing Smoke"
  echo
  echo "- Status: passed"
  echo "- Model: \`${model}\`"
  echo "- Artifacts: \`${artifact_dir}\`"
  echo
  echo "## HTTP Statuses"
  echo
  echo "| Check | Status |"
  echo "| --- | --- |"
  echo "| /healthz | $(status_of "healthz") |"
  echo "| /readyz | $(status_of "readyz") |"
  echo "| /v1/models | $(status_of "models") |"
  echo "| /v1/responses | $(status_of "responses_create") |"
  echo "| /v1/chat/completions | $(status_of "chat_completions_create") |"
  echo "| /v1/responses/input_tokens | $(status_of "input_tokens") |"
  echo "| /v1/responses/compact | $(status_of "compact") |"
  echo
  if [[ -s "${warnings_path}" ]]; then
    echo "## Warnings"
    echo
    sed 's/^/- /' "${warnings_path}"
  else
    echo "## Warnings"
    echo
    echo "None."
  fi
} >"${artifact_dir}/summary.md"

jq '{status, model, statuses}' "${artifact_dir}/summary.json"
echo "upstream provider routing smoke passed"
echo "artifacts: ${artifact_dir}"
