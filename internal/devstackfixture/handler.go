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

const (
	fixtureBuiltinShellToolName      = "__llama_shim_builtin_shell"
	fixtureBuiltinApplyPatchToolName = "__llama_shim_builtin_apply_patch"
	fixtureCodexExecCommandToolName  = "exec_command"
	fixtureCodexShellToolName        = "shell"
)

type fixtureCodexCommandToolKind string

const (
	fixtureCodexCommandToolExec  fixtureCodexCommandToolKind = "exec_command"
	fixtureCodexCommandToolShell fixtureCodexCommandToolKind = "shell"
)

func NewHandler() http.Handler {
	server := newFixtureServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("/v1/responses", handleResponses)
	mux.HandleFunc("/mcp", server.handleMCP)
	mux.HandleFunc("/sse", server.handleLegacyMCPSSE)
	mux.HandleFunc("/message", server.handleLegacyMCPMessage)
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/pages/web-search-guide", handleWebSearchGuidePage)
	mux.HandleFunc("/pages/project-sunbeam", handleProjectSunbeamPage)
	return mux
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools"`
}

type chatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
	content, toolCalls, finishReason := chatCompletionReply(request)
	message := map[string]any{
		"role":    "assistant",
		"content": content,
	}
	if len(toolCalls) > 0 {
		message["content"] = nil
		message["tool_calls"] = toolCalls
	}
	completionTokens := max(1, len(strings.Fields(content)))
	if len(toolCalls) > 0 {
		completionTokens = max(1, len(toolCalls))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl_devstack_1",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     max(1, len(request.Messages)),
			"completion_tokens": completionTokens,
			"total_tokens":      max(2, len(request.Messages)+completionTokens),
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
					"content":     "SUPPORTED FIXTURE PHRASE appears on the guide page for deterministic web search checks.",
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
    <p>This page exists so deterministic search results can point at a stable guide page during debugging and targeted tests.</p>
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
	case strings.Contains(joined, "shim-local constrained custom tool generator") && strings.Contains(joined, "`math_exp`"):
		return `{"input":"4 + 4"}`
	case strings.Contains(joined, "shim-local constrained custom tool generator") && strings.Contains(joined, "`exact_text`"):
		return `{"input":"hello 42"}`
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

func chatCompletionReply(request chatCompletionRequest) (string, []map[string]any, string) {
	if output, ok := fixtureCompactionOutput(request); ok {
		return output, nil, "stop"
	}
	if output, ok := fixtureToolSearchFinalOutput(request); ok {
		return output, nil, "stop"
	}
	if output, ok := fixtureMCPFinalOutput(request); ok {
		return output, nil, "stop"
	}
	if output, ok := fixtureCodexFunctionFinalOutput(request); ok {
		return output, nil, "stop"
	}
	if name, arguments, ok := fixtureCodexFunctionPlannedCall(request); ok {
		return "", []map[string]any{
			{
				"id":   "call_devstack_codex_1",
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": arguments,
				},
			},
		}, "tool_calls"
	}
	if output, ok := fixtureBuiltinCodingToolFinalOutput(request); ok {
		return output, nil, "stop"
	}
	if name, arguments, ok := fixtureBuiltinCodingToolPlannedCall(request); ok {
		return "", []map[string]any{
			{
				"id":   "call_devstack_builtin_1",
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": arguments,
				},
			},
		}, "tool_calls"
	}
	if name, arguments, ok := fixtureToolSearchPlannedToolCall(request); ok {
		return "", []map[string]any{
			{
				"id":   "call_devstack_tool_search_1",
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": arguments,
				},
			},
		}, "tool_calls"
	}
	if name, arguments, ok := fixtureMCPPlannedToolCall(request); ok {
		return "", []map[string]any{
			{
				"id":   "call_devstack_mcp_1",
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": arguments,
				},
			},
		}, "tool_calls"
	}
	return assistantTextForMessages(request.Messages), nil, "stop"
}

func fixtureCompactionOutput(request chatCompletionRequest) (string, bool) {
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(request.Messages)))
	if !strings.Contains(joined, "compact these prior context items for continuation") {
		return "", false
	}
	if !strings.Contains(joined, "compact prior conversation state") {
		return "", false
	}
	state := map[string]any{
		"summary":           "The user asked the shim to remember launch code 777 for the devstack compaction smoke.",
		"key_facts":         []string{"launch code is 777", "compaction smoke uses the deterministic fixture backend"},
		"constraints":       []string{"reply with requested exact values"},
		"open_loops":        []string{"answer follow-up code questions from compacted state"},
		"recent_tool_state": []string{"no pending tool calls"},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func fixtureCodexFunctionFinalOutput(request chatCompletionRequest) (string, bool) {
	if !fixtureHasCodexCommandTool(request.Tools) {
		return "", false
	}
	message, ok := lastNonEmptyMessage(request.Messages)
	if !ok || !strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
		return "", false
	}
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(request.Messages)))
	if containsAny(joined, "codex task matrix bugfix go", "matrix bugfix go") {
		if !strings.Contains(joined, "bugfix go task passed") {
			return "command did not report bugfix completion: " + strings.TrimSpace(message.Content), true
		}
		return "BUGFIXED", true
	}
	if containsAny(joined, "codex task matrix plan doc", "matrix plan doc") {
		if !strings.Contains(joined, "plan task written") {
			return "command did not report plan completion: " + strings.TrimSpace(message.Content), true
		}
		return "PLANNED", true
	}
	if containsAny(joined, "codex task matrix multi file", "matrix multi file") {
		if !strings.Contains(joined, "multi file task updated") {
			return "command did not report multi-file completion: " + strings.TrimSpace(message.Content), true
		}
		return "MULTIFILE", true
	}
	if containsAny(joined, "reply patched", "codex coding task smoke", "patched-by-codex") {
		if !strings.Contains(joined, "patched smoke_target.txt") {
			return "command did not report patch completion: " + strings.TrimSpace(message.Content), true
		}
		return "PATCHED", true
	}
	if containsAny(joined, "reply ready", "remember code 777") {
		return "READY", true
	}
	return strings.TrimSpace(message.Content), true
}

func fixtureCodexFunctionPlannedCall(request chatCompletionRequest) (string, string, bool) {
	name, kind := fixtureCodexCommandTool(request.Tools)
	if name == "" {
		return "", "", false
	}
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(request.Messages)))
	if !containsAny(joined, "exec_command", "shell tool", "run command", " run ", "pwd", "remember code 777") {
		return "", "", false
	}
	if containsAny(joined, "codex task matrix bugfix go", "matrix bugfix go") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os, subprocess; from pathlib import Path; d=Path(os.environ['LLAMA_SHIM_CODEX_MATRIX_WORKDIR']); p=d/'calc.go'; p.write_text(p.read_text().replace('return a - b', 'return a + b')); os.environ['GOCACHE']=str(d/'.gocache'); subprocess.run(['go','test','./...'], cwd=d, check=True); print('bugfix go task passed')\"", 60000), true
	}
	if containsAny(joined, "codex task matrix plan doc", "matrix plan doc") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os; from pathlib import Path; d=Path(os.environ['LLAMA_SHIM_CODEX_MATRIX_WORKDIR']); (d/'PLAN.md').write_text('# Implementation Plan\\n\\n- [x] Read requirements\\n- [x] Identify API change\\n- [x] Add regression test\\n'); print('plan task written')\"", 5000), true
	}
	if containsAny(joined, "codex task matrix multi file", "matrix multi file") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os; from pathlib import Path; d=Path(os.environ['LLAMA_SHIM_CODEX_MATRIX_WORKDIR']); (d/'app').mkdir(exist_ok=True); (d/'app/config.txt').write_text('mode=matrix\\nfeature=enabled\\n'); (d/'app/status.txt').write_text('status=updated\\n'); print('multi file task updated')\"", 5000), true
	}
	if containsAny(joined, "codex coding task smoke", "smoke_target.txt", "patched-by-codex") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os; from pathlib import Path; p=Path(os.environ['LLAMA_SHIM_CODEX_SMOKE_TARGET']); p.write_text(p.read_text().replace('status = TODO', 'status = patched-by-codex')); print('patched smoke_target.txt')\"", 5000), true
	}
	return name, fixtureCodexCommandArguments(kind, "pwd", 1000), true
}

func fixtureHasCodexCommandTool(tools []chatTool) bool {
	name, _ := fixtureCodexCommandTool(tools)
	return name != ""
}

func fixtureCodexCommandTool(tools []chatTool) (string, fixtureCodexCommandToolKind) {
	if name := fixtureFunctionToolName(tools, fixtureCodexExecCommandToolName); name != "" {
		return name, fixtureCodexCommandToolExec
	}
	if name := fixtureFunctionToolName(tools, fixtureCodexShellToolName); name != "" {
		return name, fixtureCodexCommandToolShell
	}
	return "", ""
}

func fixtureCodexCommandArguments(kind fixtureCodexCommandToolKind, command string, timeoutMS int) string {
	var payload map[string]any
	switch kind {
	case fixtureCodexCommandToolShell:
		payload = map[string]any{
			"command":    []string{"bash", "-lc", command},
			"timeout_ms": timeoutMS,
			"workdir":    ".",
		}
	default:
		payload = map[string]any{
			"cmd":               command,
			"max_output_tokens": 12000,
			"yield_time_ms":     timeoutMS,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func fixtureBuiltinCodingToolFinalOutput(request chatCompletionRequest) (string, bool) {
	if fixtureFunctionToolName(request.Tools, fixtureBuiltinShellToolName) == "" &&
		fixtureFunctionToolName(request.Tools, fixtureBuiltinApplyPatchToolName) == "" {
		return "", false
	}
	message, ok := lastNonEmptyMessage(request.Messages)
	if !ok || !strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
		return "", false
	}
	return strings.TrimSpace(message.Content), true
}

func fixtureBuiltinCodingToolPlannedCall(request chatCompletionRequest) (string, string, bool) {
	lastUser := strings.ToLower(strings.TrimSpace(lastUserContent(request.Messages)))
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(request.Messages)))

	if name := fixtureFunctionToolName(request.Tools, fixtureBuiltinShellToolName); name != "" &&
		containsAny(lastUser, "shell", "pwd", "command") {
		return name, `{"action":{"commands":["pwd"],"timeout_ms":30000,"max_output_length":12000}}`, true
	}

	if name := fixtureFunctionToolName(request.Tools, fixtureBuiltinApplyPatchToolName); name != "" &&
		containsAny(joined, "apply_patch", "patch", "game/main.go", "answer from 1 to 2") {
		return name, `{"operation":{"type":"update_file","path":"game/main.go","diff":"*** Begin Patch\n*** Update File: game/main.go\n@@\n-const answer = 1\n+const answer = 2\n*** End Patch\n"}}`, true
	}

	return "", "", false
}

func fixtureToolSearchFinalOutput(request chatCompletionRequest) (string, bool) {
	if !isFixtureToolSearchConversation(request.Messages) {
		return "", false
	}
	message, ok := lastNonEmptyMessage(request.Messages)
	if !ok || !strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
		return "", false
	}
	return strings.TrimSpace(message.Content), true
}

func fixtureToolSearchPlannedToolCall(request chatCompletionRequest) (string, string, bool) {
	name := fixtureShippingToolName(request.Tools)
	if name == "" {
		return "", "", false
	}
	if !isFixtureToolSearchConversation(request.Messages) {
		return "", "", false
	}
	return name, `{"order_id":"order_42"}`, true
}

func fixtureMCPFinalOutput(request chatCompletionRequest) (string, bool) {
	if fixtureMCPRollToolName(request.Tools) == "" {
		return "", false
	}
	message, ok := lastNonEmptyMessage(request.Messages)
	if !ok || !strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
		return "", false
	}
	return strings.TrimSpace(message.Content), true
}

func fixtureMCPPlannedToolCall(request chatCompletionRequest) (string, string, bool) {
	name := fixtureMCPRollToolName(request.Tools)
	if name == "" {
		return "", "", false
	}
	lastUser := strings.ToLower(strings.TrimSpace(lastUserContent(request.Messages)))
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(request.Messages)))
	if !containsAny(lastUser, "roll", "2d4") && !containsAny(joined, "roll 2d4+1", "roll again", "dice expression") {
		return "", "", false
	}
	return name, `{"diceRollExpression":"2d4 + 1"}`, true
}

func fixtureMCPRollToolName(tools []chatTool) string {
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(tool.Type), "function") {
			continue
		}
		name := strings.TrimSpace(tool.Function.Name)
		switch {
		case name == "roll":
			return name
		case strings.Contains(name, "mcp__") && strings.Contains(strings.ToLower(name), "roll"):
			return name
		}
	}
	return ""
}

func fixtureShippingToolName(tools []chatTool) string {
	return fixtureFunctionToolName(tools, "get_shipping_eta")
}

func fixtureFunctionToolName(tools []chatTool, wanted string) string {
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(tool.Type), "function") {
			continue
		}
		name := strings.TrimSpace(tool.Function.Name)
		if name == wanted {
			return name
		}
	}
	return ""
}

func isFixtureToolSearchConversation(messages []chatMessage) bool {
	lastUser := strings.ToLower(strings.TrimSpace(lastUserContent(messages)))
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(messages)))
	return containsAny(lastUser, "shipping eta", "order_42") || containsAny(joined, "shipping eta", "order_42", "shipping_ops")
}

func lastNonEmptyMessage(messages []chatMessage) (chatMessage, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		return message, true
	}
	return chatMessage{}, false
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
