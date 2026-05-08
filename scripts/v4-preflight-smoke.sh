#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/v4-preflight-smoke.sh

Environment:
  SHIM_BASE_URL                          Shim base URL. Default: http://127.0.0.1:8080
  SHIM_AUTH_HEADER                       Full auth header, e.g. "Authorization: Bearer $GW_API_KEY".
  SHIM_API_KEY | GW_API_KEY | OPENAI_API_KEY
                                         Used to build an Authorization header when SHIM_AUTH_HEADER is unset.
  V4_PREFLIGHT_MODEL | MODEL | CODEX_MODEL | TESTER_MODEL
                                         Model used for the direct Responses trace smoke.
                                         Falls back to the first /v1/models id, then devstack-model.
  V4_PREFLIGHT_PROVIDER_MODEL | UPSTREAM_PROVIDER_ROUTING_MODEL
                                         Optional provider/model alias. When set, also runs upstream-provider-routing-smoke.
  V4_PREFLIGHT_RUN_PROVIDER_ROUTING      1, 0, or auto. Default: auto.
  V4_PREFLIGHT_RUN_CODEX_DOCTOR          Run shimctl codex doctor in this artifact dir. Default: 0.
  V4_PREFLIGHT_REQUIRE_READYZ            Require /readyz to be 2xx. Default: 1.
  V4_PREFLIGHT_REQUIRE_DEBUG_TRACE       Require /debug/traces to work. Default: 1.
  V4_PREFLIGHT_TIMEOUT_SECONDS           Per-probe timeout / readiness wait. Default: 60.
  V4_PREFLIGHT_ARTIFACT_DIR              Artifact root. Default: .tmp/v4-preflight-smoke.
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "missing required command: ${cmd}" >&2
    exit 127
  fi
}

slugify() {
  printf '%s' "$1" | tr '/:[:space:]' '---' | tr -cd 'A-Za-z0-9._-'
}

bool_enabled() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

is_2xx() {
  local status="${1:-000}"
  [[ "${status}" =~ ^2[0-9][0-9]$ ]]
}

status_of() {
  local name="$1"
  local path="${artifact_dir}/${name}.status"
  if [[ -s "${path}" ]]; then
    cat "${path}"
  else
    printf 'missing'
  fi
}

response_of() {
  printf '%s' "${artifact_dir}/$1.response.json"
}

shim_log_size() {
  local path="$1"
  if [[ -f "${path}" ]]; then
    wc -c <"${path}" | tr -d '[:space:]'
  else
    printf '0'
  fi
}

capture_shim_log() {
  local path="$1"
  local start="$2"
  local out_dir="$3"
  local out_path="${out_dir}/shim.log.slice"

  if [[ ! -f "${path}" ]]; then
    return 0
  fi
  local size
  size="$(shim_log_size "${path}")"
  if [[ "${start}" =~ ^[0-9]+$ ]] && (( start > 0 && size >= start )); then
    tail -c +"$((start + 1))" "${path}" >"${out_path}" || true
  else
    tail -c 65536 "${path}" >"${out_path}" || true
  fi
}

require_cmd curl
require_cmd jq
require_cmd date
require_cmd tail
require_cmd wc

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
shim_base_url="${shim_base_url%/}"
timeout_seconds="${V4_PREFLIGHT_TIMEOUT_SECONDS:-60}"
require_readyz="${V4_PREFLIGHT_REQUIRE_READYZ:-1}"
require_debug_trace="${V4_PREFLIGHT_REQUIRE_DEBUG_TRACE:-1}"
shim_log="${V4_PREFLIGHT_SHIM_LOG:-.data/shim.log}"
artifact_root="${V4_PREFLIGHT_ARTIFACT_DIR:-.tmp/v4-preflight-smoke}"
provider_model="${V4_PREFLIGHT_PROVIDER_MODEL:-${UPSTREAM_PROVIDER_ROUTING_MODEL:-}}"
probe_model="${V4_PREFLIGHT_MODEL:-${MODEL:-${CODEX_MODEL:-${TESTER_MODEL:-}}}}"
if [[ -n "${V4_PREFLIGHT_RUN_ID:-}" ]]; then
  run_id="${V4_PREFLIGHT_RUN_ID}"
elif [[ -n "${probe_model}" ]]; then
  run_id="$(slugify "${probe_model}")_$(date -u +%Y%m%dT%H%M%SZ)"
else
  run_id="default_$(date -u +%Y%m%dT%H%M%SZ)"
fi
artifact_dir="${artifact_root%/}/${run_id}"
warnings_path="${artifact_dir}/warnings.txt"
failures_path="${artifact_dir}/failures.txt"
log_start="$(shim_log_size "${shim_log}")"
log_captured=0
provider_routing_status="skipped"
provider_routing_artifact_dir=""
codex_doctor_status="skipped"
codex_doctor_artifact_dir=""

auth_header="${SHIM_AUTH_HEADER:-}"
if [[ -z "${auth_header}" ]]; then
  if [[ -n "${SHIM_API_KEY:-}" ]]; then
    auth_header="Authorization: Bearer ${SHIM_API_KEY}"
  elif [[ -n "${GW_API_KEY:-}" ]]; then
    auth_header="Authorization: Bearer ${GW_API_KEY}"
  elif [[ -n "${OPENAI_API_KEY:-}" ]]; then
    auth_header="Authorization: Bearer ${OPENAI_API_KEY}"
  fi
fi

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

curl_capture() {
  local method="$1"
  local url="$2"
  local body_path="$3"
  local output_path="$4"
  local status_path="$5"
  local headers_path="${6:-}"
  local curl_err_path="${output_path}.curl.err"
  local status
  local curl_exit
  local args=(-sS -X "${method}" -o "${output_path}" -w "%{http_code}" --max-time "${timeout_seconds}")

  if [[ -n "${headers_path}" ]]; then
    args+=(-D "${headers_path}")
  fi
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

run_get() {
  local name="$1"
  local path="$2"
  curl_capture "GET" "${shim_base_url}${path}" "" \
    "${artifact_dir}/${name}.response.json" \
    "${artifact_dir}/${name}.status" \
    "${artifact_dir}/${name}.headers" >/dev/null || true
}

request_json() {
  local name="$1"
  shift
  jq -nc "$@" >"${artifact_dir}/${name}.request.json"
}

run_json_request() {
  local name="$1"
  local method="$2"
  local path="$3"
  curl_capture "${method}" "${shim_base_url}${path}" \
    "${artifact_dir}/${name}.request.json" \
    "${artifact_dir}/${name}.response.json" \
    "${artifact_dir}/${name}.status" \
    "${artifact_dir}/${name}.headers" >/dev/null || true
}

wait_get_2xx() {
  local name="$1"
  local path="$2"
  local required="$3"
  local deadline
  deadline=$(( $(date +%s) + timeout_seconds ))

  while true; do
    run_get "${name}" "${path}"
    if is_2xx "$(status_of "${name}")"; then
      return 0
    fi
    if (( $(date +%s) >= deadline )); then
      if bool_enabled "${required}"; then
        fail_harness "${path} did not become ready; last HTTP $(status_of "${name}")"
      fi
      warn_harness "${path} did not become ready; last HTTP $(status_of "${name}")"
      return 0
    fi
    sleep 1
  done
}

require_success_status() {
  local name="$1"
  local label="$2"
  local status
  status="$(status_of "${name}")"
  if ! is_2xx "${status}"; then
    echo "${label} returned HTTP ${status}" >&2
    if [[ -f "$(response_of "${name}")" ]]; then
      cat "$(response_of "${name}")" >&2 || true
      echo >&2
    fi
    fail_harness "${label} returned HTTP ${status}"
  fi
}

header_value() {
  local headers_path="$1"
  local header_name="$2"
  awk -v name="${header_name}" '
    BEGIN { IGNORECASE = 1 }
    index($0, name ":") == 1 {
      sub(/^[^:]+:[[:space:]]*/, "", $0)
      gsub(/\r/, "", $0)
      print $0
      exit
    }
  ' "${headers_path}" 2>/dev/null || true
}

validate_capabilities() {
  local path
  local require_debug_json=false
  path="$(response_of capabilities)"
  if bool_enabled "${require_debug_trace}"; then
    require_debug_json=true
  fi
  if ! jq -e --argjson require_debug "${require_debug_json}" '
    . as $root
    | .object == "shim.capabilities"
      and (.backends.schema_version == "v4.backend_capabilities.v1")
      and (.plugins.schema_version == "v4.plugin_contracts.v1")
      and ((.backends.issues // []) | length == 0)
      and ((.plugins.issues // []) | length == 0)
      and ((.backends.components // []) | type == "array")
      and ((.backends.components // []) | length > 0)
      and ((.plugins.plugins // []) | type == "array")
      and ((.plugins.plugins // []) | length > 0)
      and (all(($root.plugins.plugins // [])[]; ((.request_cleanup_hooks // []) | type) == "array"))
      and (all(($root.plugins.plugins // [])[]; all((.request_cleanup_hooks // [])[]; type == "string")))
      and (all(($root.backends.components // [])[] | select((.plugin_id // "") != ""); . as $component |
        any(($root.plugins.plugins // [])[]; .id == $component.plugin_id and ((.version // "") == ($component.plugin_version // "")))
      ))
      and (((.runtime.ops.debug_traces.enabled // false) == true) or ($require_debug | not))
  ' "${path}" >/dev/null; then
    fail_harness "/debug/capabilities does not satisfy V4 backend/plugin/debug-trace contract"
  fi
}

validate_debug_trace() {
  local name="$1"
  local request_id="$2"
  local path
  path="$(response_of "${name}")"
  if ! jq -e --arg request_id "${request_id}" '
    .object == "shim.debug_trace"
      and .request_id == $request_id
      and .surface == "responses"
      and .source_format == "responses.create"
      and ((.final_status // 0) >= 200 and (.final_status // 0) < 300)
      and ((.tool_decisions // []) | length >= 1)
      and (all((.tool_decisions // [])[]; (.type | type) == "string"
        and (.family | type) == "string"
        and (.disposition | type) == "string"
        and (.capability_class | type) == "string"))
  ' "${path}" >/dev/null; then
    fail_harness "debug trace for ${request_id} does not include the expected V4 classifier metadata"
  fi
}

json_array_file() {
  local path="$1"
  jq -R -s 'split("\n") | map(select(length > 0))' "${path}"
}

write_summary() {
  local status="$1"
  local capabilities_summary="{}"
  local trace_summary="{}"
  local provider_summary="{}"
  local codex_summary="{}"

  if [[ -s "$(response_of capabilities)" ]] && jq -e . "$(response_of capabilities)" >/dev/null 2>&1; then
    capabilities_summary="$(jq -c '{
      backend_schema_version:(.backends.schema_version // ""),
      plugin_schema_version:(.plugins.schema_version // ""),
      backend_component_count:((.backends.components // []) | length),
      plugin_count:((.plugins.plugins // []) | length),
      request_cleanup_hook_plugins:[
        (.plugins.plugins // [])[]
        | select(((.request_cleanup_hooks // []) | length) > 0)
        | {id, hooks:.request_cleanup_hooks}
      ],
      debug_traces:(.runtime.ops.debug_traces // {})
    }' "$(response_of capabilities)")"
  fi
  if [[ -s "$(response_of responses_trace_debug_trace)" ]] && jq -e . "$(response_of responses_trace_debug_trace)" >/dev/null 2>&1; then
    trace_summary="$(jq -c '{
      request_id,
      model,
      selected_backend,
      backend_projection_class,
      plugin_id,
      plugin_version,
      tool_decisions:(.tool_decisions // []),
      transforms:(.transforms // [])
    }' "$(response_of responses_trace_debug_trace)")"
  fi
  if [[ -n "${provider_routing_artifact_dir}" ]]; then
    provider_summary="$(jq -n \
      --arg status "${provider_routing_status}" \
      --arg artifact_dir "${provider_routing_artifact_dir}" \
      '{status:$status,artifact_dir:$artifact_dir}')"
  fi
  if [[ -n "${codex_doctor_artifact_dir}" ]]; then
    codex_summary="$(jq -n \
      --arg status "${codex_doctor_status}" \
      --arg artifact_dir "${codex_doctor_artifact_dir}" \
      '{status:$status,artifact_dir:$artifact_dir}')"
  fi

  jq -n \
    --arg status "${status}" \
    --arg artifact_dir "${artifact_dir}" \
    --arg shim_base_url "${shim_base_url}" \
    --arg model "${probe_model}" \
    --arg provider_model "${provider_model}" \
    --arg healthz "$(status_of healthz)" \
    --arg readyz "$(status_of readyz)" \
    --arg models "$(status_of models)" \
    --arg capabilities "$(status_of capabilities)" \
    --arg responses_trace "$(status_of responses_trace)" \
    --arg responses_trace_debug_trace "$(status_of responses_trace_debug_trace)" \
    --arg provider_routing_status "${provider_routing_status}" \
    --arg codex_doctor_status "${codex_doctor_status}" \
    --argjson capabilities_summary "${capabilities_summary}" \
    --argjson trace_summary "${trace_summary}" \
    --argjson provider_summary "${provider_summary}" \
    --argjson codex_summary "${codex_summary}" \
    --argjson warnings "$(json_array_file "${warnings_path}")" \
    --argjson failures "$(json_array_file "${failures_path}")" \
    '{
      object:"v4_preflight_smoke.summary",
      status:$status,
      artifact_dir:$artifact_dir,
      shim_base_url:$shim_base_url,
      model:$model,
      provider_model:$provider_model,
      statuses:{
        healthz:$healthz,
        readyz:$readyz,
        models:$models,
        capabilities:$capabilities,
        responses_trace:$responses_trace,
        responses_trace_debug_trace:$responses_trace_debug_trace,
        provider_routing:$provider_routing_status,
        codex_doctor:$codex_doctor_status
      },
      capabilities:$capabilities_summary,
      trace:$trace_summary,
      provider_routing:$provider_summary,
      codex_doctor:$codex_summary,
      warnings:$warnings,
      failures:$failures
    }' >"${artifact_dir}/summary.json"

  {
    echo "# V4 Preflight Smoke"
    echo
    echo "- Status: ${status}"
    echo "- Shim: \`${shim_base_url}\`"
    echo "- Model: \`${probe_model}\`"
    if [[ -n "${provider_model}" ]]; then
      echo "- Provider model: \`${provider_model}\`"
    fi
    echo "- Artifacts: \`${artifact_dir}\`"
    echo
    echo "## Checks"
    echo
    echo "| Check | Status |"
    echo "| --- | --- |"
    echo "| healthz | \`$(status_of healthz)\` |"
    echo "| readyz | \`$(status_of readyz)\` |"
    echo "| models | \`$(status_of models)\` |"
    echo "| capabilities | \`$(status_of capabilities)\` |"
    echo "| responses trace request | \`$(status_of responses_trace)\` |"
    echo "| debug trace fetch | \`$(status_of responses_trace_debug_trace)\` |"
    echo "| provider routing | \`${provider_routing_status}\` |"
    echo "| codex doctor | \`${codex_doctor_status}\` |"
    echo
    echo "## Warnings"
    echo
    if [[ -s "${warnings_path}" ]]; then
      sed 's/^/- /' "${warnings_path}"
    else
      echo "None."
    fi
    echo
    echo "## Failures"
    echo
    if [[ -s "${failures_path}" ]]; then
      sed 's/^/- /' "${failures_path}"
    else
      echo "None."
    fi
  } >"${artifact_dir}/summary.md"
}

finish_run() {
  local exit_code=$?
  if [[ -d "${artifact_dir}" && "${log_captured}" -eq 0 ]]; then
    capture_shim_log "${shim_log}" "${log_start:-0}" "${artifact_dir}" || true
    log_captured=1
  fi
  if [[ -d "${artifact_dir}" && ! -s "${artifact_dir}/summary.json" ]]; then
    if [[ "${exit_code}" -eq 0 ]]; then
      write_summary "passed" || true
    else
      write_summary "failed" || true
      echo "artifacts: ${artifact_dir}" >&2
    fi
  fi
  exit "${exit_code}"
}
trap finish_run EXIT

{
  echo "SHIM_BASE_URL=${shim_base_url}"
  echo "V4_PREFLIGHT_MODEL=${probe_model}"
  echo "V4_PREFLIGHT_PROVIDER_MODEL=${provider_model}"
  echo "V4_PREFLIGHT_REQUIRE_READYZ=${require_readyz}"
  echo "V4_PREFLIGHT_REQUIRE_DEBUG_TRACE=${require_debug_trace}"
  echo "V4_PREFLIGHT_RUN_PROVIDER_ROUTING=${V4_PREFLIGHT_RUN_PROVIDER_ROUTING:-auto}"
  echo "V4_PREFLIGHT_RUN_CODEX_DOCTOR=${V4_PREFLIGHT_RUN_CODEX_DOCTOR:-0}"
  if [[ -n "${auth_header}" ]]; then
    echo "auth_header_present=true"
  else
    echo "auth_header_present=false"
  fi
} >"${artifact_dir}/run.env"

echo "==> v4 preflight smoke: ${shim_base_url}"
echo "==> waiting for shim health"
wait_get_2xx healthz /healthz 1
echo "==> waiting for shim readiness"
wait_get_2xx readyz /readyz "${require_readyz}"

echo "==> capturing model catalog and V4 capabilities"
run_get models /v1/models
require_success_status models "/v1/models"
run_get capabilities /debug/capabilities
require_success_status capabilities "/debug/capabilities"
validate_capabilities

if [[ -z "${probe_model}" ]]; then
  probe_model="$(jq -r '[.data[]?.id] | .[0] // empty' "$(response_of models)" 2>/dev/null || true)"
fi
if [[ -z "${probe_model}" ]]; then
  probe_model="devstack-model"
  warn_harness "no explicit model and /v1/models was empty; using devstack-model"
fi
echo "selected_model=${probe_model}" >>"${artifact_dir}/run.env"

echo "==> checking Responses request debug trace and tool classifier metadata"
request_json responses_trace --arg model "${probe_model}" '{
  model:$model,
  store:false,
  input:"V4 preflight smoke. Do not call tools. Reply exactly V4_PREFLIGHT_OK.",
  tools:[{
    type:"function",
    name:"v4_preflight_lookup",
    description:"Sentinel function for V4 preflight classifier diagnostics. Do not call unless explicitly asked.",
    parameters:{type:"object",properties:{},additionalProperties:false},
    strict:false
  }]
}'
run_json_request responses_trace POST /v1/responses
require_success_status responses_trace "POST /v1/responses"
responses_request_id="$(header_value "${artifact_dir}/responses_trace.headers" "X-Request-Id")"
if [[ -z "${responses_request_id}" ]]; then
  fail_harness "POST /v1/responses did not return X-Request-Id"
fi
run_get responses_trace_debug_trace "/debug/traces/${responses_request_id}"
if is_2xx "$(status_of responses_trace_debug_trace)"; then
  validate_debug_trace responses_trace_debug_trace "${responses_request_id}"
elif bool_enabled "${require_debug_trace}"; then
  fail_harness "/debug/traces/${responses_request_id} returned HTTP $(status_of responses_trace_debug_trace)"
else
  warn_harness "/debug/traces/${responses_request_id} returned HTTP $(status_of responses_trace_debug_trace)"
fi

run_provider_routing="${V4_PREFLIGHT_RUN_PROVIDER_ROUTING:-auto}"
if [[ "${run_provider_routing}" == "auto" ]]; then
  if [[ -n "${provider_model}" ]]; then
    run_provider_routing=1
  else
    run_provider_routing=0
  fi
fi
if bool_enabled "${run_provider_routing}"; then
  if [[ -z "${provider_model}" ]]; then
    fail_harness "V4_PREFLIGHT_RUN_PROVIDER_ROUTING is enabled but V4_PREFLIGHT_PROVIDER_MODEL is empty"
  fi
  echo "==> running nested upstream provider routing smoke: ${provider_model}"
  provider_routing_artifact_dir="${artifact_dir}/provider-routing/$(slugify "${provider_model}")"
  set +e
  SHIM_BASE_URL="${shim_base_url}" \
    SHIM_AUTH_HEADER="${auth_header}" \
    UPSTREAM_PROVIDER_ROUTING_MODEL="${provider_model}" \
    UPSTREAM_PROVIDER_ROUTING_ARTIFACT_DIR="${artifact_dir}/provider-routing" \
    UPSTREAM_PROVIDER_ROUTING_RUN_ID="$(slugify "${provider_model}")" \
    UPSTREAM_PROVIDER_ROUTING_REQUIRE_READYZ="${require_readyz}" \
    bash ./scripts/upstream-provider-routing-smoke.sh \
      >"${artifact_dir}/provider-routing.stdout" \
      2>"${artifact_dir}/provider-routing.stderr"
  provider_exit=$?
  set -e
  if [[ "${provider_exit}" -eq 0 ]]; then
    provider_routing_status="passed"
  else
    provider_routing_status="failed"
    fail_harness "nested upstream provider routing smoke failed; see ${artifact_dir}/provider-routing.stderr"
  fi
fi

if bool_enabled "${V4_PREFLIGHT_RUN_CODEX_DOCTOR:-0}"; then
  require_cmd go
  echo "==> running shimctl codex doctor"
  config_path="${CONFIG:-config.yaml}"
  codex_provider="${CODEX_PROVIDER:-gateway-shim}"
  codex_api_key_env="${CODEX_API_KEY_ENV:-GW_API_KEY}"
  codex_base_url="${CODEX_BASE_URL:-${shim_base_url}/v1}"
  codex_doctor_artifact_dir="${artifact_dir}/codex-doctor"
  set +e
  go run ./cmd/shimctl -config "${config_path}" codex doctor \
    -provider "${codex_provider}" \
    -base-url "${codex_base_url}" \
    -model "${probe_model}" \
    -api-key-env "${codex_api_key_env}" \
    -out "${codex_doctor_artifact_dir}" \
    >"${artifact_dir}/codex-doctor.stdout" \
    2>"${artifact_dir}/codex-doctor.stderr"
  codex_exit=$?
  set -e
  if [[ "${codex_exit}" -eq 0 ]]; then
    codex_doctor_status="passed"
  else
    codex_doctor_status="failed"
    fail_harness "shimctl codex doctor failed; see ${artifact_dir}/codex-doctor.stderr"
  fi
fi

write_summary "passed"
echo "v4 preflight smoke passed"
echo "artifacts: ${artifact_dir}"
