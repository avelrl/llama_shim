#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/v4-provider-matrix-curate.sh [artifact-root-or-summary ...]

Environment:
  V4_PROVIDER_MATRIX_CURATE_OUT_DIR   Default: .tmp/v4-provider-matrix-curation/curation-<timestamp>
  V4_PROVIDER_MATRIX_CURATE_LIMIT     Default: 20
  V4_PROVIDER_MATRIX_CURATE_MODEL     Optional exact model filter.

If no artifact roots are passed, the script scans .tmp/v4-provider-matrix-smoke.
The report is local-only: it reads existing smoke artifacts and does not call
upstream providers.
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "missing required command: ${cmd}" >&2
    exit 127
  fi
}

is_json_object() {
  local path="$1"
  [[ -s "${path}" ]] && jq -e 'type == "object"' "${path}" >/dev/null 2>&1
}

json_file_or_empty_object() {
  local path="$1"
  local filter="$2"
  if is_json_object "${path}"; then
    jq -c "${filter}" "${path}" 2>/dev/null || printf '{}'
  else
    printf '{}'
  fi
}

json_file_or_empty_array() {
  local path="$1"
  local filter="$2"
  if is_json_object "${path}"; then
    jq -c "${filter}" "${path}" 2>/dev/null || printf '[]'
  else
    printf '[]'
  fi
}

collect_candidate_summaries() {
  local target="$1"
  if [[ -f "${target}" ]]; then
    printf '%s\n' "${target}" >>"${candidate_paths}"
    return
  fi
  if [[ ! -d "${target}" ]]; then
    return
  fi
  if [[ -f "${target%/}/summary.json" ]]; then
    printf '%s\n' "${target%/}/summary.json" >>"${candidate_paths}"
  fi
  find "${target}" -type f -name summary.json >>"${candidate_paths}" 2>/dev/null || true
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd date
require_cmd find
require_cmd head
require_cmd jq
require_cmd mkdir
require_cmd sed
require_cmd sort

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
out_dir="${V4_PROVIDER_MATRIX_CURATE_OUT_DIR:-.tmp/v4-provider-matrix-curation/curation-${timestamp}}"
limit="${V4_PROVIDER_MATRIX_CURATE_LIMIT:-20}"
model_filter="${V4_PROVIDER_MATRIX_CURATE_MODEL:-}"

if ! [[ "${limit}" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid V4_PROVIDER_MATRIX_CURATE_LIMIT: ${limit}" >&2
  exit 2
fi

mkdir -p "${out_dir}"
candidate_paths="${out_dir}/candidate-paths.txt"
selected_paths="${out_dir}/selected-paths.txt"
runs_jsonl="${out_dir}/runs.jsonl"
rows_jsonl="${out_dir}/rows.jsonl"
summary_json="${out_dir}/summary.json"
summary_md="${out_dir}/summary.md"

: >"${candidate_paths}"
: >"${selected_paths}"
: >"${runs_jsonl}"
: >"${rows_jsonl}"

if [[ $# -gt 0 ]]; then
  for target in "$@"; do
    collect_candidate_summaries "${target}"
  done
else
  collect_candidate_summaries ".tmp/v4-provider-matrix-smoke"
fi

sort -ru "${candidate_paths}" | head -n "${limit}" >"${selected_paths}"

valid_count=0
while IFS= read -r summary_path; do
  [[ -n "${summary_path}" ]] || continue
  if ! is_json_object "${summary_path}"; then
    continue
  fi
  if ! jq -e '.object == "v4_provider_matrix_smoke.summary"' "${summary_path}" >/dev/null 2>&1; then
    continue
  fi

  valid_count=$((valid_count + 1))
  run_index="${valid_count}"
  run_status="$(jq -r '.status // "unknown"' "${summary_path}")"
  run_artifact="$(jq -r '.artifact_dir // ""' "${summary_path}")"
  shim_base_url="$(jq -r '.shim_base_url // ""' "${summary_path}")"
  run_totals="$(jq -c '.totals // {}' "${summary_path}")"
  run_settings="$(jq -c '.settings // {}' "${summary_path}")"

  jq -nc \
    --argjson run_index "${run_index}" \
    --arg source "${summary_path}" \
    --arg status "${run_status}" \
    --arg artifact_dir "${run_artifact}" \
    --arg shim_base_url "${shim_base_url}" \
    --argjson totals "${run_totals}" \
    --argjson settings "${run_settings}" \
    '{
      run_index:$run_index,
      source:$source,
      status:$status,
      artifact_dir:$artifact_dir,
      shim_base_url:$shim_base_url,
      totals:$totals,
      settings:$settings
    }' >>"${runs_jsonl}"

  jq -c '.models[]?' "${summary_path}" | while IFS= read -r row; do
    row_model="$(jq -r '.model // ""' <<<"${row}")"
    if [[ -n "${model_filter}" && "${row_model}" != "${model_filter}" ]]; then
      continue
    fi

    row_result="$(jq -r '.result // "unknown"' <<<"${row}")"
    row_slug="$(jq -r '.slug // ""' <<<"${row}")"
    routing_status="$(jq -r '.routing.status // "missing"' <<<"${row}")"
    routing_exit="$(jq -r '.routing.exit_code // 0' <<<"${row}")"
    routing_artifact="$(jq -r '.routing.artifact_dir // ""' <<<"${row}")"
    routing_summary="$(jq -r '.routing.summary // ""' <<<"${row}")"
    preflight_status="$(jq -r '.preflight.status // "missing"' <<<"${row}")"
    preflight_exit="$(jq -r '.preflight.exit_code // 0' <<<"${row}")"
    preflight_artifact="$(jq -r '.preflight.artifact_dir // ""' <<<"${row}")"
    preflight_summary="$(jq -r '.preflight.summary // ""' <<<"${row}")"
    codex_doctor_status="$(jq -r '.preflight.codex_doctor // "skipped"' <<<"${row}")"

    routing_statuses="$(json_file_or_empty_object "${routing_summary}" '.statuses // {}')"
    routing_warnings="$(json_file_or_empty_array "${routing_summary}" '.warnings // []')"
    preflight_statuses="$(json_file_or_empty_object "${preflight_summary}" '.statuses // {}')"
    preflight_warnings="$(json_file_or_empty_array "${preflight_summary}" '.warnings // []')"
    preflight_failures="$(json_file_or_empty_array "${preflight_summary}" '.failures // []')"

    jq -nc \
      --argjson run_index "${run_index}" \
      --arg run_source "${summary_path}" \
      --arg run_status "${run_status}" \
      --arg run_artifact "${run_artifact}" \
      --arg model "${row_model}" \
      --arg slug "${row_slug}" \
      --arg result "${row_result}" \
      --arg routing_status "${routing_status}" \
      --argjson routing_exit "${routing_exit}" \
      --arg routing_artifact "${routing_artifact}" \
      --arg routing_summary "${routing_summary}" \
      --argjson routing_statuses "${routing_statuses}" \
      --argjson routing_warnings "${routing_warnings}" \
      --arg preflight_status "${preflight_status}" \
      --argjson preflight_exit "${preflight_exit}" \
      --arg preflight_artifact "${preflight_artifact}" \
      --arg preflight_summary "${preflight_summary}" \
      --argjson preflight_statuses "${preflight_statuses}" \
      --argjson preflight_warnings "${preflight_warnings}" \
      --argjson preflight_failures "${preflight_failures}" \
      --arg codex_doctor_status "${codex_doctor_status}" \
      'def bad_status($s): (($s // "") | test("^(000|missing|5[0-9][0-9])$"));
       def non2xx($s): (($s // "") | test("^2[0-9][0-9]$") | not);
       def category:
         if $result == "passed" and $routing_status == "passed" and $preflight_status == "passed" then
           "release_gate_ok"
         elif $result == "passed" then
           "partial_gate_ok"
         elif $codex_doctor_status == "failed" then
           "codex_doctor_failure"
         elif $routing_status != "passed" and $routing_status != "skipped" then
           if bad_status($routing_statuses.readyz) then
             "readiness_or_provider_unavailable"
           elif bad_status($routing_statuses.responses) or bad_status($routing_statuses.chat_completions) then
             "upstream_transport_or_provider"
           elif non2xx($routing_statuses.models) then
             "provider_model_catalog_failure"
           else
             "routing_failure"
           end
         elif $preflight_status != "passed" and $preflight_status != "skipped" then
           if bad_status($preflight_statuses.readyz) then
             "readiness_or_provider_unavailable"
           elif non2xx($preflight_statuses.capabilities) then
             "v4_capability_contract_failure"
           elif bad_status($preflight_statuses.responses_trace) then
             "upstream_transport_or_provider"
           elif non2xx($preflight_statuses.responses_trace_debug_trace) then
             "debug_trace_or_observability_failure"
           else
             "preflight_failure"
           end
         else
           "unknown_failure"
         end;
       def recommendation($c):
         if $c == "release_gate_ok" then "release-gate ok"
         elif $c == "partial_gate_ok" then "partial smoke ok; run full routing+preflight before promotion"
         elif $c == "readiness_or_provider_unavailable" then "rerun after checking /readyz, provider health, auth, and upstream availability"
         elif $c == "upstream_transport_or_provider" then "rerun candidate after provider/network stabilizes; inspect nested stderr and shim.log.slice"
         elif $c == "provider_model_catalog_failure" then "check provider alias, token env, and live /v1/models catalog"
         elif $c == "v4_capability_contract_failure" then "fix backend/plugin capability contract before model-quality testing"
         elif $c == "debug_trace_or_observability_failure" then "check debug trace config, request id capture, and trace eviction"
         elif $c == "codex_doctor_failure" then "fix Codex config/auth/model metadata wiring"
         else "inspect nested artifacts"
         end;
       category as $category |
       {
         run_index:$run_index,
         run_source:$run_source,
         run_status:$run_status,
         run_artifact:$run_artifact,
         model:$model,
         slug:$slug,
         result:$result,
         category:$category,
         recommendation:recommendation($category),
         routing:{
           status:$routing_status,
           exit_code:$routing_exit,
           artifact_dir:$routing_artifact,
           summary:$routing_summary,
           statuses:$routing_statuses,
           warnings:$routing_warnings
         },
         preflight:{
           status:$preflight_status,
           exit_code:$preflight_exit,
           artifact_dir:$preflight_artifact,
           summary:$preflight_summary,
           statuses:$preflight_statuses,
           codex_doctor:$codex_doctor_status,
           warnings:$preflight_warnings,
           failures:$preflight_failures
         }
       }' >>"${rows_jsonl}"
  done
done <"${selected_paths}"

if [[ "${valid_count}" -eq 0 ]]; then
  jq -n \
    --arg out_dir "${out_dir}" \
    '{object:"v4_provider_matrix_curation.summary",status:"failed",out_dir:$out_dir,error:"no v4 provider matrix smoke summaries found"}' \
    >"${summary_json}"
  {
    echo "# V4 Provider Matrix Curation"
    echo
    echo "- Status: failed"
    echo "- Reason: no V4 provider matrix smoke summaries found"
    echo "- Artifacts: \`${out_dir}\`"
  } >"${summary_md}"
  echo "v4 provider matrix curation failed: no summaries found" >&2
  echo "v4 provider matrix curation: ${out_dir}"
  exit 1
fi

jq -s \
  --slurpfile runs "${runs_jsonl}" \
  --arg out_dir "${out_dir}" \
  --arg model_filter "${model_filter}" \
  'def latest_rows:
     (if length == 0 then [] else
       (.[0].run_index as $latest | [.[] | select(.run_index == $latest)])
      end);
   def latest_status:
     (latest_rows as $rows |
       if ($rows | length) == 0 then "failed"
       elif all($rows[]; (.category == "release_gate_ok" or .category == "partial_gate_ok")) then "passed"
       else "failed"
       end);
   def latest_verdict:
     (latest_rows as $rows |
       if ($rows | length) == 0 then "no_rows"
       elif all($rows[]; .category == "release_gate_ok") then "release_gate_ok"
       elif all($rows[]; (.category == "release_gate_ok" or .category == "partial_gate_ok")) then "partial_gate_ok"
       elif all($rows[]; (.category == "release_gate_ok" or .category == "partial_gate_ok" or .category == "readiness_or_provider_unavailable" or .category == "upstream_transport_or_provider")) then "rerun_candidate"
       else "needs_attention"
       end);
   {
     object:"v4_provider_matrix_curation.summary",
     status:latest_status,
     verdict:latest_verdict,
     out_dir:$out_dir,
     model_filter:$model_filter,
     totals:{
       runs:($runs | length),
       rows:length,
       latest_rows:(latest_rows | length),
       latest_passed:([latest_rows[] | select(.category == "release_gate_ok" or .category == "partial_gate_ok")] | length),
       latest_failed:([latest_rows[] | select(.category != "release_gate_ok" and .category != "partial_gate_ok")] | length)
     },
     groups:{
       passed_rows:[.[] | select(.category == "release_gate_ok" or .category == "partial_gate_ok") | {run_index,model,category,run_artifact}],
       failed_routing:[.[] | select(.category == "routing_failure" or .category == "provider_model_catalog_failure") | {run_index,model,category,recommendation,routing}],
       failed_preflight:[.[] | select(.category == "preflight_failure" or .category == "v4_capability_contract_failure" or .category == "debug_trace_or_observability_failure") | {run_index,model,category,recommendation,preflight}],
       failed_codex_doctor:[.[] | select(.category == "codex_doctor_failure") | {run_index,model,category,recommendation,preflight}],
       readiness_or_transport:[.[] | select(.category == "readiness_or_provider_unavailable" or .category == "upstream_transport_or_provider") | {run_index,model,category,recommendation,routing,preflight}],
       unknown:[.[] | select(.category == "unknown_failure") | {run_index,model,category,recommendation,routing,preflight}]
     },
     runs:$runs,
     latest_run:($runs[0] // null),
     latest_rows:latest_rows,
     rows:.
   }' "${rows_jsonl}" >"${summary_json}"

{
  status="$(jq -r '.status' "${summary_json}")"
  verdict="$(jq -r '.verdict' "${summary_json}")"
  latest_source="$(jq -r '.latest_run.source // "missing"' "${summary_json}")"
  latest_artifact="$(jq -r '.latest_run.artifact_dir // "missing"' "${summary_json}")"
  echo "# V4 Provider Matrix Curation"
  echo
  echo "- Status: ${status}"
  echo "- Verdict: \`${verdict}\`"
  echo "- Latest source: \`${latest_source}\`"
  echo "- Latest artifacts: \`${latest_artifact}\`"
  echo "- Curation artifacts: \`${out_dir}\`"
  if [[ -n "${model_filter}" ]]; then
    echo "- Model filter: \`${model_filter}\`"
  fi
  echo
  echo "## Latest Rows"
  echo
  echo "| Model | Category | Routing | Preflight | Codex doctor | Recommendation |"
  echo "| --- | --- | --- | --- | --- | --- |"
  jq -r '.latest_rows[] |
    "| `\(.model)` | `\(.category)` | `\(.routing.status)` | `\(.preflight.status)` | `\(.preflight.codex_doctor)` | \(.recommendation) |"' \
    "${summary_json}"
  echo
  echo "## Failure Groups"
  echo
  echo "- routing: $(jq -r '(.groups.failed_routing // []) | length' "${summary_json}")"
  echo "- preflight: $(jq -r '(.groups.failed_preflight // []) | length' "${summary_json}")"
  echo "- codex doctor: $(jq -r '(.groups.failed_codex_doctor // []) | length' "${summary_json}")"
  echo "- readiness/transport: $(jq -r '(.groups.readiness_or_transport // []) | length' "${summary_json}")"
  echo "- unknown: $(jq -r '(.groups.unknown // []) | length' "${summary_json}")"
  echo
  echo "## Runs"
  echo
  echo "| Run | Status | Models | Passed | Failed | Source |"
  echo "| --- | --- | --- | --- | --- | --- |"
  jq -r '.runs[] |
    "| \(.run_index) | `\(.status)` | \(.totals.models // 0) | \(.totals.passed // 0) | \(.totals.failed // 0) | `\(.source)` |"' \
    "${summary_json}"
  echo
  echo "## Notes"
  echo
  echo "- This report curates existing V4 provider matrix smoke artifacts only."
  echo "- It does not call upstream providers and does not replace \`make codex-eval-auto\`."
  echo "- Use nested \`routing.summary\`, \`preflight.summary\`, stderr files, and \`shim.log.slice\` for root-cause diagnosis."
} >"${summary_md}"

jq '{status, verdict, totals, latest_rows:[.latest_rows[] | {model, category, recommendation}]}' "${summary_json}"
echo "v4 provider matrix curation report: ${summary_md}"
echo "v4 provider matrix curation summary: ${summary_json}"
echo "v4 provider matrix curation: ${out_dir}"

if [[ "$(jq -r '.status' "${summary_json}")" != "passed" ]]; then
  exit 1
fi
