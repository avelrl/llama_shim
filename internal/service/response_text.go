package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

func (s *ResponseService) FinalizeLocalResponse(input CreateResponseInput, contextItems []domain.Item, response domain.Response) (domain.Response, error) {
	config, err := domain.ParseResponseTextConfig(input.TextConfig)
	if err != nil {
		return domain.Response{}, err
	}
	if err := validateLocalResponseTextRequest(config, contextItems); err != nil {
		return domain.Response{}, err
	}
	response.Text = domain.MarshalResponseTextConfig(config)

	if !responseHasAssistantText(response) {
		return response, nil
	}

	switch config.Format.Type {
	case "text":
		return response, nil
	case "json_object":
		if err := validateJSONModeContext(contextItems); err != nil {
			return domain.Response{}, err
		}
		response = normalizeLocalStructuredOutput(response)
		if err := validateJSONObjectOutput(response.OutputText); err != nil {
			return domain.Response{}, err
		}
	case "json_schema":
		response = normalizeLocalStructuredOutput(response)
		if err := validateJSONSchemaOutput(config.Format.Schema, response.OutputText); err != nil {
			return domain.Response{}, err
		}
	}

	return response, nil
}

func (s *ResponseService) PrepareLocalResponseText(input CreateResponseInput, contextItems []domain.Item) (domain.ResponseTextConfig, error) {
	config, err := domain.ParseResponseTextConfig(input.TextConfig)
	if err != nil {
		return domain.ResponseTextConfig{}, err
	}
	if err := validateLocalResponseTextRequest(config, contextItems); err != nil {
		return domain.ResponseTextConfig{}, err
	}
	return config, nil
}

func responseHasAssistantText(response domain.Response) bool {
	if strings.TrimSpace(response.OutputText) != "" {
		return true
	}
	for _, item := range response.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, part := range item.Content {
			if strings.TrimSpace(part.Text) != "" {
				return true
			}
		}
	}
	return false
}

func validateJSONModeContext(items []domain.Item) error {
	var builder strings.Builder
	for _, item := range items {
		if item.Type != "message" {
			continue
		}
		builder.WriteString(domain.MessageText(item))
		builder.WriteString("\n")
	}
	if strings.Contains(builder.String(), "JSON") {
		return nil
	}
	return domain.NewValidationError("text.format", `text.format.type=json_object requires "JSON" to appear somewhere in the conversation context`)
}

func validateLocalResponseTextRequest(config domain.ResponseTextConfig, contextItems []domain.Item) error {
	if config.Format.Type != "json_object" {
		return nil
	}
	return validateJSONModeContext(contextItems)
}

func validateJSONObjectOutput(outputText string) error {
	value, err := decodeStructuredOutput(outputText)
	if err != nil {
		return err
	}
	if _, ok := value.(map[string]any); ok {
		return nil
	}
	return invalidStructuredOutputError("json_object responses must decode to a JSON object")
}

func validateJSONSchemaOutput(schemaRaw json.RawMessage, outputText string) error {
	value, err := decodeStructuredOutput(outputText)
	if err != nil {
		return err
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return domain.NewValidationError("text.format.schema", "text.format.schema must be valid JSON")
	}
	return validateJSONValueAgainstSchema(value, schema, "$")
}

func decodeStructuredOutput(outputText string) (any, error) {
	trimmed := domain.NormalizeStructuredOutputJSONText(outputText)
	if trimmed == "" {
		return nil, invalidStructuredOutputError("structured output response was empty")
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, invalidStructuredOutputError("structured output response was not valid JSON")
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, invalidStructuredOutputError("structured output response was not valid JSON")
	}
	return value, nil
}

func normalizeLocalStructuredOutput(response domain.Response) domain.Response {
	normalized := domain.NormalizeStructuredOutputJSONText(response.OutputText)
	if normalized == "" || normalized == response.OutputText {
		return response
	}
	response.OutputText = normalized
	response.Output = []domain.Item{domain.NewOutputTextMessage(normalized)}
	return response
}

func validateJSONValueAgainstSchema(value any, schema map[string]any, path string) error {
	typeName := strings.TrimSpace(schemaString(schema["type"]))
	if typeName == "" {
		return invalidStructuredOutputError(fmt.Sprintf("%s.type is required", path))
	}

	if enumValue, ok := schema["enum"]; ok {
		enumItems, ok := enumValue.([]any)
		if !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s.enum must be an array", path))
		}
		matched := false
		for _, candidate := range enumItems {
			if valuesEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return invalidStructuredOutputError(fmt.Sprintf("%s must match one of the configured enum values", path))
		}
	}

	switch typeName {
	case "object":
		objectValue, ok := value.(map[string]any)
		if !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be an object", path))
		}

		properties := map[string]any{}
		if rawProperties, ok := schema["properties"]; ok {
			props, ok := rawProperties.(map[string]any)
			if !ok {
				return invalidStructuredOutputError(fmt.Sprintf("%s.properties must be an object", path))
			}
			properties = props
		}
		if rawRequired, ok := schema["required"]; ok {
			required, ok := rawRequired.([]any)
			if !ok {
				return invalidStructuredOutputError(fmt.Sprintf("%s.required must be an array", path))
			}
			for _, entry := range required {
				name := strings.TrimSpace(schemaString(entry))
				if name == "" {
					return invalidStructuredOutputError(fmt.Sprintf("%s.required must contain strings", path))
				}
				if _, ok := objectValue[name]; !ok {
					return invalidStructuredOutputError(fmt.Sprintf("%s.%s is required", path, name))
				}
			}
		}
		if rawAdditional, ok := schema["additionalProperties"]; ok {
			additional, ok := rawAdditional.(bool)
			if !ok {
				return invalidStructuredOutputError(fmt.Sprintf("%s.additionalProperties must be a boolean", path))
			}
			if !additional {
				for key := range objectValue {
					if _, ok := properties[key]; ok {
						continue
					}
					return invalidStructuredOutputError(fmt.Sprintf("%s.%s is not allowed", path, key))
				}
			}
		}
		for name, rawChild := range properties {
			childValue, ok := objectValue[name]
			if !ok {
				continue
			}
			childSchema, ok := rawChild.(map[string]any)
			if !ok {
				return invalidStructuredOutputError(fmt.Sprintf("%s.properties.%s must be an object", path, name))
			}
			if err := validateJSONValueAgainstSchema(childValue, childSchema, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be an array", path))
		}
		rawItemSchema, ok := schema["items"]
		if !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s.items is required", path))
		}
		itemSchema, ok := rawItemSchema.(map[string]any)
		if !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s.items must be an object", path))
		}
		for idx, item := range items {
			if err := validateJSONValueAgainstSchema(item, itemSchema, fmt.Sprintf("%s[%d]", path, idx)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be a string", path))
		}
	case "number":
		if !isJSONNumber(value) {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be a number", path))
		}
	case "integer":
		if !isJSONInteger(value) {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be an integer", path))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be a boolean", path))
		}
	case "null":
		if value != nil {
			return invalidStructuredOutputError(fmt.Sprintf("%s must be null", path))
		}
	default:
		return invalidStructuredOutputError(fmt.Sprintf("unsupported schema type %q", typeName))
	}

	return nil
}

func valuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func isJSONNumber(value any) bool {
	switch value.(type) {
	case json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func isJSONInteger(value any) bool {
	switch number := value.(type) {
	case json.Number:
		floatValue, err := number.Float64()
		return err == nil && math.Trunc(floatValue) == floatValue
	case float64:
		return math.Trunc(number) == number
	case float32:
		return math.Trunc(float64(number)) == float64(number)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func schemaString(value any) string {
	text, _ := value.(string)
	return text
}

func invalidStructuredOutputError(message string) error {
	return &llama.InvalidResponseError{Message: message}
}
