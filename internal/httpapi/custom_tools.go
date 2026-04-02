package httpapi

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"log/slog"
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
}

type customToolBridge struct {
	BySynthetic map[string]customToolDescriptor
	ByCanonical map[string]customToolDescriptor
}

type customToolTransportPlan struct {
	Mode                customToolsMode
	Bridge              customToolBridge
	DroppedBuiltinTools []string
}

func (b customToolBridge) Active() bool {
	return len(b.BySynthetic) > 0
}

func (b customToolBridge) ByCanonicalIdentity(name, namespace string) (customToolDescriptor, bool) {
	descriptor, ok := b.ByCanonical[canonicalCustomToolKey(namespace, name)]
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

func remapCustomToolsPayload(rawFields map[string]json.RawMessage, configuredMode string, forceCodexToolChoiceRequired bool) ([]byte, customToolTransportPlan, error) {
	plan, tools, err := buildCustomToolTransportPlan(rawFields, configuredMode)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	payload := make(map[string]any, len(rawFields))
	for key, raw := range rawFields {
		payload[key] = json.RawMessage(raw)
	}
	if _, ok := rawFields["tools"]; ok {
		if plan.BridgeActive() {
			rewritten, err := remapCustomTools(tools, plan.Bridge)
			if err != nil {
				return nil, customToolTransportPlan{}, err
			}
			payload["tools"] = rewritten
		} else {
			payload["tools"] = tools
		}
	}
	if rawChoice, ok := rawFields["tool_choice"]; ok {
		toolChoice, err := remapToolChoice(rawChoice, rawFields, tools, plan, forceCodexToolChoiceRequired)
		if err != nil {
			return nil, customToolTransportPlan{}, err
		}
		payload["tool_choice"] = toolChoice
	}
	if shouldApplyCodexCompatibility(rawFields, tools) {
		payload["instructions"] = appendCodexCompatibilityInstructions(rawStringField(rawFields, "instructions"))
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	return body, plan, nil
}

func buildCustomToolTransportPlan(rawFields map[string]json.RawMessage, configuredMode string) (customToolTransportPlan, []map[string]any, error) {
	mode := parseCustomToolsMode(configuredMode)
	rawTools, ok := rawFields["tools"]
	if !ok {
		return customToolTransportPlan{Mode: effectiveModeWithoutTools(mode)}, nil, nil
	}

	var tools []map[string]any
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "tools must be an array")
	}
	if len(tools) == 0 {
		return customToolTransportPlan{Mode: effectiveModeWithoutTools(mode)}, tools, nil
	}

	containsCustom := false
	requiresPassthrough := false
	droppedBuiltinTools := make([]string, 0, 1)
	bridge := customToolBridge{
		BySynthetic: make(map[string]customToolDescriptor),
		ByCanonical: make(map[string]customToolDescriptor),
	}
	usedNames := make(map[string]struct{})
	filteredTools := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		toolType := strings.TrimSpace(asString(tool["type"]))
		if isDisabledWebSearchTool(tool) {
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
			default:
				requiresPassthrough = true
			}

			descriptor := customToolDescriptor{
				Name:          name,
				Namespace:     namespace,
				SyntheticName: syntheticCustomToolName(namespace, name),
			}
			key := canonicalCustomToolKey(namespace, name)
			if _, exists := bridge.ByCanonical[key]; exists {
				return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tools must not repeat the same namespace and name")
			}
			bridge.ByCanonical[key] = descriptor
			bridge.BySynthetic[descriptor.SyntheticName] = descriptor
			continue
		}
		if isUnsupportedBuiltinToolType(toolType) {
			return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "tool type "+`"`+toolType+`"`+" is not supported by this shim backend")
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
		}, filteredTools, nil
	}

	switch mode {
	case customToolsModePassthrough:
		return customToolTransportPlan{Mode: customToolsModePassthrough}, tools, nil
	case customToolsModeAuto:
		if requiresPassthrough {
			return customToolTransportPlan{Mode: customToolsModePassthrough}, tools, nil
		}
	case customToolsModeBridge:
		if requiresPassthrough {
			return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "grammar-constrained custom tools are not supported in bridge mode")
		}
	}

	for synthetic := range bridge.BySynthetic {
		if _, conflict := usedNames[synthetic]; conflict {
			return customToolTransportPlan{}, nil, domain.NewValidationError("tools", "custom tool synthetic identity conflicts with an existing function tool name")
		}
	}

	return customToolTransportPlan{
		Mode:                customToolsModeBridge,
		Bridge:              bridge,
		DroppedBuiltinTools: droppedBuiltinTools,
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
			"name": descriptor.SyntheticName,
			"parameters": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"input": map[string]any{
						"type": "string",
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
	toolType := strings.TrimSpace(asString(choice["type"]))
	if isDisabledWebSearchTool(choice) {
		return nil, domain.NewValidationError("tool_choice", "tool_choice references a disabled web_search tool")
	}
	if isUnsupportedBuiltinToolType(toolType) {
		return nil, domain.NewValidationError("tool_choice", "tool_choice type "+`"`+toolType+`"`+" is not supported by this shim backend")
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
		"name": descriptor.SyntheticName,
	}, nil
}

func shouldForceRequiredToolChoice(rawFields map[string]json.RawMessage, tools []map[string]any, enabled bool) bool {
	if !enabled || len(tools) == 0 {
		return false
	}
	return isCodexCLIRequest(rawFields) && hasFunctionToolNamed(tools, "exec_command")
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
	if !plan.BridgeActive() {
		return raw, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	output, ok := payload["output"].([]any)
	if !ok {
		return raw, nil
	}

	changed := false
	for index, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		rewritten, didChange := remapFunctionCallItemToCustom(item, plan.Bridge)
		if !didChange {
			continue
		}
		output[index] = rewritten
		changed = true
	}
	if !changed {
		return raw, nil
	}
	payload["output"] = output
	return json.Marshal(payload)
}

func remapFunctionCallItemToCustom(item map[string]any, bridge customToolBridge) (map[string]any, bool) {
	if strings.TrimSpace(asString(item["type"])) != "function_call" {
		return nil, false
	}

	descriptor, ok := bridge.BySyntheticName(strings.TrimSpace(asString(item["name"])))
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
			if plan.BridgeActive() {
				meta.Transport = "bridge"
				if descriptor, ok := plan.Bridge.ByCanonicalIdentity(item.Name(), item.Namespace()); ok {
					meta.SyntheticName = descriptor.SyntheticName
					meta.ToolName = descriptor.Name
					meta.ToolNamespace = descriptor.Namespace
				}
			} else {
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
		if item.Meta == nil || item.Meta.Transport == "passthrough" {
			return domain.Item{}, domain.NewValidationError("input", "bridge mode cannot replay passthrough-native custom tool calls from history")
		}
		descriptor := descriptorForItem(item, bridge)
		if descriptor.SyntheticName == "" {
			return domain.Item{}, domain.NewValidationError("input", "bridge mode could not resolve synthetic identity for custom tool call")
		}
		payload := item.Map()
		payload["type"] = "function_call"
		payload["name"] = descriptor.SyntheticName
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
		if ref.Meta == nil || ref.Meta.Transport == "passthrough" {
			return domain.Item{}, domain.NewValidationError("input", "bridge mode cannot replay passthrough-native custom tool outputs from history")
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
	case "web_search", "web_search_preview", "code_interpreter", "computer", "computer_use", "file_search", "image_generation", "mcp":
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

func logCustomToolTransport(ctx context.Context, logger *slog.Logger, rawFields map[string]json.RawMessage, upstreamBody []byte, plan customToolTransportPlan) {
	if logger == nil || !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	logger.DebugContext(ctx, "responses custom tools transport",
		"mode", plan.Mode,
		"bridge_active", plan.BridgeActive(),
		"bridge_tool_count", len(plan.Bridge.BySynthetic),
		"dropped_builtin_tools", plan.DroppedBuiltinTools,
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
				switch typed := input.(type) {
				case string:
					return typed
				default:
					body, _ := json.Marshal(typed)
					return string(body)
				}
			}
		}
		return trimmed
	case map[string]any:
		if input, ok := value["input"]; ok {
			switch typed := input.(type) {
			case string:
				return typed
			default:
				body, _ := json.Marshal(typed)
				return string(body)
			}
		}
	}

	body, err := json.Marshal(arguments)
	if err != nil {
		return ""
	}
	return string(body)
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
