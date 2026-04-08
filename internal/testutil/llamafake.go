package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeLlamaRequest struct {
	Model    string             `json:"model"`
	Messages []fakeLlamaMessage `json:"messages"`
	Stream   bool               `json:"stream"`
}

type fakeLlamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func NewFakeLlamaServer(t *testing.T) *httptest.Server {
	t.Helper()

	var (
		mu        sync.Mutex
		nextID    int
		responses = map[string]map[string]any{}
	)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))

			model, _ := request["model"].(string)
			if response, statusCode, ok := buildFakeWrappedValidationErrorResponse(request); ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				require.NoError(t, json.NewEncoder(w).Encode(response))
				return
			}
			mu.Lock()
			nextID++
			id := "upstream_resp_" + strconv.Itoa(nextID)
			mu.Unlock()

			if response, statusCode, kind, ok := buildFakeResponseForTools(id, model, request); ok {
				if statusCode != http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(statusCode)
					require.NoError(t, json.NewEncoder(w).Encode(response))
					return
				}

				mu.Lock()
				responses[id] = response
				mu.Unlock()

				if stream, _ := request["stream"].(bool); stream {
					switch kind {
					case "function_call":
						if useCompletedOnlyToolCallResponsesStream(request) {
							writeFakeToolCallCompletedOnlyResponsesStream(t, w, response)
						} else {
							writeFakeToolCallResponsesStream(t, w, response, false)
						}
					case "custom_tool_call":
						if useCompletedOnlyToolCallResponsesStream(request) {
							writeFakeToolCallCompletedOnlyResponsesStream(t, w, response)
						} else {
							writeFakeToolCallResponsesStream(t, w, response, true)
						}
					default:
						writeFakeResponsesStream(t, w, response, asString(response["output_text"]))
					}
					return
				}

				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(response))
				return
			}

			output := fakeResponseOutput(request["input"])
			response := buildFakeResponse(id, model, output)

			mu.Lock()
			responses[id] = response
			mu.Unlock()

			if stream, _ := request["stream"].(bool); stream {
				if useDeltaOnlyResponsesStream(request) {
					writeFakeResponsesDeltaOnlyStream(t, w, response, asString(response["output_text"]))
					return
				}
				writeFakeResponsesStream(t, w, response, output)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(response))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/responses/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/responses/")
			w.Header().Set("Content-Type", "application/json")

			mu.Lock()
			response, ok := responses[id]
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"type":    "not_found_error",
						"message": "response not found",
					},
				}))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(response))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":       "test-model",
						"object":   "model",
						"owned_by": "organization_owner",
					},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var request fakeLlamaRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))

			output := fakeLlamaOutput(request.Messages)
			if request.Stream {
				writeFakeChatCompletionStream(t, w, output)
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": output,
						},
					},
				},
			}))
		case r.URL.Path == "/v1/echo":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"method": r.Method,
				"path":   r.URL.Path,
				"query":  r.URL.RawQuery,
				"body":   string(bytes.TrimSpace(body)),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sse":
			writeFakeSSE(t, w)
		default:
			http.NotFound(w, r)
		}
	}))
}

func fakeLlamaOutput(messages []fakeLlamaMessage) string {
	if len(messages) == 0 {
		return "EMPTY"
	}

	last := strings.ToLower(messages[len(messages)-1].Content)
	joined := strings.ToLower(joinMessageContent(messages))

	switch {
	case strings.Contains(last, "what was my code") && strings.Contains(joined, "my code = 123"):
		return "123"
	case strings.Contains(last, "what is the code") && strings.Contains(joined, "code=777"):
		return "777"
	case strings.Contains(last, "say ok and nothing else"):
		return "OK"
	case strings.Contains(last, "reply ok"):
		return "OK"
	default:
		return "UNHANDLED"
	}
}

func joinMessageContent(messages []fakeLlamaMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func fakeResponseOutput(input any) string {
	switch value := input.(type) {
	case string:
		if strings.Contains(strings.ToLower(value), "delta only stream") {
			return "DELTA_ONLY_STREAM_OK"
		}
		if strings.Contains(strings.ToLower(value), "say ok") {
			return "OK"
		}
		return "UPSTREAM"
	case []any:
		joined := strings.ToLower(marshalAny(value))
		switch {
		case strings.Contains(joined, "reply with exactly hello"):
			return "HELLO"
		case strings.Contains(joined, "what was my code") && strings.Contains(joined, "my code = 123"):
			return "123"
		case strings.Contains(joined, "what is the code") && strings.Contains(joined, "code=777"):
			return "777"
		default:
			return "UPSTREAM"
		}
	default:
		return "UPSTREAM"
	}
}

func marshalAny(value any) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func buildFakeResponse(id, model, output string) map[string]any {
	return map[string]any{
		"id":          id,
		"object":      "response",
		"model":       model,
		"output_text": output,
		"output": []map[string]any{
			{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": output},
				},
			},
		},
	}
}

func buildFakeWrappedValidationErrorResponse(request map[string]any) (map[string]any, int, bool) {
	inputValue, ok := request["input"].(float64)
	if !ok || inputValue != 1 {
		return nil, 0, false
	}

	return map[string]any{
		"error": map[string]any{
			"message": `litellm.BadRequestError: OpenAIException - {"error":{"message":"Input should be a valid string","type":"Bad Request","param":null,"code":400}}. Received Model Group=test-model`,
			"type":    nil,
			"param":   nil,
			"code":    "400",
		},
	}, http.StatusBadRequest, true
}

func buildFakeResponseForTools(id, model string, request map[string]any) (map[string]any, int, string, bool) {
	if requestHasToolOutput(request["input"]) {
		return buildFakeResponse(id, model, fakeToolOutputReply(request["input"])), http.StatusOK, "message", true
	}

	joinedInput := strings.ToLower(marshalAny(request["input"]))
	if strings.Contains(joinedInput, "auto-only tool_choice backend") && !isAutoToolChoice(request["tool_choice"]) {
		return map[string]any{
			"error": map[string]any{
				"type":    "server_error",
				"message": "Only 'auto' tool_choice is supported in response API with Harmony",
			},
		}, http.StatusNotImplemented, "", true
	}
	if strings.Contains(joinedInput, "auto-only tool_choice backend returns text") && isAutoToolChoice(request["tool_choice"]) {
		return buildFakeResponse(id, model, "AUTO_FALLBACK_TEXT"), http.StatusOK, "message", true
	}

	tools, ok := request["tools"].([]any)
	if !ok || len(tools) == 0 {
		return nil, 0, "", false
	}
	if toolType := firstUnsupportedToolType(tools); toolType != "" {
		return map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "'type' of tool must be 'function'",
				"param":   "tools",
				"tool":    toolType,
			},
		}, http.StatusBadRequest, "", true
	}

	firstTool, ok := tools[0].(map[string]any)
	if !ok {
		return nil, 0, "", false
	}

	switch strings.TrimSpace(asString(firstTool["type"])) {
	case "custom":
		name := strings.TrimSpace(asString(firstTool["name"]))
		if name == "" {
			name = "tool"
		}
		namespace := strings.TrimSpace(asString(firstTool["namespace"]))
		return buildFakeCustomToolCallResponse(id, model, namespace, name, fakeCustomToolInput(name)), http.StatusOK, "custom_tool_call", true
	case "function":
		name := strings.TrimSpace(asString(firstTool["name"]))
		if name == "" {
			name = "tool"
		}
		if name == "update_plan" && strings.Contains(strings.ToLower(marshalAny(request["input"])), "completed plan reasoning stream") {
			return buildFakeCompletedPlanLoopResponse(id, model), http.StatusOK, "function_call", true
		}
		return buildFakeFunctionToolCallResponse(id, model, name, fakeToolArguments(name)), http.StatusOK, "function_call", true
	default:
		return nil, 0, "", false
	}
}

func firstUnsupportedToolType(tools []any) string {
	for _, tool := range tools {
		payload, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(payload["type"])) {
		case "function", "custom", "custom_tool":
			continue
		case "":
			continue
		default:
			return asString(payload["type"])
		}
	}
	return ""
}

func buildFakeFunctionToolCallResponse(id, model, name, arguments string) map[string]any {
	return map[string]any{
		"id":          id,
		"object":      "response",
		"model":       model,
		"output_text": "",
		"output": []map[string]any{
			{
				"id":        "fc_" + id,
				"type":      "function_call",
				"call_id":   "call_" + id,
				"name":      name,
				"arguments": arguments,
				"status":    "completed",
			},
		},
	}
}

func buildFakeCustomToolCallResponse(id, model, namespace, name, input string) map[string]any {
	item := map[string]any{
		"id":      "ctc_" + id,
		"type":    "custom_tool_call",
		"call_id": "call_" + id,
		"name":    name,
		"input":   input,
		"status":  "completed",
	}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return map[string]any{
		"id":          id,
		"object":      "response",
		"model":       model,
		"output_text": "",
		"output":      []map[string]any{item},
	}
}

func buildFakeCompletedPlanLoopResponse(id, model string) map[string]any {
	return map[string]any{
		"id":          id,
		"object":      "response",
		"model":       model,
		"output_text": "",
		"output": []map[string]any{
			{
				"id":     "rs_" + id,
				"type":   "reasoning",
				"status": "completed",
				"content": []map[string]any{
					{
						"type": "reasoning_text",
						"text": "All tasks are complete. Let me provide a summary to the user.",
					},
				},
			},
			{
				"id":        "fc_" + id,
				"type":      "function_call",
				"call_id":   "call_" + id,
				"name":      "update_plan",
				"arguments": `{"plan":[{"status":"completed","step":"done"}]}`,
				"status":    "completed",
			},
		},
	}
}

func fakeToolArguments(name string) string {
	switch name {
	case "code_exec":
		return `{"input":"print(\"hello world\")"}`
	case "exec_command":
		return `{"cmd":"cd /tmp/snake_test && go test ./game -v 2>&1","sandbox_permissions":"require_escalated","justification":"Need approval to run tests"}`
	case "add":
		return `{"a":1,"b":2}`
	default:
		return `{"input":"tool input"}`
	}
}

func fakeCustomToolInput(name string) string {
	switch name {
	case "code_exec":
		return `print("hello world")`
	default:
		return "tool input"
	}
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func isAutoToolChoice(value any) bool {
	text, ok := value.(string)
	return ok && strings.EqualFold(strings.TrimSpace(text), "auto")
}

func writeFakeChatCompletionStream(t *testing.T, w http.ResponseWriter, output string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)

	for _, chunk := range chunkString(output, 1) {
		require.NoError(t, writeSSEData(w, map[string]any{
			"choices": []map[string]any{
				{
					"delta": map[string]any{
						"content": chunk,
					},
				},
			},
		}))
		flusher.Flush()
		time.Sleep(120 * time.Millisecond)
	}

	require.NoError(t, writeSSEData(w, map[string]any{
		"choices": []map[string]any{
			{
				"delta":         map[string]any{},
				"finish_reason": "stop",
			},
		},
	}))
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
	flusher.Flush()
}

func writeFakeResponsesStream(t *testing.T, w http.ResponseWriter, response map[string]any, output string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)

	require.NoError(t, writeNamedSSEData(w, "response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": 1,
		"response": map[string]any{
			"id":          response["id"],
			"object":      "response",
			"model":       response["model"],
			"output_text": "",
			"output":      nil,
		},
	}))
	flusher.Flush()

	sequence := 2
	for _, chunk := range chunkString(output, 1) {
		require.NoError(t, writeNamedSSEData(w, "response.output_text.delta", map[string]any{
			"type":            "response.output_text.delta",
			"sequence_number": sequence,
			"output_index":    0,
			"content_index":   0,
			"delta":           chunk,
		}))
		sequence++
		flusher.Flush()
		time.Sleep(120 * time.Millisecond)
	}

	require.NoError(t, writeNamedSSEData(w, "response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": sequence,
		"response":        response,
	}))
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
	flusher.Flush()
}

func writeFakeResponsesDeltaOnlyStream(t *testing.T, w http.ResponseWriter, response map[string]any, output string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)

	for _, chunk := range chunkString(output, 1) {
		require.NoError(t, writeNamedSSEData(w, "response.output_text.delta", map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       response["id"],
			"model":         response["model"],
			"output_index":  0,
			"content_index": 0,
			"delta":         chunk,
		}))
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
	flusher.Flush()
}

func useDeltaOnlyResponsesStream(request map[string]any) bool {
	input, ok := request["input"]
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(marshalAny(input)), "delta only stream")
}

func useCompletedOnlyToolCallResponsesStream(request map[string]any) bool {
	input, ok := request["input"]
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(marshalAny(input)), "completed only tool stream")
}

func writeFakeToolCallResponsesStream(t *testing.T, w http.ResponseWriter, response map[string]any, nativeCustom bool) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)

	output, ok := response["output"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item := output[0]
	itemID := asString(item["id"])

	require.NoError(t, writeNamedSSEData(w, "response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": 1,
		"response": map[string]any{
			"id":          response["id"],
			"object":      "response",
			"model":       response["model"],
			"output_text": "",
			"output":      nil,
		},
	}))
	flusher.Flush()

	addedItem := cloneMap(item)
	if nativeCustom {
		addedItem["input"] = ""
	} else {
		addedItem["arguments"] = ""
	}
	addedItem["status"] = "in_progress"
	require.NoError(t, writeNamedSSEData(w, "response.output_item.added", map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": 2,
		"output_index":    0,
		"item":            addedItem,
	}))
	flusher.Flush()

	value := asString(item["arguments"])
	deltaEvent := "response.function_call_arguments.delta"
	doneEvent := "response.function_call_arguments.done"
	doneField := "arguments"
	if nativeCustom {
		value = asString(item["input"])
		deltaEvent = "response.custom_tool_call_input.delta"
		doneEvent = "response.custom_tool_call_input.done"
		doneField = "input"
	}

	sequence := 3
	for _, chunk := range chunkString(value, 4) {
		require.NoError(t, writeNamedSSEData(w, deltaEvent, map[string]any{
			"type":            deltaEvent,
			"sequence_number": sequence,
			"response_id":     response["id"],
			"item_id":         itemID,
			"output_index":    0,
			"delta":           chunk,
		}))
		sequence++
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	donePayload := map[string]any{
		"type":            doneEvent,
		"sequence_number": sequence,
		"response_id":     response["id"],
		"item_id":         itemID,
		"output_index":    0,
		"item":            item,
	}
	donePayload[doneField] = value
	require.NoError(t, writeNamedSSEData(w, doneEvent, donePayload))
	sequence++
	flusher.Flush()

	require.NoError(t, writeNamedSSEData(w, "response.output_item.done", map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": sequence,
		"output_index":    0,
		"item":            item,
	}))
	sequence++
	flusher.Flush()

	require.NoError(t, writeNamedSSEData(w, "response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": sequence,
		"response":        response,
	}))
	flusher.Flush()
	time.Sleep(20 * time.Millisecond)
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
	flusher.Flush()
}

func writeFakeToolCallCompletedOnlyResponsesStream(t *testing.T, w http.ResponseWriter, response map[string]any) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)

	response = cloneResponseWithoutToolItemIDs(response)
	require.NoError(t, writeNamedSSEData(w, "response.completed", map[string]any{
		"type":     "response.completed",
		"response": response,
	}))
	flusher.Flush()
	time.Sleep(20 * time.Millisecond)
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
	flusher.Flush()
}

func requestHasToolOutput(input any) bool {
	items, ok := input.([]any)
	if !ok {
		return false
	}
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "function_call_output", "custom_tool_call_output":
			return true
		}
	}
	return false
}

func fakeToolOutputReply(input any) string {
	items, ok := input.([]any)
	if !ok {
		return "TOOL_OUTPUT_OK"
	}
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "function_call_output", "custom_tool_call_output":
			switch output := item["output"].(type) {
			case string:
				return output
			case []any:
				var builder strings.Builder
				for _, rawPart := range output {
					part, ok := rawPart.(map[string]any)
					if !ok {
						continue
					}
					builder.WriteString(asString(part["text"]))
				}
				if builder.Len() > 0 {
					return builder.String()
				}
			}
		}
	}
	return "TOOL_OUTPUT_OK"
}

func cloneMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneResponseWithoutToolItemIDs(response map[string]any) map[string]any {
	cloned := cloneMap(response)
	rawOutput, ok := response["output"].([]map[string]any)
	if !ok {
		return cloned
	}

	output := make([]map[string]any, 0, len(rawOutput))
	for _, item := range rawOutput {
		itemClone := cloneMap(item)
		switch strings.TrimSpace(asString(itemClone["type"])) {
		case "function_call", "custom_tool_call":
			delete(itemClone, "id")
		}
		output = append(output, itemClone)
	}
	cloned["output"] = output
	return cloned
}

func writeFakeSSE(t *testing.T, w http.ResponseWriter) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)

	for i := 1; i <= 3; i++ {
		require.NoError(t, writeSSEData(w, map[string]any{
			"type":  "proxy.test",
			"index": i,
		}))
		flusher.Flush()
		time.Sleep(120 * time.Millisecond)
	}
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
	flusher.Flush()
}

func writeSSEData(w io.Writer, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "data: "+string(body)+"\n\n")
	return err
}

func writeNamedSSEData(w io.Writer, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "event: "+event+"\n"); err != nil {
		return err
	}
	_, err = io.WriteString(w, "data: "+string(body)+"\n\n")
	return err
}

func chunkString(value string, size int) []string {
	if value == "" {
		return nil
	}

	runes := []rune(value)
	chunks := make([]string, 0, (len(runes)+size-1)/size)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}
