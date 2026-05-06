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
  COMPUTER_HARNESS_SCENARIOS=type,keypress,scroll,drag
  COMPUTER_HARNESS_SCENARIOS=all
  COMPUTER_HARNESS_ARTIFACT_DIR=.tmp/v3-computer-browser-harness-runs
  COMPUTER_HARNESS_RUN_ID=manual-001
  COMPUTER_HARNESS_RUN_DIR=.tmp/v3-computer-browser-harness-runs/custom
  PLAYWRIGHT_BROWSER=chrome
  PLAYWRIGHT_SESSION=cbh-manual
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'

This smoke runs a real external browser harness for the shim-local computer
loop. It opens the deterministic devstack fixture page in Playwright, asks
/v1/responses for computer actions, executes the returned actions in the
browser, sends screenshot output back as computer_call_output, and verifies
real DOM state for each requested fixture scenario.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd awk
require_cmd base64
require_cmd cksum
require_cmd curl
require_cmd jq
require_cmd mktemp
require_cmd playwright-cli
require_cmd sed
require_cmd tr

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:18080}"
fixture_base_url="${FIXTURE_BASE_URL:-http://127.0.0.1:18081}"
model="${MODEL:-devstack-model}"
browser="${PLAYWRIGHT_BROWSER:-chrome}"
session="${PLAYWRIGHT_SESSION:-cbh-$(date +%s)-$$}"
active_session="${session}"
auth_header="${SHIM_AUTH_HEADER:-}"
scenarios_raw="${COMPUTER_HARNESS_SCENARIOS:-type}"
artifact_base="${COMPUTER_HARNESS_ARTIFACT_DIR:-.tmp/v3-computer-browser-harness-runs}"
run_id="${COMPUTER_HARNESS_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
model_slug="$(printf '%s' "${model}" | tr -cs '[:alnum:]_.-' '-' | sed 's/^-//; s/-$//')"
if [[ -z "${model_slug}" ]]; then
  model_slug="model"
fi
artifact_dir="${COMPUTER_HARNESS_RUN_DIR:-${artifact_base}/${model_slug}_${run_id}}"
summary_file="${artifact_dir}/summary.json"
results_file="${artifact_dir}/scenario-results.jsonl"

tmp_dir="$(mktemp -d)"
response_ids=()
playwright_sessions=()
scenario_list=()
finalized=0

export PLAYWRIGHT_DAEMON_SESSION_DIR="${PLAYWRIGHT_DAEMON_SESSION_DIR:-${PWD}/.tmp/playwright-daemon-sessions}"
export PLAYWRIGHT_DAEMON_SOCKETS_DIR="${PLAYWRIGHT_DAEMON_SOCKETS_DIR:-/tmp/llama-shim-pw-sockets}"
mkdir -p \
  "${PLAYWRIGHT_DAEMON_SESSION_DIR}" \
  "${PLAYWRIGHT_DAEMON_SOCKETS_DIR}" \
  "${artifact_dir}/actions" \
  "${artifact_dir}/errors" \
  "${artifact_dir}/requests" \
  "${artifact_dir}/responses" \
  "${artifact_dir}/screenshots" \
  "${artifact_dir}/states"
: > "${results_file}"

close_playwright_session() {
  local playwright_session="$1"
  local close_pid
  playwright-cli -s="${playwright_session}" close >/dev/null 2>&1 &
  close_pid="$!"
  for _ in $(seq 1 20); do
    if ! kill -0 "${close_pid}" >/dev/null 2>&1; then
      wait "${close_pid}" >/dev/null 2>&1 || true
      return
    fi
    sleep 0.1
  done
  kill "${close_pid}" >/dev/null 2>&1 || true
  wait "${close_pid}" >/dev/null 2>&1 || true
}

cleanup() {
  for playwright_session in "${playwright_sessions[@]:-}"; do
    close_playwright_session "${playwright_session}"
  done
  close_playwright_session "${session}"
  for response_id in "${response_ids[@]:-}"; do
    if [[ -n "${response_id}" && "${response_id}" != "null" ]]; then
      curl_shim --max-time 5 -X DELETE "${shim_base_url}/v1/responses/${response_id}" >/dev/null || true
    fi
  done
  rm -rf "${tmp_dir}"
}

write_summary() {
  local status="$1"
  local exit_code="$2"
  set +e
  jq -s \
    --arg status "${status}" \
    --argjson exit_code "${exit_code}" \
    --arg model "${model}" \
    --arg shim_base_url "${shim_base_url}" \
    --arg fixture_base_url "${fixture_base_url}" \
    --arg run_id "${run_id}" \
    --arg artifact_dir "${artifact_dir}" \
    --arg browser "${browser}" \
    --arg scenarios "$(IFS=,; printf '%s' "${scenario_list[*]}")" \
    '{
      status: $status,
      exit_code: $exit_code,
      model: $model,
      run_id: $run_id,
      artifact_dir: $artifact_dir,
      shim_base_url: $shim_base_url,
      fixture_base_url: $fixture_base_url,
      browser: $browser,
      scenarios_requested: ($scenarios | split(",") | map(select(. != ""))),
      scenarios: .
    }' "${results_file}" > "${summary_file}" 2>/dev/null
  set -e
}

finalize() {
  local exit_code="$1"
  if [[ "${finalized}" == "1" ]]; then
    return
  fi
  finalized=1
  if [[ "${exit_code}" == "0" ]]; then
    write_summary "passed" 0
  else
    write_summary "failed" "${exit_code}"
  fi
  echo "artifacts: ${artifact_dir}"
}

trap 'status=$?; finalize "${status}"; cleanup; exit "${status}"' EXIT

curl_shim() {
  if [[ -n "${auth_header}" ]]; then
    curl -fsS -H "${auth_header}" "$@"
  else
    curl -fsS "$@"
  fi
}

sanitize_label() {
  printf '%s' "$1" | tr -cs '[:alnum:]_.-' '-' | sed 's/^-//; s/-$//'
}

save_json() {
  local path="$1"
  local json="$2"
  printf '%s\n' "${json}" | jq . > "${path}"
}

post_json() {
  local body="$1"
  local label="${2:-response}"
  local safe_label response_file request_file error_file status
  safe_label="$(sanitize_label "${label}")"
  request_file="${artifact_dir}/requests/${safe_label}.json"
  response_file="${tmp_dir}/${safe_label}.response.json"
  error_file="${artifact_dir}/errors/${safe_label}.json"
  save_json "${request_file}" "${body}"

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
    cp "${response_file}" "${error_file}" >/dev/null 2>&1 || true
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
  playwright-cli -s="${active_session}" "$@"
}

js_quote() {
  jq -Rsa .
}

playwright_eval_value() {
  local expression="$1"
  local raw_value actual_value
  raw_value="$(run_playwright eval "${expression}" | awk '/^### Result/{found=1; next} found && NF {print; exit}' | tr -d '\r')"
  actual_value="$(printf '%s\n' "${raw_value}" | jq -r . 2>/dev/null || printf '%s\n' "${raw_value}")"
  printf '%s\n' "${actual_value}"
}

execute_actions() {
  local scenario="$1"
  local turn="$2"
  local response_json="$3"
  local actions_file="${tmp_dir}/${scenario}-${turn}-actions.jsonl"
  local actions_artifact="${artifact_dir}/actions/${scenario}_turn${turn}.json"
  printf '%s\n' "${response_json}" | jq '.output[0].actions // []' > "${actions_artifact}"
  printf '%s\n' "${response_json}" | jq -c '.output[0].actions[]?' > "${actions_file}"

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
        local keys_json key_count key js_key
        keys_json="$(printf '%s\n' "${action}" | jq -c 'if (.keys | type) == "array" and (.keys | length) > 0 then .keys else [(.key // "")] end')"
        key_count="$(printf '%s\n' "${keys_json}" | jq 'length')"
        if [[ "${key_count}" == "0" ]]; then
          echo "keypress action missing key: ${action}" >&2
          exit 1
        fi
        for index in $(seq 0 $((key_count - 1))); do
          key="$(printf '%s\n' "${keys_json}" | jq -r ".[$index]")"
          if [[ -z "${key}" || "${key}" == "null" ]]; then
            echo "keypress action missing key: ${action}" >&2
            exit 1
          fi
          js_key="$(printf '%s' "${key}" | js_quote)"
          run_playwright run-code "async page => { await page.keyboard.press(${js_key}); }" >/dev/null
        done
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
        run_playwright run-code "async page => { await page.mouse.move(512, 384); await page.mouse.wheel(${scroll_x}, ${scroll_y}); await page.waitForTimeout(100); }" >/dev/null
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

response_has_action() {
  local response_json="$1"
  local action_filter="$2"
  printf '%s\n' "${response_json}" | jq -e "${action_filter}" >/dev/null
}

capture_screenshot_data_url() {
  local scenario="$1"
  local turn="$2"
  local file="${artifact_dir}/screenshots/${scenario}_turn${turn}.png"
  run_playwright screenshot --filename "${file}" >/dev/null
  printf 'data:image/png;base64,%s' "$(base64 < "${file}" | tr -d '\n')"
}

post_screenshot_output() {
  local scenario="$1"
  local turn="$2"
  local previous_response_id="$3"
  local call_id="$4"
  local screenshot_data_url="$5"

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
    }')" "${scenario}_turn${turn}_screenshot_output"
}

page_input_value() {
  local selector="$1"
  local js_selector
  js_selector="$(printf '%s' "${selector}" | js_quote)"
  playwright_eval_value "() => document.querySelector(${js_selector})?.value ?? ''"
}

page_text() {
  local selector="$1"
  local js_selector
  js_selector="$(printf '%s' "${selector}" | js_quote)"
  playwright_eval_value "() => document.querySelector(${js_selector})?.textContent ?? ''"
}

page_scroll_y() {
  playwright_eval_value '() => String(Math.round(window.scrollY))'
}

page_drag_complete() {
  playwright_eval_value '() => document.body.dataset.dragComplete === "true" ? "true" : "false"'
}

write_scenario_state() {
  local scenario="$1"
  local turn="$2"
  local state
  state="$(playwright_eval_value '() => JSON.stringify({
    harnessInput: document.querySelector("#harness-input")?.value ?? "",
    keypressInput: document.querySelector("#keypress-input")?.value ?? "",
    keypressStatus: document.querySelector("#keypress-status")?.textContent ?? "",
    dragComplete: document.body.dataset.dragComplete === "true",
    dragStatus: document.querySelector("#drag-status")?.textContent ?? "",
    scrollY: Math.round(window.scrollY)
  })')"
  printf '%s\n' "${state}" | jq . > "${artifact_dir}/states/${scenario}_turn${turn}.json"
}

scenario_prompt() {
  case "$1" in
    type)
      printf '%s\n' "Use the computer tool. First request a screenshot. After you receive the screenshot, click the target search input at coordinate x=636, y=343, then type penguin. Use those coordinates exactly for the click."
      ;;
    keypress)
      printf '%s\n' "Use the computer tool. First request a screenshot. After you receive the screenshot, click the keyboard target at coordinate x=204, y=536, type orca, then press Enter. Use those coordinates exactly for the click."
      ;;
    scroll)
      printf '%s\n' "Use the computer tool. First request a screenshot. After you receive the screenshot, scroll down by 520 pixels."
      ;;
    drag)
      printf '%s\n' "Use the computer tool. First request a screenshot. After you receive the screenshot, drag the orange square from coordinate x=142, y=642 to coordinate x=400, y=646."
      ;;
    *)
      echo "unknown computer harness scenario: $1" >&2
      exit 1
      ;;
  esac
}

scenario_done() {
  local scenario="$1"
  case "${scenario}" in
    type)
      [[ "$(page_input_value '#harness-input')" == "penguin" ]]
      ;;
    keypress)
      [[ "$(page_input_value '#keypress-input')" == "orca" && "$(page_text '#keypress-status')" == "Keypress complete" ]]
      ;;
    scroll)
      [[ "$(page_scroll_y)" -ge 500 ]]
      ;;
    drag)
      [[ "$(page_drag_complete)" == "true" ]]
      ;;
    *)
      return 1
      ;;
  esac
}

scenario_terminal_action_seen() {
  local scenario="$1"
  local response_json="$2"
  case "${scenario}" in
    type)
      response_has_action "${response_json}" 'any(.output[0].actions[]?; .type == "type" and .text == "penguin")'
      ;;
    keypress)
      response_has_action "${response_json}" 'any(.output[0].actions[]?; .type == "keypress" and ((.key // "") == "Enter" or ((.keys // []) | index("Enter")) != null))'
      ;;
    scroll)
      response_has_action "${response_json}" 'any(.output[0].actions[]?; .type == "scroll")'
      ;;
    drag)
      response_has_action "${response_json}" 'any(.output[0].actions[]?; .type == "drag")'
      ;;
    *)
      return 1
      ;;
  esac
}

scenario_state_summary() {
  local scenario="$1"
  case "${scenario}" in
    type)
      printf 'harness input value: %s' "$(page_input_value '#harness-input')"
      ;;
    keypress)
      printf 'keyboard input value: %s, status: %s' "$(page_input_value '#keypress-input')" "$(page_text '#keypress-status')"
      ;;
    scroll)
      printf 'scrollY: %s' "$(page_scroll_y)"
      ;;
    drag)
      printf 'dragComplete: %s, status: %s' "$(page_drag_complete)" "$(page_text '#drag-status')"
      ;;
  esac
}

record_scenario_result() {
  local scenario="$1"
  local status="$2"
  local turns="$3"
  local message="$4"
  jq -nc \
    --arg scenario "${scenario}" \
    --arg status "${status}" \
    --argjson turns "${turns}" \
    --arg message "${message}" \
    '{scenario: $scenario, status: $status, turns: $turns, message: $message}' >> "${results_file}"
}

open_scenario_page() {
  local page_url="$1"
  local js_page_url
  js_page_url="$(printf '%s' "${page_url}" | js_quote)"
  run_playwright open "${page_url}" --browser "${browser}" >/dev/null
  run_playwright resize 1024 768 >/dev/null
  run_playwright run-code "async page => { await page.goto(${js_page_url}); await page.waitForSelector('#harness-input'); await page.evaluate(() => window.scrollTo(0, 0)); }" >/dev/null
}

run_scenario() {
  local scenario="$1"
  local page_url="${fixture_base_url}/pages/computer-harness"
  local prompt first first_response_id first_call_id previous_response_id call_id response response_id screenshot_data_url state_message
  active_session="cbh-$(printf '%s-%s' "${session}" "${scenario}" | cksum | awk '{print $1}')"
  playwright_sessions+=("${active_session}")

  echo "==> computer harness scenario: ${scenario}"
  echo "==> opening browser harness page: ${page_url}"
  open_scenario_page "${page_url}"

  prompt="$(scenario_prompt "${scenario}")"
  echo "==> requesting screenshot-first computer_call (${scenario})"
  first="$(post_json "$(jq -nc --arg model "${model}" --arg prompt "${prompt}" '{
    model: $model,
    store: true,
    input: $prompt,
    tools: [{type: "computer"}],
    tool_choice: "required",
    include: ["computer_call_output.output.image_url"]
  }')" "${scenario}_turn0_request_screenshot")"
  first_response_id="$(printf '%s\n' "${first}" | jq -r '.id')"
  first_call_id="$(printf '%s\n' "${first}" | jq -r '.output[0].call_id')"
  response_ids+=("${first_response_id}")
  save_json "${artifact_dir}/responses/${scenario}_turn0.json" "${first}"
  printf '%s\n' "${first}" | jq '{id, status, output_type: .output[0].type, actions: .output[0].actions}'
  printf '%s\n' "${first}" | jq -e '
    .status == "completed" and
    .output[0].type == "computer_call" and
    .output[0].actions == [{type: "screenshot"}]
  ' >/dev/null

  previous_response_id="${first_response_id}"
  call_id="${first_call_id}"
  for turn in $(seq 1 4); do
    echo "==> computer loop ${scenario} turn ${turn}: capturing screenshot and sending computer_call_output"
    screenshot_data_url="$(capture_screenshot_data_url "${scenario}" "${turn}")"
    response="$(post_screenshot_output "${scenario}" "${turn}" "${previous_response_id}" "${call_id}" "${screenshot_data_url}")"
    response_id="$(printf '%s\n' "${response}" | jq -r '.id')"
    response_ids+=("${response_id}")
    save_json "${artifact_dir}/responses/${scenario}_turn${turn}.json" "${response}"
    printf '%s\n' "${response}" | jq '{id, status, previous_response_id, call_id: .output[0].call_id, actions: .output[0].actions}'
    printf '%s\n' "${response}" | jq -e --arg previous_response_id "${previous_response_id}" '
      .status == "completed" and
      .previous_response_id == $previous_response_id and
      .output[0].type == "computer_call" and
      (.output[0].call_id | type) == "string" and
      (.output[0].actions | type) == "array" and
      (.output[0].actions | length) > 0
    ' >/dev/null

    echo "==> computer loop ${scenario} turn ${turn}: executing returned browser actions"
    execute_actions "${scenario}" "${turn}" "${response}"

    echo "==> computer loop ${scenario} turn ${turn}: verifying real page state"
    write_scenario_state "${scenario}" "${turn}"
    state_message="$(scenario_state_summary "${scenario}")"
    echo "==> computer loop ${scenario} turn ${turn}: ${state_message}"
    if scenario_done "${scenario}"; then
      record_scenario_result "${scenario}" "passed" "${turn}" "${state_message}"
      return 0
    fi
    if scenario_terminal_action_seen "${scenario}" "${response}"; then
      echo "browser harness terminal action for '${scenario}' did not produce expected page state: ${state_message}" >&2
      echo "last response actions:" >&2
      printf '%s\n' "${response}" | jq '.output[0].actions' >&2
      run_playwright screenshot --filename "${artifact_dir}/screenshots/${scenario}_failure.png" >/dev/null || true
      record_scenario_result "${scenario}" "failed" "${turn}" "${state_message}"
      return 1
    fi

    previous_response_id="${response_id}"
    call_id="$(printf '%s\n' "${response}" | jq -r '.output[0].call_id')"
  done

  state_message="$(scenario_state_summary "${scenario}")"
  echo "browser harness scenario '${scenario}' did not reach expected state after computer loop: ${state_message}" >&2
  run_playwright screenshot --filename "${artifact_dir}/screenshots/${scenario}_failure.png" >/dev/null || true
  record_scenario_result "${scenario}" "failed" 4 "${state_message}"
  return 1
}

expand_scenarios() {
  local expanded raw item
  raw="$(printf '%s' "${scenarios_raw}" | tr ' ' ',')"
  if [[ "${raw}" == "all" ]]; then
    raw="type,keypress,scroll,drag"
  fi
  IFS=',' read -r -a expanded <<< "${raw}"
  for item in "${expanded[@]}"; do
    item="$(printf '%s' "${item}" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
    [[ -z "${item}" ]] && continue
    case "${item}" in
      type|keypress|scroll|drag)
        scenario_list+=("${item}")
        ;;
      *)
        echo "unsupported COMPUTER_HARNESS_SCENARIOS item: ${item}" >&2
        exit 1
        ;;
    esac
  done
  if [[ "${#scenario_list[@]}" -eq 0 ]]; then
    echo "no computer harness scenarios selected" >&2
    exit 1
  fi
}

expand_scenarios

echo "==> waiting for fixture readiness: ${fixture_base_url}/healthz"
wait_http_ok "fixture" "${fixture_base_url}/healthz"
echo "==> waiting for shim readiness: ${shim_base_url}/readyz"
wait_http_ok "shim" "${shim_base_url}/readyz"

echo "==> checking local computer capability flags"
capabilities_json="$(curl_shim "${shim_base_url}/debug/capabilities")"
save_json "${artifact_dir}/capabilities.json" "${capabilities_json}"
printf '%s\n' "${capabilities_json}" | jq -e '
  .tools.computer.enabled == true and
  .tools.computer.backend == "chat_completions" and
  .probes.computer_runtime.ready == true
' >/dev/null

for scenario in "${scenario_list[@]}"; do
  run_scenario "${scenario}"
done

echo "v3 computer browser harness smoke passed"
