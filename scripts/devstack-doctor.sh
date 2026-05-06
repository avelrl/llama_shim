#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/devstack-doctor.sh [--compose docker-compose.devstack.yml]

Environment:
  SHIM_BASE_URL                   Default: http://127.0.0.1:18080
  FIXTURE_BASE_URL                Default: http://127.0.0.1:18081
  SHIM_SECONDARY_BASE_URL         Default: http://127.0.0.1:18082
  DEVSTACK_DOCTOR_REQUIRE_READY   Default: 1. Set 0 for advisory diagnostics.
  DEVSTACK_DOCTOR_SECONDARY       Default: 0. Set 1 to require secondary shim.

Runs read-only local devstack diagnostics. It does not start, stop, reset, or
delete anything.
EOF
}

compose_file="${DEVSTACK_COMPOSE:-docker-compose.devstack.yml}"
shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
fixture_base_url="${FIXTURE_BASE_URL:-http://127.0.0.1:18081}"
secondary_base_url="${SHIM_SECONDARY_BASE_URL:-http://127.0.0.1:18082}"
require_ready="${DEVSTACK_DOCTOR_REQUIRE_READY:-1}"
require_secondary="${DEVSTACK_DOCTOR_SECONDARY:-0}"
failures=0

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

strict_enabled() {
  [[ "${require_ready}" != "0" ]]
}

record_fail() {
  local message="$1"
  if strict_enabled; then
    failures=$((failures + 1))
    echo "FAIL ${message}"
  else
    echo "WARN ${message}"
  fi
}

record_warn() {
  echo "WARN $1"
}

record_pass() {
  echo "PASS $1"
}

size_of() {
  local path="$1"
  if [[ ! -e "${path}" ]]; then
    printf '%s' "-"
    return 0
  fi
  du -sh "${path}" 2>/dev/null | awk '{print $1}'
}

require_cmd() {
  local name="$1"
  if command -v "${name}" >/dev/null 2>&1; then
    record_pass "command ${name} found"
    return 0
  fi
  record_fail "command ${name} missing"
  return 1
}

PROBE_BODY=""

probe_http_required() {
  local label="$1"
  local url="$2"
  local required="$3"
  local tmp_body tmp_err code curl_exit
  tmp_body="$(mktemp)"
  tmp_err="$(mktemp)"
  PROBE_BODY=""

  set +e
  code="$(curl -fsS --max-time 5 -o "${tmp_body}" -w '%{http_code}' "${url}" 2>"${tmp_err}")"
  curl_exit=$?
  set -e

  if [[ "${curl_exit}" == "0" && "${code}" =~ ^2[0-9][0-9]$ ]]; then
    record_pass "${label} ${url} -> ${code}"
    PROBE_BODY="$(cat "${tmp_body}")"
    rm -f "${tmp_body}" "${tmp_err}"
    return 0
  fi

  if [[ "${required}" == "1" ]]; then
    record_fail "${label} ${url} not ready (http=${code:-000}, curl_exit=${curl_exit})"
  else
    record_warn "${label} ${url} unavailable (http=${code:-000}, curl_exit=${curl_exit})"
  fi
  if [[ -s "${tmp_err}" ]]; then
    sed 's/^/  curl: /' "${tmp_err}" | head -n 4
  fi
  if [[ -s "${tmp_body}" ]]; then
    sed 's/^/  body: /' "${tmp_body}" | head -n 8
  fi
  rm -f "${tmp_body}" "${tmp_err}"
  return 1
}

print_capability_summary() {
  local body="$1"
  if ! command -v jq >/dev/null 2>&1; then
    record_warn "jq missing; capability manifest summary skipped"
    return 0
  fi

  if ! echo "${body}" | jq '{
    object,
    ready,
    runtime: {
      responses_mode: .runtime.responses_mode,
      storage_backend: .runtime.persistence.backend,
      retrieval_index_backend: .runtime.retrieval.index_backend,
      compaction_backend: .runtime.compaction.backend,
      computer_backend: .tools.computer.backend
    },
    tools: {
      file_search: .tools.file_search.enabled,
      web_search: .tools.web_search.enabled,
      image_generation: .tools.image_generation.enabled,
      computer: .tools.computer.enabled,
      code_interpreter: .tools.code_interpreter.enabled,
      mcp_server_url: .tools.mcp_server_url.enabled,
      tool_search_hosted: .tools.tool_search_hosted.enabled,
      shell: .tools.shell.enabled,
      apply_patch: .tools.apply_patch.enabled
    },
    probes: .probes
  }'; then
    record_fail "capability manifest is not valid JSON"
    return 0
  fi

  if echo "${body}" | jq -e '.object == "shim.capabilities" and .ready == true' >/dev/null; then
    record_pass "capability manifest is ready"
  else
    record_fail "capability manifest reports unready or unexpected object"
  fi
}

echo "==> devstack doctor"
echo "mode: $(if strict_enabled; then echo strict; else echo advisory; fi)"
echo "compose: ${compose_file}"
echo "shim: ${shim_base_url}"
echo "fixture: ${fixture_base_url}"
echo

require_cmd curl || true
require_cmd docker || true
require_cmd mktemp || true
if command -v jq >/dev/null 2>&1; then
  record_pass "command jq found"
else
  record_warn "command jq missing; JSON summaries will be degraded"
fi

if [[ -z "${compose_file}" ]]; then
  record_fail "compose file argument is empty"
elif [[ -f "${compose_file}" ]]; then
  record_pass "compose file exists: ${compose_file}"
else
  record_fail "compose file missing: ${compose_file}"
fi

echo
echo "==> local artifact footprint"
printf '%-28s %s\n' ".data" "$(size_of .data)"
printf '%-28s %s\n' ".tmp" "$(size_of .tmp)"
printf '%-28s %s\n' ".cache" "$(size_of .cache)"
printf '%-28s %s\n' ".playwright-cli" "$(size_of .playwright-cli)"

echo
echo "==> docker compose"
if command -v docker >/dev/null 2>&1 && [[ -f "${compose_file}" ]]; then
  if docker compose version >/dev/null 2>&1; then
    record_pass "docker compose is available"
    docker compose -f "${compose_file}" ps || record_fail "docker compose ps failed"
  else
    record_fail "docker compose plugin is unavailable"
  fi
else
  record_warn "docker compose status skipped"
fi

echo
echo "==> HTTP probes"
if probe_http_required "fixture health" "${fixture_base_url}/healthz" 1; then
  printf '%s\n' "${PROBE_BODY}" | sed 's/^/  /'
fi

if probe_http_required "shim health" "${shim_base_url}/healthz" 1; then
  printf '%s\n' "${PROBE_BODY}" | sed 's/^/  /'
fi

if probe_http_required "shim readiness" "${shim_base_url}/readyz" 1; then
  printf '%s\n' "${PROBE_BODY}" | sed 's/^/  /'
fi

if probe_http_required "shim capabilities" "${shim_base_url}/debug/capabilities" 1; then
  echo
  echo "==> capability summary"
  print_capability_summary "${PROBE_BODY}"
fi

if [[ "${require_secondary}" == "1" ]]; then
  if probe_http_required "secondary shim readiness" "${secondary_base_url}/readyz" 1; then
    printf '%s\n' "${PROBE_BODY}" | sed 's/^/  /'
  fi
else
  probe_http_required "secondary shim readiness" "${secondary_base_url}/readyz" 0 >/dev/null || true
fi

echo
if [[ "${failures}" -gt 0 ]]; then
  echo "devstack doctor failed: ${failures} required check(s) failed"
  echo "next steps:"
  echo "  make devstack-up"
  echo "  make local-state-report"
  echo "  make devstack-reset-dry-run"
  exit 1
fi

if strict_enabled; then
  echo "devstack doctor passed"
else
  echo "devstack doctor completed (advisory mode)"
fi
