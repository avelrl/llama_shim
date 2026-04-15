package devstackfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const DefaultModel = "devstack-model"

const fixtureImageBase64 = "ZmFrZS1pbWFnZQ=="

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("/v1/responses", handleResponses)
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/pages/web-search-guide", handleWebSearchGuidePage)
	mux.HandleFunc("/pages/project-sunbeam", handleProjectSunbeamPage)
	return mux
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>Devstack Fixture</title></head><body><h1>Devstack Fixture Ready</h1></body></html>`))
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{
			{
				"id":         DefaultModel,
				"object":     "model",
				"created":    1712059200,
				"owned_by":   "llama-shim-devstack",
				"permission": []any{},
			},
		},
	})
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var request chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "malformed JSON body",
			},
		})
		return
	}

	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = DefaultModel
	}
	content := assistantTextForMessages(request.Messages)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl_devstack_1",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     max(1, len(request.Messages)),
			"completion_tokens": max(1, len(strings.Fields(content))),
			"total_tokens":      max(2, len(request.Messages)+len(strings.Fields(content))),
		},
	})
}

func handleResponses(w http.ResponseWriter, r *http.Request) {
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

	tools, _ := request["tools"].([]any)
	if len(tools) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "devstack fixture expects exactly one tool",
			},
		})
		return
	}
	tool, _ := tools[0].(map[string]any)
	if strings.TrimSpace(asString(tool["type"])) != "image_generation" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "devstack fixture supports only image_generation tools",
			},
		})
		return
	}

	model := strings.TrimSpace(asString(request["model"]))
	if model == "" {
		model = DefaultModel
	}
	revisedPrompt := firstNonEmpty(extractInputText(request["input"]), "A tiny orange cat curled up in a teacup.")
	now := time.Now().Unix()

	writeJSON(w, http.StatusOK, map[string]any{
		"id":                 "resp_devstack_image_1",
		"object":             "response",
		"created_at":         now,
		"status":             "completed",
		"completed_at":       now,
		"error":              nil,
		"incomplete_details": nil,
		"instructions":       nil,
		"max_output_tokens":  nil,
		"model":              model,
		"output": []map[string]any{
			{
				"id":             "ig_devstack_1",
				"type":           "image_generation_call",
				"status":         "completed",
				"background":     firstNonEmpty(asString(tool["background"]), "transparent"),
				"output_format":  firstNonEmpty(asString(tool["output_format"]), "png"),
				"quality":        firstNonEmpty(asString(tool["quality"]), "low"),
				"size":           firstNonEmpty(asString(tool["size"]), "1024x1024"),
				"result":         fixtureImageBase64,
				"revised_prompt": revisedPrompt,
				"action":         firstNonEmpty(asString(tool["action"]), "generate"),
			},
		},
		"parallel_tool_calls":  true,
		"previous_response_id": nil,
		"reasoning": map[string]any{
			"effort":  nil,
			"summary": nil,
		},
		"store":       false,
		"temperature": 1.0,
		"text": map[string]any{
			"format": map[string]any{
				"type": "text",
			},
		},
		"tool_choice": map[string]any{
			"type": "image_generation",
		},
		"tools":       tools,
		"top_p":       1.0,
		"truncation":  "disabled",
		"usage":       nil,
		"user":        nil,
		"metadata":    map[string]any{},
		"output_text": "",
	})
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	switch {
	case strings.Contains(strings.ToLower(query), "sunbeam"):
		writeJSON(w, http.StatusOK, map[string]any{
			"results": []map[string]any{
				{
					"url":         absoluteURL(r, "/pages/project-sunbeam"),
					"title":       "Project Sunbeam Launch Notes",
					"content":     "Project Sunbeam launched successfully in the deterministic fixture backend.",
					"description": "Project Sunbeam launched successfully.",
				},
			},
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"results": []map[string]any{
				{
					"url":         absoluteURL(r, "/pages/web-search-guide"),
					"title":       "Fixture Web Search Guide",
					"content":     "SUPPORTED FIXTURE PHRASE appears on the guide page for open_page and find_in_page smoke checks.",
					"description": "Guide page with a deterministic support phrase.",
				},
			},
		})
	}
}

func handleWebSearchGuidePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Fixture Web Search Guide</title>
  </head>
  <body>
    <h1>Fixture Web Search Guide</h1>
    <p>SUPPORTED FIXTURE PHRASE</p>
    <p>This page exists so the shim-local web search flow can exercise search, open_page, and find_in_page deterministically.</p>
  </body>
</html>`))
}

func handleProjectSunbeamPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Project Sunbeam Launch Notes</title>
  </head>
  <body>
    <h1>Project Sunbeam Launch Notes</h1>
    <p>Project Sunbeam launched successfully.</p>
  </body>
</html>`))
}

func assistantTextForMessages(messages []chatMessage) string {
	lastUser := strings.ToLower(strings.TrimSpace(lastUserContent(messages)))
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(messages)))

	switch {
	case strings.Contains(lastUser, "reply ready") && containsAny(joined, "remember code 777", "remember: code=777"):
		return "READY"
	case strings.Contains(lastUser, "what code did i ask you to remember") && strings.Contains(joined, "777"):
		return "777"
	case strings.Contains(joined, "what code did i ask you to remember") && strings.Contains(joined, "777"):
		return "777"
	case strings.Contains(joined, "what is the code") && containsAny(joined, "code=777", "code 777"):
		return "777"
	case strings.Contains(joined, "shim-local web search results") && strings.Contains(joined, "supported fixture phrase"):
		return "SUPPORTED FIXTURE PHRASE"
	case strings.Contains(joined, "shim-local web search results"):
		return "Used fixture web search results."
	default:
		return "OK"
	}
}

func joinMessageContent(messages []chatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if text := strings.TrimSpace(message.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func lastUserContent(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return messages[i].Content
		}
	}
	return ""
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func extractInputText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, entry := range typed {
			object, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(asString(object["type"])), "message") {
				if text := strings.TrimSpace(asString(object["content"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "fixture:8081"
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": "method not allowed",
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
