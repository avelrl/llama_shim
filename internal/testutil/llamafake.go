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

			output := fakeResponseOutput(request["input"])
			model, _ := request["model"].(string)

			mu.Lock()
			nextID++
			id := "upstream_resp_" + strconv.Itoa(nextID)
			response := buildFakeResponse(id, model, output)
			responses[id] = response
			mu.Unlock()

			if stream, _ := request["stream"].(bool); stream {
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
