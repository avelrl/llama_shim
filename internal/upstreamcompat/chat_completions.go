package upstreamcompat

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
)

type ChatCompletionCompatibility struct {
	DeveloperRolesRemapped            int
	DefaultThinkingDisabled           bool
	DefaultMaxTokensApplied           bool
	JSONSchemaDowngraded              bool
	ToolParameterPropertyTypesEnsured bool
	EmptyAssistantToolContentOmitted  int
}

type ChatCompletionOptions struct {
	Rules []ChatCompletionRule
}

type ChatCompletionRule struct {
	Model                            string
	RemapDeveloperRole               bool
	DefaultThinking                  string
	DefaultMaxTokens                 int
	JSONSchemaMode                   string
	EnsureToolParameterPropertyTypes bool
	OmitEmptyAssistantToolContent    bool
}

const (
	DefaultThinkingPassthrough      = "passthrough"
	DefaultThinkingDisabled         = "disabled"
	JSONSchemaModePassthrough       = "passthrough"
	JSONSchemaModeObjectInstruction = "json_object_instruction"
)

func (c ChatCompletionCompatibility) Applied() bool {
	return c.DeveloperRolesRemapped > 0 ||
		c.DefaultThinkingDisabled ||
		c.DefaultMaxTokensApplied ||
		c.JSONSchemaDowngraded ||
		c.ToolParameterPropertyTypesEnsured ||
		c.EmptyAssistantToolContentOmitted > 0
}

func NormalizeChatCompletionRequest(rawBody []byte, options ChatCompletionOptions) ([]byte, ChatCompletionCompatibility, error) {
	rawFields, err := decodeRawFields(rawBody)
	if err != nil {
		return nil, ChatCompletionCompatibility{}, err
	}

	var compatibility ChatCompletionCompatibility
	model := rawStringField(rawFields, "model")
	rule, ok := chatCompletionRuleForModel(model, options.Rules)
	if ok {
		if rule.RemapDeveloperRole {
			if remapped, changed, err := remapDeveloperMessages(rawFields["messages"]); err != nil {
				return nil, ChatCompletionCompatibility{}, err
			} else if changed {
				rawFields["messages"] = remapped
				compatibility.DeveloperRolesRemapped = countDeveloperMessages(rawBody)
			}
		}

		if rule.DefaultThinking == DefaultThinkingDisabled {
			if _, exists := rawFields["thinking"]; !exists {
				rawFields["thinking"] = json.RawMessage(`{"type":"disabled"}`)
				compatibility.DefaultThinkingDisabled = true
			}
		}

		if rule.DefaultMaxTokens > 0 {
			if _, hasMaxTokens := rawFields["max_tokens"]; !hasMaxTokens {
				if _, hasMaxCompletionTokens := rawFields["max_completion_tokens"]; !hasMaxCompletionTokens {
					rawFields["max_tokens"] = json.RawMessage(strconv.Itoa(rule.DefaultMaxTokens))
					compatibility.DefaultMaxTokensApplied = true
				}
			}
		}

		if rule.JSONSchemaMode == JSONSchemaModeObjectInstruction {
			if downgraded, schemaInstruction, changed, err := downgradeJSONSchemaToJSONObjectInstruction(rawFields["response_format"]); err != nil {
				return nil, ChatCompletionCompatibility{}, err
			} else if changed {
				rawFields["response_format"] = downgraded
				delete(rawFields, "json_schema")
				delete(rawFields, "structured_outputs")
				rawFields["messages"], err = prependSystemInstruction(rawFields["messages"], schemaInstruction)
				if err != nil {
					return nil, ChatCompletionCompatibility{}, err
				}
				compatibility.JSONSchemaDowngraded = true
			}
		}

		if rule.EnsureToolParameterPropertyTypes {
			if normalized, changed, err := ensureToolParameterPropertyTypes(rawFields["tools"]); err != nil {
				return nil, ChatCompletionCompatibility{}, err
			} else if changed {
				rawFields["tools"] = normalized
				compatibility.ToolParameterPropertyTypesEnsured = true
			}
		}

		if rule.OmitEmptyAssistantToolContent {
			if normalized, omitted, err := omitEmptyAssistantToolCallContent(rawFields["messages"]); err != nil {
				return nil, ChatCompletionCompatibility{}, err
			} else if omitted > 0 {
				rawFields["messages"] = normalized
				compatibility.EmptyAssistantToolContentOmitted = omitted
			}
		}
	}

	if !compatibility.Applied() {
		return rawBody, compatibility, nil
	}
	out, err := json.Marshal(rawFields)
	if err != nil {
		return nil, ChatCompletionCompatibility{}, err
	}
	return out, compatibility, nil
}

func decodeRawFields(rawBody []byte) (map[string]json.RawMessage, error) {
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &rawFields); err != nil {
		return nil, err
	}
	if rawFields == nil {
		return nil, domain.NewValidationError("", "request body must be a JSON object")
	}
	return rawFields, nil
}

func rawStringField(fields map[string]json.RawMessage, key string) string {
	var value string
	if raw, ok := fields[key]; ok {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func remapDeveloperMessages(rawMessages json.RawMessage) (json.RawMessage, bool, error) {
	if len(bytes.TrimSpace(rawMessages)) == 0 {
		return rawMessages, false, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, false, domain.NewValidationError("messages", "messages must be an array")
	}
	changed := false
	for _, message := range messages {
		var role string
		if rawRole, ok := message["role"]; ok && json.Unmarshal(rawRole, &role) == nil && role == "developer" {
			message["role"] = json.RawMessage(`"system"`)
			changed = true
		}
	}
	if !changed {
		return rawMessages, false, nil
	}
	out, err := json.Marshal(messages)
	return out, true, err
}

func countDeveloperMessages(rawBody []byte) int {
	var request struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rawBody, &request); err != nil {
		return 0
	}
	count := 0
	for _, message := range request.Messages {
		if message.Role == "developer" {
			count++
		}
	}
	return count
}

func downgradeJSONSchemaToJSONObjectInstruction(rawResponseFormat json.RawMessage) (json.RawMessage, string, bool, error) {
	trimmed := bytes.TrimSpace(rawResponseFormat)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return rawResponseFormat, "", false, nil
	}
	var responseFormat map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &responseFormat); err != nil {
		return nil, "", false, domain.NewValidationError("response_format", "response_format must be an object")
	}
	var responseFormatType string
	if err := json.Unmarshal(responseFormat["type"], &responseFormatType); err != nil || responseFormatType != "json_schema" {
		return rawResponseFormat, "", false, nil
	}

	schemaRaw := responseFormat["json_schema"]
	var schemaEnvelope map[string]json.RawMessage
	if len(bytes.TrimSpace(schemaRaw)) > 0 && json.Unmarshal(schemaRaw, &schemaEnvelope) == nil {
		if nestedSchema := bytes.TrimSpace(schemaEnvelope["schema"]); len(nestedSchema) > 0 && !bytes.Equal(nestedSchema, []byte("null")) {
			schemaRaw = nestedSchema
		}
	}
	schemaInstruction := "Return JSON only. The JSON object must match this JSON Schema: " + string(bytes.TrimSpace(schemaRaw)) + ". Do not include markdown or prose."
	return json.RawMessage(`{"type":"json_object"}`), schemaInstruction, true, nil
}

func chatCompletionRuleForModel(model string, rules []ChatCompletionRule) (ChatCompletionRule, bool) {
	model = strings.TrimSpace(model)
	for _, rule := range rules {
		if modelPatternMatches(rule.Model, model) {
			return rule, true
		}
	}
	return ChatCompletionRule{}, false
}

func modelPatternMatches(pattern string, model string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	model = strings.ToLower(strings.TrimSpace(model))
	if pattern == "" || model == "" {
		return false
	}
	if pattern == "*" || pattern == model {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	if parts[0] != "" {
		if !strings.HasPrefix(model, parts[0]) {
			return false
		}
		pos = len(parts[0])
	}
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		idx := strings.Index(model[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(model, last)
}

func prependSystemInstruction(rawMessages json.RawMessage, instruction string) (json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return nil, domain.NewValidationError("messages", "messages must be an array")
	}
	rawInstruction, err := json.Marshal(instruction)
	if err != nil {
		return nil, err
	}
	instructionMessage := map[string]json.RawMessage{
		"role":    json.RawMessage(`"system"`),
		"content": rawInstruction,
	}
	messages = append([]map[string]json.RawMessage{instructionMessage}, messages...)
	return json.Marshal(messages)
}

func ensureToolParameterPropertyTypes(rawTools json.RawMessage) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(rawTools)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return rawTools, false, nil
	}
	var tools []map[string]any
	if err := json.Unmarshal(trimmed, &tools); err != nil {
		return nil, false, domain.NewValidationError("tools", "tools must be an array")
	}
	changed := false
	for _, tool := range tools {
		if stringValue(tool["type"]) != "function" {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		parameters, ok := function["parameters"].(map[string]any)
		if !ok {
			continue
		}
		normalizeSchemaContainer(parameters, &changed)
	}
	if !changed {
		return rawTools, false, nil
	}
	out, err := json.Marshal(tools)
	return out, true, err
}

func normalizeSchemaContainer(node any, changed *bool) {
	object, ok := node.(map[string]any)
	if !ok {
		return
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		for _, property := range properties {
			normalizeSchemaProperty(property, changed)
		}
	}
	switch items := object["items"].(type) {
	case map[string]any:
		normalizeSchemaProperty(items, changed)
	case []any:
		for _, item := range items {
			normalizeSchemaProperty(item, changed)
		}
	}
	if additional, ok := object["additionalProperties"].(map[string]any); ok {
		normalizeSchemaProperty(additional, changed)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		branches, ok := object[key].([]any)
		if !ok {
			continue
		}
		for _, branch := range branches {
			normalizeSchemaProperty(branch, changed)
		}
	}
}

func normalizeSchemaProperty(node any, changed *bool) {
	object, ok := node.(map[string]any)
	if !ok {
		return
	}
	if _, hasType := object["type"]; !hasType && !hasSchemaCombinator(object) {
		object["type"] = inferSchemaType(object)
		*changed = true
	}
	normalizeSchemaContainer(object, changed)
}

func hasSchemaCombinator(object map[string]any) bool {
	for _, key := range []string{"anyOf", "oneOf", "allOf", "not", "if", "then", "else", "$ref"} {
		if _, ok := object[key]; ok {
			return true
		}
	}
	return false
}

func inferSchemaType(object map[string]any) string {
	if values, ok := object["enum"].([]any); ok && len(values) > 0 {
		return inferSchemaTypeFromValues(values)
	}
	if value, ok := object["const"]; ok {
		return inferSchemaTypeFromValues([]any{value})
	}
	if hasAnyKey(object, "properties", "additionalProperties", "patternProperties", "propertyNames", "required", "minProperties", "maxProperties") {
		return "object"
	}
	if hasAnyKey(object, "items", "prefixItems", "minItems", "maxItems", "uniqueItems", "contains") {
		return "array"
	}
	if hasAnyKey(object, "minLength", "maxLength", "pattern", "format") {
		return "string"
	}
	if hasAnyKey(object, "minimum", "maximum", "multipleOf", "exclusiveMinimum", "exclusiveMaximum") {
		return "number"
	}
	return "string"
}

func hasAnyKey(object map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := object[key]; ok {
			return true
		}
	}
	return false
}

func inferSchemaTypeFromValues(values []any) string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		switch value.(type) {
		case bool:
			seen["boolean"] = struct{}{}
		case string:
			seen["string"] = struct{}{}
		case nil:
			seen["null"] = struct{}{}
		case map[string]any:
			seen["object"] = struct{}{}
		case []any:
			seen["array"] = struct{}{}
		case float64:
			if isJSONInteger(value.(float64)) {
				seen["integer"] = struct{}{}
			} else {
				seen["number"] = struct{}{}
			}
		default:
			return "string"
		}
	}
	if len(seen) == 1 {
		for typ := range seen {
			return typ
		}
	}
	if len(seen) == 2 {
		if _, hasInteger := seen["integer"]; hasInteger {
			if _, hasNumber := seen["number"]; hasNumber {
				return "number"
			}
		}
	}
	return "string"
}

func isJSONInteger(value float64) bool {
	return value == float64(int64(value))
}

func omitEmptyAssistantToolCallContent(rawMessages json.RawMessage) (json.RawMessage, int, error) {
	trimmed := bytes.TrimSpace(rawMessages)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return rawMessages, 0, nil
	}
	var messages []map[string]any
	if err := json.Unmarshal(trimmed, &messages); err != nil {
		return nil, 0, domain.NewValidationError("messages", "messages must be an array")
	}
	omitted := 0
	for _, message := range messages {
		if stringValue(message["role"]) != "assistant" || !hasToolCalls(message["tool_calls"]) {
			continue
		}
		content, exists := message["content"]
		if !exists {
			continue
		}
		if isEmptyAssistantToolCallContent(content) {
			delete(message, "content")
			omitted++
		}
	}
	if omitted == 0 {
		return rawMessages, 0, nil
	}
	out, err := json.Marshal(messages)
	return out, omitted, err
}

func hasToolCalls(value any) bool {
	calls, ok := value.([]any)
	return ok && len(calls) > 0
}

func isEmptyAssistantToolCallContent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		for _, part := range typed {
			if !isEmptyTextContentPart(part) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isEmptyTextContentPart(value any) bool {
	part, ok := value.(map[string]any)
	if !ok {
		return false
	}
	partType := stringValue(part["type"])
	if partType != "text" && partType != "input_text" && partType != "output_text" {
		return false
	}
	return strings.TrimSpace(stringValue(part["text"])) == ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
