package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/testutil"
)

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

	fileSearchCall, err := domain.NewItem([]byte(`{"id":"fs_test","type":"file_search_call","status":"completed","results":[{"file_id":"file_123","filename":"notes.txt","score":0.91}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_file_search",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"find notes about onboarding"}`,
		ResponseJSON:         `{"id":"resp_file_search","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"fs_test","type":"file_search_call","status":"completed","results":[{"file_id":"file_123","filename":"notes.txt","score":0.91}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
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

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	_, hasResults := addedItem["results"]
	require.False(t, hasResults)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	results, ok := outputDoneItem["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
}

func TestResponsesGetStreamReplaysFileSearchCallWithoutLeakingSearchResultsInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	fileSearchCall, err := domain.NewItem([]byte(`{"id":"fs_search_results_test","type":"file_search_call","status":"completed","search_results":[{"file_id":"file_456","filename":"handbook.txt","score":0.88}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_file_search_search_results",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"find onboarding handbook"}`,
		ResponseJSON:         `{"id":"resp_file_search_search_results","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"fs_search_results_test","type":"file_search_call","status":"completed","search_results":[{"file_id":"file_456","filename":"handbook.txt","score":0.88}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
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

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	_, hasSearchResults := addedItem["search_results"]
	require.False(t, hasSearchResults)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	searchResults, ok := outputDoneItem["search_results"].([]any)
	require.True(t, ok)
	require.Len(t, searchResults, 1)
}

func TestResponsesGetStreamReplaysCodeInterpreterCallWithoutLeakingOutputsInAdded(t *testing.T) {
	app := testutil.NewTestApp(t)

	codeInterpreterCall, err := domain.NewItem([]byte(`{"id":"ci_test","type":"code_interpreter_call","status":"completed","container_id":"cntr_123","outputs":[{"type":"logs","logs":"done"}]}`))
	require.NoError(t, err)

	stored := domain.StoredResponse{
		ID:                   "resp_code_interpreter",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"input":"run some Python"}`,
		ResponseJSON:         `{"id":"resp_code_interpreter","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"test-model","output":[{"id":"ci_test","type":"code_interpreter_call","status":"completed","container_id":"cntr_123","outputs":[{"type":"logs","logs":"done"}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{},"output_text":""}`,
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

	added := findEvent(t, events, "response.output_item.added").Data
	addedItem, ok := added["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "cntr_123", asStringAny(addedItem["container_id"]))
	_, hasOutputs := addedItem["outputs"]
	require.False(t, hasOutputs)

	outputDone := findEvent(t, events, "response.output_item.done").Data
	outputDoneItem, ok := outputDone["item"].(map[string]any)
	require.True(t, ok)
	outputs, ok := outputDoneItem["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)
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

func mustDecode(t *testing.T, payload map[string]any, dst any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, dst))
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
