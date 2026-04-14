package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

const (
	LocalComputerBackendDisabled        = "disabled"
	LocalComputerBackendChatCompletions = "chat_completions"
)

var shimLocalComputerFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
	"include":             {},
}

type LocalComputerRuntimeConfig struct {
	Backend string
}

type localComputerConfig struct {
	ToolRequired bool
}

type localComputerPlan struct {
	Decision string                     `json:"decision"`
	Actions  []map[string]any           `json:"actions"`
	Message  string                     `json:"message"`
	Raw      map[string]json.RawMessage `json:"-"`
}

func (c LocalComputerRuntimeConfig) Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.Backend), LocalComputerBackendChatCompletions)
}

func isLocalComputerToolRequest(rawFields map[string]json.RawMessage) bool {
	tools := decodeToolList(rawFields)
	return len(tools) == 1 && strings.EqualFold(strings.TrimSpace(asString(tools[0]["type"])), "computer")
}

func supportsLocalComputer(rawFields map[string]json.RawMessage, runtime LocalComputerRuntimeConfig) bool {
	if !runtime.Enabled() {
		return false
	}
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		if _, ok := shimLocalComputerFields[key]; ok {
			continue
		}
		return false
	}
	_, err := parseLocalComputerConfig(rawFields)
	return err == nil
}

func localComputerDisabledError() error {
	return domain.NewValidationError("tools", "shim-local computer runtime is disabled; set responses.computer.backend to chat_completions or use responses.mode=prefer_upstream")
}

func (h *responseHandler) createLocalComputerResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	config, err := parseLocalComputerConfig(rawFields)
	if err != nil {
		return domain.Response{}, err
	}

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

	plannerBody, err := buildLocalComputerPlannerBody(input.Model, input.GenerationOptions, prepared, config.ToolRequired)
	if err != nil {
		return domain.Response{}, err
	}
	planText, err := h.proxy.client.CreateChatCompletionText(ctx, plannerBody)
	if err != nil {
		return domain.Response{}, err
	}
	plan, err := parseLocalComputerPlan(planText)
	if err != nil {
		return domain.Response{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}
	createdAt := domain.NowUTC().Unix()

	var response domain.Response
	switch plan.Decision {
	case "computer_call":
		item, err := buildLocalComputerCallItem(plan.Actions)
		if err != nil {
			return domain.Response{}, err
		}
		response = domain.Response{
			ID:                 responseID,
			Object:             "response",
			Model:              input.Model,
			CreatedAt:          createdAt,
			Status:             "completed",
			Output:             []domain.Item{item},
			OutputText:         "",
			PreviousResponseID: input.PreviousResponseID,
			Conversation:       domain.NewConversationReference(input.ConversationID),
			Metadata:           map[string]string{},
		}
	case "assistant":
		if config.ToolRequired {
			return domain.Response{}, domain.NewValidationError("tool_choice", "shim-local computer requires tool execution when tool_choice=required")
		}
		response = domain.NewResponse(responseID, input.Model, plan.Message, input.PreviousResponseID, input.ConversationID, createdAt)
	default:
		return domain.Response{}, domain.ErrUnsupportedShape
	}

	response, err = h.service.FinalizeLocalResponse(input, prepared.ContextItems, response)
	if err != nil {
		return domain.Response{}, err
	}
	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func parseLocalComputerConfig(rawFields map[string]json.RawMessage) (localComputerConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) != 1 {
		return localComputerConfig{}, domain.NewValidationError("tools", "shim-local computer requires exactly one computer tool")
	}

	tool := tools[0]
	for key := range tool {
		switch key {
		case "type":
		default:
			return localComputerConfig{}, domain.NewValidationError("tools", "unsupported computer tool field "+`"`+key+`"`+" in shim-local mode")
		}
	}
	if !strings.EqualFold(strings.TrimSpace(asString(tool["type"])), "computer") {
		return localComputerConfig{}, domain.NewValidationError("tools", "shim-local computer requires tools[0].type=computer")
	}

	toolRequired, err := validateLocalComputerToolChoice(rawFields["tool_choice"])
	if err != nil {
		return localComputerConfig{}, err
	}
	if err := validateLocalComputerInclude(rawFields["include"]); err != nil {
		return localComputerConfig{}, err
	}
	if err := validateLocalComputerParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return localComputerConfig{}, err
	}

	return localComputerConfig{ToolRequired: toolRequired}, nil
}

func validateLocalComputerToolChoice(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil
	}

	var choice string
	if err := json.Unmarshal(trimmed, &choice); err == nil {
		switch strings.TrimSpace(choice) {
		case "auto":
			return false, nil
		case "required":
			return true, nil
		default:
			return false, domain.NewValidationError("tool_choice", "shim-local computer supports tool_choice=auto or required")
		}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return false, domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local computer")
	}
	if len(payload) != 1 {
		return false, domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local computer")
	}

	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil {
		return false, domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local computer")
	}
	if !strings.EqualFold(strings.TrimSpace(choiceType), "computer") {
		return false, domain.NewValidationError("tool_choice", "shim-local computer only supports tool_choice targeting computer")
	}
	return true, nil
}

func validateLocalComputerInclude(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var includes []string
	if err := json.Unmarshal(trimmed, &includes); err != nil {
		return domain.NewValidationError("include", "include must be an array of strings")
	}
	for _, include := range includes {
		switch strings.TrimSpace(include) {
		case "", "computer_call_output.output.image_url":
		default:
			return domain.NewValidationError("include", "unsupported include value for shim-local computer")
		}
	}
	return nil
}

func validateLocalComputerParallelToolCalls(raw json.RawMessage) error {
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

func buildLocalComputerPlannerBody(model string, options map[string]json.RawMessage, prepared service.PreparedResponseContext, toolRequired bool) ([]byte, error) {
	prefixItems := prepared.ContextItems
	if len(prepared.NormalizedInput) <= len(prefixItems) {
		prefixItems = prefixItems[:len(prefixItems)-len(prepared.NormalizedInput)]
	}

	messages, err := projectLocalComputerPlannerMessages(prefixItems)
	if err != nil {
		return nil, err
	}
	messages = append(messages, map[string]any{
		"role":    "system",
		"content": buildLocalComputerPlanningPrompt(toolRequired),
	})
	currentMessages, err := projectLocalComputerPlannerMessages(prepared.NormalizedInput)
	if err != nil {
		return nil, err
	}
	messages = append(messages, currentMessages...)

	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	for key, raw := range options {
		targetKey := key
		if key == "max_output_tokens" {
			targetKey = "max_tokens"
		}
		body[targetKey] = json.RawMessage(raw)
	}
	return json.Marshal(body)
}

func buildLocalComputerPlanningPrompt(toolRequired bool) string {
	lines := []string{
		"You are the shim-local computer planner.",
		"Return JSON only without markdown or extra commentary.",
		"Choose one of these shapes exactly:",
		`{"decision":"computer_call","actions":[...]}`,
		`{"decision":"assistant","message":"..."}`,
		"Use the latest computer_call_output screenshot as the current UI state when it is present.",
		"If there is no current screenshot yet, prefer a single screenshot action before any other UI action.",
		"Supported action types in this subset are screenshot, click, double_click, scroll, type, wait, keypress, drag, and move.",
		"Only include the fields needed for the chosen action objects.",
	}
	if toolRequired {
		lines = append(lines, "tool_choice is required for this turn, so you must return decision=computer_call.")
	} else {
		lines = append(lines, "If the task is blocked or the UI is unsuitable, you may return decision=assistant with a brief explanation.")
	}
	return strings.Join(lines, "\n")
}

func projectLocalComputerPlannerMessages(items []domain.Item) ([]map[string]any, error) {
	messages := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			message, ok, err := projectLocalComputerMessageItem(item)
			if err != nil {
				return nil, err
			}
			if ok {
				messages = append(messages, message)
			}
		case "computer_call":
			message, err := projectLocalComputerCallItem(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
		case "computer_call_output":
			message, err := projectLocalComputerCallOutputItem(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func projectLocalComputerMessageItem(item domain.Item) (map[string]any, bool, error) {
	role := strings.TrimSpace(item.Role)
	if role == "developer" {
		role = "system"
	}
	switch role {
	case "system", "user", "assistant":
	default:
		return nil, false, nil
	}

	rawContent := bytes.TrimSpace(item.RawField("content"))
	if len(rawContent) == 0 || bytes.Equal(rawContent, []byte("null")) {
		return nil, false, nil
	}
	if rawContent[0] == '"' {
		return map[string]any{
			"role":    role,
			"content": domain.MessageText(item),
		}, true, nil
	}

	var rawParts []map[string]any
	if err := json.Unmarshal(rawContent, &rawParts); err != nil {
		return nil, false, domain.ErrUnsupportedShape
	}

	outParts := make([]map[string]any, 0, len(rawParts))
	for _, part := range rawParts {
		partType := strings.TrimSpace(asString(part["type"]))
		if text := strings.TrimSpace(asString(part["text"])); text != "" {
			outParts = append(outParts, map[string]any{
				"type": "text",
				"text": text,
			})
		}
		switch partType {
		case "input_image", "image":
			if imagePart, ok := localComputerImageURLPart(part); ok {
				outParts = append(outParts, imagePart)
			}
		}
	}
	if len(outParts) == 0 {
		return map[string]any{
			"role":    role,
			"content": domain.MessageText(item),
		}, true, nil
	}
	return map[string]any{
		"role":    role,
		"content": outParts,
	}, true, nil
}

func projectLocalComputerCallItem(item domain.Item) (map[string]any, error) {
	callID := strings.TrimSpace(item.CallID())
	actions := "[]"
	if rawActions := bytes.TrimSpace(item.RawField("actions")); len(rawActions) > 0 && !bytes.Equal(rawActions, []byte("null")) {
		compacted, err := domain.CompactJSON(rawActions)
		if err != nil {
			return nil, err
		}
		actions = compacted
	}
	text := "Previous computer_call"
	if callID != "" {
		text += " call_id=" + callID
	}
	text += " actions: " + actions
	return map[string]any{
		"role":    "assistant",
		"content": text,
	}, nil
}

func projectLocalComputerCallOutputItem(item domain.Item) (map[string]any, error) {
	callID := strings.TrimSpace(item.CallID())
	outputRaw := bytes.TrimSpace(item.RawField("output"))
	if len(outputRaw) == 0 || bytes.Equal(outputRaw, []byte("null")) {
		return map[string]any{
			"role":    "user",
			"content": "computer_call_output received without output payload.",
		}, nil
	}

	var output map[string]any
	if err := json.Unmarshal(outputRaw, &output); err != nil {
		return nil, err
	}

	outputType := strings.TrimSpace(asString(output["type"]))
	switch outputType {
	case "computer_screenshot":
		imageURL := strings.TrimSpace(asString(output["image_url"]))
		parts := []map[string]any{
			{
				"type": "text",
				"text": "computer_call_output screenshot received for call_id " + callID + ". Use this as the latest UI state.",
			},
		}
		if imageURL != "" {
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": imageURL,
				},
			})
		}
		return map[string]any{
			"role":    "user",
			"content": parts,
		}, nil
	default:
		compacted, err := domain.CompactJSON(outputRaw)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"role":    "user",
			"content": "computer_call_output for call_id " + callID + ": " + compacted,
		}, nil
	}
}

func localComputerImageURLPart(part map[string]any) (map[string]any, bool) {
	var (
		url    string
		detail string
	)
	switch value := part["image_url"].(type) {
	case string:
		url = strings.TrimSpace(value)
	case map[string]any:
		url = strings.TrimSpace(asString(value["url"]))
		detail = strings.TrimSpace(asString(value["detail"]))
	}
	if url == "" {
		url = strings.TrimSpace(asString(part["url"]))
	}
	if detail == "" {
		detail = strings.TrimSpace(asString(part["detail"]))
	}
	if url == "" {
		return nil, false
	}
	imageURL := map[string]any{"url": url}
	if detail != "" {
		imageURL["detail"] = detail
	}
	return map[string]any{
		"type":      "image_url",
		"image_url": imageURL,
	}, true
}

func parseLocalComputerPlan(raw string) (localComputerPlan, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return localComputerPlan{}, domain.ErrUnsupportedShape
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return localComputerPlan{}, domain.ErrUnsupportedShape
	}

	var plan localComputerPlan
	plan.Raw = payload
	if err := json.Unmarshal(payload["decision"], &plan.Decision); err != nil {
		return localComputerPlan{}, domain.ErrUnsupportedShape
	}
	plan.Decision = strings.TrimSpace(plan.Decision)

	switch plan.Decision {
	case "computer_call":
		if err := json.Unmarshal(payload["actions"], &plan.Actions); err != nil {
			return localComputerPlan{}, domain.ErrUnsupportedShape
		}
		if len(plan.Actions) == 0 {
			return localComputerPlan{}, domain.ErrUnsupportedShape
		}
		for _, action := range plan.Actions {
			if strings.TrimSpace(asString(action["type"])) == "" {
				return localComputerPlan{}, domain.ErrUnsupportedShape
			}
		}
	case "assistant":
		if err := json.Unmarshal(payload["message"], &plan.Message); err != nil {
			return localComputerPlan{}, domain.ErrUnsupportedShape
		}
		plan.Message = strings.TrimSpace(plan.Message)
		if plan.Message == "" {
			return localComputerPlan{}, domain.ErrUnsupportedShape
		}
	default:
		return localComputerPlan{}, domain.ErrUnsupportedShape
	}

	return plan, nil
}

func buildLocalComputerCallItem(actions []map[string]any) (domain.Item, error) {
	callID, err := domain.NewPrefixedID("call")
	if err != nil {
		return domain.Item{}, err
	}
	raw, err := json.Marshal(map[string]any{
		"type":    "computer_call",
		"status":  "completed",
		"call_id": callID,
		"actions": actions,
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}
