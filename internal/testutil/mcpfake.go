package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type FakeMCPTool struct {
	Name        string
	Description string
	InputSchema map[string]any
	OutputText  string
	IsError     bool
}

func NewFakeMCPServer(t *testing.T, tools []FakeMCPTool) *httptest.Server {
	t.Helper()

	type sessionState struct {
		ch chan []byte
	}

	var (
		mu       sync.Mutex
		sessions = map[string]*sessionState{}
	)

	findTool := func(name string) (FakeMCPTool, bool) {
		for _, tool := range tools {
			if strings.TrimSpace(tool.Name) == strings.TrimSpace(name) {
				return tool, true
			}
		}
		return FakeMCPTool{}, false
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			require.True(t, ok)

			sessionID := "default"
			state := &sessionState{ch: make(chan []byte, 16)}
			mu.Lock()
			sessions[sessionID] = state
			mu.Unlock()
			defer func() {
				mu.Lock()
				delete(sessions, sessionID)
				mu.Unlock()
				close(state.ch)
			}()

			_, err := w.Write([]byte("event: endpoint\ndata: /message?session=default\n\n"))
			require.NoError(t, err)
			flusher.Flush()

			for {
				select {
				case <-r.Context().Done():
					return
				case message, ok := <-state.ch:
					if !ok {
						return
					}
					_, err := w.Write([]byte("event: message\ndata: "))
					require.NoError(t, err)
					_, err = w.Write(message)
					require.NoError(t, err)
					_, err = w.Write([]byte("\n\n"))
					require.NoError(t, err)
					flusher.Flush()
				}
			}
		case r.Method == http.MethodPost && r.URL.Path == "/message":
			sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
			mu.Lock()
			state := sessions[sessionID]
			mu.Unlock()
			require.NotNil(t, state)

			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			method := strings.TrimSpace(asString(request["method"]))

			switch method {
			case "notifications/initialized":
			case "initialize":
				sendFakeMCPResponse(t, state.ch, request["id"], map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools": map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "fake-mcp",
						"version": "1.0.0",
					},
				}, nil)
			case "tools/list":
				encodedTools := make([]map[string]any, 0, len(tools))
				for _, tool := range tools {
					encodedTools = append(encodedTools, map[string]any{
						"name":        tool.Name,
						"description": tool.Description,
						"inputSchema": tool.InputSchema,
					})
				}
				sendFakeMCPResponse(t, state.ch, request["id"], map[string]any{
					"tools": encodedTools,
				}, nil)
			case "tools/call":
				params, _ := request["params"].(map[string]any)
				name := strings.TrimSpace(asString(params["name"]))
				tool, ok := findTool(name)
				if !ok {
					sendFakeMCPResponse(t, state.ch, request["id"], nil, map[string]any{
						"message": "unknown tool",
					})
					break
				}
				sendFakeMCPResponse(t, state.ch, request["id"], map[string]any{
					"content": []map[string]any{
						{
							"type": "text",
							"text": tool.OutputText,
						},
					},
					"isError": tool.IsError,
				}, nil)
			default:
				sendFakeMCPResponse(t, state.ch, request["id"], nil, map[string]any{
					"message": "unsupported method",
				})
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))

	return server
}

func sendFakeMCPResponse(t *testing.T, ch chan []byte, id any, result any, rawError any) {
	t.Helper()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
		"error":   rawError,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ch <- body
}
