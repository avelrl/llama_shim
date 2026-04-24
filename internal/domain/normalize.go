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

func NormalizeInput(raw json.RawMessage) ([]Item, error) {
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
		return []Item{NewInputTextMessage("user", text)}, nil
	case '{':
		item, err := normalizeItemObject(trimmed, "input", "input")
		if err != nil {
			return nil, err
		}
		return []Item{item}, nil
	case '[':
		var rawItems []json.RawMessage
		if err := json.Unmarshal(trimmed, &rawItems); err != nil {
			return nil, fmt.Errorf("decode array input: %w", err)
		}
		if len(rawItems) == 0 {
			return nil, NewValidationError("input", "input array must not be empty")
		}

		items := make([]Item, 0, len(rawItems))
		for _, rawItem := range rawItems {
			item, err := normalizeItemObject(rawItem, "input", "input")
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

func NormalizeConversationItems(rawItems []json.RawMessage) ([]Item, error) {
	if len(rawItems) == 0 {
		return nil, NewValidationError("items", "items must not be empty")
	}

	items := make([]Item, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, err := normalizeItemObject(rawItem, "conversation", "items")
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, nil
}

func NewInputTextMessage(role, text string) Item {
	item, _ := NewItem([]byte(fmt.Sprintf(`{"type":"message","role":%q,"content":[{"type":"input_text","text":%q}]}`, role, text)))
	return item
}

func NewOutputTextMessage(text string) Item {
	item, _ := NewItem([]byte(fmt.Sprintf(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}`, text)))
	return item
}

func CompactJSON(raw []byte) (string, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func normalizeItemObject(raw json.RawMessage, source, param string) (Item, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Item{}, fmt.Errorf("decode item: %w", err)
	}

	itemType := strings.TrimSpace(asString(payload["type"]))
	if itemType == "" {
		itemType = "message"
		payload["type"] = itemType
	}

	switch itemType {
	case "message":
		role := strings.TrimSpace(asString(payload["role"]))
		if role == "" {
			if source == "input" {
				role = "user"
				payload["role"] = role
			} else {
				return Item{}, NewValidationError("role", "role is required")
			}
		}
		switch role {
		case "system", "developer", "user", "assistant":
		default:
			return Item{}, NewValidationError("role", "unsupported role")
		}
		content, ok := payload["content"]
		if !ok || content == nil {
			return Item{}, NewValidationError("content", "content is required")
		}
		if _, err := normalizeMessageContent(content); err != nil {
			return Item{}, err
		}
	case "function_call":
		if strings.TrimSpace(asString(payload["name"])) == "" {
			return Item{}, NewValidationError(param, "function_call name is required")
		}
		if _, ok := payload["arguments"]; !ok {
			return Item{}, NewValidationError(param, "function_call arguments are required")
		}
	case "custom_tool_call":
		if strings.TrimSpace(asString(payload["name"])) == "" {
			return Item{}, NewValidationError(param, "custom_tool_call name is required")
		}
		if _, ok := payload["input"]; !ok {
			return Item{}, NewValidationError(param, "custom_tool_call input is required")
		}
	case "shell_call":
		if strings.TrimSpace(asString(payload["call_id"])) == "" {
			return Item{}, NewValidationError(param, "shell_call call_id is required")
		}
		if _, ok := payload["action"]; !ok {
			return Item{}, NewValidationError(param, "shell_call action is required")
		}
	case "apply_patch_call":
		if strings.TrimSpace(asString(payload["call_id"])) == "" {
			return Item{}, NewValidationError(param, "apply_patch_call call_id is required")
		}
		if _, ok := payload["operation"]; !ok {
			return Item{}, NewValidationError(param, "apply_patch_call operation is required")
		}
	case "function_call_output", "custom_tool_call_output":
		if strings.TrimSpace(asString(payload["call_id"])) == "" {
			return Item{}, NewValidationError(param, itemType+" call_id is required")
		}
		if _, ok := payload["output"]; !ok {
			return Item{}, NewValidationError(param, itemType+" output is required")
		}
	case "shell_call_output":
		if strings.TrimSpace(asString(payload["call_id"])) == "" {
			return Item{}, NewValidationError(param, "shell_call_output call_id is required")
		}
		if _, ok := payload["output"]; ok {
			break
		}
		return Item{}, NewValidationError(param, "shell_call_output output is required")
	case "apply_patch_call_output":
		if strings.TrimSpace(asString(payload["call_id"])) == "" {
			return Item{}, NewValidationError(param, "apply_patch_call_output call_id is required")
		}
		if strings.TrimSpace(asString(payload["status"])) == "" {
			return Item{}, NewValidationError(param, "apply_patch_call_output status is required")
		}
	case "reasoning":
	default:
		// Unknown item families are preserved losslessly so they can still be
		// stored, replayed upstream, and returned from read APIs.
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		return Item{}, fmt.Errorf("marshal normalized item: %w", err)
	}
	item, err := NewItem(normalized)
	if err != nil {
		return Item{}, err
	}
	switch item.Type {
	case "custom_tool_call":
		item.Meta = &ItemMeta{
			Transport:     "passthrough",
			CanonicalType: "custom_tool_call",
			ToolName:      item.Name(),
			ToolNamespace: item.Namespace(),
		}
	case "custom_tool_call_output":
		item.Meta = &ItemMeta{
			Transport:     "passthrough",
			CanonicalType: "custom_tool_call_output",
		}
	case "shell_call":
		item.Meta = &ItemMeta{
			Transport:     "local_builtin",
			CanonicalType: "shell_call",
			ToolName:      "shell",
		}
	case "shell_call_output":
		item.Meta = &ItemMeta{
			Transport:     "local_builtin",
			CanonicalType: "shell_call_output",
			ToolName:      "shell",
		}
	case "apply_patch_call":
		item.Meta = &ItemMeta{
			Transport:     "local_builtin",
			CanonicalType: "apply_patch_call",
			ToolName:      "apply_patch",
		}
	case "apply_patch_call_output":
		item.Meta = &ItemMeta{
			Transport:     "local_builtin",
			CanonicalType: "apply_patch_call_output",
			ToolName:      "apply_patch",
		}
	}
	return item, nil
}

func normalizeMessageContent(value any) ([]TextPart, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
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
		var rawParts []map[string]any
		if err := json.Unmarshal(trimmed, &rawParts); err != nil {
			return nil, fmt.Errorf("decode content array: %w", err)
		}
		if len(rawParts) == 0 {
			return nil, NewValidationError("content", "content array must not be empty")
		}
		parts := make([]TextPart, 0, len(rawParts))
		for _, rawPart := range rawParts {
			partType := strings.TrimSpace(asString(rawPart["type"]))
			switch partType {
			case "", "text", "input_text", "output_text", "input_image", "input_file", "file", "image":
			default:
				// Preserve unknown content parts losslessly as long as the array shape is valid.
			}
			if text := strings.TrimSpace(asString(rawPart["text"])); text != "" {
				if partType == "" {
					partType = "text"
				}
				parts = append(parts, TextPart{Type: partType, Text: text})
			}
		}
		return parts, nil
	default:
		return nil, NewValidationError("content", "unsupported content shape")
	}
}

func MessageText(item Item) string {
	if item.Type != "message" {
		return ""
	}
	var parts []string
	for _, part := range item.Content {
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, "")
}
