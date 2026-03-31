package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupportedShape = errors.New("unsupported input shape")

type ValidationError struct {
	Message string
	Param   string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func NewValidationError(param, message string) error {
	return &ValidationError{Param: param, Message: message}
}

func NormalizeInput(raw json.RawMessage) ([]MessageItem, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, NewValidationError("input", "input is required")
	}

	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, fmt.Errorf("decode string input: %w", err)
		}
		return []MessageItem{NewInputTextMessage("user", text)}, nil
	case '{':
		item, err := normalizeMessageObject(trimmed, "input")
		if err != nil {
			return nil, err
		}
		return []MessageItem{item}, nil
	case '[':
		var rawItems []json.RawMessage
		if err := json.Unmarshal(trimmed, &rawItems); err != nil {
			return nil, fmt.Errorf("decode array input: %w", err)
		}
		if len(rawItems) == 0 {
			return nil, NewValidationError("input", "input array must not be empty")
		}

		items := make([]MessageItem, 0, len(rawItems))
		for _, rawItem := range rawItems {
			item, err := normalizeMessageObject(rawItem, "input")
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, NewValidationError("input", "unsupported input shape")
	}
}

func NormalizeConversationItems(rawItems []json.RawMessage) ([]MessageItem, error) {
	if len(rawItems) == 0 {
		return nil, NewValidationError("items", "items must not be empty")
	}

	items := make([]MessageItem, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, err := normalizeMessageObject(rawItem, "conversation")
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, nil
}

func NewInputTextMessage(role, text string) MessageItem {
	return MessageItem{
		Type: "message",
		Role: role,
		Content: []TextPart{
			{
				Type: "input_text",
				Text: text,
			},
		},
	}
}

func NewOutputTextMessage(text string) MessageItem {
	return MessageItem{
		Type: "message",
		Role: "assistant",
		Content: []TextPart{
			{
				Type: "output_text",
				Text: text,
			},
		},
	}
}

func CompactJSON(raw []byte) (string, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func normalizeMessageObject(raw json.RawMessage, source string) (MessageItem, error) {
	var payload struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return MessageItem{}, fmt.Errorf("decode message item: %w", err)
	}

	itemType := strings.TrimSpace(payload.Type)
	if itemType == "" {
		itemType = "message"
	}
	if itemType != "message" {
		return MessageItem{}, NewValidationError("input", "only message items are supported in v1")
	}

	role := strings.TrimSpace(payload.Role)
	if role == "" {
		if source == "input" {
			role = "user"
		} else {
			return MessageItem{}, NewValidationError("role", "role is required")
		}
	}
	switch role {
	case "system", "user", "assistant":
	default:
		return MessageItem{}, NewValidationError("role", "unsupported role")
	}

	content, err := normalizeContent(payload.Content)
	if err != nil {
		return MessageItem{}, err
	}

	return MessageItem{
		Type:    "message",
		Role:    role,
		Content: content,
	}, nil
}

func normalizeContent(raw json.RawMessage) ([]TextPart, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, NewValidationError("content", "content is required")
	}

	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, fmt.Errorf("decode string content: %w", err)
		}
		return []TextPart{{Type: "input_text", Text: text}}, nil
	case '[':
		var rawParts []json.RawMessage
		if err := json.Unmarshal(trimmed, &rawParts); err != nil {
			return nil, fmt.Errorf("decode content array: %w", err)
		}
		if len(rawParts) == 0 {
			return nil, NewValidationError("content", "content array must not be empty")
		}

		parts := make([]TextPart, 0, len(rawParts))
		for _, rawPart := range rawParts {
			part, err := normalizeContentPart(rawPart)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		return parts, nil
	default:
		return nil, NewValidationError("content", "unsupported content shape")
	}
}

func normalizeContentPart(raw json.RawMessage) (TextPart, error) {
	var payload struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return TextPart{}, fmt.Errorf("decode content part: %w", err)
	}

	partType := strings.TrimSpace(payload.Type)
	switch partType {
	case "", "text", "input_text", "output_text":
	default:
		return TextPart{}, NewValidationError("content", "only text content is supported in v1")
	}

	return TextPart{
		Type: "input_text",
		Text: payload.Text,
	}, nil
}

func MessageText(item MessageItem) string {
	var parts []string
	for _, part := range item.Content {
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, "")
}
