package httpapi

import (
	"bytes"
	"encoding/json"
	"strings"

	"llama_shim/internal/domain"
)

type responseStreamOptions struct {
	IncludeObfuscation bool
}

type responseStreamOptionsPayload struct {
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
}

func parseCreateResponseStreamOptions(stream *bool, raw json.RawMessage) (responseStreamOptions, error) {
	options := responseStreamOptions{IncludeObfuscation: true}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return options, nil
	}
	if stream == nil || !*stream {
		return responseStreamOptions{}, domain.NewValidationError("stream_options", "stream_options is only supported when stream=true")
	}

	fields, err := decodeRawFields(trimmed)
	if err != nil {
		return responseStreamOptions{}, domain.NewValidationError("stream_options", "stream_options must be an object")
	}
	for key := range fields {
		if key == "include_obfuscation" {
			continue
		}
		return responseStreamOptions{}, domain.NewValidationError("stream_options", "unsupported stream_options field: "+key)
	}

	var payload responseStreamOptionsPayload
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return responseStreamOptions{}, domain.NewValidationError("stream_options", "stream_options must be an object")
	}
	if payload.IncludeObfuscation != nil {
		options.IncludeObfuscation = *payload.IncludeObfuscation
	}
	return options, nil
}

func normalizeResponseForStreaming(response domain.Response, preferredItemIDs map[int]string) (domain.Response, error) {
	normalized := response
	outputItems := responseReplayOutputItems(response)
	if len(outputItems) == 0 {
		return normalized, nil
	}

	outputItems = append([]domain.Item(nil), outputItems...)
	for index, item := range outputItems {
		payload := item.Map()
		if len(payload) == 0 {
			payload = map[string]any{}
		}

		if preferredID := strings.TrimSpace(preferredItemIDs[index]); preferredID != "" {
			payload["id"] = preferredID
		}
		if strings.TrimSpace(asString(payload["type"])) == "" {
			payload["type"] = fallbackString(item.Type, "message")
		}
		if strings.TrimSpace(asString(payload["type"])) == "message" {
			if strings.TrimSpace(asString(payload["role"])) == "" {
				payload["role"] = fallbackString(item.Role, "assistant")
			}
			if strings.TrimSpace(asString(payload["status"])) == "" {
				payload["status"] = "completed"
			}
			payload["content"] = normalizeStreamingMessageContent(payload["content"], response.OutputText)
		}

		rewritten, err := item.WithMap(payload)
		if err != nil {
			return domain.Response{}, err
		}
		outputItems[index] = rewritten
	}

	outputItems, err := domain.EnsureItemIDs(outputItems)
	if err != nil {
		return domain.Response{}, err
	}

	normalized.Output = outputItems
	if strings.TrimSpace(normalized.OutputText) == "" && len(outputItems) > 0 {
		normalized.OutputText = domain.MessageText(outputItems[0])
	}
	return normalized, nil
}

func normalizeStreamingMessageContent(raw any, fallbackText string) []map[string]any {
	content, ok := raw.([]any)
	if !ok || len(content) == 0 {
		return []map[string]any{{
			"type":        "output_text",
			"text":        fallbackText,
			"annotations": []any{},
		}}
	}

	parts := make([]map[string]any, 0, len(content))
	for _, entry := range content {
		part, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cloned := cloneReplayMap(part)
		if strings.TrimSpace(asString(cloned["type"])) == "output_text" {
			if _, ok := cloned["annotations"]; !ok {
				cloned["annotations"] = []any{}
			}
		}
		parts = append(parts, cloned)
	}
	if len(parts) == 0 {
		return []map[string]any{{
			"type":        "output_text",
			"text":        fallbackText,
			"annotations": []any{},
		}}
	}
	return parts
}
