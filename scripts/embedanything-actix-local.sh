#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  EMBEDANYTHING_DIR=/path/to/EmbedAnything ./scripts/embedanything-actix-local.sh
  ./scripts/embedanything-actix-local.sh /path/to/EmbedAnything

This helper follows the official EmbedAnything Actix server guide and runs:
  cargo run -p server --release

The official server starts on http://0.0.0.0:8080 by default.
Run the shim on a different port locally, for example :8083.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

embedanything_dir="${1:-${EMBEDANYTHING_DIR:-}}"
if [[ -z "${embedanything_dir}" ]]; then
  echo "embedanything checkout path is required" >&2
  usage >&2
  exit 1
fi

if [[ ! -d "${embedanything_dir}" ]]; then
  echo "EmbedAnything directory does not exist: ${embedanything_dir}" >&2
  exit 1
fi

if ! command -v cargo >/dev/null 2>&1; then
  echo "cargo is required to run the EmbedAnything Actix server" >&2
  exit 1
fi

cd "${embedanything_dir}"
exec cargo run -p server --release
