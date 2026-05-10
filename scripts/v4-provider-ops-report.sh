#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/v4-provider-ops-report.sh

Environment:
  V4_PROVIDER_OPS_OUT_DIR           Default: .tmp/v4-provider-ops/ops-<timestamp>
  V4_PROVIDER_OPS_MODEL             Optional exact provider/model alias filter.
  V4_PROVIDER_OPS_REQUIRE_CODEX     Default: 0. Require Codex curation evidence for pass.
  V4_PROVIDER_OPS_DOCTOR_SUMMARY    Optional explicit provider doctor summary.json.
  V4_PROVIDER_OPS_MATRIX_SUMMARY    Optional explicit provider matrix curation summary.json.
  V4_PROVIDER_OPS_CODEX_SUMMARY     Optional explicit Codex eval curation summary.json.

The report is local-only. It reads existing config doctor, provider matrix
curation, and Codex eval curation artifacts and does not call upstreams.
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "missing required command: ${cmd}" >&2
    exit 127
  fi
}

bool_enabled() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1 | true | yes | on) return 0 ;;
    *) return 1 ;;
  esac
}

latest_object_summary() {
  local root="$1"
  local object="$2"
  if [[ ! -d "${root}" ]]; then
    return 0
  fi
  find "${root}" -type f -name summary.json 2>/dev/null \
    | sort -r \
    | while IFS= read -r path; do
        if jq -e --arg object "${object}" '.object == $object' "${path}" >/dev/null 2>&1; then
          printf '%s\n' "${path}"
          break
        fi
      done
}

latest_json_summary() {
  local root="$1"
  if [[ ! -d "${root}" ]]; then
    return 0
  fi
  find "${root}" -type f -name summary.json 2>/dev/null | sort -r | head -n 1
}

json_or_missing() {
  local source="$1"
  local target="$2"
  local kind="$3"
  if [[ -n "${source}" && -s "${source}" ]] && jq -e 'type == "object"' "${source}" >/dev/null 2>&1; then
    cp "${source}" "${target}"
    return
  fi
  jq -n --arg kind "${kind}" '{status:"missing", kind:$kind}' >"${target}"
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd cp
require_cmd date
require_cmd find
require_cmd head
require_cmd jq
require_cmd mkdir
require_cmd sort
require_cmd tr

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
out_dir="${V4_PROVIDER_OPS_OUT_DIR:-.tmp/v4-provider-ops/ops-${timestamp}}"
model_filter="${V4_PROVIDER_OPS_MODEL:-}"
require_codex="${V4_PROVIDER_OPS_REQUIRE_CODEX:-0}"

mkdir -p "${out_dir}"

doctor_source="${V4_PROVIDER_OPS_DOCTOR_SUMMARY:-$(latest_object_summary ".tmp/v4-provider-config-doctor" "shimctl.provider.doctor")}"
matrix_source="${V4_PROVIDER_OPS_MATRIX_SUMMARY:-$(latest_object_summary ".tmp/v4-provider-matrix-curation" "v4_provider_matrix_curation.summary")}"
codex_source="${V4_PROVIDER_OPS_CODEX_SUMMARY:-$(latest_json_summary ".tmp/codex-eval-curation")}"

doctor_json="${out_dir}/provider-doctor-summary.json"
matrix_json="${out_dir}/provider-matrix-curation-summary.json"
codex_json="${out_dir}/codex-eval-curation-summary.json"
summary_json="${out_dir}/summary.json"
summary_md="${out_dir}/summary.md"

json_or_missing "${doctor_source}" "${doctor_json}" "provider_doctor"
json_or_missing "${matrix_source}" "${matrix_json}" "provider_matrix_curation"
json_or_missing "${codex_source}" "${codex_json}" "codex_eval_curation"

if ! jq -n \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg out_dir "${out_dir}" \
  --arg doctor_source "${doctor_source}" \
  --arg matrix_source "${matrix_source}" \
  --arg codex_source "${codex_source}" \
  --arg model_filter "${model_filter}" \
  --arg require_codex "${require_codex}" \
  --slurpfile doctor "${doctor_json}" \
  --slurpfile matrix "${matrix_json}" \
  --slurpfile codex "${codex_json}" \
  '
  def enabled($v): ($v | ascii_downcase) as $s | ($s == "1" or $s == "true" or $s == "yes" or $s == "on");
  def as_array($v): if ($v | type) == "array" then $v else [] end;
  def doctor: $doctor[0];
  def matrix: $matrix[0];
  def codex: $codex[0];
  def doctor_models: as_array(doctor.models);
  def matrix_rows: as_array(matrix.latest_rows);
  def codex_summaries: as_array(codex.model_summaries);
  def codex_key($row): (($row.canonical_model // $row.public_model // $row.model // "") | select(. != ""));
  def matrix_models:
    ((doctor.options.matrix_models // []) + [doctor_models[]?.public_model] + [matrix_rows[]?.model])
    | map(select(. != null and . != ""))
    | unique;
  def selected_models:
    if $model_filter != "" then [matrix_models[] | select(. == $model_filter)] else matrix_models end;
  def doctor_model($model): ([doctor_models[]? | select(.public_model == $model)] | first);
  def matrix_row($model): ([matrix_rows[]? | select(.model == $model)] | first);
  def codex_summary($model; $profile): ([codex_summaries[]? | select(codex_key(.) == $model and .profile == $profile)] | first);
  def issue_count($severity):
    ([doctor.issues[]? | select(.severity == $severity)] | length);
  def codex_status($model):
    (codex_summary($model; "baseline")) as $baseline |
    if ($baseline == null) then "not_checked"
    elif ($baseline.latest_interpretation == "promote_baseline_after_log_spot_check") then "strict_clean"
    elif ($baseline.latest_interpretation == "record_retry_dependent_baseline") then "retry_dependent"
    elif ($baseline.latest_status == "passed") then "passed_needs_review"
    else "attention"
    end;
  def codex_recommendation($model):
    (codex_summary($model; "baseline")) as $baseline |
    if ($baseline == null) then "run codex-eval-auto and codex-eval-curate before Codex promotion"
    elif ($baseline.latest_interpretation == "promote_baseline_after_log_spot_check") then "Codex baseline strict-clean; spot-check logs before promotion"
    elif ($baseline.latest_interpretation == "record_retry_dependent_baseline") then "Codex baseline passed with retries; record as retry-dependent"
    else "Codex baseline is not promotable; inspect curation failed tasks"
    end;
  def model_row($model):
    (doctor_model($model)) as $dm |
    (matrix_row($model)) as $mr |
    (codex_status($model)) as $cs |
    (codex_summary($model; "baseline")) as $baseline |
    (codex_summary($model; "expanded")) as $expanded |
    (codex_summary($model; "bench-lite")) as $bench |
    (if ((doctor.status // "missing") != "passed") then "doctor_attention"
     elif ($dm == null) then "missing_config"
     else "ok" end) as $config_status |
    (if ($mr == null) then "missing"
     else ($mr.category // "unknown") end) as $matrix_category |
    (if ($config_status != "ok") then "config_attention"
     elif ($matrix_category == "release_gate_ok" and $cs == "strict_clean") then "release_gate_strict_clean"
     elif ($matrix_category == "release_gate_ok" and $cs == "retry_dependent") then "release_gate_retry_dependent"
     elif ($matrix_category == "release_gate_ok" and enabled($require_codex) and ($cs == "not_checked" or $cs == "attention" or $cs == "passed_needs_review")) then "codex_attention"
     elif ($matrix_category == "release_gate_ok") then "release_gate_matrix_only"
     elif ($matrix_category == "partial_gate_ok") then "partial_gate"
     elif ($matrix_category == "missing") then "missing_matrix_evidence"
     elif enabled($require_codex) and ($cs == "not_checked" or $cs == "attention" or $cs == "passed_needs_review") then "codex_attention"
     else "needs_attention" end) as $decision |
    {
      model:$model,
      decision:$decision,
      release_gate:($decision | startswith("release_gate_")),
      config:{
        status:$config_status,
        provider_id:($dm.provider_id // ""),
        upstream_model:($dm.upstream_model // ""),
        has_codex_metadata:($dm.has_codex_metadata // false)
      },
      matrix:{
        category:$matrix_category,
        recommendation:($mr.recommendation // "run v4-provider-matrix-smoke and v4-provider-matrix-curate"),
        routing:($mr.routing.status // "missing"),
        preflight:($mr.preflight.status // "missing"),
        codex_doctor:($mr.preflight.codex_doctor // "missing"),
        run_artifact:($mr.run_artifact // "")
      },
      codex:{
        status:$cs,
        recommendation:codex_recommendation($model),
        baseline:{
          latest_status:($baseline.latest_status // "missing"),
          latest_interpretation:($baseline.latest_interpretation // "missing"),
          passed:($baseline.passed // 0),
          total:($baseline.total // 0),
          retry_passed_runs:($baseline.retry_passed_runs // 0),
          failed_runs:($baseline.failed_runs // 0),
          latest_source:($baseline.latest_source // "")
        },
        expanded:{
          latest_status:($expanded.latest_status // "missing"),
          latest_interpretation:($expanded.latest_interpretation // "missing")
        },
        bench_lite:{
          latest_status:($bench.latest_status // "missing"),
          latest_interpretation:($bench.latest_interpretation // "missing")
        }
      }
    };
  (selected_models | map(model_row(.))) as $rows |
  ([matrix_rows[]?.model] | unique) as $matrix_evidence_models |
  (matrix_models) as $configured_matrix_models |
  ([codex_summaries[]? | codex_key(.)] | unique) as $codex_evidence_models |
  {
    object:"v4_provider_ops.summary",
    status:(
      if ($rows | length) == 0 then "no_data"
      elif any($rows[]; (.decision == "config_attention" or .decision == "needs_attention" or .decision == "missing_matrix_evidence" or .decision == "codex_attention")) then "attention"
      else "passed"
      end
    ),
    verdict:(
      if ($rows | length) == 0 then "no_data"
      elif all($rows[]; .decision == "release_gate_strict_clean") then "release_gate_strict_clean"
      elif all($rows[]; (.decision | startswith("release_gate_"))) then "release_gate_ok_with_notes"
      elif all($rows[]; ((.decision | startswith("release_gate_")) or (.decision == "partial_gate"))) then "partial_gate_ok"
      else "needs_attention"
      end
    ),
    generated_at:$generated_at,
    artifact_dir:$out_dir,
    settings:{
      model_filter:$model_filter,
      require_codex:enabled($require_codex)
    },
    sources:{
      provider_doctor:$doctor_source,
      provider_matrix_curation:$matrix_source,
      codex_eval_curation:$codex_source
    },
    totals:{
      models:($rows | length),
      release_gate:([$rows[] | select(.release_gate)] | length),
      strict_clean:([$rows[] | select(.decision == "release_gate_strict_clean")] | length),
      retry_dependent:([$rows[] | select(.decision == "release_gate_retry_dependent")] | length),
      matrix_only:([$rows[] | select(.decision == "release_gate_matrix_only")] | length),
      partial_gate:([$rows[] | select(.decision == "partial_gate")] | length),
      attention:([$rows[] | select(.release_gate | not) | select(.decision != "partial_gate")] | length),
      doctor_errors:issue_count("error"),
      doctor_warnings:issue_count("warning")
    },
    drift:{
      configured_without_matrix_evidence:($configured_matrix_models - $matrix_evidence_models),
      matrix_evidence_without_config:($matrix_evidence_models - $configured_matrix_models),
      codex_evidence_without_config:($codex_evidence_models - $configured_matrix_models)
    },
    rows:$rows,
    recommendations:(
      (if ((doctor.status // "missing") != "passed") then ["fix provider config doctor before interpreting model quality"] else [] end) +
      (if (($configured_matrix_models - $matrix_evidence_models) | length) > 0 then ["run v4-provider-matrix-smoke and v4-provider-matrix-curate for configured aliases missing matrix evidence"] else [] end) +
      (if (($codex_evidence_models - $configured_matrix_models) | length) > 0 then ["old or non-canonical Codex curation evidence exists for model ids outside the current provider/model matrix; keep it historical unless rerun under the public alias"] else [] end) +
      (if any($rows[]; .decision == "release_gate_retry_dependent") then ["record retry-dependent Codex baselines explicitly; do not call them strict-clean"] else [] end) +
      (if any($rows[]; .decision == "release_gate_matrix_only") then ["matrix smoke is green but Codex curation is missing or not required; run Codex eval before Codex-specific promotion"] else [] end) +
      (if any($rows[]; .decision == "needs_attention" or .decision == "config_attention" or .decision == "codex_attention") then ["inspect nested provider matrix and Codex curation artifacts before promotion"] else [] end)
    )
  }' >"${summary_json}"
then
  echo "v4 provider ops report failed: could not build summary" >&2
  exit 1
fi

{
  status="$(jq -r '.status' "${summary_json}")"
  verdict="$(jq -r '.verdict' "${summary_json}")"
  echo "# V4 Provider Ops Report"
  echo
  echo "- Status: \`${status}\`"
  echo "- Verdict: \`${verdict}\`"
  echo "- Artifacts: \`${out_dir}\`"
  echo "- Provider doctor: \`$(jq -r '.sources.provider_doctor // ""' "${summary_json}")\`"
  echo "- Provider matrix curation: \`$(jq -r '.sources.provider_matrix_curation // ""' "${summary_json}")\`"
  echo "- Codex eval curation: \`$(jq -r '.sources.codex_eval_curation // ""' "${summary_json}")\`"
  if [[ -n "${model_filter}" ]]; then
    echo "- Model filter: \`${model_filter}\`"
  fi
  echo
  echo "## Model Decisions"
  echo
  echo "| Model | Decision | Config | Matrix | Codex baseline | Recommendation |"
  echo "| --- | --- | --- | --- | --- | --- |"
  jq -r '.rows[] |
    "| `\(.model)` | `\(.decision)` | `\(.config.status)` | `\(.matrix.category)` | `\(.codex.status)` | \(.matrix.recommendation) / \(.codex.recommendation) |"' \
    "${summary_json}"
  echo
  echo "## Drift"
  echo
  echo "- Configured without matrix evidence: $(jq -r '(.drift.configured_without_matrix_evidence // []) | length' "${summary_json}")"
  echo "- Matrix evidence without config: $(jq -r '(.drift.matrix_evidence_without_config // []) | length' "${summary_json}")"
  echo "- Codex evidence without config: $(jq -r '(.drift.codex_evidence_without_config // []) | length' "${summary_json}")"
  echo
  echo "## Recommendations"
  echo
  if [[ "$(jq -r '(.recommendations // []) | length' "${summary_json}")" -eq 0 ]]; then
    echo "- none"
  else
    jq -r '.recommendations[] | "- " + .' "${summary_json}"
  fi
} >"${summary_md}"

jq '{status, verdict, totals, drift, rows:[.rows[] | {model, decision, matrix:.matrix.category, codex:.codex.status}]}' "${summary_json}"
echo "v4 provider ops report: ${summary_md}"
echo "v4 provider ops report summary: ${summary_json}"
echo "v4 provider ops: ${out_dir}"

case "$(jq -r '.status' "${summary_json}")" in
  passed) exit 0 ;;
  *) exit 1 ;;
esac
