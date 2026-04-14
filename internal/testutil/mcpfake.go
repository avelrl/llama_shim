package testutil

import (
	"encoding/json"
	"fmt"
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

type FakeMCPServerOptions struct {
	ExpectedAuthorization string
	ExpectedHeaders       map[string]string
}

func NewFakeMCPServer(t *testing.T, tools []FakeMCPTool) *httptest.Server {
	t.Helper()
	return NewFakeMCPServerWithOptions(t, tools, FakeMCPServerOptions{})
}

func NewFakeMCPServerWithOptions(t *testing.T, tools []FakeMCPTool, options FakeMCPServerOptions) *httptest.Server {
	t.Helper()

	type sessionState struct {
		ch chan []byte
	}

	var (
		mu           sync.Mutex
		nextSession  int
		sessions     = map[string]*sessionState{}
		headerChecks = normalizeFakeMCPExpectedHeaders(options)
	)

	findTool := func(name string) (FakeMCPTool, bool) {
		for _, tool := range tools {
			if strings.TrimSpace(tool.Name) == strings.TrimSpace(name) {
				return tool, true
			}
		}
		return FakeMCPTool{}, false
	}

	validateHeaders := func(w http.ResponseWriter, r *http.Request) bool {
		expectedAuthorization := strings.TrimSpace(options.ExpectedAuthorization)
		if expectedAuthorization != "" && strings.TrimSpace(r.Header.Get("Authorization")) != expectedAuthorization {
			http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
			return false
		}
		for key, value := range headerChecks {
			if strings.TrimSpace(r.Header.Get(key)) != value {
				http.Error(w, fmt.Sprintf("missing required header %s", key), http.StatusBadRequest)
				return false
			}
		}
		return true
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sse":
			if !validateHeaders(w, r) {
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			require.True(t, ok)

			mu.Lock()
			nextSession++
			sessionID := fmt.Sprintf("sse-%d", nextSession)
			state := &sessionState{ch: make(chan []byte, 16)}
			sessions[sessionID] = state
			mu.Unlock()
			defer func() {
				mu.Lock()
				delete(sessions, sessionID)
				mu.Unlock()
				close(state.ch)
			}()

			_, err := w.Write([]byte("event: endpoint\ndata: /message?session=" + sessionID + "\n\n"))
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
			if !validateHeaders(w, r) {
				return
			}
			sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
			mu.Lock()
			state := sessions[sessionID]
			mu.Unlock()
			require.NotNil(t, state)

			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			result, rawError := buildFakeMCPMethodResponse(findTool, tools, request)
			sendFakeMCPResponse(t, state.ch, request["id"], result, rawError)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			if !validateHeaders(w, r) {
				return
			}

			var request map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			method := strings.TrimSpace(asString(request["method"]))
			sessionID := strings.TrimSpace(r.Header.Get("Mcp-Session-Id"))

			if method == "initialize" && sessionID == "" {
				mu.Lock()
				nextSession++
				sessionID = fmt.Sprintf("mcp-%d", nextSession)
				sessions[sessionID] = &sessionState{}
				mu.Unlock()
			} else {
				mu.Lock()
				_, ok := sessions[sessionID]
				mu.Unlock()
				if !ok {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      request["id"],
						"error": map[string]any{
							"message": "invalid or missing session id",
						},
					}))
					return
				}
			}

			result, rawError := buildFakeMCPMethodResponse(findTool, tools, request)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", sessionID)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result":  result,
				"error":   rawError,
			}))
		default:
			http.NotFound(w, r)
		}
	}))

	return server
}

func buildFakeMCPMethodResponse(findTool func(string) (FakeMCPTool, bool), tools []FakeMCPTool, request map[string]any) (any, any) {
	method := strings.TrimSpace(asString(request["method"]))

	switch method {
	case "notifications/initialized":
		return nil, nil
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "fake-mcp",
				"version": "1.0.0",
			},
		}, nil
	case "tools/list":
		encodedTools := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			encodedTools = append(encodedTools, map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
			})
		}
		return map[string]any{
			"tools": encodedTools,
		}, nil
	case "tools/call":
		params, _ := request["params"].(map[string]any)
		name := strings.TrimSpace(asString(params["name"]))
		tool, ok := findTool(name)
		if !ok {
			return nil, map[string]any{
				"message": "unknown tool",
			}
		}
		return map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": tool.OutputText,
				},
			},
			"isError": tool.IsError,
		}, nil
	default:
		return nil, map[string]any{
			"message": "unsupported method",
		}
	}
}

func normalizeFakeMCPExpectedHeaders(options FakeMCPServerOptions) map[string]string {
	if len(options.ExpectedHeaders) == 0 {
		return nil
	}
	headers := make(map[string]string, len(options.ExpectedHeaders))
	for key, value := range options.ExpectedHeaders {
		headers[key] = value
	}
	return headers
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
