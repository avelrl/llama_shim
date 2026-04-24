package httpapi

import (
	"bytes"
	"encoding/json"
	"strings"

	"llama_shim/internal/domain"
)

const (
	localBuiltinShellToolType            = "shell"
	localBuiltinApplyPatchToolType       = "apply_patch"
	localBuiltinShellCallType            = "shell_call"
	localBuiltinShellCallOutputType      = "shell_call_output"
	localBuiltinApplyPatchCallType       = "apply_patch_call"
	localBuiltinApplyPatchCallOutputType = "apply_patch_call_output"

	localBuiltinShellSyntheticName      = "__llama_shim_builtin_shell"
	localBuiltinApplyPatchSyntheticName = "__llama_shim_builtin_apply_patch"
)

type localBuiltinToolDescriptor struct {
	ToolType      string
	SyntheticName string
	CallType      string
	OutputType    string
}

func isLocalBuiltinToolType(value string) bool {
	_, ok := localBuiltinToolDescriptorForType(value)
	return ok
}

func isLocalBuiltinToolCallType(value string) bool {
	_, ok := localBuiltinToolDescriptorForCallType(value)
	return ok
}

func isLocalBuiltinToolOutputType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case localBuiltinShellCallOutputType, localBuiltinApplyPatchCallOutputType:
		return true
	default:
		return false
	}
}

func localBuiltinToolDescriptorForType(toolType string) (localBuiltinToolDescriptor, bool) {
	switch strings.ToLower(strings.TrimSpace(toolType)) {
	case localBuiltinShellToolType:
		return localBuiltinToolDescriptor{
			ToolType:      localBuiltinShellToolType,
			SyntheticName: localBuiltinShellSyntheticName,
			CallType:      localBuiltinShellCallType,
			OutputType:    localBuiltinShellCallOutputType,
		}, true
	case localBuiltinApplyPatchToolType:
		return localBuiltinToolDescriptor{
			ToolType:      localBuiltinApplyPatchToolType,
			SyntheticName: localBuiltinApplyPatchSyntheticName,
			CallType:      localBuiltinApplyPatchCallType,
			OutputType:    localBuiltinApplyPatchCallOutputType,
		}, true
	default:
		return localBuiltinToolDescriptor{}, false
	}
}

func localBuiltinToolDescriptorForSyntheticName(name string) (localBuiltinToolDescriptor, bool) {
	switch strings.TrimSpace(name) {
	case localBuiltinShellSyntheticName:
		return localBuiltinToolDescriptorForType(localBuiltinShellToolType)
	case localBuiltinApplyPatchSyntheticName:
		return localBuiltinToolDescriptorForType(localBuiltinApplyPatchToolType)
	default:
		return localBuiltinToolDescriptor{}, false
	}
}

func localBuiltinToolDescriptorForCallType(itemType string) (localBuiltinToolDescriptor, bool) {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case localBuiltinShellCallType, localBuiltinShellCallOutputType:
		return localBuiltinToolDescriptorForType(localBuiltinShellToolType)
	case localBuiltinApplyPatchCallType, localBuiltinApplyPatchCallOutputType:
		return localBuiltinToolDescriptorForType(localBuiltinApplyPatchToolType)
	default:
		return localBuiltinToolDescriptor{}, false
	}
}

func localBuiltinToolChoiceKey(value string) string {
	return "builtin:" + strings.ToLower(strings.TrimSpace(value))
}

func buildLocalBuiltinToolDefinition(tool map[string]any) (map[string]any, string, error) {
	descriptor, ok := localBuiltinToolDescriptorForType(asString(tool["type"]))
	if !ok {
		return nil, "", domain.NewValidationError("tools", "unsupported builtin tool type")
	}

	switch descriptor.ToolType {
	case localBuiltinShellToolType:
		definition, err := buildLocalShellToolDefinition(tool, descriptor)
		return definition, descriptor.SyntheticName, err
	case localBuiltinApplyPatchToolType:
		definition, err := buildLocalApplyPatchToolDefinition(tool, descriptor)
		return definition, descriptor.SyntheticName, err
	default:
		return nil, "", domain.NewValidationError("tools", "unsupported builtin tool type")
	}
}

func buildLocalShellToolDefinition(tool map[string]any, descriptor localBuiltinToolDescriptor) (map[string]any, error) {
	environment, ok := tool["environment"].(map[string]any)
	if !ok {
		return nil, domain.NewValidationError("tools", `shell tool requires environment.type "local" in shim-local mode`)
	}
	if !strings.EqualFold(strings.TrimSpace(asString(environment["type"])), "local") {
		return nil, domain.NewValidationError("tools", `shell tool requires environment.type "local" in shim-local mode`)
	}

	description := appendToolDescriptionHint(
		strings.TrimSpace(asString(tool["description"])),
		"Return the next non-interactive local shell action in `action` with `commands`, optional `timeout_ms`, and optional `max_output_length`.",
	)

	function := map[string]any{
		"name": descriptor.SyntheticName,
		"parameters": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"action": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"commands": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "string",
							},
							"description": "One or more non-interactive shell commands to execute.",
						},
						"timeout_ms": map[string]any{
							"type":        "integer",
							"description": "Optional timeout in milliseconds.",
						},
						"max_output_length": map[string]any{
							"type":        "integer",
							"description": "Optional maximum output length hint in bytes.",
						},
					},
					"required": []string{"commands"},
				},
			},
			"required": []string{"action"},
		},
	}
	if description != "" {
		function["description"] = description
	}

	return map[string]any{
		"type":     "function",
		"function": function,
	}, nil
}

func buildLocalApplyPatchToolDefinition(tool map[string]any, descriptor localBuiltinToolDescriptor) (map[string]any, error) {
	description := appendToolDescriptionHint(
		strings.TrimSpace(asString(tool["description"])),
		"Return exactly one patch operation in `operation` with `type`, `path`, and optional `diff`.",
	)

	function := map[string]any{
		"name": descriptor.SyntheticName,
		"parameters": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"operation": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"type": map[string]any{
							"type": "string",
							"enum": []string{"create_file", "update_file", "delete_file"},
						},
						"path": map[string]any{
							"type": "string",
						},
						"diff": map[string]any{
							"type":        "string",
							"description": "V4A diff for create_file and update_file operations.",
						},
					},
					"required": []string{"type", "path"},
				},
			},
			"required": []string{"operation"},
		},
	}
	if description != "" {
		function["description"] = description
	}

	return map[string]any{
		"type":     "function",
		"function": function,
	}, nil
}

func localBuiltinToolCallName(item domain.Item) string {
	if item.Meta != nil && strings.TrimSpace(item.Meta.SyntheticName) != "" {
		return strings.TrimSpace(item.Meta.SyntheticName)
	}
	descriptor, ok := localBuiltinToolDescriptorForCallType(item.Type)
	if !ok {
		return ""
	}
	return descriptor.SyntheticName
}

func localBuiltinToolItemMeta(itemType string) *domain.ItemMeta {
	descriptor, ok := localBuiltinToolDescriptorForCallType(itemType)
	if !ok {
		return nil
	}
	return &domain.ItemMeta{
		Transport:     "local_builtin",
		SyntheticName: descriptor.SyntheticName,
		CanonicalType: itemType,
		ToolName:      descriptor.ToolType,
	}
}

func decodeLocalBuiltinToolArguments(raw json.RawMessage) (map[string]any, error) {
	normalized := strings.TrimSpace(normalizeJSONStringField(raw))
	if normalized == "" {
		return map[string]any{}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(normalized), &payload); err != nil {
		return nil, domain.NewValidationError("tools", "builtin tool call arguments must be valid JSON")
	}
	return payload, nil
}

func remapFunctionCallItemToLocalBuiltin(item map[string]any) (map[string]any, *domain.ItemMeta, bool, error) {
	if strings.TrimSpace(asString(item["type"])) != "function_call" {
		return nil, nil, false, nil
	}

	descriptor, ok := localBuiltinToolDescriptorForSyntheticName(asString(item["name"]))
	if !ok {
		return nil, nil, false, nil
	}

	rawArguments, err := json.Marshal(item["arguments"])
	if err != nil {
		return nil, nil, false, err
	}
	arguments, err := decodeLocalBuiltinToolArguments(rawArguments)
	if err != nil {
		return nil, nil, false, err
	}

	payload := map[string]any{
		"type":   descriptor.CallType,
		"status": strings.TrimSpace(asString(item["status"])),
	}
	if payload["status"] == "" {
		payload["status"] = "completed"
	}
	if id := strings.TrimSpace(asString(item["id"])); id != "" {
		payload["id"] = id
	}
	if callID := customToolCallID(item); callID != "" {
		payload["call_id"] = callID
	}

	switch descriptor.ToolType {
	case localBuiltinShellToolType:
		action, _ := arguments["action"].(map[string]any)
		if action == nil {
			command := strings.TrimSpace(asString(arguments["command"]))
			if command == "" {
				command = strings.TrimSpace(asString(arguments["cmd"]))
			}
			if command != "" {
				action = map[string]any{
					"commands": []string{command},
				}
				if timeout, ok := arguments["timeout_ms"]; ok && timeout != nil {
					action["timeout_ms"] = timeout
				} else if timeout, ok := arguments["timeout"]; ok && timeout != nil {
					action["timeout_ms"] = timeout
				}
				if maxOutputLength, ok := arguments["max_output_length"]; ok && maxOutputLength != nil {
					action["max_output_length"] = maxOutputLength
				}
			}
		}
		if action == nil {
			return nil, nil, false, domain.NewValidationError("tools", "shell tool call action is required")
		}
		payload["action"] = action
	case localBuiltinApplyPatchToolType:
		operation, ok := arguments["operation"]
		if !ok || operation == nil {
			if _, hasType := arguments["type"]; hasType {
				operation = arguments
			}
		}
		if operation == nil {
			return nil, nil, false, domain.NewValidationError("tools", "apply_patch tool call operation is required")
		}
		payload["operation"] = operation
	default:
		return nil, nil, false, domain.NewValidationError("tools", "unsupported builtin tool type")
	}

	return payload, localBuiltinToolItemMeta(descriptor.CallType), true, nil
}

func localBuiltinToolArgumentsJSON(item domain.Item) (json.RawMessage, error) {
	payload := make(map[string]any)

	switch strings.TrimSpace(item.Type) {
	case localBuiltinShellCallType:
		action := item.Map()["action"]
		if action == nil {
			return nil, domain.ErrUnsupportedShape
		}
		payload["action"] = action
	case localBuiltinApplyPatchCallType:
		operation := item.Map()["operation"]
		if operation == nil {
			return nil, domain.ErrUnsupportedShape
		}
		payload["operation"] = operation
	default:
		return nil, domain.ErrUnsupportedShape
	}

	return json.Marshal(payload)
}

func stringifyLocalBuiltinToolOutput(item domain.Item) (string, error) {
	payload := make(map[string]any)

	switch strings.TrimSpace(item.Type) {
	case localBuiltinShellCallOutputType:
		for _, key := range []string{"max_output_length", "output"} {
			raw := bytes.TrimSpace(item.RawField(key))
			if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
				continue
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				continue
			}
			payload[key] = value
		}
	case localBuiltinApplyPatchCallOutputType:
		status := strings.TrimSpace(item.StringField("status"))
		if status != "" {
			payload["status"] = status
		}
		rawOutput := bytes.TrimSpace(item.RawField("output"))
		if len(rawOutput) > 0 && !bytes.Equal(rawOutput, []byte("null")) {
			var value any
			if err := json.Unmarshal(rawOutput, &value); err == nil {
				payload["output"] = value
			}
		}
	default:
		return "", domain.ErrUnsupportedShape
	}

	if len(payload) == 0 {
		return "", nil
	}
	compacted, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(compacted), nil
}
