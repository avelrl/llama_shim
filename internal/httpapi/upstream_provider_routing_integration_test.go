package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/testutil"
)

func TestUpstreamProviderRoutingChatCompletions(t *testing.T) {
	var seen atomic.Int64
	var seenAuth string
	var seenAPIKey string
	var seenModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			seen.Add(1)
			seenAuth = r.Header.Get("Authorization")
			seenAPIKey = r.Header.Get("Api-Key")
			var request struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			seenModel = request.Model
			writeChatCompletionText(t, w, "Qwen3.6-Coder", "OK")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeModelsList(t, w, "Qwen3.6-Coder")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	storeWhenOmitted := false
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ChatCompletionsStoreWhenOmitted: &storeWhenOmitted,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:          "qwen",
				BaseURL:     upstream.URL,
				BearerToken: "provider-token",
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, payload := jsonRequestWithHeaders(t, app.Server.URL+"/v1/chat/completions", map[string]any{
		"model": "qwen/coder",
		"messages": []map[string]any{
			{"role": "user", "content": "Reply OK"},
		},
	}, map[string]string{
		"Authorization": "Bearer client-token",
		"Api-Key":       "client-api-key",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "qwen/coder", asStringAny(payload["model"]))
	require.Equal(t, int64(1), seen.Load())
	require.Equal(t, "Qwen3.6-Coder", seenModel)
	require.Equal(t, "Bearer provider-token", seenAuth)
	require.Empty(t, seenAPIKey)
}

func TestUpstreamProviderRoutingChatCompletionsStreamRestoresPublicModel(t *testing.T) {
	var seenModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		var request struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		seenModel = request.Model
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_route\",\"object\":\"chat.completion.chunk\",\"model\":\"Qwen3.6-Coder\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, body := rawHTTPBody(t, app.Server.URL+"/v1/chat/completions", map[string]any{
		"model":  "qwen/coder",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "Reply OK"},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "Qwen3.6-Coder", seenModel)
	require.Contains(t, body, `"model":"qwen/coder"`)
	require.NotContains(t, body, `"model":"Qwen3.6-Coder"`)
}

func TestUpstreamProviderRoutingResponsesProxy(t *testing.T) {
	var seen atomic.Int64
	var seenAuth string
	var seenModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			seen.Add(1)
			seenAuth = r.Header.Get("Authorization")
			var request struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			seenModel = request.Model
			writeProviderResponseText(t, w, "Qwen3.6-Coder", "OK")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeModelsList(t, w, "Qwen3.6-Coder")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode: config.ResponsesModePreferUpstream,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:          "qwen",
				BaseURL:     upstream.URL,
				BearerToken: "provider-token",
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "qwen/coder",
		"store": true,
		"input": "Reply OK",
	})
	require.Equal(t, http.StatusOK, status)
	var response domain.Response
	mustDecode(t, payload, &response)
	require.Equal(t, "qwen/coder", response.Model)
	require.Equal(t, "OK", response.OutputText)
	require.Equal(t, int64(1), seen.Load())
	require.Equal(t, "Qwen3.6-Coder", seenModel)
	require.Equal(t, "Bearer provider-token", seenAuth)

	stored, err := app.Store.GetResponse(context.Background(), response.ID)
	require.NoError(t, err)
	require.Equal(t, "qwen/coder", stored.Model)
}

func TestUpstreamProviderRoutingResponsesChatBacked(t *testing.T) {
	var seen atomic.Int64
	var seenModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			seen.Add(1)
			var request struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			seenModel = request.Model
			writeChatCompletionText(t, w, "Qwen3.6-Coder", "OK")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeModelsList(t, w, "Qwen3.6-Coder")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode:              config.ResponsesModePreferUpstream,
		ResponsesUpstreamTransport: config.ResponsesUpstreamTransportChatCompletions,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "qwen/coder",
		"store": true,
		"input": "Reply OK",
	})
	require.Equal(t, http.StatusOK, status)
	var response domain.Response
	mustDecode(t, payload, &response)
	require.Equal(t, "qwen/coder", response.Model)
	require.Equal(t, "OK", response.OutputText)
	require.Equal(t, int64(1), seen.Load())
	require.Equal(t, "Qwen3.6-Coder", seenModel)
}

func TestUpstreamProviderRoutingResponsesDerivedProxyEndpoints(t *testing.T) {
	var seenModels []string
	var seenAuths []string
	var seenPaths []string
	var seenMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && (r.URL.Path == "/v1/responses/input_tokens" || r.URL.Path == "/v1/responses/compact"):
			var request struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			seenMu.Lock()
			seenPaths = append(seenPaths, r.URL.Path)
			seenModels = append(seenModels, request.Model)
			seenAuths = append(seenAuths, r.Header.Get("Authorization"))
			seenMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v1/responses/input_tokens" {
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"object":       "response.input_tokens",
					"input_tokens": 17,
				}))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":         "resp_compact_provider_route",
				"object":     "response.compaction",
				"created_at": int64(1712059200),
				"output": []map[string]any{
					{"type": "compaction", "encrypted_content": "upstream"},
				},
				"usage": map[string]any{
					"input_tokens":  1,
					"output_tokens": 1,
					"total_tokens":  2,
				},
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeModelsList(t, w, "Qwen3.6-Coder")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode: config.ResponsesModePreferUpstream,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:          "qwen",
				BaseURL:     upstream.URL,
				BearerToken: "provider-token",
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, tokenPayload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"model": "qwen/coder",
		"input": "Count through provider",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "response.input_tokens", asStringAny(tokenPayload["object"]))
	require.Equal(t, float64(17), tokenPayload["input_tokens"])

	status, compactPayload := rawRequest(t, app, http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "qwen/coder",
		"input": "Compact through provider",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "response.compaction", asStringAny(compactPayload["object"]))
	output, ok := compactPayload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "upstream", asStringAny(item["encrypted_content"]))

	seenMu.Lock()
	require.Equal(t, []string{"/v1/responses/input_tokens", "/v1/responses/compact"}, seenPaths)
	require.Equal(t, []string{"Qwen3.6-Coder", "Qwen3.6-Coder"}, seenModels)
	require.Equal(t, []string{"Bearer provider-token", "Bearer provider-token"}, seenAuths)
	seenMu.Unlock()
}

func TestUpstreamProviderRoutingResponsesDerivedWithoutModelDoesNotUseLegacyUpstream(t *testing.T) {
	var seen atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode: config.ResponsesModePreferUpstream,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, tokenPayload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"input": "Count locally without model.",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "response.input_tokens", asStringAny(tokenPayload["object"]))

	status, compactPayload := rawRequest(t, app, http.MethodPost, "/v1/responses/compact", map[string]any{
		"input": "Compact locally without model.",
	})
	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := compactPayload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "model", asStringAny(errorPayload["param"]))
	require.Equal(t, int64(0), seen.Load())
}

func TestUpstreamProviderRoutingResponsesWebSocketGenerateFalseValidatesModel(t *testing.T) {
	var seen atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialResponsesWebSocket(t, ctx, app)
	defer conn.Close(websocket.StatusNormalClosure, "")

	require.NoError(t, conn.Write(ctx, websocket.MessageText, mustJSON(t, map[string]any{
		"type":     "response.create",
		"generate": false,
		"model":    "qwen/missing",
		"store":    false,
		"input":    "warm up with an invalid alias",
	})))
	errorEvent := readWebSocketEvent(t, ctx, conn)
	require.Equal(t, "error", errorEvent.Event)
	errorPayload, ok := errorEvent.Data["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "model", asStringAny(errorPayload["param"]))

	events := sendWebSocketCreate(t, ctx, conn, map[string]any{
		"generate": false,
		"model":    "qwen/coder",
		"store":    false,
		"input":    "warm up with a valid alias",
	})
	completed := events[len(events)-1].Data["response"].(map[string]any)
	require.Equal(t, "qwen/coder", asStringAny(completed["model"]))
	require.Equal(t, "", asStringAny(completed["output_text"]))
	require.Equal(t, int64(0), seen.Load())
}

func TestUpstreamProviderRoutingResponsesWebSocketGeneratedRoutesUpstream(t *testing.T) {
	var seenModel string
	var seenAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			seenAuth = r.Header.Get("Authorization")
			var request struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			seenModel = request.Model
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":1,\"response\":{\"id\":\"resp_provider_ws\",\"object\":\"response\",\"created_at\":1712059200,\"status\":\"in_progress\",\"model\":\"Qwen3.6-Coder\",\"output\":[],\"output_text\":\"\",\"store\":true,\"background\":false,\"metadata\":{}}}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_provider_ws\",\"object\":\"response\",\"created_at\":1712059200,\"status\":\"completed\",\"completed_at\":1712059201,\"model\":\"Qwen3.6-Coder\",\"output\":[{\"id\":\"msg_provider_ws\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"WS OK\"}]}],\"output_text\":\"WS OK\",\"store\":true,\"background\":false,\"metadata\":{}}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeModelsList(t, w, "Qwen3.6-Coder")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode: config.ResponsesModePreferUpstream,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:          "qwen",
				BaseURL:     upstream.URL,
				BearerToken: "provider-token",
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialResponsesWebSocket(t, ctx, app)
	defer conn.Close(websocket.StatusNormalClosure, "")

	events := sendWebSocketCreate(t, ctx, conn, map[string]any{
		"model": "qwen/coder",
		"store": true,
		"input": "Reply over websocket",
	})
	completed := events[len(events)-1].Data["response"].(map[string]any)
	require.Equal(t, "qwen/coder", asStringAny(completed["model"]))
	require.Equal(t, "WS OK", asStringAny(completed["output_text"]))
	require.Equal(t, "Qwen3.6-Coder", seenModel)
	require.Equal(t, "Bearer provider-token", seenAuth)
}

func TestUpstreamProviderRoutingModelsAndReadyz(t *testing.T) {
	var modelListCalls atomic.Int64
	var seenAuths []string
	var seenMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		seenMu.Lock()
		seenAuths = append(seenAuths, r.Header.Get("Authorization"))
		seenMu.Unlock()
		modelListCalls.Add(1)
		writeModelsList(t, w, "Qwen3.6-Coder")
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaReadinessBearerToken: "legacy-readiness-token",
		LlamaProviders: []config.LlamaProvider{
			{
				ID:          "qwen",
				BaseURL:     upstream.URL,
				BearerToken: "provider-token",
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	readyStatus, _ := rawRequest(t, app, http.MethodGet, "/readyz", nil)
	require.Equal(t, http.StatusOK, readyStatus)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/models", nil)
	require.Equal(t, http.StatusOK, status)
	data, ok := payload["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	model, ok := data[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "qwen/coder", asStringAny(model["id"]))
	require.Equal(t, "provider:qwen", asStringAny(model["owned_by"]))
	require.Equal(t, int64(2), modelListCalls.Load())
	seenMu.Lock()
	require.Equal(t, []string{"Bearer provider-token", "Bearer provider-token"}, seenAuths)
	seenMu.Unlock()
}

func TestUpstreamProviderRoutingReadyzUsesProviderTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		time.Sleep(2100 * time.Millisecond)
		writeModelsList(t, w, "Qwen3.6-Coder")
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaTimeout: 4 * time.Second,
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, _ := rawRequest(t, app, http.MethodGet, "/readyz", nil)
	require.Equal(t, http.StatusOK, status)
}

func TestUpstreamProviderRoutingModelsOmitsUnavailableUpstreamModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		writeModelsList(t, w, "other-model")
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/models", nil)
	require.Equal(t, http.StatusServiceUnavailable, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "service_unavailable", asStringAny(errorPayload["type"]))
}

func TestUpstreamProviderRoutingRejectsUnknownBeforeUpstream(t *testing.T) {
	var seen atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaProviders: []config.LlamaProvider{
			{
				ID:      "qwen",
				BaseURL: upstream.URL,
				Models: []config.LlamaProviderModel{
					{Model: "coder", UpstreamModel: "Qwen3.6-Coder"},
				},
			},
		},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "qwen/missing",
		"input": "Reply OK",
	})
	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "model", asStringAny(errorPayload["param"]))
	require.Equal(t, int64(0), seen.Load())
}

func jsonRequestWithHeaders(t *testing.T, url string, body map[string]any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	return resp.StatusCode, payload
}

func rawHTTPBody(t *testing.T, url string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out bytes.Buffer
	_, err = out.ReadFrom(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out.String()
}

func writeProviderResponseText(t *testing.T, w http.ResponseWriter, model string, text string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"id":         "resp_provider_route",
		"object":     "response",
		"created_at": int64(1712059200),
		"status":     "completed",
		"model":      model,
		"output": []map[string]any{
			{
				"id":     "msg_provider_route",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{
					{"type": "output_text", "text": text},
				},
			},
		},
		"output_text": text,
	}))
}
