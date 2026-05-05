package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const fixtureImageBase64 = "ZmFrZS1pbWFnZQ=="

type fixtureProvider struct {
	counter atomic.Uint64
}

type fixturePayload struct {
	Response map[string]any
	Item     map[string]any
}

func (p *fixtureProvider) CheckReady(context.Context) error {
	return nil
}

func (p *fixtureProvider) Create(ctx context.Context, requestBody []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, err := p.buildPayload(requestBody)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload.Response)
}

func (p *fixtureProvider) CreateStream(ctx context.Context, requestBody []byte) (StreamResponse, error) {
	if err := ctx.Err(); err != nil {
		return StreamResponse{}, err
	}
	payload, err := p.buildPayload(requestBody)
	if err != nil {
		return StreamResponse{}, err
	}
	body, err := encodeFixtureStream(payload)
	if err != nil {
		return StreamResponse{}, err
	}
	return StreamResponse{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (p *fixtureProvider) buildPayload(requestBody []byte) (fixturePayload, error) {
	var request map[string]any
	if err := json.Unmarshal(requestBody, &request); err != nil {
		return fixturePayload{}, fmt.Errorf("decode image_generation fixture request: %w", err)
	}

	tool, err := fixtureImageTool(request["tools"])
	if err != nil {
		return fixturePayload{}, err
	}

	n := p.counter.Add(1)
	createdAt := time.Now().Unix()
	responseID := fmt.Sprintf("resp_fixture_image_%d", n)
	itemID := fmt.Sprintf("ig_fixture_image_%d", n)
	model := stringOrDefault(request["model"], "fixture-image-model")
	inputPrompt := strings.TrimSpace(firstText(request["input"]))
	if inputPrompt == "" {
		inputPrompt = "Deterministic fixture image."
	}

	item := map[string]any{
		"id":             itemID,
		"type":           "image_generation_call",
		"status":         "completed",
		"background":     stringOrDefault(tool["background"], "transparent"),
		"output_format":  stringOrDefault(tool["output_format"], "png"),
		"quality":        stringOrDefault(tool["quality"], "low"),
		"size":           stringOrDefault(tool["size"], "1024x1024"),
		"result":         fixtureImageBase64,
		"revised_prompt": inputPrompt,
		"action":         stringOrDefault(tool["action"], "generate"),
	}
	response := map[string]any{
		"id":                   responseID,
		"object":               "response",
		"created_at":           createdAt,
		"status":               "completed",
		"completed_at":         createdAt,
		"error":                nil,
		"incomplete_details":   nil,
		"instructions":         request["instructions"],
		"max_output_tokens":    request["max_output_tokens"],
		"model":                model,
		"output":               []map[string]any{item},
		"parallel_tool_calls":  boolOrDefault(request["parallel_tool_calls"], true),
		"previous_response_id": request["previous_response_id"],
		"reasoning": map[string]any{
			"effort":  nil,
			"summary": nil,
		},
		"store":       false,
		"temperature": float64OrDefault(request["temperature"], 1.0),
		"text": map[string]any{
			"format": map[string]any{
				"type": "text",
			},
		},
		"tool_choice": map[string]any{
			"type": "image_generation",
		},
		"tools":       []map[string]any{tool},
		"top_p":       float64OrDefault(request["top_p"], 1.0),
		"truncation":  stringOrDefault(request["truncation"], "disabled"),
		"usage":       nil,
		"user":        request["user"],
		"metadata":    mapOrEmpty(request["metadata"]),
		"output_text": "",
	}
	return fixturePayload{Response: response, Item: item}, nil
}

func fixtureImageTool(value any) (map[string]any, error) {
	tools, ok := value.([]any)
	if !ok || len(tools) != 1 {
		return nil, errors.New("image_generation fixture backend requires exactly one image_generation tool")
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		return nil, errors.New("image_generation fixture backend requires an object tool")
	}
	if strings.TrimSpace(strings.ToLower(asString(tool["type"]))) != "image_generation" {
		return nil, errors.New("image_generation fixture backend supports only image_generation tools")
	}
	copied := make(map[string]any, len(tool))
	for key, value := range tool {
		copied[key] = value
	}
	return copied, nil
}

func encodeFixtureStream(payload fixturePayload) (string, error) {
	created := cloneMap(payload.Response)
	created["status"] = "in_progress"
	created["completed_at"] = nil
	created["output"] = []map[string]any{}

	itemStarted := cloneMap(payload.Item)
	itemStarted["status"] = "in_progress"
	delete(itemStarted, "result")

	sequence := 0
	var builder strings.Builder
	write := func(eventType string, value map[string]any) error {
		sequence++
		value["sequence_number"] = sequence
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		builder.WriteString("event: ")
		builder.WriteString(eventType)
		builder.WriteString("\n")
		builder.WriteString("data: ")
		builder.Write(data)
		builder.WriteString("\n\n")
		return nil
	}

	events := []struct {
		eventType string
		payload   map[string]any
	}{
		{
			eventType: "response.created",
			payload: map[string]any{
				"type":     "response.created",
				"response": created,
			},
		},
		{
			eventType: "response.output_item.added",
			payload: map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item":         itemStarted,
			},
		},
		{
			eventType: "response.image_generation_call.in_progress",
			payload: map[string]any{
				"type":         "response.image_generation_call.in_progress",
				"output_index": 0,
				"item_id":      payload.Item["id"],
			},
		},
		{
			eventType: "response.image_generation_call.generating",
			payload: map[string]any{
				"type":         "response.image_generation_call.generating",
				"output_index": 0,
				"item_id":      payload.Item["id"],
			},
		},
		{
			eventType: "response.image_generation_call.partial_image",
			payload: map[string]any{
				"type":                "response.image_generation_call.partial_image",
				"output_index":        0,
				"item_id":             payload.Item["id"],
				"partial_image_index": 0,
				"partial_image_b64":   "cGFydGlhbC0w",
			},
		},
		{
			eventType: "response.image_generation_call.partial_image",
			payload: map[string]any{
				"type":                "response.image_generation_call.partial_image",
				"output_index":        0,
				"item_id":             payload.Item["id"],
				"partial_image_index": 1,
				"partial_image_b64":   "cGFydGlhbC0x",
			},
		},
		{
			eventType: "response.output_item.done",
			payload: map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         payload.Item,
			},
		},
		{
			eventType: "response.completed",
			payload: map[string]any{
				"type":     "response.completed",
				"response": payload.Response,
			},
		},
	}
	for _, event := range events {
		if err := write(event.eventType, event.payload); err != nil {
			return "", err
		}
	}
	return builder.String(), nil
}

func firstText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		for _, item := range typed {
			if text := firstText(item); text != "" {
				return text
			}
		}
	case map[string]any:
		if text := asString(typed["text"]); text != "" {
			return text
		}
		if content, ok := typed["content"]; ok {
			return firstText(content)
		}
	}
	return ""
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func stringOrDefault(value any, fallback string) string {
	if text := strings.TrimSpace(asString(value)); text != "" {
		return text
	}
	return fallback
}

func boolOrDefault(value any, fallback bool) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	return fallback
}

func float64OrDefault(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return fallback
	}
}

func mapOrEmpty(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok && typed != nil {
		return typed
	}
	return map[string]any{}
}

func cloneMap(value map[string]any) map[string]any {
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
