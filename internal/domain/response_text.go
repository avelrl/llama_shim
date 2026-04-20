package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ResponseTextConfig struct {
	Format ResponseTextFormat `json:"format"`
}

type ResponseTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

func DefaultResponseTextConfig() ResponseTextConfig {
	return ResponseTextConfig{
		Format: ResponseTextFormat{Type: "text"},
	}
}

func ParseResponseTextConfig(raw json.RawMessage) (ResponseTextConfig, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return DefaultResponseTextConfig(), nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return ResponseTextConfig{}, NewValidationError("text", "text must be an object")
	}
	if len(payload) == 0 {
		return DefaultResponseTextConfig(), nil
	}

	for key := range payload {
		if key != "format" {
			return ResponseTextConfig{}, NewValidationError("text", "unsupported text field "+`"`+key+`"`)
		}
	}

	formatRaw, ok := payload["format"]
	if !ok || len(bytes.TrimSpace(formatRaw)) == 0 || bytes.Equal(bytes.TrimSpace(formatRaw), []byte("null")) {
		return DefaultResponseTextConfig(), nil
	}

	format, err := parseResponseTextFormat(formatRaw)
	if err != nil {
		return ResponseTextConfig{}, err
	}

	return ResponseTextConfig{Format: format}, nil
}

func MarshalResponseTextConfig(config ResponseTextConfig) json.RawMessage {
	raw, err := json.Marshal(config)
	if err != nil {
		return mustMarshalDefaultResponseTextConfig()
	}
	return raw
}

func InferResponseTextConfigFromRequestJSON(requestJSON string) json.RawMessage {
	if strings.TrimSpace(requestJSON) == "" {
		return mustMarshalDefaultResponseTextConfig()
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(requestJSON), &payload); err != nil {
		return mustMarshalDefaultResponseTextConfig()
	}

	config, err := ParseResponseTextConfig(payload["text"])
	if err != nil {
		return mustMarshalDefaultResponseTextConfig()
	}
	return MarshalResponseTextConfig(config)
}

func parseResponseTextFormat(raw json.RawMessage) (ResponseTextFormat, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ResponseTextFormat{}, NewValidationError("text.format", "text.format must be an object")
	}

	typeName := "text"
	if rawType, ok := payload["type"]; ok && len(bytes.TrimSpace(rawType)) > 0 {
		if err := json.Unmarshal(rawType, &typeName); err != nil {
			return ResponseTextFormat{}, NewValidationError("text.format.type", "text.format.type must be a string")
		}
		typeName = strings.TrimSpace(typeName)
	}

	format := ResponseTextFormat{Type: typeName}
	switch typeName {
	case "text":
		if err := rejectUnsupportedResponseTextFormatKeys(payload, map[string]struct{}{"type": {}}); err != nil {
			return ResponseTextFormat{}, err
		}
	case "json_object":
		if err := rejectUnsupportedResponseTextFormatKeys(payload, map[string]struct{}{"type": {}}); err != nil {
			return ResponseTextFormat{}, err
		}
	case "json_schema":
		if err := rejectUnsupportedResponseTextFormatKeys(payload, map[string]struct{}{
			"type":   {},
			"name":   {},
			"strict": {},
			"schema": {},
		}); err != nil {
			return ResponseTextFormat{}, err
		}
		if rawName, ok := payload["name"]; ok && len(bytes.TrimSpace(rawName)) > 0 && !bytes.Equal(bytes.TrimSpace(rawName), []byte("null")) {
			var name string
			if err := json.Unmarshal(rawName, &name); err != nil {
				return ResponseTextFormat{}, NewValidationError("text.format.name", "text.format.name must be a string")
			}
			format.Name = strings.TrimSpace(name)
		}
		if rawStrict, ok := payload["strict"]; ok && len(bytes.TrimSpace(rawStrict)) > 0 && !bytes.Equal(bytes.TrimSpace(rawStrict), []byte("null")) {
			var strict bool
			if err := json.Unmarshal(rawStrict, &strict); err != nil {
				return ResponseTextFormat{}, NewValidationError("text.format.strict", "text.format.strict must be a boolean")
			}
			format.Strict = &strict
		}
		schemaRaw, ok := payload["schema"]
		if !ok || len(bytes.TrimSpace(schemaRaw)) == 0 || bytes.Equal(bytes.TrimSpace(schemaRaw), []byte("null")) {
			return ResponseTextFormat{}, NewValidationError("text.format.schema", "text.format.schema is required when text.format.type=json_schema")
		}
		schemaRaw = bytes.TrimSpace(schemaRaw)
		if !bytes.HasPrefix(schemaRaw, []byte("{")) {
			return ResponseTextFormat{}, NewValidationError("text.format.schema", "text.format.schema must be an object")
		}
		if err := validateSupportedResponseJSONSchema(schemaRaw, "$"); err != nil {
			return ResponseTextFormat{}, err
		}
		format.Schema = append(json.RawMessage(nil), schemaRaw...)
	default:
		return ResponseTextFormat{}, NewValidationError("text.format.type", "unsupported text.format.type")
	}

	return format, nil
}

func rejectUnsupportedResponseTextFormatKeys(payload map[string]json.RawMessage, allowed map[string]struct{}) error {
	unsupported := make([]string, 0)
	for key := range payload {
		if _, ok := allowed[key]; ok {
			continue
		}
		unsupported = append(unsupported, key)
	}
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return NewValidationError("text.format", "unsupported text.format field(s): "+strings.Join(unsupported, ", "))
}

func validateSupportedResponseJSONSchema(raw json.RawMessage, path string) error {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return NewValidationError("text.format.schema", "text.format.schema must be valid JSON")
	}
	return validateSupportedResponseJSONSchemaNode(payload, path)
}

func validateSupportedResponseJSONSchemaNode(payload map[string]any, path string) error {
	typeName := strings.TrimSpace(asString(payload["type"]))
	if typeName == "" {
		return NewValidationError("text.format.schema", fmt.Sprintf("%s.type is required", path))
	}

	allowedKeys := map[string]struct{}{
		"type":        {},
		"title":       {},
		"description": {},
		"enum":        {},
	}
	switch typeName {
	case "object":
		allowedKeys["properties"] = struct{}{}
		allowedKeys["required"] = struct{}{}
		allowedKeys["additionalProperties"] = struct{}{}
	case "array":
		allowedKeys["items"] = struct{}{}
	case "string", "number", "integer", "boolean", "null":
	default:
		return NewValidationError("text.format.schema", fmt.Sprintf("unsupported schema type %q at %s", typeName, path))
	}

	for key := range payload {
		if _, ok := allowedKeys[key]; ok {
			continue
		}
		return NewValidationError("text.format.schema", fmt.Sprintf("schema feature %q at %s is not supported by shim-local responses", key, path))
	}

	if enumValue, ok := payload["enum"]; ok {
		enumItems, ok := enumValue.([]any)
		if !ok || len(enumItems) == 0 {
			return NewValidationError("text.format.schema", fmt.Sprintf("%s.enum must be a non-empty array", path))
		}
	}

	switch typeName {
	case "object":
		if rawAdditional, ok := payload["additionalProperties"]; ok {
			if _, ok := rawAdditional.(bool); !ok {
				return NewValidationError("text.format.schema", fmt.Sprintf("%s.additionalProperties must be a boolean", path))
			}
		}
		properties := map[string]any{}
		if rawProperties, ok := payload["properties"]; ok {
			props, ok := rawProperties.(map[string]any)
			if !ok {
				return NewValidationError("text.format.schema", fmt.Sprintf("%s.properties must be an object", path))
			}
			properties = props
			for name, rawChild := range props {
				child, ok := rawChild.(map[string]any)
				if !ok {
					return NewValidationError("text.format.schema", fmt.Sprintf("%s.properties.%s must be an object", path, name))
				}
				if err := validateSupportedResponseJSONSchemaNode(child, path+".properties."+name); err != nil {
					return err
				}
			}
		}
		if rawRequired, ok := payload["required"]; ok {
			required, ok := rawRequired.([]any)
			if !ok {
				return NewValidationError("text.format.schema", fmt.Sprintf("%s.required must be an array of strings", path))
			}
			for _, entry := range required {
				name := strings.TrimSpace(asString(entry))
				if name == "" {
					return NewValidationError("text.format.schema", fmt.Sprintf("%s.required must be an array of strings", path))
				}
				if len(properties) > 0 {
					if _, ok := properties[name]; !ok {
						return NewValidationError("text.format.schema", fmt.Sprintf("%s.required references unknown property %q", path, name))
					}
				}
			}
		}
	case "array":
		rawItems, ok := payload["items"]
		if !ok {
			return NewValidationError("text.format.schema", fmt.Sprintf("%s.items is required for array schemas", path))
		}
		items, ok := rawItems.(map[string]any)
		if !ok {
			return NewValidationError("text.format.schema", fmt.Sprintf("%s.items must be an object", path))
		}
		if err := validateSupportedResponseJSONSchemaNode(items, path+".items"); err != nil {
			return err
		}
	}

	return nil
}

func NormalizeStructuredOutputJSONText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}
	if unwrapped, ok := unwrapStructuredOutputJSONFence(trimmed); ok && json.Valid([]byte(unwrapped)) {
		return unwrapped
	}
	return trimmed
}

func unwrapStructuredOutputJSONFence(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "```") || !strings.HasSuffix(raw, "```") {
		return "", false
	}

	body := strings.TrimPrefix(raw, "```")
	newlineIdx := strings.IndexByte(body, '\n')
	if newlineIdx < 0 {
		return "", false
	}

	info := strings.TrimSpace(strings.TrimRight(body[:newlineIdx], "\r"))
	if info != "" && !strings.EqualFold(info, "json") {
		return "", false
	}

	content := strings.TrimSuffix(body[newlineIdx+1:], "```")
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	return content, true
}

func mustMarshalDefaultResponseTextConfig() json.RawMessage {
	raw, _ := json.Marshal(DefaultResponseTextConfig())
	return raw
}
