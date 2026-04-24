package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type wsEvent map[string]any

func main() {
	var (
		baseURL        = flag.String("base-url", envDefault("SHIM_BASE_URL", "http://127.0.0.1:18080"), "shim base URL")
		model          = flag.String("model", envDefault("MODEL", "devstack-model"), "Responses model")
		apiKey         = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "optional bearer token")
		fixtureMCPURL  = flag.String("fixture-mcp-url", envDefault("FIXTURE_INTERNAL_MCP_URL", "http://fixture:8081/mcp"), "fixture MCP URL visible to the shim")
		runLocalFamily = flag.Bool("local-families", envBoolDefault("RESPONSES_WS_SMOKE_LOCAL_FAMILIES", true), "exercise devstack local tool families")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	httpSmoke := smokeHTTP{
		baseURL: strings.TrimRight(*baseURL, "/"),
		apiKey:  strings.TrimSpace(*apiKey),
		client:  &http.Client{Timeout: 15 * time.Second},
	}
	conn, err := dial(ctx, *baseURL, *apiKey)
	if err != nil {
		exitf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	firstEvents, err := sendResponseCreate(ctx, conn, map[string]any{
		"model": *model,
		"store": true,
		"input": "Remember code 777. Reply READY.",
	})
	if err != nil {
		exitf("first response.create: %v", err)
	}
	requireEvent(firstEvents, "response.created")
	requireEvent(firstEvents, "response.output_text.delta")
	firstCompleted := requireEvent(firstEvents, "response.completed")
	firstResponse := eventResponse(firstCompleted)
	firstID := asString(firstResponse["id"])
	if firstID == "" {
		exitf("first response.completed missing response.id")
	}

	secondEvents, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":                *model,
		"store":                true,
		"previous_response_id": firstID,
		"input":                "What code did I ask you to remember? Reply digits only.",
	})
	if err != nil {
		exitf("second response.create: %v", err)
	}
	secondResponse := eventResponse(requireEvent(secondEvents, "response.completed"))
	if got := asString(secondResponse["previous_response_id"]); got != firstID {
		exitf("second response previous_response_id mismatch: got %q want %q", got, firstID)
	}

	shellEvents, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":       *model,
		"store":       true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Run the local shell command and do not answer directly.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "shell",
				"environment": map[string]any{
					"type": "local",
				},
			},
		},
	})
	if err != nil {
		exitf("shell response.create: %v", err)
	}
	requireEvent(shellEvents, "response.shell_call_command.delta")
	requireEvent(shellEvents, "response.shell_call_command.done")
	requireEvent(shellEvents, "response.completed")

	applyPatchEvents, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":       *model,
		"store":       true,
		"tool_choice": "required",
		"input": []map[string]any{
			{
				"role":    "user",
				"content": "Patch the code and do not answer directly.",
			},
		},
		"tools": []map[string]any{
			{
				"type": "apply_patch",
			},
		},
	})
	if err != nil {
		exitf("apply_patch response.create: %v", err)
	}
	requireEvent(applyPatchEvents, "response.apply_patch_call_operation_diff.done")
	requireEvent(applyPatchEvents, "response.completed")

	if *runLocalFamily {
		fileID, vectorStoreID := seedFileSearchFixture(ctx, httpSmoke)
		defer httpSmoke.delete(ctx, "/v1/files/"+fileID)
		defer httpSmoke.delete(ctx, "/v1/vector_stores/"+vectorStoreID)
		runFileSearchSmoke(ctx, conn, *model, vectorStoreID)
		runWebSearchSmoke(ctx, conn, *model)
		runImageGenerationSmoke(ctx, conn, *model)
		runMCPSmoke(ctx, conn, *model, *fixtureMCPURL)
		runToolSearchSmoke(ctx, conn, *model)
	}

	fmt.Println("responses websocket smoke passed")
}

func envDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBoolDefault(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

type smokeHTTP struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func (s smokeHTTP) postJSON(ctx context.Context, path string, payload any) map[string]any {
	raw, err := json.Marshal(payload)
	if err != nil {
		exitf("marshal %s request: %v", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		exitf("build %s request: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)
	return s.doJSON(req, http.StatusOK)
}

func (s smokeHTTP) uploadFile(ctx context.Context, filename string, content string) map[string]any {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("purpose", "assistants"); err != nil {
		exitf("build file upload purpose: %v", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		exitf("build file upload part: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader(content)); err != nil {
		exitf("write file upload part: %v", err)
	}
	if err := writer.Close(); err != nil {
		exitf("finish file upload body: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/files", &body)
	if err != nil {
		exitf("build file upload request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	s.authorize(req)
	return s.doJSON(req, http.StatusOK)
}

func (s smokeHTTP) delete(ctx context.Context, path string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.baseURL+path, nil)
	if err != nil {
		return
	}
	s.authorize(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (s smokeHTTP) doJSON(req *http.Request, wantStatus int) map[string]any {
	resp, err := s.client.Do(req)
	if err != nil {
		exitf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		exitf("read %s response: %v", req.URL.Path, err)
	}
	if resp.StatusCode != wantStatus {
		exitf("%s %s status %d: %s", req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		exitf("decode %s response: %v: %s", req.URL.Path, err, strings.TrimSpace(string(raw)))
	}
	return payload
}

func (s smokeHTTP) authorize(req *http.Request) {
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
}

func seedFileSearchFixture(ctx context.Context, httpSmoke smokeHTTP) (string, string) {
	file := httpSmoke.uploadFile(ctx, "codes.txt", "Remember: code=777. Reply OK.\n")
	fileID := asString(file["id"])
	if fileID == "" {
		exitf("file upload missing id: %v", file)
	}
	vectorStore := httpSmoke.postJSON(ctx, "/v1/vector_stores", map[string]any{
		"name":     "responses-websocket-smoke",
		"file_ids": []string{fileID},
	})
	vectorStoreID := asString(vectorStore["id"])
	if vectorStoreID == "" {
		exitf("vector store create missing id: %v", vectorStore)
	}
	return fileID, vectorStoreID
}

func dial(ctx context.Context, baseURL string, apiKey string) (*websocket.Conn, error) {
	wsURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/v1/responses")
	if err != nil {
		return nil, err
	}
	switch wsURL.Scheme {
	case "http":
		wsURL.Scheme = "ws"
	case "https":
		wsURL.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("unsupported base URL scheme %q", wsURL.Scheme)
	}

	header := http.Header{}
	if strings.TrimSpace(apiKey) != "" {
		header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	conn, _, err := websocket.Dial(ctx, wsURL.String(), &websocket.DialOptions{HTTPHeader: header})
	return conn, err
}

func sendResponseCreate(ctx context.Context, conn *websocket.Conn, payload map[string]any) ([]wsEvent, error) {
	body := make(map[string]any, len(payload)+1)
	body["type"] = "response.create"
	for key, value := range payload {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return nil, err
	}

	events := make([]wsEvent, 0, 16)
	for {
		messageType, raw, err := conn.Read(ctx)
		if err != nil {
			return events, err
		}
		if messageType != websocket.MessageText {
			return events, fmt.Errorf("unexpected websocket message type %v", messageType)
		}
		var event wsEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return events, err
		}
		events = append(events, event)
		switch asString(event["type"]) {
		case "response.completed", "response.failed", "response.cancelled", "response.incomplete":
			return events, nil
		case "error":
			return events, fmt.Errorf("websocket error event: %s", strings.TrimSpace(string(raw)))
		}
	}
}

func requireEvent(events []wsEvent, eventType string) wsEvent {
	for _, event := range events {
		if asString(event["type"]) == eventType {
			return event
		}
	}
	exitf("missing event %q in %v", eventType, eventTypes(events))
	return nil
}

func eventTypes(events []wsEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, asString(event["type"]))
	}
	return out
}

func eventResponse(event wsEvent) map[string]any {
	response, ok := event["response"].(map[string]any)
	if !ok {
		exitf("event %q missing response object", asString(event["type"]))
	}
	return response
}

func runFileSearchSmoke(ctx context.Context, conn *websocket.Conn, model string, vectorStoreID string) {
	events, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":       model,
		"store":       true,
		"input":       "What is the code?",
		"tool_choice": "required",
		"tools": []map[string]any{
			{
				"type":             "file_search",
				"vector_store_ids": []string{vectorStoreID},
			},
		},
	})
	if err != nil {
		exitf("file_search response.create: %v", err)
	}
	response := eventResponse(requireEvent(events, "response.completed"))
	requireResponseText(response, "777", "file_search")
	requireOutputType(response, 0, "file_search_call", "file_search")
}

func runWebSearchSmoke(ctx context.Context, conn *websocket.Conn, model string) {
	events, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":       model,
		"store":       true,
		"input":       "Open the fixture guide page and find \"SUPPORTED FIXTURE PHRASE\" in that page. Reply with the exact phrase only.",
		"include":     []string{"web_search_call.action.sources"},
		"tool_choice": "required",
		"tools": []map[string]any{
			{
				"type":                "web_search",
				"search_context_size": "medium",
			},
		},
	})
	if err != nil {
		exitf("web_search response.create: %v", err)
	}
	response := eventResponse(requireEvent(events, "response.completed"))
	if !strings.HasPrefix(asString(response["output_text"]), "SUPPORTED FIXTURE PHRASE") {
		exitf("web_search output_text mismatch: %q", asString(response["output_text"]))
	}
	requireOutputType(response, 0, "web_search_call", "web_search")
}

func runImageGenerationSmoke(ctx context.Context, conn *websocket.Conn, model string) {
	events, err := sendResponseCreate(ctx, conn, map[string]any{
		"model": model,
		"store": true,
		"input": "Generate a tiny orange cat in a teacup.",
		"tool_choice": map[string]any{
			"type": "image_generation",
		},
		"tools": []map[string]any{
			{
				"type":          "image_generation",
				"output_format": "png",
				"quality":       "low",
				"size":          "1024x1024",
			},
		},
	})
	if err != nil {
		exitf("image_generation response.create: %v", err)
	}
	response := eventResponse(requireEvent(events, "response.completed"))
	output := requireOutputMap(response, 0, "image_generation")
	if asString(output["type"]) != "image_generation_call" ||
		asString(output["status"]) != "completed" ||
		asString(output["result"]) == "" ||
		asString(output["revised_prompt"]) == "" {
		exitf("image_generation output mismatch: %v", output)
	}
}

func runMCPSmoke(ctx context.Context, conn *websocket.Conn, model string, fixtureMCPURL string) {
	events, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":       model,
		"store":       true,
		"input":       "Roll 2d4+1 and return only the numeric result.",
		"tool_choice": "required",
		"tools": []map[string]any{
			{
				"type":             "mcp",
				"server_label":     "dmcp",
				"server_url":       fixtureMCPURL,
				"require_approval": "never",
			},
		},
	})
	if err != nil {
		exitf("mcp response.create: %v", err)
	}
	response := eventResponse(requireEvent(events, "response.completed"))
	requireResponseText(response, "4", "mcp")
	requireOutputType(response, 0, "mcp_list_tools", "mcp")
	requireOutputType(response, 1, "mcp_call", "mcp")
	firstID := asString(response["id"])
	if firstID == "" {
		exitf("mcp response missing id")
	}

	followUpEvents, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":                model,
		"store":                true,
		"previous_response_id": firstID,
		"input":                "Roll again and return only the numeric result.",
	})
	if err != nil {
		exitf("mcp follow-up response.create: %v", err)
	}
	followUp := eventResponse(requireEvent(followUpEvents, "response.completed"))
	requireResponseText(followUp, "4", "mcp follow-up")
	requireOutputType(followUp, 0, "mcp_call", "mcp follow-up")
}

func runToolSearchSmoke(ctx context.Context, conn *websocket.Conn, model string) {
	events, err := sendResponseCreate(ctx, conn, map[string]any{
		"model": model,
		"store": true,
		"input": "Find the shipping ETA namespace tool and use it for order_42.",
		"tool_choice": map[string]any{
			"type": "tool_search",
		},
		"tools": []map[string]any{
			{
				"type":        "tool_search",
				"description": "Search deferred project tools.",
			},
			{
				"type":        "namespace",
				"name":        "shipping_ops",
				"description": "Tools for shipping ETA and tracking lookups.",
				"tools": []map[string]any{
					{
						"type":          "function",
						"name":          "get_shipping_eta",
						"description":   "Look up shipping ETA details for an order.",
						"defer_loading": true,
						"parameters": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"order_id": map[string]any{
									"type": "string",
								},
							},
							"required":             []string{"order_id"},
							"additionalProperties": false,
						},
					},
					{
						"type":          "function",
						"name":          "get_tracking_events",
						"description":   "List tracking events for an order.",
						"defer_loading": true,
						"parameters": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"order_id": map[string]any{
									"type": "string",
								},
							},
							"required":             []string{"order_id"},
							"additionalProperties": false,
						},
					},
				},
			},
		},
	})
	if err != nil {
		exitf("tool_search response.create: %v", err)
	}
	response := eventResponse(requireEvent(events, "response.completed"))
	requireResponseText(response, "", "tool_search")
	requireOutputType(response, 0, "tool_search_call", "tool_search")
	requireOutputType(response, 1, "tool_search_output", "tool_search")
	requireOutputType(response, 2, "function_call", "tool_search")
	callID := asString(requireOutputMap(response, 2, "tool_search")["call_id"])
	if callID == "" {
		exitf("tool_search function_call missing call_id")
	}
	firstID := asString(response["id"])
	if firstID == "" {
		exitf("tool_search response missing id")
	}

	followUpEvents, err := sendResponseCreate(ctx, conn, map[string]any{
		"model":                model,
		"store":                true,
		"previous_response_id": firstID,
		"input": []map[string]any{
			{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  "ETA for order_42 is 2026-04-20.",
			},
		},
	})
	if err != nil {
		exitf("tool_search follow-up response.create: %v", err)
	}
	followUp := eventResponse(requireEvent(followUpEvents, "response.completed"))
	requireResponseText(followUp, "ETA for order_42 is 2026-04-20.", "tool_search follow-up")
	requireOutputType(followUp, 0, "message", "tool_search follow-up")
}

func requireResponseText(response map[string]any, want string, label string) {
	if got := asString(response["output_text"]); got != want {
		exitf("%s output_text mismatch: got %q want %q", label, got, want)
	}
}

func requireOutputType(response map[string]any, index int, want string, label string) {
	output := requireOutputMap(response, index, label)
	if got := asString(output["type"]); got != want {
		exitf("%s output[%d].type mismatch: got %q want %q", label, index, got, want)
	}
}

func requireOutputMap(response map[string]any, index int, label string) map[string]any {
	items, ok := response["output"].([]any)
	if !ok {
		exitf("%s response missing output array: %v", label, response["output"])
	}
	if index < 0 || index >= len(items) {
		exitf("%s response output index %d out of range: len=%d", label, index, len(items))
	}
	item, ok := items[index].(map[string]any)
	if !ok {
		exitf("%s response output[%d] is not object: %v", label, index, items[index])
	}
	return item
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
