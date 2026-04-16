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
