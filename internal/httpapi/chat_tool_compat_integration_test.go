package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/testutil"
)

func rawChatCompatRequest(t *testing.T, app *testutil.TestApp, method, path string, payload map[string]any) (int, map[string]any) {
	t.Helper()

	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		rawBody, err := json.Marshal(payload)
		require.NoError(t, err)
		body = bytes.NewReader(rawBody)
	}

	req, err := http.NewRequest(method, app.Server.URL+path, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return resp.StatusCode, decoded
}

func TestChatCompletionsRetryRepairsForcedNamedToolCall(t *testing.T) {
	var attempts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)

		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		switch attempts.Add(1) {
		case 1:
			toolChoice := payload["tool_choice"].(map[string]any)
			require.Equal(t, "function", toolChoice["type"])
			function := toolChoice["function"].(map[string]any)
			require.Equal(t, "add", function["name"])
			require.Equal(t, float64(64), payload["max_tokens"])
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_first",
				"object":  "chat.completion",
				"created": 1712059200,
				"model":   "test-model",
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
					},
					"finish_reason": "length",
				}},
			}))
		case 2:
			require.Equal(t, "required", payload["tool_choice"])
			require.Equal(t, float64(256), payload["max_tokens"])
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_second",
				"object":  "chat.completion",
				"created": 1712059201,
				"model":   "test-model",
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]any{{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      "add",
								"arguments": `{"a":1,"b":2}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			}))
		default:
			t.Fatalf("unexpected retry count %d", attempts.Load())
		}
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{LlamaBaseURL: upstream.URL})

	status, body := rawChatCompatRequest(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":      "test-model",
		"max_tokens": 64,
		"messages": []map[string]any{
			{"role": "user", "content": "Call add."},
		},
		"tool_choice": map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "add",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "add",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"a": map[string]any{"type": "number"},
							"b": map[string]any{"type": "number"},
						},
						"required": []string{"a", "b"},
					},
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	choices := body["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "add", function["name"])
	require.Equal(t, `{"a":1,"b":2}`, function["arguments"])
	require.EqualValues(t, 2, attempts.Load())
}

func TestChatCompletionsRetryRepairsRequiredToolCallArguments(t *testing.T) {
	var attempts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)

		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		switch attempts.Add(1) {
		case 1:
			require.Equal(t, "required", payload["tool_choice"])
			require.Equal(t, float64(64), payload["max_tokens"])
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_first",
				"object":  "chat.completion",
				"created": 1712059200,
				"model":   "test-model",
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]any{{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      "add",
								"arguments": `{"a":`,
							},
						}},
					},
					"finish_reason": "length",
				}},
			}))
		case 2:
			require.Equal(t, "required", payload["tool_choice"])
			require.Equal(t, float64(256), payload["max_tokens"])
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_second",
				"object":  "chat.completion",
				"created": 1712059201,
				"model":   "test-model",
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]any{{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      "add",
								"arguments": `{"a":1,"b":2}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			}))
		default:
			t.Fatalf("unexpected retry count %d", attempts.Load())
		}
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{LlamaBaseURL: upstream.URL})

	status, body := rawChatCompatRequest(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":      "test-model",
		"max_tokens": 64,
		"messages": []map[string]any{
			{"role": "user", "content": "Call add."},
		},
		"tool_choice": "required",
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name": "add",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"a": map[string]any{"type": "number"},
							"b": map[string]any{"type": "number"},
						},
						"required": []string{"a", "b"},
					},
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	choices := body["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "add", function["name"])
	require.Equal(t, `{"a":1,"b":2}`, function["arguments"])
	require.EqualValues(t, 2, attempts.Load())
}
