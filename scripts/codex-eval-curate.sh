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
if [[ ${#roots[@]} -gt 0 ]]; then
  cmd+=("${roots[@]}")
fi

"${cmd[@]}"

echo "codex eval curation: ${out_dir}"
