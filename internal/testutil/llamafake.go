package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
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

type fakeStoredChatCompletion struct {
	Request  map[string]any
	Response map[string]any
}

func NewFakeLlamaServer(t *testing.T) *httptest.Server {
	t.Helper()

	var (
		mu              sync.Mutex
		nextID          int
		responses       = map[string]map[string]any{}
		chatCompletions = map[string]fakeStoredChatCompletion{}
	)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses/input_tokens":
			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object":       "response.input_tokens",
				"input_tokens": fakeInputTokenCount(request),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses/compact":
			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))

			mu.Lock()
			nextID++
			id := "upstream_compact_" + strconv.Itoa(nextID)
			compactionID := "upstream_cmp_" + strconv.Itoa(nextID)
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":         id,
				"object":     "response.compaction",
				"created_at": time.Now().Unix(),
				"output": []map[string]any{
					{
						"id":                compactionID,
						"type":              "compaction",
						"encrypted_content": "upstream-opaque-compaction",
					},
				},
				"usage": map[string]any{
					"input_tokens":  fakeInputTokenCount(request),
					"output_tokens": 4,
					"total_tokens":  fakeInputTokenCount(request) + 4,
				},
			}))
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
			response := buildFakeResponse(id, model, output, request)

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
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/responses/") && !strings.HasSuffix(r.URL.Path, "/cancel") && !strings.HasSuffix(r.URL.Path, "/input_items"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/responses/")
			w.Header().Set("Content-Type", "application/json")

			mu.Lock()
			_, ok := responses[id]
			if ok {
				delete(responses, id)
			}
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
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":      id,
				"object":  "response",
				"deleted": true,
			}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel") && strings.HasPrefix(r.URL.Path, "/v1/responses/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/responses/"), "/cancel")
			w.Header().Set("Content-Type", "application/json")

			mu.Lock()
			response, ok := responses[id]
			if ok {
				response = cloneMap(response)
				response["status"] = "cancelled"
				response["completed_at"] = nil
				responses[id] = response
			}
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			mu.Lock()
			page, statusCode, payload := buildFakeStoredChatCompletionsList(chatCompletions, r.URL.Query())
			mu.Unlock()
			if statusCode != http.StatusOK {
				w.WriteHeader(statusCode)
				require.NoError(t, json.NewEncoder(w).Encode(payload))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(page))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))

			model, _ := request["model"].(string)
			if response, ok := buildFakeChatCompletionToolResponse(request); ok {
				mu.Lock()
				nextID++
				id := "chatcmpl_" + strconv.Itoa(nextID)
				completion := buildFakeChatCompletionResponse(id, model, request, response["choices"])
				if shouldStoreFakeChatCompletion(request) {
					chatCompletions[id] = fakeStoredChatCompletion{
						Request:  cloneJSONObject(request),
						Response: cloneJSONObject(completion),
					}
				}
				mu.Unlock()

				if stream, _ := request["stream"].(bool); stream {
					if toolCalls := extractFakeChatCompletionToolCalls(response["choices"]); len(toolCalls) > 0 {
						writeFakeChatCompletionToolStream(t, w, id, model, toolCalls)
						return
					}
					writeFakeChatCompletionStream(t, w, id, model, extractFakeChatCompletionAssistantContent(response["choices"]))
					return
				}

				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(completion))
				return
			}

			output := fakeLlamaOutputFromChatMessages(chatMessagesFromRequest(request["messages"]))
			if stream, _ := request["stream"].(bool); stream {
				mu.Lock()
				nextID++
				id := "chatcmpl_" + strconv.Itoa(nextID)
				mu.Unlock()
				writeFakeChatCompletionStream(t, w, id, model, output)
				return
			}
			mu.Lock()
			nextID++
			id := "chatcmpl_" + strconv.Itoa(nextID)
			completion := buildFakeChatCompletionResponse(id, model, request, []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":          "assistant",
						"content":       output,
						"tool_calls":    nil,
						"function_call": nil,
					},
					"finish_reason": "stop",
					"logprobs":      nil,
				},
			})
			if shouldStoreFakeChatCompletion(request) {
				chatCompletions[id] = fakeStoredChatCompletion{
					Request:  cloneJSONObject(request),
					Response: cloneJSONObject(completion),
				}
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(completion))
		case strings.HasPrefix(r.URL.Path, "/v1/chat/completions/"):
			handleFakeStoredChatCompletionRoute(t, w, r, chatCompletions, &mu)
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

func buildFakeChatCompletionResponse(id, model string, request map[string]any, choices any) map[string]any {
	metadata, ok := request["metadata"].(map[string]any)
	if !ok || metadata == nil {
		metadata = map[string]any{}
	}
	return map[string]any{
		"id":                 id,
		"object":             "chat.completion",
		"created":            time.Now().Unix(),
		"model":              model,
		"choices":            choices,
		"usage":              map[string]any{"prompt_tokens": 13, "completion_tokens": 18, "total_tokens": 31},
		"service_tier":       "default",
		"tool_choice":        request["tool_choice"],
		"temperature":        1.0,
		"top_p":              1.0,
		"presence_penalty":   0.0,
		"frequency_penalty":  0.0,
		"metadata":           metadata,
		"tools":              request["tools"],
		"response_format":    request["response_format"],
		"input_user":         request["user"],
		"request_id":         "req_fake_chat_completion",
		"system_fingerprint": "fp_fake_chat_completion",
		"seed":               1,
	}
}

func shouldStoreFakeChatCompletion(request map[string]any) bool {
	if rawStore, ok := request["store"]; ok {
		store, ok := rawStore.(bool)
		return ok && store
	}
	return true
}

func extractFakeChatCompletionAssistantContent(choices any) string {
	rawChoices, ok := choices.([]map[string]any)
	if !ok || len(rawChoices) == 0 {
		return ""
	}
	message, ok := rawChoices[0]["message"].(map[string]any)
	if !ok {
		return ""
	}
	return asString(message["content"])
}

func extractFakeChatCompletionToolCalls(choices any) []map[string]any {
	rawChoices, ok := choices.([]map[string]any)
	if !ok || len(rawChoices) == 0 {
		return nil
	}
	message, ok := rawChoices[0]["message"].(map[string]any)
	if !ok {
		return nil
	}
	rawToolCalls, ok := message["tool_calls"].([]map[string]any)
	if ok {
		return rawToolCalls
	}
	typed, ok := message["tool_calls"].([]any)
	if !ok {
		return nil
	}
	toolCalls := make([]map[string]any, 0, len(typed))
	for _, rawToolCall := range typed {
		toolCall, ok := rawToolCall.(map[string]any)
		if ok {
			toolCalls = append(toolCalls, toolCall)
		}
	}
	return toolCalls
}

func fakeInputTokenCount(request map[string]any) int {
	raw, err := json.Marshal(request["input"])
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0
	}
	return max(1, len([]rune(trimmed))/4+1)
}

func buildFakeChatCompletionToolResponse(request map[string]any) (map[string]any, bool) {
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) == 0 {
		return nil, false
	}

	messages := chatMessagesFromRequest(request["messages"])
	if output, ok := fakeToolOutputReplyFromChatMessages(messages); ok {
		return map[string]any{
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": output,
					},
					"finish_reason": "stop",
					"logprobs":      nil,
				},
			},
		}, true
	}

	firstTool, ok := tools[0].(map[string]any)
	if !ok {
		return nil, false
	}
	function, ok := firstTool["function"].(map[string]any)
	if !ok {
		return nil, false
	}
	name := strings.TrimSpace(asString(function["name"]))
	if name == "" {
		name = "tool"
	}

	joined := strings.ToLower(joinChatMessageContent(messages))
	arguments := fakeToolArguments(name)
	switch {
	case name == "math_exp" && strings.Contains(joined, "always invalid grammar tool"):
		arguments = `{"input":"4+4"}`
	case name == "math_exp" && strings.Contains(joined, "invalid grammar first attempt") && !hasConstrainedCustomToolRepairPrompt(messages):
		arguments = `{"input":"4+4"}`
	}
	message := map[string]any{
		"role":    "assistant",
		"content": nil,
		"tool_calls": []map[string]any{
			{
				"id":   "call_chat_1",
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": arguments,
				},
			},
		},
	}
	if name == "update_plan" && strings.Contains(joined, "completed plan reasoning stream") {
		message["content"] = "All tasks are complete. Let me provide a summary to the user."
	}

	return map[string]any{
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       message,
				"finish_reason": "tool_calls",
				"logprobs":      nil,
			},
		},
	}, true
}

func hasConstrainedCustomToolRepairPrompt(messages []map[string]any) bool {
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(asString(message["role"])), "system") {
			continue
		}
		content := strings.ToLower(chatMessageContent(message))
		if strings.Contains(content, "previous attempt for custom tool") &&
			strings.Contains(content, "produced invalid raw input") {
			return true
		}
	}
	return false
}

func chatMessagesFromRequest(value any) []map[string]any {
	rawMessages, ok := value.([]any)
	if !ok {
		return nil
	}
	messages := make([]map[string]any, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		messages = append(messages, message)
	}
	return messages
}

func fakeLlamaOutputFromChatMessages(messages []map[string]any) string {
	if len(messages) == 0 {
		return "EMPTY"
	}
	if output, ok := fakeToolOutputReplyFromChatMessages(messages); ok {
		return output
	}

	last := strings.ToLower(chatMessageContent(messages[len(messages)-1]))
	joined := strings.ToLower(joinChatMessageContent(messages))
	if output, ok := fakeConstrainedCustomToolJSONOutput(joined); ok {
		return output
	}
	if output, ok := fakeLocalCodeInterpreterPlannerOutput(joined); ok {
		return output
	}
	if output, ok := fakeLocalCodeInterpreterFinalOutput(last, joined); ok {
		return output
	}
	if output, ok := fakeStructuredJSONOutput(last, joined); ok {
		return output
	}

	switch {
	case strings.Contains(last, "what was my code") && strings.Contains(joined, "my code = 123"):
		return "123"
	case strings.Contains(last, "what is the code") && hasFakeCode777Context(joined):
		return "777"
	case strings.Contains(last, "say ok and nothing else"):
		return "OK"
	case strings.Contains(last, "reply ok"):
		return "OK"
	default:
		return "UNHANDLED"
	}
}

func fakeConstrainedCustomToolJSONOutput(joined string) (string, bool) {
	if !strings.Contains(joined, "shim-local constrained custom tool generator") {
		return "", false
	}
	switch {
	case strings.Contains(joined, "invalid native constrained runtime output") && strings.Contains(joined, "`math_exp`"):
		return `{"input":"4+4"}`, true
	case strings.Contains(joined, "`math_exp`"):
		return `{"input":"4 + 4"}`, true
	case strings.Contains(joined, "`exact_text`"):
		return `{"input":"hello 42"}`, true
	case strings.Contains(joined, "`shell.exec`"):
		return `{"input":"print(\"hello world\")"}`, true
	default:
		return `{"input":"UNHANDLED"}`, true
	}
}

func joinChatMessageContent(messages []map[string]any) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if content := chatMessageContent(message); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func fakeStructuredJSONOutput(last, joined string) (string, bool) {
	switch {
	case strings.Contains(last, `json object {"ok":true}`) || strings.Contains(last, `json object {\"ok\":true}`):
		return `{"ok":true}`, true
	case strings.Contains(last, "json object containing ok=true"):
		return `{"ok":true}`, true
	case strings.Contains(last, "json object containing answer and count"):
		return `{"answer":"OK","count":1}`, true
	case strings.Contains(last, "json object containing code") && hasFakeCode777Context(joined):
		return `{"code":777}`, true
	case strings.Contains(last, "json object containing code") && strings.Contains(joined, "my code = 123"):
		return `{"code":123}`, true
	default:
		return "", false
	}
}

func chatMessageContent(message map[string]any) string {
	if message == nil {
		return ""
	}
	switch content := message["content"].(type) {
	case string:
		return content
	case []any:
		var builder strings.Builder
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			builder.WriteString(asString(part["text"]))
		}
		return builder.String()
	default:
		return ""
	}
}

func fakeToolOutputReplyFromChatMessages(messages []map[string]any) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !strings.EqualFold(strings.TrimSpace(asString(message["role"])), "tool") {
			continue
		}
		return chatMessageContent(message), true
	}
	return "", false
}

func fakeLlamaOutput(messages []fakeLlamaMessage) string {
	if len(messages) == 0 {
		return "EMPTY"
	}

	last := strings.ToLower(messages[len(messages)-1].Content)
	joined := strings.ToLower(joinMessageContent(messages))
	if output, ok := fakeLocalCodeInterpreterPlannerOutput(joined); ok {
		return output
	}
	if output, ok := fakeLocalCodeInterpreterFinalOutput(last, joined); ok {
		return output
	}
	if output, ok := fakeStructuredJSONOutput(last, joined); ok {
		return output
	}

	switch {
	case strings.Contains(last, "what was my code") && strings.Contains(joined, "my code = 123"):
		return "123"
	case strings.Contains(last, "what is the code") && hasFakeCode777Context(joined):
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
		lower := strings.ToLower(value)
		if output, ok := fakeStructuredJSONOutput(lower, lower); ok {
			return output
		}
		if strings.Contains(lower, "delta only stream") {
			return "DELTA_ONLY_STREAM_OK"
		}
		if strings.Contains(lower, "say ok") {
			return "OK"
		}
		return "UPSTREAM"
	case []any:
		joined := strings.ToLower(marshalAny(value))
		if output, ok := fakeStructuredJSONOutput(joined, joined); ok {
			return output
		}
		switch {
		case strings.Contains(joined, "reply with exactly hello"):
			return "HELLO"
		case strings.Contains(joined, "what was my code") && strings.Contains(joined, "my code = 123"):
			return "123"
		case strings.Contains(joined, "what is the code") && hasFakeCode777Context(joined):
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

func hasFakeCode777Context(text string) bool {
	return strings.Contains(text, "code=777") || strings.Contains(text, "code 777")
}

func fakeLocalCodeInterpreterPlannerOutput(joined string) (string, bool) {
	if !strings.Contains(joined, "shim-local code interpreter planner") {
		return "", false
	}

	switch {
	case strings.Contains(joined, "2+2"):
		return `{"use_code_interpreter":true,"code":"print(2+2)"}`, true
	case strings.Contains(joined, "write report.txt") && strings.Contains(joined, "artifact-body"):
		return `{"use_code_interpreter":true,"code":"with open(\"report.txt\", \"w\", encoding=\"utf-8\") as handle:\n    handle.write(\"artifact-body\")\nprint(\"created report.txt\")"}`, true
	case strings.Contains(joined, "write plot.png"):
		return `{"use_code_interpreter":true,"code":"with open(\"plot.png\", \"wb\") as handle:\n    handle.write(b\"fake-png\")\nprint(\"created plot.png\")"}`, true
	case strings.Contains(joined, "uploaded files:") && strings.Contains(joined, "codes.txt") && strings.Contains(joined, "what is the code"):
		return `{"use_code_interpreter":true,"code":"print(open(\"codes.txt\", encoding=\"utf-8\").read())"}`, true
	case strings.Contains(joined, "uploaded files:") && strings.Contains(joined, "codes.txt") && strings.Contains(joined, "read the uploaded file"):
		return `{"use_code_interpreter":true,"code":"print(open(\"codes.txt\", encoding=\"utf-8\").read())"}`, true
	case strings.Contains(joined, "result=2.0"):
		return `{"use_code_interpreter":true,"code":"print(\"result=2.0\")"}`, true
	case strings.Contains(joined, "say ok and nothing else"):
		return `{"use_code_interpreter":false,"code":""}`, true
	default:
		return `{"use_code_interpreter":true,"code":"print(2+2)"}`, true
	}
}

func fakeLocalCodeInterpreterFinalOutput(last, joined string) (string, bool) {
	if !strings.Contains(joined, "shim-local code interpreter already ran for this turn") {
		return "", false
	}

	switch {
	case strings.Contains(joined, "execution logs:\n4"):
		if strings.Contains(last, "json") {
			return `{"result":4}`, true
		}
		return "4", true
	case strings.Contains(joined, "generated files saved by the shim") && strings.Contains(joined, "report.txt"):
		return "Created report.txt.", true
	case strings.Contains(joined, "generated files saved by the shim") && strings.Contains(joined, "plot.png"):
		return "Created plot.png.", true
	case strings.Contains(joined, "runtime/tool error") && strings.Contains(joined, "fixture boom"):
		return `The run failed because the code deliberately raised a RuntimeError with the message "fixture boom."`, true
	case hasFakeCode777Context(joined):
		return "777", true
	case strings.Contains(joined, "execution logs:\nresult=2.0"):
		return "Printed the requested line to stdout.", true
	default:
		return "Execution completed.", true
	}
}

func buildFakeResponse(id, model, output string, request map[string]any) map[string]any {
	text := request["text"]
	if text == nil {
		text = map[string]any{
			"format": map[string]any{
				"type": "text",
			},
		}
	}
	createdAt := time.Now().UTC().Unix()
	store := true
	if value, ok := request["store"].(bool); ok {
		store = value
	}
	background := false
	if value, ok := request["background"].(bool); ok {
		background = value
	}
	metadata := map[string]any{}
	if value, ok := request["metadata"].(map[string]any); ok {
		metadata = value
	}

	response := map[string]any{
		"id":                 id,
		"object":             "response",
		"created_at":         createdAt,
		"status":             "completed",
		"completed_at":       createdAt,
		"error":              nil,
		"incomplete_details": nil,
		"model":              model,
		"background":         background,
		"store":              store,
		"text":               text,
		"usage":              nil,
		"metadata":           metadata,
		"output_text":        output,
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
	if background {
		response["status"] = "in_progress"
		response["completed_at"] = nil
		response["output_text"] = ""
		response["output"] = []any{}
	}
	return response
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
	if shouldRejectStructuredInputArray(request) {
		return map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "426 validation errors:\n  {'type': 'string_type', 'loc': ('body', 'input', 'str'), 'msg': 'Input should be a valid string'}",
				"param":   nil,
				"code":    nil,
			},
		}, http.StatusBadRequest, "", true
	}
	if shouldRejectReplayedTypedInput(request) {
		return map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "637 validation errors:\n  {'type': 'string_type', 'loc': ('body', 'input', 'str'), 'msg': 'Input should be a valid string'}",
				"param":   nil,
				"code":    nil,
			},
		}, http.StatusBadRequest, "", true
	}
	if requestHasToolSearchOutput(request["input"]) {
		return buildFakeToolSearchFollowupFunctionResponse(id, model, request["input"]), http.StatusOK, "function_call", true
	}
	if requestHasToolOutput(request["input"]) {
		return buildFakeResponse(id, model, fakeToolOutputReply(request["input"]), request), http.StatusOK, "message", true
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
		return buildFakeResponse(id, model, "AUTO_FALLBACK_TEXT", request), http.StatusOK, "message", true
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
	if strings.Contains(joinedInput, "backend rejects native custom tools") && requestHasNativeCustomTool(tools) {
		return map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "tool type custom not supported",
			},
		}, http.StatusBadRequest, "", true
	}
	if execution, ok := detectToolSearchExecution(tools); ok {
		switch execution {
		case "client":
			return buildFakeClientToolSearchCallResponse(id, model), http.StatusOK, "message", true
		default:
			return buildFakeHostedToolSearchResponse(id, model, tools), http.StatusOK, "message", true
		}
	}

	firstTool, ok := tools[0].(map[string]any)
	if !ok {
		return nil, 0, "", false
	}

	switch strings.TrimSpace(asString(firstTool["type"])) {
	case "custom", "custom_tool":
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

func shouldRejectStructuredInputArray(request map[string]any) bool {
	items, ok := request["input"].([]any)
	if !ok || len(items) == 0 {
		return false
	}
	return strings.Contains(strings.ToLower(marshalAny(items)), "backend rejects structured input arrays")
}

func shouldRejectReplayedTypedInput(request map[string]any) bool {
	if _, ok := request["previous_response_id"]; ok {
		return false
	}
	items, ok := request["input"].([]any)
	if !ok || len(items) < 2 {
		return false
	}
	return strings.Contains(strings.ToLower(marshalAny(items)), "backend rejects replayed typed input")
}

func firstUnsupportedToolType(tools []any) string {
	for _, tool := range tools {
		payload, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(payload["type"])) {
		case "function", "custom", "custom_tool", "tool_search", "namespace":
			continue
		case "":
			continue
		default:
			return asString(payload["type"])
		}
	}
	return ""
}

func requestHasNativeCustomTool(tools []any) bool {
	for _, tool := range tools {
		payload, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(payload["type"])) {
		case "custom", "custom_tool":
			return true
		}
	}
	return false
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

func buildFakeHostedToolSearchResponse(id, model string, tools []any) map[string]any {
	loadedTool := fakeDeferredToolForToolSearch(tools)
	toolName := strings.TrimSpace(asString(loadedTool["name"]))
	if toolName == "" {
		toolName = "get_shipping_eta"
	}
	return map[string]any{
		"id":          id,
		"object":      "response",
		"model":       model,
		"output_text": "",
		"output": []map[string]any{
			{
				"id":        "tsc_" + id,
				"type":      "tool_search_call",
				"execution": "server",
				"call_id":   nil,
				"status":    "completed",
				"arguments": map[string]any{
					"goal": "Find the shipping ETA tool for order_42.",
				},
			},
			{
				"id":        "tso_" + id,
				"type":      "tool_search_output",
				"execution": "server",
				"call_id":   nil,
				"status":    "completed",
				"tools":     []map[string]any{loadedTool},
			},
			{
				"id":        "fc_" + id,
				"type":      "function_call",
				"call_id":   "call_" + id,
				"name":      toolName,
				"namespace": toolName,
				"arguments": fakeToolArguments(toolName),
				"status":    "completed",
			},
		},
	}
}

func buildFakeClientToolSearchCallResponse(id, model string) map[string]any {
	return map[string]any{
		"id":          id,
		"object":      "response",
		"model":       model,
		"output_text": "",
		"output": []map[string]any{
			{
				"id":        "tsc_" + id,
				"type":      "tool_search_call",
				"execution": "client",
				"call_id":   "call_" + id,
				"status":    "completed",
				"arguments": map[string]any{
					"goal": "Find the shipping ETA tool for order_42.",
				},
			},
		},
	}
}

func buildFakeToolSearchFollowupFunctionResponse(id, model string, input any) map[string]any {
	loadedTool := fakeLoadedToolFromToolSearchOutput(input)
	toolName := strings.TrimSpace(asString(loadedTool["name"]))
	if toolName == "" {
		toolName = "get_shipping_eta"
	}
	return buildFakeFunctionToolCallResponse(id, model, toolName, fakeToolArguments(toolName))
}

func detectToolSearchExecution(tools []any) (string, bool) {
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(tool["type"])) != "tool_search" {
			continue
		}
		execution := strings.ToLower(strings.TrimSpace(asString(tool["execution"])))
		if execution == "client" {
			return "client", true
		}
		return "server", true
	}
	return "", false
}

func fakeDeferredToolForToolSearch(tools []any) map[string]any {
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(tool["type"])) {
		case "function":
			if deferLoading, ok := tool["defer_loading"].(bool); ok && deferLoading {
				return cloneMap(tool)
			}
		case "namespace":
			if nested := firstNamespaceDeferredTool(tool); len(nested) > 0 {
				return nested
			}
		}
	}
	return map[string]any{
		"type":          "function",
		"name":          "get_shipping_eta",
		"description":   "Look up shipping ETA details for an order.",
		"defer_loading": true,
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{"type": "string"},
			},
			"required":             []any{"order_id"},
			"additionalProperties": false,
		},
	}
}

func firstNamespaceDeferredTool(tool map[string]any) map[string]any {
	rawTools, ok := tool["tools"].([]any)
	if !ok {
		return nil
	}
	for _, rawNested := range rawTools {
		nested, ok := rawNested.(map[string]any)
		if !ok {
			continue
		}
		cloned := cloneMap(nested)
		if strings.TrimSpace(asString(cloned["type"])) == "" {
			cloned["type"] = "function"
		}
		if strings.TrimSpace(asString(cloned["name"])) != "" {
			return cloned
		}
	}
	return nil
}

func fakeLoadedToolFromToolSearchOutput(input any) map[string]any {
	items, ok := input.([]any)
	if !ok {
		return fakeDeferredToolForToolSearch(nil)
	}
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok || strings.TrimSpace(asString(item["type"])) != "tool_search_output" {
			continue
		}
		tools, ok := item["tools"].([]any)
		if !ok {
			continue
		}
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(asString(tool["name"])) == "" {
				continue
			}
			return cloneMap(tool)
		}
	}
	return fakeDeferredToolForToolSearch(nil)
}

func fakeToolArguments(name string) string {
	switch name {
	case "code_exec":
		return `{"input":"print(\"hello world\")"}`
	case "math_exp":
		return `{"input":"4 + 4"}`
	case "get_shipping_eta":
		return `{"order_id":"order_42"}`
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
	case "math_exp":
		return "4 + 4"
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

func writeFakeChatCompletionStream(t *testing.T, w http.ResponseWriter, id string, model string, output string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)
	created := time.Now().Unix()

	for i, chunk := range chunkString(output, 1) {
		delta := map[string]any{
			"content": chunk,
		}
		if i == 0 {
			delta["role"] = "assistant"
		}
		require.NoError(t, writeSSEData(w, map[string]any{
			"id":                 id,
			"object":             "chat.completion.chunk",
			"created":            created,
			"model":              model,
			"system_fingerprint": "fp_fake_chat_completion",
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": delta,
				},
			},
		}))
		flusher.Flush()
		time.Sleep(120 * time.Millisecond)
	}

	require.NoError(t, writeSSEData(w, map[string]any{
		"id":                 id,
		"object":             "chat.completion.chunk",
		"created":            created,
		"model":              model,
		"system_fingerprint": "fp_fake_chat_completion",
		"choices": []map[string]any{
			{
				"index":         0,
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

func writeFakeChatCompletionToolStream(t *testing.T, w http.ResponseWriter, id string, model string, toolCalls []map[string]any) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok)
	created := time.Now().Unix()

	delta := map[string]any{
		"role": "assistant",
	}
	streamToolCalls := make([]map[string]any, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		function, _ := toolCall["function"].(map[string]any)
		arguments := asString(function["arguments"])
		midpoint := len(arguments) / 2
		if midpoint <= 0 {
			midpoint = len(arguments)
		}
		streamToolCalls = append(streamToolCalls, map[string]any{
			"index": index,
			"id":    asString(toolCall["id"]),
			"type":  fakeToolCallType(toolCall),
			"function": map[string]any{
				"name":      asString(function["name"]),
				"arguments": arguments[:midpoint],
			},
		})
	}
	delta["tool_calls"] = streamToolCalls
	require.NoError(t, writeSSEData(w, map[string]any{
		"id":                 id,
		"object":             "chat.completion.chunk",
		"created":            created,
		"model":              model,
		"system_fingerprint": "fp_fake_chat_completion",
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": delta,
			},
		},
	}))
	flusher.Flush()
	time.Sleep(120 * time.Millisecond)

	continuation := make([]map[string]any, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		function, _ := toolCall["function"].(map[string]any)
		arguments := asString(function["arguments"])
		midpoint := len(arguments) / 2
		if midpoint <= 0 {
			midpoint = len(arguments)
		}
		if midpoint >= len(arguments) {
			continue
		}
		continuation = append(continuation, map[string]any{
			"index": index,
			"function": map[string]any{
				"arguments": arguments[midpoint:],
			},
		})
	}
	if len(continuation) > 0 {
		require.NoError(t, writeSSEData(w, map[string]any{
			"id":                 id,
			"object":             "chat.completion.chunk",
			"created":            created,
			"model":              model,
			"system_fingerprint": "fp_fake_chat_completion",
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": continuation,
					},
				},
			},
		}))
		flusher.Flush()
		time.Sleep(120 * time.Millisecond)
	}

	require.NoError(t, writeSSEData(w, map[string]any{
		"id":                 id,
		"object":             "chat.completion.chunk",
		"created":            created,
		"model":              model,
		"system_fingerprint": "fp_fake_chat_completion",
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "tool_calls",
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

	created := cloneMap(response)
	created["status"] = "in_progress"
	created["completed_at"] = nil
	created["output_text"] = ""
	created["output"] = nil
	require.NoError(t, writeNamedSSEData(w, "response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": 1,
		"response":        created,
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

func requestHasToolSearchOutput(input any) bool {
	items, ok := input.([]any)
	if !ok {
		return false
	}
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(item["type"])) == "tool_search_output" {
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

func cloneJSONObject(src map[string]any) map[string]any {
	raw, err := json.Marshal(src)
	if err != nil {
		return cloneMap(src)
	}
	var dst map[string]any
	if err := json.Unmarshal(raw, &dst); err != nil {
		return cloneMap(src)
	}
	return dst
}

func handleFakeStoredChatCompletionRoute(t *testing.T, w http.ResponseWriter, r *http.Request, stored map[string]fakeStoredChatCompletion, mu *sync.Mutex) {
	t.Helper()

	completionID, isMessages := fakeStoredChatCompletionPath(r.URL.Path)
	if completionID == "" {
		http.NotFound(w, r)
		return
	}

	switch {
	case r.Method == http.MethodGet && isMessages:
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		record, ok := stored[completionID]
		mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(fakeNotFoundError("chat completion not found")))
			return
		}
		page, statusCode, payload := buildFakeStoredChatCompletionMessages(record, completionID, r.URL.Query())
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			require.NoError(t, json.NewEncoder(w).Encode(payload))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(page))
	case r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		record, ok := stored[completionID]
		mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(fakeNotFoundError("chat completion not found")))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(record.Response))
	case r.Method == http.MethodPost:
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		metadata, ok := request["metadata"].(map[string]any)
		if len(request) != 1 || !ok {
			w.WriteHeader(http.StatusBadRequest)
			require.NoError(t, json.NewEncoder(w).Encode(fakeValidationError("metadata", "metadata is required")))
			return
		}

		mu.Lock()
		record, found := stored[completionID]
		if found {
			response := cloneJSONObject(record.Response)
			response["metadata"] = metadata
			record.Response = response
			requestClone := cloneJSONObject(record.Request)
			requestClone["metadata"] = metadata
			record.Request = requestClone
			stored[completionID] = record
		}
		mu.Unlock()
		if !found {
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(fakeNotFoundError("chat completion not found")))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(record.Response))
	case r.Method == http.MethodDelete:
		mu.Lock()
		_, found := stored[completionID]
		if found {
			delete(stored, completionID)
		}
		mu.Unlock()
		if !found {
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(fakeNotFoundError("chat completion not found")))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id":      completionID,
			"object":  "chat.completion.deleted",
			"deleted": true,
		}))
	default:
		http.NotFound(w, r)
	}
}

func fakeStoredChatCompletionPath(path string) (string, bool) {
	if !strings.HasPrefix(path, "/v1/chat/completions/") {
		return "", false
	}
	trimmed := strings.TrimPrefix(path, "/v1/chat/completions/")
	if trimmed == "" {
		return "", false
	}
	if strings.HasSuffix(trimmed, "/messages") {
		return strings.TrimSuffix(trimmed, "/messages"), true
	}
	if strings.Contains(trimmed, "/") {
		return "", false
	}
	return trimmed, false
}

func buildFakeStoredChatCompletionsList(stored map[string]fakeStoredChatCompletion, values map[string][]string) (map[string]any, int, map[string]any) {
	entries := make([]map[string]any, 0, len(stored))
	for _, record := range stored {
		entries = append(entries, cloneJSONObject(record.Response))
	}

	model := strings.TrimSpace(lastQueryValue(values, "model"))
	metadataFilters := parseFakeMetadataFilters(values)
	filtered := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if model != "" && asString(entry["model"]) != model {
			continue
		}
		if !matchesFakeMetadataFilter(entry["metadata"], metadataFilters) {
			continue
		}
		filtered = append(filtered, entry)
	}

	order := strings.TrimSpace(lastQueryValue(values, "order"))
	if order == "" {
		order = "asc"
	}
	sort.Slice(filtered, func(i, j int) bool {
		leftCreated := int64(asFloat(filtered[i]["created"]))
		rightCreated := int64(asFloat(filtered[j]["created"]))
		leftID := asString(filtered[i]["id"])
		rightID := asString(filtered[j]["id"])
		if order == "desc" {
			if leftCreated != rightCreated {
				return leftCreated > rightCreated
			}
			return leftID > rightID
		}
		if leftCreated != rightCreated {
			return leftCreated < rightCreated
		}
		return leftID < rightID
	})

	after := strings.TrimSpace(lastQueryValue(values, "after"))
	start := 0
	if after != "" {
		start = -1
		for i, entry := range filtered {
			if asString(entry["id"]) == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, http.StatusNotFound, fakeNotFoundError("chat completion not found")
		}
	}

	limit := 20
	if rawLimit := strings.TrimSpace(lastQueryValue(values, "limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 {
			return nil, http.StatusBadRequest, fakeValidationError("limit", "limit must be a positive integer")
		}
		limit = parsed
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + limit
	hasMore := end < len(filtered)
	if end > len(filtered) {
		end = len(filtered)
	}

	data := filtered[start:end]
	page := map[string]any{
		"object":   "list",
		"data":     data,
		"has_more": hasMore,
	}
	if len(data) > 0 {
		page["first_id"] = asString(data[0]["id"])
		page["last_id"] = asString(data[len(data)-1]["id"])
	} else {
		page["first_id"] = nil
		page["last_id"] = nil
	}
	return page, http.StatusOK, nil
}

func buildFakeStoredChatCompletionMessages(record fakeStoredChatCompletion, completionID string, values map[string][]string) (map[string]any, int, map[string]any) {
	rawMessages, _ := record.Request["messages"].([]any)
	messages := make([]map[string]any, 0, len(rawMessages))
	for index, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		cloned := cloneJSONObject(message)
		if _, ok := cloned["id"]; !ok {
			cloned["id"] = completionID + "-" + strconv.Itoa(index)
		}
		if _, ok := cloned["name"]; !ok {
			cloned["name"] = nil
		}
		if content, ok := cloned["content"].([]any); ok {
			cloned["content_parts"] = content
			cloned["content"] = nil
		} else if _, ok := cloned["content_parts"]; !ok {
			cloned["content_parts"] = nil
		}
		messages = append(messages, cloned)
	}

	order := strings.TrimSpace(lastQueryValue(values, "order"))
	if order == "" {
		order = "asc"
	}
	if order == "desc" {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}

	after := strings.TrimSpace(lastQueryValue(values, "after"))
	start := 0
	if after != "" {
		start = -1
		for i, message := range messages {
			if asString(message["id"]) == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, http.StatusNotFound, fakeNotFoundError("chat completion not found")
		}
	}
	limit := 20
	if rawLimit := strings.TrimSpace(lastQueryValue(values, "limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 {
			return nil, http.StatusBadRequest, fakeValidationError("limit", "limit must be a positive integer")
		}
		limit = parsed
	}
	if start > len(messages) {
		start = len(messages)
	}
	end := start + limit
	hasMore := end < len(messages)
	if end > len(messages) {
		end = len(messages)
	}

	data := messages[start:end]
	page := map[string]any{
		"object":   "list",
		"data":     data,
		"has_more": hasMore,
	}
	if len(data) > 0 {
		page["first_id"] = asString(data[0]["id"])
		page["last_id"] = asString(data[len(data)-1]["id"])
	} else {
		page["first_id"] = nil
		page["last_id"] = nil
	}
	return page, http.StatusOK, nil
}

func parseFakeMetadataFilters(values map[string][]string) map[string]string {
	filters := map[string]string{}
	for key, rawValues := range values {
		if !strings.HasPrefix(key, "metadata[") || !strings.HasSuffix(key, "]") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(key, "metadata["), "]"))
		if name == "" {
			continue
		}
		filters[name] = lastQueryValue(values, key)
		_ = rawValues
	}
	return filters
}

func matchesFakeMetadataFilter(metadataValue any, filters map[string]string) bool {
	if len(filters) == 0 {
		return true
	}
	metadata, _ := metadataValue.(map[string]any)
	for key, expected := range filters {
		if asString(metadata[key]) != expected {
			return false
		}
	}
	return true
}

func lastQueryValue(values map[string][]string, key string) string {
	rawValues := values[key]
	if len(rawValues) == 0 {
		return ""
	}
	return rawValues[len(rawValues)-1]
}

func fakeNotFoundError(message string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"type":    "not_found_error",
			"message": message,
		},
	}
}

func fakeValidationError(param string, message string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"type":    "invalid_request_error",
			"param":   param,
			"message": message,
		},
	}
}

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

func fakeToolCallType(toolCall map[string]any) string {
	if kind := asString(toolCall["type"]); kind != "" {
		return kind
	}
	return "function"
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
