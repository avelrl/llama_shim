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
  SHIM_BASE_URL=http://127.0.0.1:8080 \
  MODEL=gpu/qwen3-30b-instruct \
  ./scripts/v4-chat-agent-smoke.sh

Optional:
  CHAT_AGENT_MODEL=provider/model
  SHIM_AUTH_HEADER='Authorization: Bearer <token>'
  SHIM_API_KEY=<token>
  CHAT_AGENT_SMOKE_SCENARIOS=all
  CHAT_AGENT_SMOKE_ARTIFACT_DIR=.tmp/v4-chat-agent-smoke
  CHAT_AGENT_SMOKE_RUN_ID=manual

This smoke runs small OpenCode/Aider-style coding tasks through
/v1/chat/completions. It is intentionally separate from Codex eval: the model
gets OpenAI Chat function tools, this script executes local file/command tools,
and the final workspace is checked directly.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd curl
require_cmd date
require_cmd dirname
require_cmd jq
require_cmd mkdir
require_cmd python3
require_cmd tr

shim_base_url="${SHIM_BASE_URL:-http://127.0.0.1:8080}"
model="${CHAT_AGENT_MODEL:-${MODEL:-devstack-model}}"
scenarios="${CHAT_AGENT_SMOKE_SCENARIOS:-all}"
artifact_root="${CHAT_AGENT_SMOKE_ARTIFACT_DIR:-.tmp/v4-chat-agent-smoke}"
run_id="${CHAT_AGENT_SMOKE_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"

slugify() {
  local value="$1"
  value="$(printf '%s' "${value}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9._-' '-')"
  value="${value#-}"
  value="${value%-}"
  if [[ -z "${value}" ]]; then
    value="value"
  fi
  printf '%s' "${value}"
}

csv_to_words() {
  local value="$1"
  value="${value//,/ }"
  printf '%s' "${value}"
}

scenario_enabled() {
  local name="$1"
  if [[ "${scenarios}" == "all" ]]; then
    return 0
  fi
  for item in $(csv_to_words "${scenarios}"); do
    if [[ "${item}" == "${name}" ]]; then
      return 0
    fi
  done
  return 1
}

auth_args=()
if [[ -n "${SHIM_AUTH_HEADER:-}" ]]; then
  auth_args=(-H "${SHIM_AUTH_HEADER}")
elif [[ -n "${SHIM_API_KEY:-}" ]]; then
  auth_args=(-H "Authorization: Bearer ${SHIM_API_KEY}")
elif [[ -n "${GW_API_KEY:-}" ]]; then
  auth_args=(-H "Authorization: Bearer ${GW_API_KEY}")
elif [[ -n "${OPENAI_API_KEY:-}" ]]; then
  auth_args=(-H "Authorization: Bearer ${OPENAI_API_KEY}")
fi

curl_with_auth() {
  if [[ "${#auth_args[@]}" -gt 0 ]]; then
    curl "${auth_args[@]}" "$@"
  else
    curl "$@"
  fi
}

run_dir="${artifact_root%/}/$(slugify "${model}")_${run_id}"
mkdir -p "${run_dir}"

echo "==> waiting for shim readiness: ${shim_base_url%/}/readyz"
for _ in $(seq 1 60); do
  if curl -fsS "${shim_base_url%/}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "${shim_base_url%/}/readyz" >"${run_dir}/readyz.json"

post_json() {
  local payload_file="$1"
  local response_file="$2"
  local status_file="$3"
  local status

  status="$(
    curl_with_auth -sS -o "${response_file}" -w '%{http_code}' \
      -H 'Content-Type: application/json' \
      --data-binary @"${payload_file}" \
      "${shim_base_url%/}/v1/chat/completions"
  )"
  printf '%s' "${status}" >"${status_file}"
  if [[ "${status}" != 2* ]]; then
    echo "POST /v1/chat/completions failed with HTTP ${status}" >&2
    cat "${response_file}" >&2
    return 1
  fi
}

chat_tools_json() {
  cat <<'JSON'
[
  {
    "type": "function",
    "function": {
      "name": "list_files",
      "description": "List task workspace files before choosing paths to inspect or edit.",
      "parameters": {
        "type": "object",
        "additionalProperties": false,
        "properties": {},
        "required": []
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "read_file",
      "description": "Read a UTF-8 text file from the current task workspace.",
      "parameters": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "path": {"type": "string"}
        },
        "required": ["path"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "write_file",
      "description": "Write a UTF-8 text file in the current task workspace.",
      "parameters": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "path": {"type": "string"},
          "content": {"type": "string"}
        },
        "required": ["path", "content"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "replace_text",
      "description": "Replace one exact text span in a UTF-8 text file.",
      "parameters": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "path": {"type": "string"},
          "old": {"type": "string"},
          "new": {"type": "string"}
        },
        "required": ["path", "old", "new"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "run_command",
      "description": "Run an allowlisted command in the current task workspace.",
      "parameters": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "command": {"type": "string"}
        },
        "required": ["command"]
      }
    }
  }
]
JSON
}

safe_path() {
  local rel="$1"
  if [[ -z "${rel}" || "${rel}" == /* || "${rel}" == *".."* ]]; then
    return 1
  fi
  return 0
}

tool_error() {
  jq -cn --arg error "$1" '{ok:false,error:$error}'
}

execute_tool() {
  local workspace="$1"
  local name="$2"
  local arguments="$3"
  local args path file old new content command output status

  if ! args="$(printf '%s' "${arguments}" | jq -c '.')" ; then
    tool_error "tool arguments are not valid JSON"
    return 0
  fi

  case "${name}" in
    list_files)
      python3 - "${workspace}" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
skip = {".gocache", ".gotmp"}
files = []
for path in sorted(root.rglob("*")):
    rel = path.relative_to(root)
    if any(part in skip for part in rel.parts):
        continue
    if path.is_file():
        files.append(str(rel))
print(json.dumps({"ok": True, "files": files}))
PY
      ;;
    read_file)
      path="$(jq -r '.path // empty' <<<"${args}")"
      if ! safe_path "${path}"; then
        tool_error "unsafe path"
        return 0
      fi
      file="${workspace}/${path}"
      if [[ ! -f "${file}" ]]; then
        tool_error "file not found"
        return 0
      fi
      jq -cn --rawfile content "${file}" '{ok:true,content:$content}'
      ;;
    write_file)
      path="$(jq -r '.path // empty' <<<"${args}")"
      content="$(jq -r '.content // ""' <<<"${args}")"
      if ! safe_path "${path}"; then
        tool_error "unsafe path"
        return 0
      fi
      file="${workspace}/${path}"
      mkdir -p "$(dirname "${file}")"
      printf '%s' "${content}" >"${file}"
      jq -cn --arg path "${path}" '{ok:true,path:$path}'
      ;;
    replace_text)
      path="$(jq -r '.path // empty' <<<"${args}")"
      old="$(jq -r '.old // empty' <<<"${args}")"
      new="$(jq -r '.new // empty' <<<"${args}")"
      if ! safe_path "${path}"; then
        tool_error "unsafe path"
        return 0
      fi
      file="${workspace}/${path}"
      if [[ ! -f "${file}" ]]; then
        tool_error "file not found"
        return 0
      fi
      if python3 - "${file}" "${old}" "${new}" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
old = sys.argv[2]
new = sys.argv[3]
text = path.read_text()
if old not in text:
    print("old text not found", file=sys.stderr)
    sys.exit(2)
path.write_text(text.replace(old, new, 1))
PY
      then
        jq -cn --arg path "${path}" '{ok:true,path:$path}'
      else
        tool_error "old text not found"
      fi
      ;;
    run_command)
      command="$(jq -r '.command // empty' <<<"${args}")"
      case "${command}" in
        "go test ./..." | "go test ./... -v")
          mkdir -p "${workspace}/.gocache" "${workspace}/.gotmp"
          set +e
          output="$(cd "${workspace}" && GOCACHE="${workspace}/.gocache" GOTMPDIR="${workspace}/.gotmp" ${command} 2>&1)"
          status=$?
          set -e
          jq -cn --arg output "${output}" --argjson status "${status}" '{ok:($status == 0),exit_code:$status,output:$output}'
          ;;
        *)
          tool_error "command not allowlisted"
          ;;
      esac
      ;;
    *)
      tool_error "unknown tool"
      ;;
  esac
}

append_json_array_item() {
  local array_json="$1"
  local item_json="$2"
  jq -c --argjson item "${item_json}" '. + [$item]' <<<"${array_json}"
}

final_matches() {
  local text="$1"
  local expected="$2"
  local last_line

  if [[ "${text}" == "${expected}" ]]; then
    return 0
  fi

  last_line="$(printf '%s\n' "${text}" | awk 'NF { line = $0 } END { print line }')"
  [[ "${last_line}" == "${expected}" ]]
}

run_chat_task() {
  local scenario="$1"
  local workspace="$2"
  local prompt="$3"
  local expected_final="$4"
  local required_command="${5:-}"
  local task_dir="${run_dir}/${scenario}"
  local messages tools turn payload response status assistant tool_calls_len final_text call_count call_idx
  local call_id call_name call_args call_command tool_output tool_msg command_seen=0

  mkdir -p "${task_dir}"
  tools="$(chat_tools_json)"
  messages="$(
    jq -cn \
      --arg prompt "${prompt}" \
      '[
        {
          role:"system",
          content:"You are a chat-first coding agent. Use list_files before guessing paths. Use the provided tools to inspect and edit files. Do not guess file contents. After tools prove the task is complete, reply with the exact requested final marker and no extra prose."
        },
        {role:"user", content:$prompt}
      ]'
  )"

  echo "==> chat-agent scenario: ${scenario}"
  for turn in $(seq 1 12); do
    payload="${task_dir}/turn-${turn}.request.json"
    response="${task_dir}/turn-${turn}.response.json"
    status="${task_dir}/turn-${turn}.status"
    jq -cn \
      --arg model "${model}" \
      --argjson messages "${messages}" \
      --argjson tools "${tools}" \
      '{
        model:$model,
        messages:$messages,
        tools:$tools,
        tool_choice:"auto",
        temperature:0
      }' >"${payload}"

    post_json "${payload}" "${response}" "${status}"
    assistant="$(jq -c '.choices[0].message' "${response}")"
    messages="$(append_json_array_item "${messages}" "${assistant}")"
    tool_calls_len="$(jq '.tool_calls // [] | length' <<<"${assistant}")"

    if [[ "${tool_calls_len}" -eq 0 ]]; then
      final_text="$(jq -r '.content // ""' <<<"${assistant}")"
      printf '%s\n' "${final_text}" >"${task_dir}/final.txt"
      if ! final_matches "${final_text}" "${expected_final}"; then
        echo "scenario ${scenario} final mismatch:" >&2
        printf 'got: %s\nwant: %s\n' "${final_text}" "${expected_final}" >&2
        exit 1
      fi
      if [[ -n "${required_command}" && "${command_seen}" -ne 1 ]]; then
        echo "scenario ${scenario} did not call required command: ${required_command}" >&2
        exit 1
      fi
      return 0
    fi

    call_count="${tool_calls_len}"
    for call_idx in $(seq 0 $((call_count - 1))); do
      call_id="$(jq -r --argjson idx "${call_idx}" '.tool_calls[$idx].id' <<<"${assistant}")"
      call_name="$(jq -r --argjson idx "${call_idx}" '.tool_calls[$idx].function.name' <<<"${assistant}")"
      call_args="$(jq -r --argjson idx "${call_idx}" '.tool_calls[$idx].function.arguments // "{}"' <<<"${assistant}")"
      if [[ "${call_name}" == "run_command" ]]; then
        call_command="$(printf '%s' "${call_args}" | jq -r '.command // empty' 2>/dev/null || true)"
        if [[ "${call_command}" == "${required_command}" ]]; then
          command_seen=1
        fi
      fi
      tool_output="$(execute_tool "${workspace}" "${call_name}" "${call_args}")"
      printf '%s\n' "${tool_output}" >"${task_dir}/turn-${turn}.tool-${call_idx}.json"
      tool_msg="$(jq -cn --arg call_id "${call_id}" --arg content "${tool_output}" '{role:"tool",tool_call_id:$call_id,content:$content}')"
      messages="$(append_json_array_item "${messages}" "${tool_msg}")"
    done
  done

  echo "scenario ${scenario} exhausted chat-agent turns" >&2
  exit 1
}

run_stream_text() {
  local scenario="stream_text"
  local task_dir="${run_dir}/${scenario}"
  local payload="${task_dir}/request.json"
  local response="${task_dir}/response.sse"
  local text

  mkdir -p "${task_dir}"
  echo "==> chat-agent scenario: ${scenario}"
  jq -cn \
    --arg model "${model}" \
    '{
      model:$model,
      stream:true,
      temperature:0,
      messages:[
        {role:"system",content:"Reply with exactly HELLO and nothing else."},
        {role:"user",content:"go"}
      ]
    }' >"${payload}"

  curl_with_auth -fsS \
    -H 'Content-Type: application/json' \
    --data-binary @"${payload}" \
    "${shim_base_url%/}/v1/chat/completions" >"${response}"

  text="$(
    awk '/^data: / { sub(/^data: /, ""); if ($0 != "[DONE]") print }' "${response}" |
      jq -r '.choices[0].delta.content? // empty' |
      tr -d '\n'
  )"
  printf '%s\n' "${text}" >"${task_dir}/text.txt"
  if [[ "${text}" != "HELLO" ]]; then
    echo "stream_text mismatch:" >&2
    printf 'got: %s\nwant: HELLO\n' "${text}" >&2
    exit 1
  fi
}

prepare_workspace() {
  local scenario="$1"
  local workspace
  workspace="$(cd "${run_dir}" && pwd)/${scenario}/workspace"
  rm -rf "${workspace}"
  mkdir -p "${workspace}"
  printf '%s' "${workspace}"
}

run_read_file() {
  local workspace
  workspace="$(prepare_workspace read_file)"
  printf 'secret-code=alpha-42\n' >"${workspace}/notes.txt"
  run_chat_task \
    read_file \
    "${workspace}" \
    'Use read_file to inspect notes.txt. Reply exactly READ:alpha-42.' \
    'READ:alpha-42'
}

run_basic_patch() {
  local workspace actual
  workspace="$(prepare_workspace basic_patch)"
  printf 'name = llama_shim\nstatus = TODO\n' >"${workspace}/smoke_target.txt"
  run_chat_task \
    basic_patch \
    "${workspace}" \
    'Use tools to replace `status = TODO` with `status = patched-by-chat-agent` in smoke_target.txt. Reply exactly PATCHED.' \
    'PATCHED'
  actual="$(cat "${workspace}/smoke_target.txt")"
  if [[ "${actual}" != $'name = llama_shim\nstatus = patched-by-chat-agent\n' && "${actual}" != $'name = llama_shim\nstatus = patched-by-chat-agent' ]]; then
    echo "basic_patch did not update smoke_target.txt as expected" >&2
    exit 1
  fi
}

run_bugfix_go() {
  local workspace
  workspace="$(prepare_workspace bugfix_go)"
  cat >"${workspace}/go.mod" <<'EOF'
module chatagentsmoke

go 1.22
EOF
  cat >"${workspace}/calc.go" <<'EOF'
package chatagentsmoke

func Add(a, b int) int {
	return a - b
}
EOF
  cat >"${workspace}/calc_test.go" <<'EOF'
package chatagentsmoke

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
EOF
  run_chat_task \
    bugfix_go \
    "${workspace}" \
    'Use list_files to find the Go files, inspect them, fix the Add bug, call run_command with exactly `go test ./...`, and reply exactly BUGFIXED after the test passes.' \
    'BUGFIXED' \
    'go test ./...'
  grep -q 'return a + b' "${workspace}/calc.go" || {
    echo "bugfix_go did not patch calc.go as expected" >&2
    exit 1
  }
  (cd "${workspace}" && GOCACHE="${workspace}/.gocache" GOTMPDIR="${workspace}/.gotmp" go test ./...)
}

run_multi_file() {
  local workspace
  workspace="$(prepare_workspace multi_file)"
  mkdir -p "${workspace}/app"
  printf 'mode=initial\nfeature=disabled\n' >"${workspace}/app/config.txt"
  printf 'status=stale\n' >"${workspace}/app/status.txt"
  run_chat_task \
    multi_file \
    "${workspace}" \
    'Use tools to update app/config.txt so it contains `mode=matrix` and `feature=enabled`, and update app/status.txt so it contains `status=updated`. Reply exactly MULTIFILE.' \
    'MULTIFILE'
  if [[ "$(cat "${workspace}/app/config.txt")" != $'mode=matrix\nfeature=enabled\n' && "$(cat "${workspace}/app/config.txt")" != $'mode=matrix\nfeature=enabled' ]]; then
    echo "multi_file did not update app/config.txt as expected" >&2
    exit 1
  fi
  if [[ "$(cat "${workspace}/app/status.txt")" != $'status=updated\n' && "$(cat "${workspace}/app/status.txt")" != "status=updated" ]]; then
    echo "multi_file did not update app/status.txt as expected" >&2
    exit 1
  fi
}

scenario_enabled stream_text && run_stream_text
scenario_enabled read_file && run_read_file
scenario_enabled basic_patch && run_basic_patch
scenario_enabled bugfix_go && run_bugfix_go
scenario_enabled multi_file && run_multi_file

cat >"${run_dir}/summary.json" <<EOF
{"status":"passed","model":"${model}","scenarios":"${scenarios}","artifact_dir":"${run_dir}"}
EOF

echo "v4 chat-agent smoke passed"
echo "artifacts: ${run_dir}"
