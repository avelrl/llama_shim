package devstackfixture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandlerExposesHealthAndModels(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var health map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	require.Equal(t, "ok", health["status"])

	resp, err = server.Client().Get(server.URL + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var models map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&models))
	require.Equal(t, "list", models["object"])
	data, ok := models["data"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, data)
	require.Equal(t, DefaultModel, data[0].(map[string]any)["id"])
}

func TestHandlerExposesComputerHarnessPage(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/pages/computer-harness")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	text := string(body)
	require.Contains(t, text, "Computer Harness Fixture")
	require.Contains(t, text, `id="harness-input"`)
	require.Contains(t, text, "coordinate 636,343")
}

func TestHandlerChatCompletionsUsesDeterministicRules(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Remember code 777. Reply READY."},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok := response["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "READY", message["content"])

	payload["messages"] = []map[string]any{
		{"role": "system", "content": "Remember: code=777. Reply OK."},
		{"role": "user", "content": "What is the code?"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok = response["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	message = choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "777", message["content"])
}

func TestHandlerChatCompletionsAcceptsMultimodalContentParts(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Say OK and nothing else."},
					{
						"type": "image_url",
						"image_url": map[string]any{
							"url":    "data:image/png;base64,ZmFrZQ==",
							"detail": "original",
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok := response["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "OK", message["content"])
}

func TestHandlerChatCompletionsPlansLocalComputerLoop(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "system", "content": "You are the shim-local computer planner."},
			{"role": "user", "content": "Use the computer tool. First request a screenshot."},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok := response["choices"].([]any)
	require.True(t, ok)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.JSONEq(t, `{"decision":"computer_call","actions":[{"type":"screenshot"}]}`, asString(message["content"]))

	payload["messages"] = []map[string]any{
		{"role": "system", "content": "You are the shim-local computer planner."},
		{"role": "user", "content": "After you receive the screenshot, click it and type penguin."},
		{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "computer_call_output screenshot received for call_id call_test. Use this as the latest UI state."},
				{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,ZmFrZQ==", "detail": "original"}},
			},
		},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok = response["choices"].([]any)
	require.True(t, ok)
	message = choices[0].(map[string]any)["message"].(map[string]any)
	require.JSONEq(t, `{"decision":"computer_call","actions":[{"type":"click","button":"left","keys":null,"x":636,"y":343},{"type":"type","text":"penguin"}]}`, asString(message["content"]))
}

func TestHandlerChatCompletionsPlansAndCompletesMCPToolCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Roll 2d4+1 and return only the numeric result."},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "mcp__dmcp__roll",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "mcp__dmcp__roll", function["name"])
	require.JSONEq(t, `{"diceRollExpression":"2d4 + 1"}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "user", "content": "Roll 2d4+1 and return only the numeric result."},
		{"role": "tool", "tool_call_id": "call_devstack_mcp_1", "content": "4"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "4", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestHandlerChatCompletionsPlansAndCompletesBuiltinShellToolCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Use the shell tool to run exactly this command: pwd"},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "__llama_shim_builtin_shell",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "__llama_shim_builtin_shell", function["name"])
	require.JSONEq(t, `{"action":{"commands":["pwd"],"timeout_ms":30000,"max_output_length":12000}}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "user", "content": "Use the shell tool to run exactly this command: pwd"},
		{"role": "tool", "tool_call_id": "call_devstack_builtin_1", "content": "tool says hi"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "tool says hi", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestHandlerChatCompletionsPlansAndCompletesBuiltinApplyPatchToolCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Use apply_patch to change answer from 1 to 2 in game/main.go."},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "__llama_shim_builtin_apply_patch",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "__llama_shim_builtin_apply_patch", function["name"])
	require.JSONEq(t, `{"operation":{"type":"update_file","path":"game/main.go","diff":"*** Begin Patch\n*** Update File: game/main.go\n@@\n-const answer = 1\n+const answer = 2\n*** End Patch\n"}}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "user", "content": "Use apply_patch to change answer from 1 to 2 in game/main.go."},
		{"role": "tool", "tool_call_id": "call_devstack_builtin_1", "content": "patched cleanly"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "patched cleanly", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestHandlerChatCompletionsPlansAndCompletesCodexExecCommandToolCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "system", "content": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
			{"role": "user", "content": "Use exec_command to run pwd, then reply READY."},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "exec_command",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "exec_command", function["name"])
	require.JSONEq(t, `{"cmd":"pwd","yield_time_ms":1000,"max_output_tokens":12000}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "system", "content": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
		{"role": "user", "content": "Use exec_command to run pwd, then reply READY."},
		{"role": "tool", "tool_call_id": "call_devstack_codex_1", "content": "/workdir"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "READY", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestHandlerChatCompletionsPlansAndCompletesCodexShellToolCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "system", "content": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
			{"role": "user", "content": "Use the shell tool to run pwd, then reply READY."},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "shell",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "shell", function["name"])
	require.JSONEq(t, `{"command":["bash","-lc","pwd"],"timeout_ms":1000,"workdir":"."}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "system", "content": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
		{"role": "user", "content": "Use the shell tool to run pwd, then reply READY."},
		{"role": "tool", "tool_call_id": "call_devstack_codex_1", "content": "/workdir"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "READY", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestHandlerChatCompletionsPlansAndCompletesCodexCodingTaskToolCall(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "system", "content": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
			{"role": "user", "content": "This is the Codex coding task smoke. Use exec_command to update smoke_target.txt by replacing `status = TODO` with `status = patched-by-codex`. Then reply PATCHED."},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "exec_command",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "exec_command", function["name"])
	require.JSONEq(t, `{"cmd":"python3 -c \"import os; from pathlib import Path; p=Path(os.environ['LLAMA_SHIM_CODEX_SMOKE_TARGET']); p.write_text(p.read_text().replace('status = TODO', 'status = patched-by-codex')); print('patched smoke_target.txt')\"","yield_time_ms":60000,"max_output_tokens":12000}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "system", "content": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
		{"role": "user", "content": "This is the Codex coding task smoke. Use exec_command to update smoke_target.txt by replacing `status = TODO` with `status = patched-by-codex`. Then reply PATCHED."},
		{"role": "tool", "tool_call_id": "call_devstack_codex_1", "content": "patched smoke_target.txt"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "PATCHED", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestChatCompletionsCodexTaskMatrixRules(t *testing.T) {
	tools := []chatTool{
		{
			Type: "function",
			Function: chatToolFunction{
				Name: "exec_command",
			},
		},
	}
	cases := []struct {
		name          string
		prompt        string
		commandMarker string
		toolOutput    string
		final         string
	}{
		{
			name:          "bugfix go",
			prompt:        "This is the Codex task matrix bugfix go case. Use exec_command to fix calc.go and reply BUGFIXED.",
			commandMarker: "bugfix go task passed",
			toolOutput:    "bugfix go task passed",
			final:         "BUGFIXED",
		},
		{
			name:          "mixed bugfix",
			prompt:        "This is a single-turn coding task. First include exactly MIXED_CAUSE_FOUND, then use tools to fix mathutil.go and finish with MIXED_BUGFIX_OK.",
			commandMarker: "mixed bugfix task passed",
			toolOutput:    "mixed bugfix task passed",
			final:         "MIXED_CAUSE_FOUND: Add used subtraction.\nMIXED_BUGFIX_OK",
		},
		{
			name:          "plan doc",
			prompt:        "This is the Codex task matrix plan doc case. Use exec_command to write PLAN.md and reply PLANNED.",
			commandMarker: "PLAN.md",
			toolOutput:    "plan task written",
			final:         "PLANNED",
		},
		{
			name:          "multi file",
			prompt:        "This is the Codex task matrix multi file case. Use exec_command to update app files and reply MULTIFILE.",
			commandMarker: "multi file task updated",
			toolOutput:    "multi file task updated",
			final:         "MULTIFILE",
		},
		{
			name:          "command recovery",
			prompt:        "This is the Codex command recovery case. Use exec_command to run `sh verify.sh`, update status.txt, verify, and reply RECOVERED.",
			commandMarker: "verify.sh",
			toolOutput:    "recovery task verified",
			final:         "RECOVERED",
		},
		{
			name:          "command timeout recovery",
			prompt:        "This is the Codex command timeout recovery case. Use exec_command to run `sh slow.sh` with a short timeout, then run `sh fast.sh`, and reply TIMEOUT_RECOVERED.",
			commandMarker: "sh slow.sh",
			toolOutput:    "TIMEOUT_RECOVERED",
			final:         "TIMEOUT_RECOVERED",
		},
		{
			name:          "no edit",
			prompt:        "This is the Codex no-edit safety case. Use exec_command to read README.md and reply NO_EDIT_OK.",
			commandMarker: "cat README.md",
			toolOutput:    "do-not-edit-token",
			final:         "NO_EDIT_OK",
		},
		{
			name:          "stderr handling",
			prompt:        "This is the Codex stderr handling case. Use exec_command to run `sh emit_stderr.sh` and reply STDERR_OK.",
			commandMarker: "sh emit_stderr.sh",
			toolOutput:    "stdout-token\nstderr-token",
			final:         "STDERR_OK",
		},
		{
			name:          "long stdout",
			prompt:        "This is the Codex long stdout case. Use exec_command to run `sh long_stdout.sh` and reply LONG_STDOUT_OK.",
			commandMarker: "sh long_stdout.sh",
			toolOutput:    "line-001\nLONG_STDOUT_DONE",
			final:         "LONG_STDOUT_OK",
		},
		{
			name:          "command pipeline",
			prompt:        "This is the Codex command pipeline case. Use exec_command to create pipeline.txt and reply PIPELINE_OK.",
			commandMarker: "pipeline task passed",
			toolOutput:    "ALPHA\nBETA\npipeline task passed",
			final:         "PIPELINE_OK",
		},
		{
			name:          "eval read file",
			prompt:        "This is the Codex eval read file case. Use exec_command to read README.md and reply READ_OK.",
			commandMarker: "cat README.md",
			toolOutput:    "codex-smoke-token: llama-shim-42",
			final:         "READ_OK",
		},
		{
			name:          "js bugfix",
			prompt:        "This is the Codex JS bugfix case. Use exec_command to fix math.js and reply JS_BUGFIX_OK.",
			commandMarker: "js bugfix task passed",
			toolOutput:    "js bugfix task passed",
			final:         "JS_BUGFIX_OK",
		},
		{
			name:          "python bugfix",
			prompt:        "This is the Codex Python bugfix case. Use exec_command to fix mathutil.py and reply PY_BUGFIX_OK.",
			commandMarker: "python bugfix task passed",
			toolOutput:    "python bugfix task passed",
			final:         "PY_BUGFIX_OK",
		},
		{
			name:          "json config",
			prompt:        "This is the Codex JSON config edit case. Use exec_command to update config.json and reply JSON_CONFIG_OK.",
			commandMarker: "json config task updated",
			toolOutput:    "json config task updated",
			final:         "JSON_CONFIG_OK",
		},
		{
			name:          "env var",
			prompt:        "This is the Codex env var case. Use exec_command to capture CODEX_EVAL_MAGIC and reply ENV_VAR_OK.",
			commandMarker: "CODEX_EVAL_MAGIC",
			toolOutput:    "EVAL_MAGIC=phase3-core",
			final:         "ENV_VAR_OK",
		},
		{
			name:          "nested workdir",
			prompt:        "This is the Codex nested workdir case. Use exec_command to write src/output.txt and reply NESTED_WORKDIR_OK.",
			commandMarker: "cd src",
			toolOutput:    "NESTED WORKDIR OK",
			final:         "NESTED_WORKDIR_OK",
		},
		{
			name:          "context patch",
			prompt:        "This is the Codex context patch case. Use exec_command to read context and reply CONTEXT_PATCH_OK.",
			commandMarker: "context patch task updated",
			toolOutput:    "context patch task updated",
			final:         "CONTEXT_PATCH_OK",
		},
		{
			name:          "no delete",
			prompt:        "This is the Codex no delete case. Use exec_command to read protected files and reply NO_DELETE_OK.",
			commandMarker: "cat protected.txt scratch.txt",
			toolOutput:    "keep-me-safe",
			final:         "NO_DELETE_OK",
		},
		{
			name:          "shell script fix",
			prompt:        "This is the Codex shell script fix case. Use exec_command to fix app.sh and reply SHELL_SCRIPT_OK.",
			commandMarker: "shell script task passed",
			toolOutput:    "shell script task passed",
			final:         "SHELL_SCRIPT_OK",
		},
		{
			name:          "fallback shell",
			prompt:        "This is the Codex fallback shell case. Use the shell tool to read fallback.txt and reply FALLBACK_SHELL_OK.",
			commandMarker: "cat fallback.txt",
			toolOutput:    "fallback-shell-token",
			final:         "FALLBACK_SHELL_OK",
		},
		{
			name:          "websocket read",
			prompt:        "This is the Codex websocket read case. Use exec_command to read README.md and reply WS_READ_OK.",
			commandMarker: "cat README.md",
			toolOutput:    "codex-smoke-token: llama-shim-42",
			final:         "WS_READ_OK",
		},
		{
			name:          "websocket patch",
			prompt:        "This is the Codex websocket patch case. Use exec_command to patch websocket_target.txt and reply WS_PATCH_OK.",
			commandMarker: "patched websocket_target.txt",
			toolOutput:    "patched websocket_target.txt",
			final:         "WS_PATCH_OK",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := chatCompletionRequest{
				Model: DefaultModel,
				Messages: []chatMessage{
					{Role: "system", Content: "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
					{Role: "user", Content: tc.prompt},
				},
				Tools: tools,
			}

			content, toolCalls, finishReason := chatCompletionReply(request)
			require.Empty(t, content)
			require.Equal(t, "tool_calls", finishReason)
			require.Len(t, toolCalls, 1)
			function := toolCalls[0]["function"].(map[string]any)
			require.Equal(t, "exec_command", function["name"])
			require.Contains(t, function["arguments"], tc.commandMarker)

			request.Messages = append(request.Messages, chatMessage{
				Role:       "tool",
				ToolCallID: "call_devstack_codex_1",
				Content:    tc.toolOutput,
			})
			content, toolCalls, finishReason = chatCompletionReply(request)
			require.Equal(t, tc.final, content)
			require.Nil(t, toolCalls)
			require.Equal(t, "stop", finishReason)
		})
	}
}

func TestChatCompletionsCodexWriteStdinPlansInteractiveFollowup(t *testing.T) {
	tools := []chatTool{
		{Type: "function", Function: chatToolFunction{Name: "exec_command"}},
		{Type: "function", Function: chatToolFunction{Name: "write_stdin"}},
	}
	request := chatCompletionRequest{
		Model: DefaultModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
			{Role: "user", Content: "This is the Codex write stdin PTY case. Start an interactive command, send stdin, then reply STDIN_OK."},
		},
		Tools: tools,
	}

	content, toolCalls, finishReason := chatCompletionReply(request)
	require.Empty(t, content)
	require.Equal(t, "tool_calls", finishReason)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0]["function"].(map[string]any)
	require.Equal(t, "exec_command", function["name"])
	require.Contains(t, function["arguments"], `"tty":true`)

	request.Messages = append(request.Messages, chatMessage{
		Role:       "tool",
		ToolCallID: "call_devstack_codex_1",
		Content:    "Chunk ID: abc\nWall time: 1.0000 seconds\nProcess running with session ID 7\nOutput:\nREADY_FOR_STDIN\n",
	})
	content, toolCalls, finishReason = chatCompletionReply(request)
	require.Empty(t, content)
	require.Equal(t, "tool_calls", finishReason)
	require.Len(t, toolCalls, 1)
	function = toolCalls[0]["function"].(map[string]any)
	require.Equal(t, "write_stdin", function["name"])
	require.Contains(t, function["arguments"], `"session_id":7`)
	require.Contains(t, function["arguments"], `\u0003`)

	request.Messages = append(request.Messages, chatMessage{
		Role:       "tool",
		ToolCallID: "call_devstack_codex_2",
		Content:    "STDIN_DONE codex-stdin-token",
	})
	content, toolCalls, finishReason = chatCompletionReply(request)
	require.Equal(t, "STDIN_OK", content)
	require.Nil(t, toolCalls)
	require.Equal(t, "stop", finishReason)
}

func TestFixtureCodexSessionIDParsesCodexUnifiedExecOutput(t *testing.T) {
	require.Equal(t, 7, fixtureCodexSessionID(`{"session_id":7,"output":"READY"}`))
	require.Equal(t, 1234, fixtureCodexSessionID("Chunk ID: abc\nProcess running with session ID 1234\nOutput:\nREADY\n"))
	require.Zero(t, fixtureCodexSessionID("READY_FOR_STDIN\n"))
}

func TestChatCompletionsCodexShellCommandToolArguments(t *testing.T) {
	request := chatCompletionRequest{
		Model: DefaultModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
			{Role: "user", Content: "This is the Codex fallback shell case. Use the shell tool to read fallback.txt and reply FALLBACK_SHELL_OK."},
		},
		Tools: []chatTool{
			{Type: "function", Function: chatToolFunction{Name: "shell_command"}},
		},
	}

	content, toolCalls, finishReason := chatCompletionReply(request)
	require.Empty(t, content)
	require.Equal(t, "tool_calls", finishReason)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0]["function"].(map[string]any)
	require.Equal(t, "shell_command", function["name"])
	require.JSONEq(t, `{"command":"cat fallback.txt","timeout_ms":60000,"workdir":"."}`, function["arguments"].(string))
}

func TestChatCompletionsCodexCommandTimeoutPlansFastRecovery(t *testing.T) {
	tools := []chatTool{
		{
			Type: "function",
			Function: chatToolFunction{
				Name: "exec_command",
			},
		},
	}
	request := chatCompletionRequest{
		Model: DefaultModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You are a coding agent running in the Codex CLI, a terminal-based coding assistant."},
			{Role: "user", Content: "This is the Codex command timeout recovery case. Use exec_command to run `sh slow.sh` with a short timeout, then run `sh fast.sh`, and reply TIMEOUT_RECOVERED."},
			{Role: "tool", ToolCallID: "call_devstack_codex_1", Content: "command timed out after 500ms"},
		},
		Tools: tools,
	}

	content, toolCalls, finishReason := chatCompletionReply(request)
	require.Empty(t, content)
	require.Equal(t, "tool_calls", finishReason)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0]["function"].(map[string]any)
	require.Equal(t, "exec_command", function["name"])
	require.Contains(t, function["arguments"], "sh fast.sh")
}

func TestHandlerChatCompletionsReturnsCompactionJSON(t *testing.T) {
	request := chatCompletionRequest{
		Model: DefaultModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You compact prior conversation state for an OpenAI-compatible Responses API shim."},
			{Role: "user", Content: "Compact these prior context items for continuation:\n\n001 user: Remember launch code 777."},
		},
	}

	content, toolCalls, finishReason := chatCompletionReply(request)
	require.Nil(t, toolCalls)
	require.Equal(t, "stop", finishReason)

	var state map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &state))
	require.Contains(t, state["summary"], "launch code 777")
}

func TestHandlerChatCompletionsPlansAndCompletesToolSearchFunctionCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Find the shipping ETA tool and use it for order_42."},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "get_shipping_eta",
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices := response["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	require.Equal(t, "tool_calls", firstChoice["finish_reason"])
	message := firstChoice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "get_shipping_eta", function["name"])
	require.JSONEq(t, `{"order_id":"order_42"}`, function["arguments"].(string))

	payload["messages"] = []map[string]any{
		{"role": "user", "content": "Find the shipping ETA tool and use it for order_42."},
		{"role": "tool", "tool_call_id": "call_devstack_tool_search_1", "content": "ETA for order_42 is 2026-04-20."},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices = response["choices"].([]any)
	firstChoice = choices[0].(map[string]any)
	require.Equal(t, "stop", firstChoice["finish_reason"])
	message = firstChoice["message"].(map[string]any)
	require.Equal(t, "ETA for order_42 is 2026-04-20.", message["content"])
	_, hasToolCalls := message["tool_calls"]
	require.False(t, hasToolCalls)
}

func TestHandlerSearchAndImageResponses(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/search?q=fixture+guide&format=json")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var search map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&search))
	results, ok := search["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	result := results[0].(map[string]any)
	require.Contains(t, result["url"], "/pages/web-search-guide")
	require.Equal(t, "Fixture Web Search Guide", result["title"])

	payload := map[string]any{
		"model": DefaultModel,
		"input": "Generate a tiny orange cat in a teacup.",
		"tools": []map[string]any{
			{
				"type":          "image_generation",
				"output_format": "png",
				"quality":       "low",
				"size":          "1024x1024",
			},
		},
		"tool_choice": map[string]any{
			"type": "image_generation",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/responses", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	require.Equal(t, "response", response["object"])
	output, ok := response["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item := output[0].(map[string]any)
	require.Equal(t, "image_generation_call", item["type"])
	require.Equal(t, "completed", item["status"])
	require.Equal(t, fixtureImageBase64, item["result"])
}

func TestHandlerComfyUIRoutes(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/system_stats")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := json.Marshal(map[string]any{
		"client_id": "test-client",
		"prompt": map[string]any{
			"9": map[string]any{
				"class_type": "SaveImage",
			},
		},
	})
	require.NoError(t, err)
	resp, err = server.Client().Post(server.URL+"/prompt", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var prompt map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&prompt))
	require.Equal(t, "comfyui_devstack_1", prompt["prompt_id"])

	resp, err = server.Client().Get(server.URL + "/history/comfyui_devstack_1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var history map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&history))
	entry := history["comfyui_devstack_1"].(map[string]any)
	outputs := entry["outputs"].(map[string]any)
	image := outputs["9"].(map[string]any)["images"].([]any)[0].(map[string]any)
	require.Equal(t, "devstack-comfyui.png", image["filename"])

	resp, err = server.Client().Get(server.URL + "/view?filename=devstack-comfyui.png&type=output")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, []byte("fake-image"), raw)
}

func TestHandlerSupportsStreamableAndLegacyMCP(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	initializeBody := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"llama_shim","version":"local"}}}`))
	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", initializeBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	result := payload["result"].(map[string]any)
	require.Equal(t, "2024-11-05", result["protocolVersion"])

	sessionID := resp.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, sessionID)

	listBody := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	req, err = http.NewRequest(http.MethodPost, server.URL+"/mcp", listBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)

	resp, err = server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	result = payload["result"].(map[string]any)
	tools := result["tools"].([]any)
	require.Len(t, tools, 1)
	require.Equal(t, "roll", tools[0].(map[string]any)["name"])

	sseResp, err := server.Client().Get(server.URL + "/sse")
	require.NoError(t, err)
	defer sseResp.Body.Close()
	require.Equal(t, http.StatusOK, sseResp.StatusCode)
	require.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")

	reader := bufio.NewReader(sseResp.Body)
	eventType, data := readFixtureSSEEvent(t, reader)
	require.Equal(t, "endpoint", eventType)
	require.Contains(t, data, "/message?session=sse-")

	messageBody := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"llama_shim","version":"local"}}}`))
	req, err = http.NewRequest(http.MethodPost, server.URL+strings.TrimSpace(data), messageBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err = server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	eventType, data = readFixtureSSEEvent(t, reader)
	require.Equal(t, "message", eventType)

	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	result = payload["result"].(map[string]any)
	require.Equal(t, "2024-11-05", result["protocolVersion"])
}

func readFixtureSSEEvent(t *testing.T, reader *bufio.Reader) (string, string) {
	t.Helper()

	var (
		eventType string
		dataLines []string
	)
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(dataLines) == 0 {
				continue
			}
			return eventType, strings.Join(dataLines, "\n")
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}
