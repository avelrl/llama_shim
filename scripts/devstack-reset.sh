#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/devstack-reset.sh [--dry-run] [--volumes] [--compose docker-compose.devstack.yml]

Stops the devstack Compose project with an allowlisted command shape.

Modes:
  --dry-run   Print the command that would run.
  --volumes   Also remove Compose-managed volumes with docker compose down -v.

This script does not remove .data, .tmp, .cache, logs, or local artifacts.
Use make clean-artifacts or shimctl governance purge for those separate scopes.
EOF
}

dry_run=0
remove_volumes=0
compose_file="${DEVSTACK_COMPOSE:-docker-compose.devstack.yml}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      dry_run=1
      shift
      ;;
    --volumes)
      remove_volumes=1
      shift
      ;;
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

if [[ -z "${compose_file}" ]]; then
  echo "compose file is required" >&2
  exit 2
fi
if [[ ! -f "${compose_file}" ]]; then
  echo "compose file not found: ${compose_file}" >&2
  exit 1
fi

cmd=(docker compose -f "${compose_file}" down --remove-orphans)
if [[ "${remove_volumes}" == "1" ]]; then
  cmd+=(-v)
fi

echo "==> devstack reset"
echo "compose: ${compose_file}"
if [[ "${remove_volumes}" == "1" ]]; then
  echo "volumes: remove"
else
  echo "volumes: keep"
fi

printf 'command:'
printf ' %q' "${cmd[@]}"
printf '\n'

if [[ "${dry_run}" == "1" ]]; then
  echo "dry-run: command not executed"
  echo "next steps:"
  echo "  make clean-artifacts          # remove disposable .tmp run artifacts"
  echo "  make local-state-report       # inspect .tmp/.cache/.data sizes"
  echo "  shimctl governance purge ...  # reset configured local API state"
  exit 0
fi

"${cmd[@]}"

echo "devstack reset completed"
echo "next steps:"
echo "  make devstack-up"
echo "  make devstack-ci-smoke"
