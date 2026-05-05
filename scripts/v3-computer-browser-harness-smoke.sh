#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

usage() {
  cat <<'EOF'
Usage:
  SHIM_BASE_URL=http://127.0.0.1:18080 \
  FIXTURE_BASE_URL=http://127.0.0.1:18081 \
  MODEL=devstack-model \
  ./scripts/v3-computer-browser-harness-smoke.sh

Optional:
  PLAYWRIGHT_BROWSER=chrome
  PLAYWRIGHT_SESSION=v3-computer-browser-harness
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'

This smoke runs a real external browser harness for the shim-local computer
loop. It opens the deterministic devstack fixture page in Playwright, asks
/v1/responses for computer actions, executes the returned actions in the
browser, sends screenshot output back as computer_call_output, and verifies
that the page input was actually filled with "penguin".
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd awk
require_cmd base64
require_cmd curl
require_cmd jq
require_cmd mktemp
require_cmd playwright-cli

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
fixture_base_url="${FIXTURE_BASE_URL:-http://127.0.0.1:18081}"
model="${MODEL:-devstack-model}"
browser="${PLAYWRIGHT_BROWSER:-chrome}"
session="${PLAYWRIGHT_SESSION:-v3-computer-browser-harness-$(date +%s)-$$}"
auth_header="${SHIM_AUTH_HEADER:-}"

tmp_dir="$(mktemp -d)"
response_ids=()

export PLAYWRIGHT_DAEMON_SESSION_DIR="${PLAYWRIGHT_DAEMON_SESSION_DIR:-${PWD}/.tmp/playwright-daemon-sessions}"
export PLAYWRIGHT_DAEMON_SOCKETS_DIR="${PLAYWRIGHT_DAEMON_SOCKETS_DIR:-${PWD}/.tmp/playwright-daemon-sockets}"
mkdir -p "${PLAYWRIGHT_DAEMON_SESSION_DIR}" "${PLAYWRIGHT_DAEMON_SOCKETS_DIR}"

cleanup() {
  playwright-cli -s="${session}" close >/dev/null 2>&1 || true
  for response_id in "${response_ids[@]:-}"; do
    if [[ -n "${response_id}" && "${response_id}" != "null" ]]; then
      curl_shim -X DELETE "${shim_base_url}/v1/responses/${response_id}" >/dev/null || true
    fi
  done
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

curl_shim() {
  if [[ -n "${auth_header}" ]]; then
    curl -fsS -H "${auth_header}" "$@"
  else
    curl -fsS "$@"
  fi
}

post_json() {
  local body="$1"
  local response_file status
  response_file="${tmp_dir}/post-response.json"
  if [[ -n "${auth_header}" ]]; then
    status="$(curl -sS -o "${response_file}" -w '%{http_code}' -H "${auth_header}" "${shim_base_url}/v1/responses" \
      -H 'Content-Type: application/json' \
      -d "${body}")"
  else
    status="$(curl -sS -o "${response_file}" -w '%{http_code}' "${shim_base_url}/v1/responses" \
      -H 'Content-Type: application/json' \
      -d "${body}")"
  fi
  if [[ "${status}" -lt 200 || "${status}" -ge 300 ]]; then
    echo "POST /v1/responses failed with HTTP ${status}" >&2
    cat "${response_file}" >&2
    echo >&2
    return 22
  fi
  cat "${response_file}"
}

wait_http_ok() {
  local label="$1"
  local url="$2"

  for _ in $(seq 1 60); do
    if curl_shim "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "${label} did not become ready: ${url}" >&2
  exit 1
}

run_playwright() {
  playwright-cli -s="${session}" "$@"
}

js_quote() {
  jq -Rsa .
}

execute_actions() {
  local response_json="$1"
  local actions_file="${tmp_dir}/actions.jsonl"
  printf '%s\n' "${response_json}" | jq -c '.output[0].actions[]' > "${actions_file}"

  while IFS= read -r action; do
    local action_type
    action_type="$(printf '%s\n' "${action}" | jq -r '.type')"
    case "${action_type}" in
      screenshot)
        ;;
      click)
        local x y button js_button
        x="$(printf '%s\n' "${action}" | jq -r '.x')"
        y="$(printf '%s\n' "${action}" | jq -r '.y')"
        button="$(printf '%s\n' "${action}" | jq -r '.button // "left"')"
        js_button="$(printf '%s' "${button}" | js_quote)"
        run_playwright run-code "async page => { await page.mouse.click(${x}, ${y}, {button: ${js_button}}); }" >/dev/null
        ;;
      double_click)
        local x y button js_button
        x="$(printf '%s\n' "${action}" | jq -r '.x')"
        y="$(printf '%s\n' "${action}" | jq -r '.y')"
        button="$(printf '%s\n' "${action}" | jq -r '.button // "left"')"
        js_button="$(printf '%s' "${button}" | js_quote)"
        run_playwright run-code "async page => { await page.mouse.click(${x}, ${y}, {button: ${js_button}, clickCount: 2}); }" >/dev/null
        ;;
      type)
        local text js_text
        text="$(printf '%s\n' "${action}" | jq -r '.text')"
        js_text="$(printf '%s' "${text}" | js_quote)"
        run_playwright run-code "async page => { await page.keyboard.type(${js_text}); }" >/dev/null
        ;;
      wait)
        local ms
        ms="$(printf '%s\n' "${action}" | jq -r '.ms // .duration // 250')"
        run_playwright run-code "async page => { await page.waitForTimeout(${ms}); }" >/dev/null
        ;;
      keypress)
        local key js_key
        key="$(printf '%s\n' "${action}" | jq -r 'if (.keys | type) == "array" and (.keys | length) > 0 then .keys[0] else (.key // "") end')"
        if [[ -z "${key}" || "${key}" == "null" ]]; then
          echo "keypress action missing key: ${action}" >&2
          exit 1
        fi
        js_key="$(printf '%s' "${key}" | js_quote)"
        run_playwright run-code "async page => { await page.keyboard.press(${js_key}); }" >/dev/null
        ;;
      move)
        local x y
        x="$(printf '%s\n' "${action}" | jq -r '.x')"
        y="$(printf '%s\n' "${action}" | jq -r '.y')"
        run_playwright run-code "async page => { await page.mouse.move(${x}, ${y}); }" >/dev/null
        ;;
      scroll)
        local scroll_x scroll_y
        scroll_x="$(printf '%s\n' "${action}" | jq -r '.scroll_x // .delta_x // 0')"
        scroll_y="$(printf '%s\n' "${action}" | jq -r '.scroll_y // .delta_y // 0')"
        run_playwright mousewheel "${scroll_x}" "${scroll_y}" >/dev/null
        ;;
      drag)
        local path_json
        path_json="$(printf '%s\n' "${action}" | jq -c '
          if (.path | type) == "array" and (.path | length) > 1 then
            [.path[] | {x: .x, y: .y}]
          elif has("x") and has("y") and has("end_x") and has("end_y") then
            [{x: .x, y: .y}, {x: .end_x, y: .end_y}]
          else
            empty
          end
        ')"
        if [[ -z "${path_json}" ]]; then
          echo "drag action missing path or x/y/end_x/end_y: ${action}" >&2
          exit 1
        fi
        run_playwright run-code "async page => { const path = ${path_json}; await page.mouse.move(path[0].x, path[0].y); await page.mouse.down(); for (const point of path.slice(1)) { await page.mouse.move(point.x, point.y); } await page.mouse.up(); }" >/dev/null
        ;;
      *)
        echo "unsupported harness action type: ${action_type}" >&2
        exit 1
        ;;
    esac
  done < "${actions_file}"
}

response_types_expected_text() {
  local response_json="$1"
  local expected_text="$2"
  printf '%s\n' "${response_json}" | jq -e --arg expected_text "${expected_text}" '
    any(.output[0].actions[]?; .type == "type" and .text == $expected_text)
  ' >/dev/null
}

capture_screenshot_data_url() {
  local file="${tmp_dir}/computer-harness.png"
  run_playwright screenshot --filename "${file}" >/dev/null
  printf 'data:image/png;base64,%s' "$(base64 < "${file}" | tr -d '\n')"
}

post_screenshot_output() {
  local previous_response_id="$1"
  local call_id="$2"
  local screenshot_data_url="$3"

  post_json "$(jq -nc \
    --arg model "${model}" \
    --arg previous_response_id "${previous_response_id}" \
    --arg call_id "${call_id}" \
    --arg screenshot "${screenshot_data_url}" \
    '{
      model: $model,
      store: true,
      previous_response_id: $previous_response_id,
      input: [{
        type: "computer_call_output",
        call_id: $call_id,
        output: {
          type: "computer_screenshot",
          image_url: $screenshot,
          detail: "original"
        }
      }],
      tools: [{type: "computer"}],
      tool_choice: "required"
    }')"
}

page_input_value() {
  local raw_value actual_value
  raw_value="$(run_playwright eval '() => document.querySelector("#harness-input").value' | awk '/^### Result/{found=1; next} found && NF {print; exit}' | tr -d '\r')"
  actual_value="$(printf '%s\n' "${raw_value}" | jq -r . 2>/dev/null || printf '%s\n' "${raw_value}")"
  printf '%s\n' "${actual_value}"
}

echo "==> waiting for fixture readiness: ${fixture_base_url}/healthz"
wait_http_ok "fixture" "${fixture_base_url}/healthz"
echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
wait_http_ok "shim" "${shim_base_url}/readyz"

echo "==> checking local computer capability flags"
capabilities_json="$(curl_shim "${shim_base_url}/debug/capabilities")"
printf '%s\n' "${capabilities_json}" | jq -e '
  .tools.computer.enabled == true and
  .tools.computer.backend == "chat_completions" and
  .probes.computer_runtime.ready == true
' >/dev/null

page_url="${fixture_base_url}/pages/computer-harness"
echo "==> opening browser harness page: ${page_url}"
run_playwright open "${page_url}" --browser "${browser}" >/dev/null
run_playwright resize 1024 768 >/dev/null

echo "==> requesting screenshot-first computer_call"
first="$(post_json "$(jq -nc --arg model "${model}" '{
  model: $model,
  store: true,
  input: "Use the computer tool. First request a screenshot. After you receive the screenshot, click the target search input at coordinate x=636, y=343, then type penguin. Use those coordinates exactly for the click.",
  tools: [{type: "computer"}],
  tool_choice: "required",
  include: ["computer_call_output.output.image_url"]
}')")"
first_response_id="$(printf '%s\n' "${first}" | jq -r '.id')"
first_call_id="$(printf '%s\n' "${first}" | jq -r '.output[0].call_id')"
response_ids+=("${first_response_id}")
printf '%s\n' "${first}" | jq '{id, status, output_type: .output[0].type, actions: .output[0].actions}'
printf '%s\n' "${first}" | jq -e '
  .status == "completed" and
  .output[0].type == "computer_call" and
  .output[0].actions == [{type: "screenshot"}]
' >/dev/null

previous_response_id="${first_response_id}"
call_id="${first_call_id}"
for turn in $(seq 1 4); do
  echo "==> computer loop turn ${turn}: capturing screenshot and sending computer_call_output"
  screenshot_data_url="$(capture_screenshot_data_url)"
  response="$(post_screenshot_output "${previous_response_id}" "${call_id}" "${screenshot_data_url}")"
  response_id="$(printf '%s\n' "${response}" | jq -r '.id')"
  response_ids+=("${response_id}")
  printf '%s\n' "${response}" | jq '{id, status, previous_response_id, call_id: .output[0].call_id, actions: .output[0].actions}'
  printf '%s\n' "${response}" | jq -e --arg previous_response_id "${previous_response_id}" '
    .status == "completed" and
    .previous_response_id == $previous_response_id and
    .output[0].type == "computer_call" and
    (.output[0].call_id | type) == "string" and
    (.output[0].actions | type) == "array" and
    (.output[0].actions | length) > 0
  ' >/dev/null

  echo "==> computer loop turn ${turn}: executing returned browser actions"
  execute_actions "${response}"

  echo "==> computer loop turn ${turn}: verifying real page state"
  actual_value="$(page_input_value)"
  echo "==> computer loop turn ${turn}: page input value: ${actual_value}"
  if [[ "${actual_value}" == "penguin" ]]; then
    echo "v3 computer browser harness smoke passed"
    exit 0
  fi
  if response_types_expected_text "${response}" "penguin"; then
    echo "browser harness received a type action for 'penguin' but the page value is '${actual_value}'" >&2
    echo "last response actions:" >&2
    printf '%s\n' "${response}" | jq '.output[0].actions' >&2
    run_playwright screenshot --filename "${tmp_dir}/failure.png" >/dev/null || true
    exit 1
  fi

  previous_response_id="${response_id}"
  call_id="$(printf '%s\n' "${response}" | jq -r '.output[0].call_id')"
done

actual_value="$(page_input_value)"
echo "browser harness did not type expected value after computer loop: got '${actual_value}', want 'penguin'" >&2
run_playwright screenshot --filename "${tmp_dir}/failure.png" >/dev/null || true
exit 1
