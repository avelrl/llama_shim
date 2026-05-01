#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  GW_API_KEY=sk-... \
  CODEX_EVAL_MODELS="deepseek-v4-pro,kimi-k2,Qwen3.6-35B-A3B" \
  ./scripts/codex-eval-loop.sh

Common optional knobs:
  CODEX_EVAL_LOOP_OUT=.tmp/codex-eval-loops/<loop-id>
  CODEX_EVAL_LOOP_STRICT_REAL_UPSTREAM=false
  CODEX_EVAL_CONTROL_SHIM_BASE_URL=http://127.0.0.1:18080
  CODEX_EVAL_CONTROL_MODEL=devstack-model
  CODEX_EVAL_CONTROL_SUITE=codex-core
  CODEX_EVAL_CANDIDATE_SUITE=codex-real-upstream
  CODEX_EVAL_ATTEMPTS=2
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
loop_out="${CODEX_EVAL_LOOP_OUT:-.tmp/codex-eval-loops/loop-${timestamp}}"
control_dir="${loop_out}/control"
matrix_out="${loop_out}/matrix.md"
compare_out="${loop_out}/compare.md"
summary_out="${loop_out}/summary.json"
bundle_dir="${loop_out}/failure-bundles"
bundle_out="${loop_out}/failure-bundle.md"
run_errors="${loop_out}/run-errors.md"

control_base_url="${CODEX_EVAL_CONTROL_SHIM_BASE_URL:-http://127.0.0.1:18080}"
control_model="${CODEX_EVAL_CONTROL_MODEL:-devstack-model}"
control_provider="${CODEX_EVAL_CONTROL_PROVIDER:-gateway-shim}"
control_suite="${CODEX_EVAL_CONTROL_SUITE:-codex-core}"
control_api_key_env="${CODEX_EVAL_CONTROL_API_KEY_ENV:-OPENAI_API_KEY}"
control_api_key="${CODEX_EVAL_CONTROL_API_KEY:-shim-dev-key}"

candidate_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
candidate_provider="${CODEX_PROVIDER:-gateway-shim}"
candidate_suite="${CODEX_EVAL_CANDIDATE_SUITE:-codex-real-upstream}"
candidate_api_key_env="${CODEX_API_KEY_ENV:-OPENAI_API_KEY}"
candidate_api_key="${CODEX_API_KEY:-}"
if [[ -z "${candidate_api_key}" && -n "${candidate_api_key_env}" ]]; then
  candidate_api_key="${!candidate_api_key_env:-}"
fi

models_raw="${CODEX_EVAL_MODELS:-${CODEX_MODEL:-}}"
models_raw="${models_raw//,/ }"
models=()
for model in ${models_raw}; do
  [[ -n "${model}" ]] && models+=("${model}")
done

if [[ ${#models[@]} -eq 0 ]]; then
  echo "codex eval loop failed: CODEX_EVAL_MODELS or CODEX_MODEL is required" >&2
  usage >&2
  exit 2
fi

mkdir -p "${loop_out}" "${bundle_dir}"

run_eval() {
  local out_dir="$1"
  local suite="$2"
  local model="$3"
  local provider="$4"
  local base_url="$5"
  local api_key_env="$6"
  local api_key="$7"
  local provider_base_url="${base_url%/}/v1"

  env \
    SHIM_BASE_URL="${base_url}" \
    CODEX_BASE_URL="${provider_base_url}" \
    CODEX_MODEL="${model}" \
    CODEX_PROVIDER="${provider}" \
    CODEX_API_KEY_ENV="${api_key_env}" \
    CODEX_API_KEY="${api_key}" \
    "${api_key_env}=${api_key}" \
    CODEX_EVAL_SUITE="${suite}" \
    CODEX_EVAL_OUT="${out_dir}" \
    bash ./scripts/codex-eval-runner.sh
}

slugify_model() {
  local value="$1"
  value="$(printf '%s' "${value}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9._-' '-')"
  value="${value#-}"
  value="${value%-}"
  if [[ -z "${value}" ]]; then
    value="model"
  fi
  printf '%s' "${value}"
}

echo "==> codex eval loop: control ${control_model} (${control_suite})"
run_eval "${control_dir}" "${control_suite}" "${control_model}" "${control_provider}" "${control_base_url}" "${control_api_key_env}" "${control_api_key}"

candidate_dirs=()
candidate_failed=0
: > "${run_errors}"
for model in "${models[@]}"; do
  slug="$(slugify_model "${model}")"
  candidate_dir="${loop_out}/candidate-${slug}"
  candidate_dirs+=("${candidate_dir}")
  echo "==> codex eval loop: candidate ${model} (${candidate_suite})"
  set +e
  run_eval "${candidate_dir}" "${candidate_suite}" "${model}" "${candidate_provider}" "${candidate_base_url}" "${candidate_api_key_env}" "${candidate_api_key}"
  status=$?
  set -e
  if [[ ${status} -ne 0 ]]; then
    candidate_failed=1
    {
      echo "- \`${model}\`: runner exit ${status}"
      if [[ ! -f "${candidate_dir}/summary.json" ]]; then
        echo "  - no summary.json was produced"
      fi
    } >> "${run_errors}"
  fi
done

summary_dirs=()
for dir in "${control_dir}" "${candidate_dirs[@]}"; do
  if [[ -f "${dir}/summary.json" ]]; then
    summary_dirs+=("${dir}")
  fi
done

echo "==> codex eval loop: matrix"
go run ./cmd/codex-eval-runner matrix --out "${matrix_out}" "${summary_dirs[@]}"

candidate_summary_dirs=()
for dir in "${candidate_dirs[@]}"; do
  if [[ -f "${dir}/summary.json" ]]; then
    candidate_summary_dirs+=("${dir}")
  fi
done

if [[ ${#candidate_summary_dirs[@]} -eq 0 ]]; then
  echo "codex eval loop failed: no candidate summary.json files were produced" >&2
  exit 1
fi

echo "==> codex eval loop: compare"
go run ./cmd/codex-eval-runner compare \
  --control "${control_dir}" \
  --out "${compare_out}" \
  --json-out "${summary_out}" \
  "${candidate_summary_dirs[@]}"

{
  echo "# Codex Eval Failure Bundle"
  echo
} > "${bundle_out}"
bundle_count=0
for dir in "${summary_dirs[@]}"; do
  run_id="$(basename "${dir}")"
  out="${bundle_dir}/${run_id}.md"
  set +e
  go run ./cmd/codex-eval-runner failure-bundle --out "${out}" "${dir}" >/dev/null 2>/dev/null
  status=$?
  set -e
  if [[ ${status} -eq 0 ]]; then
    cat "${out}" >> "${bundle_out}"
    printf '\n' >> "${bundle_out}"
    bundle_count=$((bundle_count + 1))
  else
    rm -f "${out}"
  fi
done
if [[ ${bundle_count} -eq 0 ]]; then
  {
    echo "No failed tasks were found in this loop."
    echo
  } >> "${bundle_out}"
fi

if [[ -s "${run_errors}" ]]; then
  {
    echo
    echo "## Runner Errors"
    echo
    cat "${run_errors}"
  } >> "${bundle_out}"
else
  rm -f "${run_errors}"
fi

echo "codex eval loop: ${loop_out}"

strict="${CODEX_EVAL_LOOP_STRICT_REAL_UPSTREAM:-false}"
strict="$(printf '%s' "${strict}" | tr '[:upper:]' '[:lower:]')"
if [[ "${strict}" == "1" || "${strict}" == "true" || "${strict}" == "yes" || "${strict}" == "on" ]]; then
  exit "${candidate_failed}"
fi

exit 0
