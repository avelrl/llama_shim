package devstackfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type fixtureServer struct {
	mu          sync.Mutex
	nextSession int
	sessions    map[string]*fixtureSessionState
}

type fixtureSessionState struct {
	ch chan []byte
}

type fixtureMCPTool struct {
	Name        string
	Description string
	InputSchema map[string]any
	OutputText  string
	IsError     bool
}

var fixtureMCPTools = []fixtureMCPTool{
	{
		Name:        "roll",
		Description: "Roll dice from a dice expression.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diceRollExpression": map[string]any{"type": "string"},
			},
			"required":             []string{"diceRollExpression"},
			"additionalProperties": false,
		},
		OutputText: "4",
	},
}

func newFixtureServer() *fixtureServer {
	return &fixtureServer{
		sessions: make(map[string]*fixtureSessionState),
	}
}

func (s *fixtureServer) handleLegacyMCPSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{
				"type":    "server_error",
				"message": "response writer does not support streaming",
			},
		})
		return
	}

	sessionID := s.newSessionID("sse")
	state := &fixtureSessionState{ch: make(chan []byte, 16)}

	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sessions, sessionID)
		s.mu.Unlock()
		close(state.ch)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if err := writeFixtureMCPSSEEvent(w, "endpoint", "/message?session="+sessionID); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case message, ok := <-state.ch:
			if !ok {
				return
			}
			if err := writeFixtureMCPSSEEvent(w, "message", string(message)); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *fixtureServer) handleLegacyMCPMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	state := s.lookupSession(sessionID)
	if state == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"jsonrpc": "2.0",
			"error": map[string]any{
				"message": "invalid or missing session id",
			},
		})
		return
	}

	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "malformed JSON body",
			},
		})
		return
	}

	result, rawError := buildFixtureMCPMethodResponse(request)
	if err := sendFixtureMCPResponse(state.ch, request["id"], result, rawError); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{
				"type":    "server_error",
				"message": err.Error(),
			},
		})
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *fixtureServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "malformed JSON body",
			},
		})
		return
	}

	method := strings.TrimSpace(asString(request["method"]))
	sessionID := strings.TrimSpace(r.Header.Get("Mcp-Session-Id"))
	if method == "initialize" && sessionID == "" {
		sessionID = s.newSessionID("mcp")
		s.mu.Lock()
		s.sessions[sessionID] = &fixtureSessionState{}
		s.mu.Unlock()
	} else if s.lookupSession(sessionID) == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"error": map[string]any{
				"message": "invalid or missing session id",
			},
		})
		return
	}

	result, rawError := buildFixtureMCPMethodResponse(request)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", sessionID)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"result":  result,
		"error":   rawError,
	})
}

func (s *fixtureServer) newSessionID(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSession++
	return fmt.Sprintf("%s-%d", prefix, s.nextSession)
}

func (s *fixtureServer) lookupSession(sessionID string) *fixtureSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

func buildFixtureMCPMethodResponse(request map[string]any) (any, any) {
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
				"name":    "devstack-mcp",
				"version": "1.0.0",
			},
		}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(fixtureMCPTools))
		for _, tool := range fixtureMCPTools {
			tools = append(tools, map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
			})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		params, _ := request["params"].(map[string]any)
		name := strings.TrimSpace(asString(params["name"]))
		tool, ok := findFixtureMCPTool(name)
		if !ok {
			return nil, map[string]any{"message": "unknown tool"}
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
		return nil, map[string]any{"message": "unsupported method"}
	}
}

func findFixtureMCPTool(name string) (fixtureMCPTool, bool) {
	name = strings.TrimSpace(name)
	for _, tool := range fixtureMCPTools {
		if strings.TrimSpace(tool.Name) == name {
			return tool, true
		}
	}
	return fixtureMCPTool{}, false
}

func sendFixtureMCPResponse(ch chan []byte, id any, result any, rawError any) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
		"error":   rawError,
	})
	if err != nil {
		return err
	}
	ch <- payload
	return nil
}

func writeFixtureMCPSSEEvent(w http.ResponseWriter, eventType string, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", eventType); err != nil {
		return err
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\n")
	return err
}
