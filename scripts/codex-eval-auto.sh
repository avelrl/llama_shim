#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  GW_API_KEY=sk-... \
  CODEX_EVAL_MODELS="deepseek/deepseek-v4-pro" \
  bash ./scripts/codex-eval-auto.sh

Runs the configured model(s) through a full local automation pass:
  baseline   = codex-core control vs codex-real-upstream candidate
  expanded   = codex-core control vs codex-real-upstream-expanded candidate
  bench-lite = codex-bench-lite control vs codex-bench-lite candidate

Common optional knobs:
  CODEX_EVAL_AUTO_OUT=.tmp/codex-eval-auto/<auto-id>
  CODEX_EVAL_AUTO_PROFILES=baseline,expanded,bench-lite
  CODEX_EVAL_AUTO_STRICT=baseline  # none, baseline, or all
  CODEX_EVAL_NOTIFY=bell           # off, bell, or macos
  CODEX_EVAL_SHIM_LOG=.data/shim.log
  CODEX_EVAL_ATTEMPTS=2

The wrapper writes:
  <auto-out>/summary.md
  <auto-out>/summary.json
  <auto-out>/profiles/<profile>/compare.md
  <auto-out>/profiles/<profile>/summary.json
  <auto-out>/profiles/<profile>/shim.log.slice
  <auto-out>/profiles/<profile>/shim-log-diagnostics.md
EOF
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

shim_log_size() {
  local log_path="$1"
  if [[ ! -f "${log_path}" ]]; then
    printf '0'
    return
  fi
  wc -c < "${log_path}" | tr -d '[:space:]'
}

capture_shim_log() {
  local log_path="$1"
  local start_bytes="$2"
  local out_dir="$3"
  local slice="${out_dir}/shim.log.slice"
  local diagnostics="${out_dir}/shim-log-diagnostics.md"
  local matches="${out_dir}/shim-log-diagnostics.matches"

  if [[ ! -f "${log_path}" ]]; then
    {
      echo "# Shim Log Diagnostics"
      echo
      echo "Shim log was not found at \`${log_path}\`."
    } > "${diagnostics}"
    : > "${slice}"
    return
  fi

  local end_bytes
  end_bytes="$(shim_log_size "${log_path}")"
  if [[ "${end_bytes}" =~ ^[0-9]+$ && "${start_bytes}" =~ ^[0-9]+$ && "${end_bytes}" -ge "${start_bytes}" ]]; then
    if [[ "${start_bytes}" -eq 0 ]]; then
      cp "${log_path}" "${slice}"
    else
      tail -c +"$((start_bytes + 1))" "${log_path}" > "${slice}"
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
  } > "${diagnostics}"

  if grep -nE '"level":"(WARN|ERROR)"|"status":5[0-9][0-9]|turn\.failed|response\.failed|failed_raw_tool_markup|raw_tool_markup|upstream request failed|unexpected status|panic' "${slice}" > "${matches}"; then
    {
      echo "## High-Signal Matches"
      echo
      echo '```text'
      cat "${matches}"
      echo '```'
    } >> "${diagnostics}"
  else
    {
      echo "## High-Signal Matches"
      echo
      echo "No high-signal diagnostics matched."
    } >> "${diagnostics}"
  fi
  rm -f "${matches}"
}

profile_config() {
  local profile="$1"
  case "${profile}" in
    baseline)
      printf '%s %s' "codex-core" "codex-real-upstream"
      ;;
    expanded)
      printf '%s %s' "codex-core" "codex-real-upstream-expanded"
      ;;
    bench-lite)
      printf '%s %s' "codex-bench-lite" "codex-bench-lite"
      ;;
    *)
      echo "codex eval auto failed: unknown profile ${profile}" >&2
      exit 2
      ;;
  esac
}

strict_policy() {
  local strict="$1"
  strict="$(printf '%s' "${strict}" | tr '[:upper:]' '[:lower:]')"
  case "${strict}" in
    ""|0|false|no|off|none)
      printf 'none'
      ;;
    1|true|yes|on|all)
      printf 'all'
      ;;
    baseline)
      printf 'baseline'
      ;;
    *)
      printf 'baseline'
      ;;
  esac
}

send_notification() {
  local mode="$1"
  local status="$2"
  local out_dir="$3"
  mode="$(printf '%s' "${mode}" | tr '[:upper:]' '[:lower:]')"
  case "${mode}" in
    ""|off|none|0|false|no)
      return
      ;;
    bell|1|true|yes|on)
      printf '\a'
      ;;
    macos)
      printf '\a'
      if command -v osascript >/dev/null 2>&1; then
        osascript -e "display notification \"${status}: ${out_dir}\" with title \"Codex eval auto\"" >/dev/null 2>&1 || true
      fi
      ;;
    *)
      printf '\a'
      ;;
  esac
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"

models_raw="${CODEX_EVAL_MODELS:-${CODEX_MODEL:-}}"
models=()
for model in $(csv_to_words "${models_raw}"); do
  [[ -n "${model}" ]] && models+=("${model}")
done
if [[ ${#models[@]} -eq 0 ]]; then
  echo "codex eval auto failed: CODEX_EVAL_MODELS or CODEX_MODEL is required" >&2
  usage >&2
  exit 2
fi

profiles=()
for profile in $(csv_to_words "${CODEX_EVAL_AUTO_PROFILES:-baseline,expanded,bench-lite}"); do
  [[ -n "${profile}" ]] && profiles+=("${profile}")
done
if [[ ${#profiles[@]} -eq 0 ]]; then
  echo "codex eval auto failed: CODEX_EVAL_AUTO_PROFILES resolved to no profiles" >&2
  exit 2
fi

if [[ -n "${CODEX_EVAL_AUTO_OUT:-}" ]]; then
  auto_out="${CODEX_EVAL_AUTO_OUT}"
elif [[ ${#models[@]} -eq 1 ]]; then
  auto_out=".tmp/codex-eval-auto/$(slugify "${models[0]}")_full_${timestamp}"
else
  auto_out=".tmp/codex-eval-auto/multi_full_${timestamp}"
fi

strict="$(strict_policy "${CODEX_EVAL_AUTO_STRICT:-baseline}")"
notify="${CODEX_EVAL_NOTIFY:-bell}"
shim_log="${CODEX_EVAL_SHIM_LOG:-.data/shim.log}"
profiles_dir="${auto_out}/profiles"
run_errors="${auto_out}/run-errors.md"

mkdir -p "${profiles_dir}"
: > "${run_errors}"

profile_dirs=()
any_profile_failed=0
baseline_profile_failed=0
codex_core_control_run=""
codex_bench_lite_control_run=""

for profile in "${profiles[@]}"; do
  read -r control_suite candidate_suite <<< "$(profile_config "${profile}")"
  profile_dir="${profiles_dir}/${profile}"
  mkdir -p "${profile_dir}"
  profile_dirs+=("${profile_dir}")

  echo "==> codex eval auto: ${profile} (${control_suite} -> ${candidate_suite})"
  control_reuse_path=""
  case "${control_suite}" in
    codex-core)
      if [[ -n "${codex_core_control_run}" ]]; then
        control_reuse_path="${codex_core_control_run}"
      fi
      ;;
    codex-bench-lite)
      if [[ -n "${codex_bench_lite_control_run}" ]]; then
        control_reuse_path="${codex_bench_lite_control_run}"
      fi
      ;;
  esac
  log_start="$(shim_log_size "${shim_log}")"
  set +e
  if [[ -n "${control_reuse_path}" ]]; then
    (
      CODEX_EVAL_LOOP_OUT="${profile_dir}" \
      CODEX_EVAL_CONTROL_SUITE="${control_suite}" \
      CODEX_EVAL_CANDIDATE_SUITE="${candidate_suite}" \
      CODEX_EVAL_LOOP_STRICT_REAL_UPSTREAM=false \
      CODEX_EVAL_CONTROL_RUN="${control_reuse_path}" \
      bash ./scripts/codex-eval-loop.sh
    ) 2>&1 | tee "${profile_dir}/loop.log"
    status=${PIPESTATUS[0]}
  else
    (
      CODEX_EVAL_LOOP_OUT="${profile_dir}" \
      CODEX_EVAL_CONTROL_SUITE="${control_suite}" \
      CODEX_EVAL_CANDIDATE_SUITE="${candidate_suite}" \
      CODEX_EVAL_LOOP_STRICT_REAL_UPSTREAM=false \
      bash ./scripts/codex-eval-loop.sh
    ) 2>&1 | tee "${profile_dir}/loop.log"
    status=${PIPESTATUS[0]}
  fi
  set -e
  printf '%s\n' "${status}" > "${profile_dir}/runner-exit-code.txt"
  capture_shim_log "${shim_log}" "${log_start}" "${profile_dir}"
  if [[ ${status} -eq 0 && -z "${control_reuse_path}" && -f "${profile_dir}/control/summary.json" ]]; then
    case "${control_suite}" in
      codex-core)
        codex_core_control_run="${profile_dir}/control"
        ;;
      codex-bench-lite)
        codex_bench_lite_control_run="${profile_dir}/control"
        ;;
    esac
  fi

  if [[ ${status} -ne 0 ]]; then
    any_profile_failed=1
    [[ "${profile}" == "baseline" ]] && baseline_profile_failed=1
    {
      echo "- \`${profile}\`: loop exited with status ${status}"
      if [[ ! -f "${profile_dir}/summary.json" ]]; then
        echo "  - no compare summary was produced"
      fi
    } >> "${run_errors}"
  fi
done

completed_profile_dirs=()
for dir in "${profile_dirs[@]}"; do
  if [[ -f "${dir}/summary.json" ]]; then
    completed_profile_dirs+=("${dir}")
  fi
done

if [[ ${#completed_profile_dirs[@]} -eq 0 ]]; then
  {
    echo "# Codex Eval Auto Report"
    echo
    echo "- Generated: \`$(date -u +%Y-%m-%dT%H:%M:%SZ)\`"
    echo "- Status: \`failed\`"
    echo
    echo "No profile produced a compare summary."
    echo
    echo "## Runner Errors"
    echo
    cat "${run_errors}"
  } > "${auto_out}/summary.md"
  printf '{\n  "status": "failed",\n  "profiles": []\n}\n' > "${auto_out}/summary.json"
else
  set +e
  go run ./cmd/codex-eval-runner auto-report \
    --out "${auto_out}/summary.md" \
    --json-out "${auto_out}/summary.json" \
    --strict none \
    "${completed_profile_dirs[@]}"
  report_status=$?
  set -e
  if [[ ${report_status} -ne 0 ]]; then
    any_profile_failed=1
    {
      echo "- auto-report: exited with status ${report_status}"
    } >> "${run_errors}"
  fi
  if [[ -s "${run_errors}" ]]; then
    {
      echo
      echo "## Runner Errors"
      echo
      cat "${run_errors}"
    } >> "${auto_out}/summary.md"
  else
    rm -f "${run_errors}"
  fi
fi

echo "codex eval auto: ${auto_out}"

exit_code=0
case "${strict}" in
  all)
    if [[ ${any_profile_failed} -ne 0 ]]; then
      exit_code=1
    else
      set +e
      go run ./cmd/codex-eval-runner auto-report --strict all "${completed_profile_dirs[@]}" >/dev/null
      exit_code=$?
      set -e
    fi
    ;;
  baseline)
    if [[ ${baseline_profile_failed} -ne 0 ]]; then
      exit_code=1
    else
      set +e
      go run ./cmd/codex-eval-runner auto-report --strict baseline "${completed_profile_dirs[@]}" >/dev/null
      exit_code=$?
      set -e
    fi
    ;;
  none)
    exit_code=0
    ;;
esac

if [[ ${exit_code} -eq 0 ]]; then
  send_notification "${notify}" "passed" "${auto_out}"
else
  send_notification "${notify}" "failed" "${auto_out}"
fi
exit "${exit_code}"
