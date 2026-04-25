package devstackfixture

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	require.JSONEq(t, `{"cmd":"python3 -c \"import os; from pathlib import Path; p=Path(os.environ['LLAMA_SHIM_CODEX_SMOKE_TARGET']); p.write_text(p.read_text().replace('status = TODO', 'status = patched-by-codex')); print('patched smoke_target.txt')\"","yield_time_ms":5000,"max_output_tokens":12000}`, function["arguments"].(string))

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
