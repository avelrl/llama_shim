package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
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

type LocalCodeInterpreterSessionStore interface {
	GetCodeInterpreterSession(ctx context.Context, id string) (domain.CodeInterpreterSession, error)
	SaveCodeInterpreterSession(ctx context.Context, session domain.CodeInterpreterSession) error
	TouchCodeInterpreterSession(ctx context.Context, id string, lastActiveAt string) error
	DeleteCodeInterpreterSession(ctx context.Context, id string) error
}

type LocalCodeInterpreterFileStore interface {
	GetFile(ctx context.Context, id string) (domain.StoredFile, error)
}

type localCodeInterpreterConfig struct {
	IncludeOutputs bool
	InputFileIDs   []string
	ToolRequired   bool
}

type localCodeInterpreterInputFile struct {
	Content       []byte
	FileID        string
	Filename      string
	WorkspaceName string
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

	inputFiles, err := h.resolveLocalCodeInterpreterInputFiles(ctx, config.InputFileIDs)
	if err != nil {
		return domain.Response{}, err
	}

	planningContext, err := buildLocalCodeInterpreterPlanningContext(prepared, inputFiles)
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

	logs, containerID, err := h.executeLocalCodeInterpreter(ctx, prepared, inputFiles, plan.Code)
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
	inputFileIDs, err := parseLocalCodeInterpreterContainer(tool["container"])
	if err != nil {
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
		InputFileIDs:   inputFileIDs,
		ToolRequired:   toolRequired,
	}, nil
}

func parseLocalCodeInterpreterContainer(value any) ([]string, error) {
	container, ok := value.(map[string]any)
	if !ok || container == nil {
		return nil, domain.NewValidationError("tools", "code_interpreter.container must be an object")
	}
	for key := range container {
		switch key {
		case "type", "file_ids":
		default:
			return nil, domain.NewValidationError("tools", "unsupported code_interpreter.container field "+`"`+key+`"`+" in shim-local mode")
		}
	}
	if !strings.EqualFold(strings.TrimSpace(asString(container["type"])), "auto") {
		return nil, domain.NewValidationError("tools", "shim-local code_interpreter only supports container.type=auto")
	}

	rawFileIDs, ok := container["file_ids"]
	if !ok || rawFileIDs == nil {
		return nil, nil
	}
	values, ok := rawFileIDs.([]any)
	if !ok {
		return nil, domain.NewValidationError("tools", "code_interpreter.container.file_ids must be an array of strings")
	}
	seen := make(map[string]struct{}, len(values))
	fileIDs := make([]string, 0, len(values))
	for _, value := range values {
		fileID := strings.TrimSpace(asString(value))
		if fileID == "" {
			return nil, domain.NewValidationError("tools", "code_interpreter.container.file_ids must not contain empty values")
		}
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}
		fileIDs = append(fileIDs, fileID)
	}
	return fileIDs, nil
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

func buildLocalCodeInterpreterPlanningContext(prepared service.PreparedResponseContext, inputFiles []localCodeInterpreterInputFile) ([]domain.Item, error) {
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

	plannerPrompt := domain.NewInputTextMessage("system", buildLocalCodeInterpreterPlanningPrompt(inputFiles))
	out := make([]domain.Item, 0, len(prefix)+len(currentInput)+1)
	out = append(out, prefix...)
	out = append(out, plannerPrompt)
	out = append(out, currentInput...)
	return out, nil
}

func buildLocalCodeInterpreterPlanningPrompt(inputFiles []localCodeInterpreterInputFile) string {
	base := []string{
		"You are the shim-local code interpreter planner.",
		"Decide whether the current turn needs Python execution.",
		"Return JSON only with keys use_code_interpreter and code.",
		"If Python is not needed, return {\"use_code_interpreter\":false,\"code\":\"\"}.",
		"If Python is needed, return {\"use_code_interpreter\":true,\"code\":\"...\"}.",
		"The code must be pure Python for the shim-local code interpreter backend.",
		"Do not access the network, subprocesses, environment variables, or interactive input.",
		"Prefer concise code that prints the useful result to stdout.",
	}
	if len(inputFiles) == 0 {
		base = append(base, "Do not access any filesystem paths for this turn.")
		return strings.Join(base, " ")
	}

	var builder strings.Builder
	builder.WriteString(strings.Join(base, " "))
	builder.WriteString(" You may read only the uploaded files already placed in the current working directory using relative paths with open().")
	builder.WriteString(" Available uploaded files:")
	for _, inputFile := range inputFiles {
		builder.WriteString(" ")
		builder.WriteString(inputFile.WorkspaceName)
		builder.WriteString(" (file_id=")
		builder.WriteString(inputFile.FileID)
		builder.WriteString(")")
	}
	builder.WriteString(" Do not access any other filesystem paths.")
	return builder.String()
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

func (h *responseHandler) executeLocalCodeInterpreter(ctx context.Context, prepared service.PreparedResponseContext, inputFiles []localCodeInterpreterInputFile, code string) (string, string, error) {
	if !h.localCodeInterpreter.Enabled() {
		return "", "", localCodeInterpreterDisabledError()
	}

	sessionID, canReuse, err := h.findReusableLocalCodeInterpreterSessionID(ctx, prepared)
	if err != nil {
		return "", "", err
	}
	if canReuse {
		result, err := h.executeLocalCodeInterpreterSession(ctx, sessionID, inputFiles, code)
		if err == nil {
			if touchErr := h.localCodeInterpreterSessions.TouchCodeInterpreterSession(ctx, sessionID, domain.FormatTime(domain.NowUTC())); touchErr != nil {
				return result.Logs, "", touchErr
			}
			return result.Logs, sessionID, nil
		}
		if errors.Is(err, sandbox.ErrSessionNotFound) {
			_ = h.localCodeInterpreterSessions.DeleteCodeInterpreterSession(ctx, sessionID)
		} else if errors.Is(err, sandbox.ErrDisabled) {
			return "", "", localCodeInterpreterDisabledError()
		} else {
			return result.Logs, "", fmt.Errorf("execute shim-local code_interpreter via %s backend: %w", h.localCodeInterpreter.Backend.Kind(), err)
		}
	}

	sessionID, err = domain.NewPrefixedID("cntr")
	if err != nil {
		return "", "", fmt.Errorf("generate container id: %w", err)
	}
	if err := h.localCodeInterpreter.Backend.CreateSession(ctx, sessionID); err != nil {
		if errors.Is(err, sandbox.ErrDisabled) {
			return "", "", localCodeInterpreterDisabledError()
		}
		return "", "", fmt.Errorf("create shim-local code_interpreter session via %s backend: %w", h.localCodeInterpreter.Backend.Kind(), err)
	}

	result, err := h.executeLocalCodeInterpreterSession(ctx, sessionID, inputFiles, code)
	if err != nil {
		_ = h.localCodeInterpreter.Backend.DestroySession(ctx, sessionID)
		if errors.Is(err, sandbox.ErrDisabled) {
			return "", "", localCodeInterpreterDisabledError()
		}
		return result.Logs, "", fmt.Errorf("execute shim-local code_interpreter via %s backend: %w", h.localCodeInterpreter.Backend.Kind(), err)
	}

	now := domain.FormatTime(domain.NowUTC())
	if err := h.localCodeInterpreterSessions.SaveCodeInterpreterSession(ctx, domain.CodeInterpreterSession{
		ID:           sessionID,
		Backend:      h.localCodeInterpreter.Backend.Kind(),
		CreatedAt:    now,
		LastActiveAt: now,
	}); err != nil {
		_ = h.localCodeInterpreter.Backend.DestroySession(ctx, sessionID)
		return result.Logs, "", err
	}
	return result.Logs, sessionID, nil
}

func (h *responseHandler) executeLocalCodeInterpreterSession(ctx context.Context, sessionID string, inputFiles []localCodeInterpreterInputFile, code string) (sandbox.ExecuteResult, error) {
	for _, inputFile := range inputFiles {
		if err := h.localCodeInterpreter.Backend.UploadFile(ctx, sessionID, sandbox.SessionFile{
			Name:    inputFile.WorkspaceName,
			Content: inputFile.Content,
		}); err != nil {
			return sandbox.ExecuteResult{}, err
		}
	}
	return h.localCodeInterpreter.Backend.ExecutePython(ctx, sandbox.ExecuteRequest{
		SessionID: sessionID,
		Code:      code,
	})
}

func (h *responseHandler) findReusableLocalCodeInterpreterSessionID(ctx context.Context, prepared service.PreparedResponseContext) (string, bool, error) {
	if h.localCodeInterpreterSessions == nil || h.localCodeInterpreter.Backend == nil {
		return "", false, nil
	}

	for i := len(prepared.EffectiveInput) - 1; i >= 0; i-- {
		item := prepared.EffectiveInput[i]
		if strings.TrimSpace(item.Type) != "code_interpreter_call" {
			continue
		}
		containerID := strings.TrimSpace(item.StringField("container_id"))
		if containerID == "" {
			continue
		}
		session, err := h.localCodeInterpreterSessions.GetCodeInterpreterSession(ctx, containerID)
		if err != nil {
			if errors.Is(err, sqlite.ErrNotFound) {
				return "", false, nil
			}
			return "", false, err
		}
		if strings.TrimSpace(session.Backend) != h.localCodeInterpreter.Backend.Kind() {
			return "", false, nil
		}
		return session.ID, true, nil
	}
	return "", false, nil
}

func (h *responseHandler) resolveLocalCodeInterpreterInputFiles(ctx context.Context, fileIDs []string) ([]localCodeInterpreterInputFile, error) {
	if len(fileIDs) == 0 {
		return nil, nil
	}
	if h.localCodeInterpreterFiles == nil {
		return nil, fmt.Errorf("local code interpreter file store is not configured")
	}

	usedNames := make(map[string]int, len(fileIDs))
	files := make([]localCodeInterpreterInputFile, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		file, err := h.localCodeInterpreterFiles.GetFile(ctx, fileID)
		if err != nil {
			if errors.Is(err, sqlite.ErrNotFound) {
				return nil, domain.NewValidationError("tools", "unknown code_interpreter.container.file_ids value "+`"`+fileID+`"`)
			}
			return nil, err
		}
		files = append(files, localCodeInterpreterInputFile{
			Content:       file.Content,
			FileID:        file.ID,
			Filename:      file.Filename,
			WorkspaceName: uniqueLocalCodeInterpreterWorkspaceName(file.Filename, file.ID, usedNames),
		})
	}
	return files, nil
}

func uniqueLocalCodeInterpreterWorkspaceName(filename string, fallback string, used map[string]int) string {
	name := sanitizeLocalCodeInterpreterWorkspaceName(filename, fallback)
	candidate := name
	for suffix := 1; ; suffix++ {
		key := strings.ToLower(candidate)
		if used[key] == 0 {
			used[key] = 1
			return candidate
		}

		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		if stem == "" {
			stem = fallback
			ext = ""
		}
		candidate = fmt.Sprintf("%s-%d%s", stem, suffix+1, ext)
	}
}

func sanitizeLocalCodeInterpreterWorkspaceName(filename string, fallback string) string {
	base := strings.TrimSpace(filepath.Base(filename))
	if base == "" || base == "." || base == ".." {
		base = fallback
	}

	var builder strings.Builder
	for _, r := range base {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}

	sanitized := strings.Trim(builder.String(), "._-")
	if sanitized == "" || sanitized == "." || sanitized == ".." {
		return fallback
	}
	return sanitized
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
