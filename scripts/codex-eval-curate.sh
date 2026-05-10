#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/codex-eval-curate.sh [artifact-root-or-summary ...]

Environment:
  CODEX_EVAL_CURATE_OUT_DIR   Default: .tmp/codex-eval-curation/curation-<timestamp>
  CODEX_EVAL_CURATE_LIMIT     Default: 50
  CODEX_EVAL_CURATE_MODEL     Optional exact model filter.
  CODEX_EVAL_CURATE_SINCE     Optional RFC3339 or YYYY-MM-DD lower bound.
  CODEX_EVAL_CURATE_PROVIDER_CONFIG
                                Optional shim config path for provider/model alias normalization.
                                Default: $CONFIG when set, else ./config.yaml when it exists.
                                Set to disabled to skip config-based normalization.
  CODEX_EVAL_CURATE_MODEL_ALIASES
                                Optional comma-separated raw=provider/model aliases.

If no artifact roots are passed, the script scans existing local Codex eval
artifact roots under .tmp/codex-eval-auto, .tmp/codex-eval-loops, and
.tmp/codex-eval-runs.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
out_dir="${CODEX_EVAL_CURATE_OUT_DIR:-.tmp/codex-eval-curation/curation-${timestamp}}"
limit="${CODEX_EVAL_CURATE_LIMIT:-50}"
model="${CODEX_EVAL_CURATE_MODEL:-}"
since="${CODEX_EVAL_CURATE_SINCE:-}"
provider_config="${CODEX_EVAL_CURATE_PROVIDER_CONFIG:-${CONFIG:-}}"
if [[ -z "${provider_config}" && -f "config.yaml" ]]; then
  provider_config="config.yaml"
fi
case "${provider_config}" in
  disabled|none|0)
    provider_config=""
    ;;
esac
model_aliases="${CODEX_EVAL_CURATE_MODEL_ALIASES:-}"

roots=()
if [[ $# -gt 0 ]]; then
  roots=("$@")
else
  for path in .tmp/codex-eval-auto .tmp/codex-eval-loops .tmp/codex-eval-runs; do
    if [[ -e "${path}" ]]; then
      roots+=("${path}")
    fi
  done
fi

mkdir -p "${out_dir}"

cmd=(go run ./cmd/codex-eval-runner curate
  --out "${out_dir}/summary.md"
  --json-out "${out_dir}/summary.json"
  --limit "${limit}")

if [[ -n "${model}" ]]; then
  cmd+=(--model "${model}")
fi
if [[ -n "${since}" ]]; then
  cmd+=(--since "${since}")
fi
if [[ -n "${provider_config}" ]]; then
  cmd+=(--provider-config "${provider_config}")
fi
if [[ -n "${model_aliases}" ]]; then
  cmd+=(--model-aliases "${model_aliases}")
fi
if [[ ${#roots[@]} -gt 0 ]]; then
  cmd+=("${roots[@]}")
fi

"${cmd[@]}"

echo "codex eval curation: ${out_dir}"
