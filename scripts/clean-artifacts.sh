#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/clean-artifacts.sh [--profile artifacts|dev|codex-eval] [--dry-run]

Profiles:
  artifacts   Remove disposable run artifacts under .tmp. Does not touch .data.
  dev         Remove disposable run artifacts plus local tool caches. Does not touch .data.
  codex-eval  Remove only Codex eval run artifacts under .tmp.

This script intentionally uses a fixed allowlist. It never removes .data,
config.yaml, .env, or arbitrary paths supplied by the caller.
EOF
}

profile="artifacts"
dry_run=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      profile="${2:-}"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

codex_eval_paths=(
  ".tmp/codex-eval-runs"
  ".tmp/codex-eval-loops"
  ".tmp/codex-eval-auto"
  ".tmp/codex-eval-curation"
)

artifact_paths=(
  "${codex_eval_paths[@]}"
  ".tmp/codex-real-upstream-smoke"
  ".tmp/codex-smoke"
  ".tmp/codex-shell-tool-smoke"
  ".tmp/codex-coding-task-smoke"
  ".tmp/codex-task-matrix-smoke"
  ".tmp/governance-purge-smoke"
  ".tmp/v4-preflight-smoke"
  ".tmp/v4-provider-config-doctor"
  ".tmp/v4-provider-matrix-smoke"
  ".tmp/v4-provider-matrix-curation"
  ".tmp/v4-provider-ops"
  ".tmp/v3-computer-browser-harness-runs"
  ".tmp/playwright-daemon-sessions"
  ".tmp/playwright-daemon-sockets"
  ".tmp/playwright-qwen36-sessions"
  ".tmp/playwright-qwen36-sockets"
)

dev_cache_paths=(
  ".cache"
  ".playwright-cli"
  ".tmp/go-build"
  ".tmp/go-tmp"
)

case "${profile}" in
  artifacts)
    paths=("${artifact_paths[@]}")
    ;;
  dev)
    paths=("${artifact_paths[@]}" "${dev_cache_paths[@]}")
    ;;
  codex-eval)
    paths=("${codex_eval_paths[@]}")
    ;;
  *)
    echo "unsupported cleanup profile: ${profile}" >&2
    usage >&2
    exit 2
    ;;
esac

is_safe_path() {
  local path="$1"
  case "${path}" in
    .tmp/*|.cache|.playwright-cli)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

echo "==> cleanup profile: ${profile}"
if [[ "${dry_run}" == "1" ]]; then
  echo "==> dry-run only"
fi

removed=0
for path in "${paths[@]}"; do
  if ! is_safe_path "${path}"; then
    echo "refusing unsafe cleanup path: ${path}" >&2
    exit 1
  fi
  if [[ ! -e "${path}" ]]; then
    echo "skip missing: ${path}"
    continue
  fi
  if [[ "${dry_run}" == "1" ]]; then
    echo "would remove: ${path}"
  else
    echo "remove: ${path}"
    rm -rf -- "${path}"
  fi
  removed=$((removed + 1))
done

if [[ "${dry_run}" == "1" ]]; then
  echo "cleanup dry-run completed: matched=${removed}"
else
  echo "cleanup completed: removed=${removed}"
fi

echo "next steps:"
echo "  make local-state-report       # inspect remaining .tmp/.cache/.data state"
echo "  make devstack-reset-dry-run   # preview Docker devstack reset"
echo "  shimctl governance purge ...  # reset configured local API state"
