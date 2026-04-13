package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

func (h *responseHandler) tryRunPreparedLocalConstrainedCustomToolResponse(ctx context.Context, input service.CreateResponseInput, prepared service.PreparedResponseContext, rawFields map[string]json.RawMessage, responseID string) (domain.Response, bool, error) {
	rawTools := decodeToolList(rawFields)
	effectiveTools := rawTools
	if shouldApplyCodexCompatibility(rawFields, rawTools, h.codexCompatibilityEnabled) {
		effectiveTools = augmentCodexToolDescriptions(rawTools)
	}

	_, plan, _, _, err := buildLocalToolLoopTransportPlan(rawFields, effectiveTools, h.forceCodexToolChoiceRequired)
	if err != nil {
		return domain.Response{}, false, err
	}

	descriptor, ok := selectNamedLocalConstrainedCustomTool(plan)
	if !ok {
		return domain.Response{}, false, nil
	}

	items, err := buildLocalConstrainedCustomToolRuntimeItems(prepared.ContextItems, len(prepared.NormalizedInput), descriptor)
	if err != nil {
		return domain.Response{}, true, err
	}
	options, err := buildLocalConstrainedCustomToolRuntimeOptions(buildGenerationOptions(rawFields), descriptor)
	if err != nil {
		return domain.Response{}, true, err
	}

	chatBody, err := buildLocalConstrainedCustomToolRuntimeChatCompletionBody(input.Model, items, options)
	if err != nil {
		return domain.Response{}, true, err
	}
	rawOutput, err := h.proxy.client.CreateChatCompletionText(ctx, chatBody)
	if err != nil {
		return domain.Response{}, true, err
	}

	toolInput, err := parseLocalConstrainedCustomToolRuntimeOutput(rawOutput, descriptor)
	if err != nil {
		return domain.Response{}, true, err
	}

	callItem, err := buildLocalConstrainedCustomToolCallItem(descriptor, toolInput)
	if err != nil {
		return domain.Response{}, true, err
	}

	response := domain.Response{
		ID:                 responseID,
		Object:             "response",
		Model:              input.Model,
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       domain.NewConversationReference(input.ConversationID),
		OutputText:         "",
		Output:             []domain.Item{callItem},
	}
	if err := enforceToolChoiceContract(response, plan.ToolChoiceContract); err != nil {
		return domain.Response{}, true, err
	}
	return annotateResponseCustomToolMetadata(response, plan), true, nil
}

func selectNamedLocalConstrainedCustomTool(plan customToolTransportPlan) (customToolDescriptor, bool) {
	if plan.ToolChoiceContract.Mode != toolChoiceContractRequiredNamedCustom {
		return customToolDescriptor{}, false
	}
	descriptor, ok := plan.Bridge.ByCanonicalIdentity(plan.ToolChoiceContract.Name, plan.ToolChoiceContract.Namespace)
	if !ok || descriptor.Constraint == nil {
		return customToolDescriptor{}, false
	}
	return descriptor, true
}

func buildLocalConstrainedCustomToolRuntimeItems(items []domain.Item, currentInputLen int, descriptor customToolDescriptor) ([]domain.Item, error) {
	label := descriptor.Name
	if descriptor.Namespace != "" {
		label = descriptor.Namespace + "." + descriptor.Name
	}
	prompt := strings.Join([]string{
		"You are the shim-local constrained custom tool generator.",
		"Generate raw input for the required custom tool `" + label + "`.",
		"Return JSON only with a single required string key named `input`.",
		"Do not emit assistant prose.",
		"Do not emit a tool wrapper.",
		"The `input` value must fully satisfy this " + descriptor.Constraint.Syntax + " constraint: " + descriptor.Constraint.Definition,
	}, " ")
	return insertLocalToolLoopInstructions(items, currentInputLen, prompt), nil
}

func buildLocalConstrainedCustomToolRuntimeOptions(options map[string]json.RawMessage, descriptor customToolDescriptor) (map[string]json.RawMessage, error) {
	cloned := cloneGenerationOptions(options)

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"input": map[string]any{
				"type":    "string",
				"pattern": descriptor.Constraint.Anchored,
			},
		},
		"required": []string{"input"},
	}

	responseFormat := map[string]any{
		"type":   "json_schema",
		"strict": true,
		"schema": schema,
	}

	responseFormatRaw, err := json.Marshal(responseFormat)
	if err != nil {
		return nil, err
	}
	schemaRaw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}

	// Keep the OpenAI-shaped response_format while also giving llama.cpp-compatible
	// backends a native json_schema hook when they support it.
	cloned["response_format"] = responseFormatRaw
	cloned["json_schema"] = schemaRaw
	return cloned, nil
}

func buildLocalConstrainedCustomToolRuntimeChatCompletionBody(model string, items []domain.Item, options map[string]json.RawMessage) ([]byte, error) {
	messages, err := buildChatCompletionMessagesFromItems(items)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"model":    model,
		"messages": messages,
	}
	for key, raw := range options {
		targetKey := key
		if key == "max_output_tokens" {
			targetKey = "max_tokens"
		}
		payload[targetKey] = json.RawMessage(raw)
	}
	return json.Marshal(payload)
}

func parseLocalConstrainedCustomToolRuntimeOutput(raw string, descriptor customToolDescriptor) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("shim-local constrained custom tool %s returned empty structured output", descriptor.Name)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", fmt.Errorf("shim-local constrained custom tool %s did not return valid JSON: %w", descriptor.Name, err)
	}

	rawInput, ok := payload["input"]
	if !ok {
		return "", fmt.Errorf("shim-local constrained custom tool %s did not return required input field", descriptor.Name)
	}

	var input string
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return "", fmt.Errorf("shim-local constrained custom tool %s input field must be a string", descriptor.Name)
	}
	if err := descriptor.Constraint.Validate(input); err != nil {
		return "", err
	}
	return input, nil
}

func buildLocalConstrainedCustomToolCallItem(descriptor customToolDescriptor, input string) (domain.Item, error) {
	itemID, err := domain.NewPrefixedID("item")
	if err != nil {
		return domain.Item{}, err
	}
	callID, err := domain.NewPrefixedID("call")
	if err != nil {
		return domain.Item{}, err
	}

	payload := map[string]any{
		"id":      itemID,
		"type":    "custom_tool_call",
		"call_id": callID,
		"name":    descriptor.Name,
		"input":   input,
		"status":  "completed",
	}
	if descriptor.Namespace != "" {
		payload["namespace"] = descriptor.Namespace
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}
