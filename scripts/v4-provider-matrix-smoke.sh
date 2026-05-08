#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  V4_PROVIDER_MATRIX_MODELS="deepseek/deepseek-v4-pro xiaomi/mimo-v2.5-pro" \
  ./scripts/v4-provider-matrix-smoke.sh

Environment:
  SHIM_BASE_URL                         Shim base URL. Default: http://127.0.0.1:8080
  SHIM_AUTH_HEADER                      Full auth header, e.g. "Authorization: Bearer $GW_API_KEY".
  SHIM_API_KEY | GW_API_KEY | OPENAI_API_KEY
                                        Used by nested smokes when SHIM_AUTH_HEADER is unset.
  V4_PROVIDER_MATRIX_MODELS             Space or comma separated provider/model aliases.
                                        Default: current V4 operator matrix models.
  V4_PROVIDER_MATRIX_RUN_ROUTING         Run upstream-provider-routing-smoke per model. Default: 1.
  V4_PROVIDER_MATRIX_RUN_PREFLIGHT       Run v4-preflight-smoke per model. Default: 1.
  V4_PROVIDER_MATRIX_RUN_CODEX_DOCTOR    Pass V4_PREFLIGHT_RUN_CODEX_DOCTOR to preflight. Default: 0.
  V4_PROVIDER_MATRIX_REQUIRE_READYZ      Require /readyz in nested checks. Default: 1.
  V4_PROVIDER_MATRIX_REQUIRE_DERIVED     Require derived helper endpoints in routing smoke. Default: 0.
  V4_PROVIDER_MATRIX_TIMEOUT_SECONDS     Per nested smoke timeout. Default: 180.
  V4_PROVIDER_MATRIX_ARTIFACT_DIR        Artifact root. Default: .tmp/v4-provider-matrix-smoke.
  V4_PROVIDER_MATRIX_RUN_ID              Artifact run id. Default: matrix_<UTC timestamp>.

This is a shim-owned operator smoke. It does not replace codex-eval-auto and
does not widen any OpenAI API compatibility claim.
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
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1 | true | yes | on) return 0 ;;
    *) return 1 ;;
  esac
}

summary_status() {
  local path="$1"
  if [[ -s "${path}" ]] && jq -e . "${path}" >/dev/null 2>&1; then
    jq -r '.status // "missing"' "${path}"
  else
    printf 'missing'
  fi
}

summary_nested_status() {
  local path="$1"
  local jq_path="$2"
  if [[ -s "${path}" ]] && jq -e . "${path}" >/dev/null 2>&1; then
    jq -r "${jq_path} // \"missing\"" "${path}"
  else
    printf 'missing'
  fi
}

run_result() {
  local routing_enabled="$1"
  local routing_status="$2"
  local preflight_enabled="$3"
  local preflight_status="$4"

  if bool_enabled "${routing_enabled}" && [[ "${routing_status}" != "passed" ]]; then
    printf 'failed'
    return
  fi
  if bool_enabled "${preflight_enabled}" && [[ "${preflight_status}" != "passed" ]]; then
    printf 'failed'
    return
  fi
  printf 'passed'
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd bash
require_cmd date
require_cmd jq
require_cmd mkdir
require_cmd tr

default_models="deepseek/deepseek-v4-pro xiaomi/mimo-v2.5-pro svgun/kimi-k2.6 svgun/qwen-3.6"
models_raw="${V4_PROVIDER_MATRIX_MODELS:-${CODEX_EVAL_MODELS:-${default_models}}}"
models=()
for model in $(csv_to_words "${models_raw}"); do
  if [[ -n "${model}" ]]; then
    models+=("${model}")
  fi
done
if [[ "${#models[@]}" -eq 0 ]]; then
  echo "v4 provider matrix smoke failed: no models configured" >&2
  usage >&2
  exit 2
fi

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
shim_base_url="${shim_base_url%/}"
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

run_routing="${V4_PROVIDER_MATRIX_RUN_ROUTING:-1}"
run_preflight="${V4_PROVIDER_MATRIX_RUN_PREFLIGHT:-1}"
run_codex_doctor="${V4_PROVIDER_MATRIX_RUN_CODEX_DOCTOR:-0}"
require_readyz="${V4_PROVIDER_MATRIX_REQUIRE_READYZ:-1}"
require_derived="${V4_PROVIDER_MATRIX_REQUIRE_DERIVED:-0}"
timeout_seconds="${V4_PROVIDER_MATRIX_TIMEOUT_SECONDS:-180}"
if ! [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid V4_PROVIDER_MATRIX_TIMEOUT_SECONDS: ${timeout_seconds}" >&2
  exit 2
fi
if ! bool_enabled "${run_routing}" && ! bool_enabled "${run_preflight}"; then
  echo "v4 provider matrix smoke failed: enable routing, preflight, or both" >&2
  exit 2
fi

artifact_root="${V4_PROVIDER_MATRIX_ARTIFACT_DIR:-.tmp/v4-provider-matrix-smoke}"
run_id="${V4_PROVIDER_MATRIX_RUN_ID:-matrix_$(date -u +%Y%m%dT%H%M%SZ)}"
artifact_dir="${artifact_root%/}/${run_id}"
rows_path="${artifact_dir}/rows.jsonl"
summary_json="${artifact_dir}/summary.json"
summary_md="${artifact_dir}/summary.md"

mkdir -p "${artifact_dir}"
: >"${rows_path}"

echo "==> v4 provider matrix smoke: ${shim_base_url}"
printf '==> models:'
for model in "${models[@]}"; do
  printf ' %s' "${model}"
done
printf '\n'

for model in "${models[@]}"; do
  if [[ "${model}" != */* || "${model}" == */ || "${model}" == /* ]]; then
    echo "invalid provider/model alias: ${model}" >&2
    jq -nc \
      --arg model "${model}" \
      '{model:$model,result:"failed",routing:{status:"skipped"},preflight:{status:"skipped",codex_doctor:"skipped"},error:"model must use provider/model form"}' \
      >>"${rows_path}"
    continue
  fi

  slug="$(slugify "${model}")"
  model_dir="${artifact_dir}/models/${slug}"
  mkdir -p "${model_dir}"
  printf '%s\n' "${model}" >"${model_dir}/model.txt"

  routing_status="skipped"
  routing_exit=0
  routing_summary=""
  routing_artifact=""
  preflight_status="skipped"
  preflight_exit=0
  preflight_summary=""
  preflight_artifact=""
  codex_doctor_status="skipped"

  echo "==> v4 provider matrix: ${model}"

  if bool_enabled "${run_routing}"; then
    echo "==> ${model}: upstream provider routing"
    routing_artifact="${model_dir}/routing/run"
    set +e
    SHIM_BASE_URL="${shim_base_url}" \
      SHIM_AUTH_HEADER="${auth_header}" \
      UPSTREAM_PROVIDER_ROUTING_MODEL="${model}" \
      UPSTREAM_PROVIDER_ROUTING_ARTIFACT_DIR="${model_dir}/routing" \
      UPSTREAM_PROVIDER_ROUTING_RUN_ID="run" \
      UPSTREAM_PROVIDER_ROUTING_REQUIRE_READYZ="${require_readyz}" \
      UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED="${require_derived}" \
      UPSTREAM_PROVIDER_ROUTING_TIMEOUT_SECONDS="${timeout_seconds}" \
      bash ./scripts/upstream-provider-routing-smoke.sh \
        >"${model_dir}/routing.stdout" \
        2>"${model_dir}/routing.stderr"
    routing_exit=$?
    set -e
    routing_summary="${routing_artifact}/summary.json"
    routing_status="$(summary_status "${routing_summary}")"
    if [[ "${routing_exit}" -ne 0 && "${routing_status}" == "missing" ]]; then
      routing_status="failed"
    fi
  fi

  if bool_enabled "${run_preflight}"; then
    echo "==> ${model}: v4 preflight"
    preflight_artifact="${model_dir}/preflight/run"
    set +e
    SHIM_BASE_URL="${shim_base_url}" \
      SHIM_AUTH_HEADER="${auth_header}" \
      V4_PREFLIGHT_MODEL="${model}" \
      V4_PREFLIGHT_PROVIDER_MODEL="${model}" \
      V4_PREFLIGHT_RUN_PROVIDER_ROUTING=0 \
      V4_PREFLIGHT_RUN_CODEX_DOCTOR="${run_codex_doctor}" \
      V4_PREFLIGHT_REQUIRE_READYZ="${require_readyz}" \
      V4_PREFLIGHT_TIMEOUT_SECONDS="${timeout_seconds}" \
      V4_PREFLIGHT_ARTIFACT_DIR="${model_dir}/preflight" \
      V4_PREFLIGHT_RUN_ID="run" \
      bash ./scripts/v4-preflight-smoke.sh \
        >"${model_dir}/preflight.stdout" \
        2>"${model_dir}/preflight.stderr"
    preflight_exit=$?
    set -e
    preflight_summary="${preflight_artifact}/summary.json"
    preflight_status="$(summary_status "${preflight_summary}")"
    codex_doctor_status="$(summary_nested_status "${preflight_summary}" '.statuses.codex_doctor')"
    if [[ "${preflight_exit}" -ne 0 && "${preflight_status}" == "missing" ]]; then
      preflight_status="failed"
    fi
  fi

  result="$(run_result "${run_routing}" "${routing_status}" "${run_preflight}" "${preflight_status}")"
  jq -nc \
    --arg model "${model}" \
    --arg slug "${slug}" \
    --arg result "${result}" \
    --arg routing_status "${routing_status}" \
    --arg routing_artifact "${routing_artifact}" \
    --arg routing_summary "${routing_summary}" \
    --argjson routing_exit "${routing_exit}" \
    --arg preflight_status "${preflight_status}" \
    --arg preflight_artifact "${preflight_artifact}" \
    --arg preflight_summary "${preflight_summary}" \
    --argjson preflight_exit "${preflight_exit}" \
    --arg codex_doctor_status "${codex_doctor_status}" \
    '{
      model:$model,
      slug:$slug,
      result:$result,
      routing:{status:$routing_status,exit_code:$routing_exit,artifact_dir:$routing_artifact,summary:$routing_summary},
      preflight:{status:$preflight_status,exit_code:$preflight_exit,artifact_dir:$preflight_artifact,summary:$preflight_summary,codex_doctor:$codex_doctor_status}
    }' >>"${rows_path}"
done

jq -s \
  --arg artifact_dir "${artifact_dir}" \
  --arg shim_base_url "${shim_base_url}" \
  --arg run_routing "${run_routing}" \
  --arg run_preflight "${run_preflight}" \
  --arg run_codex_doctor "${run_codex_doctor}" \
  '{
    object:"v4_provider_matrix_smoke.summary",
    status:(if all(.[]; .result == "passed") then "passed" else "failed" end),
    artifact_dir:$artifact_dir,
    shim_base_url:$shim_base_url,
    settings:{
      run_routing:$run_routing,
      run_preflight:$run_preflight,
      run_codex_doctor:$run_codex_doctor
    },
    totals:{
      models:length,
      passed:([.[] | select(.result == "passed")] | length),
      failed:([.[] | select(.result != "passed")] | length)
    },
    models:.
  }' "${rows_path}" >"${summary_json}"

{
  status="$(jq -r '.status' "${summary_json}")"
  echo "# V4 Provider Matrix Smoke"
  echo
  echo "- Status: ${status}"
  echo "- Shim: \`${shim_base_url}\`"
  echo "- Artifacts: \`${artifact_dir}\`"
  echo "- Routing smoke: \`${run_routing}\`"
  echo "- V4 preflight: \`${run_preflight}\`"
  echo "- Codex doctor: \`${run_codex_doctor}\`"
  echo
  echo "## Models"
  echo
  echo "| Model | Result | Routing | Preflight | Codex doctor | Artifacts |"
  echo "| --- | --- | --- | --- | --- | --- |"
  jq -r '.models[] |
    "| `\(.model)` | `\(.result)` | `\(.routing.status)` | `\(.preflight.status)` | `\(.preflight.codex_doctor)` | `models/\(.slug)` |"' \
    "${summary_json}"
  echo
  echo "## Notes"
  echo
  echo "- This smoke checks configured provider aliases and V4 operator surfaces."
  echo "- It is not a model-quality benchmark and does not replace \`make codex-eval-auto\`."
  echo "- Read nested \`summary.json\`, \`stderr\`, and \`shim.log.slice\` files under a model directory for diagnosis."
} >"${summary_md}"

jq '{status, totals, models:[.models[] | {model, result, routing:.routing.status, preflight:.preflight.status, codex_doctor:.preflight.codex_doctor}]}' "${summary_json}"
echo "v4 provider matrix smoke: ${artifact_dir}"

if [[ "$(jq -r '.status' "${summary_json}")" != "passed" ]]; then
  exit 1
fi
