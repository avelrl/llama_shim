package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/service"
)

const (
	defaultLocalCodeInterpreterPlannedCodeLimit = 16 << 10
)

var shimLocalCodeInterpreterFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
	"include":             {},
}

var localCodeInterpreterForbiddenFragments = []string{
	"import os",
	"from os",
	"import subprocess",
	"from subprocess",
	"import socket",
	"from socket",
	"import pathlib",
	"from pathlib",
	"import shutil",
	"from shutil",
	"import glob",
	"from glob",
	"import urllib",
	"from urllib",
	"import requests",
	"from requests",
	"open(",
	"exec(",
	"eval(",
	"compile(",
	"__import__(",
	"input(",
	"breakpoint(",
}

type LocalCodeInterpreterRuntimeConfig struct {
	Backend sandbox.Backend
}

func (c LocalCodeInterpreterRuntimeConfig) Enabled() bool {
	return c.Backend != nil
}

type localCodeInterpreterConfig struct {
	IncludeOutputs bool
	ToolRequired   bool
}

type localCodeInterpreterPlan struct {
	UseCodeInterpreter bool   `json:"use_code_interpreter"`
	Code               string `json:"code"`
}

func isLocalCodeInterpreterToolRequest(rawFields map[string]json.RawMessage) bool {
	tools := decodeToolList(rawFields)
	return len(tools) == 1 && strings.EqualFold(strings.TrimSpace(asString(tools[0]["type"])), "code_interpreter")
}

func supportsLocalCodeInterpreter(rawFields map[string]json.RawMessage, runtime LocalCodeInterpreterRuntimeConfig) bool {
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
		if _, ok := shimLocalCodeInterpreterFields[key]; ok {
			continue
		}
		return false
	}

	_, err := parseLocalCodeInterpreterConfig(rawFields)
	return err == nil
}

func (h *responseHandler) createLocalCodeInterpreterResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	config, err := parseLocalCodeInterpreterConfig(rawFields)
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

	planningContext, err := buildLocalCodeInterpreterPlanningContext(prepared)
	if err != nil {
		return domain.Response{}, err
	}
	planRaw, err := h.proxy.client.Generate(ctx, input.Model, planningContext, buildLocalCodeInterpreterPlanningOptions(input.GenerationOptions))
	if err != nil {
		return domain.Response{}, err
	}
	plan, err := parseLocalCodeInterpreterPlan(planRaw)
	if err != nil {
		return domain.Response{}, err
	}

	if !plan.UseCodeInterpreter {
		if config.ToolRequired {
			return domain.Response{}, domain.NewValidationError("tool_choice", "shim-local code_interpreter requires tool execution when tool_choice=required")
		}
		return h.service.Create(ctx, input)
	}
	if err := validateLocalCodeInterpreterPlanCode(plan.Code); err != nil {
		return domain.Response{}, err
	}

	logs, err := h.executeLocalCodeInterpreter(ctx, plan.Code)
	if err != nil {
		return domain.Response{}, err
	}

	generationContext, err := buildLocalCodeInterpreterExecutionContext(prepared, plan.Code, logs)
	if err != nil {
		return domain.Response{}, err
	}
	if _, err := h.service.PrepareLocalResponseText(input, generationContext); err != nil {
		return domain.Response{}, err
	}

	outputText, err := h.proxy.client.Generate(ctx, input.Model, generationContext, input.GenerationOptions)
	if err != nil {
		return domain.Response{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}
	containerID, err := domain.NewPrefixedID("cntr")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate container id: %w", err)
	}
	createdAt := domain.NowUTC().Unix()
	response := domain.NewResponse(responseID, input.Model, outputText, input.PreviousResponseID, input.ConversationID, createdAt)

	codeInterpreterItem, err := buildLocalCodeInterpreterCallItem(plan.Code, containerID, logs, config.IncludeOutputs)
	if err != nil {
		return domain.Response{}, err
	}
	messageItem, err := buildCompletedAssistantMessage(outputText)
	if err != nil {
		return domain.Response{}, err
	}
	response.Output = []domain.Item{codeInterpreterItem, messageItem}
	response.OutputText = outputText

	response, err = h.service.FinalizeLocalResponse(input, generationContext, response)
	if err != nil {
		return domain.Response{}, err
	}

	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func parseLocalCodeInterpreterConfig(rawFields map[string]json.RawMessage) (localCodeInterpreterConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) != 1 {
		return localCodeInterpreterConfig{}, domain.NewValidationError("tools", "shim-local code_interpreter requires exactly one code_interpreter tool")
	}

	tool := tools[0]
	for key := range tool {
		switch key {
		case "type", "container":
		default:
			return localCodeInterpreterConfig{}, domain.NewValidationError("tools", "unsupported code_interpreter tool field "+`"`+key+`"`+" in shim-local mode")
		}
	}

	if !strings.EqualFold(strings.TrimSpace(asString(tool["type"])), "code_interpreter") {
		return localCodeInterpreterConfig{}, domain.NewValidationError("tools", "shim-local code_interpreter requires tools[0].type=code_interpreter")
	}
	if err := validateLocalCodeInterpreterContainer(tool["container"]); err != nil {
		return localCodeInterpreterConfig{}, err
	}

	includeOutputs, err := parseLocalCodeInterpreterInclude(rawFields["include"])
	if err != nil {
		return localCodeInterpreterConfig{}, err
	}
	toolRequired, err := validateLocalCodeInterpreterToolChoice(rawFields["tool_choice"])
	if err != nil {
		return localCodeInterpreterConfig{}, err
	}
	if err := validateLocalCodeInterpreterParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return localCodeInterpreterConfig{}, err
	}

	return localCodeInterpreterConfig{
		IncludeOutputs: includeOutputs,
		ToolRequired:   toolRequired,
	}, nil
}

func validateLocalCodeInterpreterContainer(value any) error {
	container, ok := value.(map[string]any)
	if !ok || container == nil {
		return domain.NewValidationError("tools", "code_interpreter.container must be an object")
	}
	for key := range container {
		switch key {
		case "type":
		default:
			return domain.NewValidationError("tools", "unsupported code_interpreter.container field "+`"`+key+`"`+" in shim-local mode")
		}
	}
	if !strings.EqualFold(strings.TrimSpace(asString(container["type"])), "auto") {
		return domain.NewValidationError("tools", "shim-local code_interpreter only supports container.type=auto")
	}
	return nil
}

func parseLocalCodeInterpreterInclude(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil
	}

	var includes []string
	if err := json.Unmarshal(trimmed, &includes); err != nil {
		return false, domain.NewValidationError("include", "include must be an array of strings")
	}

	includeOutputs := false
	for _, include := range includes {
		switch strings.TrimSpace(include) {
		case "":
		case "code_interpreter_call.outputs":
			includeOutputs = true
		default:
			return false, domain.NewValidationError("include", "unsupported include value for shim-local code_interpreter")
		}
	}
	return includeOutputs, nil
}

func validateLocalCodeInterpreterToolChoice(raw json.RawMessage) (bool, error) {
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
			return false, domain.NewValidationError("tool_choice", "shim-local code_interpreter supports tool_choice=auto or required")
		}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return false, domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local code_interpreter")
	}
	if len(payload) != 1 {
		return false, domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local code_interpreter")
	}

	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil {
		return false, domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local code_interpreter")
	}
	if !strings.EqualFold(strings.TrimSpace(choiceType), "code_interpreter") {
		return false, domain.NewValidationError("tool_choice", "shim-local code_interpreter only supports tool_choice targeting code_interpreter")
	}
	return true, nil
}

func validateLocalCodeInterpreterParallelToolCalls(raw json.RawMessage) error {
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

func buildLocalCodeInterpreterPlanningContext(prepared service.PreparedResponseContext) ([]domain.Item, error) {
	prefixItems := prepared.ContextItems
	if len(prepared.NormalizedInput) <= len(prefixItems) {
		prefixItems = prefixItems[:len(prefixItems)-len(prepared.NormalizedInput)]
	}

	prefix, err := domain.ProjectLocalTextGenerationContext(prefixItems)
	if err != nil {
		return nil, err
	}
	currentInput, err := domain.ProjectLocalTextGenerationContext(prepared.NormalizedInput)
	if err != nil {
		return nil, err
	}

	plannerPrompt := domain.NewInputTextMessage("system", buildLocalCodeInterpreterPlanningPrompt())
	out := make([]domain.Item, 0, len(prefix)+len(currentInput)+1)
	out = append(out, prefix...)
	out = append(out, plannerPrompt)
	out = append(out, currentInput...)
	return out, nil
}

func buildLocalCodeInterpreterPlanningPrompt() string {
	return strings.Join([]string{
		"You are the shim-local code interpreter planner.",
		"Decide whether the current turn needs Python execution.",
		"Return JSON only with keys use_code_interpreter and code.",
		"If Python is not needed, return {\"use_code_interpreter\":false,\"code\":\"\"}.",
		"If Python is needed, return {\"use_code_interpreter\":true,\"code\":\"...\"}.",
		"The code must be pure Python for the shim-local code interpreter backend.",
		"Do not access files, the network, subprocesses, environment variables, or interactive input.",
		"Prefer concise code that prints the useful result to stdout.",
	}, " ")
}

func buildLocalCodeInterpreterPlanningOptions(options map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := cloneGenerationOptions(options)
	cloned["response_format"] = json.RawMessage(`{"type":"json_object"}`)
	return cloned
}

func parseLocalCodeInterpreterPlan(raw string) (localCodeInterpreterPlan, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return localCodeInterpreterPlan{}, &llama.InvalidResponseError{Message: "shim-local code_interpreter planner response was empty"}
	}

	var plan localCodeInterpreterPlan
	if err := json.Unmarshal([]byte(trimmed), &plan); err != nil {
		return localCodeInterpreterPlan{}, &llama.InvalidResponseError{Message: "shim-local code_interpreter planner did not return valid JSON"}
	}
	plan.Code = strings.TrimSpace(plan.Code)
	return plan, nil
}

func validateLocalCodeInterpreterPlanCode(code string) error {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return &llama.InvalidResponseError{Message: "shim-local code_interpreter planner returned empty code"}
	}
	if len(trimmed) > defaultLocalCodeInterpreterPlannedCodeLimit {
		return domain.NewValidationError("tools", "shim-local code_interpreter planned code exceeded the maximum supported size")
	}
	lowered := strings.ToLower(trimmed)
	for _, fragment := range localCodeInterpreterForbiddenFragments {
		if strings.Contains(lowered, fragment) {
			return domain.NewValidationError("tools", "shim-local code_interpreter only supports pure-python snippets without filesystem, network, or subprocess access")
		}
	}
	return nil
}

func (h *responseHandler) executeLocalCodeInterpreter(ctx context.Context, code string) (string, error) {
	if !h.localCodeInterpreter.Enabled() {
		return "", localCodeInterpreterDisabledError()
	}
	result, err := h.localCodeInterpreter.Backend.ExecutePython(ctx, sandbox.ExecuteRequest{Code: code})
	if err != nil {
		if errors.Is(err, sandbox.ErrDisabled) {
			return "", localCodeInterpreterDisabledError()
		}
		return result.Logs, fmt.Errorf("execute shim-local code_interpreter via %s backend: %w", h.localCodeInterpreter.Backend.Kind(), err)
	}
	return result.Logs, nil
}

func buildLocalCodeInterpreterExecutionContext(prepared service.PreparedResponseContext, code string, logs string) ([]domain.Item, error) {
	prefixItems := prepared.ContextItems
	if len(prepared.NormalizedInput) <= len(prefixItems) {
		prefixItems = prefixItems[:len(prefixItems)-len(prepared.NormalizedInput)]
	}

	prefix, err := domain.ProjectLocalTextGenerationContext(prefixItems)
	if err != nil {
		return nil, err
	}
	currentInput, err := domain.ProjectLocalTextGenerationContext(prepared.NormalizedInput)
	if err != nil {
		return nil, err
	}

	executionPrompt := domain.NewInputTextMessage("system", buildLocalCodeInterpreterExecutionPrompt(code, logs))
	out := make([]domain.Item, 0, len(prefix)+len(currentInput)+1)
	out = append(out, prefix...)
	out = append(out, executionPrompt)
	out = append(out, currentInput...)
	return out, nil
}

func buildLocalCodeInterpreterExecutionPrompt(code string, logs string) string {
	var builder strings.Builder
	builder.WriteString("A shim-local code interpreter already ran for this turn.\n")
	builder.WriteString("Use only the execution result below as the tool output.\n")
	builder.WriteString("If the execution result does not answer the request, say so plainly.\n")
	builder.WriteString("Executed Python code:\n")
	builder.WriteString(code)
	builder.WriteString("\n")
	builder.WriteString("Execution logs:\n")
	if strings.TrimSpace(logs) == "" {
		builder.WriteString("(no logs)\n")
	} else {
		builder.WriteString(logs)
		if !strings.HasSuffix(logs, "\n") {
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func buildLocalCodeInterpreterCallItem(code string, containerID string, logs string, includeOutputs bool) (domain.Item, error) {
	payload := map[string]any{
		"type":         "code_interpreter_call",
		"status":       "completed",
		"container_id": containerID,
		"code":         code,
		"outputs":      nil,
	}
	if includeOutputs {
		outputs := make([]map[string]any, 0, 1)
		if logs != "" {
			outputs = append(outputs, map[string]any{
				"type": "logs",
				"logs": logs,
			})
		}
		payload["outputs"] = outputs
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func cloneGenerationOptions(options map[string]json.RawMessage) map[string]json.RawMessage {
	if len(options) == 0 {
		return map[string]json.RawMessage{}
	}
	cloned := make(map[string]json.RawMessage, len(options))
	for key, value := range options {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}

func localCodeInterpreterDisabledError() error {
	return domain.NewValidationError("tools", "shim-local code_interpreter execution is disabled; set responses.code_interpreter.backend to unsafe_host or docker")
}
