#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/local-state-report.sh [--compose docker-compose.devstack.yml]

Prints a read-only local state and artifact report. It does not delete or
modify files, Docker containers, or volumes.
EOF
}

compose_file="${DEVSTACK_COMPOSE:-docker-compose.devstack.yml}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --compose)
      compose_file="${2:-}"
      shift 2
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

size_of() {
  local path="$1"
  if [[ ! -e "${path}" ]]; then
    printf '%s' "-"
    return 0
  fi
  du -sh "${path}" 2>/dev/null | awk '{print $1}'
}

file_count_of() {
  local path="$1"
  if [[ ! -e "${path}" ]]; then
    printf '%s' "0"
    return 0
  fi
  find "${path}" -type f 2>/dev/null | wc -l | tr -d '[:space:]'
}

print_path_row() {
  local label="$1"
  local path="$2"
  local class="$3"
  printf '%-36s %-46s %-12s %8s files  %s\n' \
    "${label}" \
    "${path}" \
    "$(size_of "${path}")" \
    "$(file_count_of "${path}")" \
    "${class}"
}

echo "==> local state report"
echo
printf '%-36s %-46s %-12s %14s  %s\n' "label" "path" "size" "files" "class"
printf '%-36s %-46s %-12s %14s  %s\n' "-----" "----" "----" "-----" "-----"

print_path_row "durable data" ".data" "durable"
print_path_row "shim sqlite db" ".data/shim.db" "durable"
print_path_row "shim log" ".data/shim.log" "durable"
print_path_row "responses compat artifacts" ".data/responses-compat-external" "durable-artifacts"
print_path_row "tmp root" ".tmp" "disposable"
print_path_row "codex eval runs" ".tmp/codex-eval-runs" "disposable"
print_path_row "codex eval loops" ".tmp/codex-eval-loops" "disposable"
print_path_row "codex eval auto" ".tmp/codex-eval-auto" "disposable"
print_path_row "codex eval curation" ".tmp/codex-eval-curation" "disposable"
print_path_row "v4 provider matrix smoke" ".tmp/v4-provider-matrix-smoke" "disposable"
print_path_row "v4 provider matrix curation" ".tmp/v4-provider-matrix-curation" "disposable"
print_path_row "computer browser harness" ".tmp/v3-computer-browser-harness-runs" "disposable"
print_path_row "governance smoke runs" ".tmp/governance-purge-smoke" "disposable"
print_path_row "go build cache" ".tmp/go-build" "cache"
print_path_row "go tmp" ".tmp/go-tmp" "cache"
print_path_row "tool cache" ".cache" "cache"
print_path_row "playwright cache" ".playwright-cli" "cache"

echo
echo "cleanup hints:"
echo "  make clean-artifacts-dry-run      # preview disposable .tmp artifact cleanup"
echo "  make clean-artifacts              # remove disposable .tmp run artifacts"
echo "  make clean-dev-artifacts-dry-run  # preview artifact + cache cleanup"
echo "  make clean-dev-artifacts          # remove allowlisted artifacts and caches"
echo "  make devstack-reset-dry-run       # preview docker compose devstack reset"
echo "  make devstack-reset-volumes-dry-run # preview docker volume reset"
echo "  shimctl governance purge ...      # reset configured local API state"
echo
echo ".data is durable and is not removed by cleanup targets."

if command -v docker >/dev/null 2>&1 && [[ -f "${compose_file}" ]]; then
  echo
  echo "==> devstack compose status (${compose_file})"
  docker compose -f "${compose_file}" ps 2>/dev/null || true
fi
