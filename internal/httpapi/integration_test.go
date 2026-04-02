package httpapi_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/testutil"
)

func TestResponsesStoreAndGet(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
	})

	require.NotEmpty(t, response.ID)
	require.NotEmpty(t, response.OutputText)

	got := getResponse(t, app, response.ID)
	require.Equal(t, response.ID, got.ID)
	require.NotEmpty(t, got.OutputText)
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

	require.Equal(t, "upstream_resp_1", first.ID)
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

	require.Equal(t, conversation.ID, response.Conversation)
	require.Equal(t, "777", response.OutputText)
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

func TestConversationItemsMissingConversationReturns404(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/conv_missing/items", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", payload["error"].(map[string]any)["type"])
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

func TestConversationItemsRejectUnsupportedInclude(t *testing.T) {
	app := testutil.NewTestApp(t)
	conversation := seedConversationWithResponse(t, app)

	status, payload := rawRequest(t, app, http.MethodGet, "/v1/conversations/"+conversation.ID+"/items?include=message.output_text.logprobs", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", payload["error"].(map[string]any)["type"])
	require.Equal(t, "include", payload["error"].(map[string]any)["param"])
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

func TestResponsesStreamNormalizesDeltaOnlyUpstreamFlow(t *testing.T) {
	app := testutil.NewTestApp(t)

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

func TestResponsesWithSupportedGenerationFieldsProxyUpstreamAndShadowStore(t *testing.T) {
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

	require.Equal(t, "upstream_resp_1", response.ID)
	require.Equal(t, "OK", response.OutputText)

	got := getResponse(t, app, response.ID)
	require.Equal(t, response.ID, got.ID)
	require.Equal(t, "OK", got.OutputText)
}

func TestResponsesWithUnsupportedFieldsAreProxiedUpstream(t *testing.T) {
	app := testutil.NewTestApp(t)

	response := postResponse(t, app, map[string]any{
		"model": "test-model",
		"store": true,
		"input": "Say OK and nothing else",
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})

	require.Equal(t, "upstream_resp_1", response.ID)
	require.Equal(t, "OK", response.OutputText)
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

func TestResponsesWithUnsupportedFieldsUseUpstreamResponsesForLocalConversationState(t *testing.T) {
	app := testutil.NewTestApp(t)

	conversation := postConversation(t, app, map[string]any{
		"items": []map[string]any{
			{"type": "message", "role": "system", "content": "You are a test assistant."},
			{"type": "message", "role": "user", "content": "Remember: code=777. Reply OK."},
		},
	})

	response := postResponse(t, app, map[string]any{
		"model":        "test-model",
		"conversation": conversation.ID,
		"input":        "What is the code? Reply with just the number.",
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_object",
			},
		},
	})
	require.Equal(t, conversation.ID, response.Conversation)
	require.Equal(t, "777", response.OutputText)
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

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)

	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", item["type"])
	require.Equal(t, "add", item["name"])
	require.Equal(t, `{"a":1,"b":2}`, item["arguments"])
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

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)

	got := getResponse(t, app, responseID)
	require.Equal(t, responseID, got.ID)
	require.Len(t, got.Output, 1)
	require.Equal(t, "custom_tool_call", got.Output[0].Type)
	require.Equal(t, "code_exec", got.Output[0].Name())
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

func TestResponsesStreamDowngradesSafeExecCommandEscalation(t *testing.T) {
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
	require.NotContains(t, asStringAny(item["arguments"]), "require_escalated")
	require.NotContains(t, asStringAny(done["arguments"]), "require_escalated")
	require.Contains(t, asStringAny(item["arguments"]), `"workdir":"/tmp/snake_test"`)
	require.Contains(t, asStringAny(item["arguments"]), `"cmd":"go test ./game -v"`)
	require.Contains(t, asStringAny(item["arguments"]), `"yield_time_ms":30000`)
}

func TestResponsesStreamDropsCompletedPlanLoopAndShowsSummary(t *testing.T) {
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
	require.Contains(t, eventTypes(events), "response.output_text.done")
	require.NotContains(t, eventTypes(events), "response.function_call_arguments.done")

	completed := findEvent(t, events, "response.completed").Data
	responsePayload, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	responseID := asStringAny(responsePayload["id"])
	require.NotEmpty(t, responseID)
	require.Equal(t, "All tasks are complete.", asStringAny(responsePayload["output_text"]))

	got := getResponse(t, app, responseID)
	require.Equal(t, "All tasks are complete.", got.OutputText)
	require.Len(t, got.Output, 1)
	require.Equal(t, "message", got.Output[0].Type)
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

	require.Equal(t, first.ID, second.PreviousResponseID)
	require.Equal(t, "tool says hi", second.OutputText)

	inputItems := getResponseInputItems(t, app, second.ID)
	require.Len(t, inputItems.Data, 1)
	require.Equal(t, "custom_tool_call_output", asStringAny(inputItems.Data[0]["type"]))
	outputParts, ok := inputItems.Data[0]["output"].([]any)
	require.True(t, ok)
	require.Len(t, outputParts, 1)
	firstPart, ok := outputParts[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool says hi", firstPart["text"])
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

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "shell", item["namespace"])
	require.Equal(t, "exec", item["name"])
}

func TestResponsesBridgeRejectsGrammarCustomTools(t *testing.T) {
	app := testutil.NewTestApp(t)

	status, body := rawRequest(t, app, http.MethodPost, "/v1/responses", map[string]any{
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
	require.Equal(t, "invalid_request_error", body["error"].(map[string]any)["type"])
}

func TestResponsesAutoPassthroughsGrammarCustomTools(t *testing.T) {
	app := testutil.NewTestAppWithCustomToolsMode(t, "auto")

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

	output, ok := body["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", item["type"])
	require.Equal(t, "shell", item["namespace"])
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
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, app.Server.URL+"/v1/responses/"+id+"/input_items", nil)
	require.NoError(t, err)

	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var items conversationItemsListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&items))
	return items
}

func postConversation(t *testing.T, app *testutil.TestApp, payload map[string]any) domain.Conversation {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodPost, "/v1/conversations", payload)
	require.Equal(t, http.StatusOK, status)

	var conversation domain.Conversation
	mustDecode(t, body, &conversation)
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

func seedConversationWithResponse(t *testing.T, app *testutil.TestApp) domain.Conversation {
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

func conversationItemTexts(items conversationItemsListResponse) []string {
	out := make([]string, 0, len(items.Data))
	for _, item := range items.Data {
		raw, err := json.Marshal(item)
		if err != nil {
			out = append(out, "")
			continue
		}
		decoded, err := domain.NewItem(raw)
		if err != nil {
			out = append(out, "")
			continue
		}
		out = append(out, domain.MessageText(decoded))
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
