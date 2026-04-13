package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/storage/sqlite"
	"llama_shim/internal/testutil"
)

type semanticTestEmbedder struct{}

func (semanticTestEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "banana"):
			out = append(out, []float32{1, 0, 0})
		case strings.Contains(lower, "ocean"):
			out = append(out, []float32{0, 1, 0})
		default:
			out = append(out, []float32{0, 0, 1})
		}
	}
	return out, nil
}

type semanticV1Embedder struct{}

func (semanticV1Embedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "banana"):
			out = append(out, []float32{1, 0})
		case strings.Contains(lower, "ocean"):
			out = append(out, []float32{0, 1})
		default:
			out = append(out, []float32{0.5, 0.5})
		}
	}
	return out, nil
}

type semanticV2Embedder struct{}

func (semanticV2Embedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "banana"):
			out = append(out, []float32{1, 0, 0})
		case strings.Contains(lower, "ocean"):
			out = append(out, []float32{0, 1, 0})
		default:
			out = append(out, []float32{0, 0, 1})
		}
	}
	return out, nil
}

type hybridRankingTestEmbedder struct{}

func (hybridRankingTestEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.TrimSpace(strings.ToLower(text))
		switch {
		case strings.Contains(lower, "semanticwinner"), lower == "banana nutrition":
			out = append(out, []float32{1, 0})
		default:
			out = append(out, []float32{0, 1})
		}
	}
	return out, nil
}

type rerankingTestEmbedder struct{}

func (rerankingTestEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.TrimSpace(strings.ToLower(text))
		switch {
		case lower == "banana nutrition", strings.Contains(lower, "semanticwinner"):
			out = append(out, []float32{1, 0})
		case strings.Contains(lower, "banana nutrition exact phrase"):
			out = append(out, []float32{0.8, 0.6})
		default:
			out = append(out, []float32{0, 1})
		}
	}
	return out, nil
}

type failingReadyEmbedder struct{}

func (failingReadyEmbedder) EmbedTexts(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1, 0, 0}}, nil
}

func (failingReadyEmbedder) CheckReady(context.Context) error {
	return errors.New("embedder down")
}

func TestResponsesStoreAndGet(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model":    "test-model",
		"store":    true,
		"metadata": map[string]any{"topic": "demo"},
		"input":    "Say OK and nothing else",
	})

	require.NotEmpty(t, response.ID)
	require.NotEmpty(t, response.OutputText)
	require.Equal(t, "response", response.Object)
	require.NotZero(t, response.CreatedAt)
	require.Equal(t, "completed", response.Status)
	require.NotNil(t, response.CompletedAt)
	require.JSONEq(t, "null", string(response.Error))
	require.JSONEq(t, "null", string(response.IncompleteDetails))
	require.JSONEq(t, "null", string(response.Usage))
	require.Equal(t, map[string]string{"topic": "demo"}, response.Metadata)
	require.NotNil(t, response.Store)
	require.True(t, *response.Store)
	require.NotNil(t, response.Background)
	require.False(t, *response.Background)

	got := getResponse(t, app, response.ID)
	require.Equal(t, response.ID, got.ID)
	require.NotEmpty(t, got.OutputText)
	require.Equal(t, response.CreatedAt, got.CreatedAt)
	require.Equal(t, response.Status, got.Status)
	require.Equal(t, response.Metadata, got.Metadata)
	require.NotNil(t, got.Store)
	require.True(t, *got.Store)
}

func TestReadyzChecksSQLiteAndLlamaBackend(t *testing.T) {
	app := testutil.NewTestApp(t)

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/readyz", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "ready", payload["status"])
}

func TestReadyzReturns503WhenLlamaBackendIsUnavailable(t *testing.T) {
	llamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend failed", http.StatusBadGateway)
	}))
	defer llamaServer.Close()

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		LlamaBaseURL: llamaServer.URL,
	})

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/readyz", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var payload map[string]map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "service_unavailable", payload["error"]["type"])
	require.Equal(t, "llama backend is not ready", payload["error"]["message"])
}

func TestReadyzReturns503WhenRetrievalEmbedderIsUnavailable(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		RetrievalEmbedder: failingReadyEmbedder{},
	})

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/readyz", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	var payload map[string]map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "service_unavailable", payload["error"]["type"])
	require.Equal(t, "retrieval embedder is not ready", payload["error"]["message"])
}

func TestResponsesGetIncludesExpandedResponseSurface(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"instructions": "Be terse.",
		"reasoning": map[string]any{
			"effort": "minimal",
		},
		"temperature": 0,
		"top_p":       0.25,
		"input":       "Say OK and nothing else",
	})

	require.JSONEq(t, `"Be terse."`, string(response.Instructions))
	require.JSONEq(t, "null", string(response.MaxOutputTokens))
	require.JSONEq(t, "true", string(response.ParallelToolCalls))
	require.JSONEq(t, `{"effort":"minimal","summary":null}`, string(response.Reasoning))
	require.JSONEq(t, "0", string(response.Temperature))
	require.JSONEq(t, `0.25`, string(response.TopP))
	require.JSONEq(t, `"auto"`, string(response.ToolChoice))
	require.JSONEq(t, `[]`, string(response.Tools))
	require.JSONEq(t, `"disabled"`, string(response.Truncation))
	require.JSONEq(t, "null", string(response.User))

	got := getResponse(t, app, response.ID)
	require.JSONEq(t, `"Be terse."`, string(got.Instructions))
	require.JSONEq(t, "null", string(got.MaxOutputTokens))
	require.JSONEq(t, "true", string(got.ParallelToolCalls))
	require.JSONEq(t, `{"effort":"minimal","summary":null}`, string(got.Reasoning))
	require.JSONEq(t, "0", string(got.Temperature))
	require.JSONEq(t, `0.25`, string(got.TopP))
	require.JSONEq(t, `"auto"`, string(got.ToolChoice))
	require.JSONEq(t, `[]`, string(got.Tools))
	require.JSONEq(t, `"disabled"`, string(got.Truncation))
	require.JSONEq(t, "null", string(got.User))
}

func TestResponsesGetStreamReplaysStoredResponse(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"instructions": "Be terse.",
		"input":        "Say OK and nothing else",
	})

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+response.ID+"?stream=true&include_obfuscation=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Equal(t, "response.created", events[0].Event)
	require.Contains(t, eventTypes(events), "response.in_progress")
	require.Contains(t, eventTypes(events), "response.content_part.added")
	require.Contains(t, eventTypes(events), "response.output_text.delta")
	require.Contains(t, eventTypes(events), "response.content_part.done")
	require.Contains(t, eventTypes(events), "response.completed")

	delta := findEvent(t, events, "response.output_text.delta").Data
	require.Equal(t, response.OutputText, asStringAny(delta["delta"]))
	require.Equal(t, strings.Repeat("x", len([]rune(response.OutputText))), asStringAny(delta["obfuscation"]))

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, response.ID, asStringAny(responsePayload["id"]))
	require.Equal(t, response.OutputText, asStringAny(responsePayload["output_text"]))
}

func TestResponsesGetStreamIncludesObfuscationByDefault(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
	})

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+response.ID+"?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	delta := findEvent(t, events, "response.output_text.delta").Data
	require.Equal(t, response.OutputText, asStringAny(delta["delta"]))
	require.Equal(t, strings.Repeat("x", len([]rune(response.OutputText))), asStringAny(delta["obfuscation"]))
}

func TestResponsesGetStreamReplaysMultipleOutputItems(t *testing.T) {
	app := testutil.NewTestApp(t)

	functionCall, err := domain.NewItem([]byte(`{"id":"fc_test","type":"function_call","call_id":"call_test","name":"lookup","arguments":"{\"id\":123}","status":"completed"}`))
	require.NoError(t, err)
	message, err := domain.NewItem([]byte(`{"id":"msg_test","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_multi",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"hi"}`,
		ResponseJSON:         `{"id":"resp_multi","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"fc_test","type":"function_call","call_id":"call_test","name":"lookup","arguments":"{\"id\":123}","status":"completed"},{"id":"msg_test","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":"done"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "hi")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "hi")},
		Output:               []domain.Item{functionCall, message},
		OutputText:           "done",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_multi?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	addedEvents := findEvents(events, "response.output_item.added")
	require.Len(t, addedEvents, 2)
	require.Equal(t, float64(0), addedEvents[0].Data["output_index"])
	require.Equal(t, "function_call", asStringAny(addedEvents[0].Data["item"].(map[string]any)["type"]))
	require.Equal(t, float64(1), addedEvents[1].Data["output_index"])
	require.Equal(t, "message", asStringAny(addedEvents[1].Data["item"].(map[string]any)["type"]))

	doneEvents := findEvents(events, "response.output_item.done")
	require.Len(t, doneEvents, 2)
	require.Equal(t, float64(0), doneEvents[0].Data["output_index"])
	require.Equal(t, float64(1), doneEvents[1].Data["output_index"])

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	output, ok := responsePayload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 2)
}

func TestResponsesGetStreamReplaysReasoningTextEvents(t *testing.T) {
	app := testutil.NewTestApp(t)

	reasoning, err := domain.NewItem([]byte(`{"id":"rs_test","type":"reasoning","status":"completed","content":[{"type":"reasoning_text","text":"Need to inspect the files before replying."}]}`))
	require.NoError(t, err)
	functionCall, err := domain.NewItem([]byte(`{"id":"fc_test","type":"function_call","call_id":"call_test","name":"update_plan","arguments":"{\"plan\":[{\"status\":\"completed\",\"step\":\"inspect\"}]}","status":"completed"}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_reasoning",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"hi"}`,
		ResponseJSON:         `{"id":"resp_reasoning","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"rs_test","type":"reasoning","status":"completed","content":[{"type":"reasoning_text","text":"Need to inspect the files before replying."}]},{"id":"fc_test","type":"function_call","call_id":"call_test","name":"update_plan","arguments":"{\"plan\":[{\"status\":\"completed\",\"step\":\"inspect\"}]}","status":"completed"}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "hi")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "hi")},
		Output:               []domain.Item{reasoning, functionCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_reasoning?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.reasoning_text.delta")
	require.Contains(t, eventTypes(events), "response.reasoning_text.done")

	delta := findEvent(t, events, "response.reasoning_text.delta").Data
	require.Equal(t, "rs_test", asStringAny(delta["item_id"]))
	require.Equal(t, float64(0), delta["output_index"])
	require.Equal(t, float64(0), delta["content_index"])
	require.Equal(t, "Need to inspect the files before replying.", asStringAny(delta["delta"]))

	done := findEvent(t, events, "response.reasoning_text.done").Data
	require.Equal(t, "rs_test", asStringAny(done["item_id"]))
	require.Equal(t, float64(0), done["output_index"])
	require.Equal(t, float64(0), done["content_index"])
	require.Equal(t, "Need to inspect the files before replying.", asStringAny(done["text"]))
}

func TestResponsesGetStreamReplaysMCPCallEvents(t *testing.T) {
	app := testutil.NewTestApp(t)

	mcpCall, err := domain.NewItem([]byte(`{"id":"mcp_test","type":"mcp_call","name":"lookup_orders","server_label":"shopify","arguments":"{\"status\":\"open\"}","output":"{\"count\":3}","status":"completed"}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_mcp",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"lookup open orders"}`,
		ResponseJSON:         `{"id":"resp_mcp","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"mcp_test","type":"mcp_call","name":"lookup_orders","server_label":"shopify","arguments":"{\"status\":\"open\"}","output":"{\"count\":3}","status":"completed"}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "lookup open orders")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "lookup open orders")},
		Output:               []domain.Item{mcpCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_mcp?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.mcp_call_arguments.delta")
	require.Contains(t, eventTypes(events), "response.mcp_call_arguments.done")
	require.Contains(t, eventTypes(events), "response.mcp_call.in_progress")
	require.NotContains(t, eventTypes(events), "response.mcp_call.failed")
	require.NotContains(t, eventTypes(events), "response.output_text.done")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_call", addedItem["type"])
	require.Equal(t, "mcp_test", asStringAny(addedItem["id"]))
	require.Equal(t, "", asStringAny(addedItem["arguments"]))
	require.Equal(t, "in_progress", asStringAny(addedItem["status"]))
	_, hasOutput := addedItem["output"]
	require.False(t, hasOutput)

	done := findEvent(t, events, "response.mcp_call_arguments.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_test", asStringAny(done["item_id"]))
	require.Equal(t, float64(0), done["output_index"])
	require.Equal(t, `{"status":"open"}`, asStringAny(done["arguments"]))
	require.Equal(t, "mcp_call", doneItem["type"])
	require.Equal(t, `{"count":3}`, asStringAny(doneItem["output"]))

	inProgress := findEvent(t, events, "response.mcp_call.in_progress").Data
	require.Equal(t, "mcp_test", asStringAny(inProgress["item_id"]))
	require.Equal(t, float64(0), inProgress["output_index"])

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_call", outputDoneItem["type"])
	require.Equal(t, "mcp_test", asStringAny(outputDoneItem["id"]))
	require.Equal(t, `{"count":3}`, asStringAny(outputDoneItem["output"]))

	require.Less(t, eventIndex(t, events, "response.output_item.added"), eventIndex(t, events, "response.mcp_call_arguments.delta"))
	require.Less(t, eventIndex(t, events, "response.mcp_call_arguments.delta"), eventIndex(t, events, "response.mcp_call_arguments.done"))
	require.Less(t, eventIndex(t, events, "response.mcp_call_arguments.done"), eventIndex(t, events, "response.mcp_call.in_progress"))
	require.Less(t, eventIndex(t, events, "response.mcp_call.in_progress"), eventIndex(t, events, "response.output_item.done"))
}

func TestResponsesGetStreamReplaysLegacyMCPToolCallEvents(t *testing.T) {
	app := testutil.NewTestApp(t)

	mcpCall, err := domain.NewItem([]byte(`{"type":"mcp_tool_call","call_id":"mcp_call_legacy","name":"lookup_contacts","server_label":"crm","arguments":"{\"segment\":\"vip\"}","output":{"count":2},"status":"completed"}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_mcp_legacy",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"lookup vip contacts"}`,
		ResponseJSON:         `{"id":"resp_mcp_legacy","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"type":"mcp_tool_call","call_id":"mcp_call_legacy","name":"lookup_contacts","server_label":"crm","arguments":"{\"segment\":\"vip\"}","output":{"count":2},"status":"completed"}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "lookup vip contacts")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "lookup vip contacts")},
		Output:               []domain.Item{mcpCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_mcp_legacy?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.mcp_call_arguments.delta")
	require.Contains(t, eventTypes(events), "response.mcp_call_arguments.done")
	require.Contains(t, eventTypes(events), "response.mcp_call.in_progress")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_tool_call", addedItem["type"])
	require.Equal(t, "mcp_call_legacy", asStringAny(addedItem["id"]))
	require.Equal(t, "", asStringAny(addedItem["arguments"]))

	done := findEvent(t, events, "response.mcp_call_arguments.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_call_legacy", asStringAny(done["item_id"]))
	require.Equal(t, "mcp_tool_call", doneItem["type"])
	require.Equal(t, "mcp_call_legacy", asStringAny(doneItem["id"]))

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_tool_call", outputDoneItem["type"])
	require.Equal(t, "mcp_call_legacy", asStringAny(outputDoneItem["id"]))
}

func TestResponsesGetStreamReplaysFailedMCPCallEvents(t *testing.T) {
	app := testutil.NewTestApp(t)

	mcpCall, err := domain.NewItem([]byte(`{"id":"mcp_failed","type":"mcp_call","name":"lookup_orders","server_label":"shopify","arguments":"{\"status\":\"open\"}","error":{"type":"tool_execution_error","message":"remote MCP unavailable"},"status":"failed"}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_mcp_failed",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"lookup open orders"}`,
		ResponseJSON:         `{"id":"resp_mcp_failed","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"mcp_failed","type":"mcp_call","name":"lookup_orders","server_label":"shopify","arguments":"{\"status\":\"open\"}","error":{"type":"tool_execution_error","message":"remote MCP unavailable"},"status":"failed"}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "lookup open orders")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "lookup open orders")},
		Output:               []domain.Item{mcpCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_mcp_failed?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.mcp_call_arguments.done")
	require.Contains(t, eventTypes(events), "response.mcp_call.in_progress")
	require.Contains(t, eventTypes(events), "response.mcp_call.failed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	_, hasError := addedItem["error"]
	require.False(t, hasError)

	failed := findEvent(t, events, "response.mcp_call.failed").Data
	require.Equal(t, "mcp_failed", asStringAny(failed["item_id"]))
	require.Equal(t, float64(0), failed["output_index"])
	errorPayload, ok := failed["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_execution_error", asStringAny(errorPayload["type"]))
	require.Equal(t, "remote MCP unavailable", asStringAny(errorPayload["message"]))

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "failed", asStringAny(outputDoneItem["status"]))
	doneError, ok := outputDoneItem["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_execution_error", asStringAny(doneError["type"]))

	require.Less(t, eventIndex(t, events, "response.mcp_call_arguments.done"), eventIndex(t, events, "response.mcp_call.in_progress"))
	require.Less(t, eventIndex(t, events, "response.mcp_call.in_progress"), eventIndex(t, events, "response.mcp_call.failed"))
	require.Less(t, eventIndex(t, events, "response.mcp_call.failed"), eventIndex(t, events, "response.output_item.done"))
}

func TestResponsesGetStreamReplaysWebSearchCallWithoutLeakingFinalActionInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	webSearchCall, err := domain.NewItem([]byte(`{"id":"ws_test","type":"web_search_call","status":"completed","action":{"type":"search","query":"latest weather in Paris","sources":[{"type":"url","url":"https://example.com/weather"}]}}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_web_search",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"latest weather in Paris"}`,
		ResponseJSON:         `{"id":"resp_web_search","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ws_test","type":"web_search_call","status":"completed","action":{"type":"search","query":"latest weather in Paris","sources":[{"type":"url","url":"https://example.com/weather"}]}}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "latest weather in Paris")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "latest weather in Paris")},
		Output:               []domain.Item{webSearchCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_web_search?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.web_search_call.in_progress")
	require.Contains(t, eventTypes(events), "response.web_search_call.searching")
	require.Contains(t, eventTypes(events), "response.web_search_call.completed")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	require.NotContains(t, eventTypes(events), "response.function_call_arguments.done")
	require.NotContains(t, eventTypes(events), "response.mcp_call_arguments.done")
	require.Less(t, eventIndex(t, events, "response.output_item.added"), eventIndex(t, events, "response.output_item.done"))
	require.Less(t, eventIndex(t, events, "response.output_item.added"), eventIndex(t, events, "response.web_search_call.in_progress"))
	require.Less(t, eventIndex(t, events, "response.web_search_call.in_progress"), eventIndex(t, events, "response.web_search_call.searching"))
	require.Less(t, eventIndex(t, events, "response.web_search_call.searching"), eventIndex(t, events, "response.web_search_call.completed"))
	require.Less(t, eventIndex(t, events, "response.web_search_call.completed"), eventIndex(t, events, "response.output_item.done"))

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search_call", addedItem["type"])
	require.Equal(t, "ws_test", asStringAny(addedItem["id"]))
	require.Equal(t, "in_progress", asStringAny(addedItem["status"]))
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	searching := findEvent(t, events, "response.web_search_call.searching").Data
	require.Equal(t, "ws_test", asStringAny(searching["item_id"]))

	completed := findEvent(t, events, "response.web_search_call.completed").Data
	require.Equal(t, "ws_test", asStringAny(completed["item_id"]))

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	action, ok := outputDoneItem["action"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "search", asStringAny(action["type"]))
	require.Equal(t, "latest weather in Paris", asStringAny(action["query"]))
}

func TestResponsesGetStreamReplaysWebSearchOpenPageCallWithoutLeakingFinalActionInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	webSearchCall, err := domain.NewItem([]byte(`{"id":"ws_open_page_test","type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://developers.openai.com/api/docs/guides/tools-web-search"}}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_web_search_open_page",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"open the OpenAI Web search guide"}`,
		ResponseJSON:         `{"id":"resp_web_search_open_page","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ws_open_page_test","type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://developers.openai.com/api/docs/guides/tools-web-search"}}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "open the OpenAI Web search guide")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "open the OpenAI Web search guide")},
		Output:               []domain.Item{webSearchCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-11T12:40:00Z",
		CompletedAt:          "2026-04-11T12:40:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_web_search_open_page?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.web_search_call.in_progress")
	require.Contains(t, eventTypes(events), "response.web_search_call.searching")
	require.Contains(t, eventTypes(events), "response.web_search_call.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	action, ok := outputDoneItem["action"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "open_page", asStringAny(action["type"]))
	require.Equal(t, "https://developers.openai.com/api/docs/guides/tools-web-search", asStringAny(action["url"]))
}

func TestResponsesGetStreamReplaysWebSearchFindInPageCallWithoutLeakingFinalActionInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	webSearchCall, err := domain.NewItem([]byte(`{"id":"ws_find_in_page_test","type":"web_search_call","status":"completed","action":{"type":"find_in_page","url":"https://developers.openai.com/api/docs/guides/tools-web-search","pattern":"Supported in reasoning models"}}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_web_search_find_in_page",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"find the phrase Supported in reasoning models in the OpenAI Web search guide"}`,
		ResponseJSON:         `{"id":"resp_web_search_find_in_page","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ws_find_in_page_test","type":"web_search_call","status":"completed","action":{"type":"find_in_page","url":"https://developers.openai.com/api/docs/guides/tools-web-search","pattern":"Supported in reasoning models"}}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "find the phrase Supported in reasoning models in the OpenAI Web search guide")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "find the phrase Supported in reasoning models in the OpenAI Web search guide")},
		Output:               []domain.Item{webSearchCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-11T12:41:00Z",
		CompletedAt:          "2026-04-11T12:41:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_web_search_find_in_page?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.web_search_call.in_progress")
	require.Contains(t, eventTypes(events), "response.web_search_call.searching")
	require.Contains(t, eventTypes(events), "response.web_search_call.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	action, ok := outputDoneItem["action"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "find_in_page", asStringAny(action["type"]))
	require.Equal(t, "https://developers.openai.com/api/docs/guides/tools-web-search", asStringAny(action["url"]))
	require.Equal(t, "Supported in reasoning models", asStringAny(action["pattern"]))
}

func TestResponsesGetStreamReplaysFileSearchCallWithoutLeakingResultsInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	fileSearchCall, err := domain.NewItem([]byte(`{"id":"fs_test","type":"file_search_call","status":"completed","queries":["find notes about onboarding"],"results":[{"file_id":"file_123","filename":"notes.txt","score":0.91}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_file_search",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"find notes about onboarding"}`,
		ResponseJSON:         `{"id":"resp_file_search","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"fs_test","type":"file_search_call","status":"completed","queries":["find notes about onboarding"],"results":[{"file_id":"file_123","filename":"notes.txt","score":0.91}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "find notes about onboarding")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "find notes about onboarding")},
		Output:               []domain.Item{fileSearchCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_file_search?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.file_search_call.in_progress")
	require.Contains(t, eventTypes(events), "response.file_search_call.searching")
	require.Contains(t, eventTypes(events), "response.file_search_call.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	queries, ok := addedItem["queries"].([]any)
	require.True(t, ok)
	require.Empty(t, queries)
	_, hasResults := addedItem["results"]
	require.False(t, hasResults)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	doneQueries, ok := outputDoneItem["queries"].([]any)
	require.True(t, ok)
	require.Len(t, doneQueries, 1)
	results, ok := outputDoneItem["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
}

func TestResponsesGetStreamReplaysFileSearchCallWithoutLeakingSearchResultsInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	fileSearchCall, err := domain.NewItem([]byte(`{"id":"fs_search_results_test","type":"file_search_call","status":"completed","queries":["find onboarding handbook"],"search_results":[{"file_id":"file_456","filename":"handbook.txt","score":0.88}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_file_search_search_results",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"find onboarding handbook"}`,
		ResponseJSON:         `{"id":"resp_file_search_search_results","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"fs_search_results_test","type":"file_search_call","status":"completed","queries":["find onboarding handbook"],"search_results":[{"file_id":"file_456","filename":"handbook.txt","score":0.88}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "find onboarding handbook")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "find onboarding handbook")},
		Output:               []domain.Item{fileSearchCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_file_search_search_results?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.file_search_call.in_progress")
	require.Contains(t, eventTypes(events), "response.file_search_call.searching")
	require.Contains(t, eventTypes(events), "response.file_search_call.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	queries, ok := addedItem["queries"].([]any)
	require.True(t, ok)
	require.Empty(t, queries)
	_, hasSearchResults := addedItem["search_results"]
	require.False(t, hasSearchResults)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	doneQueries, ok := outputDoneItem["queries"].([]any)
	require.True(t, ok)
	require.Len(t, doneQueries, 1)
	searchResults, ok := outputDoneItem["search_results"].([]any)
	require.True(t, ok)
	require.Len(t, searchResults, 1)
}

func TestResponsesGetStreamReplaysCodeInterpreterCallWithoutLeakingOutputsInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	codeInterpreterCall, err := domain.NewItem([]byte(`{"id":"ci_test","type":"code_interpreter_call","status":"completed","container_id":"cntr_123","code":"print(\"result=2.0\")","outputs":[{"type":"logs","logs":"result=2.0\n"}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_code_interpreter",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"run some Python"}`,
		ResponseJSON:         `{"id":"resp_code_interpreter","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ci_test","type":"code_interpreter_call","status":"completed","container_id":"cntr_123","code":"print(\"result=2.0\")","outputs":[{"type":"logs","logs":"result=2.0\n"}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "run some Python")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "run some Python")},
		Output:               []domain.Item{codeInterpreterCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_code_interpreter?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.in_progress")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call_code.delta")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call_code.done")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.interpreting")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "cntr_123", asStringAny(addedItem["container_id"]))
	require.Equal(t, "", asStringAny(addedItem["code"]))
	addedOutputs, ok := addedItem["outputs"].([]any)
	require.True(t, ok)
	require.Empty(t, addedOutputs)

	codeDelta := findEvent(t, events, "response.code_interpreter_call_code.delta").Data
	require.Equal(t, "ci_test", asStringAny(codeDelta["item_id"]))
	require.Equal(t, "print(\"result=2.0\")", asStringAny(codeDelta["delta"]))

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "print(\"result=2.0\")", asStringAny(outputDoneItem["code"]))
	outputs, ok := outputDoneItem["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)
}

func TestResponsesGetStreamReplaysCodeInterpreterCallWithNilOutputsPlaceholder(t *testing.T) {
	app := testutil.NewTestApp(t)

	codeInterpreterCall, err := domain.NewItem([]byte(`{"id":"ci_nil_outputs_test","type":"code_interpreter_call","status":"completed","container_id":"cntr_456","code":"print(\"result=2.0\")","outputs":null}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_code_interpreter_nil_outputs",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"run some Python"}`,
		ResponseJSON:         `{"id":"resp_code_interpreter_nil_outputs","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ci_nil_outputs_test","type":"code_interpreter_call","status":"completed","container_id":"cntr_456","code":"print(\"result=2.0\")","outputs":null}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "run some Python")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "run some Python")},
		Output:               []domain.Item{codeInterpreterCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-10T10:00:00Z",
		CompletedAt:          "2026-04-10T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_code_interpreter_nil_outputs?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.in_progress")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call_code.delta")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call_code.done")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.interpreting")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "", asStringAny(addedItem["code"]))
	outputs, hasOutputs := addedItem["outputs"]
	require.True(t, hasOutputs)
	require.Nil(t, outputs)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "print(\"result=2.0\")", asStringAny(outputDoneItem["code"]))
	outputs, hasOutputs = outputDoneItem["outputs"]
	require.True(t, hasOutputs)
	require.Nil(t, outputs)
}

func TestResponsesGetStreamReplaysComputerCallWithoutLeakingActionsInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	computerCall, err := domain.NewItem([]byte(`{"id":"cu_test","type":"computer_call","status":"completed","call_id":"call_test","actions":[{"type":"click","button":"left","keys":null,"x":636,"y":343},{"type":"type","text":"penguin"}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_computer_call",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"use computer"}`,
		ResponseJSON:         `{"id":"resp_computer_call","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"cu_test","type":"computer_call","status":"completed","call_id":"call_test","actions":[{"type":"click","button":"left","keys":null,"x":636,"y":343},{"type":"type","text":"penguin"}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "use computer")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "use computer")},
		Output:               []domain.Item{computerCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-12T10:00:00Z",
		CompletedAt:          "2026-04-12T10:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_computer_call?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	for _, eventType := range eventTypes(events) {
		require.NotContains(t, eventType, "response.computer_call")
	}

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "computer_call", asStringAny(addedItem["type"]))
	require.Equal(t, "call_test", asStringAny(addedItem["call_id"]))
	require.Equal(t, "in_progress", asStringAny(addedItem["status"]))
	_, hasActions := addedItem["actions"]
	require.False(t, hasActions)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	actions, ok := outputDoneItem["actions"].([]any)
	require.True(t, ok)
	require.Len(t, actions, 2)
	firstAction, ok := actions[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "click", asStringAny(firstAction["type"]))
	secondAction, ok := actions[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "type", asStringAny(secondAction["type"]))
	require.Equal(t, "penguin", asStringAny(secondAction["text"]))
}

func TestResponsesGetStreamReplaysImageGenerationCallReplaySubset(t *testing.T) {
	app := testutil.NewTestApp(t)

	imageGenerationCall, err := domain.NewItem([]byte(`{"id":"ig_test","type":"image_generation_call","status":"completed","background":"opaque","output_format":"jpeg","quality":"low","size":"1024x1024","result":"/9j/4AAQSkZJRgABAQAAAQABAAD...","revised_prompt":"A tiny orange cat curled up in a teacup.","action":"generate"}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_image_generation_call",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"draw a tiny orange cat"}`,
		ResponseJSON:         `{"id":"resp_image_generation_call","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ig_test","type":"image_generation_call","status":"completed","background":"opaque","output_format":"jpeg","quality":"low","size":"1024x1024","result":"/9j/4AAQSkZJRgABAQAAAQABAAD...","revised_prompt":"A tiny orange cat curled up in a teacup.","action":"generate"}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":{"type":"image_generation"},"tools":[{"type":"image_generation"}],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "draw a tiny orange cat")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "draw a tiny orange cat")},
		Output:               []domain.Item{imageGenerationCall},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-12T13:00:00Z",
		CompletedAt:          "2026-04-12T13:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_image_generation_call?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	require.Contains(t, eventTypes(events), "response.image_generation_call.in_progress")
	require.Contains(t, eventTypes(events), "response.image_generation_call.generating")
	require.Contains(t, eventTypes(events), "response.image_generation_call.completed")
	require.NotContains(t, eventTypes(events), "response.image_generation_call.partial_image")

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation_call", asStringAny(addedItem["type"]))
	require.Equal(t, "in_progress", asStringAny(addedItem["status"]))
	_, hasBackground := addedItem["background"]
	require.False(t, hasBackground)
	_, hasOutputFormat := addedItem["output_format"]
	require.False(t, hasOutputFormat)
	_, hasQuality := addedItem["quality"]
	require.False(t, hasQuality)
	_, hasSize := addedItem["size"]
	require.False(t, hasSize)
	_, hasResult := addedItem["result"]
	require.False(t, hasResult)
	_, hasRevisedPrompt := addedItem["revised_prompt"]
	require.False(t, hasRevisedPrompt)
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation_call", asStringAny(outputDoneItem["type"]))
	require.Equal(t, "/9j/4AAQSkZJRgABAQAAAQABAAD...", asStringAny(outputDoneItem["result"]))
	require.Equal(t, "A tiny orange cat curled up in a teacup.", asStringAny(outputDoneItem["revised_prompt"]))
	require.Equal(t, "generate", asStringAny(outputDoneItem["action"]))
}

func TestResponsesGetStreamReplaysMCPApprovalRequestAsGenericOutputItemReplay(t *testing.T) {
	app := testutil.NewTestApp(t)

	approvalRequest, err := domain.NewItem([]byte(`{"id":"mcpr_test","type":"mcp_approval_request","arguments":"{\"diceRollExpression\":\"2d4 + 1\"}","name":"roll","server_label":"dmcp"}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_mcp_approval_request",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"Roll 2d4+1"}`,
		ResponseJSON:         `{"id":"resp_mcp_approval_request","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"mcpr_test","type":"mcp_approval_request","arguments":"{\"diceRollExpression\":\"2d4 + 1\"}","name":"roll","server_label":"dmcp"}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "Roll 2d4+1")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "Roll 2d4+1")},
		Output:               []domain.Item{approvalRequest},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-12T11:00:00Z",
		CompletedAt:          "2026-04-12T11:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_mcp_approval_request?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	for _, eventType := range eventTypes(events) {
		require.NotContains(t, eventType, "response.mcp_approval_request")
	}

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_approval_request", asStringAny(addedItem["type"]))
	require.Equal(t, "{\"diceRollExpression\":\"2d4 + 1\"}", asStringAny(addedItem["arguments"]))
	_, hasStatus := addedItem["status"]
	require.False(t, hasStatus)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_approval_request", asStringAny(outputDoneItem["type"]))
	require.Equal(t, "{\"diceRollExpression\":\"2d4 + 1\"}", asStringAny(outputDoneItem["arguments"]))
	_, hasStatus = outputDoneItem["status"]
	require.False(t, hasStatus)
}

func TestResponsesGetStreamReplaysMCPListToolsAsGenericOutputItemReplay(t *testing.T) {
	app := testutil.NewTestApp(t)

	listTools, err := domain.NewItem([]byte(`{"id":"mcpl_test","type":"mcp_list_tools","server_label":"dmcp","tools":[{"annotations":null,"description":"Given a string of text describing a dice roll...","input_schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"diceRollExpression":{"type":"string"}},"required":["diceRollExpression"],"additionalProperties":false},"name":"roll"}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_mcp_list_tools",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"Roll 2d4+1"}`,
		ResponseJSON:         `{"id":"resp_mcp_list_tools","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"mcpl_test","type":"mcp_list_tools","server_label":"dmcp","tools":[{"annotations":null,"description":"Given a string of text describing a dice roll...","input_schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"diceRollExpression":{"type":"string"}},"required":["diceRollExpression"],"additionalProperties":false},"name":"roll"}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "Roll 2d4+1")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "Roll 2d4+1")},
		Output:               []domain.Item{listTools},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-12T12:00:00Z",
		CompletedAt:          "2026-04-12T12:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_mcp_list_tools?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	for _, eventType := range eventTypes(events) {
		require.NotContains(t, eventType, "response.mcp_list_tools")
	}

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_list_tools", asStringAny(addedItem["type"]))
	require.Equal(t, "dmcp", asStringAny(addedItem["server_label"]))
	tools, ok := addedItem["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	_, hasStatus := addedItem["status"]
	require.False(t, hasStatus)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_list_tools", asStringAny(outputDoneItem["type"]))
	doneTools, ok := outputDoneItem["tools"].([]any)
	require.True(t, ok)
	require.Len(t, doneTools, 1)
	firstTool, ok := doneTools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "roll", asStringAny(firstTool["name"]))
	_, hasStatus = outputDoneItem["status"]
	require.False(t, hasStatus)
}

func TestResponsesCreateHostedToolSearchProxyPassthrough(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "gpt-5.4",
		"store": true,
		"input": "Find the shipping ETA tool first, then use it for order_42.",
		"tools": []map[string]any{
			{
				"type":        "tool_search",
				"description": "Find the project-specific tools needed to continue the task.",
			},
			{
				"type":          "function",
				"name":          "get_shipping_eta",
				"description":   "Look up shipping ETA details for an order.",
				"defer_loading": true,
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"order_id": map[string]any{"type": "string"},
					},
					"required":             []string{"order_id"},
					"additionalProperties": false,
				},
			},
		},
		"parallel_tool_calls": false,
	})

	require.Len(t, response.Output, 3)
	require.Equal(t, "tool_search_call", response.Output[0].Type)
	require.Equal(t, "tool_search_output", response.Output[1].Type)
	require.Equal(t, "function_call", response.Output[2].Type)

	searchCall := response.Output[0].Map()
	require.Equal(t, "server", asStringAny(searchCall["execution"]))
	callID, hasCallID := searchCall["call_id"]
	require.True(t, hasCallID)
	require.Nil(t, callID)

	searchOutput := response.Output[1].Map()
	require.Equal(t, "server", asStringAny(searchOutput["execution"]))
	loadedTools, ok := searchOutput["tools"].([]any)
	require.True(t, ok)
	require.Len(t, loadedTools, 1)
	firstTool, ok := loadedTools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "get_shipping_eta", asStringAny(firstTool["name"]))

	functionCall := response.Output[2].Map()
	require.Equal(t, "get_shipping_eta", asStringAny(functionCall["name"]))
	require.Equal(t, `{"order_id":"order_42"}`, asStringAny(functionCall["arguments"]))

	got := getResponse(t, app, response.ID)
	require.Len(t, got.Output, 3)
	require.Equal(t, "tool_search_call", got.Output[0].Type)
	require.Equal(t, "tool_search_output", got.Output[1].Type)
	require.Equal(t, "function_call", got.Output[2].Type)
}

func TestResponsesCreateClientToolSearchFollowupLoadsDeferredFunction(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	first := postResponse(t, app, map[string]any{
		"model": "gpt-5.4",
		"store": true,
		"input": "Find the shipping ETA tool first, then use it for order_42.",
		"tools": []map[string]any{
			{
				"type":        "tool_search",
				"execution":   "client",
				"description": "Find the project-specific tools needed to continue the task.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"goal": map[string]any{"type": "string"},
					},
					"required":             []string{"goal"},
					"additionalProperties": false,
				},
			},
		},
		"parallel_tool_calls": false,
	})

	require.Len(t, first.Output, 1)
	require.Equal(t, "tool_search_call", first.Output[0].Type)

	searchCall := first.Output[0].Map()
	require.Equal(t, "client", asStringAny(searchCall["execution"]))
	callID := asStringAny(searchCall["call_id"])
	require.NotEmpty(t, callID)
	arguments, ok := searchCall["arguments"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, asStringAny(arguments["goal"]), "shipping ETA")

	second := postResponse(t, app, map[string]any{
		"model": "gpt-5.4",
		"store": true,
		"input": []any{
			first.Output[0],
			map[string]any{
				"type":      "tool_search_output",
				"execution": "client",
				"call_id":   callID,
				"status":    "completed",
				"tools": []map[string]any{
					{
						"type":          "function",
						"name":          "get_shipping_eta",
						"description":   "Look up shipping ETA details for an order.",
						"defer_loading": true,
						"parameters": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"order_id": map[string]any{"type": "string"},
							},
							"required":             []string{"order_id"},
							"additionalProperties": false,
						},
					},
				},
			},
		},
	})

	require.Len(t, second.Output, 1)
	require.Equal(t, "function_call", second.Output[0].Type)
	functionCall := second.Output[0].Map()
	require.Equal(t, "get_shipping_eta", asStringAny(functionCall["name"]))
	require.Equal(t, `{"order_id":"order_42"}`, asStringAny(functionCall["arguments"]))

	inputItems := getResponseInputItemsWithQuery(t, app, second.ID, "?order=asc")
	require.Len(t, inputItems.Data, 2)
	require.Equal(t, "tool_search_call", asStringAny(inputItems.Data[0]["type"]))
	require.Equal(t, "tool_search_output", asStringAny(inputItems.Data[1]["type"]))
	require.Equal(t, callID, asStringAny(inputItems.Data[1]["call_id"]))
}

func TestResponsesGetStreamReplaysToolSearchAsGenericOutputItemReplay(t *testing.T) {
	app := testutil.NewTestApp(t)

	searchCall, err := domain.NewItem([]byte(`{"id":"tsc_test","type":"tool_search_call","execution":"client","call_id":"call_abc123","status":"completed","arguments":{"goal":"Find the shipping ETA tool for order_42."}}`))
	require.NoError(t, err)
	searchOutput, err := domain.NewItem([]byte(`{"id":"tso_test","type":"tool_search_output","execution":"client","call_id":"call_abc123","status":"completed","tools":[{"type":"function","name":"get_shipping_eta","description":"Look up shipping ETA details for an order.","defer_loading":true,"parameters":{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"],"additionalProperties":false}}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_tool_search",
		Model:                "gpt-5.4",
		RequestJSON:          `{"model":"gpt-5.4","store":true,"input":"Find the shipping ETA tool first, then use it for order_42."}`,
		ResponseJSON:         `{"id":"resp_tool_search","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"gpt-5.4","output":[{"id":"tsc_test","type":"tool_search_call","execution":"client","call_id":"call_abc123","status":"completed","arguments":{"goal":"Find the shipping ETA tool for order_42."}},{"id":"tso_test","type":"tool_search_output","execution":"client","call_id":"call_abc123","status":"completed","tools":[{"type":"function","name":"get_shipping_eta","description":"Look up shipping ETA details for an order.","defer_loading":true,"parameters":{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"],"additionalProperties":false}}]}],"parallel_tool_calls":false,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "Find the shipping ETA tool first, then use it for order_42.")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "Find the shipping ETA tool first, then use it for order_42.")},
		Output:               []domain.Item{searchCall, searchOutput},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-13T12:00:00Z",
		CompletedAt:          "2026-04-13T12:00:01Z",
	}
	require.NoError(t, app.Store.SaveResponse(context.Background(), stored))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/resp_tool_search?stream=true", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	for _, eventType := range eventTypes(events) {
		require.NotContains(t, eventType, "response.tool_search")
	}

	added := findEvents(events, "response.output_item.added")
	require.Len(t, added, 2)
	require.Equal(t, "tool_search_call", asStringAny(added[0].Data["item"].(map[string]any)["type"]))
	require.Equal(t, "in_progress", asStringAny(added[0].Data["item"].(map[string]any)["status"]))
	require.Equal(t, "tool_search_output", asStringAny(added[1].Data["item"].(map[string]any)["type"]))
	require.Equal(t, "in_progress", asStringAny(added[1].Data["item"].(map[string]any)["status"]))

	done := findEvents(events, "response.output_item.done")
	require.Len(t, done, 2)
	require.Equal(t, "tool_search_call", asStringAny(done[0].Data["item"].(map[string]any)["type"]))
	require.Equal(t, "completed", asStringAny(done[0].Data["item"].(map[string]any)["status"]))
	require.Equal(t, "tool_search_output", asStringAny(done[1].Data["item"].(map[string]any)["type"]))
	require.Equal(t, "completed", asStringAny(done[1].Data["item"].(map[string]any)["status"]))
}

func TestResponsesGetStreamSupportsStartingAfter(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
	})

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+response.ID+"?stream=true&starting_after=4", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := readSSEEvents(t, resp.Body)
	require.NotEmpty(t, events)
	require.Equal(t, float64(5), events[0].Data["sequence_number"])
	require.NotContains(t, eventTypes(events), "response.created")
	require.Contains(t, eventTypes(events), "response.completed")
}

func TestResponsesDeleteRemovesLocalStoredResponse(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
	})

	status, payload := rawRequest(t, app, http.MethodDelete, "/v1/responses/"+response.ID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, response.ID, payload["id"])
	require.Equal(t, "response", payload["object"])
	require.Equal(t, true, payload["deleted"])

	status, payload = rawRequest(t, app, http.MethodGet, "/v1/responses/"+response.ID, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestResponsesCancelRejectsNonBackgroundLocalResponse(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/"+response.ID+"/cancel", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "background", payload["error"].(map[string]any)["param"])
}

func TestResponsesCancelRefreshesShadowStoredBackgroundResponse(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model":      "test-model",
		"store":      true,
		"background": true,
		"metadata":   map[string]any{"topic": "demo"},
		"input":      "Do this in the background",
	})
	require.Equal(t, "in_progress", response.Status)
	require.Nil(t, response.CompletedAt)
	require.NotNil(t, response.Background)
	require.True(t, *response.Background)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/"+response.ID+"/cancel", nil)
	require.Equal(t, http.StatusOK, status)

	var cancelled domain.Response
	mustDecode(t, payload, &cancelled)
	require.Equal(t, response.ID, cancelled.ID)
	require.Equal(t, "cancelled", cancelled.Status)
	require.Nil(t, cancelled.CompletedAt)
	require.Equal(t, map[string]string{"topic": "demo"}, cancelled.Metadata)

	got := getResponse(t, app, response.ID)
	require.Equal(t, "cancelled", got.Status)
	require.Nil(t, got.CompletedAt)
	require.Equal(t, map[string]string{"topic": "demo"}, got.Metadata)
	require.NotNil(t, got.Background)
	require.True(t, *got.Background)
}

func TestResponsesInputTokensCountLocalSubset(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"model": "test-model",
		"input": "Count this input locally.",
	})
	require.Equal(t, http.StatusOK, status)

	var counted domain.ResponseInputTokens
	mustDecode(t, payload, &counted)
	require.Equal(t, "response.input_tokens", counted.Object)
	require.Greater(t, counted.InputTokens, 0)
}

func TestResponsesInputTokensAllowsEmptyBody(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", nil)
	require.Equal(t, http.StatusOK, status)

	var counted domain.ResponseInputTokens
	mustDecode(t, payload, &counted)
	require.Equal(t, "response.input_tokens", counted.Object)
	require.Zero(t, counted.InputTokens)
}

func TestResponsesInputTokensAcceptConversationObject(t *testing.T) {
	app := testutil.NewTestApp(t)

	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Remember the code is 777.",
			},
		},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"conversation": map[string]any{"id": conversation.ID},
	})
	require.Equal(t, http.StatusOK, status)

	var counted domain.ResponseInputTokens
	mustDecode(t, payload, &counted)
	require.Equal(t, "response.input_tokens", counted.Object)
	require.Greater(t, counted.InputTokens, 0)
}

func TestResponsesInputTokensIncludePreviousResponseState(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Remember that the secret code is 777.",
	})
	require.NotEmpty(t, first.ID)

	baseStatus, basePayload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"model": "test-model",
		"input": "What is the code?",
	})
	require.Equal(t, http.StatusOK, baseStatus)
	var base domain.ResponseInputTokens
	mustDecode(t, basePayload, &base)

	statefulStatus, statefulPayload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"model":                "test-model",
		"previous_response_id": first.ID,
		"input":                "What is the code?",
	})
	require.Equal(t, http.StatusOK, statefulStatus)
	var stateful domain.ResponseInputTokens
	mustDecode(t, statefulPayload, &stateful)

	require.Greater(t, stateful.InputTokens, base.InputTokens)
}

func TestResponsesInputTokensPreferUpstreamWhenNoLocalState(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/input_tokens", map[string]any{
		"model": "test-model",
		"input": "123456",
	})
	require.Equal(t, http.StatusOK, status)

	var counted domain.ResponseInputTokens
	mustDecode(t, payload, &counted)
	require.Equal(t, "response.input_tokens", counted.Object)
	require.Equal(t, 3, counted.InputTokens)
}

func TestResponsesCompactReturnsSyntheticCompactionResource(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "test-model",
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Remember that the launch code is 777.",
			},
			{
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]any{{"type": "output_text", "text": "I will remember the launch code."}},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)

	var compacted domain.ResponseCompaction
	mustDecode(t, payload, &compacted)
	require.NotEmpty(t, compacted.ID)
	require.Equal(t, "response.compaction", compacted.Object)
	require.NotZero(t, compacted.CreatedAt)
	require.Len(t, compacted.Output, 1)
	require.Equal(t, "compaction", compacted.Output[0].Type)
	require.NotEmpty(t, compacted.Output[0].StringField("encrypted_content"))

	var usage map[string]any
	require.NoError(t, json.Unmarshal(compacted.Usage, &usage))
	require.Greater(t, int(usage["input_tokens"].(float64)), 0)
	require.Greater(t, int(usage["output_tokens"].(float64)), 0)
	require.Greater(t, int(usage["total_tokens"].(float64)), 0)
}

func TestResponsesCompactAllowsModelOnlyRequest(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "test-model",
	})
	require.Equal(t, http.StatusOK, status)

	var compacted domain.ResponseCompaction
	mustDecode(t, payload, &compacted)
	require.NotEmpty(t, compacted.ID)
	require.Equal(t, "response.compaction", compacted.Object)
	require.Len(t, compacted.Output, 1)
	require.Equal(t, "compaction", compacted.Output[0].Type)
	require.NotEmpty(t, compacted.Output[0].StringField("encrypted_content"))
}

func TestResponsesCompactOutputCanBeUsedInNextLocalResponse(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "test-model",
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "You are helping with a launch checklist. The code is 777.",
			},
			{
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]any{{"type": "output_text", "text": "Understood. I will keep the launch checklist in mind."}},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)

	var compacted domain.ResponseCompaction
	mustDecode(t, payload, &compacted)
	require.Len(t, compacted.Output, 1)

	next := postResponse(t, app, map[string]any{
		"model": "test-model",
		"input": []any{
			compacted.Output[0].Map(),
			map[string]any{
				"type":    "message",
				"role":    "user",
				"content": "Reply with just OK.",
			},
		},
	})
	require.Equal(t, "completed", next.Status)
	require.NotEmpty(t, next.OutputText)
}

func TestResponsesCompactPreferUpstreamWhenNoLocalState(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "test-model",
		"input": "123456",
	})
	require.Equal(t, http.StatusOK, status)

	var compacted domain.ResponseCompaction
	mustDecode(t, payload, &compacted)
	require.Equal(t, "response.compaction", compacted.Object)
	require.True(t, strings.HasPrefix(compacted.ID, "upstream_compact_"))
	require.Len(t, compacted.Output, 1)
	require.Equal(t, "upstream-opaque-compaction", compacted.Output[0].StringField("encrypted_content"))
}

func TestResponsesPreviousResponseID(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Remember: my code = 123. Reply OK",
	})
	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input":                "What was my code? Reply with just the number.",
	})

	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "123", second.OutputText)
}

func TestResponseInputItemsPagination(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": []map[string]any{
			{"type": "message", "role": "system", "content": "one"},
			{"type": "message", "role": "user", "content": "two"},
			{"type": "message", "role": "user", "content": "three"},
		},
	})

	firstPage := getResponseInputItemsWithQuery(t, app, response.ID, "?limit=2&order=asc&include=message.output_text.logprobs")
	require.Equal(t, "list", firstPage.Object)
	require.Len(t, firstPage.Data, 2)
	require.True(t, firstPage.HasMore)
	require.NotNil(t, firstPage.FirstID)
	require.NotNil(t, firstPage.LastID)

	secondPage := getResponseInputItemsWithQuery(t, app, response.ID, "?limit=2&order=asc&after="+*firstPage.LastID)
	require.Len(t, secondPage.Data, 1)
	require.False(t, secondPage.HasMore)
	require.NotNil(t, secondPage.FirstID)
	require.Equal(t, *secondPage.FirstID, *secondPage.LastID)
}

func TestResponseInputItemsRejectInvalidAfter(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
	})

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/responses/"+response.ID+"/input_items?after=item_missing", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "after", payload["error"].(map[string]any)["param"])
}

func TestResponseInputItemsIncludeLineageContext(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Remember: my code = 123. Reply OK",
	})
	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input":                "What was my code? Reply with just the number.",
	})

	items := getResponseInputItemsWithQuery(t, app, second.ID, "?order=asc")
	require.Len(t, items.Data, 3)
	require.Equal(t, "Remember: my code = 123. Reply OK", firstContentText(items.Data[0]))
	require.Equal(t, "OK", firstContentText(items.Data[1]))
	require.Equal(t, "What was my code? Reply with just the number.", firstContentText(items.Data[2]))
}

func TestResponsesPreviousResponseIDStoreFalseRemainsHiddenButUsable(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Remember: my code = 123. Reply OK",
	})
	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                false,
		"previous_response_id": first.ID,
		"input":                "What was my code? Reply with just the number.",
	})

	require.Equal(t, first.ID, second.PreviousResponseID)
	require.False(t, *second.Store)
	require.Equal(t, "123", second.OutputText)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/responses/"+second.ID, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])

	third := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                false,
		"previous_response_id": second.ID,
		"input":                "What was my code? Reply with just the number.",
	})

	require.Equal(t, second.ID, third.PreviousResponseID)
	require.False(t, *third.Store)
	require.Equal(t, "123", third.OutputText)
}

func TestResponsesPreviousResponseIDWithSupportedGenerationFieldsUsesLocalShim(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"input": "Remember: my code = 123. Reply OK",
		"reasoning": map[string]any{
			"effort": "minimal",
		},
		"temperature": 0,
	})
	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"previous_response_id": first.ID,
		"input":                "What was my code? Reply with just the number.",
		"reasoning": map[string]any{
			"effort": "minimal",
		},
		"temperature": 0,
	})

	require.NotEmpty(t, first.ID)
	require.NotEqual(t, "upstream_resp_1", first.ID)
	require.NotEmpty(t, second.ID)
	require.NotEqual(t, "upstream_resp_2", second.ID)
	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "123", second.OutputText)
}

func TestResponsesConversationMode(t *testing.T) {
	app := testutil.NewTestApp(t)

	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "system", "content": "You are a test assistant."},
			{"type": "message", "role": "user", "content": "Remember: code=777. Reply OK."},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"conversation": conversation.ID,
		"input":        "What is the code? Reply with just the number.",
	})

	require.Equal(t, conversation.ID, responseConversationID(response))
	require.Equal(t, "777", response.OutputText)
}

func TestCreateConversationReturnsOfficialResourceShape(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/conversations", map[string]any{
		"metadata": map[string]any{"topic": "demo"},
		"items": []map[string]any{
			{"type": "message", "role": "user", "content": "Hello!"},
		},
	})
	require.Equal(t, http.StatusOK, status)
	_, hasItems := payload["items"]
	require.False(t, hasItems)

	var conversation conversationResource
	mustDecode(t, payload, &conversation)
	require.NotEmpty(t, conversation.ID)
	require.Equal(t, "conversation", conversation.Object)
	require.NotZero(t, conversation.CreatedAt)
	require.Equal(t, map[string]string{"topic": "demo"}, conversation.Metadata)
}

func TestCreateConversationAllowsEmptyBody(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/conversations", nil)
	require.Equal(t, http.StatusOK, status)

	var conversation conversationResource
	mustDecode(t, payload, &conversation)
	require.NotEmpty(t, conversation.ID)
	require.Equal(t, "conversation", conversation.Object)
	require.NotZero(t, conversation.CreatedAt)
	require.Empty(t, conversation.Metadata)
}

func TestCreateConversationRejectsTooManyInitialItems(t *testing.T) {
	app := testutil.NewTestApp(t)

	items := make([]map[string]any, 0, 21)
	for i := 0; i < 21; i++ {
		items = append(items, map[string]any{
			"type":    "message",
			"role":    "user",
			"content": "hello",
		})
	}

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/conversations", map[string]any{
		"items": items,
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "items", payload["error"].(map[string]any)["param"])
}

func TestGetConversationReturnsOfficialShape(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID, nil)
	require.Equal(t, http.StatusOK, status)
	_, hasItems := payload["items"]
	require.False(t, hasItems)

	var got conversationResource
	mustDecode(t, payload, &got)
	require.Equal(t, conversation.ID, got.ID)
	require.Equal(t, "conversation", got.Object)
	require.NotZero(t, got.CreatedAt)
	require.Empty(t, got.Metadata)

	items := getConversationItems(t, app, conversation.ID, "?order=asc")
	require.Equal(t, []string{"system", "user", "user", "assistant"}, conversationItemRoles(items))
	require.Equal(t, []string{
		"You are a test assistant.",
		"Remember: code=777. Reply OK.",
		"What is the code? Reply with just the number.",
		"777",
	}, conversationItemTexts(items))
}

func TestConversationItemsDefaultDesc(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	items := getConversationItems(t, app, conversation.ID, "")
	require.Equal(t, "list", items.Object)
	require.Len(t, items.Data, 4)
	require.False(t, items.HasMore)
	require.NotNil(t, items.FirstID)
	require.NotNil(t, items.LastID)
	require.Equal(t, payloadID(items.Data[0]), *items.FirstID)
	require.Equal(t, payloadID(items.Data[len(items.Data)-1]), *items.LastID)

	require.Equal(t, []string{"message", "message", "message", "message"}, conversationItemTypes(items))
	require.Equal(t, []string{"assistant", "user", "user", "system"}, conversationItemRoles(items))
	require.Equal(t, []string{
		"777",
		"What is the code? Reply with just the number.",
		"Remember: code=777. Reply OK.",
		"You are a test assistant.",
	}, conversationItemTexts(items))
}

func TestConversationItemsAscendingOrder(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	items := getConversationItems(t, app, conversation.ID, "?order=asc")
	require.Equal(t, "list", items.Object)
	require.Len(t, items.Data, 4)
	require.False(t, items.HasMore)
	require.NotNil(t, items.FirstID)
	require.NotNil(t, items.LastID)
	require.Equal(t, payloadID(items.Data[0]), *items.FirstID)
	require.Equal(t, payloadID(items.Data[len(items.Data)-1]), *items.LastID)

	require.Equal(t, []string{"system", "user", "user", "assistant"}, conversationItemRoles(items))
	require.Equal(t, []string{
		"You are a test assistant.",
		"Remember: code=777. Reply OK.",
		"What is the code? Reply with just the number.",
		"777",
	}, conversationItemTexts(items))
}

func TestConversationItemsPagination(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	descFirstPage := getConversationItems(t, app, conversation.ID, "?limit=2")
	require.Len(t, descFirstPage.Data, 2)
	require.True(t, descFirstPage.HasMore)
	require.NotNil(t, descFirstPage.FirstID)
	require.NotNil(t, descFirstPage.LastID)
	require.Equal(t, payloadID(descFirstPage.Data[0]), *descFirstPage.FirstID)
	require.Equal(t, payloadID(descFirstPage.Data[1]), *descFirstPage.LastID)
	require.Equal(t, []string{"777", "What is the code? Reply with just the number."}, conversationItemTexts(descFirstPage))

	descSecondPage := getConversationItems(t, app, conversation.ID, "?limit=2&after="+*descFirstPage.LastID)
	require.Len(t, descSecondPage.Data, 2)
	require.False(t, descSecondPage.HasMore)
	require.NotNil(t, descSecondPage.FirstID)
	require.NotNil(t, descSecondPage.LastID)
	require.Equal(t, payloadID(descSecondPage.Data[0]), *descSecondPage.FirstID)
	require.Equal(t, payloadID(descSecondPage.Data[1]), *descSecondPage.LastID)
	require.Equal(t, []string{"Remember: code=777. Reply OK.", "You are a test assistant."}, conversationItemTexts(descSecondPage))

	ascFirstPage := getConversationItems(t, app, conversation.ID, "?limit=2&order=asc")
	require.Len(t, ascFirstPage.Data, 2)
	require.True(t, ascFirstPage.HasMore)
	require.NotNil(t, ascFirstPage.LastID)
	require.Equal(t, []string{"You are a test assistant.", "Remember: code=777. Reply OK."}, conversationItemTexts(ascFirstPage))

	ascSecondPage := getConversationItems(t, app, conversation.ID, "?limit=2&order=asc&after="+*ascFirstPage.LastID)
	require.Len(t, ascSecondPage.Data, 2)
	require.False(t, ascSecondPage.HasMore)
	require.NotNil(t, ascSecondPage.LastID)
	require.Equal(t, []string{"What is the code? Reply with just the number.", "777"}, conversationItemTexts(ascSecondPage))

	emptyPage := getConversationItems(t, app, conversation.ID, "?limit=2&order=asc&after="+*ascSecondPage.LastID)
	require.Empty(t, emptyPage.Data)
	require.False(t, emptyPage.HasMore)
	require.Nil(t, emptyPage.FirstID)
	require.Nil(t, emptyPage.LastID)
}

func TestGetMissingResponseReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/responses/resp_missing", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestCreateResponseMissingConversationReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":        "test-model",
		"conversation": "conv_missing",
		"input":        "hello",
	})
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestCreateResponseRejectsMutuallyExclusiveStateFields(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":                "test-model",
		"previous_response_id": "resp_1",
		"conversation":         "conv_1",
		"input":                "hello",
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
}

func TestResponsesCanonicalizeWrappedUpstreamValidationError(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": 1,
	})
	require.Equal(t, http.StatusBadRequest, status)

	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Equal(t, "Input should be a valid string", errorPayload["message"])
	require.Contains(t, errorPayload, "param")
	require.Nil(t, errorPayload["param"])
	require.Contains(t, errorPayload, "code")
	require.Nil(t, errorPayload["code"])
}

func TestConversationItemsMissingConversationReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/conv_missing/items", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestGetConversationMissingConversationReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/conv_missing", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestCreateConversationItemsAndFollowUpResponse(t *testing.T) {
	app := testutil.NewTestApp(t)

	conversation := postConversation(t, app, map[string]any{
		"metadata": map[string]any{"topic": "append"},
		"items": []map[string]any{
			{"type": "message", "role": "system", "content": "You are a test assistant."},
		},
	})

	appended := postConversationItems(t, app, conversation.ID, map[string]any{
		"items": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Remember: code=777. Reply OK.",
			},
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Also remember: city=Paris."},
				},
			},
		},
	})
	require.Equal(t, "list", appended.Object)
	require.Len(t, appended.Data, 2)
	require.NotNil(t, appended.FirstID)
	require.NotNil(t, appended.LastID)
	require.Equal(t, payloadID(appended.Data[0]), *appended.FirstID)
	require.Equal(t, payloadID(appended.Data[1]), *appended.LastID)
	require.Equal(t, "message", asStringAny(appended.Data[0]["type"]))
	require.Equal(t, "user", asStringAny(appended.Data[0]["role"]))

	gotItem := getConversationItem(t, app, conversation.ID, payloadID(appended.Data[0]))
	require.Equal(t, payloadID(appended.Data[0]), payloadID(gotItem))
	require.Equal(t, "Remember: code=777. Reply OK.", messageTextFromPayload(gotItem))

	items := getConversationItems(t, app, conversation.ID, "?order=asc")
	require.Len(t, items.Data, 3)
	require.Equal(t, []string{
		"You are a test assistant.",
		"Remember: code=777. Reply OK.",
		"Also remember: city=Paris.",
	}, conversationItemTexts(items))

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"conversation": conversation.ID,
		"input":        "What is the code? Reply with just the number.",
	})
	require.Equal(t, "777", response.OutputText)
}

func TestDeleteConversationItemRemovesItemAndAllowsFurtherAppend(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	items := getConversationItems(t, app, conversation.ID, "?order=asc")
	require.Len(t, items.Data, 4)
	deleteID := payloadID(items.Data[0])

	status, payload := rawRequest(t, app, http.MethodDelete, "/v1/conversations/"+conversation.ID+"/items/"+deleteID, nil)
	require.Equal(t, http.StatusOK, status)

	var got conversationResource
	mustDecode(t, payload, &got)
	require.Equal(t, conversation.ID, got.ID)
	require.Equal(t, "conversation", got.Object)
	require.NotZero(t, got.CreatedAt)

	status, payload = rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items/"+deleteID, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])

	appended := postConversationItems(t, app, conversation.ID, map[string]any{
		"items": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Also remember: city=Paris.",
			},
		},
	})
	require.Len(t, appended.Data, 1)

	remaining := getConversationItems(t, app, conversation.ID, "?order=asc")
	require.Equal(t, []string{
		"Remember: code=777. Reply OK.",
		"What is the code? Reply with just the number.",
		"777",
		"Also remember: city=Paris.",
	}, conversationItemTexts(remaining))

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"conversation": conversation.ID,
		"input":        "What is the code? Reply with just the number.",
	})
	require.Equal(t, "777", response.OutputText)
}

func TestGetConversationItemMissingReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "user", "content": "Hello!"},
		},
	})

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items/item_missing", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestDeleteConversationItemMissingReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "user", "content": "Hello!"},
		},
	})

	status, payload := rawRequest(t, app, http.MethodDelete, "/v1/conversations/"+conversation.ID+"/items/item_missing", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestAppendConversationItemMissingConversationReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/conversations/conv_missing/items", map[string]any{
		"items": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Hello!",
			},
		},
	})
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestDeleteConversationItemMissingConversationReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodDelete, "/v1/conversations/conv_missing/items/item_missing", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
}

func TestCreateConversationItemsAcceptsSupportedInclude(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := postConversation(t, app, nil)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/conversations/"+conversation.ID+"/items?include=web_search_call.action.sources", map[string]any{
		"items": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Hello!",
			},
		},
	})
	require.Equal(t, http.StatusOK, status)

	var items conversationItemsListResponse
	mustDecode(t, payload, &items)
	require.Equal(t, "list", items.Object)
	require.Len(t, items.Data, 1)
}

func TestCreateConversationItemsRejectsTooManyItems(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := postConversation(t, app, nil)

	items := make([]map[string]any, 0, 21)
	for i := 0; i < 21; i++ {
		items = append(items, map[string]any{
			"type":    "message",
			"role":    "user",
			"content": "hello",
		})
	}

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/conversations/"+conversation.ID+"/items", map[string]any{
		"items": items,
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "items", payload["error"].(map[string]any)["param"])
}

func TestConversationItemsRejectInvalidLimit(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items?limit=0", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "limit", payload["error"].(map[string]any)["param"])
}

func TestConversationItemsRejectInvalidOrder(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items?order=sideways", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "order", payload["error"].(map[string]any)["param"])
}

func TestConversationItemsAcceptSupportedInclude(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items?include=code_interpreter_call.outputs", nil)
	require.Equal(t, http.StatusOK, status)

	var items conversationItemsListResponse
	mustDecode(t, payload, &items)
	require.Equal(t, "list", items.Object)
	require.NotEmpty(t, items.Data)
}

func TestConversationItemsRejectUnsupportedInclude(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items?include=message.output_text.logprobs", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "include", payload["error"].(map[string]any)["param"])
}

func TestGetConversationItemAcceptsSupportedInclude(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "user", "content": "Hello!"},
		},
	})

	items := getConversationItems(t, app, conversation.ID, "?order=asc")
	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items/"+payloadID(items.Data[0])+"?include=file_search_call.results", nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, payloadID(items.Data[0]), payloadID(payload))
}

func TestConversationItemsRejectInvalidAfter(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items?after=item_missing", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "after", payload["error"].(map[string]any)["param"])
}

func TestConversationItemsRejectAfterFromAnotherConversation(t *testing.T) {
	app := testutil.NewTestApp(t)
	firstConversation := seedConversationWithResponse(t, app)
	secondConversation := seedConversationWithResponse(t, app)
	secondItems := getConversationItems(t, app, secondConversation.ID, "")

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+firstConversation.ID+"/items?after="+payloadID(secondItems.Data[0]), nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "after", payload["error"].(map[string]any)["param"])
}

func TestModelsAreProxied(t *testing.T) {
	app := testutil.NewTestApp(t)

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/models", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	data, ok := payload["data"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, data)
}

func TestUnknownPostRouteIsProxied(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/echo?foo=bar", map[string]any{
		"hello": "world",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "POST", payload["method"])
	require.Equal(t, "/v1/echo", payload["path"])
	require.Equal(t, "foo=bar", payload["query"])
	require.JSONEq(t, `{"hello":"world"}`, payload["body"].(string))
}

func TestProxySSEPassesThrough(t *testing.T) {
	app := testutil.NewTestApp(t)

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/sse", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"index":3`)
	require.Contains(t, string(body), "data: [DONE]")
}

func TestChatCompletionsStreamPassesThrough(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": "Say OK and nothing else",
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/chat/completions", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.NotEmpty(t, events)
	require.Equal(t, "[DONE]", events[len(events)-1].Raw)

	var deltaText strings.Builder
	for _, event := range events[:len(events)-1] {
		require.Empty(t, event.Event)

		choices, ok := event.Data["choices"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, choices)

		choice, ok := choices[0].(map[string]any)
		require.True(t, ok)

		if finishReason, ok := choice["finish_reason"].(string); ok {
			require.Equal(t, "stop", finishReason)
			continue
		}

		delta, ok := choice["delta"].(map[string]any)
		require.True(t, ok)

		content, ok := delta["content"].(string)
		require.True(t, ok)
		deltaText.WriteString(content)
	}

	require.Equal(t, "OK", deltaText.String())
}

func TestResponsesStream(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"store":  true,
		"stream": true,
		"input":  "Say OK and nothing else",
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.NotEmpty(t, events)
	require.Equal(t, "response.created", events[0].Event)
	require.Contains(t, eventTypes(events), "response.output_text.delta")
	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID, ok := responsePayload["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, responseID)
	require.Equal(t, "OK", responsePayload["output_text"])

	got := getResponse(t, app, responseID)
	require.Equal(t, responseID, got.ID)
	require.Equal(t, "OK", got.OutputText)
}

func TestResponsesStreamLocalShimIncludesCoreStreamingEvents(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"store":  true,
		"stream": true,
		"input":  "Say OK and nothing else",
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.NotEmpty(t, events)
	require.Equal(t, "response.created", events[0].Event)
	require.Contains(t, eventTypes(events), "response.in_progress")
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.content_part.added")
	require.Contains(t, eventTypes(events), "response.output_text.delta")
	require.Contains(t, eventTypes(events), "response.output_text.done")
	require.Contains(t, eventTypes(events), "response.content_part.done")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	require.Contains(t, eventTypes(events), "response.completed")
	require.Equal(t, "[DONE]", events[len(events)-1].Raw)

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)

	deltaEvents := findEvents(events, "response.output_text.delta")
	require.NotEmpty(t, deltaEvents)
	var deltaText strings.Builder
	for _, event := range deltaEvents {
		deltaText.WriteString(asStringAny(event.Data["delta"]))
	}
	require.Equal(t, "OK", deltaText.String())
	require.NotEmpty(t, asStringAny(deltaEvents[0].Data["obfuscation"]))

	done := findEvent(t, events, "response.output_text.done").Data
	require.Equal(t, responseID, asStringAny(done["response_id"]))
}

func TestResponsesStreamLocalShimCanDisableObfuscation(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"store":  true,
		"stream": true,
		"input":  "Say OK and nothing else",
		"stream_options": map[string]any{
			"include_obfuscation": false,
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	delta := findEvent(t, events, "response.output_text.delta").Data
	_, hasObfuscation := delta["obfuscation"]
	require.False(t, hasObfuscation)
	require.Equal(t, "[DONE]", events[len(events)-1].Raw)
}

func TestResponsesStreamRejectsStreamOptionsWithoutStreaming(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Say OK and nothing else",
		"stream_options": map[string]any{
			"include_obfuscation": false,
		},
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "stream_options", payload["error"].(map[string]any)["param"])
}

func TestResponsesStreamNormalizesDeltaOnlyUpstreamFlow(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"store":  true,
		"stream": true,
		"input":  "delta only stream",
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.NotEmpty(t, events)
	require.Equal(t, "response.created", events[0].Event)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.output_text.delta")
	require.Contains(t, eventTypes(events), "response.output_text.done")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	require.Contains(t, eventTypes(events), "response.completed")

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)
	require.Equal(t, "DELTA_ONLY_STREAM_OK", asStringAny(responsePayload["output_text"]))

	got := getResponse(t, app, responseID)
	require.Equal(t, responseID, got.ID)
	require.Equal(t, "DELTA_ONLY_STREAM_OK", got.OutputText)
}

func TestResponsesWithSupportedGenerationFieldsUseLocalShimByDefault(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
		"reasoning": map[string]any{
			"effort": "minimal",
		},
		"temperature": 0,
	})

	require.NotEmpty(t, response.ID)
	require.NotEqual(t, "upstream_resp_1", response.ID)
	require.Equal(t, "OK", response.OutputText)

	got := getResponse(t, app, response.ID)
	require.Equal(t, response.ID, got.ID)
	require.Equal(t, "OK", got.OutputText)
}

func TestResponsesWithSupportedGenerationFieldsPreferUpstreamProxyAndShadowStore(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
		"reasoning": map[string]any{
			"effort": "minimal",
		},
		"temperature": 0,
	})

	require.Equal(t, "upstream_resp_1", response.ID)
	require.Equal(t, "OK", response.OutputText)

	got := getResponse(t, app, response.ID)
	require.Equal(t, response.ID, got.ID)
	require.Equal(t, "OK", got.OutputText)
}

func TestResponsesWithJSONTextFormatAreHandledLocally(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": `Reply with JSON object {"ok":true} and nothing else.`,
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})

	require.NotEqual(t, "upstream_resp_1", response.ID)
	require.JSONEq(t, `{"ok":true}`, response.OutputText)
	require.JSONEq(t, `{"format":{"type":"json_object"}}`, string(response.Text))

	got := getResponse(t, app, response.ID)
	require.Equal(t, response.ID, got.ID)
	require.JSONEq(t, `{"ok":true}`, got.OutputText)
	require.JSONEq(t, `{"format":{"type":"json_object"}}`, string(got.Text))
}

func TestResponsesLocalOnlyRejectsJSONModeWithoutJSONInstruction(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModeLocalOnly)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload := payload["error"].(map[string]any)
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Equal(t, "text.format", errorPayload["param"])
	require.Contains(t, asStringAny(errorPayload["message"]), `"JSON"`)
}

func TestResponsesWithJSONSchemaAreHandledLocally(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Reply with JSON object containing answer and count.",
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer": map[string]any{"type": "string"},
						"count":  map[string]any{"type": "integer"},
					},
					"required":             []string{"answer", "count"},
					"additionalProperties": false,
				},
			},
		},
	})

	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, "upstream_resp_1", asStringAny(body["id"]))
	require.JSONEq(t, `{"answer":"OK","count":1}`, asStringAny(body["output_text"]))
	textPayload, ok := body["text"].(map[string]any)
	require.True(t, ok)
	formatPayload, ok := textPayload["format"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "json_schema", formatPayload["type"])
	require.Equal(t, true, formatPayload["strict"])
}

func TestResponsesJSONSchemaRejectsUnsupportedSchemaFeatures(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": `Reply with JSON object {"ok":true} and nothing else.`,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"oneOf": []map[string]any{
						{"type": "object"},
					},
				},
			},
		},
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload := payload["error"].(map[string]any)
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Equal(t, "text.format.schema", errorPayload["param"])
	require.Contains(t, asStringAny(errorPayload["message"]), "oneOf")
}

func TestResponsesStreamJSONTextFormatCompletesWithStructuredTextConfig(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"input":  `Reply with JSON object {"ok":true} and nothing else.`,
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	created := findEvent(t, events, "response.created").Data
	createdResponse, ok := created["response"].(map[string]any)
	require.True(t, ok)
	createdText, ok := createdResponse["text"].(map[string]any)
	require.True(t, ok)
	createdFormat, ok := createdText["format"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "json_object", createdFormat["type"])
	require.Contains(t, eventTypes(events), "response.output_text.done")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	require.Contains(t, eventTypes(events), "response.completed")

	done := findEvent(t, events, "response.output_item.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	content, ok := doneItem["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	part, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.JSONEq(t, `{"ok":true}`, asStringAny(part["text"]))

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	require.JSONEq(t, `{"ok":true}`, asStringAny(responsePayload["output_text"]))
	textPayload, ok := responsePayload["text"].(map[string]any)
	require.True(t, ok)
	formatPayload, ok := textPayload["format"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "json_object", formatPayload["type"])
}

func TestResponsesStreamJSONModeWithoutJSONInstructionFailsBeforeSSEStarts(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModeLocalOnly)

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"input":  "Say OK and nothing else",
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	require.NotContains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	errorPayload := payload["error"].(map[string]any)
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Equal(t, "text.format", errorPayload["param"])
	require.Contains(t, asStringAny(errorPayload["message"]), `"JSON"`)
}

func TestResponsesPreferLocalHandlesGrammarCustomToolsLocally(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})

	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, "upstream_resp_1", body["id"])

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "math_exp", item["name"])
	require.Equal(t, "4 + 4", item["input"])
}

func TestResponsesLocalOnlyHandlesGrammarCustomToolsLocally(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode:   config.ResponsesModeLocalOnly,
		CustomToolsMode: "auto",
	})

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})

	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, "upstream_resp_1", body["id"])
	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "4 + 4", item["input"])
}

func TestResponsesStreamHandlesGrammarCustomToolsLocally(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"input": []map[string]any{
			{"role": "user", "content": "Use grammar tool"},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.custom_tool_call_input.delta")
	require.Contains(t, eventTypes(events), "response.custom_tool_call_input.done")

	done := findEvent(t, events, "response.custom_tool_call_input.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", doneItem["type"])
	require.Equal(t, "4 + 4", doneItem["input"])
}

func TestResponsesPreferLocalRepairsInvalidGrammarCustomToolOutput(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Invalid grammar first attempt. Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})

	require.Equal(t, http.StatusOK, status)
	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "4 + 4", item["input"])
}

func TestResponsesStreamRepairsInvalidGrammarCustomToolOutput(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"input": []map[string]any{
			{"role": "user", "content": "Invalid grammar first attempt. Use grammar tool"},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	events := readSSEEvents(t, resp.Body)
	done := findEvent(t, events, "response.custom_tool_call_input.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", doneItem["type"])
	require.Equal(t, "4 + 4", doneItem["input"])
}

func TestResponsesPreferLocalFailsWhenGrammarCustomToolRepairIsExhausted(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Always invalid grammar tool. Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})

	require.Equal(t, http.StatusBadGateway, status)
	errorPayload := payload["error"].(map[string]any)
	require.Equal(t, "upstream_error", errorPayload["type"])
	require.Equal(t, "llama.cpp request failed", errorPayload["message"])
}

func TestResponsesLocalOnlyRejectsUnsupportedGrammarCustomTools(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode:   config.ResponsesModeLocalOnly,
		CustomToolsMode: "auto",
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: expr ADD INT | INT\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload := payload["error"].(map[string]any)
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Equal(t, "tools", errorPayload["param"])
	require.Contains(t, asStringAny(errorPayload["message"]), "recursive lark rule")
}

func TestResponsesRetryStructuredInputAsStringForProxyRequests(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":       "test-model",
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "backend rejects structured input arrays. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})

	require.Equal(t, http.StatusOK, status)
	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "add", item["name"])
}

func TestResponsesStreamRetryStructuredInputAsStringForProxyRequests(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":       "test-model",
		"stream":      true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "backend rejects structured input arrays. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.function_call_arguments.done")
}

func TestResponsesDisabledWebSearchToolIsDroppedForUpstreamCompatibility(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
		"tools": []map[string]any{
			{
				"type":                "web_search",
				"external_web_access": false,
			},
		},
		"tool_choice": "auto",
	})

	require.Equal(t, "upstream_resp_1", response.ID)
	require.Equal(t, "OK", response.OutputText)
}

func TestResponsesEnabledWebSearchToolReturnsValidationError(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Search the web",
		"tools": []map[string]any{
			{
				"type": "web_search",
			},
		},
	})

	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "tools", payload["error"].(map[string]any)["param"])
}

func TestResponsesWithJSONTextFormatKeepLocalConversationState(t *testing.T) {
	app := testutil.NewTestApp(t)

	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "system", "content": "You are a JSON test assistant."},
			{"type": "message", "role": "user", "content": "Remember: code=777. Reply OK."},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"conversation": conversation.ID,
		"input":        "What is the code? Reply with JSON object containing code.",
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})
	require.NotEqual(t, "upstream_resp_1", response.ID)
	require.Equal(t, conversation.ID, responseConversationID(response))
	require.JSONEq(t, `{"code":777}`, response.OutputText)
}

func TestResponsesCustomToolsAreBridged(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":       "test-model",
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Use the code_exec tool to print hello world to the console. Do not answer directly.",
			},
		},
		"tools": []map[string]any{
			{
				"type":        "custom",
				"name":        "code_exec",
				"description": "Executes arbitrary Python code",
			},
		},
	})

	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, "upstream_resp_1", body["id"])
	require.Empty(t, body["output_text"])

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)

	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "code_exec", item["name"])
	require.NotEmpty(t, item["call_id"])
	require.NotEmpty(t, item["id"])
	require.Equal(t, `print("hello world")`, item["input"])
}

func TestResponsesFunctionToolsRemainFunctionCalls(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":       "test-model",
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})

	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, "upstream_resp_1", body["id"])

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)

	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "add", item["name"])
	require.Equal(t, `{"a":1,"b":2}`, item["arguments"])
}

func TestResponsesRetryToolChoiceWithAutoOnUnsupportedBackend(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":       "test-model",
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "auto-only tool_choice backend. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})

	require.Equal(t, http.StatusOK, status)

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)

	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "add", item["name"])
}

func TestResponsesRetryToolChoiceWithAutoRejectsAssistantText(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":       "test-model",
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "auto-only tool_choice backend returns text. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})

	require.Equal(t, http.StatusNotImplemented, status)
	errorPayload, ok := body["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "server_error", errorPayload["type"])
	require.Equal(t, "tool_choice", errorPayload["param"])
	require.Equal(t, "tool_choice_incompatible_backend", errorPayload["code"])
}

func TestResponsesCustomToolsStreamAreBridgedAndShadowStored(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":       "test-model",
		"store":       true,
		"stream":      true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Use the code_exec tool and do not answer directly.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.custom_tool_call_input.delta")
	require.Contains(t, eventTypes(events), "response.custom_tool_call_input.done")

	added := findEvent(t, events, "response.output_item.added").Data
	item, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "code_exec", item["name"])
	require.Equal(t, "", asStringAny(item["input"]))

	done := findEvent(t, events, "response.custom_tool_call_input.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", doneItem["type"])
	require.Equal(t, "code_exec", doneItem["name"])
	require.Equal(t, `print("hello world")`, asStringAny(done["input"]))
	require.Equal(t, `print("hello world")`, asStringAny(doneItem["input"]))

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)
	output, ok := responsePayload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	completedItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", completedItem["type"])
	require.Equal(t, "code_exec", completedItem["name"])
	require.Equal(t, `print("hello world")`, completedItem["input"])

	got := getResponse(t, app, responseID)
	require.Equal(t, responseID, got.ID)
	require.Len(t, got.Output, 1)
	require.Equal(t, "custom_tool_call", got.Output[0].Type)
	require.Equal(t, "code_exec", got.Output[0].Name())
	require.Equal(t, `print("hello world")`, got.Output[0].Input())
}

func TestResponsesStreamNormalizesCompletedOnlyFunctionCallFlow(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":       "test-model",
		"store":       true,
		"stream":      true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "completed only tool stream",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.Equal(t, "response.created", events[0].Event)
	require.Contains(t, eventTypes(events), "response.output_item.added")
	require.Contains(t, eventTypes(events), "response.function_call_arguments.delta")
	require.Contains(t, eventTypes(events), "response.function_call_arguments.done")
	require.Contains(t, eventTypes(events), "response.output_item.done")
	require.Contains(t, eventTypes(events), "response.completed")

	added := findEvent(t, events, "response.output_item.added").Data
	item, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "add", item["name"])
	require.NotEmpty(t, asStringAny(item["id"]))
	require.Equal(t, "", item["arguments"])
	require.Equal(t, "in_progress", item["status"])

	done := findEvent(t, events, "response.function_call_arguments.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", doneItem["type"])
	require.Equal(t, "add", doneItem["name"])
	require.Equal(t, asStringAny(item["id"]), asStringAny(doneItem["id"]))
	require.Equal(t, `{"a":1,"b":2}`, doneItem["arguments"])

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)

	got := getResponse(t, app, responseID)
	require.Equal(t, responseID, got.ID)
	require.Len(t, got.Output, 1)
	require.Equal(t, "function_call", got.Output[0].Type)
	require.Equal(t, "add", got.Output[0].Name())
}

func TestResponsesStreamRetriesToolChoiceWithAutoOnUnsupportedBackend(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":       "test-model",
		"stream":      true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "auto-only tool_choice backend. completed only tool stream",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.function_call_arguments.done")
	require.NotContains(t, eventTypes(events), "response.output_text.done")
}

func TestResponsesStreamRetryToolChoiceWithAutoRejectsAssistantText(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModePreferUpstream)

	reqBody, err := json.Marshal(map[string]any{
		"model":       "test-model",
		"stream":      true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "auto-only tool_choice backend returns text. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_choice_incompatible_backend", errorPayload["code"])
}

func TestResponsesCodexRequestsUseLocalToolLoopByDefault(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"tool_choice":  "required",
		"instructions": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Run tests and do not answer directly.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"name": "exec_command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
					"required": []string{"cmd"},
				},
			},
		},
	})

	require.NotEmpty(t, response.ID)
	require.NotEqual(t, "upstream_resp_1", response.ID)
	require.Empty(t, response.OutputText)
	require.Len(t, response.Output, 1)
	require.Equal(t, "function_call", response.Output[0].Type)
	require.Equal(t, "exec_command", response.Output[0].Name())
	require.Contains(t, response.Output[0].Arguments(), `"cmd":"cd /tmp/snake_test && go test ./game -v 2>&1"`)
}

func TestResponsesCodexToolOutputFollowUpUsesLocalToolLoop(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"tool_choice":  "required",
		"instructions": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Run tests and do not answer directly.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"name": "exec_command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
					"required": []string{"cmd"},
				},
			},
		},
	})
	require.Len(t, first.Output, 1)
	callID := first.Output[0].CallID()
	require.NotEmpty(t, callID)

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"instructions":         "You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"input": []map[string]any{
			{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  "tool says hi",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"name": "exec_command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
					"required": []string{"cmd"},
				},
			},
		},
	})

	require.NotEmpty(t, second.ID)
	require.NotEqual(t, "upstream_resp_2", second.ID)
	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "tool says hi", second.OutputText)

	got := getResponse(t, app, second.ID)
	require.Equal(t, second.ID, got.ID)
	require.Equal(t, first.ID, got.PreviousResponseID)
	require.Equal(t, "tool says hi", got.OutputText)
}

func TestResponsesStreamKeepsSafeExecCommandEscalationByDefault(t *testing.T) {
	app := testutil.NewTestApp(t)

	reqBody, err := json.Marshal(map[string]any{
		"model":        "test-model",
		"store":        true,
		"stream":       true,
		"tool_choice":  "required",
		"instructions": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "completed only tool stream",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"name": "exec_command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
					"required": []string{"cmd"},
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	done := findEvent(t, events, "response.function_call_arguments.done").Data
	item, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "exec_command", item["name"])
	require.Contains(t, asStringAny(item["arguments"]), "require_escalated")
	require.Contains(t, asStringAny(done["arguments"]), "require_escalated")
	require.Contains(t, asStringAny(item["arguments"]), `"cmd":"cd /tmp/snake_test && go test ./game -v 2>&1"`)
	require.NotContains(t, asStringAny(item["arguments"]), `"workdir":"/tmp/snake_test"`)
	require.NotContains(t, asStringAny(item["arguments"]), `"yield_time_ms":30000`)
}

func TestResponsesStreamKeepsExecCommandUntouchedWhenCodexCompatibilityEnabled(t *testing.T) {
	app := testutil.NewTestAppWithCodexSettings(t, "", true, false)

	reqBody, err := json.Marshal(map[string]any{
		"model":        "test-model",
		"store":        true,
		"stream":       true,
		"tool_choice":  "required",
		"instructions": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "completed only tool stream",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"name": "exec_command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
					"required": []string{"cmd"},
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	done := findEvent(t, events, "response.function_call_arguments.done").Data
	item, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "exec_command", item["name"])
	require.Contains(t, asStringAny(item["arguments"]), "require_escalated")
	require.Contains(t, asStringAny(done["arguments"]), "require_escalated")
	require.Contains(t, asStringAny(item["arguments"]), `"cmd":"cd /tmp/snake_test && go test ./game -v 2>&1"`)
	require.NotContains(t, asStringAny(item["arguments"]), `"workdir":"/tmp/snake_test"`)
	require.NotContains(t, asStringAny(item["arguments"]), `"yield_time_ms":30000`)
}

func TestResponsesStreamKeepsCompletedPlanLoopAndDoesNotSynthesizeSummary(t *testing.T) {
	app := testutil.NewTestAppWithCodexSettings(t, "", true, false)

	reqBody, err := json.Marshal(map[string]any{
		"model":        "test-model",
		"store":        true,
		"stream":       true,
		"tool_choice":  "required",
		"instructions": "You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "completed only tool stream completed plan reasoning stream",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"name": "update_plan",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"plan": map[string]any{"type": "array"},
					},
					"required": []string{"plan"},
				},
			},
			{
				"type": "function",
				"name": "exec_command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
					"required": []string{"cmd"},
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := readSSEEvents(t, resp.Body)
	require.NotContains(t, eventTypes(events), "response.output_text.done")
	require.Contains(t, eventTypes(events), "response.function_call_arguments.done")

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)
	require.Empty(t, asStringAny(responsePayload["output_text"]))

	got := getResponse(t, app, responseID)
	require.Empty(t, got.OutputText)
	require.Len(t, got.Output, 2)
	require.Equal(t, "reasoning", got.Output[0].Type)
	require.Equal(t, "function_call", got.Output[1].Type)
}

func TestResponsesCustomToolFollowUpWithPreviousResponseID(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model":       "test-model",
		"store":       true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Use the code_exec tool.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
			},
		},
	})
	require.Len(t, first.Output, 1)
	require.NotEqual(t, "upstream_resp_1", first.ID)
	require.Equal(t, "custom_tool_call", first.Output[0].Type)
	require.Equal(t, "code_exec", first.Output[0].Name())
	require.Equal(t, `print("hello world")`, first.Output[0].Input())
	callID := first.Output[0].CallID()
	require.NotEmpty(t, callID)

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input": []map[string]any{
			{
				"type":    "custom_tool_call_output",
				"call_id": callID,
				"output": []map[string]any{
					{"type": "input_text", "text": "tool says hi"},
				},
			},
		},
	})

	require.NotEqual(t, "upstream_resp_2", second.ID)
	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "tool says hi", second.OutputText)

	inputItems := getResponseInputItems(t, app, second.ID)
	require.Len(t, inputItems.Data, 3)
	require.Equal(t, "custom_tool_call_output", asStringAny(inputItems.Data[0]["type"]))
	outputParts, ok := inputItems.Data[0]["output"].([]any)
	require.True(t, ok)
	require.Len(t, outputParts, 1)
	firstPart, ok := outputParts[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool says hi", firstPart["text"])
}

func TestResponsesGrammarCustomToolFollowUpWithPreviousResponseID(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	first := postResponse(t, app, map[string]any{
		"model":       "test-model",
		"store":       true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Use grammar tool",
			},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "math_exp",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
				},
			},
		},
	})
	require.Len(t, first.Output, 1)
	require.NotEqual(t, "upstream_resp_1", first.ID)
	require.Equal(t, "custom_tool_call", first.Output[0].Type)
	require.Equal(t, "math_exp", first.Output[0].Name())
	require.Equal(t, "4 + 4", first.Output[0].Input())
	callID := first.Output[0].CallID()
	require.NotEmpty(t, callID)

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input": []map[string]any{
			{
				"type":    "custom_tool_call_output",
				"call_id": callID,
				"output": []map[string]any{
					{"type": "input_text", "text": "grammar tool says hi"},
				},
			},
		},
	})

	require.NotEqual(t, "upstream_resp_2", second.ID)
	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "grammar tool says hi", second.OutputText)
}

func TestResponsesRetryStructuredInputAsStringForLocalStateRequests(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Reply OK",
	})
	require.NotEmpty(t, first.ID)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":                "test-model",
		"previous_response_id": first.ID,
		"tool_choice":          "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "backend rejects structured input arrays. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})

	require.Equal(t, http.StatusOK, status)
	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "add", item["name"])
}

func TestResponsesPreviousResponseIDWithToolsFallsBackToDirectProxyWhenReplayInputRejected(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model":       "test-model",
		"store":       true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "backend rejects replayed typed input. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.Len(t, first.Output, 1)
	require.Equal(t, "function_call", first.Output[0].Type)
	callID := first.Output[0].CallID()
	require.NotEmpty(t, callID)

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input": []map[string]any{
			{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  "tool says hi",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})

	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "tool says hi", second.OutputText)

	got := getResponse(t, app, second.ID)
	require.Equal(t, second.ID, got.ID)
	require.Equal(t, first.ID, got.PreviousResponseID)
	require.Equal(t, "tool says hi", got.OutputText)

	inputItems := getResponseInputItems(t, app, second.ID)
	require.Len(t, inputItems.Data, 3)
	require.Equal(t, "function_call_output", asStringAny(inputItems.Data[0]["type"]))
	require.Equal(t, "tool says hi", asStringAny(inputItems.Data[0]["output"]))
}

func TestResponsesStreamPreviousResponseIDWithToolsFallsBackToDirectProxyWhenReplayInputRejected(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postResponse(t, app, map[string]any{
		"model":       "test-model",
		"store":       true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "backend rejects replayed typed input. Call add.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.Len(t, first.Output, 1)
	require.Equal(t, "function_call", first.Output[0].Type)
	callID := first.Output[0].CallID()
	require.NotEmpty(t, callID)

	reqBody, err := json.Marshal(map[string]any{
		"model":                "test-model",
		"metadata":             map[string]any{"case": "force-upstream-replay-fallback"},
		"store":                true,
		"stream":               true,
		"previous_response_id": first.ID,
		"input": []map[string]any{
			{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  "tool says hi",
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
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
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.output_text.delta")
	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)
	require.Equal(t, "tool says hi", asStringAny(responsePayload["output_text"]))

	got := getResponse(t, app, responseID)
	require.Equal(t, responseID, got.ID)
	require.Equal(t, first.ID, got.PreviousResponseID)
	require.Equal(t, "tool says hi", got.OutputText)

	inputItems := getResponseInputItems(t, app, responseID)
	require.Len(t, inputItems.Data, 3)
	require.Equal(t, "function_call_output", asStringAny(inputItems.Data[0]["type"]))
	require.Equal(t, "tool says hi", asStringAny(inputItems.Data[0]["output"]))
}

func TestResponsesNamespacedCustomToolsAreBridged(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":       "test-model",
		"tool_choice": "required",
		"input": []map[string]any{
			{"role": "user", "content": "Use shell.exec."},
		},
		"tools": []map[string]any{
			{
				"type":      "custom",
				"namespace": "shell",
				"name":      "exec",
				"format": map[string]any{
					"type": "text",
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, "upstream_resp_1", body["id"])

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "shell", item["namespace"])
	require.Equal(t, "exec", item["name"])
}

func TestResponsesPreferUpstreamBridgeRejectsGrammarCustomTools(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode:   config.ResponsesModePreferUpstream,
		CustomToolsMode: "bridge",
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: /.+/",
				},
			},
		},
	})
	require.Equal(t, http.StatusBadRequest, status)
	errorPayload := payload["error"].(map[string]any)
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Equal(t, "tools", errorPayload["param"])
	require.Contains(t, asStringAny(errorPayload["message"]), "custom tool format is not supported in bridge mode")
}

func TestResponsesPreferUpstreamAutoPassthroughsGrammarCustomTools(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode:   config.ResponsesModePreferUpstream,
		CustomToolsMode: "auto",
	})

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use grammar tool",
		"tools": []map[string]any{
			{
				"type":      "custom",
				"namespace": "shell",
				"name":      "exec",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: /.+/",
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "upstream_resp_1", body["id"])

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "shell", item["namespace"])
}

func TestResponsesPreferLocalHandlesGrammarCustomToolsWithoutUpstreamResponsesSupport(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Backend rejects native custom tools. Use grammar tool",
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: /.+/",
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	output, ok := payload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, `print("hello world")`, item["input"])
}

func TestResponsesStreamPreferLocalHandlesGrammarCustomToolsWithoutUpstreamResponsesSupport(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"input": []map[string]any{
			{"role": "user", "content": "Backend rejects native custom tools. Use grammar tool"},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: /.+/",
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := readSSEEvents(t, resp.Body)
	done := findEvent(t, events, "response.custom_tool_call_input.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", doneItem["type"])
	require.Equal(t, `print("hello world")`, doneItem["input"])
}

func TestResponsesPreferLocalHandlesGrammarCustomToolsAfterStructuredInputRetryMarkers(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": []map[string]any{
			{"role": "user", "content": "Backend rejects structured input arrays. Backend rejects native custom tools. Use grammar tool"},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: /.+/",
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	output, ok := payload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, `print("hello world")`, item["input"])
}

func TestResponsesStreamPreferLocalHandlesGrammarCustomToolsAfterStructuredInputRetryMarkers(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

	reqBody, err := json.Marshal(map[string]any{
		"model":  "test-model",
		"stream": true,
		"input": []map[string]any{
			{"role": "user", "content": "Backend rejects structured input arrays. Backend rejects native custom tools. Use grammar tool"},
		},
		"tools": []map[string]any{
			{
				"type": "custom",
				"name": "code_exec",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: /.+/",
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := readSSEEvents(t, resp.Body)
	done := findEvent(t, events, "response.custom_tool_call_input.done").Data
	doneItem, ok := done["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", doneItem["type"])
	require.Equal(t, `print("hello world")`, doneItem["input"])
}

func TestConversationsPreservePhaseAndMixedItems(t *testing.T) {
	app := testutil.NewTestApp(t)

	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{
				"type":  "message",
				"role":  "assistant",
				"phase": "commentary",
				"content": []map[string]any{
					{"type": "output_text", "text": "thinking"},
				},
			},
			{
				"type":      "custom_tool_call",
				"id":        "ctc_manual",
				"call_id":   "call_manual",
				"namespace": "shell",
				"name":      "exec",
				"input":     "echo hi",
				"status":    "completed",
			},
			{
				"type":    "custom_tool_call_output",
				"id":      "cto_manual",
				"call_id": "call_manual",
				"output": []map[string]any{
					{"type": "input_text", "text": "hi"},
				},
			},
		},
	})

	items := getConversationItems(t, app, conversation.ID, "?order=asc")
	require.Len(t, items.Data, 3)
	require.Equal(t, "commentary", asStringAny(items.Data[0]["phase"]))
	require.Equal(t, "custom_tool_call", asStringAny(items.Data[1]["type"]))
	require.Equal(t, "shell", asStringAny(items.Data[1]["namespace"]))
	require.Equal(t, "custom_tool_call_output", asStringAny(items.Data[2]["type"]))
}

func TestChatCompletionsRejectInvalidMessagesShape(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "test-model",
		"messages": 1,
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "messages", payload["error"].(map[string]any)["param"])
}

func TestChatCompletionsStoreTrueExposesStoredReadSurface(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-5.4",
		"store":    true,
		"metadata": map[string]any{"topic": "demo"},
		"messages": []map[string]any{
			{"role": "developer", "content": "You are terse."},
			{"role": "user", "content": "Say OK and nothing else"},
		},
	})
	require.Equal(t, http.StatusOK, status)
	completionID := asStringAny(body["id"])
	require.NotEmpty(t, completionID)
	require.Equal(t, "chat.completion", asStringAny(body["object"]))

	list := getStoredChatCompletions(t, app, "")
	require.Equal(t, "list", list.Object)
	require.Len(t, list.Data, 1)
	require.Equal(t, completionID, asStringAny(list.Data[0]["id"]))
	require.NotNil(t, list.FirstID)
	require.NotNil(t, list.LastID)
	require.Equal(t, completionID, *list.FirstID)
	require.Equal(t, completionID, *list.LastID)
	require.False(t, list.HasMore)

	stored := getStoredChatCompletion(t, app, completionID)
	require.Equal(t, completionID, asStringAny(stored["id"]))
	require.Equal(t, "gpt-5.4", asStringAny(stored["model"]))
	metadata, ok := stored["metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "demo", asStringAny(metadata["topic"]))

	messages := getStoredChatCompletionMessages(t, app, completionID, "")
	require.Equal(t, "list", messages.Object)
	require.Len(t, messages.Data, 2)
	require.Equal(t, []string{completionID + "-0", completionID + "-1"}, []string{
		asStringAny(messages.Data[0]["id"]),
		asStringAny(messages.Data[1]["id"]),
	})
	require.Equal(t, []string{"developer", "user"}, []string{
		asStringAny(messages.Data[0]["role"]),
		asStringAny(messages.Data[1]["role"]),
	})
	require.Equal(t, []string{"You are terse.", "Say OK and nothing else"}, []string{
		asStringAny(messages.Data[0]["content"]),
		asStringAny(messages.Data[1]["content"]),
	})
	_, hasName := messages.Data[0]["name"]
	require.True(t, hasName)
	require.Nil(t, messages.Data[0]["name"])
	_, hasParts := messages.Data[0]["content_parts"]
	require.True(t, hasParts)
	require.Nil(t, messages.Data[0]["content_parts"])
}

func TestChatCompletionsWithoutExplicitStoreDoNotShadowStore(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "user", "content": "Say OK and nothing else"},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, asStringAny(body["id"]))

	list := getStoredChatCompletions(t, app, "")
	require.Empty(t, list.Data)
	require.Nil(t, list.FirstID)
	require.Nil(t, list.LastID)
	require.False(t, list.HasMore)
}

func TestChatCompletionsStoredListFiltersAndPaginates(t *testing.T) {
	app := testutil.NewTestApp(t)

	first := postStoredChatCompletion(t, app, map[string]any{
		"model":    "gpt-5.4",
		"store":    true,
		"metadata": map[string]any{"topic": "alpha"},
		"messages": []map[string]any{{"role": "user", "content": "Say OK and nothing else"}},
	})
	_ = postStoredChatCompletion(t, app, map[string]any{
		"model":    "gpt-4o-mini",
		"store":    true,
		"metadata": map[string]any{"topic": "beta"},
		"messages": []map[string]any{{"role": "user", "content": "Say OK and nothing else"}},
	})
	third := postStoredChatCompletion(t, app, map[string]any{
		"model":    "gpt-5.4",
		"store":    true,
		"metadata": map[string]any{"topic": "alpha"},
		"messages": []map[string]any{{"role": "user", "content": "Say OK and nothing else"}},
	})

	page1 := getStoredChatCompletions(t, app, "?model=gpt-5.4&metadata[topic]=alpha&limit=1&order=asc")
	require.Len(t, page1.Data, 1)
	require.True(t, page1.HasMore)
	require.Equal(t, first, asStringAny(page1.Data[0]["id"]))

	page2 := getStoredChatCompletions(t, app, "?model=gpt-5.4&metadata[topic]=alpha&limit=1&order=asc&after="+first)
	require.Len(t, page2.Data, 1)
	require.False(t, page2.HasMore)
	require.Equal(t, third, asStringAny(page2.Data[0]["id"]))
}

func TestFilesEndpointsUploadListRetrieveContentAndDelete(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "notes.txt", "assistants", []byte("alpha beta gamma"), map[string]string{
		"expires_after[anchor]":  "created_at",
		"expires_after[seconds]": "3600",
	})
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])
	require.NotEmpty(t, fileID)
	require.Equal(t, "file", asStringAny(uploaded["object"]))
	require.Equal(t, "assistants", asStringAny(uploaded["purpose"]))
	require.Equal(t, "notes.txt", asStringAny(uploaded["filename"]))
	require.Equal(t, "processed", asStringAny(uploaded["status"]))
	require.NotNil(t, uploaded["expires_at"])

	status, page := rawRequest(t, app, http.MethodGet, "/v1/files?purpose=assistants&limit=10&order=asc", nil)
	require.Equal(t, http.StatusOK, status)
	data := page["data"].([]any)
	require.Len(t, data, 1)
	require.Equal(t, fileID, asStringAny(data[0].(map[string]any)["id"]))
	require.Equal(t, fileID, asStringAny(page["first_id"]))
	require.Equal(t, fileID, asStringAny(page["last_id"]))
	require.Equal(t, false, page["has_more"])

	status, stored := rawRequest(t, app, http.MethodGet, "/v1/files/"+fileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fileID, asStringAny(stored["id"]))
	require.Equal(t, "notes.txt", asStringAny(stored["filename"]))

	content := getFileContent(t, app, fileID)
	require.Equal(t, []byte("alpha beta gamma"), content)

	status, deleted := rawRequest(t, app, http.MethodDelete, "/v1/files/"+fileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fileID, asStringAny(deleted["id"]))
	require.Equal(t, "file", asStringAny(deleted["object"]))
	require.Equal(t, true, deleted["deleted"])

	status, missing := rawRequest(t, app, http.MethodGet, "/v1/files/"+fileID, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", missing["error"].(map[string]any)["type"])
}

func TestVectorStoresEndpointsCreateAttachSearchAndDelete(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "faq.txt", "assistants", []byte("The support answer says you can search local docs."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "FAQ",
		"file_ids": []string{fileID},
		"metadata": map[string]any{"topic": "docs"},
		"expires_after": map[string]any{
			"anchor": "last_active_at",
			"days":   7,
		},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])
	require.NotEmpty(t, vectorStoreID)
	require.Equal(t, "vector_store", asStringAny(created["object"]))
	require.Equal(t, "FAQ", asStringAny(created["name"]))
	require.Equal(t, "completed", asStringAny(created["status"]))
	require.Equal(t, "docs", asStringAny(created["metadata"].(map[string]any)["topic"]))
	require.Equal(t, float64(1), created["file_counts"].(map[string]any)["completed"])

	status, page := rawRequest(t, app, http.MethodGet, "/v1/vector_stores?limit=10&order=asc", nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, page["data"].([]any), 1)
	require.Equal(t, vectorStoreID, asStringAny(page["first_id"]))
	require.Equal(t, vectorStoreID, asStringAny(page["last_id"]))

	status, storeFiles := rawRequest(t, app, http.MethodGet, "/v1/vector_stores/"+vectorStoreID+"/files?filter=completed&limit=10", nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, storeFiles["data"].([]any), 1)
	require.Equal(t, fileID, asStringAny(storeFiles["data"].([]any)[0].(map[string]any)["id"]))

	status, attached := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/files", map[string]any{
		"file_id": fileID,
		"attributes": map[string]any{
			"tenant": "alpha",
			"topic":  "docs",
		},
		"chunking_strategy": map[string]any{
			"type": "static",
			"static": map[string]any{
				"max_chunk_size_tokens": 100,
				"chunk_overlap_tokens":  0,
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "vector_store.file", asStringAny(attached["object"]))
	require.Equal(t, "completed", asStringAny(attached["status"]))
	require.Equal(t, "alpha", asStringAny(attached["attributes"].(map[string]any)["tenant"]))

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query":           "support answer",
		"max_num_results": 10,
		"filters": map[string]any{
			"type":  "eq",
			"key":   "tenant",
			"value": "alpha",
		},
		"ranking_options": map[string]any{
			"score_threshold": 0.1,
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "vector_store.search_results.page", asStringAny(search["object"]))
	require.Equal(t, "support answer", asStringAny(search["search_query"]))
	require.Len(t, search["data"].([]any), 1)
	result := search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileID, asStringAny(result["file_id"]))
	require.Equal(t, "faq.txt", asStringAny(result["filename"]))
	require.Contains(t, asStringAny(result["content"].([]any)[0].(map[string]any)["text"]), "support answer")

	status, deletedFile := rawRequest(t, app, http.MethodDelete, "/v1/vector_stores/"+vectorStoreID+"/files/"+fileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "vector_store.file.deleted", asStringAny(deletedFile["object"]))
	require.Equal(t, true, deletedFile["deleted"])

	status, deletedStore := rawRequest(t, app, http.MethodDelete, "/v1/vector_stores/"+vectorStoreID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "vector_store.deleted", asStringAny(deletedStore["object"]))
	require.Equal(t, true, deletedStore["deleted"])

	status, missing := rawRequest(t, app, http.MethodGet, "/v1/vector_stores/"+vectorStoreID, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", missing["error"].(map[string]any)["type"])
}

func TestVectorStoreAttachBinaryFileReturnsFailedStatus(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "binary.bin", "assistants", []byte{0xff, 0xfe, 0xfd}, nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name": "Binary",
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, attached := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/files", map[string]any{
		"file_id": fileID,
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "failed", asStringAny(attached["status"]))
	require.Equal(t, "unsupported_file", asStringAny(attached["last_error"].(map[string]any)["code"]))

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "anything",
	})
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, search["data"].([]any))
}

func TestVectorStoreSearchRewriteQueryReturnsRewrittenSearchQuery(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Codes",
		"file_ids": []string{fileID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query":         "What is the code?",
		"rewrite_query": true,
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "code", asStringAny(search["search_query"]))
	require.NotEmpty(t, search["data"].([]any))
	require.Equal(t, fileID, asStringAny(search["data"].([]any)[0].(map[string]any)["file_id"]))
}

func TestVectorStoreSearchUsesSQLiteVecSemanticBackend(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		RetrievalEmbedder: semanticTestEmbedder{},
	})

	status, uploadedBanana := uploadFile(t, app, "banana.txt", "assistants", []byte("Banana smoothie recipe and ripe banana notes."), nil)
	require.Equal(t, http.StatusOK, status)
	fileBananaID := asStringAny(uploadedBanana["id"])

	status, uploadedOcean := uploadFile(t, app, "ocean.txt", "assistants", []byte("Ocean tides and marine currents reference."), nil)
	require.Equal(t, http.StatusOK, status)
	fileOceanID := asStringAny(uploadedOcean["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Semantic",
		"file_ids": []string{fileBananaID, fileOceanID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query":           "banana nutrition",
		"max_num_results": 5,
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "vector_store.search_results.page", asStringAny(search["object"]))
	require.NotEmpty(t, search["data"].([]any))
	result := search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileBananaID, asStringAny(result["file_id"]))
	require.Equal(t, "banana.txt", asStringAny(result["filename"]))
	require.Greater(t, result["score"].(float64), 0.8)
}

func TestVectorStoreSearchSQLiteVecReindexesOnEmbedderModelChange(t *testing.T) {
	dbPath := testutil.TempDBPath(t)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		DBPath: dbPath,
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
			Embedder: retrieval.EmbedderConfig{
				Model: "embed-v1",
			},
		},
		RetrievalEmbedder: semanticV1Embedder{},
	})

	status, uploadedBanana := uploadFile(t, app, "banana.txt", "assistants", []byte("Banana smoothie recipe and ripe banana notes."), nil)
	require.Equal(t, http.StatusOK, status)
	fileBananaID := asStringAny(uploadedBanana["id"])

	status, uploadedOcean := uploadFile(t, app, "ocean.txt", "assistants", []byte("Ocean tides and marine currents reference."), nil)
	require.Equal(t, http.StatusOK, status)
	fileOceanID := asStringAny(uploadedOcean["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Semantic",
		"file_ids": []string{fileBananaID, fileOceanID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])
	app.Close()

	app = testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		DBPath: dbPath,
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
			Embedder: retrieval.EmbedderConfig{
				Model: "embed-v2",
			},
		},
		RetrievalEmbedder: semanticV2Embedder{},
	})

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query":           "banana nutrition",
		"max_num_results": 5,
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, search["data"].([]any))
	result := search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileBananaID, asStringAny(result["file_id"]))
}

func TestVectorStoreSearchSupportsHybridRankingOptions(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		RetrievalEmbedder: hybridRankingTestEmbedder{},
	})

	status, uploadedSemantic := uploadFile(t, app, "semantic.txt", "assistants", []byte("semanticwinner banana orchard notes"), nil)
	require.Equal(t, http.StatusOK, status)
	fileSemanticID := asStringAny(uploadedSemantic["id"])

	status, uploadedLexical := uploadFile(t, app, "lexical.txt", "assistants", []byte("banana nutrition facts nutrition calories"), nil)
	require.Equal(t, http.StatusOK, status)
	fileLexicalID := asStringAny(uploadedLexical["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Hybrid",
		"file_ids": []string{fileSemanticID, fileLexicalID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "banana nutrition",
		"ranking_options": map[string]any{
			"ranker": "none",
			"hybrid_search": map[string]any{
				"embedding_weight": 10,
				"text_weight":      1,
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, search["data"].([]any))
	result := search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileSemanticID, asStringAny(result["file_id"]))

	status, search = rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "banana nutrition",
		"ranking_options": map[string]any{
			"ranker": "none",
			"hybrid_search": map[string]any{
				"embedding_weight": 1,
				"text_weight":      10,
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, search["data"].([]any))
	result = search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileLexicalID, asStringAny(result["file_id"]))
}

func TestVectorStoreSearchRejectsHybridRankingWithoutPositiveWeights(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name": "HybridValidation",
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "banana nutrition",
		"ranking_options": map[string]any{
			"hybrid_search": map[string]any{
				"embedding_weight": 0,
				"text_weight":      0,
			},
		},
	})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Contains(t, asStringAny(payload["error"].(map[string]any)["message"]), "must be greater than zero")
}

func TestVectorStoreSearchAppliesLocalRerankingByDefault(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		RetrievalEmbedder: rerankingTestEmbedder{},
	})

	status, uploadedSemantic := uploadFile(t, app, "semantic.txt", "assistants", []byte("semanticwinner banana orchard notes"), nil)
	require.Equal(t, http.StatusOK, status)
	fileSemanticID := asStringAny(uploadedSemantic["id"])

	status, uploadedReranked := uploadFile(t, app, "banana-nutrition.txt", "assistants", []byte("banana nutrition exact phrase and calories"), nil)
	require.Equal(t, http.StatusOK, status)
	fileRerankedID := asStringAny(uploadedReranked["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Rerank",
		"file_ids": []string{fileSemanticID, fileRerankedID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "banana nutrition",
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, search["data"].([]any))
	result := search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileRerankedID, asStringAny(result["file_id"]))

	status, search = rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "banana nutrition",
		"ranking_options": map[string]any{
			"ranker": "none",
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, search["data"].([]any))
	result = search["data"].([]any)[0].(map[string]any)
	require.Equal(t, fileSemanticID, asStringAny(result["file_id"]))
}

func TestResponsesCreateExecutesLocalFileSearch(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Codes",
		"file_ids": []string{fileID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "What is the code?",
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	require.Len(t, response.Output, 2)
	require.Equal(t, "file_search_call", response.Output[0].Type)
	require.Equal(t, "completed", response.Output[0].Status())
	require.Equal(t, "message", response.Output[1].Type)

	fileSearchPayload := response.Output[0].Map()
	require.Equal(t, []any{"code"}, fileSearchPayload["queries"].([]any))
	require.Nil(t, fileSearchPayload["results"])

	messagePayload := response.Output[1].Map()
	content, ok := messagePayload["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	textPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "777", asStringAny(textPart["text"]))
	annotations, ok := textPart["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, annotations, 1)
	annotation, ok := annotations[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "file_citation", asStringAny(annotation["type"]))
	require.Equal(t, fileID, asStringAny(annotation["file_id"]))
	require.Equal(t, "codes.txt", asStringAny(annotation["filename"]))
	require.EqualValues(t, utf8.RuneCountInString("777"), annotation["index"])

	got := getResponse(t, app, response.ID)
	require.Equal(t, "777", got.OutputText)
	require.Len(t, got.Output, 2)
	require.Equal(t, "file_search_call", got.Output[0].Type)
	require.Equal(t, "message", got.Output[1].Type)
	gotContent, ok := got.Output[1].Map()["content"].([]any)
	require.True(t, ok)
	require.Len(t, gotContent, 1)
	gotAnnotations, ok := gotContent[0].(map[string]any)["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, gotAnnotations, 1)
	gotAnnotation, ok := gotAnnotations[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "file_citation", asStringAny(gotAnnotation["type"]))
	require.Equal(t, fileID, asStringAny(gotAnnotation["file_id"]))
}

func TestResponsesCreateLocalFileSearchUsesMultipleChunksFromSameFile(t *testing.T) {
	app := testutil.NewTestApp(t)

	firstChunk := strings.TrimSpace(strings.Repeat("code decoy placeholder ", 33))
	secondChunk := strings.TrimSpace(strings.Repeat("actual code 777 ", 33))
	fileContent := []byte(firstChunk + " " + secondChunk)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", fileContent, nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name": "Codes",
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, attached := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/files", map[string]any{
		"file_id": fileID,
		"chunking_strategy": map[string]any{
			"type": "static",
			"static": map[string]any{
				"max_chunk_size_tokens": 100,
				"chunk_overlap_tokens":  0,
			},
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "completed", asStringAny(attached["status"]))

	status, search := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/search", map[string]any{
		"query": "code",
	})
	require.Equal(t, http.StatusOK, status)
	require.Len(t, search["data"].([]any), 1)
	searchResult := search["data"].([]any)[0].(map[string]any)
	content, ok := searchResult["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	require.Contains(t, asStringAny(content[0].(map[string]any)["text"]), "code decoy placeholder")
	require.Contains(t, asStringAny(content[1].(map[string]any)["text"]), "actual code 777")

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"include": []string{"file_search_call.results"},
		"input":   "What is the code?",
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	fileSearchPayload := response.Output[0].Map()
	require.Equal(t, []any{"code"}, fileSearchPayload["queries"].([]any))
	results, ok := fileSearchPayload["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	resultContent, ok := results[0].(map[string]any)["content"].([]any)
	require.True(t, ok)
	require.Len(t, resultContent, 2)
	require.Contains(t, asStringAny(resultContent[0].(map[string]any)["text"]), "code decoy placeholder")
	require.Contains(t, asStringAny(resultContent[1].(map[string]any)["text"]), "actual code 777")
}

func TestResponsesCreateLocalFileSearchPlansMultipleQueries(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploadedBanana := uploadFile(t, app, "banana.txt", "assistants", []byte("banana nutrition reference"), nil)
	require.Equal(t, http.StatusOK, status)
	fileBananaID := asStringAny(uploadedBanana["id"])

	status, uploadedApple := uploadFile(t, app, "apple.txt", "assistants", []byte("apple storage guide"), nil)
	require.Equal(t, http.StatusOK, status)
	fileAppleID := asStringAny(uploadedApple["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Compare",
		"file_ids": []string{fileBananaID, fileAppleID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "Compare banana nutrition and apple storage.",
		"include": []string{"file_search_call.results"},
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
	})

	fileSearchPayload := response.Output[0].Map()
	require.Equal(t, []any{
		"banana nutrition apple storage",
		"banana nutrition",
		"apple storage",
	}, fileSearchPayload["queries"].([]any))

	results := fileSearchPayload["results"].([]any)
	require.Len(t, results, 2)
	gotFileIDs := []string{
		asStringAny(results[0].(map[string]any)["file_id"]),
		asStringAny(results[1].(map[string]any)["file_id"]),
	}
	require.ElementsMatch(t, []string{fileBananaID, fileAppleID}, gotFileIDs)
}

func TestResponsesCreateLocalFileSearchStreamReplaysToolEvents(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Codes",
		"file_ids": []string{fileID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(mustJSON(t, map[string]any{
		"model":  "test-model",
		"store":  true,
		"stream": true,
		"input":  "What is the code?",
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
	})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.file_search_call.in_progress")
	require.Contains(t, eventTypes(events), "response.file_search_call.searching")
	require.Contains(t, eventTypes(events), "response.file_search_call.completed")
	require.Contains(t, eventTypes(events), "response.output_text.delta")
	require.Contains(t, eventTypes(events), "response.output_text.annotation.added")

	added := findEvents(events, "response.output_item.added")
	require.Len(t, added, 2)
	require.Equal(t, "file_search_call", asStringAny(added[0].Data["item"].(map[string]any)["type"]))
	require.Equal(t, "message", asStringAny(added[1].Data["item"].(map[string]any)["type"]))

	annotationEvents := findEvents(events, "response.output_text.annotation.added")
	require.Len(t, annotationEvents, 1)
	streamAnnotation, ok := annotationEvents[0].Data["annotation"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "file_citation", asStringAny(streamAnnotation["type"]))
	require.Equal(t, fileID, asStringAny(streamAnnotation["file_id"]))
	require.Equal(t, "codes.txt", asStringAny(streamAnnotation["filename"]))
	require.EqualValues(t, utf8.RuneCountInString("777"), streamAnnotation["index"])

	outputDoneEvents := findEvents(events, "response.output_item.done")
	require.Len(t, outputDoneEvents, 2)
	doneItem, ok := outputDoneEvents[1].Data["item"].(map[string]any)
	require.True(t, ok)
	doneContent, ok := doneItem["content"].([]any)
	require.True(t, ok)
	require.Len(t, doneContent, 1)
	doneAnnotations, ok := doneContent[0].(map[string]any)["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, doneAnnotations, 1)

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "777", asStringAny(responsePayload["output_text"]))
}

func TestResponsesCreateLocalFileSearchIncludeResults(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name": "Codes",
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	status, attached := rawRequest(t, app, http.MethodPost, "/v1/vector_stores/"+vectorStoreID+"/files", map[string]any{
		"file_id": fileID,
		"attributes": map[string]any{
			"tenant": "alpha",
		},
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "completed", asStringAny(attached["status"]))

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "What is the code?",
		"include": []string{"file_search_call.results"},
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
				"filters": map[string]any{
					"type":  "eq",
					"key":   "tenant",
					"value": "alpha",
				},
			},
		},
	})

	require.Equal(t, "777", response.OutputText)
	fileSearchPayload := response.Output[0].Map()
	results, ok := fileSearchPayload["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)

	result, ok := results[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, fileID, asStringAny(result["file_id"]))
	require.Equal(t, "codes.txt", asStringAny(result["filename"]))
	require.Equal(t, vectorStoreID, asStringAny(result["vector_store_id"]))
	content, ok := result["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, content)
	require.Contains(t, asStringAny(content[0].(map[string]any)["text"]), "code")
	require.Contains(t, asStringAny(content[0].(map[string]any)["text"]), "777")
	require.Equal(t, "alpha", asStringAny(result["attributes"].(map[string]any)["tenant"]))
}

func TestResponsesCreateLocalFileSearchSupportsHybridRankingOptions(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		RetrievalEmbedder: hybridRankingTestEmbedder{},
	})

	status, uploadedSemantic := uploadFile(t, app, "semantic.txt", "assistants", []byte("semanticwinner banana orchard notes"), nil)
	require.Equal(t, http.StatusOK, status)
	fileSemanticID := asStringAny(uploadedSemantic["id"])

	status, uploadedLexical := uploadFile(t, app, "lexical.txt", "assistants", []byte("banana nutrition facts nutrition calories"), nil)
	require.Equal(t, http.StatusOK, status)
	fileLexicalID := asStringAny(uploadedLexical["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Hybrid",
		"file_ids": []string{fileSemanticID, fileLexicalID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "banana nutrition",
		"include": []string{"file_search_call.results"},
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
				"ranking_options": map[string]any{
					"ranker": "none",
					"hybrid_search": map[string]any{
						"embedding_weight": 1,
						"text_weight":      10,
					},
				},
			},
		},
	})

	require.NotEmpty(t, response.Output)
	fileSearchPayload := response.Output[0].Map()
	results, ok := fileSearchPayload["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results)
	require.Equal(t, fileLexicalID, asStringAny(results[0].(map[string]any)["file_id"]))
	require.Equal(t, "lexical.txt", asStringAny(results[0].(map[string]any)["filename"]))
}

func TestResponsesCreateLocalFileSearchAppliesLocalRerankingByDefault(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		RetrievalConfig: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		RetrievalEmbedder: rerankingTestEmbedder{},
	})

	status, uploadedSemantic := uploadFile(t, app, "semantic.txt", "assistants", []byte("semanticwinner banana orchard notes"), nil)
	require.Equal(t, http.StatusOK, status)
	fileSemanticID := asStringAny(uploadedSemantic["id"])

	status, uploadedReranked := uploadFile(t, app, "banana-nutrition.txt", "assistants", []byte("banana nutrition exact phrase and calories"), nil)
	require.Equal(t, http.StatusOK, status)
	fileRerankedID := asStringAny(uploadedReranked["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Rerank",
		"file_ids": []string{fileSemanticID, fileRerankedID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "banana nutrition",
		"include": []string{"file_search_call.results"},
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
	})

	results := response.Output[0].Map()["results"].([]any)
	require.NotEmpty(t, results)
	require.Equal(t, fileRerankedID, asStringAny(results[0].(map[string]any)["file_id"]))

	response = postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "banana nutrition",
		"include": []string{"file_search_call.results"},
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
				"ranking_options": map[string]any{
					"ranker": "none",
				},
			},
		},
	})

	results = response.Output[0].Map()["results"].([]any)
	require.NotEmpty(t, results)
	require.Equal(t, fileSemanticID, asStringAny(results[0].(map[string]any)["file_id"]))
}

func TestResponsesCreateLocalFileSearchWorksInLocalOnlyMode(t *testing.T) {
	app := testutil.NewTestAppWithResponsesMode(t, config.ResponsesModeLocalOnly)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Codes",
		"file_ids": []string{fileID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"input": "What is the code?",
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
	})

	require.Equal(t, "777", response.OutputText)
	require.Equal(t, "file_search_call", response.Output[0].Type)
}

func TestResponsesCreatePlainFollowUpAfterLocalFileSearchStoredOutput(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	status, created := rawRequest(t, app, http.MethodPost, "/v1/vector_stores", map[string]any{
		"name":     "Codes",
		"file_ids": []string{fileID},
	})
	require.Equal(t, http.StatusOK, status)
	vectorStoreID := asStringAny(created["id"])

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "What is the code?",
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
	})
	require.Equal(t, "777", first.OutputText)

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"previous_response_id": first.ID,
		"input":                "Say OK and nothing else",
	})

	require.Equal(t, "OK", second.OutputText)
	require.Len(t, second.Output, 1)
	require.Equal(t, "message", second.Output[0].Type)
}

func TestContainersCreateListGetDelete(t *testing.T) {
	var (
		mu        sync.Mutex
		created   = map[string]string{}
		destroyed = map[string]bool{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				created[req.SessionID] = req.MemoryLimit
				return nil
			},
			DestroySessionFunc: func(_ context.Context, sessionID string) error {
				mu.Lock()
				defer mu.Unlock()
				destroyed[sessionID] = true
				return nil
			},
		},
	})

	status, createdPayload := rawRequest(t, app, http.MethodPost, "/v1/containers", map[string]any{
		"name":         "My Container",
		"memory_limit": "4g",
		"expires_after": map[string]any{
			"anchor":  "last_active_at",
			"minutes": 45,
		},
	})
	require.Equal(t, http.StatusOK, status)
	containerID := asStringAny(createdPayload["id"])
	require.NotEmpty(t, containerID)
	require.Equal(t, "container", asStringAny(createdPayload["object"]))
	require.Equal(t, "running", asStringAny(createdPayload["status"]))
	require.Equal(t, "4g", asStringAny(createdPayload["memory_limit"]))
	require.Equal(t, "My Container", asStringAny(createdPayload["name"]))
	expiresAfter, ok := createdPayload["expires_after"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "last_active_at", asStringAny(expiresAfter["anchor"]))
	require.EqualValues(t, 45, expiresAfter["minutes"])

	mu.Lock()
	require.Equal(t, "4g", created[containerID])
	mu.Unlock()

	status, listPayload := rawRequest(t, app, http.MethodGet, "/v1/containers?limit=10&order=asc&name=My%20Container", nil)
	require.Equal(t, http.StatusOK, status)
	data, ok := listPayload["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	require.Equal(t, containerID, asStringAny(data[0].(map[string]any)["id"]))

	status, getPayload := rawRequest(t, app, http.MethodGet, "/v1/containers/"+containerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, containerID, asStringAny(getPayload["id"]))
	require.Equal(t, "My Container", asStringAny(getPayload["name"]))

	status, createdFile := uploadContainerFile(t, app, containerID, "cleanup.txt", []byte("delete me"))
	require.Equal(t, http.StatusOK, status)
	containerFileID := asStringAny(createdFile["id"])
	containerFile, err := app.Store.GetCodeInterpreterContainerFile(context.Background(), containerID, containerFileID)
	require.NoError(t, err)

	status, deletePayload := rawRequest(t, app, http.MethodDelete, "/v1/containers/"+containerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, containerID, asStringAny(deletePayload["id"]))
	require.Equal(t, "container.deleted", asStringAny(deletePayload["object"]))
	require.Equal(t, true, deletePayload["deleted"])

	mu.Lock()
	require.True(t, destroyed[containerID])
	mu.Unlock()

	status, missing := rawRequest(t, app, http.MethodGet, "/v1/containers/"+containerID, nil)
	require.Equal(t, http.StatusNotFound, status)
	errorPayload, ok := missing["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))

	_, err = app.Store.GetFile(context.Background(), containerFile.BackingFileID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestCodeInterpreterCleanupLoopExpiresContainersInBackground(t *testing.T) {
	var (
		mu        sync.Mutex
		destroyed = map[string]int{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterCleanupInterval: 10 * time.Millisecond,
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			DestroySessionFunc: func(_ context.Context, sessionID string) error {
				mu.Lock()
				defer mu.Unlock()
				destroyed[sessionID]++
				return nil
			},
		},
	})

	ctx := context.Background()
	session := domain.CodeInterpreterSession{
		ID:                  "cntr_expired_cleanup",
		Backend:             "docker",
		Status:              "running",
		Name:                "Expired Container",
		MemoryLimit:         "1g",
		ExpiresAfterMinutes: 20,
		CreatedAt:           "2026-04-13T08:00:00Z",
		LastActiveAt:        "2026-04-13T07:00:00Z",
	}
	require.NoError(t, app.Store.SaveCodeInterpreterSession(ctx, session))
	require.NoError(t, app.Store.SaveFile(ctx, domain.StoredFile{
		ID:        "file_cleanup",
		Filename:  "report.txt",
		Purpose:   "assistants_output",
		Bytes:     4,
		CreatedAt: 1712995200,
		Status:    "processed",
		Content:   []byte("done"),
	}))
	_, err := app.Store.SaveCodeInterpreterContainerFile(ctx, domain.CodeInterpreterContainerFile{
		ID:                "cfile_cleanup",
		ContainerID:       session.ID,
		BackingFileID:     "file_cleanup",
		DeleteBackingFile: true,
		Path:              "/mnt/data/report.txt",
		Source:            "assistant",
		Bytes:             4,
		CreatedAt:         1712995200,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		got, err := app.Store.GetCodeInterpreterSession(ctx, session.ID)
		return err == nil && got.Status == "expired"
	}, time.Second, 20*time.Millisecond)

	mu.Lock()
	require.Equal(t, 1, destroyed[session.ID])
	mu.Unlock()

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/containers/"+session.ID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "expired", asStringAny(payload["status"]))

	status, expiredFiles := rawRequest(t, app, http.MethodGet, "/v1/containers/"+session.ID+"/files?limit=10", nil)
	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := expiredFiles["error"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, asStringAny(errorPayload["message"]), "expired")

	_, err = app.Store.GetCodeInterpreterContainerFile(ctx, session.ID, "cfile_cleanup")
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	status, backingPayload := rawRequest(t, app, http.MethodGet, "/v1/files/file_cleanup", nil)
	require.Equal(t, http.StatusNotFound, status)
	errorPayload, ok = backingPayload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "not_found_error", asStringAny(errorPayload["type"]))
}

func TestContainerFilesCreateListGetContentDelete(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				if _, ok := activeSessions[req.SessionID]; !ok {
					activeSessions[req.SessionID] = map[string][]byte{}
				}
				return nil
			},
			UploadFileFunc: func(_ context.Context, sessionID string, file sandbox.SessionFile) error {
				mu.Lock()
				defer mu.Unlock()
				session := activeSessions[sessionID]
				session[file.Name] = append([]byte(nil), file.Content...)
				return nil
			},
			DeleteFileFunc: func(_ context.Context, sessionID string, name string) error {
				mu.Lock()
				defer mu.Unlock()
				delete(activeSessions[sessionID], name)
				return nil
			},
		},
	})

	status, createdPayload := rawRequest(t, app, http.MethodPost, "/v1/containers", map[string]any{"name": "File Box"})
	require.Equal(t, http.StatusOK, status)
	containerID := asStringAny(createdPayload["id"])

	status, createdFile := uploadContainerFile(t, app, containerID, "notes.txt", []byte("hello from container"))
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(createdFile["id"])
	require.NotEmpty(t, fileID)
	require.Equal(t, "container.file", asStringAny(createdFile["object"]))
	require.Equal(t, containerID, asStringAny(createdFile["container_id"]))
	require.Equal(t, "user", asStringAny(createdFile["source"]))
	require.Equal(t, "notes.txt", path.Base(asStringAny(createdFile["path"])))
	firstContainerFile, err := app.Store.GetCodeInterpreterContainerFile(context.Background(), containerID, fileID)
	require.NoError(t, err)
	firstBackingFileID := firstContainerFile.BackingFileID

	status, listPayload := rawRequest(t, app, http.MethodGet, "/v1/containers/"+containerID+"/files?limit=10&order=asc", nil)
	require.Equal(t, http.StatusOK, status)
	data, ok := listPayload["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	require.Equal(t, fileID, asStringAny(data[0].(map[string]any)["id"]))

	status, getPayload := rawRequest(t, app, http.MethodGet, "/v1/containers/"+containerID+"/files/"+fileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fileID, asStringAny(getPayload["id"]))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/containers/"+containerID+"/files/"+fileID+"/content", nil)
	require.NoError(t, err)
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	content, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "hello from container", string(content))

	status, replacedFile := uploadContainerFile(t, app, containerID, "notes.txt", []byte("updated from container"))
	require.Equal(t, http.StatusOK, status)
	replacedFileID := asStringAny(replacedFile["id"])
	require.NotEmpty(t, replacedFileID)
	require.NotEqual(t, fileID, replacedFileID)

	_, err = app.Store.GetCodeInterpreterContainerFile(context.Background(), containerID, fileID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)
	_, err = app.Store.GetFile(context.Background(), firstBackingFileID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	replacedContainerFile, err := app.Store.GetCodeInterpreterContainerFile(context.Background(), containerID, replacedFileID)
	require.NoError(t, err)
	secondBackingFileID := replacedContainerFile.BackingFileID

	status, deletePayload := rawRequest(t, app, http.MethodDelete, "/v1/containers/"+containerID+"/files/"+replacedFileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, replacedFileID, asStringAny(deletePayload["id"]))
	require.Equal(t, "container.file.deleted", asStringAny(deletePayload["object"]))
	require.Equal(t, true, deletePayload["deleted"])
	_, err = app.Store.GetFile(context.Background(), secondBackingFileID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	status, listAfterDelete := rawRequest(t, app, http.MethodGet, "/v1/containers/"+containerID+"/files?limit=10", nil)
	require.Equal(t, http.StatusOK, status)
	data, ok = listAfterDelete["data"].([]any)
	require.True(t, ok)
	require.Empty(t, data)
}

func TestContainerFilesRejectEmptyFileID(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	status, createdPayload := rawRequest(t, app, http.MethodPost, "/v1/containers", map[string]any{"name": "Empty File ID"})
	require.Equal(t, http.StatusOK, status)
	containerID := asStringAny(createdPayload["id"])

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/containers/"+containerID+"/files", map[string]any{
		"file_id": "",
	})
	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Equal(t, "file_id", asStringAny(errorPayload["param"]))
}

func TestResponsesCreateLocalCodeInterpreterUsesExplicitContainerAndRestoresPersistedFiles(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				if _, ok := activeSessions[req.SessionID]; !ok {
					activeSessions[req.SessionID] = map[string][]byte{}
				}
				return nil
			},
			UploadFileFunc: func(_ context.Context, sessionID string, file sandbox.SessionFile) error {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return sandbox.ErrSessionNotFound
				}
				session[file.Name] = append([]byte(nil), file.Content...)
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				require.Contains(t, req.Code, `open("codes.txt"`)
				return sandbox.ExecuteResult{Logs: string(session["codes.txt"])}, nil
			},
		},
	})

	status, createdContainer := rawRequest(t, app, http.MethodPost, "/v1/containers", map[string]any{"name": "Explicit"})
	require.Equal(t, http.StatusOK, status)
	containerID := asStringAny(createdContainer["id"])

	status, uploaded := uploadFile(t, app, "codes.txt", "user_data", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	storedFileID := asStringAny(uploaded["id"])

	status, attached := rawRequest(t, app, http.MethodPost, "/v1/containers/"+containerID+"/files", map[string]any{
		"file_id": storedFileID,
	})
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, asStringAny(attached["id"]))

	mu.Lock()
	delete(activeSessions, containerID)
	mu.Unlock()

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "What is the code in the uploaded file? Return only the number.",
		"tools": []map[string]any{
			{
				"type":      "code_interpreter",
				"container": containerID,
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	require.Equal(t, containerID, asStringAny(response.Output[0].Map()["container_id"]))
}

func TestResponsesCreateExecutesLocalCodeInterpreter(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "4", response.OutputText)
	require.Len(t, response.Output, 2)
	require.Equal(t, "code_interpreter_call", response.Output[0].Type)
	require.Equal(t, "message", response.Output[1].Type)

	payload := response.Output[0].Map()
	require.Equal(t, "completed", asStringAny(payload["status"]))
	require.Equal(t, "print(2+2)", asStringAny(payload["code"]))
	require.NotEmpty(t, asStringAny(payload["container_id"]))
	require.Nil(t, payload["outputs"])

	got := getResponse(t, app, response.ID)
	require.Equal(t, "4", got.OutputText)
	require.Equal(t, "code_interpreter_call", got.Output[0].Type)
}

func TestResponsesCreateLocalCodeInterpreterReturnsFailedResponseOnExecutionTimeout(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			ExecuteFunc: func(_ context.Context, _ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				return sandbox.ExecuteResult{Logs: "Traceback: sandbox execution timed out\n"}, context.DeadlineExceeded
			},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "Use Python to calculate 2+2. Return only the numeric result.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "failed", response.Status)
	require.Nil(t, response.CompletedAt)
	require.Empty(t, response.OutputText)
	require.JSONEq(t, `{"code":"server_error","message":"shim-local code_interpreter execution timed out"}`, string(response.Error))
	require.Len(t, response.Output, 1)
	require.Equal(t, "code_interpreter_call", response.Output[0].Type)

	callItem := response.Output[0].Map()
	require.Equal(t, "failed", asStringAny(callItem["status"]))
	require.Equal(t, "print(2+2)", asStringAny(callItem["code"]))
	require.NotEmpty(t, asStringAny(callItem["container_id"]))

	outputs, ok := callItem["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)
	logEntry, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "logs", asStringAny(logEntry["type"]))
	require.Equal(t, "Traceback: sandbox execution timed out\n", asStringAny(logEntry["logs"]))

	got := getResponse(t, app, response.ID)
	require.Equal(t, "failed", got.Status)
	require.JSONEq(t, `{"code":"server_error","message":"shim-local code_interpreter execution timed out"}`, string(got.Error))
	require.Len(t, got.Output, 1)
	require.Equal(t, "failed", asStringAny(got.Output[0].Map()["status"]))

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+response.ID+"?stream=true", nil)
	require.NoError(t, err)
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.failed")
	require.NotContains(t, eventTypes(events), "response.completed")
	require.NotContains(t, eventTypes(events), "response.code_interpreter_call.completed")

	failed := findEvent(t, events, "response.failed").Data
	responsePayload, ok := failed["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "failed", asStringAny(responsePayload["status"]))
	errorPayload, ok := responsePayload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "server_error", asStringAny(errorPayload["code"]))
	require.Equal(t, "shim-local code_interpreter execution timed out", asStringAny(errorPayload["message"]))
}

func TestResponsesCreateLocalCodeInterpreterCompletesResponseOnToolError(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			ExecuteFunc: func(_ context.Context, _ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				return sandbox.ExecuteResult{
					Logs: "Traceback (most recent call last):\nRuntimeError: fixture boom\n",
				}, &sandbox.ToolExecutionError{Err: errors.New("exit status 1")}
			},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "Use Python to calculate 2+2. Return only the numeric result.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.NotNil(t, response.CompletedAt)
	require.JSONEq(t, `null`, string(response.Error))
	require.Equal(t, `The run failed because the code deliberately raised a RuntimeError with the message "fixture boom."`, response.OutputText)
	require.Len(t, response.Output, 2)

	callItem := response.Output[0].Map()
	require.Equal(t, "completed", asStringAny(callItem["status"]))
	require.Equal(t, "print(2+2)", asStringAny(callItem["code"]))
	outputs, ok := callItem["outputs"].([]any)
	require.True(t, ok)
	require.Empty(t, outputs)

	messageItem := response.Output[1].Map()
	content, ok := messageItem["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	textPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, response.OutputText, asStringAny(textPart["text"]))
	require.Equal(t, []any{}, textPart["annotations"])

	got := getResponse(t, app, response.ID)
	require.Equal(t, "completed", got.Status)
	require.JSONEq(t, `null`, string(got.Error))
	require.Equal(t, response.OutputText, got.OutputText)
}

func TestResponsesCreateLocalCodeInterpreterStagesContainerFileIDs(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = map[string][]byte{}
				return nil
			},
			UploadFileFunc: func(_ context.Context, sessionID string, file sandbox.SessionFile) error {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return sandbox.ErrSessionNotFound
				}
				session[file.Name] = append([]byte(nil), file.Content...)
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				require.Contains(t, req.Code, `open("codes.txt"`)
				content, ok := session["codes.txt"]
				require.True(t, ok)
				return sandbox.ExecuteResult{Logs: string(content)}, nil
			},
		},
	})

	status, uploaded := uploadFile(t, app, "codes.txt", "assistants", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Use Python to read the uploaded file and return only the code.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type":     "auto",
					"file_ids": []string{fileID},
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	require.Equal(t, "code_interpreter_call", response.Output[0].Type)
	require.NotEmpty(t, asStringAny(response.Output[0].Map()["container_id"]))
}

func TestResponsesCreateLocalCodeInterpreterAutoUploadsInputFileID(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = map[string][]byte{}
				return nil
			},
			UploadFileFunc: func(_ context.Context, sessionID string, file sandbox.SessionFile) error {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return sandbox.ErrSessionNotFound
				}
				session[file.Name] = append([]byte(nil), file.Content...)
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				require.Contains(t, req.Code, `open("codes.txt"`)
				content, ok := session["codes.txt"]
				require.True(t, ok)
				return sandbox.ExecuteResult{Logs: string(content)}, nil
			},
		},
	})

	status, uploaded := uploadFile(t, app, "codes.txt", "user_data", []byte("Remember: code=777. Reply OK."), nil)
	require.Equal(t, http.StatusOK, status)
	fileID := asStringAny(uploaded["id"])

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "What is the code in the uploaded file? Return only the number."},
					{"type": "input_file", "file_id": fileID},
				},
			},
		},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	require.Len(t, response.Output, 2)
	require.Equal(t, "code_interpreter_call", response.Output[0].Type)
	require.NotEmpty(t, asStringAny(response.Output[0].Map()["container_id"]))
}

func TestResponsesCreateLocalCodeInterpreterAutoUploadsInlineInputFileData(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = map[string][]byte{}
				return nil
			},
			UploadFileFunc: func(_ context.Context, sessionID string, file sandbox.SessionFile) error {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return sandbox.ErrSessionNotFound
				}
				session[file.Name] = append([]byte(nil), file.Content...)
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				require.Contains(t, req.Code, `open("codes.txt"`)
				content, ok := session["codes.txt"]
				require.True(t, ok)
				return sandbox.ExecuteResult{Logs: string(content)}, nil
			},
		},
	})

	inlineData := base64.StdEncoding.EncodeToString([]byte("Remember: code=777. Reply OK."))

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Read the uploaded file and return only the code."},
					{"type": "input_file", "filename": "codes.txt", "file_data": inlineData},
				},
			},
		},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	require.Len(t, response.Output, 2)
	require.Equal(t, "code_interpreter_call", response.Output[0].Type)
	require.NotEmpty(t, asStringAny(response.Output[0].Map()["container_id"]))
}

func TestResponsesCreateLocalCodeInterpreterAutoUploadsInputFileURL(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/codes.txt", r.URL.Path)
		_, err := io.WriteString(w, "Remember: code=777. Reply OK.")
		require.NoError(t, err)
	}))
	defer fileServer.Close()

	parsedURL, err := url.Parse(fileServer.URL)
	require.NoError(t, err)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterInputFileURLPolicy:     config.ResponsesCodeInterpreterInputFileURLPolicyAllowlist,
		CodeInterpreterInputFileURLAllowHosts: []string{parsedURL.Hostname()},
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = map[string][]byte{}
				return nil
			},
			UploadFileFunc: func(_ context.Context, sessionID string, file sandbox.SessionFile) error {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return sandbox.ErrSessionNotFound
				}
				session[file.Name] = append([]byte(nil), file.Content...)
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				require.Contains(t, req.Code, `open("codes.txt"`)
				content, ok := session["codes.txt"]
				require.True(t, ok)
				return sandbox.ExecuteResult{Logs: string(content)}, nil
			},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Read the uploaded file and return the code."},
					{"type": "input_file", "file_url": fileServer.URL + "/codes.txt"},
				},
			},
		},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "777", response.OutputText)
	require.Len(t, response.Output, 2)
	require.Equal(t, "code_interpreter_call", response.Output[0].Type)
	require.NotEmpty(t, asStringAny(response.Output[0].Map()["container_id"]))
}

func TestResponsesCreateLocalCodeInterpreterRejectsInputFileURLWhenPolicyDisabled(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Read the uploaded file and return the code."},
					{"type": "input_file", "file_url": "https://example.com/codes.txt"},
				},
			},
		},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Contains(t, asStringAny(errorPayload["message"]), "disables input_file.file_url by default")
	require.Equal(t, "input", asStringAny(errorPayload["param"]))
}

func TestResponsesCreateLocalCodeInterpreterRejectsInputFileURLFromNonAllowlistedHost(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterInputFileURLPolicy:     config.ResponsesCodeInterpreterInputFileURLPolicyAllowlist,
		CodeInterpreterInputFileURLAllowHosts: []string{"example.com"},
		CodeInterpreterBackend:                testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Read the uploaded file and return the code."},
					{"type": "input_file", "file_url": "https://not-allowed.example.net/codes.txt"},
				},
			},
		},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Contains(t, asStringAny(errorPayload["message"]), "not allowlisted")
	require.Equal(t, "input", asStringAny(errorPayload["param"]))
}

func TestResponsesCreateLocalCodeInterpreterRejectsUnsupportedInputFileURLScheme(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterInputFileURLPolicy: config.ResponsesCodeInterpreterInputFileURLPolicyUnsafeAllowHTTPHTTPS,
		CodeInterpreterBackend:            testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Read the uploaded file and return the code."},
					{"type": "input_file", "file_url": "file:///tmp/codes.txt"},
				},
			},
		},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Contains(t, asStringAny(errorPayload["message"]), "http(s)")
	require.Equal(t, "input", asStringAny(errorPayload["param"]))
}

func TestResponsesCreateLocalCodeInterpreterPersistsGeneratedArtifacts(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = map[string][]byte{}
				return nil
			},
			ListFilesFunc: func(_ context.Context, sessionID string) ([]sandbox.SessionFile, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return nil, sandbox.ErrSessionNotFound
				}
				files := make([]sandbox.SessionFile, 0, len(session))
				for name, content := range session {
					files = append(files, sandbox.SessionFile{
						Name:    name,
						Content: append([]byte(nil), content...),
					})
				}
				slices.SortFunc(files, func(a, b sandbox.SessionFile) int {
					return strings.Compare(a.Name, b.Name)
				})
				return files, nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				session["report.txt"] = []byte("artifact-body")
				return sandbox.ExecuteResult{Logs: "created report.txt\n"}, nil
			},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "Use Python to write report.txt containing artifact-body, then say created.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	expectedOutputText := "Created report.txt."
	expectedAnnotationStart := strings.Index(expectedOutputText, "report.txt")
	expectedAnnotationEnd := expectedAnnotationStart + len("report.txt")

	require.Equal(t, "completed", response.Status)
	require.Equal(t, expectedOutputText, response.OutputText)
	require.Len(t, response.Output, 2)

	callPayload := response.Output[0].Map()
	outputs, ok := callPayload["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)

	logOutput, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "logs", asStringAny(logOutput["type"]))
	require.Equal(t, "created report.txt\n", asStringAny(logOutput["logs"]))

	messagePayload := response.Output[1].Map()
	content, ok := messagePayload["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	textPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, expectedOutputText, asStringAny(textPart["text"]))
	annotations, ok := textPart["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, annotations, 1)
	annotation, ok := annotations[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "container_file_citation", asStringAny(annotation["type"]))
	fileID := asStringAny(annotation["file_id"])
	require.Equal(t, fileID, asStringAny(annotation["file_id"]))
	require.Equal(t, "report.txt", asStringAny(annotation["filename"]))
	require.NotEmpty(t, asStringAny(annotation["container_id"]))
	require.EqualValues(t, expectedAnnotationStart, annotation["start_index"])
	require.EqualValues(t, expectedAnnotationEnd, annotation["end_index"])

	containerID := asStringAny(annotation["container_id"])
	status, filePayload := rawRequest(t, app, http.MethodGet, "/v1/containers/"+containerID+"/files/"+fileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fileID, asStringAny(filePayload["id"]))
	require.Equal(t, "report.txt", path.Base(asStringAny(filePayload["path"])))
	require.Equal(t, "assistant", asStringAny(filePayload["source"]))
	require.EqualValues(t, len("artifact-body"), filePayload["bytes"])
	containerFile, err := app.Store.GetCodeInterpreterContainerFile(context.Background(), containerID, fileID)
	require.NoError(t, err)
	backingFileID := containerFile.BackingFileID
	require.NotEmpty(t, backingFileID)

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/containers/"+containerID+"/files/"+fileID+"/content", nil)
	require.NoError(t, err)
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "artifact-body", string(body))

	status, backingPayload := rawRequest(t, app, http.MethodGet, "/v1/files/"+backingFileID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, backingFileID, asStringAny(backingPayload["id"]))
	require.Equal(t, "assistants_output", asStringAny(backingPayload["purpose"]))

	stored := getResponse(t, app, response.ID)
	require.Len(t, stored.Output, 2)
	storedOutputs, ok := stored.Output[0].Map()["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, storedOutputs, 1)
	storedContent, ok := stored.Output[1].Map()["content"].([]any)
	require.True(t, ok)
	require.Len(t, storedContent, 1)
	storedAnnotations, ok := storedContent[0].(map[string]any)["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, storedAnnotations, 1)

	streamReq, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+response.ID+"?stream=true", nil)
	require.NoError(t, err)
	streamResp, err := app.Client().Do(streamReq)
	require.NoError(t, err)
	defer streamResp.Body.Close()
	require.Equal(t, http.StatusOK, streamResp.StatusCode)
	require.Contains(t, streamResp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, streamResp.Body)
	require.Contains(t, eventTypes(events), "response.output_text.annotation.added")
	annotationEvents := findEvents(events, "response.output_text.annotation.added")
	require.Len(t, annotationEvents, 1)
	annotationPayload := annotationEvents[0].Data
	require.EqualValues(t, 0, annotationPayload["annotation_index"])
	streamAnnotation, ok := annotationPayload["annotation"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "container_file_citation", asStringAny(streamAnnotation["type"]))
	require.Equal(t, fileID, asStringAny(streamAnnotation["file_id"]))

	outputDoneEvents := findEvents(events, "response.output_item.done")
	require.Len(t, outputDoneEvents, 2)
	outputDone := outputDoneEvents[1].Data
	doneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	doneContent, ok := doneItem["content"].([]any)
	require.True(t, ok)
	require.Len(t, doneContent, 1)
	doneTextPart, ok := doneContent[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, expectedOutputText, asStringAny(doneTextPart["text"]))
	doneAnnotations, ok := doneTextPart["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, doneAnnotations, 1)
}

func TestResponsesCreateLocalCodeInterpreterPersistsGeneratedImageArtifacts(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]map[string][]byte{}
		pngBytes       = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = map[string][]byte{}
				return nil
			},
			ListFilesFunc: func(_ context.Context, sessionID string) ([]sandbox.SessionFile, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[sessionID]
				if !ok {
					return nil, sandbox.ErrSessionNotFound
				}
				files := make([]sandbox.SessionFile, 0, len(session))
				for name, content := range session {
					files = append(files, sandbox.SessionFile{
						Name:    name,
						Content: append([]byte(nil), content...),
					})
				}
				slices.SortFunc(files, func(left, right sandbox.SessionFile) int {
					return strings.Compare(left.Name, right.Name)
				})
				return files, nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				session, ok := activeSessions[req.SessionID]
				if !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				session["plot.png"] = append([]byte(nil), pngBytes...)
				return sandbox.ExecuteResult{Logs: "created plot.png\n"}, nil
			},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":   "test-model",
		"store":   true,
		"input":   "Use Python to write plot.png and then say created.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, "completed", response.Status)
	require.Equal(t, "Created plot.png.", response.OutputText)
	require.Len(t, response.Output, 2)

	callPayload := response.Output[0].Map()
	outputs, ok := callPayload["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)

	logOutput, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "logs", asStringAny(logOutput["type"]))

	messagePayload := response.Output[1].Map()
	content, ok := messagePayload["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	textPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	annotations, ok := textPart["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, annotations, 1)
	annotation, ok := annotations[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "container_file_citation", asStringAny(annotation["type"]))
	containerID := asStringAny(annotation["container_id"])
	containerFileID := asStringAny(annotation["file_id"])
	require.NotEmpty(t, containerID)
	require.NotEmpty(t, containerFileID)
	imageURL := "/v1/containers/" + containerID + "/files/" + containerFileID + "/content"

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+imageURL, nil)
	require.NoError(t, err)
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, pngBytes, body)
}

func TestResponsesCreateLocalCodeInterpreterStreamReplaysToolEvents(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(mustJSON(t, map[string]any{
		"model":   "test-model",
		"store":   true,
		"stream":  true,
		"input":   "Use Python to calculate 2+2. Return only the numeric result.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.in_progress")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call_code.delta")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call_code.done")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.interpreting")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.completed")

	added := findEvents(events, "response.output_item.added")
	require.Len(t, added, 2)
	require.Equal(t, "code_interpreter_call", asStringAny(added[0].Data["item"].(map[string]any)["type"]))
	require.Equal(t, "message", asStringAny(added[1].Data["item"].(map[string]any)["type"]))

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "4", asStringAny(responsePayload["output_text"]))

	output, ok := responsePayload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 2)
	callItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	outputs, ok := callItem["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)
	logEntry, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "logs", asStringAny(logEntry["type"]))
	require.Equal(t, "4\n", asStringAny(logEntry["logs"]))
}

func TestResponsesCreateLocalCodeInterpreterStreamReturnsFailedResponseOnExecutionTimeout(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			ExecuteFunc: func(_ context.Context, _ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				return sandbox.ExecuteResult{Logs: "Traceback: sandbox execution timed out\n"}, context.DeadlineExceeded
			},
		},
	})

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(mustJSON(t, map[string]any{
		"model":   "test-model",
		"store":   true,
		"stream":  true,
		"input":   "Use Python to calculate 2+2. Return only the numeric result.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.Contains(t, eventTypes(events), "response.failed")
	require.NotContains(t, eventTypes(events), "response.completed")
	require.NotContains(t, eventTypes(events), "response.code_interpreter_call.completed")

	added := findEvents(events, "response.output_item.added")
	require.Len(t, added, 1)
	require.Equal(t, "code_interpreter_call", asStringAny(added[0].Data["item"].(map[string]any)["type"]))

	failed := findEvent(t, events, "response.failed").Data
	responsePayload, ok := failed["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "failed", asStringAny(responsePayload["status"]))
	require.Empty(t, asStringAny(responsePayload["output_text"]))

	output, ok := responsePayload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	callItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "failed", asStringAny(callItem["status"]))
	require.Equal(t, "print(2+2)", asStringAny(callItem["code"]))

	errorPayload, ok := responsePayload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "server_error", asStringAny(errorPayload["code"]))
	require.Equal(t, "shim-local code_interpreter execution timed out", asStringAny(errorPayload["message"]))
}

func TestResponsesCreateLocalCodeInterpreterStreamCompletesResponseOnToolError(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			ExecuteFunc: func(_ context.Context, _ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				return sandbox.ExecuteResult{
					Logs: "Traceback (most recent call last):\nRuntimeError: fixture boom\n",
				}, &sandbox.ToolExecutionError{Err: errors.New("exit status 1")}
			},
		},
	})

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(mustJSON(t, map[string]any{
		"model":   "test-model",
		"store":   true,
		"stream":  true,
		"input":   "Use Python to calculate 2+2. Return only the numeric result.",
		"include": []string{"code_interpreter_call.outputs"},
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	events := readSSEEvents(t, resp.Body)
	require.NotContains(t, eventTypes(events), "response.failed")
	require.Contains(t, eventTypes(events), "response.completed")
	require.Contains(t, eventTypes(events), "response.code_interpreter_call.completed")

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", asStringAny(responsePayload["status"]))
	require.Nil(t, responsePayload["error"])

	output, ok := responsePayload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 2)
	callItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", asStringAny(callItem["status"]))
	outputs, ok := callItem["outputs"].([]any)
	require.True(t, ok)
	require.Empty(t, outputs)

	messageItem, ok := output[1].(map[string]any)
	require.True(t, ok)
	content, ok := messageItem["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	textPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, `The run failed because the code deliberately raised a RuntimeError with the message "fixture boom."`, asStringAny(textPart["text"]))
}

func TestResponsesCreateLocalCodeInterpreterRejectsUnknownContainerFileID(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use Python to read the uploaded file and return only the code.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type":     "auto",
					"file_ids": []string{"file_missing"},
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Contains(t, asStringAny(errorPayload["message"]), "code_interpreter.container.file_ids")
}

func TestResponsesCreateLocalCodeInterpreterWorksAfterStoredFollowUp(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{KindValue: "docker"},
	})

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})
	require.Equal(t, "4", first.OutputText)

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"previous_response_id": first.ID,
		"input":                "Say OK and nothing else",
	})

	require.Equal(t, "OK", second.OutputText)
	require.Len(t, second.Output, 1)
	require.Equal(t, "message", second.Output[0].Type)
}

func TestResponsesCreateLocalCodeInterpreterReusesStoredSessionContainerID(t *testing.T) {
	var (
		mu                 sync.Mutex
		activeSessions     = map[string]struct{}{}
		executedSessionIDs []string
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = struct{}{}
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				if _, ok := activeSessions[req.SessionID]; !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				executedSessionIDs = append(executedSessionIDs, req.SessionID)
				return sandbox.ExecuteResult{Logs: "4\n"}, nil
			},
		},
	})

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})
	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input":                "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	firstContainerID := asStringAny(first.Output[0].Map()["container_id"])
	secondContainerID := asStringAny(second.Output[0].Map()["container_id"])
	require.NotEmpty(t, firstContainerID)
	require.Equal(t, firstContainerID, secondContainerID)
	require.Equal(t, []string{firstContainerID, firstContainerID}, executedSessionIDs)
}

func TestResponsesCreateLocalCodeInterpreterRestoresSameSessionWhenStoredRuntimeIsGone(t *testing.T) {
	var (
		mu             sync.Mutex
		activeSessions = map[string]struct{}{}
	)

	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		CodeInterpreterBackend: testutil.FakeSandboxBackend{
			KindValue: "docker",
			CreateSessionFunc: func(_ context.Context, req sandbox.CreateSessionRequest) error {
				mu.Lock()
				defer mu.Unlock()
				activeSessions[req.SessionID] = struct{}{}
				return nil
			},
			ExecuteFunc: func(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
				mu.Lock()
				defer mu.Unlock()
				if _, ok := activeSessions[req.SessionID]; !ok {
					return sandbox.ExecuteResult{}, sandbox.ErrSessionNotFound
				}
				return sandbox.ExecuteResult{Logs: "4\n"}, nil
			},
		},
	})

	first := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	firstContainerID := asStringAny(first.Output[0].Map()["container_id"])
	mu.Lock()
	delete(activeSessions, firstContainerID)
	mu.Unlock()

	second := postResponse(t, app, map[string]any{
		"model":                "test-model",
		"store":                true,
		"previous_response_id": first.ID,
		"input":                "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	secondContainerID := asStringAny(second.Output[0].Map()["container_id"])
	require.NotEmpty(t, firstContainerID)
	require.NotEmpty(t, secondContainerID)
	require.Equal(t, firstContainerID, secondContainerID)
}

func TestResponsesCreateLocalCodeInterpreterLocalOnlyRequiresUnsafeExecutor(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode: config.ResponsesModeLocalOnly,
	})

	status, payload := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "test-model",
		"input": "Use Python to calculate 2+2. Return only the numeric result.",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
		"tool_choice": "required",
	})

	require.Equal(t, http.StatusBadRequest, status)
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Contains(t, asStringAny(errorPayload["message"]), "responses.code_interpreter.backend")
}

func TestResponsesCreateLocalCodeInterpreterStreamLocalOnlyRequiresUnsafeExecutor(t *testing.T) {
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		ResponsesMode: config.ResponsesModeLocalOnly,
	})

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/responses", bytes.NewReader(mustJSON(t, map[string]any{
		"model":       "test-model",
		"stream":      true,
		"input":       "Use Python to calculate 2+2. Return only the numeric result.",
		"tool_choice": "required",
		"tools": []map[string]any{
			{
				"type": "code_interpreter",
				"container": map[string]any{
					"type": "auto",
				},
			},
		},
	})))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	errorPayload, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", asStringAny(errorPayload["type"]))
	require.Contains(t, asStringAny(errorPayload["message"]), "responses.code_interpreter.backend")
}

func postResponse(t *testing.T, app *testutil.TestApp, payload map[string]any) domain.Response {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", payload)
	require.Equal(t, http.StatusOK, status)

	var response domain.Response
	mustDecode(t, body, &response)
	return response
}

func getResponse(t *testing.T, app *testutil.TestApp, id string) domain.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+id, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response domain.Response
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	return response
}

func getResponseInputItems(t *testing.T, app *testutil.TestApp, id string) conversationItemsListResponse {
	return getResponseInputItemsWithQuery(t, app, id, "")
}

func getResponseInputItemsWithQuery(t *testing.T, app *testutil.TestApp, id string, rawQuery string) conversationItemsListResponse {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+id+"/input_items"+rawQuery, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var items conversationItemsListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&items))
	return items
}

func postConversation(t *testing.T, app *testutil.TestApp, payload map[string]any) conversationResource {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodPost, "/v1/conversations", payload)
	require.Equal(t, http.StatusOK, status)

	var conversation conversationResource
	mustDecode(t, body, &conversation)
	return conversation
}

func getConversation(t *testing.T, app *testutil.TestApp, conversationID string) conversationResource {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/conversations/"+conversationID, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var conversation conversationResource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&conversation))
	return conversation
}

func getConversationItems(t *testing.T, app *testutil.TestApp, conversationID, rawQuery string) conversationItemsListResponse {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/conversations/"+conversationID+"/items"+rawQuery, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var items conversationItemsListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&items))
	return items
}

func postConversationItems(t *testing.T, app *testutil.TestApp, conversationID string, payload map[string]any) conversationItemsListResponse {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodPost, "/v1/conversations/"+conversationID+"/items", payload)
	require.Equal(t, http.StatusOK, status)

	var items conversationItemsListResponse
	mustDecode(t, body, &items)
	return items
}

func getConversationItem(t *testing.T, app *testutil.TestApp, conversationID, itemID string) map[string]any {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversationID+"/items/"+itemID, nil)
	require.Equal(t, http.StatusOK, status)
	return body
}

func seedConversationWithResponse(t *testing.T, app *testutil.TestApp) conversationResource {
	t.Helper()

	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "system", "content": "You are a test assistant."},
			{"type": "message", "role": "user", "content": "Remember: code=777. Reply OK."},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"store":        true,
		"conversation": conversation.ID,
		"input":        "What is the code? Reply with just the number.",
	})
	require.Equal(t, "777", response.OutputText)

	return conversation
}

func rawRequest(t *testing.T, app *testutil.TestApp, method, path string, payload any) (int, map[string]any) {
	t.Helper()

	var bodyBytes []byte
	if payload != nil {
		var err error
		bodyBytes, err = json.Marshal(payload)
		require.NoError(t, err)
	}

	req, err := http.NewRequest(method, app.Server.URL+path, bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return resp.StatusCode, decoded
}

func uploadFile(t *testing.T, app *testutil.TestApp, filename, purpose string, content []byte, extraFields map[string]string) (int, map[string]any) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("purpose", purpose))
	for key, value := range extraFields {
		require.NoError(t, writer.WriteField(key, value))
	}
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/files", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return resp.StatusCode, decoded
}

func uploadContainerFile(t *testing.T, app *testutil.TestApp, containerID string, filename string, content []byte) (int, map[string]any) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req, err := http.NewRequest(http.MethodPost, app.Server.URL+"/v1/containers/"+containerID+"/files", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return resp.StatusCode, decoded
}

func getFileContent(t *testing.T, app *testutil.TestApp, fileID string) []byte {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/files/"+fileID+"/content", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	content, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return content
}

func mustDecode(t *testing.T, payload map[string]any, dst any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, dst))
}

func mustJSON(t *testing.T, payload any) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

type sseEvent struct {
	Event string
	Data  map[string]any
	Raw   string
}

type conversationItemsListResponse struct {
	Object  string           `json:"object"`
	Data    []map[string]any `json:"data"`
	FirstID *string          `json:"first_id"`
	LastID  *string          `json:"last_id"`
	HasMore bool             `json:"has_more"`
}

type chatCompletionsListResponse struct {
	Object  string           `json:"object"`
	Data    []map[string]any `json:"data"`
	FirstID *string          `json:"first_id"`
	LastID  *string          `json:"last_id"`
	HasMore bool             `json:"has_more"`
}

type conversationResource struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Metadata  map[string]string `json:"metadata"`
}

func responseConversationID(response domain.Response) string {
	if response.Conversation == nil {
		return ""
	}
	return response.Conversation.ID
}

func readSSEEvents(t *testing.T, body io.Reader) []sseEvent {
	t.Helper()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024), 1<<20)

	var (
		eventName string
		dataLines []string
		events    []sseEvent
	)

	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		raw := strings.Join(dataLines, "\n")
		event := sseEvent{Event: eventName, Raw: raw}
		if raw != "[DONE]" {
			require.NoError(t, json.Unmarshal([]byte(raw), &event.Data))
		}
		events = append(events, event)
		eventName = ""
		dataLines = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	require.NoError(t, scanner.Err())
	flush()
	return events
}

func eventTypes(events []sseEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Event)
	}
	return out
}

func findEvent(t *testing.T, events []sseEvent, eventType string) sseEvent {
	t.Helper()

	for _, event := range events {
		if event.Event == eventType {
			return event
		}
	}
	t.Fatalf("event %q not found", eventType)
	return sseEvent{}
}

func findEvents(events []sseEvent, eventType string) []sseEvent {
	out := make([]sseEvent, 0, len(events))
	for _, event := range events {
		if event.Event == eventType {
			out = append(out, event)
		}
	}
	return out
}

func eventIndex(t *testing.T, events []sseEvent, eventType string) int {
	t.Helper()

	for idx, event := range events {
		if event.Event == eventType {
			return idx
		}
	}
	t.Fatalf("event %q not found", eventType)
	return -1
}

func conversationItemTexts(items conversationItemsListResponse) []string {
	out := make([]string, 0, len(items.Data))
	for _, item := range items.Data {
		out = append(out, messageTextFromPayload(item))
	}
	return out
}

func conversationItemRoles(items conversationItemsListResponse) []string {
	out := make([]string, 0, len(items.Data))
	for _, item := range items.Data {
		out = append(out, asStringAny(item["role"]))
	}
	return out
}

func messageTextFromPayload(payload map[string]any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	decoded, err := domain.NewItem(raw)
	if err != nil {
		return ""
	}
	return domain.MessageText(decoded)
}

func firstContentText(payload map[string]any) string {
	content, ok := payload["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	part, ok := content[0].(map[string]any)
	if !ok {
		return ""
	}
	return asStringAny(part["text"])
}

func conversationItemTypes(items conversationItemsListResponse) []string {
	out := make([]string, 0, len(items.Data))
	for _, item := range items.Data {
		out = append(out, asStringAny(item["type"]))
	}
	return out
}

func conversationItemStatuses(items conversationItemsListResponse) []string {
	out := make([]string, 0, len(items.Data))
	for _, item := range items.Data {
		out = append(out, asStringAny(item["status"]))
	}
	return out
}

func asStringAny(value any) string {
	text, _ := value.(string)
	return text
}

func payloadID(payload map[string]any) string {
	return asStringAny(payload["id"])
}

func postStoredChatCompletion(t *testing.T, app *testutil.TestApp, payload map[string]any) string {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodPost, "/v1/chat/completions", payload)
	require.Equal(t, http.StatusOK, status)
	return asStringAny(body["id"])
}

func getStoredChatCompletions(t *testing.T, app *testutil.TestApp, rawQuery string) chatCompletionsListResponse {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/chat/completions"+rawQuery, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var page chatCompletionsListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	return page
}

func getStoredChatCompletion(t *testing.T, app *testutil.TestApp, id string) map[string]any {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/chat/completions/"+id, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	return payload
}

func getStoredChatCompletionMessages(t *testing.T, app *testutil.TestApp, id string, rawQuery string) conversationItemsListResponse {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/chat/completions/"+id+"/messages"+rawQuery, nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload conversationItemsListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	return payload
}
