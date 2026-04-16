package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

const localMCPMaxIterations = 3

var shimLocalMCPFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
}

type localMCPServerConfig struct {
	ServerLabel       string
	ServerURL         string
	ConnectorID       string
	Authorization     string
	Headers           map[string]string
	Transport         string
	ServerDescription string
	AllowedTools      []string
	ApprovalPolicy    localMCPApprovalPolicy
	ApprovalRaw       string
}

type localMCPApprovalPolicy struct {
	NeverAll   bool
	NeverTools map[string]struct{}
}

type localMCPToolDefinition struct {
	Name          string
	Description   string
	Annotations   any
	InputSchema   any
	SyntheticName string
}

type localMCPRuntimeServer struct {
	Config localMCPServerConfig
	Tools  []localMCPToolDefinition
	Cached bool
}

type localMCPToolBinding struct {
	Server localMCPRuntimeServer
	Tool   localMCPToolDefinition
}

type localMCPPendingApproval struct {
	ID          string
	ServerLabel string
	Name        string
	Arguments   string
}

type localMCPApprovalResponse struct {
	ApprovalRequestID string
	Approve           bool
}

func supportsLocalMCP(rawFields map[string]json.RawMessage) bool {
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		if _, ok := shimLocalMCPFields[key]; ok {
			continue
		}
		return false
	}

	_, err := parseLocalMCPToolConfigs(rawFields)
	return err == nil
}

func hasDeclaredMCPTools(rawFields map[string]json.RawMessage) bool {
	for _, tool := range decodeToolList(rawFields) {
		if strings.TrimSpace(asString(tool["type"])) == "mcp" {
			return true
		}
	}
	return false
}

func hasConnectorMCPTools(rawFields map[string]json.RawMessage) bool {
	for _, tool := range decodeToolList(rawFields) {
		if strings.TrimSpace(asString(tool["type"])) != "mcp" {
			continue
		}
		if strings.TrimSpace(asString(tool["connector_id"])) != "" {
			return true
		}
	}
	return false
}

func hasUnsupportedLocalMCPTools(rawFields map[string]json.RawMessage) bool {
	return hasDeclaredMCPTools(rawFields) && !hasConnectorMCPTools(rawFields) && !supportsLocalMCP(rawFields)
}

func hasLocalMCPApprovalResponse(rawFields map[string]json.RawMessage) bool {
	rawInput, ok := rawFields["input"]
	if !ok {
		return false
	}
	items, err := domain.NormalizeInput(rawInput)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.Type == "mcp_approval_response" {
			return true
		}
	}
	return false
}

func (h *responseHandler) hasLocalMCPState(ctx context.Context, request CreateResponseRequest) bool {
	if strings.TrimSpace(request.PreviousResponseID) == "" {
		return false
	}
	response, err := h.service.Get(ctx, request.PreviousResponseID)
	if err == nil {
		for _, item := range response.Output {
			switch item.Type {
			case "mcp_list_tools", "mcp_call", "mcp_tool_call", "mcp_approval_request":
				return true
			}
		}
	}
	items, err := h.service.GetInputItems(ctx, request.PreviousResponseID)
	if err != nil {
		return false
	}
	for _, item := range items {
		switch item.Type {
		case "mcp_list_tools", "mcp_call", "mcp_tool_call", "mcp_approval_request":
			return true
		}
	}
	return false
}

func (h *responseHandler) createLocalMCPResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	input := service.CreateResponseInput{
		Model:              request.Model,
		Input:              request.Input,
		TextConfig:         request.Text,
		Metadata:           request.Metadata,
		Store:              request.Store,
		Stream:             request.Stream,
		Background:         request.Background,
		PreviousResponseID: request.PreviousResponseID,
		ConversationID:     request.Conversation,
		Instructions:       request.Instructions,
		RequestJSON:        requestJSON,
		GenerationOptions:  buildGenerationOptions(rawFields),
	}

	prepared, err := h.service.PrepareCreateContext(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}
	if _, err := h.service.PrepareLocalResponseText(input, prepared.ContextItems); err != nil {
		return domain.Response{}, err
	}

	currentConfigs, configErr := parseLocalMCPToolConfigs(rawFields)
	if configErr != nil && !hasLocalMCPApprovalResponse(rawFields) && !hasCachedLocalMCPServers(prepared.EffectiveInput) {
		if strings.TrimSpace(request.PreviousResponseID) != "" && !hasDeclaredMCPTools(rawFields) {
			return domain.Response{}, domain.ErrUnsupportedShape
		}
		return domain.Response{}, configErr
	}

	cachedServers, err := collectCachedLocalMCPServers(prepared.EffectiveInput)
	if err != nil {
		return domain.Response{}, err
	}
	servers, importedItems, err := h.resolveLocalMCPServers(ctx, currentConfigs, cachedServers)
	if err != nil {
		return domain.Response{}, err
	}
	if len(servers) == 0 {
		return domain.Response{}, domain.NewValidationError("tools", "shim-local remote MCP requires at least one server_url tool or cached mcp_list_tools state")
	}

	toolChoice, err := parseLocalMCPToolChoice(rawFields["tool_choice"])
	if err != nil {
		return domain.Response{}, err
	}
	if err := validateLocalMCPParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return domain.Response{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}

	registry := buildLocalMCPToolRegistry(servers)
	if len(registry) == 0 {
		return domain.Response{}, domain.NewValidationError("tools", "shim-local remote MCP could not import any callable tools")
	}

	publicOutput := make([]domain.Item, 0, len(importedItems)+3)
	publicOutput = append(publicOutput, importedItems...)
	contextItems := append([]domain.Item{}, prepared.ContextItems...)
	contextItems = append(contextItems, importedItems...)

	pendingApprovals := collectPendingLocalMCPApprovals(prepared.EffectiveInput)
	approvalResponses, err := decodeLocalMCPApprovalResponses(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	for _, approval := range approvalResponses {
		pending, ok := pendingApprovals[approval.ApprovalRequestID]
		if !ok {
			return domain.Response{}, domain.NewValidationError("input", "mcp_approval_response references an unknown approval_request_id")
		}
		binding, ok := resolveLocalMCPBinding(registry, pending.ServerLabel, pending.Name)
		if !ok {
			return domain.Response{}, domain.NewValidationError("input", "mcp_approval_response references a tool that is no longer available")
		}
		if !approval.Approve {
			return h.finalizeLocalMCPResponse(ctx, input, prepared, responseID, contextItems, append(publicOutput, buildLocalMCPApprovalDeniedMessageItem()...))
		}
		callItem, err := h.executeLocalMCPTool(ctx, binding, pending.Arguments, approval.ApprovalRequestID)
		if err != nil {
			return domain.Response{}, err
		}
		publicOutput = append(publicOutput, callItem)
		contextItems = append(contextItems, callItem)
	}

	for attempt := 0; attempt < localMCPMaxIterations; attempt++ {
		chatBody, err := buildLocalMCPChatCompletionBody(rawFields, input.Model, contextItems, registry, toolChoice)
		if err != nil {
			return domain.Response{}, err
		}

		rawResponse, err := h.proxy.client.CreateChatCompletion(ctx, chatBody)
		if err != nil {
			return domain.Response{}, err
		}
		planned, err := parseLocalToolLoopChatCompletion(rawResponse, responseID, input.Model, input.PreviousResponseID, input.ConversationID, customToolTransportPlan{})
		if err != nil {
			return domain.Response{}, err
		}

		var functionCalls []domain.Item
		for _, item := range planned.Output {
			switch item.Type {
			case "reasoning":
				publicOutput = append(publicOutput, item)
			case "function_call":
				functionCalls = append(functionCalls, item)
			case "message":
				publicOutput = append(publicOutput, item)
				return h.finalizeLocalMCPResponse(ctx, input, prepared, responseID, contextItems, publicOutput)
			}
		}
		if len(functionCalls) == 0 {
			if strings.TrimSpace(planned.OutputText) != "" {
				publicOutput = append(publicOutput, domain.NewOutputTextMessage(planned.OutputText))
				return h.finalizeLocalMCPResponse(ctx, input, prepared, responseID, contextItems, publicOutput)
			}
			return domain.Response{}, fmt.Errorf("shim-local remote MCP model turn did not yield assistant text or tool calls")
		}

		for _, call := range functionCalls {
			binding, ok := registry[strings.TrimSpace(call.Name())]
			if !ok {
				return domain.Response{}, domain.NewValidationError("tools", "shim-local remote MCP selected an unknown tool")
			}
			arguments := normalizeJSONStringField(call.RawField("arguments"))
			if binding.Server.Config.ApprovalPolicy.RequiresApproval(binding.Tool.Name) {
				approvalItem, err := buildLocalMCPApprovalRequestItem(binding, arguments)
				if err != nil {
					return domain.Response{}, err
				}
				publicOutput = append(publicOutput, approvalItem)
				return h.finalizeLocalMCPResponse(ctx, input, prepared, responseID, contextItems, publicOutput)
			}

			callItem, err := h.executeLocalMCPTool(ctx, binding, arguments, "")
			if err != nil {
				return domain.Response{}, err
			}
			publicOutput = append(publicOutput, callItem)
			contextItems = append(contextItems, callItem)
		}
	}

	return domain.Response{}, fmt.Errorf("shim-local remote MCP exceeded %d tool iterations", localMCPMaxIterations)
}

func (h *responseHandler) finalizeLocalMCPResponse(ctx context.Context, input service.CreateResponseInput, prepared service.PreparedResponseContext, responseID string, contextItems []domain.Item, output []domain.Item) (domain.Response, error) {
	response := domain.Response{
		ID:                 responseID,
		Object:             "response",
		Model:              input.Model,
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       domain.NewConversationReference(input.ConversationID),
		Output:             output,
		OutputText:         localMCPResponseOutputText(output),
	}
	response, err := h.service.FinalizeLocalResponse(input, contextItems, response)
	if err != nil {
		return domain.Response{}, err
	}
	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func (h *responseHandler) resolveLocalMCPServers(ctx context.Context, current []localMCPServerConfig, cached map[string]localMCPRuntimeServer) ([]localMCPRuntimeServer, []domain.Item, error) {
	if len(current) == 0 {
		servers := make([]localMCPRuntimeServer, 0, len(cached))
		for _, server := range cached {
			servers = append(servers, server)
		}
		sort.Slice(servers, func(i, j int) bool {
			return servers[i].Config.ServerLabel < servers[j].Config.ServerLabel
		})
		return servers, nil, nil
	}

	client := newLocalMCPClient()
	servers := make([]localMCPRuntimeServer, 0, len(current))
	importedItems := make([]domain.Item, 0, len(current))
	for _, config := range current {
		if cachedServer, ok := cached[config.ServerLabel]; ok && localMCPServerConfigEqual(config, cachedServer.Config) {
			servers = append(servers, cachedServer)
			continue
		}

		tools, transport, err := client.ListTools(ctx, config)
		if err != nil {
			return nil, nil, fmt.Errorf("import MCP tools from %s: %w", config.ServerLabel, err)
		}
		config.Transport = transport
		server := localMCPRuntimeServer{
			Config: config,
			Tools:  filterAndNormalizeLocalMCPTools(config, tools),
			Cached: false,
		}
		if len(server.Tools) == 0 {
			return nil, nil, fmt.Errorf("import MCP tools from %s: no tools matched the allowed_tools subset", config.ServerLabel)
		}
		item, err := buildLocalMCPListToolsItem(server)
		if err != nil {
			return nil, nil, err
		}
		servers = append(servers, server)
		importedItems = append(importedItems, item)
	}
	return servers, importedItems, nil
}

func parseLocalMCPToolConfigs(rawFields map[string]json.RawMessage) ([]localMCPServerConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) == 0 {
		return nil, domain.NewValidationError("tools", "shim-local remote MCP requires at least one mcp tool")
	}

	configs := make([]localMCPServerConfig, 0, len(tools))
	seenLabels := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		for key := range tool {
			switch key {
			case "type", "server_label", "server_description", "server_url", "connector_id", "authorization", "headers", "allowed_tools", "require_approval":
			default:
				return nil, domain.NewValidationError("tools", "unsupported mcp tool field "+`"`+key+`"`+" in shim-local mode")
			}
		}
		if strings.TrimSpace(asString(tool["type"])) != "mcp" {
			return nil, domain.NewValidationError("tools", "shim-local remote MCP requires only mcp tools in the tools array")
		}
		serverLabel := strings.TrimSpace(asString(tool["server_label"]))
		if serverLabel == "" {
			return nil, domain.NewValidationError("tools", "mcp.server_label is required in shim-local mode")
		}
		if _, ok := seenLabels[serverLabel]; ok {
			return nil, domain.NewValidationError("tools", "duplicate mcp.server_label values are not supported in shim-local mode")
		}
		seenLabels[serverLabel] = struct{}{}

		serverURL := strings.TrimSpace(asString(tool["server_url"]))
		connectorID := strings.TrimSpace(asString(tool["connector_id"]))
		switch {
		case serverURL != "" && connectorID != "":
			return nil, domain.NewValidationError("tools", "mcp tools in shim-local mode must set exactly one of server_url or connector_id")
		case serverURL == "" && connectorID == "":
			return nil, domain.NewValidationError("tools", "mcp.server_url is required in shim-local mode")
		case connectorID != "":
			return nil, domain.NewValidationError("tools", "shim-local remote MCP supports server_url tools; connectors remain upstream-only")
		}

		authorization := strings.TrimSpace(asString(tool["authorization"]))
		if authorization != "" {
			return nil, domain.NewValidationError("tools", "shim-local remote MCP does not support mcp.authorization")
		}
		if rawHeaders, ok := tool["headers"]; ok && rawHeaders != nil {
			return nil, domain.NewValidationError("tools", "shim-local remote MCP does not support mcp.headers")
		}
		allowedTools, err := parseLocalMCPAllowedTools(tool["allowed_tools"])
		if err != nil {
			return nil, err
		}
		approvalPolicy, approvalRaw, err := parseLocalMCPApprovalPolicy(tool["require_approval"])
		if err != nil {
			return nil, err
		}

		configs = append(configs, localMCPServerConfig{
			ServerLabel:       serverLabel,
			ServerURL:         serverURL,
			ConnectorID:       connectorID,
			Authorization:     "",
			Headers:           nil,
			ServerDescription: strings.TrimSpace(asString(tool["server_description"])),
			AllowedTools:      allowedTools,
			ApprovalPolicy:    approvalPolicy,
			ApprovalRaw:       approvalRaw,
		})
	}
	return configs, nil
}

func parseLocalMCPAllowedTools(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, domain.NewValidationError("tools", "mcp.allowed_tools must be an array of strings")
	}
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		name := strings.TrimSpace(asString(entry))
		if name == "" {
			return nil, domain.NewValidationError("tools", "mcp.allowed_tools must not contain empty values")
		}
		out = append(out, name)
	}
	return out, nil
}

func parseLocalMCPHeaders(value any) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, domain.NewValidationError("tools", "mcp.headers must be an object of string values")
	}
	headers := make(map[string]string, len(raw))
	for key, entry := range raw {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, domain.NewValidationError("tools", "mcp.headers must not contain empty names")
		}
		value, ok := entry.(string)
		if !ok {
			return nil, domain.NewValidationError("tools", "mcp.headers must be an object of string values")
		}
		headers[name] = value
	}
	return headers, nil
}

func parseLocalMCPApprovalPolicy(value any) (localMCPApprovalPolicy, string, error) {
	if value == nil {
		return localMCPApprovalPolicy{NeverTools: map[string]struct{}{}}, "", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return localMCPApprovalPolicy{}, "", domain.NewValidationError("tools", "mcp.require_approval must be valid JSON")
	}

	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		switch strings.TrimSpace(literal) {
		case "", "always":
			return localMCPApprovalPolicy{NeverTools: map[string]struct{}{}}, string(raw), nil
		case "never":
			return localMCPApprovalPolicy{NeverAll: true, NeverTools: map[string]struct{}{}}, string(raw), nil
		default:
			return localMCPApprovalPolicy{}, "", domain.NewValidationError("tools", "unsupported mcp.require_approval value in shim-local mode")
		}
	}

	var payload struct {
		Never *struct {
			ToolNames []string `json:"tool_names"`
		} `json:"never"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Never == nil {
		return localMCPApprovalPolicy{}, "", domain.NewValidationError("tools", "unsupported mcp.require_approval object in shim-local mode")
	}

	policy := localMCPApprovalPolicy{NeverTools: map[string]struct{}{}}
	for _, name := range payload.Never.ToolNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return localMCPApprovalPolicy{}, "", domain.NewValidationError("tools", "mcp.require_approval.never.tool_names must not contain empty values")
		}
		policy.NeverTools[trimmed] = struct{}{}
	}
	return policy, string(raw), nil
}

func (p localMCPApprovalPolicy) RequiresApproval(toolName string) bool {
	if p.NeverAll {
		return false
	}
	_, ok := p.NeverTools[strings.TrimSpace(toolName)]
	return !ok
}

func validateLocalMCPParallelToolCalls(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var value bool
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return domain.NewValidationError("parallel_tool_calls", "parallel_tool_calls must be a boolean")
	}
	return nil
}

func parseLocalMCPToolChoice(raw json.RawMessage) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var literal string
	if err := json.Unmarshal(trimmed, &literal); err == nil {
		switch strings.TrimSpace(literal) {
		case "auto", "required":
			return literal, nil
		default:
			return nil, domain.NewValidationError("tool_choice", "shim-local remote MCP supports tool_choice=auto or required")
		}
	}
	return nil, domain.NewValidationError("tool_choice", "shim-local remote MCP supports only tool_choice=auto or required")
}

func hasCachedLocalMCPServers(items []domain.Item) bool {
	for _, item := range items {
		if item.Type == "mcp_list_tools" && item.Meta != nil && strings.TrimSpace(item.Meta.MCPServerURL) != "" {
			return true
		}
	}
	return false
}

func collectCachedLocalMCPServers(items []domain.Item) (map[string]localMCPRuntimeServer, error) {
	out := make(map[string]localMCPRuntimeServer)
	for _, item := range items {
		if item.Type != "mcp_list_tools" || item.Meta == nil || strings.TrimSpace(item.Meta.MCPServerURL) == "" {
			continue
		}
		config := localMCPServerConfig{
			ServerLabel:    strings.TrimSpace(item.StringField("server_label")),
			ServerURL:      strings.TrimSpace(item.Meta.MCPServerURL),
			ConnectorID:    strings.TrimSpace(item.Meta.MCPConnectorID),
			Authorization:  strings.TrimSpace(item.Meta.MCPAuthorization),
			ApprovalPolicy: localMCPApprovalPolicy{NeverTools: map[string]struct{}{}},
			ApprovalRaw:    strings.TrimSpace(item.Meta.MCPApproval),
			AllowedTools:   append([]string(nil), item.Meta.MCPToolNames...),
			Transport:      strings.TrimSpace(item.Meta.MCPTransport),
		}
		if len(item.Meta.MCPHeaders) > 0 {
			config.Headers = make(map[string]string, len(item.Meta.MCPHeaders))
			for key, value := range item.Meta.MCPHeaders {
				config.Headers[key] = value
			}
		}
		if config.ServerLabel == "" || config.ServerURL == "" {
			continue
		}
		if strings.TrimSpace(config.ApprovalRaw) != "" {
			policy, _, err := parseLocalMCPApprovalPolicy(json.RawMessage(config.ApprovalRaw))
			if err == nil {
				config.ApprovalPolicy = policy
			}
		}

		tools, err := decodeLocalMCPListToolsItem(item)
		if err != nil {
			return nil, err
		}
		out[config.ServerLabel] = localMCPRuntimeServer{
			Config: config,
			Tools:  tools,
			Cached: true,
		}
	}
	return out, nil
}

func decodeLocalMCPListToolsItem(item domain.Item) ([]localMCPToolDefinition, error) {
	var payload struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Annotations any    `json:"annotations"`
			InputSchema any    `json:"input_schema"`
		} `json:"tools"`
		ServerLabel string `json:"server_label"`
	}
	if err := json.Unmarshal(item.Raw, &payload); err != nil {
		return nil, fmt.Errorf("decode cached mcp_list_tools item: %w", err)
	}
	tools := make([]localMCPToolDefinition, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		tools = append(tools, localMCPToolDefinition{
			Name:          name,
			Description:   strings.TrimSpace(tool.Description),
			Annotations:   tool.Annotations,
			InputSchema:   tool.InputSchema,
			SyntheticName: syntheticLocalMCPToolName(payload.ServerLabel, name),
		})
	}
	return tools, nil
}

func localMCPServerConfigEqual(left, right localMCPServerConfig) bool {
	if left.ServerLabel != right.ServerLabel ||
		left.ServerURL != right.ServerURL ||
		left.ConnectorID != right.ConnectorID ||
		left.Authorization != right.Authorization ||
		left.Transport != right.Transport ||
		left.ApprovalRaw != right.ApprovalRaw {
		return false
	}
	if len(left.Headers) != len(right.Headers) {
		return false
	}
	for key, value := range left.Headers {
		if right.Headers[key] != value {
			return false
		}
	}
	if len(left.AllowedTools) != len(right.AllowedTools) {
		return false
	}
	for i := range left.AllowedTools {
		if left.AllowedTools[i] != right.AllowedTools[i] {
			return false
		}
	}
	return true
}

func filterAndNormalizeLocalMCPTools(config localMCPServerConfig, tools []localMCPTool) []localMCPToolDefinition {
	allowed := make(map[string]struct{}, len(config.AllowedTools))
	for _, name := range config.AllowedTools {
		allowed[strings.TrimSpace(name)] = struct{}{}
	}
	filtered := make([]localMCPToolDefinition, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		filtered = append(filtered, localMCPToolDefinition{
			Name:          name,
			Description:   tool.Description,
			Annotations:   tool.Annotations,
			InputSchema:   tool.InputSchema,
			SyntheticName: syntheticLocalMCPToolName(config.ServerLabel, name),
		})
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Name < filtered[j].Name
	})
	return filtered
}

func buildLocalMCPToolRegistry(servers []localMCPRuntimeServer) map[string]localMCPToolBinding {
	registry := make(map[string]localMCPToolBinding)
	for _, server := range servers {
		for _, tool := range server.Tools {
			registry[tool.SyntheticName] = localMCPToolBinding{Server: server, Tool: tool}
		}
	}
	return registry
}

func resolveLocalMCPBinding(registry map[string]localMCPToolBinding, serverLabel string, toolName string) (localMCPToolBinding, bool) {
	for _, binding := range registry {
		if binding.Server.Config.ServerLabel == serverLabel && binding.Tool.Name == toolName {
			return binding, true
		}
	}
	return localMCPToolBinding{}, false
}

func syntheticLocalMCPToolName(serverLabel string, toolName string) string {
	serverLabel = strings.Map(localMCPIdentifierRune, strings.ToLower(strings.TrimSpace(serverLabel)))
	toolName = strings.Map(localMCPIdentifierRune, strings.TrimSpace(toolName))
	serverLabel = strings.Trim(serverLabel, "_")
	toolName = strings.Trim(toolName, "_")
	if serverLabel == "" {
		return toolName
	}
	return "mcp__" + serverLabel + "__" + toolName
}

func localMCPIdentifierRune(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z':
		return r
	case r >= 'A' && r <= 'Z':
		return r + ('a' - 'A')
	case r >= '0' && r <= '9':
		return r
	default:
		return '_'
	}
}

func buildLocalMCPChatCompletionBody(rawFields map[string]json.RawMessage, model string, items []domain.Item, registry map[string]localMCPToolBinding, toolChoice any) ([]byte, error) {
	messages, err := buildChatCompletionMessagesFromItems(items)
	if err != nil {
		return nil, err
	}

	tools := make([]map[string]any, 0, len(registry))
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		binding := registry[name]
		schema := binding.Tool.InputSchema
		if schema == nil {
			schema = map[string]any{
				"type":                 "object",
				"additionalProperties": true,
			}
		}
		description := strings.TrimSpace(binding.Tool.Description)
		if binding.Server.Config.ServerDescription != "" {
			description = strings.TrimSpace(strings.Join([]string{
				binding.Server.Config.ServerDescription,
				description,
			}, "\n\n"))
		}
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        binding.Tool.SyntheticName,
				"description": description,
				"parameters":  schema,
			},
		})
	}

	body := map[string]any{
		"model":    model,
		"messages": messages,
		"tools":    tools,
	}
	if toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	if rawParallel, ok := rawFields["parallel_tool_calls"]; ok {
		body["parallel_tool_calls"] = json.RawMessage(rawParallel)
	}
	for key, raw := range rawFields {
		if _, ok := shimLocalGenerationFields[key]; !ok {
			continue
		}
		targetKey := key
		if key == "max_output_tokens" {
			targetKey = "max_tokens"
		}
		body[targetKey] = json.RawMessage(raw)
	}
	return json.Marshal(body)
}

func buildLocalMCPListToolsItem(server localMCPRuntimeServer) (domain.Item, error) {
	id, err := domain.NewPrefixedID("mcpl")
	if err != nil {
		return domain.Item{}, err
	}
	tools := make([]map[string]any, 0, len(server.Tools))
	toolNames := make([]string, 0, len(server.Tools))
	for _, tool := range server.Tools {
		toolNames = append(toolNames, tool.Name)
		tools = append(tools, map[string]any{
			"annotations":  tool.Annotations,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
			"name":         tool.Name,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"id":           id,
		"type":         "mcp_list_tools",
		"server_label": server.Config.ServerLabel,
		"tools":        tools,
	})
	if err != nil {
		return domain.Item{}, err
	}
	item, err := domain.NewItem(raw)
	if err != nil {
		return domain.Item{}, err
	}
	return item.WithMeta(domain.ItemMeta{
		MCPServerURL:     server.Config.ServerURL,
		MCPConnectorID:   server.Config.ConnectorID,
		MCPAuthorization: server.Config.Authorization,
		MCPApproval:      server.Config.ApprovalRaw,
		MCPTransport:     server.Config.Transport,
		MCPToolNames:     toolNames,
		MCPHeaders:       server.Config.Headers,
	}), nil
}

func buildLocalMCPApprovalRequestItem(binding localMCPToolBinding, arguments string) (domain.Item, error) {
	id, err := domain.NewPrefixedID("mcpr")
	if err != nil {
		return domain.Item{}, err
	}
	raw, err := json.Marshal(map[string]any{
		"id":           id,
		"type":         "mcp_approval_request",
		"arguments":    arguments,
		"name":         binding.Tool.Name,
		"server_label": binding.Server.Config.ServerLabel,
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func buildLocalMCPApprovalDeniedMessageItem() []domain.Item {
	return []domain.Item{domain.NewOutputTextMessage("MCP tool call was not approved.")}
}

func collectPendingLocalMCPApprovals(items []domain.Item) map[string]localMCPPendingApproval {
	out := make(map[string]localMCPPendingApproval)
	for _, item := range items {
		if item.Type != "mcp_approval_request" {
			continue
		}
		id := strings.TrimSpace(item.ID())
		if id == "" {
			continue
		}
		out[id] = localMCPPendingApproval{
			ID:          id,
			ServerLabel: strings.TrimSpace(item.StringField("server_label")),
			Name:        strings.TrimSpace(item.Name()),
			Arguments:   normalizeJSONStringField(item.RawField("arguments")),
		}
	}
	return out
}

func decodeLocalMCPApprovalResponses(items []domain.Item) ([]localMCPApprovalResponse, error) {
	responses := make([]localMCPApprovalResponse, 0, len(items))
	for _, item := range items {
		if item.Type != "mcp_approval_response" {
			continue
		}
		approvalRequestID := strings.TrimSpace(item.StringField("approval_request_id"))
		if approvalRequestID == "" {
			return nil, domain.NewValidationError("input", "mcp_approval_response.approval_request_id is required")
		}
		rawApprove := bytes.TrimSpace(item.RawField("approve"))
		var approve bool
		if err := json.Unmarshal(rawApprove, &approve); err != nil {
			return nil, domain.NewValidationError("input", "mcp_approval_response.approve must be a boolean")
		}
		responses = append(responses, localMCPApprovalResponse{
			ApprovalRequestID: approvalRequestID,
			Approve:           approve,
		})
	}
	return responses, nil
}

func (h *responseHandler) executeLocalMCPTool(ctx context.Context, binding localMCPToolBinding, arguments string, approvalRequestID string) (domain.Item, error) {
	callID, err := domain.NewPrefixedID("mcp")
	if err != nil {
		return domain.Item{}, err
	}
	result, callErr := newLocalMCPClient().CallTool(ctx, binding.Server.Config, binding.Tool.Name, json.RawMessage(arguments))
	if callErr != nil {
		return buildLocalMCPCallItem(callID, binding, arguments, approvalRequestID, "", callErr, true)
	}
	output := stringifyLocalMCPCallContent(result.Content)
	if result.IsError {
		return buildLocalMCPCallItem(callID, binding, arguments, approvalRequestID, output, fmt.Errorf("%s", strings.TrimSpace(output)), true)
	}
	return buildLocalMCPCallItem(callID, binding, arguments, approvalRequestID, output, nil, false)
}

func buildLocalMCPCallItem(id string, binding localMCPToolBinding, arguments string, approvalRequestID string, output string, callErr error, failed bool) (domain.Item, error) {
	payload := map[string]any{
		"id":                  id,
		"type":                "mcp_call",
		"approval_request_id": nil,
		"arguments":           arguments,
		"name":                binding.Tool.Name,
		"output":              nil,
		"server_label":        binding.Server.Config.ServerLabel,
		"status":              "completed",
		"error":               nil,
	}
	if strings.TrimSpace(approvalRequestID) != "" {
		payload["approval_request_id"] = approvalRequestID
	}
	if strings.TrimSpace(output) != "" {
		payload["output"] = output
	}
	if failed {
		payload["status"] = "failed"
		message := "remote MCP unavailable"
		if callErr != nil && strings.TrimSpace(callErr.Error()) != "" {
			message = strings.TrimSpace(callErr.Error())
		}
		payload["error"] = map[string]any{
			"type":    "tool_execution_error",
			"message": message,
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Item{}, err
	}
	item, err := domain.NewItem(raw)
	if err != nil {
		return domain.Item{}, err
	}
	return item.WithMeta(domain.ItemMeta{
		SyntheticName:    binding.Tool.SyntheticName,
		MCPServerURL:     binding.Server.Config.ServerURL,
		MCPConnectorID:   binding.Server.Config.ConnectorID,
		MCPAuthorization: binding.Server.Config.Authorization,
		MCPApproval:      binding.Server.Config.ApprovalRaw,
		MCPTransport:     binding.Server.Config.Transport,
		MCPHeaders:       binding.Server.Config.Headers,
	}), nil
}

func stringifyLocalMCPCallContent(content []map[string]any) string {
	if len(content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, part := range content {
		partType := strings.TrimSpace(asString(part["type"]))
		switch partType {
		case "text":
			text := strings.TrimSpace(asString(part["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		default:
			raw, err := json.Marshal(part)
			if err == nil {
				parts = append(parts, string(raw))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func localMCPResponseOutputText(output []domain.Item) string {
	for i := len(output) - 1; i >= 0; i-- {
		item := output[i]
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		if text := strings.TrimSpace(domain.MessageText(item)); text != "" {
			return text
		}
	}
	return ""
}
