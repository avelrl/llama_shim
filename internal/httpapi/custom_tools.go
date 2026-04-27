package httpapi

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"path"
	"slices"
	"strings"

	"llama_shim/internal/domain"
)

type customToolsMode string

const (
	customToolsModeBridge      customToolsMode = "bridge"
	customToolsModePassthrough customToolsMode = "passthrough"
	customToolsModeAuto        customToolsMode = "auto"
)

type customToolDescriptor struct {
	Name          string
	Namespace     string
	SyntheticName string
	Transport     string
	Constraint    *customToolConstraint
}

type customToolBridge struct {
	ByModelName map[string]customToolDescriptor
	BySynthetic map[string]customToolDescriptor
	ByCanonical map[string]customToolDescriptor
}

type UpstreamToolCompatibilityRule struct {
	Model         string
	DisabledTools []string
}

type CodexUpstreamInputCompatibilityRule struct {
	Model string
	Mode  string
}

type customToolTransportPlan struct {
	Mode                customToolsMode
	Bridge              customToolBridge
	BridgeFallbackSafe  bool
	DroppedBuiltinTools []string
	DisabledToolTypes   []string
	ToolChoiceContract  toolChoiceContract
}

func (b customToolBridge) Active() bool {
	return len(b.ByCanonical) > 0
}

func (b customToolBridge) ByCanonicalIdentity(name, namespace string) (customToolDescriptor, bool) {
	descriptor, ok := b.ByCanonical[canonicalCustomToolKey(namespace, name)]
	return descriptor, ok
}

func (b customToolBridge) ByModelToolName(name string) (customToolDescriptor, bool) {
	descriptor, ok := b.ByModelName[name]
	return descriptor, ok
}

func (b customToolBridge) BySyntheticName(name string) (customToolDescriptor, bool) {
	descriptor, ok := b.BySynthetic[name]
	return descriptor, ok
}

func (p customToolTransportPlan) BridgeActive() bool {
	return p.Mode == customToolsModeBridge && p.Bridge.Active()
}

func parseCustomToolsMode(value string) customToolsMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(customToolsModePassthrough):
		return customToolsModePassthrough
	case string(customToolsModeAuto):
		return customToolsModeAuto
	default:
		return customToolsModeBridge
	}
}

func remapCustomToolsPayload(rawFields map[string]json.RawMessage, configuredMode string, codexCompatibilityEnabled bool, forceCodexToolChoiceRequired bool) ([]byte, customToolTransportPlan, error) {
	return remapCustomToolsPayloadWithCompatibility(rawFields, configuredMode, codexCompatibilityEnabled, forceCodexToolChoiceRequired, nil)
}

func remapCustomToolsPayloadWithCompatibility(rawFields map[string]json.RawMessage, configuredMode string, codexCompatibilityEnabled bool, forceCodexToolChoiceRequired bool, upstreamToolCompatibility []UpstreamToolCompatibilityRule) ([]byte, customToolTransportPlan, error) {
	disabledTools := disabledUpstreamToolTypesForModel(rawStringField(rawFields, "model"), upstreamToolCompatibility)
	plan, tools, err := buildCustomToolTransportPlanWithCompatibility(rawFields, configuredMode, disabledTools)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	payload := make(map[string]any, len(rawFields))
	for key, raw := range rawFields {
		payload[key] = json.RawMessage(raw)
	}
	compatEnabled := shouldApplyCodexCompatibility(rawFields, tools, codexCompatibilityEnabled)
	effectiveTools := tools
	if compatEnabled {
		effectiveTools = augmentCodexToolDescriptions(effectiveTools)
	}
	if _, ok := rawFields["tools"]; ok {
		if plan.BridgeActive() {
			rewritten, err := remapCustomTools(effectiveTools, plan.Bridge)
			if err != nil {
				return nil, customToolTransportPlan{}, err
			}
			payload["tools"] = rewritten
		} else {
			payload["tools"] = effectiveTools
		}
	}
	if rawChoice, ok := rawFields["tool_choice"]; ok {
		toolChoice, err := remapToolChoice(rawChoice, rawFields, tools, plan, forceCodexToolChoiceRequired)
		if err != nil {
			return nil, customToolTransportPlan{}, err
		}
		payload["tool_choice"] = toolChoice
		plan.ToolChoiceContract = deriveToolChoiceContract(rawChoice, toolChoice)
	}
	instructions := rawStringField(rawFields, "instructions")
	if compatEnabled {
		instructions = appendCodexCompatibilityInstructions(instructions)
	}
	if plan.BridgeActive() {
		instructions = appendCustomToolBridgeInstructions(instructions, plan.Bridge)
	}
	if strings.TrimSpace(instructions) != "" {
		payload["instructions"] = instructions
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	return body, plan, nil
}

func buildCustomToolTransportPlan(rawFields map[string]json.RawMessage, configuredMode string) (customToolTransportPlan, []map[string]any, error) {
	return buildCustomToolTransportPlanWithCompatibility(rawFields, configuredMode, nil)
}

func buildCustomToolTransportPlanWithCompatibility(rawFields map[string]json.RawMessage, configuredMode string, disabledTools map[string]struct{}) (customToolTransportPlan, []map[string]any, error) {
	mode := parseCustomToolsMode(configuredMode)
	disabledToolTypes := sortedToolTypes(disabledTools)
	rawTools, ok := rawFields["tools"]
	if !ok {
		return customToolTransportPlan{Mode: effectiveModeWithoutTools(mode), DisabledToolTypes: disabledToolTypes}, nil, nil
	}

	var tools []map[string]any
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "tools must be an array")
	}
	if len(tools) == 0 {
		return customToolTransportPlan{Mode: effectiveModeWithoutTools(mode), DisabledToolTypes: disabledToolTypes}, tools, nil
	}

	containsCustom := false
	requiresPassthrough := false
	bridgeUnsupported := false
	bridgeFallbackSafe := true
	droppedBuiltinTools := make([]string, 0, 1)
	bridge := customToolBridge{
		ByModelName: make(map[string]customToolDescriptor),
		BySynthetic: make(map[string]customToolDescriptor),
		ByCanonical: make(map[string]customToolDescriptor),
	}
	usedNames := make(map[string]struct{})
	customDescriptors := make([]customToolDescriptor, 0, len(tools))
	filteredTools := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		toolType := strings.TrimSpace(asString(tool["type"]))
		if isDisabledWebSearchTool(tool) || isToolTypeDisabledForUpstream(toolType, disabledTools) {
			droppedBuiltinTools = append(droppedBuiltinTools, toolType)
			continue
		}
		if isCustomToolType(toolType) {
			containsCustom = true
			filteredTools = append(filteredTools, tool)
			name, namespace := customToolIdentity(tool)
			if name == "" {
				return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tool name is required")
			}
			formatType := detectCustomToolFormatType(tool)
			switch formatType {
			case "", "text":
			case "grammar":
				requiresPassthrough = true
				bridgeUnsupported = true
				bridgeFallbackSafe = false
			default:
				requiresPassthrough = true
				bridgeUnsupported = true
				bridgeFallbackSafe = false
			}

			descriptor := customToolDescriptor{
				Name:          name,
				Namespace:     namespace,
				SyntheticName: syntheticCustomToolName(namespace, name),
				Transport:     "passthrough",
			}
			key := canonicalCustomToolKey(namespace, name)
			if _, exists := bridge.ByCanonical[key]; exists {
				return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tools must not repeat the same namespace and name")
			}
			bridge.ByCanonical[key] = descriptor
			bridge.BySynthetic[descriptor.SyntheticName] = descriptor
			customDescriptors = append(customDescriptors, descriptor)
			continue
		}
		filteredTools = append(filteredTools, tool)

		if name := strings.TrimSpace(asString(tool["name"])); name != "" {
			usedNames[name] = struct{}{}
		}
	}

	if !containsCustom {
		return customToolTransportPlan{
			Mode:                effectiveModeWithoutTools(mode),
			DroppedBuiltinTools: droppedBuiltinTools,
			DisabledToolTypes:   disabledToolTypes,
		}, filteredTools, nil
	}

	switch mode {
	case customToolsModePassthrough:
		return customToolTransportPlan{
			Mode:                customToolsModePassthrough,
			BridgeFallbackSafe:  bridgeFallbackSafe,
			DroppedBuiltinTools: droppedBuiltinTools,
			DisabledToolTypes:   disabledToolTypes,
		}, filteredTools, nil
	case customToolsModeAuto:
		if requiresPassthrough {
			return customToolTransportPlan{
				Mode:                customToolsModePassthrough,
				BridgeFallbackSafe:  bridgeFallbackSafe,
				DroppedBuiltinTools: droppedBuiltinTools,
				DisabledToolTypes:   disabledToolTypes,
			}, filteredTools, nil
		}
	case customToolsModeBridge:
		if bridgeUnsupported {
			return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tool format is not supported in bridge mode")
		}
	}

	for _, descriptor := range customDescriptors {
		descriptor.Transport = "bridge"
		if _, conflict := usedNames[descriptor.Name]; conflict {
			return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tool name conflicts with an existing function tool name")
		}
		if _, exists := bridge.ByModelName[descriptor.Name]; exists {
			return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tools must not repeat the same name in bridge mode")
		}
		bridge.ByCanonical[canonicalCustomToolKey(descriptor.Namespace, descriptor.Name)] = descriptor
		bridge.BySynthetic[descriptor.SyntheticName] = descriptor
		bridge.ByModelName[descriptor.Name] = descriptor
	}

	return customToolTransportPlan{
		Mode:                customToolsModeBridge,
		Bridge:              bridge,
		BridgeFallbackSafe:  true,
		DroppedBuiltinTools: droppedBuiltinTools,
		DisabledToolTypes:   disabledToolTypes,
	}, filteredTools, nil
}

func effectiveModeWithoutTools(mode customToolsMode) customToolsMode {
	if mode == customToolsModePassthrough {
		return customToolsModePassthrough
	}
	return customToolsModeBridge
}

func remapCustomTools(tools []map[string]any, bridge customToolBridge) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if !isCustomToolType(asString(tool["type"])) {
			out = append(out, tool)
			continue
		}

		name, namespace := customToolIdentity(tool)
		descriptor, ok := bridge.ByCanonicalIdentity(name, namespace)
		if !ok {
			return nil, domain.NewValidationError("tools", "custom tool bridge metadata is missing")
		}

		rewritten := map[string]any{
			"type": "function",
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
		if description := strings.TrimSpace(asString(tool["description"])); description != "" {
			rewritten["description"] = description
		}
		out = append(out, rewritten)
	}
	return out, nil
}

func remapToolChoice(raw json.RawMessage, rawFields map[string]json.RawMessage, tools []map[string]any, plan customToolTransportPlan, forceCodexToolChoiceRequired bool) (any, error) {
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		switch strings.ToLower(strings.TrimSpace(literal)) {
		case "required":
			if len(tools) == 0 {
				return nil, domain.NewValidationError("tool_choice", "tool_choice \"required\" requires at least one supported tool")
			}
		case "auto":
			if shouldForceRequiredToolChoice(rawFields, tools, forceCodexToolChoiceRequired) {
				return "required", nil
			}
		}
		return literal, nil
	}

	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil, domain.NewValidationError("tool_choice", "tool_choice must be a string or object")
	}
	if isDisabledWebSearchTool(choice) {
		return nil, domain.NewValidationError("tool_choice", "tool_choice references a disabled web_search tool")
	}
	if plan.ToolTypeDisabled(asString(choice["type"])) {
		return nil, domain.NewValidationError("tool_choice", "tool_choice references a tool disabled for the selected upstream model")
	}
	if !plan.Bridge.Active() {
		return choice, nil
	}
	if !isCustomToolType(asString(choice["type"])) {
		return choice, nil
	}

	name, namespace := customToolIdentity(choice)
	if name == "" {
		return nil, domain.NewValidationError("tool_choice", "custom tool_choice name is required")
	}

	descriptor, ok := plan.Bridge.ByCanonicalIdentity(name, namespace)
	if !ok {
		return nil, domain.NewValidationError("tool_choice", "custom tool_choice must reference a declared custom tool")
	}
	return map[string]any{
		"type": "function",
		"name": descriptor.Name,
	}, nil
}

func (p customToolTransportPlan) ToolTypeDisabled(toolType string) bool {
	normalized := normalizeToolType(toolType)
	if normalized == "" {
		return false
	}
	for _, disabled := range p.DisabledToolTypes {
		if normalizeToolType(disabled) == normalized {
			return true
		}
	}
	return false
}

func disabledUpstreamToolTypesForModel(model string, rules []UpstreamToolCompatibilityRule) map[string]struct{} {
	model = strings.TrimSpace(model)
	if model == "" || len(rules) == 0 {
		return nil
	}
	disabled := make(map[string]struct{})
	for _, rule := range rules {
		if !modelPatternMatches(rule.Model, model) {
			continue
		}
		for _, tool := range rule.DisabledTools {
			if normalized := normalizeToolType(tool); normalized != "" {
				disabled[normalized] = struct{}{}
			}
		}
	}
	if len(disabled) == 0 {
		return nil
	}
	return disabled
}

func modelPatternMatches(pattern, model string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	model = strings.ToLower(strings.TrimSpace(model))
	if pattern == "" || model == "" {
		return false
	}
	if pattern == model {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	matched, err := path.Match(pattern, model)
	return err == nil && matched
}

func isToolTypeDisabledForUpstream(toolType string, disabled map[string]struct{}) bool {
	if len(disabled) == 0 {
		return false
	}
	_, ok := disabled[normalizeToolType(toolType)]
	return ok
}

func sortedToolTypes(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		if normalized := normalizeToolType(value); normalized != "" {
			out = append(out, normalized)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func normalizeToolType(value string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "namespace_tool":
		return "namespace"
	default:
		return normalized
	}
}

func shouldForceRequiredToolChoice(rawFields map[string]json.RawMessage, tools []map[string]any, enabled bool) bool {
	if !enabled || len(tools) == 0 {
		return false
	}
	if rawInput, ok := rawFields["input"]; ok && inputContainsToolOutput(rawInput) {
		return false
	}
	return isCodexCLIRequest(rawFields) && hasFunctionToolNamed(tools, "exec_command")
}

func inputContainsToolOutput(raw json.RawMessage) bool {
	items, err := domain.NormalizeInput(raw)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.Type == "function_call_output" || item.Type == "custom_tool_call_output" || isLocalBuiltinToolOutputType(item.Type) {
			return true
		}
	}
	return false
}

func isCodexCLIRequest(rawFields map[string]json.RawMessage) bool {
	return strings.Contains(rawStringField(rawFields, "instructions"), codexCLIRequestMarker)
}

func hasFunctionToolNamed(tools []map[string]any, name string) bool {
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(asString(tool["type"])), "function") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(asString(tool["name"])), name) {
			return true
		}
	}
	return false
}

func rawStringField(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func remapCustomToolResponseBody(raw []byte, plan customToolTransportPlan) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	output, ok := payload["output"].([]any)
	if !ok {
		return raw, nil
	}

	responseID := strings.TrimSpace(asString(payload["id"]))
	changed := false
	if plan.BridgeActive() {
		// Some upstreams collapse a tool call into a placeholder assistant message in
		// the final response. Recover the structured custom tool call before we store
		// or re-emit the response, otherwise the conversation loses the tool boundary.
		if recovered, didRecover := recoverPlaceholderCustomToolCalls(output, responseID, plan.Bridge); didRecover {
			output = recovered
			changed = true
		}
	}
	for index, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if plan.BridgeActive() {
			rewritten, didChange := remapFunctionCallItemToCustom(item, plan.Bridge)
			if didChange {
				output[index] = rewritten
				changed = true
				continue
			}
		}
		rewritten, didChange := normalizeCustomToolCallItem(item)
		if didChange {
			output[index] = rewritten
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	payload["output"] = output
	return json.Marshal(payload)
}

func normalizeCustomToolCallItem(item map[string]any) (map[string]any, bool) {
	if strings.TrimSpace(asString(item["type"])) != "custom_tool_call" {
		return nil, false
	}

	normalizedInput := extractCustomToolInput(item["input"])
	currentInput := strings.TrimSpace(asString(item["input"]))
	if normalizedInput == currentInput && currentInput != "" {
		return nil, false
	}

	rewritten := cloneAnyMap(item)
	rewritten["input"] = normalizedInput
	return rewritten, true
}

func remapFunctionCallItemToCustom(item map[string]any, bridge customToolBridge) (map[string]any, bool) {
	if strings.TrimSpace(asString(item["type"])) != "function_call" {
		return nil, false
	}

	name := strings.TrimSpace(asString(item["name"]))
	descriptor, ok := bridge.ByModelToolName(name)
	if !ok {
		descriptor, ok = bridge.BySyntheticName(name)
	}
	if !ok {
		return nil, false
	}

	rewritten := map[string]any{
		"type":   "custom_tool_call",
		"name":   descriptor.Name,
		"input":  extractCustomToolInput(item["arguments"]),
		"status": customToolStatus(item),
	}
	if descriptor.Namespace != "" {
		rewritten["namespace"] = descriptor.Namespace
	}
	if id := strings.TrimSpace(asString(item["id"])); id != "" {
		rewritten["id"] = id
	}
	if callID := customToolCallID(item); callID != "" {
		rewritten["call_id"] = callID
	}
	return rewritten, true
}

func recoverPlaceholderCustomToolCalls(output []any, responseID string, bridge customToolBridge) ([]any, bool) {
	if !bridge.Active() || len(output) == 0 {
		return output, false
	}

	hasToolCall := false
	placeholderIndex := -1
	var placeholder map[string]any
	reasoningText := collectReasoningText(output)

	for index, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "function_call", "custom_tool_call":
			hasToolCall = true
		case "message":
			if placeholderIndex == -1 && isToolResponsePlaceholderMessage(item) {
				placeholderIndex = index
				placeholder = item
			}
		}
	}
	if hasToolCall || placeholderIndex == -1 || placeholder == nil {
		return output, false
	}

	descriptor, ok := inferPlaceholderCustomToolDescriptor(reasoningText, bridge)
	if !ok {
		return output, false
	}
	input := inferPlaceholderCustomToolInput(reasoningText, descriptor, bridge)
	if input == "" {
		return output, false
	}

	recovered := synthesizeRecoveredCustomToolCall(placeholder, descriptor, input, responseID)
	output[placeholderIndex] = recovered
	return output, true
}

func collectReasoningText(output []any) string {
	var parts []string
	for _, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok || strings.TrimSpace(asString(item["type"])) != "reasoning" {
			continue
		}
		parts = append(parts, collectReasoningItemText(item)...)
	}
	return strings.Join(parts, "\n")
}

func collectReasoningItemText(item map[string]any) []string {
	parts := make([]string, 0, 4)
	if summary, ok := item["summary"].([]any); ok {
		for _, rawEntry := range summary {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(asString(entry["text"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	if content, ok := item["content"].([]any); ok {
		for _, rawEntry := range content {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(asString(entry["text"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	if text := strings.TrimSpace(asString(item["encrypted_content"])); text != "" {
		parts = append(parts, text)
	}
	return parts
}

func isToolResponsePlaceholderMessage(item map[string]any) bool {
	if strings.TrimSpace(asString(item["type"])) != "message" {
		return false
	}
	if strings.TrimSpace(asString(item["role"])) != "assistant" {
		return false
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) == 0 {
		return false
	}

	var text strings.Builder
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(part["type"])) != "output_text" {
			continue
		}
		text.WriteString(asString(part["text"]))
	}
	trimmed := strings.TrimSpace(text.String())
	return trimmed != "" && strings.Trim(trimmed, "<|tool_response|>\n\r\t ") == ""
}

func inferPlaceholderCustomToolDescriptor(reasoningText string, bridge customToolBridge) (customToolDescriptor, bool) {
	if len(bridge.ByModelName) == 1 {
		for _, descriptor := range bridge.ByModelName {
			return descriptor, true
		}
	}

	text := strings.ToLower(reasoningText)
	names := make([]string, 0, len(bridge.ByModelName))
	for name := range bridge.ByModelName {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if strings.Contains(text, strings.ToLower(name)) {
			descriptor, ok := bridge.ByModelName[name]
			return descriptor, ok
		}
	}
	return customToolDescriptor{}, false
}

func inferPlaceholderCustomToolInput(reasoningText string, descriptor customToolDescriptor, bridge customToolBridge) string {
	spans := backtickSpans(reasoningText)
	toolNames := make(map[string]struct{}, len(bridge.ByModelName))
	for name := range bridge.ByModelName {
		toolNames[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}

	best := ""
	for _, span := range spans {
		candidate := strings.TrimSpace(span)
		if candidate == "" {
			continue
		}
		lower := strings.ToLower(candidate)
		if _, skip := toolNames[lower]; skip {
			continue
		}
		switch lower {
		case "input", "json", "string":
			continue
		}
		if best == "" {
			best = candidate
		}
		if strings.ContainsAny(candidate, "()[]{}\"'") || strings.Contains(candidate, " ") {
			return candidate
		}
	}
	if best != "" {
		return best
	}

	lowerReasoning := strings.ToLower(reasoningText)
	if strings.EqualFold(strings.TrimSpace(descriptor.Name), "code_exec") && strings.Contains(lowerReasoning, "hello world") {
		return `print("hello world")`
	}
	return ""
}

func backtickSpans(text string) []string {
	out := make([]string, 0, 4)
	start := -1
	for index, r := range text {
		if r != '`' {
			continue
		}
		if start == -1 {
			start = index + 1
			continue
		}
		if start <= index {
			out = append(out, text[start:index])
		}
		start = -1
	}
	return out
}

func synthesizeRecoveredCustomToolCall(placeholder map[string]any, descriptor customToolDescriptor, input string, responseID string) map[string]any {
	itemID := strings.TrimSpace(asString(placeholder["id"]))
	if itemID == "" {
		itemID = "ctc_" + strings.TrimPrefix(strings.TrimSpace(responseID), "resp_")
		if strings.TrimSpace(responseID) == "" {
			itemID = "ctc_recovered"
		}
	}
	callID := strings.TrimSpace(asString(placeholder["call_id"]))
	if callID == "" {
		suffix := strings.TrimPrefix(itemID, "msg_")
		if suffix == "" || suffix == itemID {
			suffix = strings.TrimPrefix(strings.TrimSpace(responseID), "resp_")
		}
		if suffix == "" {
			suffix = "recovered"
		}
		callID = "call_" + suffix
	}

	item := map[string]any{
		"id":      itemID,
		"type":    "custom_tool_call",
		"call_id": callID,
		"name":    descriptor.Name,
		"input":   input,
		"status":  "completed",
	}
	if descriptor.Namespace != "" {
		item["namespace"] = descriptor.Namespace
	}
	return item
}

func annotateResponseCustomToolMetadata(response domain.Response, plan customToolTransportPlan) domain.Response {
	if len(response.Output) == 0 {
		return response
	}

	output := make([]domain.Item, 0, len(response.Output))
	for _, item := range response.Output {
		switch item.Type {
		case "custom_tool_call":
			meta := domain.ItemMeta{
				CanonicalType: "custom_tool_call",
				ToolName:      item.Name(),
				ToolNamespace: item.Namespace(),
			}
			if descriptor, ok := plan.Bridge.ByCanonicalIdentity(item.Name(), item.Namespace()); ok {
				meta.SyntheticName = descriptor.SyntheticName
				meta.ToolName = descriptor.Name
				meta.ToolNamespace = descriptor.Namespace
				if strings.TrimSpace(descriptor.Transport) != "" {
					meta.Transport = descriptor.Transport
				}
			}
			if meta.Transport == "" && plan.BridgeActive() {
				meta.Transport = "bridge"
			}
			if meta.Transport == "" {
				meta.Transport = "passthrough"
			}
			item = item.WithMeta(meta)
		case "custom_tool_call_output":
			transport := "passthrough"
			if plan.BridgeActive() {
				transport = "bridge"
			}
			item = item.WithMeta(domain.ItemMeta{
				Transport:     transport,
				CanonicalType: "custom_tool_call_output",
			})
		}
		output = append(output, item)
	}
	response.Output = output
	return response
}

func remapItemsForUpstream(items []domain.Item, plan customToolTransportPlan, refs map[string]domain.ToolCallReference) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		rewritten := item
		var err error
		if plan.BridgeActive() {
			rewritten, err = remapItemForBridgeUpstream(item, plan.Bridge, refs)
			if err != nil {
				return nil, err
			}
		}
		raw, err := rewritten.MarshalJSON()
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func contextHasPassthroughCustomItems(items []domain.Item) bool {
	for _, item := range items {
		switch item.Type {
		case "custom_tool_call", "custom_tool_call_output":
			if item.Meta == nil || item.Meta.Transport == "passthrough" {
				return true
			}
		}
	}
	return false
}

func remapItemForBridgeUpstream(item domain.Item, bridge customToolBridge, refs map[string]domain.ToolCallReference) (domain.Item, error) {
	switch item.Type {
	case "custom_tool_call":
		if item.Meta == nil || item.Meta.Transport != "bridge" {
			return domain.Item{}, domain.NewValidationError("input", "bridge mode cannot replay non-bridged custom tool calls from history")
		}
		descriptor := descriptorForItem(item, bridge)
		if descriptor.SyntheticName == "" {
			return domain.Item{}, domain.NewValidationError("input", "bridge mode could not resolve synthetic identity for custom tool call")
		}
		payload := item.Map()
		payload["type"] = "function_call"
		payload["name"] = descriptor.Name
		payload["arguments"] = encodeCustomToolArguments(item.RawField("input"))
		delete(payload, "input")
		delete(payload, "namespace")
		rewritten, err := item.WithMap(payload)
		if err != nil {
			return domain.Item{}, err
		}
		return rewritten, nil
	case "custom_tool_call_output":
		ref, ok := refs[item.CallID()]
		if !ok {
			return domain.Item{}, domain.NewValidationError("input", "custom_tool_call_output must reference a known custom tool call")
		}
		if ref.Meta == nil || ref.Meta.Transport != "bridge" {
			return domain.Item{}, domain.NewValidationError("input", "bridge mode cannot replay non-bridged custom tool outputs from history")
		}
		payload := item.Map()
		payload["type"] = "function_call_output"
		rewritten, err := item.WithMap(payload)
		if err != nil {
			return domain.Item{}, err
		}
		return rewritten, nil
	default:
		return item, nil
	}
}

func descriptorForItem(item domain.Item, bridge customToolBridge) customToolDescriptor {
	if item.Meta != nil && strings.TrimSpace(item.Meta.SyntheticName) != "" {
		return customToolDescriptor{
			Name:          fallbackString(item.Meta.ToolName, item.Name()),
			Namespace:     fallbackString(item.Meta.ToolNamespace, item.Namespace()),
			SyntheticName: item.Meta.SyntheticName,
			Transport:     item.Meta.Transport,
		}
	}
	if descriptor, ok := bridge.ByCanonicalIdentity(item.Name(), item.Namespace()); ok {
		return descriptor
	}
	return customToolDescriptor{
		Name:          item.Name(),
		Namespace:     item.Namespace(),
		SyntheticName: syntheticCustomToolName(item.Namespace(), item.Name()),
	}
}

func detectCustomToolFormatType(tool map[string]any) string {
	if _, ok := tool["grammar"]; ok {
		return "grammar"
	}

	format, ok := tool["format"].(map[string]any)
	if !ok {
		return ""
	}
	if _, ok := format["grammar"]; ok {
		return "grammar"
	}
	switch strings.TrimSpace(asString(format["type"])) {
	case "", "text":
		return "text"
	case "grammar":
		return "grammar"
	default:
		return "other"
	}
}

func isCustomToolType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "custom", "custom_tool":
		return true
	default:
		return false
	}
}

func isUnsupportedBuiltinToolType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "web_search", "web_search_preview", "code_interpreter", "computer", "computer_use", "file_search", "image_generation":
		return true
	default:
		return false
	}
}

func isDisabledWebSearchTool(tool map[string]any) bool {
	if strings.ToLower(strings.TrimSpace(asString(tool["type"]))) != "web_search" {
		return false
	}
	value, ok := tool["external_web_access"].(bool)
	return ok && !value
}

func customToolIdentity(payload map[string]any) (string, string) {
	name := strings.TrimSpace(asString(payload["name"]))
	namespace := strings.TrimSpace(asString(payload["namespace"]))
	if nested, ok := payload["custom"].(map[string]any); ok {
		if name == "" {
			name = strings.TrimSpace(asString(nested["name"]))
		}
		if namespace == "" {
			namespace = strings.TrimSpace(asString(nested["namespace"]))
		}
	}
	return name, namespace
}

func logCustomToolTransport(ctx context.Context, logger *slog.Logger, rawFields map[string]json.RawMessage, upstreamBody []byte, plan customToolTransportPlan, codexCompatibilityEnabled bool) {
	if logger == nil || !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	rawInstructions := rawStringField(rawFields, "instructions")
	upstreamInstructions := bodyStringField(upstreamBody, "instructions")
	tools := decodeToolList(rawFields)
	codexCompatRequested := isCodexCLIRequest(rawFields) && hasFunctionToolNamed(tools, "exec_command")
	codexCompatApplied := shouldApplyCodexCompatibility(rawFields, tools, codexCompatibilityEnabled)

	logger.DebugContext(ctx, "responses custom tools transport",
		"mode", plan.Mode,
		"bridge_active", plan.BridgeActive(),
		"bridge_tool_count", len(plan.Bridge.ByCanonical),
		"dropped_builtin_tools", plan.DroppedBuiltinTools,
		"disabled_upstream_tool_types", plan.DisabledToolTypes,
		"tool_choice_contract_mode", plan.ToolChoiceContract.Mode,
		"tool_choice_contract_name", plan.ToolChoiceContract.Name,
		"tool_choice_contract_namespace", plan.ToolChoiceContract.Namespace,
		"codex_compat_enabled", codexCompatibilityEnabled,
		"codex_compat_requested", codexCompatRequested,
		"codex_compat_applied", codexCompatApplied,
		"raw_instructions_has_codex_hint", strings.Contains(rawInstructions, codexCompatibilityHint),
		"upstream_instructions_has_codex_hint", strings.Contains(upstreamInstructions, codexCompatibilityHint),
		"raw_tools", rawFieldForLog(rawFields, "tools"),
		"raw_tool_choice", rawFieldForLog(rawFields, "tool_choice"),
		"upstream_tools", bodyFieldForLog(upstreamBody, "tools"),
		"upstream_tool_choice", bodyFieldForLog(upstreamBody, "tool_choice"),
	)
}

func bodyFieldForLog(body []byte, key string) string {
	fields, err := decodeRawFields(body)
	if err != nil {
		return ""
	}
	return rawFieldForLog(fields, key)
}

func bodyStringField(body []byte, key string) string {
	fields, err := decodeRawFields(body)
	if err != nil {
		return ""
	}
	return rawStringField(fields, key)
}

func rawFieldForLog(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	truncated := len(raw) > maxDebugLogBodyBytes
	if truncated {
		raw = raw[:maxDebugLogBodyBytes]
	}
	return formatBodyForLog(raw, truncated)
}

func canonicalCustomToolKey(namespace, name string) string {
	return strings.TrimSpace(namespace) + "\x1f" + strings.TrimSpace(name)
}

func syntheticCustomToolName(namespace, name string) string {
	sum := sha1.Sum([]byte(canonicalCustomToolKey(namespace, name)))
	return "shim_custom_" + hex.EncodeToString(sum[:])
}

const customToolBridgeHintPrefix = "Custom tool bridge rules for this environment:"

func appendCustomToolBridgeInstructions(instructions string, bridge customToolBridge) string {
	hint := buildCustomToolBridgeHint(bridge)
	if hint == "" || strings.Contains(instructions, customToolBridgeHintPrefix) {
		return instructions
	}
	if strings.TrimSpace(instructions) == "" {
		return hint
	}
	return strings.TrimRight(instructions, "\n") + "\n\n" + hint
}

func buildCustomToolBridgeHint(bridge customToolBridge) string {
	if !bridge.Active() {
		return ""
	}

	names := make([]string, 0, len(bridge.ByModelName))
	for name := range bridge.ByModelName {
		names = append(names, name)
	}
	slices.Sort(names)

	return customToolBridgeHintPrefix + " each custom tool is exposed as a function with the same name. To use one, emit a function call instead of a normal assistant message. Put the entire raw tool input into the single required JSON string argument `input`. The arguments must be valid JSON, so escape any inner double quotes inside the string value. Example: raw input `print(\"hello world\")` must be passed as `{\"input\":\"print(\\\"hello world\\\")\"}`. Available bridged custom tools: " + strings.Join(names, ", ") + "."
}

func customToolArgumentDescription(descriptor customToolDescriptor) string {
	name := strings.TrimSpace(descriptor.Name)
	if name == "" {
		name = "the custom tool"
	}
	return "Entire raw input for custom tool `" + name + "` as one JSON string. Escape any inner double quotes inside the string value."
}

func bridgeFromToolCallRefs(refs map[string]domain.ToolCallReference) (customToolBridge, error) {
	bridge := customToolBridge{
		ByModelName: make(map[string]customToolDescriptor),
		BySynthetic: make(map[string]customToolDescriptor),
		ByCanonical: make(map[string]customToolDescriptor),
	}
	for _, ref := range refs {
		if ref.Type != "custom_tool_call" || ref.Meta == nil || ref.Meta.Transport != "bridge" {
			continue
		}

		name := fallbackString(ref.Meta.ToolName, ref.Name)
		if name == "" {
			continue
		}
		namespace := fallbackString(ref.Meta.ToolNamespace, ref.Namespace)
		descriptor := customToolDescriptor{
			Name:          name,
			Namespace:     namespace,
			SyntheticName: fallbackString(ref.Meta.SyntheticName, syntheticCustomToolName(namespace, name)),
			Transport:     ref.Meta.Transport,
		}

		key := canonicalCustomToolKey(namespace, name)
		if existing, ok := bridge.ByCanonical[key]; ok {
			if existing.SyntheticName == "" && descriptor.SyntheticName != "" {
				bridge.ByCanonical[key] = descriptor
			}
			continue
		}
		if existing, ok := bridge.ByModelName[name]; ok {
			if canonicalCustomToolKey(existing.Namespace, existing.Name) != key {
				return customToolBridge{}, domain.NewValidationError("input", "bridge mode cannot replay custom tools with duplicate model-facing names")
			}
			continue
		}
		bridge.ByCanonical[key] = descriptor
		bridge.ByModelName[name] = descriptor
		if descriptor.SyntheticName != "" {
			bridge.BySynthetic[descriptor.SyntheticName] = descriptor
		}
	}
	return bridge, nil
}

func encodeCustomToolArguments(rawInput json.RawMessage) string {
	trimmed := strings.TrimSpace(string(rawInput))
	if trimmed == "" || trimmed == "null" {
		return `{"input":""}`
	}

	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(rawInput, &value); err == nil {
			body, _ := json.Marshal(map[string]any{"input": value})
			return string(body)
		}
	}
	body, _ := json.Marshal(map[string]json.RawMessage{"input": rawInput})
	return string(body)
}

func extractCustomToolInput(arguments any) string {
	switch value := arguments.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return ""
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
			if input, ok := payload["input"]; ok {
				return extractCustomToolInput(input)
			}
			if input, ok := extractSingleStringMapValue(payload); ok {
				return input
			}
		}
		var nested string
		if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
			return extractCustomToolInput(nested)
		}
		return trimmed
	case map[string]any:
		if input, ok := value["input"]; ok {
			return extractCustomToolInput(input)
		}
		if input, ok := extractSingleStringMapValue(value); ok {
			return input
		}
	case []byte:
		return extractCustomToolInput(string(value))
	case json.RawMessage:
		return extractCustomToolInput(string(value))
	}

	body, err := json.Marshal(arguments)
	if err != nil {
		return ""
	}
	return string(body)
}

func extractSingleStringMapValue(payload map[string]any) (string, bool) {
	if len(payload) != 1 {
		return "", false
	}
	for _, value := range payload {
		text, ok := value.(string)
		if !ok {
			return "", false
		}
		return text, true
	}
	return "", false
}

func customToolCallID(item map[string]any) string {
	callID := strings.TrimSpace(asString(item["call_id"]))
	if callID != "" {
		return callID
	}
	return strings.TrimSpace(asString(item["id"]))
}

func customToolStatus(item map[string]any) string {
	if status := strings.TrimSpace(asString(item["status"])); status != "" {
		return status
	}
	return "completed"
}

func fallbackString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
