package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

const maxLocalConstrainedCustomToolRepairAttempts = 3

type constrainedCustomToolValidationError struct {
	Descriptor  customToolDescriptor
	Input       string
	Cause       error
	CallID      string
	PrefixItems []domain.Item
}

func (e *constrainedCustomToolValidationError) Error() string {
	name := e.Descriptor.Name
	if e.Descriptor.Namespace != "" {
		name = e.Descriptor.Namespace + "." + e.Descriptor.Name
	}
	return fmt.Sprintf("shim-local constrained custom tool %s produced invalid input %q: %v", name, e.Input, e.Cause)
}

func (e *constrainedCustomToolValidationError) Unwrap() error {
	return e.Cause
}

func buildLocalChatCompletionRequest(rawFields map[string]json.RawMessage, contextItems []domain.Item, currentInput []domain.Item, _ map[string]domain.ToolCallReference, codexCompatibilityEnabled bool, forceCodexToolChoiceRequired bool, repairPrompt string) ([]byte, customToolTransportPlan, error) {
	model := strings.TrimSpace(rawStringField(rawFields, "model"))
	if model == "" {
		return nil, customToolTransportPlan{}, domain.NewValidationError("model", "model is required")
	}

	rawTools := decodeToolList(rawFields)
	effectiveTools := rawTools
	if shouldApplyCodexCompatibility(rawFields, rawTools, codexCompatibilityEnabled) {
		contextItems = injectCodexCompatibilityContext(contextItems, len(currentInput))
		effectiveTools = augmentCodexToolDescriptions(rawTools)
	}

	chatTools, plan, toolChoice, extraInstructions, err := buildLocalToolLoopTransportPlan(rawFields, effectiveTools, forceCodexToolChoiceRequired)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	if extraInstructions != "" {
		contextItems = insertLocalToolLoopInstructions(contextItems, len(currentInput), extraInstructions)
	}
	if strings.TrimSpace(repairPrompt) != "" {
		contextItems = insertLocalToolLoopInstructions(contextItems, len(currentInput), repairPrompt)
	}

	messages, err := buildChatCompletionMessagesFromItems(contextItems)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	out := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if len(chatTools) > 0 {
		out["tools"] = chatTools
	}
	if toolChoice != nil {
		out["tool_choice"] = toolChoice
	}
	if rawParallel, ok := rawFields["parallel_tool_calls"]; ok && len(chatTools) > 0 {
		out["parallel_tool_calls"] = json.RawMessage(rawParallel)
	}
	for key, raw := range rawFields {
		if _, ok := shimLocalGenerationFields[key]; !ok {
			continue
		}
		targetKey := key
		if key == "max_output_tokens" {
			targetKey = "max_tokens"
		}
		out[targetKey] = json.RawMessage(raw)
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	return body, plan, nil
}

func buildLocalToolLoopTransportPlan(rawFields map[string]json.RawMessage, tools []map[string]any, forceCodexToolChoiceRequired bool) ([]map[string]any, customToolTransportPlan, any, string, error) {
	plan := customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			ByModelName: make(map[string]customToolDescriptor),
			BySynthetic: make(map[string]customToolDescriptor),
			ByCanonical: make(map[string]customToolDescriptor),
		},
	}
	if len(tools) == 0 {
		var toolChoice any
		if rawChoice, ok := rawFields["tool_choice"]; ok {
			rewrittenChoice, err := remapToolChoice(rawChoice, rawFields, nil, plan, forceCodexToolChoiceRequired)
			if err != nil {
				return nil, customToolTransportPlan{}, nil, "", err
			}
			toolChoice = rewrittenChoice
			plan.ToolChoiceContract = deriveToolChoiceContract(rawChoice, rewrittenChoice)
		}
		return nil, plan, toolChoice, "", nil
	}

	rewrittenToolChoice := rawFields["tool_choice"]
	allowedSubset, rewrittenAllowedChoice, err := applyLocalAllowedToolsSubset(rawFields["tool_choice"], tools)
	if err != nil {
		return nil, customToolTransportPlan{}, nil, "", err
	}
	if allowedSubset != nil {
		tools = allowedSubset
		rewrittenToolChoice = rewrittenAllowedChoice
	}

	localTools := make([]map[string]any, 0, len(tools))
	customDescriptors := make([]customToolDescriptor, 0, len(tools))
	usedNames := make(map[string]struct{})
	droppedBuiltinTools := make([]string, 0, 1)

	for _, tool := range tools {
		if tool == nil {
			continue
		}

		toolType := strings.ToLower(strings.TrimSpace(asString(tool["type"])))
		if isDisabledWebSearchTool(tool) {
			droppedBuiltinTools = append(droppedBuiltinTools, toolType)
			continue
		}
		switch {
		case toolType == "function":
			definition, name, err := buildLocalFunctionToolDefinition(tool)
			if err != nil {
				return nil, customToolTransportPlan{}, nil, "", err
			}
			if _, exists := usedNames[name]; exists {
				return nil, customToolTransportPlan{}, nil, "", domain.NewValidationError("tools", "tool names must be unique in shim-local tool loop")
			}
			usedNames[name] = struct{}{}
			localTools = append(localTools, definition)
		case isCustomToolType(toolType):
			descriptor, definition, err := buildLocalCustomToolDefinition(tool)
			if err != nil {
				return nil, customToolTransportPlan{}, nil, "", err
			}
			if _, exists := usedNames[descriptor.Name]; exists {
				return nil, customToolTransportPlan{}, nil, "", domain.NewValidationError("tools", "custom tool name conflicts with an existing function tool name")
			}
			if _, exists := plan.Bridge.ByModelName[descriptor.Name]; exists {
				return nil, customToolTransportPlan{}, nil, "", domain.NewValidationError("tools", "custom tools must not repeat the same name in shim-local tool loop")
			}
			usedNames[descriptor.Name] = struct{}{}
			key := canonicalCustomToolKey(descriptor.Namespace, descriptor.Name)
			plan.Bridge.ByCanonical[key] = descriptor
			plan.Bridge.ByModelName[descriptor.Name] = descriptor
			plan.Bridge.BySynthetic[descriptor.SyntheticName] = descriptor
			customDescriptors = append(customDescriptors, descriptor)
			localTools = append(localTools, definition)
		default:
			if isUnsupportedBuiltinToolType(toolType) {
				return nil, customToolTransportPlan{}, nil, "", domain.NewValidationError("tools", "tool type "+`"`+toolType+`"`+" is not supported by shim-local responses")
			}
			return nil, customToolTransportPlan{}, nil, "", domain.NewValidationError("tools", "tool type "+`"`+toolType+`"`+" is not supported by shim-local tool loop")
		}
	}

	plan.DroppedBuiltinTools = droppedBuiltinTools

	var toolChoice any
	if rawChoice := rewrittenToolChoice; len(bytes.TrimSpace(rawChoice)) > 0 {
		rewrittenChoice, err := remapToolChoice(rawChoice, rawFields, tools, plan, forceCodexToolChoiceRequired)
		if err != nil {
			return nil, customToolTransportPlan{}, nil, "", err
		}
		toolChoice = rewrittenChoice
		plan.ToolChoiceContract = deriveToolChoiceContract(rawChoice, rewrittenChoice)
	}

	return localTools, plan, toolChoice, buildLocalCustomToolLoopInstructions(customDescriptors), nil
}

func applyLocalAllowedToolsSubset(rawChoice json.RawMessage, tools []map[string]any) ([]map[string]any, json.RawMessage, error) {
	trimmed := bytes.TrimSpace(rawChoice)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, nil, nil
	}
	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil || !strings.EqualFold(strings.TrimSpace(choiceType), "allowed_tools") {
		return nil, nil, nil
	}

	var mode string
	if err := json.Unmarshal(payload["mode"], &mode); err != nil {
		return nil, nil, domain.NewValidationError("tool_choice", "allowed_tools mode is required")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "auto" && mode != "required" {
		return nil, nil, domain.NewValidationError("tool_choice", "allowed_tools mode must be auto or required")
	}

	var rawAllowed []map[string]any
	if err := json.Unmarshal(payload["tools"], &rawAllowed); err != nil || len(rawAllowed) == 0 {
		return nil, nil, domain.NewValidationError("tool_choice", "allowed_tools.tools must be a non-empty array")
	}

	allowed := make(map[string]struct{}, len(rawAllowed))
	for _, ref := range rawAllowed {
		key, err := allowedToolChoiceKey(ref)
		if err != nil {
			return nil, nil, err
		}
		allowed[key] = struct{}{}
	}

	filtered := make([]map[string]any, 0, len(tools))
	matched := make(map[string]struct{}, len(allowed))
	for _, tool := range tools {
		key, ok := localToolChoiceKey(tool)
		if !ok {
			continue
		}
		if _, exists := allowed[key]; !exists {
			continue
		}
		filtered = append(filtered, tool)
		matched[key] = struct{}{}
	}

	if len(filtered) == 0 {
		return nil, nil, domain.NewValidationError("tool_choice", "allowed_tools did not match any declared shim-local tools")
	}
	for key := range allowed {
		if _, ok := matched[key]; !ok {
			return nil, nil, domain.NewValidationError("tool_choice", "allowed_tools must reference only declared shim-local tools")
		}
	}

	rewrittenChoice := json.RawMessage(`"` + mode + `"`)
	return filtered, rewrittenChoice, nil
}

func allowedToolChoiceKey(payload map[string]any) (string, error) {
	switch strings.ToLower(strings.TrimSpace(asString(payload["type"]))) {
	case "function":
		name := strings.TrimSpace(asString(payload["name"]))
		if name == "" {
			return "", domain.NewValidationError("tool_choice", "allowed_tools function entries require a name")
		}
		return "function:" + name, nil
	case "custom", "custom_tool":
		name, namespace := customToolIdentity(payload)
		if name == "" {
			return "", domain.NewValidationError("tool_choice", "allowed_tools custom tool entries require a name")
		}
		return "custom:" + canonicalCustomToolKey(namespace, name), nil
	default:
		return "", domain.NewValidationError("tool_choice", "allowed_tools currently supports only function and custom tool entries in shim-local mode")
	}
}

func localToolChoiceKey(tool map[string]any) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(asString(tool["type"]))) {
	case "function":
		name := strings.TrimSpace(asString(tool["name"]))
		if name == "" {
			return "", false
		}
		return "function:" + name, true
	case "custom", "custom_tool":
		name, namespace := customToolIdentity(tool)
		if name == "" {
			return "", false
		}
		return "custom:" + canonicalCustomToolKey(namespace, name), true
	default:
		return "", false
	}
}

func buildLocalFunctionToolDefinition(tool map[string]any) (map[string]any, string, error) {
	name := strings.TrimSpace(asString(tool["name"]))
	if name == "" {
		return nil, "", domain.NewValidationError("tools", "function tool name is required")
	}
	function := map[string]any{"name": name}
	if description := strings.TrimSpace(asString(tool["description"])); description != "" {
		function["description"] = description
	}
	if parameters, ok := tool["parameters"]; ok {
		function["parameters"] = parameters
	}
	return map[string]any{
		"type":     "function",
		"function": function,
	}, name, nil
}

func buildLocalCustomToolDefinition(tool map[string]any) (customToolDescriptor, map[string]any, error) {
	name, namespace := customToolIdentity(tool)
	if name == "" {
		return customToolDescriptor{}, nil, domain.NewValidationError("tools", "custom tool name is required")
	}

	constraint, err := compileCustomToolConstraint(tool)
	if err != nil {
		return customToolDescriptor{}, nil, domain.NewValidationError("tools", err.Error())
	}

	descriptor := customToolDescriptor{
		Name:          name,
		Namespace:     namespace,
		SyntheticName: syntheticCustomToolName(namespace, name),
		Transport:     "bridge",
		Constraint:    constraint,
	}
	if constraint != nil {
		descriptor.Transport = customToolTransportLocalConstrained
	}

	description := strings.TrimSpace(asString(tool["description"]))
	description = appendToolDescriptionHint(description, customToolArgumentDescription(descriptor))
	if descriptor.Constraint != nil {
		description = appendToolDescriptionHint(description, descriptor.Constraint.DescriptionHint())
	}

	function := map[string]any{
		"name": descriptor.Name,
		"parameters": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": customToolArgumentDescription(descriptor),
				},
			},
			"required": []string{"input"},
		},
	}
	if description != "" {
		function["description"] = description
	}

	return descriptor, map[string]any{
		"type":     "function",
		"function": function,
	}, nil
}

func hasConstrainedCustomTools(bridge customToolBridge) bool {
	for _, descriptor := range bridge.ByCanonical {
		if descriptor.Constraint != nil {
			return true
		}
	}
	return false
}

func buildLocalCustomToolLoopInstructions(descriptors []customToolDescriptor) string {
	if len(descriptors) == 0 {
		return ""
	}

	parts := []string{
		"Custom tools in this environment are exposed as function calls with one required JSON string argument named `input`. Put the exact raw tool input into that string and do not wrap it in any extra prose.",
	}
	for _, descriptor := range descriptors {
		if descriptor.Constraint == nil {
			continue
		}
		label := descriptor.Name
		if descriptor.Namespace != "" {
			label = descriptor.Namespace + "." + descriptor.Name
		}
		parts = append(parts, "For custom tool `"+label+"`, the raw `input` string must fully satisfy this "+descriptor.Constraint.Syntax+" constraint: "+descriptor.Constraint.Definition)
	}
	return strings.Join(parts, " ")
}

func insertLocalToolLoopInstructions(items []domain.Item, currentInputLen int, instructions string) []domain.Item {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return items
	}
	if currentInputLen < 0 || currentInputLen > len(items) {
		currentInputLen = 0
	}
	insertAt := len(items) - currentInputLen
	hintItem := domain.NewInputTextMessage("system", instructions)

	out := make([]domain.Item, 0, len(items)+1)
	out = append(out, items[:insertAt]...)
	out = append(out, hintItem)
	out = append(out, items[insertAt:]...)
	return out
}

func validateLocalConstrainedToolCall(item domain.Item, bridge customToolBridge, prefixItems []domain.Item) error {
	if item.Type != "custom_tool_call" {
		return nil
	}
	descriptor, ok := bridge.ByCanonicalIdentity(item.Name(), item.Namespace())
	if !ok || descriptor.Constraint == nil {
		return nil
	}
	if err := descriptor.Constraint.Validate(item.Input()); err != nil {
		clonedPrefix := append([]domain.Item(nil), prefixItems...)
		return &constrainedCustomToolValidationError{
			Descriptor:  descriptor,
			Input:       item.Input(),
			Cause:       err,
			CallID:      item.CallID(),
			PrefixItems: clonedPrefix,
		}
	}
	return nil
}

func buildConstrainedCustomToolRepairPrompt(err *constrainedCustomToolValidationError) string {
	if err == nil {
		return ""
	}
	name := err.Descriptor.Name
	if err.Descriptor.Namespace != "" {
		name = err.Descriptor.Namespace + "." + err.Descriptor.Name
	}
	constraintType := "grammar"
	constraintSyntax := ""
	constraintDefinition := ""
	if err.Descriptor.Constraint != nil {
		if strings.TrimSpace(err.Descriptor.Constraint.FormatType) != "" {
			constraintType = err.Descriptor.Constraint.FormatType
		}
		constraintSyntax = strings.TrimSpace(err.Descriptor.Constraint.Syntax)
		constraintDefinition = strings.TrimSpace(err.Descriptor.Constraint.Definition)
	}
	var builder strings.Builder
	builder.WriteString("Previous attempt for custom tool `")
	builder.WriteString(name)
	builder.WriteString("` produced invalid raw input ")
	builder.WriteString(fmt.Sprintf("%q", err.Input))
	builder.WriteString(". Retry by emitting the same tool call again with corrected `input` only. Do not answer with normal assistant text.")
	if constraintDefinition != "" {
		builder.WriteString(" The `input` must fully satisfy the required ")
		builder.WriteString(constraintType)
		if constraintSyntax != "" {
			builder.WriteString(" (")
			builder.WriteString(constraintSyntax)
			builder.WriteString(")")
		}
		builder.WriteString(" definition: ")
		builder.WriteString(constraintDefinition)
		builder.WriteString(".")
	}
	return builder.String()
}

func buildConstrainedCustomToolRepairExhaustedError(err *constrainedCustomToolValidationError, attempts int) error {
	if err == nil {
		return &llama.InvalidResponseError{Message: "shim-local constrained custom tool repair loop failed"}
	}
	name := err.Descriptor.Name
	if err.Descriptor.Namespace != "" {
		name = err.Descriptor.Namespace + "." + err.Descriptor.Name
	}
	return &llama.InvalidResponseError{
		Message: fmt.Sprintf("shim-local constrained custom tool %s failed to satisfy its constraint after %d attempts; last invalid input was %q", name, attempts, err.Input),
	}
}
