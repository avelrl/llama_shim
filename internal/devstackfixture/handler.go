package devstackfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
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
	fixtureCodexShellCommandToolName = "shell_command"
	fixtureCodexWriteStdinToolName   = "write_stdin"
)

type fixtureCodexCommandToolKind string

const (
	fixtureCodexCommandToolExec         fixtureCodexCommandToolKind = "exec_command"
	fixtureCodexCommandToolShell        fixtureCodexCommandToolKind = "shell"
	fixtureCodexCommandToolShellCommand fixtureCodexCommandToolKind = "shell_command"
)

func NewHandler() http.Handler {
	server := newFixtureServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/embeddings", handleEmbeddings)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("/v1/responses", handleResponses)
	mux.HandleFunc("/system_stats", handleComfyUISystemStats)
	mux.HandleFunc("/prompt", handleComfyUIPrompt)
	mux.HandleFunc("/history/", handleComfyUIHistory)
	mux.HandleFunc("/view", handleComfyUIView)
	mux.HandleFunc("/mcp", server.handleMCP)
	mux.HandleFunc("/sse", server.handleLegacyMCPSSE)
	mux.HandleFunc("/message", server.handleLegacyMCPMessage)
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/pages/web-search-guide", handleWebSearchGuidePage)
	mux.HandleFunc("/pages/project-sunbeam", handleProjectSunbeamPage)
	mux.HandleFunc("/pages/computer-harness", handleComputerHarnessPage)
	return mux
}

func handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var request struct {
		Model string `json:"model"`
		Input any    `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "malformed JSON body",
			},
		})
		return
	}

	inputs := normalizeEmbeddingInputs(request.Input)
	data := make([]map[string]any, 0, len(inputs))
	for i, input := range inputs {
		data = append(data, map[string]any{
			"object":    "embedding",
			"index":     i,
			"embedding": fixtureEmbedding(input),
		})
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = "devstack-embedding"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"model":  model,
		"data":   data,
		"usage": map[string]any{
			"prompt_tokens": len(inputs),
			"total_tokens":  len(inputs),
		},
	})
}

func normalizeEmbeddingInputs(input any) []string {
	switch typed := input.(type) {
	case string:
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, asString(item))
		}
		return out
	default:
		return []string{asString(input)}
	}
}

func fixtureEmbedding(input string) []float64 {
	text := strings.ToLower(input)
	switch {
	case strings.Contains(text, "orionpepper") || strings.Contains(text, "777") || strings.Contains(text, "code"):
		return []float64{1, 0, 0, 0}
	case strings.Contains(text, "ordinary") || strings.Contains(text, "decoy"):
		return []float64{0, 1, 0, 0}
	case strings.Contains(text, "sunbeam"):
		return []float64{0, 0, 1, 0}
	default:
		return []float64{0, 0, 0, 1}
	}
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

func (m *chatMessage) UnmarshalJSON(data []byte) error {
	var payload struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	content, err := decodeChatMessageContent(payload.Content)
	if err != nil {
		return err
	}
	m.Role = payload.Role
	m.Content = content
	m.ToolCallID = payload.ToolCallID
	return nil
}

func decodeChatMessageContent(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if partText := strings.TrimSpace(asString(part["text"])); partText != "" {
			out = append(out, partText)
		}
	}
	return strings.Join(out, "\n"), nil
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

func handleComfyUISystemStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"system": map[string]any{
			"os": "llama-shim-devstack",
		},
	})
}

func handleComfyUIPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "malformed JSON body",
		})
		return
	}
	if _, ok := request["prompt"].(map[string]any); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "devstack ComfyUI fixture expects prompt object",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prompt_id":   "comfyui_devstack_1",
		"number":      1,
		"node_errors": map[string]any{},
	})
}

func handleComfyUIHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	promptID := strings.TrimPrefix(r.URL.Path, "/history/")
	if strings.TrimSpace(promptID) == "" {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		promptID: map[string]any{
			"status": map[string]any{
				"completed":  true,
				"status_str": "success",
			},
			"outputs": map[string]any{
				"9": map[string]any{
					"images": []map[string]any{
						{
							"filename":  "devstack-comfyui.png",
							"subfolder": "",
							"type":      "output",
						},
					},
				},
			},
		},
	})
}

func handleComfyUIView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("filename")) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "missing filename",
		})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("fake-image"))
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

func handleComputerHarnessPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>Computer Harness Fixture</title>
    <style>
      html, body {
        margin: 0;
        width: 1024px;
        min-height: 1400px;
        font-family: system-ui, sans-serif;
        background: #f7f9fc;
        color: #172033;
      }
      main {
        position: relative;
        width: 1024px;
        height: 1400px;
      }
      h1 {
        position: absolute;
        left: 64px;
        top: 72px;
        margin: 0;
        font-size: 36px;
      }
      p {
        position: absolute;
        left: 64px;
        top: 130px;
        width: 420px;
        margin: 0;
        font-size: 18px;
        line-height: 1.45;
      }
      label {
        position: absolute;
        left: 560px;
        top: 288px;
        font-size: 16px;
        font-weight: 700;
      }
      #harness-input {
        position: absolute;
        left: 560px;
        top: 320px;
        width: 280px;
        height: 48px;
        padding: 0 14px;
        font-size: 22px;
        border: 2px solid #2563eb;
        border-radius: 6px;
        background: #fff;
      }
      #status {
        position: absolute;
        left: 560px;
        top: 390px;
        font-size: 16px;
      }
      #keypress-label {
        position: absolute;
        left: 64px;
        top: 482px;
      }
      #keypress-input {
        position: absolute;
        left: 64px;
        top: 514px;
        width: 280px;
        height: 44px;
        padding: 0 14px;
        font-size: 20px;
        border: 2px solid #0f766e;
        border-radius: 6px;
        background: #fff;
      }
      #keypress-status {
        position: absolute;
        left: 64px;
        top: 580px;
        font-size: 16px;
      }
      #drag-source {
        position: absolute;
        left: 120px;
        top: 620px;
        width: 44px;
        height: 44px;
        border-radius: 6px;
        background: #f59e0b;
        border: 2px solid #92400e;
      }
      #drag-target {
        position: absolute;
        left: 320px;
        top: 610px;
        width: 160px;
        height: 70px;
        border: 2px dashed #7c3aed;
        border-radius: 8px;
        display: grid;
        place-items: center;
        font-weight: 700;
      }
      #drag-status {
        position: absolute;
        left: 320px;
        top: 694px;
        font-size: 16px;
      }
      #scroll-section {
        position: absolute;
        left: 64px;
        top: 1040px;
        width: 480px;
        padding: 28px;
        border: 2px solid #475569;
        border-radius: 8px;
        background: #e2e8f0;
      }
      #scroll-target-button {
        height: 48px;
        padding: 0 18px;
        font-size: 18px;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>Computer Harness Fixture</h1>
      <p>This deterministic page exists for the V3 computer browser harness.
      The target input is intentionally placed around coordinate 636,343.</p>
      <label for="harness-input">Search term</label>
      <input id="harness-input" autocomplete="off" autofocus>
      <div id="status">Waiting for input</div>
      <label id="keypress-label" for="keypress-input">Keyboard target</label>
      <input id="keypress-input" autocomplete="off">
      <div id="keypress-status">Waiting for Enter</div>
      <div id="drag-source" aria-label="drag source"></div>
      <div id="drag-target">Drop zone</div>
      <div id="drag-status">Waiting for drag</div>
      <section id="scroll-section">
        <h2>Scroll target section</h2>
        <p>This section starts below the initial viewport.</p>
        <button id="scroll-target-button" type="button">Scroll target visible</button>
      </section>
    </main>
    <script>
      const input = document.getElementById("harness-input");
      const status = document.getElementById("status");
      input.addEventListener("input", () => {
        status.textContent = input.value === "penguin" ? "Harness input complete" : "Input: " + input.value;
      });
      const keypressInput = document.getElementById("keypress-input");
      const keypressStatus = document.getElementById("keypress-status");
      keypressInput.addEventListener("keydown", event => {
        if (event.key === "Enter" && keypressInput.value === "orca") {
          keypressStatus.textContent = "Keypress complete";
        }
      });
      const dragSource = document.getElementById("drag-source");
      const dragTarget = document.getElementById("drag-target");
      const dragStatus = document.getElementById("drag-status");
      let dragging = false;
      dragSource.addEventListener("mousedown", () => {
        dragging = true;
      });
      document.addEventListener("mouseup", event => {
        if (!dragging) {
          return;
        }
        dragging = false;
        const target = dragTarget.getBoundingClientRect();
        if (
          event.clientX >= target.left &&
          event.clientX <= target.right &&
          event.clientY >= target.top &&
          event.clientY <= target.bottom
        ) {
          document.body.dataset.dragComplete = "true";
          dragStatus.textContent = "Drag complete";
        }
      });
    </script>
  </body>
</html>`))
}

func assistantTextForMessages(messages []chatMessage) string {
	lastUser := strings.ToLower(strings.TrimSpace(lastUserContent(messages)))
	joined := strings.ToLower(strings.TrimSpace(joinMessageContent(messages)))

	switch {
	case strings.Contains(joined, "shim-local computer planner") && !strings.Contains(joined, "computer_call_output screenshot received"):
		return `{"decision":"computer_call","actions":[{"type":"screenshot"}]}`
	case strings.Contains(joined, "shim-local computer planner") && strings.Contains(joined, "ui is not suitable for a typing action"):
		return `{"decision":"assistant","message":"The UI is not suitable for a typing action."}`
	case strings.Contains(joined, "shim-local computer planner") && strings.Contains(joined, "press enter") && strings.Contains(joined, "orca"):
		return `{"decision":"computer_call","actions":[{"type":"click","x":204,"y":536},{"type":"type","text":"orca"},{"type":"keypress","key":"Enter"}]}`
	case strings.Contains(joined, "shim-local computer planner") && strings.Contains(joined, "scroll down by 520"):
		return `{"decision":"computer_call","actions":[{"type":"scroll","scroll_y":520}]}`
	case strings.Contains(joined, "shim-local computer planner") && strings.Contains(joined, "drag the orange square"):
		return `{"decision":"computer_call","actions":[{"type":"drag","path":[{"x":142,"y":642},{"x":400,"y":646}]}]}`
	case strings.Contains(joined, "shim-local computer planner") && strings.Contains(joined, "type penguin"):
		return `{"decision":"computer_call","actions":[{"type":"click","button":"left","keys":null,"x":636,"y":343},{"type":"type","text":"penguin"}]}`
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
	toolOutput := strings.ToLower(strings.TrimSpace(message.Content))
	if containsAny(joined, "codex task matrix bugfix go", "matrix bugfix go") {
		if !strings.Contains(joined, "bugfix go task passed") {
			return "command did not report bugfix completion: " + strings.TrimSpace(message.Content), true
		}
		return "BUGFIXED", true
	}
	if containsAny(joined, "mixed_bugfix_ok", "mixed_cause_found") {
		if !strings.Contains(joined, "mixed bugfix task passed") {
			return "command did not report mixed bugfix completion: " + strings.TrimSpace(message.Content), true
		}
		return "MIXED_CAUSE_FOUND: Add used subtraction.\nMIXED_BUGFIX_OK", true
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
	if containsAny(joined, "codex command recovery case", "command recovery case") {
		if !strings.Contains(joined, "recovery task verified") {
			return "command did not report recovery completion: " + strings.TrimSpace(message.Content), true
		}
		return "RECOVERED", true
	}
	if containsAny(joined, "codex command timeout recovery case", "command timeout recovery case") {
		if !strings.Contains(toolOutput, "timeout_recovered") {
			return "", false
		}
		return "TIMEOUT_RECOVERED", true
	}
	if containsAny(joined, "codex no-edit safety case", "no-edit safety case") {
		if !strings.Contains(toolOutput, "do-not-edit-token") {
			return "command did not report no-edit token: " + strings.TrimSpace(message.Content), true
		}
		return "NO_EDIT_OK", true
	}
	if containsAny(joined, "codex stderr handling case", "stderr handling case") {
		if !strings.Contains(toolOutput, "stderr-token") {
			return "command did not report stderr token: " + strings.TrimSpace(message.Content), true
		}
		return "STDERR_OK", true
	}
	if containsAny(joined, "codex long stdout case", "long stdout case") {
		if !strings.Contains(toolOutput, "long_stdout_done") {
			return "command did not report long stdout marker: " + strings.TrimSpace(message.Content), true
		}
		return "LONG_STDOUT_OK", true
	}
	if containsAny(joined, "codex command pipeline case", "command pipeline case") {
		if !strings.Contains(toolOutput, "pipeline task passed") {
			return "command did not report pipeline completion: " + strings.TrimSpace(message.Content), true
		}
		return "PIPELINE_OK", true
	}
	if containsAny(joined, "codex js bugfix case", "js bugfix case") {
		if !strings.Contains(joined, "js bugfix task passed") {
			return "command did not report JS bugfix completion: " + strings.TrimSpace(message.Content), true
		}
		return "JS_BUGFIX_OK", true
	}
	if containsAny(joined, "codex python bugfix case", "python bugfix case") {
		if !strings.Contains(joined, "python bugfix task passed") {
			return "command did not report Python bugfix completion: " + strings.TrimSpace(message.Content), true
		}
		return "PY_BUGFIX_OK", true
	}
	if containsAny(joined, "codex json config edit case", "json config edit case") {
		if !strings.Contains(joined, "json config task updated") {
			return "command did not report JSON config completion: " + strings.TrimSpace(message.Content), true
		}
		return "JSON_CONFIG_OK", true
	}
	if containsAny(joined, "codex env var case", "env var case") {
		if !strings.Contains(toolOutput, "eval_magic=phase3-core") {
			return "command did not report env marker: " + strings.TrimSpace(message.Content), true
		}
		return "ENV_VAR_OK", true
	}
	if containsAny(joined, "codex nested workdir case", "nested workdir case") {
		if !strings.Contains(toolOutput, "nested workdir ok") {
			return "command did not report nested workdir marker: " + strings.TrimSpace(message.Content), true
		}
		return "NESTED_WORKDIR_OK", true
	}
	if containsAny(joined, "codex context patch case", "context patch case") {
		if !strings.Contains(joined, "context patch task updated") {
			return "command did not report context patch completion: " + strings.TrimSpace(message.Content), true
		}
		return "CONTEXT_PATCH_OK", true
	}
	if containsAny(joined, "codex no delete case", "no delete case") {
		if !strings.Contains(toolOutput, "keep-me-safe") {
			return "command did not report protected file marker: " + strings.TrimSpace(message.Content), true
		}
		return "NO_DELETE_OK", true
	}
	if containsAny(joined, "codex shell script fix case", "shell script fix case") {
		if !strings.Contains(joined, "shell script task passed") {
			return "command did not report shell script completion: " + strings.TrimSpace(message.Content), true
		}
		return "SHELL_SCRIPT_OK", true
	}
	if containsAny(joined, "codex fallback shell case", "fallback shell case") {
		if !strings.Contains(toolOutput, "fallback-shell-token") {
			return "command did not report fallback shell token: " + strings.TrimSpace(message.Content), true
		}
		return "FALLBACK_SHELL_OK", true
	}
	if containsAny(joined, "codex write stdin pty case", "write stdin pty case") {
		if !strings.Contains(toolOutput, "stdin_done codex-stdin-token") {
			return "", false
		}
		return "STDIN_OK", true
	}
	if containsAny(joined, "codex websocket read case", "websocket read case") {
		if !strings.Contains(toolOutput, "llama-shim-42") {
			return "command did not report websocket read token: " + strings.TrimSpace(message.Content), true
		}
		return "WS_READ_OK", true
	}
	if containsAny(joined, "codex websocket patch case", "websocket patch case") {
		if !strings.Contains(joined, "patched websocket_target.txt") {
			return "command did not report websocket patch completion: " + strings.TrimSpace(message.Content), true
		}
		return "WS_PATCH_OK", true
	}
	if containsAny(joined, "codex eval read file", "eval read file") {
		if !strings.Contains(toolOutput, "llama-shim-42") {
			return "command did not report read_file token: " + strings.TrimSpace(message.Content), true
		}
		return "READ_OK", true
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
	if !containsAny(joined, "exec_command", "shell tool", "run command", " run ", "pwd", "remember code 777", "mixed_cause_found", "mixed_bugfix_ok", "write stdin pty case", "fallback shell case", "websocket read case", "websocket patch case") {
		return "", "", false
	}
	if containsAny(joined, "codex task matrix bugfix go", "matrix bugfix go") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os, subprocess; from pathlib import Path; d=Path(os.environ['LLAMA_SHIM_CODEX_MATRIX_WORKDIR']); p=d/'calc.go'; p.write_text(p.read_text().replace('return a - b', 'return a + b')); os.environ['GOCACHE']=str(d/'.gocache'); subprocess.run(['go','test','./...'], cwd=d, check=True); print('bugfix go task passed')\"", 60000), true
	}
	if containsAny(joined, "mixed_bugfix_ok", "mixed_cause_found") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os, subprocess; from pathlib import Path; d=Path.cwd(); p=d/'mathutil.go'; p.write_text(p.read_text().replace('return a - b', 'return a + b')); os.environ['GOCACHE']=str(d/'.gocache'); subprocess.run(['go','test','./...'], cwd=d, check=True); print('mixed bugfix task passed')\"", 60000), true
	}
	if containsAny(joined, "codex task matrix plan doc", "matrix plan doc") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os; from pathlib import Path; d=Path(os.environ['LLAMA_SHIM_CODEX_MATRIX_WORKDIR']); (d/'PLAN.md').write_text('# Implementation Plan\\n\\n- [x] Read requirements\\n- [x] Identify API change\\n- [x] Add regression test\\n'); print('plan task written')\"", 60000), true
	}
	if containsAny(joined, "codex task matrix multi file", "matrix multi file") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os; from pathlib import Path; d=Path(os.environ['LLAMA_SHIM_CODEX_MATRIX_WORKDIR']); (d/'app').mkdir(exist_ok=True); (d/'app/config.txt').write_text('mode=matrix\\nfeature=enabled\\n'); (d/'app/status.txt').write_text('status=updated\\n'); print('multi file task updated')\"", 60000), true
	}
	if containsAny(joined, "codex command recovery case", "command recovery case") {
		return name, fixtureCodexCommandArguments(kind, "d=\"$LLAMA_SHIM_CODEX_MATRIX_WORKDIR\"; cd \"$d\"; sh verify.sh >/dev/null 2>&1 || true; printf 'status=ready\\n' > status.txt; sh verify.sh; echo 'recovery task verified'", 60000), true
	}
	if containsAny(joined, "codex command timeout recovery case", "command timeout recovery case") {
		if message, ok := lastNonEmptyMessage(request.Messages); ok && strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
			return name, fixtureCodexCommandArguments(kind, "sh fast.sh", 60000), true
		}
		return name, fixtureCodexCommandArguments(kind, "sh slow.sh", 60000), true
	}
	if containsAny(joined, "codex no-edit safety case", "no-edit safety case") {
		return name, fixtureCodexCommandArguments(kind, "cat README.md", 60000), true
	}
	if containsAny(joined, "codex stderr handling case", "stderr handling case") {
		return name, fixtureCodexCommandArguments(kind, "sh emit_stderr.sh", 60000), true
	}
	if containsAny(joined, "codex long stdout case", "long stdout case") {
		return name, fixtureCodexCommandArguments(kind, "sh long_stdout.sh", 60000), true
	}
	if containsAny(joined, "codex command pipeline case", "command pipeline case") {
		return name, fixtureCodexCommandArguments(kind, "tr '[:lower:]' '[:upper:]' < input.txt | sort > pipeline.txt && cat pipeline.txt && echo 'pipeline task passed'", 60000), true
	}
	if containsAny(joined, "codex js bugfix case", "js bugfix case") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"from pathlib import Path; p=Path('math.js'); p.write_text(p.read_text().replace('return a - b;', 'return a + b;'))\" && node math.test.js && echo 'js bugfix task passed'", 60000), true
	}
	if containsAny(joined, "codex python bugfix case", "python bugfix case") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"from pathlib import Path; p=Path('mathutil.py'); p.write_text(p.read_text().replace('return a - b', 'return a + b'))\" && python3 test_mathutil.py && echo 'python bugfix task passed'", 60000), true
	}
	if containsAny(joined, "codex json config edit case", "json config edit case") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import json; from pathlib import Path; p=Path('config.json'); data=json.loads(p.read_text()); data['feature']['enabled']=True; data['feature']['mode']='strict'; p.write_text(json.dumps(data, indent=2, sort_keys=True)+'\\n')\" && python3 -m json.tool config.json >/dev/null && echo 'json config task updated'", 60000), true
	}
	if containsAny(joined, "codex env var case", "env var case") {
		return name, fixtureCodexCommandArguments(kind, "printf 'EVAL_MAGIC=%s\\n' \"$CODEX_EVAL_MAGIC\" > env_capture.txt && cat env_capture.txt", 60000), true
	}
	if containsAny(joined, "codex nested workdir case", "nested workdir case") {
		return name, fixtureCodexCommandArguments(kind, "cd src && tr '[:lower:]' '[:upper:]' < input.txt > output.txt && cat output.txt", 60000), true
	}
	if containsAny(joined, "codex context patch case", "context patch case") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"from pathlib import Path; req=Path('requirements.txt').read_text().strip(); p=Path('service.txt'); p.write_text('service=payments\\nmode=compatible\\nrequirement='+req+'\\n')\" && cat service.txt && echo 'context patch task updated'", 60000), true
	}
	if containsAny(joined, "codex no delete case", "no delete case") {
		return name, fixtureCodexCommandArguments(kind, "cat protected.txt scratch.txt", 60000), true
	}
	if containsAny(joined, "codex shell script fix case", "shell script fix case") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"from pathlib import Path; p=Path('app.sh'); p.write_text(p.read_text().replace('echo broken', 'echo fixed'))\" && sh verify.sh && echo 'shell script task passed'", 60000), true
	}
	if containsAny(joined, "codex fallback shell case", "fallback shell case") {
		return name, fixtureCodexCommandArguments(kind, "cat fallback.txt", 60000), true
	}
	if containsAny(joined, "codex write stdin pty case", "write stdin pty case") {
		if message, ok := lastNonEmptyMessage(request.Messages); ok && strings.EqualFold(strings.TrimSpace(message.Role), "tool") && strings.Contains(strings.ToLower(message.Content), "ready_for_stdin") {
			if writeStdinName := fixtureFunctionToolName(request.Tools, fixtureCodexWriteStdinToolName); writeStdinName != "" {
				return writeStdinName, fixtureCodexWriteStdinArguments(fixtureCodexSessionID(message.Content), "\x03", 3000), true
			}
		}
		return name, fixtureCodexInteractiveExecArguments("bash -lc 'trap \"echo STDIN_DONE codex-stdin-token; exit 0\" INT; echo READY_FOR_STDIN; sleep 300'", 1000), true
	}
	if containsAny(joined, "codex websocket read case", "websocket read case") {
		return name, fixtureCodexCommandArguments(kind, "cat README.md", 60000), true
	}
	if containsAny(joined, "codex websocket patch case", "websocket patch case") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"from pathlib import Path; p=Path('websocket_target.txt'); p.write_text(p.read_text().replace('status = TODO', 'status = websocket-patched')); print('patched websocket_target.txt')\"", 60000), true
	}
	if containsAny(joined, "codex eval read file", "eval read file") {
		return name, fixtureCodexCommandArguments(kind, "cat README.md", 60000), true
	}
	if containsAny(joined, "codex coding task smoke", "smoke_target.txt", "patched-by-codex") {
		return name, fixtureCodexCommandArguments(kind, "python3 -c \"import os; from pathlib import Path; p=Path(os.environ['LLAMA_SHIM_CODEX_SMOKE_TARGET']); p.write_text(p.read_text().replace('status = TODO', 'status = patched-by-codex')); print('patched smoke_target.txt')\"", 60000), true
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
	if name := fixtureFunctionToolName(tools, fixtureCodexShellCommandToolName); name != "" {
		return name, fixtureCodexCommandToolShellCommand
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
	case fixtureCodexCommandToolShellCommand:
		payload = map[string]any{
			"command":    command,
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

func fixtureCodexInteractiveExecArguments(command string, yieldTimeMS int) string {
	payload := map[string]any{
		"cmd":               command,
		"tty":               true,
		"yield_time_ms":     yieldTimeMS,
		"max_output_tokens": 12000,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func fixtureCodexWriteStdinArguments(sessionID int, chars string, yieldTimeMS int) string {
	payload := map[string]any{
		"session_id":        sessionID,
		"chars":             chars,
		"yield_time_ms":     yieldTimeMS,
		"max_output_tokens": 12000,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func fixtureCodexSessionID(text string) int {
	for _, pattern := range []string{
		`"session_id"\s*:\s*([0-9]+)`,
		`(?i)process\s+running\s+with\s+session\s+id\s+([0-9]+)`,
	} {
		matches := regexp.MustCompile(pattern).FindStringSubmatch(text)
		if len(matches) == 2 {
			if sessionID, err := strconv.Atoi(matches[1]); err == nil {
				return sessionID
			}
		}
	}
	return 0
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
