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

func postConversation(t *testing.T, app *testutil.TestApp, payload map[string]any) domain.Conversation {
	t.Helper()

	status, body := rawRequest(t, app, http.MethodPost, "/v1/conversations", payload)
	require.Equal(t, http.StatusOK, status)

	var conversation domain.Conversation
	mustDecode(t, body, &conversation)
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
