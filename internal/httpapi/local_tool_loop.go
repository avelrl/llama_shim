package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
)

var shimLocalToolLoopFields = map[string]struct{}{
	"model":                {},
	"input":                {},
	"text":                 {},
	"store":                {},
	"stream":               {},
	"previous_response_id": {},
	"conversation":         {},
	"instructions":         {},
	"tools":                {},
	"tool_choice":          {},
	"parallel_tool_calls":  {},
}

var shimLocalToolLoopNoopFields = map[string]struct{}{
	// Codex CLI request metadata. These fields do not affect the shim-local
	// Chat Completions tool loop and are intentionally not forwarded upstream.
	"client_metadata":  {},
	"prompt_cache_key": {},
}

type localChatCompletionResponse struct {
	Choices []localChatCompletionChoice `json:"choices"`
}

type localChatCompletionChoice struct {
	Message localChatCompletionMessage `json:"message"`
}

type localChatCompletionMessage struct {
	Content   json.RawMessage         `json:"content"`
	ToolCalls []localChatToolCallItem `json:"tool_calls"`
}

type localChatToolCallItem struct {
	ID       string                `json:"id"`
	Type     string                `json:"type"`
	Function localChatToolFunction `json:"function"`
}

type localChatToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func supportsLocalToolLoop(rawFields map[string]json.RawMessage) bool {
	for key, raw := range rawFields {
		if _, ok := shimLocalToolLoopFields[key]; ok {
			continue
		}
		if _, ok := shimLocalToolLoopNoopFields[key]; ok {
			continue
		}
		if key == "include" && isEmptyJSONArray(raw) {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		return false
	}

	tools := decodeToolList(rawFields)
	if len(tools) > 0 {
		return supportsLocalToolDefinitions(tools)
	}
	rawInput, ok := rawFields["input"]
	if !ok {
		return false
	}
	return supportsLocalToolReplayInput(rawInput)
}

func isEmptyJSONArray(raw json.RawMessage) bool {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return false
	}
	return len(values) == 0
}

func supportsLocalToolDefinitions(tools []map[string]any) bool {
	supported := false
	for _, tool := range tools {
		if isDisabledWebSearchTool(tool) {
			continue
		}
		toolType := strings.TrimSpace(asString(tool["type"]))
		switch {
		case toolType == "function":
			supported = true
		case isLocalBuiltinToolType(toolType):
			supported = true
		case isCustomToolType(toolType):
			supported = true
		default:
			return false
		}
	}
	return supported
}

func supportsLocalToolReplayInput(raw json.RawMessage) bool {
	items, err := domain.NormalizeInput(raw)
	if err != nil {
		return false
	}
	for _, item := range items {
		switch item.Type {
		case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output", localBuiltinShellCallType, localBuiltinApplyPatchCallType, localBuiltinShellCallOutputType, localBuiltinApplyPatchCallOutputType:
			return true
		}
	}
	return false
}

func hasCustomTools(tools []map[string]any) bool {
	for _, tool := range tools {
		if isCustomToolType(strings.TrimSpace(asString(tool["type"]))) {
			return true
		}
	}
	return false
}

func (h *responseHandler) createLocalToolLoopResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
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
	response, err := h.runPreparedLocalToolLoopResponse(ctx, input, prepared, rawFields)
	if err != nil {
		return domain.Response{}, err
	}
	response, err = h.service.FinalizeLocalResponse(input, prepared.ContextItems, response)
	if err != nil {
		return domain.Response{}, err
	}
	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func buildLocalToolLoopChatCompletionBody(rawFields map[string]json.RawMessage, contextItems []domain.Item, currentInput []domain.Item, refs map[string]domain.ToolCallReference, serviceLimits ServiceLimits, customToolsMode string, codexCompatibilityEnabled bool, forceCodexToolChoiceRequired bool, repairPrompt string) ([]byte, customToolTransportPlan, error) {
	_ = customToolsMode
	return buildLocalChatCompletionRequest(rawFields, contextItems, currentInput, refs, serviceLimits, codexCompatibilityEnabled, forceCodexToolChoiceRequired, repairPrompt)
}

func rewriteResponsesBodyToChatCompletionsBody(body []byte) ([]byte, error) {
	fields, err := decodeRawFields(body)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(rawStringField(fields, "model"))
	if model == "" {
		return nil, domain.NewValidationError("model", "model is required")
	}

	rawInput, ok := fields["input"]
	if !ok {
		return nil, domain.NewValidationError("input", "input is required")
	}
	items, err := decodeResponseInputItems(rawInput)
	if err != nil {
		return nil, err
	}
	messages, err := buildChatCompletionMessagesFromItems(items)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"model":    model,
		"messages": messages,
	}

	if rawTools, ok := fields["tools"]; ok {
		tools, err := decodeChatToolDefinitions(rawTools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
	}

	if rawChoice, ok := fields["tool_choice"]; ok {
		toolChoice, err := decodeChatToolChoice(rawChoice)
		if err != nil {
			return nil, err
		}
		if toolChoice != nil {
			out["tool_choice"] = toolChoice
		}
	}

	if rawParallel, ok := fields["parallel_tool_calls"]; ok {
		out["parallel_tool_calls"] = json.RawMessage(rawParallel)
	}

	for key, raw := range fields {
		if _, ok := shimLocalGenerationFields[key]; !ok {
			continue
		}
		targetKey := key
		if key == "max_output_tokens" {
			targetKey = "max_tokens"
		}
		out[targetKey] = json.RawMessage(raw)
	}

	return json.Marshal(out)
}

func decodeResponseInputItems(raw json.RawMessage) ([]domain.Item, error) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(raw, &rawItems); err != nil {
		return nil, domain.ErrUnsupportedShape
	}

	items := make([]domain.Item, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, err := domain.NewItem(rawItem)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func buildChatCompletionMessagesFromItems(items []domain.Item) ([]map[string]any, error) {
	messages := make([]map[string]any, 0, len(items))
	lastTextMessage := -1

	for _, item := range items {
		switch item.Type {
		case "message":
			if item.HasNonTextMessageContent() {
				return nil, domain.ErrUnsupportedShape
			}

			role := strings.TrimSpace(item.Role)
			if role == "developer" {
				role = "system"
			}
			switch role {
			case "system", "user", "assistant":
			default:
				return nil, domain.ErrUnsupportedShape
			}

			content := domain.MessageText(item)
			if lastTextMessage >= 0 && strings.EqualFold(strings.TrimSpace(asString(messages[lastTextMessage]["role"])), role) {
				previous := strings.TrimSpace(asString(messages[lastTextMessage]["content"]))
				switch {
				case previous == "":
					messages[lastTextMessage]["content"] = content
				case content != "":
					messages[lastTextMessage]["content"] = previous + "\n\n" + content
				}
				continue
			}

			messages = append(messages, map[string]any{
				"role":    role,
				"content": content,
			})
			lastTextMessage = len(messages) - 1
		case "function_call", "custom_tool_call", localBuiltinShellCallType, localBuiltinApplyPatchCallType:
			name := strings.TrimSpace(item.Name())
			arguments := item.RawField("arguments")
			switch item.Type {
			case "custom_tool_call":
				arguments = json.RawMessage(encodeCustomToolArguments(item.RawField("input")))
			case localBuiltinShellCallType, localBuiltinApplyPatchCallType:
				name = localBuiltinToolCallName(item)
				if name == "" {
					return nil, domain.ErrUnsupportedShape
				}
				encodedArguments, err := localBuiltinToolArgumentsJSON(item)
				if err != nil {
					return nil, err
				}
				arguments = encodedArguments
			}
			if name == "" {
				return nil, domain.ErrUnsupportedShape
			}
			callID, err := ensureLocalToolCallID(localChatToolCallID(item))
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					{
						"id":   callID,
						"type": "function",
						"function": map[string]any{
							"name":      name,
							"arguments": normalizeJSONStringField(arguments),
						},
					},
				},
			})
			lastTextMessage = -1
		case "function_call_output", "custom_tool_call_output", localBuiltinShellCallOutputType, localBuiltinApplyPatchCallOutputType:
			callID := strings.TrimSpace(item.CallID())
			if callID == "" {
				return nil, domain.ErrUnsupportedShape
			}
			var output string
			var err error
			switch item.Type {
			case localBuiltinShellCallOutputType, localBuiltinApplyPatchCallOutputType:
				output, err = stringifyLocalBuiltinToolOutput(item)
			default:
				output, err = stringifyToolOutput(item.OutputRaw())
			}
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      output,
			})
			lastTextMessage = -1
		case "mcp_call", "mcp_tool_call":
			name := strings.TrimSpace(item.Name())
			if item.Meta != nil && strings.TrimSpace(item.Meta.SyntheticName) != "" {
				name = strings.TrimSpace(item.Meta.SyntheticName)
			}
			if name == "" {
				return nil, domain.ErrUnsupportedShape
			}
			callID, err := ensureLocalToolCallID(localChatToolCallID(item))
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					{
						"id":   callID,
						"type": "function",
						"function": map[string]any{
							"name":      name,
							"arguments": normalizeJSONStringField(item.RawField("arguments")),
						},
					},
				},
			})
			output, err := stringifyMCPToolOutput(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      output,
			})
			lastTextMessage = -1
		case "tool_search_call", "tool_search_output", "mcp_list_tools", "mcp_approval_request", "mcp_approval_response":
			continue
		default:
			return nil, domain.ErrUnsupportedShape
		}
	}

	messages = synthesizeMissingChatToolOutputs(messages)
	return messages, nil
}

const missingChatToolOutputContent = "tool output was not supplied by the client; treat this tool call as failed and continue with available context."

func synthesizeMissingChatToolOutputs(messages []map[string]any) []map[string]any {
	if len(messages) == 0 {
		return messages
	}

	out := make([]map[string]any, 0, len(messages))
	pending := make([]string, 0)

	flushPending := func() {
		for _, callID := range pending {
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      missingChatToolOutputContent,
			})
		}
		pending = pending[:0]
	}

	for _, message := range messages {
		role := strings.TrimSpace(asString(message["role"]))
		if role == "tool" {
			callID := strings.TrimSpace(asString(message["tool_call_id"]))
			if len(pending) > 0 && callID != "" {
				for idx, pendingID := range pending {
					if pendingID == callID {
						pending = append(pending[:idx], pending[idx+1:]...)
						break
					}
				}
			}
			out = append(out, message)
			continue
		}

		if len(pending) > 0 {
			flushPending()
		}
		out = append(out, message)

		if role != "assistant" {
			continue
		}
		toolCalls, ok := message["tool_calls"].([]map[string]any)
		if !ok {
			continue
		}
		for _, call := range toolCalls {
			callID := strings.TrimSpace(asString(call["id"]))
			if callID != "" {
				pending = append(pending, callID)
			}
		}
	}
	if len(pending) > 0 {
		flushPending()
	}
	return out
}

func ensureLocalToolCallID(callID string) (string, error) {
	callID = strings.TrimSpace(callID)
	if callID != "" {
		return callID, nil
	}
	return domain.NewPrefixedID("call")
}

func localChatToolCallID(item domain.Item) string {
	if callID := strings.TrimSpace(item.CallID()); callID != "" {
		return callID
	}
	return strings.TrimSpace(item.ID())
}

func normalizeJSONStringField(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err == nil {
			return value
		}
	}
	return string(trimmed)
}

func stringifyToolOutput(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return "", err
		}
		return value, nil
	}

	var parts []map[string]any
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(asString(part["text"]))
		}
		if builder.Len() > 0 {
			return builder.String(), nil
		}
	}

	compacted, err := domain.CompactJSON(trimmed)
	if err != nil {
		return "", err
	}
	return compacted, nil
}

func stringifyMCPToolOutput(item domain.Item) (string, error) {
	if output, err := stringifyToolOutput(item.OutputRaw()); err == nil && strings.TrimSpace(output) != "" {
		return output, nil
	}

	rawError := bytes.TrimSpace(item.RawField("error"))
	if len(rawError) == 0 || bytes.Equal(rawError, []byte("null")) {
		return "", nil
	}
	var payload map[string]any
	if err := json.Unmarshal(rawError, &payload); err == nil {
		if message := strings.TrimSpace(asString(payload["message"])); message != "" {
			return message, nil
		}
	}
	compacted, err := domain.CompactJSON(rawError)
	if err != nil {
		return "", err
	}
	return compacted, nil
}

func decodeChatToolDefinitions(raw json.RawMessage) ([]map[string]any, error) {
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, domain.ErrUnsupportedShape
	}

	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		switch strings.ToLower(strings.TrimSpace(asString(tool["type"]))) {
		case "function":
			name := strings.TrimSpace(asString(tool["name"]))
			if name == "" {
				return nil, domain.ErrUnsupportedShape
			}
			function := map[string]any{
				"name": name,
			}
			if description := strings.TrimSpace(asString(tool["description"])); description != "" {
				function["description"] = description
			}
			if parameters, ok := tool["parameters"]; ok {
				function["parameters"] = parameters
			}
			out = append(out, map[string]any{
				"type":     "function",
				"function": function,
			})
		case localBuiltinShellToolType, localBuiltinApplyPatchToolType:
			definition, _, err := buildLocalBuiltinToolDefinition(tool)
			if err != nil {
				return nil, err
			}
			out = append(out, definition)
		default:
			return nil, domain.ErrUnsupportedShape
		}
	}
	return out, nil
}

func decodeChatToolChoice(raw json.RawMessage) (any, error) {
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		return literal, nil
	}

	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil, domain.ErrUnsupportedShape
	}
	switch strings.ToLower(strings.TrimSpace(asString(choice["type"]))) {
	case "function":
		name := strings.TrimSpace(asString(choice["name"]))
		if name == "" {
			if _, ok := choice["function"]; ok {
				return choice, nil
			}
			return nil, domain.ErrUnsupportedShape
		}
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		}, nil
	case localBuiltinShellToolType, localBuiltinApplyPatchToolType:
		descriptor, ok := localBuiltinToolDescriptorForType(asString(choice["type"]))
		if !ok {
			return nil, domain.ErrUnsupportedShape
		}
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": descriptor.SyntheticName,
			},
		}, nil
	default:
		return nil, domain.ErrUnsupportedShape
	}
}

func parseLocalToolLoopChatCompletion(raw []byte, responseID string, model string, previousResponseID string, conversationID string, plan customToolTransportPlan) (domain.Response, error) {
	var payload localChatCompletionResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return domain.Response{}, fmt.Errorf("decode chat completion response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return domain.Response{}, &llama.InvalidResponseError{Message: "chat completion response did not contain choices"}
	}

	message := payload.Choices[0].Message
	content := extractChatCompletionContent(message.Content)
	toolCalls := make([]domain.Item, 0, len(message.ToolCalls)+1)
	if len(message.ToolCalls) > 0 && strings.TrimSpace(content) != "" {
		reasoning, err := newLocalReasoningItem(content)
		if err != nil {
			return domain.Response{}, err
		}
		toolCalls = append(toolCalls, reasoning)
	}

	for _, call := range message.ToolCalls {
		if !strings.EqualFold(strings.TrimSpace(call.Type), "function") {
			return domain.Response{}, domain.ErrUnsupportedShape
		}
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			return domain.Response{}, &llama.InvalidResponseError{Message: "chat completion tool call name was empty"}
		}
		callID, err := ensureLocalToolCallID(call.ID)
		if err != nil {
			return domain.Response{}, err
		}

		itemPayload := map[string]any{
			"type":      "function_call",
			"call_id":   callID,
			"name":      name,
			"arguments": normalizeJSONStringField(call.Function.Arguments),
			"status":    "completed",
		}
		var builtinMeta *domain.ItemMeta
		if rewritten, meta, changed, err := remapFunctionCallItemToLocalBuiltin(itemPayload); err != nil {
			return domain.Response{}, err
		} else if changed {
			itemPayload = rewritten
			builtinMeta = meta
		}
		if rewritten, changed := remapFunctionCallItemToCustom(itemPayload, plan.Bridge); changed {
			itemPayload = rewritten
		}

		rawItem, err := json.Marshal(itemPayload)
		if err != nil {
			return domain.Response{}, err
		}
		item, err := domain.NewItem(rawItem)
		if err != nil {
			return domain.Response{}, err
		}
		if builtinMeta != nil {
			item.Meta = builtinMeta
		}
		if err := validateLocalConstrainedToolCall(item, plan.Bridge, toolCalls); err != nil {
			return domain.Response{}, err
		}
		toolCalls = append(toolCalls, item)
	}

	if len(toolCalls) > 0 {
		response := domain.Response{
			ID:                 responseID,
			Object:             "response",
			Model:              model,
			PreviousResponseID: previousResponseID,
			Conversation:       domain.NewConversationReference(conversationID),
			OutputText:         "",
			Output:             toolCalls,
		}
		return annotateResponseCustomToolMetadata(response, plan), nil
	}

	if strings.TrimSpace(content) == "" {
		return domain.Response{}, &llama.InvalidResponseError{Message: "chat completion response did not include assistant text or tool calls"}
	}

	return domain.NewResponse(responseID, model, content, previousResponseID, conversationID, domain.NowUTC().Unix()), nil
}

func extractChatCompletionContent(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text
	}

	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		return builder.String()
	}

	return ""
}

func newLocalReasoningItem(text string) (domain.Item, error) {
	raw, err := json.Marshal(map[string]any{
		"type":   "reasoning",
		"status": "completed",
		"content": []map[string]any{
			{
				"type": "reasoning_text",
				"text": text,
			},
		},
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}
