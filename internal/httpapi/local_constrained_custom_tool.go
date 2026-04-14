package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
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

	descriptor, ok := selectDirectLocalConstrainedCustomTool(plan, effectiveTools)
	if ok {
		response, err := h.runPreparedLocalConstrainedCustomToolResponse(ctx, input, prepared, rawFields, responseID, plan, descriptor, nil, "")
		if err != nil {
			if shouldFallbackLocalConstrainedRuntimeError(err) {
				return domain.Response{}, false, nil
			}
			return domain.Response{}, true, err
		}
		return response, true, nil
	}

	return h.tryRunPreparedPlannedLocalConstrainedCustomToolResponse(ctx, input, prepared, rawFields, responseID, plan, effectiveTools)
}

func (h *responseHandler) tryRecoverPreparedLocalConstrainedCustomToolResponse(ctx context.Context, input service.CreateResponseInput, prepared service.PreparedResponseContext, rawFields map[string]json.RawMessage, responseID string, plan customToolTransportPlan, validationErr *constrainedCustomToolValidationError) (domain.Response, bool, error) {
	if validationErr == nil || validationErr.Descriptor.Constraint == nil {
		return domain.Response{}, false, nil
	}

	response, err := h.runPreparedLocalConstrainedCustomToolResponse(
		ctx,
		input,
		prepared,
		rawFields,
		responseID,
		plan,
		validationErr.Descriptor,
		validationErr.PrefixItems,
		validationErr.CallID,
	)
	if err != nil {
		if shouldFallbackLocalConstrainedRuntimeError(err) {
			return domain.Response{}, false, nil
		}
		return domain.Response{}, true, err
	}
	return response, true, nil
}

func (h *responseHandler) runPreparedLocalConstrainedCustomToolResponse(ctx context.Context, input service.CreateResponseInput, prepared service.PreparedResponseContext, rawFields map[string]json.RawMessage, responseID string, plan customToolTransportPlan, descriptor customToolDescriptor, prefixItems []domain.Item, callID string) (domain.Response, error) {
	toolInput, err := h.generateLocalConstrainedCustomToolInput(ctx, input.Model, prepared.ContextItems, len(prepared.NormalizedInput), buildGenerationOptions(rawFields), descriptor)
	if err != nil {
		return domain.Response{}, err
	}

	callItem, err := buildLocalConstrainedCustomToolCallItem(descriptor, toolInput, callID)
	if err != nil {
		return domain.Response{}, err
	}

	output := append([]domain.Item(nil), prefixItems...)
	output = append(output, callItem)
	response := domain.Response{
		ID:                 responseID,
		Object:             "response",
		Model:              input.Model,
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       domain.NewConversationReference(input.ConversationID),
		OutputText:         "",
		Output:             output,
	}
	if err := enforceToolChoiceContract(response, plan.ToolChoiceContract); err != nil {
		return domain.Response{}, err
	}
	return annotateResponseCustomToolMetadata(response, plan), nil
}

func selectDirectLocalConstrainedCustomTool(plan customToolTransportPlan, tools []map[string]any) (customToolDescriptor, bool) {
	if descriptor, ok := selectNamedLocalConstrainedCustomTool(plan); ok {
		return descriptor, true
	}

	if plan.ToolChoiceContract.Mode != toolChoiceContractRequiredAny || len(tools) != 1 {
		return customToolDescriptor{}, false
	}
	name, namespace := customToolIdentity(tools[0])
	if name == "" {
		return customToolDescriptor{}, false
	}
	descriptor, ok := plan.Bridge.ByCanonicalIdentity(name, namespace)
	if !ok || descriptor.Constraint == nil {
		return customToolDescriptor{}, false
	}
	return descriptor, true
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

type localConstrainedToolCandidate struct {
	SelectionID string
	ToolType    string
	Tool        map[string]any
	Descriptor  customToolDescriptor
}

func (h *responseHandler) tryRunPreparedPlannedLocalConstrainedCustomToolResponse(ctx context.Context, input service.CreateResponseInput, prepared service.PreparedResponseContext, rawFields map[string]json.RawMessage, responseID string, plan customToolTransportPlan, tools []map[string]any) (domain.Response, bool, error) {
	if plan.ToolChoiceContract.Mode == toolChoiceContractRequiredNamedFunction || plan.ToolChoiceContract.Mode == toolChoiceContractRequiredNamedCustom {
		return domain.Response{}, false, nil
	}

	if subset, _, err := applyLocalAllowedToolsSubset(rawFields["tool_choice"], tools); err == nil && len(subset) > 0 {
		tools = subset
	} else if err != nil {
		return domain.Response{}, true, err
	}

	candidates := buildLocalConstrainedToolCandidates(plan, tools)
	if !shouldUseLocalConstrainedToolPlanner(candidates) {
		return domain.Response{}, false, nil
	}

	selection, err := h.selectLocalConstrainedToolCandidate(ctx, input.Model, prepared.ContextItems, len(prepared.NormalizedInput), buildGenerationOptions(rawFields), candidates, !plan.ToolChoiceContract.Active())
	if err != nil {
		if shouldFallbackLocalConstrainedRuntimeError(err) {
			return domain.Response{}, false, nil
		}
		return domain.Response{}, true, err
	}

	if selection.SelectionID == "assistant" {
		response, err := h.runPreparedLocalConstrainedAssistantFallback(ctx, input, rawFields)
		return response, true, err
	}

	for _, candidate := range candidates {
		if candidate.SelectionID != selection.SelectionID {
			continue
		}
		if candidate.Descriptor.Constraint != nil {
			response, err := h.runPreparedLocalConstrainedCustomToolResponse(ctx, input, prepared, rawFields, responseID, plan, candidate.Descriptor, nil, "")
			return response, true, err
		}
		narrowedFields, err := rewriteLocalToolLoopRawFieldsForSelectedTool(rawFields, candidate.Tool)
		if err != nil {
			return domain.Response{}, true, err
		}
		response, err := h.runPreparedLocalToolLoopResponse(ctx, input, prepared, narrowedFields)
		return response, true, err
	}

	return domain.Response{}, false, nil
}

func buildLocalConstrainedToolCandidates(plan customToolTransportPlan, tools []map[string]any) []localConstrainedToolCandidate {
	candidates := make([]localConstrainedToolCandidate, 0, len(tools))
	for _, tool := range tools {
		switch strings.ToLower(strings.TrimSpace(asString(tool["type"]))) {
		case "function":
			name := strings.TrimSpace(asString(tool["name"]))
			if name == "" {
				continue
			}
			candidates = append(candidates, localConstrainedToolCandidate{
				SelectionID: "function:" + name,
				ToolType:    "function",
				Tool:        cloneToolDefinition(tool),
			})
		case "custom", "custom_tool":
			name, namespace := customToolIdentity(tool)
			if name == "" {
				continue
			}
			descriptor, ok := plan.Bridge.ByCanonicalIdentity(name, namespace)
			if !ok {
				continue
			}
			candidates = append(candidates, localConstrainedToolCandidate{
				SelectionID: localConstrainedCustomSelectionID(namespace, name),
				ToolType:    "custom",
				Tool:        cloneToolDefinition(tool),
				Descriptor:  descriptor,
			})
		}
	}
	return candidates
}

func shouldUseLocalConstrainedToolPlanner(candidates []localConstrainedToolCandidate) bool {
	for _, candidate := range candidates {
		if candidate.Descriptor.Constraint != nil {
			return true
		}
	}
	return false
}

func (h *responseHandler) selectLocalConstrainedToolCandidate(ctx context.Context, model string, items []domain.Item, currentInputLen int, options map[string]json.RawMessage, candidates []localConstrainedToolCandidate, allowAssistant bool) (localConstrainedToolCandidate, error) {
	selectionItems, err := buildLocalConstrainedToolSelectionItems(items, currentInputLen, candidates, allowAssistant)
	if err != nil {
		return localConstrainedToolCandidate{}, err
	}
	selectionOptions, err := buildLocalConstrainedToolSelectionOptions(options, candidates, allowAssistant)
	if err != nil {
		return localConstrainedToolCandidate{}, err
	}
	chatBody, err := buildLocalConstrainedCustomToolRuntimeChatCompletionBody(model, selectionItems, selectionOptions)
	if err != nil {
		return localConstrainedToolCandidate{}, err
	}
	rawOutput, err := h.proxy.client.CreateChatCompletionText(ctx, chatBody)
	if err != nil {
		return localConstrainedToolCandidate{}, err
	}
	return parseLocalConstrainedToolSelectionOutput(rawOutput, candidates, allowAssistant)
}

func buildLocalConstrainedToolSelectionItems(items []domain.Item, currentInputLen int, candidates []localConstrainedToolCandidate, allowAssistant bool) ([]domain.Item, error) {
	lines := []string{
		"You are the shim-local constrained tool selector.",
		"Choose the single best next action for the current request.",
		"Return JSON only with a single required string key named `selection`.",
	}
	if allowAssistant {
		lines = append(lines, "Choose `assistant` only if the request should be answered directly without calling any tool.")
	} else {
		lines = append(lines, "A tool call is required for this request.")
	}
	lines = append(lines, "Available selections:")
	for _, candidate := range candidates {
		line := "- " + candidate.SelectionID + " => " + candidate.ToolType + " " + localConstrainedToolCandidateName(candidate)
		if candidate.Descriptor.Constraint != nil {
			line += " [constrained " + candidate.Descriptor.Constraint.Syntax + "]"
		}
		description := localConstrainedToolCandidateDescription(candidate)
		if description != "" {
			line += ": " + description
		}
		lines = append(lines, line)
	}
	return insertLocalToolLoopInstructions(items, currentInputLen, strings.Join(lines, "\n")), nil
}

func buildLocalConstrainedToolSelectionOptions(options map[string]json.RawMessage, candidates []localConstrainedToolCandidate, allowAssistant bool) (map[string]json.RawMessage, error) {
	cloned := cloneGenerationOptions(options)
	selectionEnum := make([]string, 0, len(candidates)+1)
	if allowAssistant {
		selectionEnum = append(selectionEnum, "assistant")
	}
	for _, candidate := range candidates {
		selectionEnum = append(selectionEnum, candidate.SelectionID)
	}
	slices.Sort(selectionEnum)

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"selection": map[string]any{
				"type": "string",
				"enum": selectionEnum,
			},
		},
		"required": []string{"selection"},
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
	cloned["response_format"] = responseFormatRaw
	cloned["json_schema"] = schemaRaw
	return cloned, nil
}

func parseLocalConstrainedToolSelectionOutput(raw string, candidates []localConstrainedToolCandidate, allowAssistant bool) (localConstrainedToolCandidate, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return localConstrainedToolCandidate{}, &llama.InvalidResponseError{Message: "shim-local constrained tool selector returned empty structured output"}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return localConstrainedToolCandidate{}, &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained tool selector did not return valid JSON: %v", err)}
	}
	rawSelection, ok := payload["selection"]
	if !ok {
		return localConstrainedToolCandidate{}, &llama.InvalidResponseError{Message: "shim-local constrained tool selector did not return required selection field"}
	}
	var selection string
	if err := json.Unmarshal(rawSelection, &selection); err != nil {
		return localConstrainedToolCandidate{}, &llama.InvalidResponseError{Message: "shim-local constrained tool selector selection field must be a string"}
	}
	selection = strings.TrimSpace(selection)
	if selection == "assistant" && allowAssistant {
		return localConstrainedToolCandidate{SelectionID: "assistant"}, nil
	}
	for _, candidate := range candidates {
		if candidate.SelectionID == selection {
			return candidate, nil
		}
	}
	return localConstrainedToolCandidate{}, &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained tool selector returned unsupported selection %q", selection)}
}

func rewriteLocalToolLoopRawFieldsForSelectedTool(rawFields map[string]json.RawMessage, selectedTool map[string]any) (map[string]json.RawMessage, error) {
	cloned := cloneRawFields(rawFields)
	toolsRaw, err := json.Marshal([]map[string]any{cloneToolDefinition(selectedTool)})
	if err != nil {
		return nil, err
	}
	cloned["tools"] = toolsRaw

	switch strings.ToLower(strings.TrimSpace(asString(selectedTool["type"]))) {
	case "function":
		choiceRaw, err := json.Marshal(map[string]any{
			"type": "function",
			"name": strings.TrimSpace(asString(selectedTool["name"])),
		})
		if err != nil {
			return nil, err
		}
		cloned["tool_choice"] = choiceRaw
	case "custom", "custom_tool":
		name, namespace := customToolIdentity(selectedTool)
		payload := map[string]any{"type": "custom", "name": name}
		if namespace != "" {
			payload["namespace"] = namespace
		}
		choiceRaw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		cloned["tool_choice"] = choiceRaw
	default:
		cloned["tool_choice"] = json.RawMessage(`"required"`)
	}
	return cloned, nil
}

func (h *responseHandler) runPreparedLocalConstrainedAssistantFallback(ctx context.Context, input service.CreateResponseInput, rawFields map[string]json.RawMessage) (domain.Response, error) {
	strippedFields := cloneRawFields(rawFields)
	delete(strippedFields, "tools")
	delete(strippedFields, "tool_choice")
	delete(strippedFields, "parallel_tool_calls")
	requestJSON, err := json.Marshal(strippedFields)
	if err != nil {
		return domain.Response{}, err
	}

	assistantInput := input
	assistantInput.RequestJSON = string(requestJSON)
	assistantInput.GenerationOptions = buildGenerationOptions(strippedFields)
	return h.service.Create(ctx, assistantInput)
}

func cloneRawFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	if len(fields) == 0 {
		return map[string]json.RawMessage{}
	}
	cloned := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}

func cloneToolDefinition(tool map[string]any) map[string]any {
	cloned := make(map[string]any, len(tool))
	for key, value := range tool {
		switch typed := value.(type) {
		case map[string]any:
			cloned[key] = mapsClone(typed)
		case []any:
			copied := make([]any, len(typed))
			copy(copied, typed)
			cloned[key] = copied
		default:
			cloned[key] = value
		}
	}
	return cloned
}

func localConstrainedToolCandidateName(candidate localConstrainedToolCandidate) string {
	if candidate.Descriptor.Name != "" {
		if candidate.Descriptor.Namespace != "" {
			return candidate.Descriptor.Namespace + "." + candidate.Descriptor.Name
		}
		return candidate.Descriptor.Name
	}
	if candidate.ToolType == "function" {
		return strings.TrimSpace(asString(candidate.Tool["name"]))
	}
	name, namespace := customToolIdentity(candidate.Tool)
	if namespace != "" {
		return namespace + "." + name
	}
	return name
}

func localConstrainedToolCandidateDescription(candidate localConstrainedToolCandidate) string {
	description := strings.TrimSpace(asString(candidate.Tool["description"]))
	if description != "" {
		return description
	}
	function, ok := candidate.Tool["function"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(asString(function["description"]))
}

func localConstrainedCustomSelectionID(namespace, name string) string {
	if namespace == "" {
		return "custom:" + strings.TrimSpace(name)
	}
	return "custom:" + strings.TrimSpace(namespace) + "." + strings.TrimSpace(name)
}

func (h *responseHandler) generateLocalConstrainedCustomToolInput(ctx context.Context, model string, items []domain.Item, currentInputLen int, options map[string]json.RawMessage, descriptor customToolDescriptor) (string, error) {
	runtimeItems, err := buildLocalConstrainedCustomToolRuntimeItems(items, currentInputLen, descriptor)
	if err != nil {
		return "", err
	}
	runtimeOptions, err := buildLocalConstrainedCustomToolRuntimeOptions(options, descriptor)
	if err != nil {
		return "", err
	}
	chatBody, err := buildLocalConstrainedCustomToolRuntimeChatCompletionBody(model, runtimeItems, runtimeOptions)
	if err != nil {
		return "", err
	}
	rawOutput, err := h.proxy.client.CreateChatCompletionText(ctx, chatBody)
	if err != nil {
		return "", err
	}
	return parseLocalConstrainedCustomToolRuntimeOutput(rawOutput, descriptor)
}

func shouldFallbackLocalConstrainedRuntimeError(err error) bool {
	var upstreamErr *llama.UpstreamError
	var timeoutErr *llama.TimeoutError
	var invalidErr *llama.InvalidResponseError
	return errors.As(err, &upstreamErr) || errors.As(err, &timeoutErr) || errors.As(err, &invalidErr)
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
		return "", &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained custom tool %s returned empty structured output", descriptor.Name)}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained custom tool %s did not return valid JSON: %v", descriptor.Name, err)}
	}

	rawInput, ok := payload["input"]
	if !ok {
		return "", &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained custom tool %s did not return required input field", descriptor.Name)}
	}

	var input string
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return "", &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained custom tool %s input field must be a string", descriptor.Name)}
	}
	if err := descriptor.Constraint.Validate(input); err != nil {
		return "", &llama.InvalidResponseError{Message: fmt.Sprintf("shim-local constrained custom tool %s returned invalid constrained input: %v", descriptor.Name, err)}
	}
	return input, nil
}

func buildLocalConstrainedCustomToolCallItem(descriptor customToolDescriptor, input string, callID string) (domain.Item, error) {
	itemID, err := domain.NewPrefixedID("item")
	if err != nil {
		return domain.Item{}, err
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		callID, err = domain.NewPrefixedID("call")
		if err != nil {
			return domain.Item{}, err
		}
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
