# Test V3 Coding Tools

Last updated: April 24, 2026.

This runbook explains how to verify the current shim-local native coding-tools
slice for `/v1/responses`:

- `shell` with local execution mode
- `apply_patch`
- typed follow-up items:
  - `shell_call_output`
  - `apply_patch_call_output`
- stored reads:
  - `GET /v1/responses/{id}`
  - `GET /v1/responses/{id}/input_items`
- fixture-backed stream replay for local coding-tool items

Use this together with [v3-coding-tools.md](v3-coding-tools.md).

## Last Live Manual Smoke

The live manual smoke was last run on April 24, 2026 against a running local
shim with a Qwen-compatible upstream model behind the shim.

Result: the minimum manual pass checklist below passed for:

- non-stream `shell_call`
- `shell_call_output` follow-up
- stored shell retrieve and `/input_items`
- non-stream `apply_patch_call`
- `apply_patch_call_output` follow-up
- stored apply-patch retrieve and `/input_items`
- shell create-stream with `response.shell_call_command.*`
- generic shell retrieve-stream preserving `shell_call`
- apply-patch create-stream with `response.apply_patch_call_operation_diff.done`
- apply-patch retrieve-stream with
  `response.apply_patch_call_operation_diff.done`

Observed live-run notes:

- A vague shell prompt timed out through the Qwen-compatible upstream. The
  explicit `pwd` prompt in this runbook produced the expected `shell_call`.
- The apply-patch live run returned a structured `update_file` operation with
  an empty `operation.diff`. In that case the stream correctly emitted
  `response.apply_patch_call_operation_diff.done` without a preceding
  `response.apply_patch_call_operation_diff.delta`.
- This live smoke is evidence for the shim-local subset. It is not a claim of
  full hosted shell/container parity or chunk-for-chunk hosted SSE parity.

## What Counts As Passing

For this slice, a pass means all of the following are true:

1. The shim accepts official tool declarations:
   - `{"type":"shell","environment":{"type":"local"}}`
   - `{"type":"apply_patch"}`
2. The first response returns typed tool call items:
   - `shell_call`
   - `apply_patch_call`
3. A follow-up request with typed tool output items is accepted:
   - `shell_call_output`
   - `apply_patch_call_output`
4. Stored follow-up works through `previous_response_id`.
5. Stored reads preserve the typed items in:
   - `GET /v1/responses/{id}`
   - `GET /v1/responses/{id}/input_items`
6. Streaming matches the current shim-local replay contract:
   - create-stream `shell_call` emits:
     - `response.output_item.added`
     - `response.shell_call_command.added`
     - `response.shell_call_command.delta`
     - `response.shell_call_command.done`
     - `response.output_item.done`
   - create-stream and retrieve-stream `apply_patch_call` emit:
     - `response.output_item.added`
     - `response.apply_patch_call_operation_diff.delta` when the upstream item
       has a non-empty `operation.diff`
     - `response.apply_patch_call_operation_diff.done`
     - `response.output_item.done`
   - terminal `response.completed` is preserved

## What Is Not A Failure For This Slice

The following are intentionally out of scope right now:

- hosted container semantics for shell
- `/debug/capabilities` flags for native coding tools
- the existing repo-owned `devstack-smoke` script covering shell/apply-patch
- background-created `shell` retrieve-stream parity; upstream currently fails
  `background + local shell` before any `shell_call` item is emitted
- chunk-for-chunk hosted parity beyond the current fixture-backed local subset

## Recommended Order

Run verification in this order:

1. deterministic repo-owned tests
2. live non-stream manual smoke
3. live retrieve/input-items checks
4. live stream smoke
5. negative validation checks

## 1. Deterministic Repo-Owned Checks

These checks are the stable acceptance path inside this repository. Run them
first.

### Focused Tests

```bash
go test ./internal/httpapi -run 'TestBuildLocalToolLoopTransportPlanConvertsShellToolChoiceToChatShape|TestParseLocalToolLoopChatCompletionRemapsLocalBuiltinShellTool|TestParseLocalToolLoopChatCompletionRemapsLocalBuiltinApplyPatchTool|TestNormalizeCompletedToolCallEventSynthesizesLocalShellReplayEvents|TestNormalizeCompletedToolCallEventSynthesizesLocalApplyPatchReplayEvents|TestResponsesNativeShellToolFollowUpUsesLocalToolLoop|TestResponsesNativeApplyPatchToolFollowUpUsesLocalToolLoop|TestResponsesCreateLocalShellStreamReplaysShellCommandEvents|TestResponsesCreateLocalApplyPatchStreamReplaysDiffEvents|TestResponsesRetrieveLocalApplyPatchStreamReplaysDiffEvents'
```

What this covers:

- `tool_choice: {"type":"shell"}` remaps into shim-local transport
- backend function-call output remaps back into `shell_call`
- backend function-call output remaps back into `apply_patch_call`
- completed-response stream normalization synthesizes local `shell` and
  `apply_patch` SSE families
- stored follow-up accepts `shell_call_output`
- stored follow-up accepts `apply_patch_call_output`
- `/input_items` preserves both typed output families
- create-stream replays `response.shell_call_command.*`
- create/retrieve replay emit `response.apply_patch_call_operation_diff.*`

Relevant tests:

- [internal/httpapi/local_tool_loop_request_test.go](../internal/httpapi/local_tool_loop_request_test.go)
- [internal/httpapi/handlers_responses_test.go](../internal/httpapi/handlers_responses_test.go)
- [internal/httpapi/integration_test.go](../internal/httpapi/integration_test.go)

### Full Regression Bar

```bash
go test ./...
make lint
git diff --check
```

## 2. Live Manual Smoke Prerequisites

You need a running shim with `responses.mode` set to `prefer_local` or
`local_only`.

Two common setups:

- normal local run:
  `go run ./cmd/shim -config ./config.yaml`
- repo dev stack:
  `make devstack-up`

If you use the dev stack, the shim base URL is `http://127.0.0.1:18080`.
Otherwise it is usually `http://127.0.0.1:8080`.

Set the base URL explicitly before running the examples:

```bash
export SHIM_BASE_URL=http://127.0.0.1:8080
export MODEL=gpt-5.4
export AUTHORIZATION=replace-with-your-shim-token
export SHIM_AUTH_HEADER="Authorization: Bearer $AUTHORIZATION"
```

Notes:

- `store: true` is required below because retrieve and `input_items` checks use
  stored response IDs.
- `MODEL` must be a real model name supported by the configured backend and
  capable of tool calling. For the live gate-backed smoke, do not use
  `test-model`; use the same coding-capable model you are validating, such as
  `gpt-5.4` or `gpt-5.3-codex`.
- For Qwen-like upstreams, keep the first-turn smoke prompts explicit and short.
  These models may support OpenAI-style `tool_calls` but still spend a long
  time on vague forced-tool requests. The examples below ask for concrete
  commands such as `pwd`.
- The auth examples use `-H "$SHIM_AUTH_HEADER"` so the shell expands
  `$AUTHORIZATION`. Do not write this header in single quotes, because
  `-H 'Authorization: Bearer $AUTHORIZATION'` sends the literal string
  `$AUTHORIZATION`.
- Use explicit `tool_choice` values for this smoke. Do not use `"auto"` when
  verifying this slice.
- The live manual path still depends on the backend model producing a tool call.
  The deterministic repo-owned proof remains the `go test` path above.

## 3. Shell: Non-Stream Create And Follow-Up

### 3.1 Create A `shell_call`

```bash
first_shell=$(
  curl -sS "$SHIM_BASE_URL/v1/responses" \
    -H 'Content-Type: application/json' \
    -H "$SHIM_AUTH_HEADER" \
    -d @- <<JSON
{
  "model": "$MODEL",
  "store": true,
  "tool_choice": { "type": "shell" },
  "input": [
    {
      "role": "user",
      "content": "Use the shell tool to run exactly this command: pwd"
    }
  ],
  "tools": [
    {
      "type": "shell",
      "environment": { "type": "local" }
    }
  ]
}
JSON
)

echo "$first_shell" | jq
```

Expected checks:

- `.id` is present
- `.output[0].type == "shell_call"`
- `.output[0].call_id` is present
- `.output[0].action.commands` is a non-empty array
- `.output[0].action.timeout_ms` may be present
- `.output[0].action.max_output_length` may be present

Extract the IDs:

```bash
export SHELL_RESPONSE_ID="$(echo "$first_shell" | jq -r '.id')"
export SHELL_CALL_ID="$(echo "$first_shell" | jq -r '.output[0].call_id')"
```

### 3.2 Send `shell_call_output`

```bash
second_shell=$(
  curl -sS "$SHIM_BASE_URL/v1/responses" \
    -H 'Content-Type: application/json' \
    -H "$SHIM_AUTH_HEADER" \
    -d @- <<JSON
{
  "model": "$MODEL",
  "store": true,
  "previous_response_id": "$SHELL_RESPONSE_ID",
  "input": [
    {
      "type": "shell_call_output",
      "call_id": "$SHELL_CALL_ID",
      "max_output_length": 12000,
      "output": [
        {
          "stdout": "tool says hi",
          "stderr": "",
          "outcome": {
            "type": "exit",
            "exit_code": 0
          }
        }
      ]
    }
  ],
  "tools": [
    {
      "type": "shell",
      "environment": { "type": "local" }
    }
  ]
}
JSON
)

echo "$second_shell" | jq
```

Expected checks:

- `.previous_response_id == $SHELL_RESPONSE_ID`
- the response completes successfully
- a final assistant message exists
- `.output_text` or assistant message text contains `tool says hi`

## 4. Shell: Stored Reads

### 4.1 Retrieve The Stored Response

```bash
curl -sS "$SHIM_BASE_URL/v1/responses/$(echo "$second_shell" | jq -r '.id')" \
  -H "$SHIM_AUTH_HEADER" \
  | jq
```

Expected checks:

- returned `.id` matches the second response ID
- `.previous_response_id` still points to the first shell response
- the final message still contains `tool says hi`

### 4.2 Read `input_items`

```bash
curl -sS "$SHIM_BASE_URL/v1/responses/$(echo "$second_shell" | jq -r '.id')/input_items" \
  -H "$SHIM_AUTH_HEADER" \
  | jq
```

Expected checks:

- at least one item has `.type == "shell_call_output"`
- that item has the same `call_id`
- the first output entry contains:
  - `.stdout == "tool says hi"`
  - `.outcome.type == "exit"`
  - `.outcome.exit_code == 0`

Example `jq` filter:

```bash
curl -sS "$SHIM_BASE_URL/v1/responses/$(echo "$second_shell" | jq -r '.id')/input_items" \
  -H "$SHIM_AUTH_HEADER" \
  | jq '.data[] | select(.type == "shell_call_output")'
```

## 5. Apply Patch: Non-Stream Create And Follow-Up

### 5.1 Create An `apply_patch_call`

```bash
first_patch=$(
  curl -sS "$SHIM_BASE_URL/v1/responses" \
    -H 'Content-Type: application/json' \
    -H "$SHIM_AUTH_HEADER" \
    -d @- <<JSON
{
  "model": "$MODEL",
  "store": true,
  "tool_choice": { "type": "apply_patch" },
  "input": [
    {
      "role": "user",
      "content": "The user has the following files:\n<BEGIN_FILES>\n===== game/main.go\npackage game\n\nconst answer = 1\n\nfunc Value() int {\n    return answer\n}\n<END_FILES>\n\nUse apply_patch to change answer from 1 to 2 in game/main.go. Emit patch operations only and do not explain the change yet."
    }
  ],
  "tools": [
    {
      "type": "apply_patch"
    }
  ]
}
JSON
)

echo "$first_patch" | jq
```

Expected checks:

- `.id` is present
- `.output[0].type == "apply_patch_call"`
- `.output[0].call_id` is present
- `.output[0].operation.type` is present
- `.output[0].operation.path` is present
- for `create_file` or `update_file`, `.output[0].operation.diff` may be present

Extract the IDs:

```bash
export PATCH_RESPONSE_ID="$(echo "$first_patch" | jq -r '.id')"
export PATCH_CALL_ID="$(echo "$first_patch" | jq -r '.output[0].call_id')"
```

### 5.2 Send `apply_patch_call_output`

```bash
second_patch=$(
  curl -sS "$SHIM_BASE_URL/v1/responses" \
    -H 'Content-Type: application/json' \
    -H "$SHIM_AUTH_HEADER" \
    -d @- <<JSON
{
  "model": "$MODEL",
  "store": true,
  "previous_response_id": "$PATCH_RESPONSE_ID",
  "input": [
    {
      "type": "apply_patch_call_output",
      "call_id": "$PATCH_CALL_ID",
      "status": "completed",
      "output": "patched cleanly"
    }
  ],
  "tools": [
    {
      "type": "apply_patch"
    }
  ]
}
JSON
)

echo "$second_patch" | jq
```

Expected checks:

- `.previous_response_id == $PATCH_RESPONSE_ID`
- the response completes successfully
- a final assistant message exists
- `.output_text` or assistant message text contains `patched cleanly`

## 6. Apply Patch: Stored Reads

### 6.1 Retrieve The Stored Response

```bash
curl -sS "$SHIM_BASE_URL/v1/responses/$(echo "$second_patch" | jq -r '.id')" \
  -H "$SHIM_AUTH_HEADER" \
  | jq
```

Expected checks:

- returned `.id` matches the second patch response ID
- `.previous_response_id` still points to the first patch response
- the final message still contains `patched cleanly`

### 6.2 Read `input_items`

```bash
curl -sS "$SHIM_BASE_URL/v1/responses/$(echo "$second_patch" | jq -r '.id')/input_items" \
  -H "$SHIM_AUTH_HEADER" \
  | jq
```

Expected checks:

- at least one item has `.type == "apply_patch_call_output"`
- that item has the same `call_id`
- `.status == "completed"`
- `.output == "patched cleanly"`

Example `jq` filter:

```bash
curl -sS "$SHIM_BASE_URL/v1/responses/$(echo "$second_patch" | jq -r '.id')/input_items" \
  -H "$SHIM_AUTH_HEADER" \
  | jq '.data[] | select(.type == "apply_patch_call_output")'
```

## 7. Stream Checks

These are manual smoke checks for the current fixture-backed stream subset.

### 7.1 Shell Create-Stream

```bash
curl -N -sS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "$SHIM_AUTH_HEADER" \
  -d @- <<JSON
{
  "model": "$MODEL",
  "store": true,
  "stream": true,
  "tool_choice": { "type": "shell" },
  "input": [
    {
      "role": "user",
      "content": "Use the shell tool to run exactly this command: pwd"
    }
  ],
  "tools": [
    {
      "type": "shell",
      "environment": { "type": "local" }
    }
  ]
}
JSON
```

Expected checks:

- there is an `event: response.output_item.added`
- there is an `event: response.shell_call_command.added`
- there is an `event: response.shell_call_command.delta`
- there is an `event: response.shell_call_command.done`
- there is an `event: response.output_item.done`
- `response.output_item.added` contains `"type":"shell_call"` and an empty
  `action.commands` array
- `response.output_item.done` contains the finalized `shell_call` with populated
  `action.commands`
- there is an `event: response.completed`

### 7.2 Shell Retrieve-Stream

```bash
curl -N -sS "$SHIM_BASE_URL/v1/responses/$SHELL_RESPONSE_ID?stream=true" \
  -H "$SHIM_AUTH_HEADER"
```

Expected checks:

- there is an `event: response.output_item.added`
- there is an `event: response.output_item.done`
- the replayed item type is still `shell_call`
- do not require `response.shell_call_command.*` here yet
- there is an `event: response.completed`

### 7.3 Apply Patch Create-Stream

```bash
curl -N -sS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "$SHIM_AUTH_HEADER" \
  -d @- <<JSON
{
  "model": "$MODEL",
  "store": true,
  "stream": true,
  "tool_choice": { "type": "apply_patch" },
  "input": [
    {
      "role": "user",
      "content": "The user has the following files:\n<BEGIN_FILES>\n===== game/main.go\npackage game\n\nconst answer = 1\n\nfunc Value() int {\n    return answer\n}\n<END_FILES>\n\nUse apply_patch to change answer from 1 to 2 in game/main.go. Emit patch operations only and do not explain the change yet."
    }
  ],
  "tools": [
    {
      "type": "apply_patch"
    }
  ]
}
JSON
```

Expected checks:

- there is an `event: response.output_item.added`
- there is an `event: response.apply_patch_call_operation_diff.delta` if the
  upstream item has a non-empty `operation.diff`
- there is an `event: response.apply_patch_call_operation_diff.done`
- there is an `event: response.output_item.done`
- `response.output_item.added` contains `"type":"apply_patch_call"` with an
  empty `operation.diff`
- `response.output_item.done` contains the finalized `apply_patch_call` with the
  finalized operation path/type; `operation.diff` may be absent or empty when
  the upstream model only returns a structured file operation
- there is an `event: response.completed`

### 7.4 Apply Patch Retrieve-Stream

```bash
curl -N -sS "$SHIM_BASE_URL/v1/responses/$PATCH_RESPONSE_ID?stream=true" \
  -H "$SHIM_AUTH_HEADER"
```

Expected checks:

- there is an `event: response.output_item.added`
- there is an `event: response.apply_patch_call_operation_diff.delta` if the
  stored item has a non-empty `operation.diff`
- there is an `event: response.apply_patch_call_operation_diff.done`
- there is an `event: response.output_item.done`
- the replayed item type is still `apply_patch_call`
- `response.output_item.added` still shows an empty `operation.diff`
- `response.output_item.done` contains the finalized operation; the finalized
  diff may be empty if the stored call had no diff
- there is an `event: response.completed`

## 8. Negative Validation Checks

These are quick checks for obvious request-shape regressions.

### 8.1 Reject `shell` Without `environment.type: "local"`

```bash
curl -sS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "$SHIM_AUTH_HEADER" \
  -d @- <<JSON | jq
{
  "model": "$MODEL",
  "tool_choice": { "type": "shell" },
  "input": "bad shell request",
  "tools": [
    {
      "type": "shell"
    }
  ]
}
JSON
```

Expected checks:

- request fails
- the error points at `tools`
- the message says local shell requires `environment.type` = `local`

### 8.2 Reject `shell_call_output` Without `call_id`

```bash
curl -sS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "$SHIM_AUTH_HEADER" \
  -d @- <<JSON | jq
{
  "model": "$MODEL",
  "previous_response_id": "resp_missing",
  "input": [
    {
      "type": "shell_call_output",
      "output": [
        {
          "stdout": "oops"
        }
      ]
    }
  ]
}
JSON
```

Expected checks:

- request fails
- the message says `shell_call_output call_id is required`

### 8.3 Reject `apply_patch_call_output` Without `status`

```bash
curl -sS "$SHIM_BASE_URL/v1/responses" \
  -H 'Content-Type: application/json' \
  -H "$SHIM_AUTH_HEADER" \
  -d @- <<JSON | jq
{
  "model": "$MODEL",
  "previous_response_id": "resp_missing",
  "input": [
    {
      "type": "apply_patch_call_output",
      "call_id": "call_missing_status"
    }
  ]
}
JSON
```

Expected checks:

- request fails
- the message says `apply_patch_call_output status is required`

## 9. Common Failure Interpretations

### The first request returns a normal assistant message instead of a tool call

Interpretation:

- the backend model ignored the tool request
- or the shim is not on the local tool-loop path
- or a Qwen-like upstream supports `tool_calls` but struggles with a vague
  forced-tool prompt

What to check:

- `responses.mode` is `prefer_local` or `local_only`
- the tool declaration is exactly correct
- `tool_choice` is explicitly set to `{"type":"shell"}` or
  `{"type":"apply_patch"}`
- for shell, use an explicit command such as `pwd` in the prompt before trying a
  more open-ended request
- rerun the deterministic `go test` acceptance path first

### Shell retrieve-stream shows only generic `response.output_item.*` events

Interpretation:

- this is still expected for `shell`

That is a pass if the embedded item type is still:

- `shell_call`

### Apply-patch stream is missing `response.apply_patch_call_operation_diff.done`

Interpretation:

- create-stream or stored retrieve replay for `apply_patch_call` regressed

That is a real failure for this slice.

Missing only `response.apply_patch_call_operation_diff.delta` is not a failure
when the item has an empty `operation.diff`; in that case the stream should
still emit `response.apply_patch_call_operation_diff.done`.

### `input_items` does not show the tool output item

Interpretation:

- stored follow-up or lineage reconstruction is broken

That is a real failure for this slice.

## 10. Minimum Manual Pass Checklist

If you want the shortest realistic live pass, verify these six things:

1. shell create returns `shell_call`
2. shell follow-up accepts `shell_call_output`
3. shell `input_items` preserves `shell_call_output`
4. apply-patch create returns `apply_patch_call`
5. apply-patch follow-up accepts `apply_patch_call_output`
6. apply-patch `input_items` preserves `apply_patch_call_output`

If you also want stream confidence, add:

7. shell create-stream emits `response.shell_call_command.*`
8. shell retrieve-stream still preserves `shell_call` generically
9. apply-patch create-stream emits `response.apply_patch_call_operation_diff.*`
10. apply-patch retrieve-stream emits
    `response.apply_patch_call_operation_diff.*`
