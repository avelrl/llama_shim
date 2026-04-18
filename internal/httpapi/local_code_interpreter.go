package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

type LocalCodeInterpreterRuntimeConfig struct {
	Backend                sandbox.Backend
	Limits                 LocalCodeInterpreterLimits
	InputFileURLPolicy     string
	InputFileURLAllowHosts []string
}

func (c LocalCodeInterpreterRuntimeConfig) Enabled() bool {
	return c.Backend != nil
}

func (c LocalCodeInterpreterRuntimeConfig) normalizedLimits() LocalCodeInterpreterLimits {
	return normalizeLocalCodeInterpreterLimits(c.Limits)
}

func (c LocalCodeInterpreterRuntimeConfig) allowsRemoteInputFileURL(parsedURL *neturl.URL) error {
	mode := strings.ToLower(strings.TrimSpace(c.InputFileURLPolicy))
	if mode == "" {
		mode = "disabled"
	}

	switch mode {
	case "disabled":
		return domain.NewValidationError("input", "shim-local code_interpreter disables input_file.file_url by default; set responses.code_interpreter.input_file_url_policy to allowlist or unsafe_allow_http_https")
	case "unsafe_allow_http_https":
		return nil
	case "allowlist":
		host := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
		if host == "" {
			return domain.NewValidationError("input", "input_file.file_url must include a host in shim-local code_interpreter mode")
		}
		for _, candidate := range c.InputFileURLAllowHosts {
			if matchesLocalCodeInterpreterAllowedHost(host, candidate) {
				return nil
			}
		}
		if len(c.InputFileURLAllowHosts) == 0 {
			return domain.NewValidationError("input", "shim-local code_interpreter input_file.file_url allowlist is empty")
		}
		return domain.NewValidationError("input", "input_file.file_url host is not allowlisted in shim-local code_interpreter mode")
	default:
		return domain.NewValidationError("input", "shim-local code_interpreter input_file.file_url policy is invalid")
	}
}

func matchesLocalCodeInterpreterAllowedHost(host string, candidate string) bool {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	normalizedCandidate := strings.ToLower(strings.TrimSpace(candidate))
	if normalizedHost == "" || normalizedCandidate == "" {
		return false
	}
	if strings.HasPrefix(normalizedCandidate, "*.") {
		suffix := strings.TrimPrefix(normalizedCandidate, "*.")
		return suffix != "" && strings.HasSuffix(normalizedHost, "."+suffix)
	}
	if strings.HasPrefix(normalizedCandidate, ".") {
		suffix := strings.TrimPrefix(normalizedCandidate, ".")
		return suffix != "" && strings.HasSuffix(normalizedHost, "."+suffix)
	}
	return normalizedHost == normalizedCandidate
}

type LocalCodeInterpreterSessionStore interface {
	GetCodeInterpreterSession(ctx context.Context, id string) (domain.CodeInterpreterSession, error)
	ListCodeInterpreterSessions(ctx context.Context, query domain.ListCodeInterpreterSessionsQuery) (domain.CodeInterpreterSessionPage, error)
	SaveCodeInterpreterSession(ctx context.Context, session domain.CodeInterpreterSession) error
	TouchCodeInterpreterSession(ctx context.Context, id string, lastActiveAt string) error
	DeleteCodeInterpreterSession(ctx context.Context, id string) error
	GetCodeInterpreterContainerFile(ctx context.Context, containerID string, id string) (domain.CodeInterpreterContainerFile, error)
	GetCodeInterpreterContainerFileByPath(ctx context.Context, containerID string, containerPath string) (domain.CodeInterpreterContainerFile, error)
	ListCodeInterpreterContainerFiles(ctx context.Context, query domain.ListCodeInterpreterContainerFilesQuery) (domain.CodeInterpreterContainerFilePage, error)
	SaveCodeInterpreterContainerFile(ctx context.Context, file domain.CodeInterpreterContainerFile) (domain.CodeInterpreterContainerFile, error)
	DeleteCodeInterpreterContainerFile(ctx context.Context, containerID string, id string) error
	CountCodeInterpreterContainerFileBackingReferences(ctx context.Context, backingFileID string) (int, error)
}

type LocalCodeInterpreterFileStore interface {
	GetFile(ctx context.Context, id string) (domain.StoredFile, error)
	SaveFile(ctx context.Context, file domain.StoredFile) error
	DeleteFile(ctx context.Context, id string) error
}

type localCodeInterpreterConfig struct {
	IncludeOutputs bool
	Container      localCodeInterpreterContainerConfig
	ToolRequired   bool
}

type localCodeInterpreterContainerConfig struct {
	InputFileIDs []string
	MemoryLimit  string
	Mode         string
	SessionID    string
	Owner        string
}

type localCodeInterpreterInputFile struct {
	Content           []byte
	DeleteBackingFile bool
	FileID            string
	Filename          string
	WorkspaceName     string
}

type localCodeInterpreterGeneratedFile struct {
	Bytes           int64
	FileID          string
	BackingFileID   string
	Filename        string
	ContainerID     string
	ContainerPath   string
	ContainerSource string
}

type localCodeInterpreterExecutionResult struct {
	ContainerID    string
	GeneratedFiles []localCodeInterpreterGeneratedFile
	Logs           string
	ToolError      bool
}

type localCodeInterpreterExecutionFailure struct {
	ContainerID string
	Logs        string
	Message     string
}

type localCodeInterpreterAnnotationRange struct {
	Start int
	End   int
}

func (f *localCodeInterpreterExecutionFailure) Error() string {
	if f == nil {
		return ""
	}
	return strings.TrimSpace(f.Message)
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
	config.Container.Owner = strings.TrimSpace(AuthSubjectFromContext(ctx))

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

	inputFiles, err := h.resolveLocalCodeInterpreterInputFiles(ctx, prepared.NormalizedInput, config.Container.InputFileIDs)
	if err != nil {
		return domain.Response{}, err
	}
	planningFiles, err := h.resolveLocalCodeInterpreterPlanningFiles(ctx, prepared, config.Container, inputFiles)
	if err != nil {
		return domain.Response{}, err
	}

	planningContext, err := buildLocalCodeInterpreterPlanningContext(prepared, planningFiles)
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

	release, err := h.codeInterpreterGate.tryAcquire()
	if err != nil {
		return domain.Response{}, err
	}
	defer release()

	executionStart := time.Now()
	execution, err := h.executeLocalCodeInterpreter(ctx, prepared, config.Container, inputFiles, plan.Code)
	if err != nil {
		var execFailure *localCodeInterpreterExecutionFailure
		if errors.As(err, &execFailure) {
			if h.metrics != nil {
				h.metrics.IncCodeInterpreterRun("failed")
			}
			h.logger.WarnContext(ctx, "local code interpreter execution failed",
				"request_id", RequestIDFromContext(ctx),
				"container_id", execFailure.ContainerID,
				"duration_ms", time.Since(executionStart).Milliseconds(),
				"message", execFailure.Message,
			)
			return h.buildLocalCodeInterpreterFailedResponse(ctx, prepared, input, config, plan.Code, execFailure)
		}
		return domain.Response{}, err
	}

	generationContext, err := buildLocalCodeInterpreterExecutionContext(prepared, plan.Code, execution.Logs, execution.GeneratedFiles, execution.ToolError)
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

	messageItem, finalOutputText, err := buildLocalCodeInterpreterAssistantMessage(outputText, execution.ContainerID, execution.GeneratedFiles)
	if err != nil {
		return domain.Response{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}
	createdAt := domain.NowUTC().Unix()
	response := domain.NewResponse(responseID, input.Model, finalOutputText, input.PreviousResponseID, input.ConversationID, createdAt)

	codeInterpreterItem, err := buildLocalCodeInterpreterCallItem(plan.Code, execution.ContainerID, execution.Logs, execution.GeneratedFiles, config.IncludeOutputs, execution.ToolError)
	if err != nil {
		return domain.Response{}, err
	}
	response.Output = []domain.Item{codeInterpreterItem, messageItem}
	response.OutputText = finalOutputText

	response, err = h.service.FinalizeLocalResponse(input, generationContext, response)
	if err != nil {
		return domain.Response{}, err
	}

	outcome := "ok"
	if execution.ToolError {
		outcome = "tool_error"
	}
	if h.metrics != nil {
		h.metrics.IncCodeInterpreterRun(outcome)
	}
	h.logger.InfoContext(ctx, "local code interpreter execution",
		"request_id", RequestIDFromContext(ctx),
		"container_id", execution.ContainerID,
		"generated_files", len(execution.GeneratedFiles),
		"logs_bytes", len(execution.Logs),
		"tool_error", execution.ToolError,
		"duration_ms", time.Since(executionStart).Milliseconds(),
	)

	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func (h *responseHandler) buildLocalCodeInterpreterFailedResponse(ctx context.Context, prepared service.PreparedResponseContext, input service.CreateResponseInput, config localCodeInterpreterConfig, code string, failure *localCodeInterpreterExecutionFailure) (domain.Response, error) {
	generationContext, err := buildLocalCodeInterpreterExecutionContext(prepared, code, failure.Logs, nil, false)
	if err != nil {
		return domain.Response{}, err
	}
	if _, err := h.service.PrepareLocalResponseText(input, generationContext); err != nil {
		return domain.Response{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}
	errorPayload, err := marshalLocalCodeInterpreterResponseError(failure.Message)
	if err != nil {
		return domain.Response{}, err
	}
	codeInterpreterItem, err := buildLocalCodeInterpreterCallItemWithStatus(code, failure.ContainerID, failure.Logs, nil, config.IncludeOutputs, false, "failed")
	if err != nil {
		return domain.Response{}, err
	}

	response := domain.Response{
		ID:         responseID,
		Object:     "response",
		CreatedAt:  domain.NowUTC().Unix(),
		Status:     "failed",
		Error:      errorPayload,
		Model:      input.Model,
		Output:     []domain.Item{codeInterpreterItem},
		Metadata:   map[string]string{},
		OutputText: "",
	}

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
	container, err := parseLocalCodeInterpreterContainer(tool["container"])
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
		Container:      container,
		ToolRequired:   toolRequired,
	}, nil
}

func parseLocalCodeInterpreterContainer(value any) (localCodeInterpreterContainerConfig, error) {
	containerID := strings.TrimSpace(asString(value))
	if containerID != "" {
		if !strings.HasPrefix(containerID, "cntr_") {
			return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "code_interpreter.container must be container.type=auto or a cntr_* container id")
		}
		return localCodeInterpreterContainerConfig{
			Mode:      "explicit",
			SessionID: containerID,
		}, nil
	}

	container, ok := value.(map[string]any)
	if !ok || container == nil {
		return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "code_interpreter.container must be an object or container id string")
	}
	for key := range container {
		switch key {
		case "type", "file_ids", "memory_limit":
		default:
			return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "unsupported code_interpreter.container field "+`"`+key+`"`+" in shim-local mode")
		}
	}
	if !strings.EqualFold(strings.TrimSpace(asString(container["type"])), "auto") {
		return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "shim-local code_interpreter only supports container.type=auto or explicit cntr_* container ids")
	}

	rawFileIDs, ok := container["file_ids"]
	fileIDs := make([]string, 0)
	if ok && rawFileIDs != nil {
		values, ok := rawFileIDs.([]any)
		if !ok {
			return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "code_interpreter.container.file_ids must be an array of strings")
		}
		seen := make(map[string]struct{}, len(values))
		fileIDs = make([]string, 0, len(values))
		for _, value := range values {
			fileID := strings.TrimSpace(asString(value))
			if fileID == "" {
				return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "code_interpreter.container.file_ids must not contain empty values")
			}
			if _, ok := seen[fileID]; ok {
				continue
			}
			seen[fileID] = struct{}{}
			fileIDs = append(fileIDs, fileID)
		}
	}

	memoryLimit, err := normalizeLocalCodeInterpreterMemoryLimit(asString(container["memory_limit"]))
	if err != nil {
		return localCodeInterpreterContainerConfig{}, domain.NewValidationError("tools", "code_interpreter.container.memory_limit "+err.Error())
	}

	return localCodeInterpreterContainerConfig{
		InputFileIDs: fileIDs,
		MemoryLimit:  memoryLimit,
		Mode:         "auto",
	}, nil
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

	prefix, err := projectLocalCodeInterpreterTextGenerationContext(prefixItems)
	if err != nil {
		return nil, err
	}
	currentInput, err := projectLocalCodeInterpreterTextGenerationContext(prepared.NormalizedInput)
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
		"The code runs inside a shim-managed Docker container with no network access, a writable current working directory, and bounded local resource limits.",
		"Prefer concise non-interactive Python that prints the useful result to stdout.",
	}
	if len(inputFiles) == 0 {
		base = append(base, "Do not assume any uploaded files are available in the current working directory for this turn.")
		return strings.Join(base, " ")
	}

	var builder strings.Builder
	builder.WriteString(strings.Join(base, " "))
	builder.WriteString(" Prefer reading the uploaded files already placed in the current working directory using relative paths.")
	builder.WriteString(" Available uploaded files:")
	for _, inputFile := range inputFiles {
		builder.WriteString(" ")
		builder.WriteString(inputFile.WorkspaceName)
		if strings.TrimSpace(inputFile.FileID) != "" {
			builder.WriteString(" (file_id=")
			builder.WriteString(inputFile.FileID)
			builder.WriteString(")")
		}
	}
	builder.WriteString(" Avoid depending on container system files or paths outside the current working directory unless the task explicitly requires them.")
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
	return nil
}

func (h *responseHandler) localCodeInterpreterContainerManager() localCodeInterpreterContainerManager {
	return newLocalCodeInterpreterContainerManager(h.localCodeInterpreter, h.localCodeInterpreterFiles, h.localCodeInterpreterSessions)
}

func (h *responseHandler) executeLocalCodeInterpreter(ctx context.Context, prepared service.PreparedResponseContext, container localCodeInterpreterContainerConfig, inputFiles []localCodeInterpreterInputFile, code string) (localCodeInterpreterExecutionResult, error) {
	if !h.localCodeInterpreter.Enabled() {
		return localCodeInterpreterExecutionResult{}, localCodeInterpreterDisabledError()
	}

	manager := h.localCodeInterpreterContainerManager()
	owner := strings.TrimSpace(container.Owner)
	switch container.Mode {
	case "explicit":
		return localCodeInterpreterExecutionResult{}, domain.NewValidationError("tools", "explicit code_interpreter.container ids are disabled in shim-local mode")
	case "auto":
	default:
		return localCodeInterpreterExecutionResult{}, domain.NewValidationError("tools", "unsupported code_interpreter.container mode in shim-local path")
	}

	sessionID, canReuse, err := h.findReusableLocalCodeInterpreterSessionID(ctx, prepared, owner)
	if err != nil {
		return localCodeInterpreterExecutionResult{}, err
	}
	if canReuse {
		session, err := manager.ensureContainerSession(ctx, sessionID, owner)
		if err == nil {
			result, execErr := h.executeLocalCodeInterpreterSession(ctx, session.ID, inputFiles, code)
			if execErr == nil {
				if touchErr := h.localCodeInterpreterSessions.TouchCodeInterpreterSession(ctx, session.ID, domain.FormatTime(domain.NowUTC())); touchErr != nil {
					return localCodeInterpreterExecutionResult{}, touchErr
				}
				result.ContainerID = session.ID
				return result, nil
			}
			if errors.Is(execErr, sandbox.ErrDisabled) {
				return localCodeInterpreterExecutionResult{}, localCodeInterpreterDisabledError()
			}
			return localCodeInterpreterExecutionResult{}, newLocalCodeInterpreterExecutionFailure(session.ID, result.Logs, execErr)
		}
		var validationErr *domain.ValidationError
		if !errors.As(err, &validationErr) {
			if errors.Is(err, sandbox.ErrDisabled) {
				return localCodeInterpreterExecutionResult{}, localCodeInterpreterDisabledError()
			}
			return localCodeInterpreterExecutionResult{}, newLocalCodeInterpreterExecutionFailure(sessionID, "", err)
		}
	}

	session, err := manager.createContainer(ctx, owner, "Auto container", container.MemoryLimit, defaultLocalCodeInterpreterContainerExpiryMins)
	if err != nil {
		if errors.Is(err, sandbox.ErrDisabled) {
			return localCodeInterpreterExecutionResult{}, localCodeInterpreterDisabledError()
		}
		return localCodeInterpreterExecutionResult{}, newLocalCodeInterpreterExecutionFailure("", "", err)
	}
	result, err := h.executeLocalCodeInterpreterSession(ctx, session.ID, inputFiles, code)
	if err != nil {
		_ = manager.deleteContainer(ctx, session.ID, owner)
		if errors.Is(err, sandbox.ErrDisabled) {
			return localCodeInterpreterExecutionResult{}, localCodeInterpreterDisabledError()
		}
		return localCodeInterpreterExecutionResult{}, newLocalCodeInterpreterExecutionFailure(session.ID, result.Logs, err)
	}
	result.ContainerID = session.ID
	return result, nil
}

func newLocalCodeInterpreterExecutionFailure(containerID string, logs string, err error) error {
	if err == nil {
		return nil
	}
	var failure *localCodeInterpreterExecutionFailure
	if errors.As(err, &failure) {
		return failure
	}
	return &localCodeInterpreterExecutionFailure{
		ContainerID: strings.TrimSpace(containerID),
		Logs:        logs,
		Message:     localCodeInterpreterExecutionFailureMessage(err),
	}
}

func localCodeInterpreterExecutionFailureMessage(err error) string {
	switch {
	case err == nil:
		return "shim-local code_interpreter execution failed"
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "timed out"):
		return "shim-local code_interpreter execution timed out"
	case errors.Is(err, sandbox.ErrSessionNotFound):
		return "shim-local code_interpreter container runtime was unavailable"
	default:
		return "shim-local code_interpreter execution failed"
	}
}

func marshalLocalCodeInterpreterResponseError(message string) (json.RawMessage, error) {
	payload := map[string]any{
		"code":    "server_error",
		"message": strings.TrimSpace(message),
	}
	if payload["message"] == "" {
		payload["message"] = "shim-local code_interpreter execution failed"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (h *responseHandler) executeLocalCodeInterpreterSession(ctx context.Context, sessionID string, inputFiles []localCodeInterpreterInputFile, code string) (localCodeInterpreterExecutionResult, error) {
	manager := h.localCodeInterpreterContainerManager()
	if _, err := manager.stageInputFiles(ctx, sessionID, inputFiles); err != nil {
		return localCodeInterpreterExecutionResult{}, err
	}

	beforeFiles, err := h.localCodeInterpreter.Backend.ListFiles(ctx, sessionID)
	if err != nil {
		return localCodeInterpreterExecutionResult{}, err
	}

	execResult, err := h.localCodeInterpreter.Backend.ExecutePython(ctx, sandbox.ExecuteRequest{
		SessionID: sessionID,
		Code:      code,
	})
	toolError := sandbox.IsToolExecutionError(err)
	if err != nil && !toolError {
		return localCodeInterpreterExecutionResult{Logs: execResult.Logs}, err
	}

	afterFiles, err := h.localCodeInterpreter.Backend.ListFiles(ctx, sessionID)
	if err != nil {
		return localCodeInterpreterExecutionResult{Logs: execResult.Logs}, err
	}

	generatedFiles, err := manager.persistGeneratedFiles(ctx, sessionID, diffLocalCodeInterpreterGeneratedFiles(beforeFiles, afterFiles))
	if err != nil {
		return localCodeInterpreterExecutionResult{Logs: execResult.Logs}, err
	}

	return localCodeInterpreterExecutionResult{
		GeneratedFiles: generatedFiles,
		Logs:           execResult.Logs,
		ToolError:      toolError,
	}, nil
}

func (h *responseHandler) findReusableLocalCodeInterpreterSessionID(ctx context.Context, prepared service.PreparedResponseContext, owner string) (string, bool, error) {
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
		if owner != "" && strings.TrimSpace(session.Owner) != strings.TrimSpace(owner) {
			return "", false, nil
		}
		if session.Status != "running" {
			return "", false, nil
		}
		return session.ID, true, nil
	}
	return "", false, nil
}

func (h *responseHandler) resolveLocalCodeInterpreterPlanningFiles(ctx context.Context, prepared service.PreparedResponseContext, container localCodeInterpreterContainerConfig, current []localCodeInterpreterInputFile) ([]localCodeInterpreterInputFile, error) {
	files := append([]localCodeInterpreterInputFile(nil), current...)
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		seen[strings.ToLower(strings.TrimSpace(file.WorkspaceName))] = struct{}{}
	}

	var (
		containerID string
		canInspect  bool
		err         error
	)
	switch container.Mode {
	case "explicit":
		containerID = strings.TrimSpace(container.SessionID)
		canInspect = containerID != ""
	case "auto":
		containerID, canInspect, err = h.findReusableLocalCodeInterpreterSessionID(ctx, prepared, container.Owner)
		if err != nil {
			return nil, err
		}
	}
	if !canInspect || containerID == "" {
		return files, nil
	}

	manager := h.localCodeInterpreterContainerManager()
	session, err := manager.getContainer(ctx, containerID, false, strings.TrimSpace(container.Owner))
	if err != nil {
		if errors.Is(err, sqlite.ErrNotFound) {
			return files, nil
		}
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			return files, nil
		}
		return nil, err
	}
	if session.Status != "running" {
		return files, nil
	}

	page, err := h.localCodeInterpreterSessions.ListCodeInterpreterContainerFiles(ctx, domain.ListCodeInterpreterContainerFilesQuery{
		ContainerID: containerID,
		Limit:       maxLocalCodeInterpreterContainerFilesListLimit,
		Order:       domain.ListOrderAsc,
	})
	if err != nil {
		return nil, err
	}
	for _, file := range page.Files {
		workspaceName := path.Base(strings.TrimSpace(file.Path))
		if workspaceName == "" {
			continue
		}
		key := strings.ToLower(workspaceName)
		if _, ok := seen[key]; ok {
			continue
		}
		files = append(files, localCodeInterpreterInputFile{
			FileID:        file.BackingFileID,
			Filename:      workspaceName,
			WorkspaceName: workspaceName,
		})
		seen[key] = struct{}{}
	}
	return files, nil
}

func (h *responseHandler) resolveLocalCodeInterpreterInputFiles(ctx context.Context, items []domain.Item, fileIDs []string) ([]localCodeInterpreterInputFile, error) {
	if len(fileIDs) == 0 && len(items) == 0 {
		return nil, nil
	}
	if h.localCodeInterpreterFiles == nil {
		return nil, fmt.Errorf("local code interpreter file store is not configured")
	}

	usedNames := make(map[string]int, len(fileIDs))
	files := make([]localCodeInterpreterInputFile, 0, len(fileIDs))
	seenStoredFileIDs := make(map[string]struct{}, len(fileIDs))
	for _, fileID := range fileIDs {
		inputFile, err := h.resolveLocalCodeInterpreterStoredInputFile(ctx, fileID, usedNames, "tools")
		if err != nil {
			return nil, err
		}
		files = append(files, inputFile)
		seenStoredFileIDs[fileID] = struct{}{}
	}

	for _, item := range items {
		autoInputFiles, err := h.extractLocalCodeInterpreterAutomaticInputFiles(ctx, item, usedNames, seenStoredFileIDs)
		if err != nil {
			return nil, err
		}
		files = append(files, autoInputFiles...)
	}
	return files, nil
}

func (h *responseHandler) resolveLocalCodeInterpreterStoredInputFile(ctx context.Context, fileID string, usedNames map[string]int, field string) (localCodeInterpreterInputFile, error) {
	file, err := h.localCodeInterpreterFiles.GetFile(ctx, fileID)
	if err != nil {
		if errors.Is(err, sqlite.ErrNotFound) {
			if field == "input" {
				return localCodeInterpreterInputFile{}, domain.NewValidationError(field, "unknown input_file.file_id value "+`"`+fileID+`"`+" in shim-local code_interpreter mode")
			}
			return localCodeInterpreterInputFile{}, domain.NewValidationError(field, "unknown code_interpreter.container.file_ids value "+`"`+fileID+`"`)
		}
		return localCodeInterpreterInputFile{}, err
	}
	return localCodeInterpreterInputFile{
		Content:       file.Content,
		FileID:        file.ID,
		Filename:      file.Filename,
		WorkspaceName: uniqueLocalCodeInterpreterWorkspaceName(file.Filename, file.ID, usedNames),
	}, nil
}

func (h *responseHandler) extractLocalCodeInterpreterAutomaticInputFiles(ctx context.Context, item domain.Item, usedNames map[string]int, seenStoredFileIDs map[string]struct{}) ([]localCodeInterpreterInputFile, error) {
	if strings.TrimSpace(item.Type) != "message" {
		return nil, nil
	}

	content, ok := item.Map()["content"].([]any)
	if !ok || len(content) == 0 {
		return nil, nil
	}

	files := make([]localCodeInterpreterInputFile, 0, len(content))
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(part["type"])) != "input_file" {
			continue
		}

		switch {
		case strings.TrimSpace(asString(part["file_id"])) != "":
			fileID := strings.TrimSpace(asString(part["file_id"]))
			if _, seen := seenStoredFileIDs[fileID]; seen {
				continue
			}
			inputFile, err := h.resolveLocalCodeInterpreterStoredInputFile(ctx, fileID, usedNames, "input")
			if err != nil {
				return nil, err
			}
			files = append(files, inputFile)
			seenStoredFileIDs[fileID] = struct{}{}
		case strings.TrimSpace(asString(part["file_data"])) != "":
			filename := strings.TrimSpace(asString(part["filename"]))
			if filename == "" {
				return nil, domain.NewValidationError("input", "input_file.file_data requires filename in shim-local code_interpreter mode")
			}
			content, err := decodeLocalCodeInterpreterInlineFileData(asString(part["file_data"]))
			if err != nil {
				return nil, domain.NewValidationError("input", "input_file.file_data must be valid base64 in shim-local code_interpreter mode")
			}
			files = append(files, localCodeInterpreterInputFile{
				Content:       content,
				Filename:      filename,
				WorkspaceName: uniqueLocalCodeInterpreterWorkspaceName(filename, "inline_file", usedNames),
			})
		case strings.TrimSpace(asString(part["file_url"])) != "":
			inputFile, err := h.resolveLocalCodeInterpreterRemoteInputFile(ctx, asString(part["file_url"]), asString(part["filename"]), usedNames)
			if err != nil {
				return nil, err
			}
			files = append(files, inputFile)
		default:
			return nil, domain.NewValidationError("input", "input_file must include file_id, file_data, or file_url")
		}
	}

	return files, nil
}

func (h *responseHandler) resolveLocalCodeInterpreterRemoteInputFile(ctx context.Context, fileURL string, filename string, usedNames map[string]int) (localCodeInterpreterInputFile, error) {
	parsedURL, err := neturl.Parse(strings.TrimSpace(fileURL))
	if err != nil || parsedURL == nil {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "input_file.file_url must be a valid absolute URL in shim-local code_interpreter mode")
	}
	switch strings.ToLower(strings.TrimSpace(parsedURL.Scheme)) {
	case "http", "https":
	default:
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "shim-local code_interpreter only supports http(s) input_file.file_url values")
	}
	if strings.TrimSpace(parsedURL.Host) == "" {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "input_file.file_url must include a host in shim-local code_interpreter mode")
	}
	if err := h.localCodeInterpreter.allowsRemoteInputFileURL(parsedURL); err != nil {
		return localCodeInterpreterInputFile{}, err
	}

	client := http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := h.localCodeInterpreter.allowsRemoteInputFileURL(req.URL); err != nil {
				return err
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "input_file.file_url could not be fetched in shim-local code_interpreter mode")
	}
	resp, err := client.Do(req)
	if err != nil {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "input_file.file_url could not be fetched in shim-local code_interpreter mode")
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", fmt.Sprintf("input_file.file_url returned HTTP %d in shim-local code_interpreter mode", resp.StatusCode))
	}

	limits := h.localCodeInterpreter.normalizedLimits()
	content, err := io.ReadAll(io.LimitReader(resp.Body, int64(limits.RemoteInputFileBytes+1)))
	if err != nil {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "input_file.file_url could not be read in shim-local code_interpreter mode")
	}
	if len(content) > limits.RemoteInputFileBytes {
		return localCodeInterpreterInputFile{}, domain.NewValidationError("input", "input_file.file_url exceeds the configured shim-local code_interpreter size limit")
	}

	resolvedFilename := strings.TrimSpace(filename)
	if resolvedFilename == "" {
		resolvedFilename = path.Base(parsedURL.Path)
	}
	resolvedFilename = sanitizeLocalCodeInterpreterWorkspaceName(resolvedFilename, "remote_input_file")

	return localCodeInterpreterInputFile{
		Content:       content,
		Filename:      resolvedFilename,
		WorkspaceName: uniqueLocalCodeInterpreterWorkspaceName(resolvedFilename, "remote_input_file", usedNames),
	}, nil
}

func decodeLocalCodeInterpreterInlineFileData(value string) ([]byte, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" {
		return nil, fmt.Errorf("empty file_data")
	}
	if strings.HasPrefix(strings.ToLower(encoded), "data:") {
		parts := strings.SplitN(encoded, ",", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid data url")
		}
		encoded = parts[1]
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return decoded, nil
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

func diffLocalCodeInterpreterGeneratedFiles(before []sandbox.SessionFile, after []sandbox.SessionFile) []sandbox.SessionFile {
	if len(after) == 0 {
		return nil
	}

	beforeByName := make(map[string][]byte, len(before))
	for _, file := range before {
		beforeByName[file.Name] = file.Content
	}

	generated := make([]sandbox.SessionFile, 0, len(after))
	for _, file := range after {
		if content, ok := beforeByName[file.Name]; ok && bytes.Equal(content, file.Content) {
			continue
		}
		generated = append(generated, sandbox.SessionFile{
			Name:    file.Name,
			Content: append([]byte(nil), file.Content...),
		})
	}
	return generated
}

func (h *responseHandler) persistLocalCodeInterpreterGeneratedFiles(ctx context.Context, generated []sandbox.SessionFile) ([]localCodeInterpreterGeneratedFile, error) {
	if len(generated) == 0 {
		return nil, nil
	}
	if h.localCodeInterpreterFiles == nil {
		return nil, fmt.Errorf("local code interpreter file store is not configured")
	}

	limits := h.localCodeInterpreter.normalizedLimits()
	saved := make([]localCodeInterpreterGeneratedFile, 0, min(len(generated), limits.GeneratedFiles))
	totalBytes := 0
	now := domain.NowUTC().Unix()
	for _, file := range generated {
		if len(saved) >= limits.GeneratedFiles {
			break
		}
		if len(file.Content) > limits.GeneratedFileBytes {
			continue
		}
		if totalBytes+len(file.Content) > limits.GeneratedTotalBytes {
			continue
		}

		fileID, err := domain.NewPrefixedID("file")
		if err != nil {
			return nil, err
		}

		storedFile := domain.StoredFile{
			ID:        fileID,
			Filename:  file.Name,
			Purpose:   "assistants",
			Bytes:     int64(len(file.Content)),
			CreatedAt: now,
			Status:    "processed",
			Content:   append([]byte(nil), file.Content...),
		}
		if err := h.localCodeInterpreterFiles.SaveFile(ctx, storedFile); err != nil {
			return nil, err
		}

		totalBytes += len(file.Content)
		saved = append(saved, localCodeInterpreterGeneratedFile{
			Bytes:    storedFile.Bytes,
			FileID:   storedFile.ID,
			Filename: storedFile.Filename,
		})
	}

	return saved, nil
}

func buildLocalCodeInterpreterExecutionContext(prepared service.PreparedResponseContext, code string, logs string, generatedFiles []localCodeInterpreterGeneratedFile, toolError bool) ([]domain.Item, error) {
	prefixItems := prepared.ContextItems
	if len(prepared.NormalizedInput) <= len(prefixItems) {
		prefixItems = prefixItems[:len(prefixItems)-len(prepared.NormalizedInput)]
	}

	prefix, err := projectLocalCodeInterpreterTextGenerationContext(prefixItems)
	if err != nil {
		return nil, err
	}
	currentInput, err := projectLocalCodeInterpreterTextGenerationContext(prepared.NormalizedInput)
	if err != nil {
		return nil, err
	}

	executionPrompt := domain.NewInputTextMessage("system", buildLocalCodeInterpreterExecutionPrompt(code, logs, generatedFiles, toolError))
	out := make([]domain.Item, 0, len(prefix)+len(currentInput)+1)
	out = append(out, prefix...)
	out = append(out, executionPrompt)
	out = append(out, currentInput...)
	return out, nil
}

func buildLocalCodeInterpreterExecutionPrompt(code string, logs string, generatedFiles []localCodeInterpreterGeneratedFile, toolError bool) string {
	var builder strings.Builder
	builder.WriteString("A shim-local code interpreter already ran for this turn.\n")
	builder.WriteString("Use only the execution result below as the tool output.\n")
	if toolError {
		builder.WriteString("The code interpreter run ended with a runtime/tool error.\n")
		builder.WriteString("Explain the failure plainly using the execution result below.\n")
	} else {
		builder.WriteString("If the execution result does not answer the request, say so plainly.\n")
	}
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
	if len(generatedFiles) == 0 {
		builder.WriteString("Generated files: (none)\n")
		return builder.String()
	}

	builder.WriteString("Generated files saved by the shim and available via /v1/containers/{container_id}/files/{file_id}/content:\n")
	builder.WriteString("If you mention a generated file in the answer, use its exact filename verbatim.\n")
	for _, generatedFile := range generatedFiles {
		builder.WriteString("- ")
		builder.WriteString(generatedFile.Filename)
		builder.WriteString(" (file_id=")
		builder.WriteString(generatedFile.FileID)
		builder.WriteString(", bytes=")
		builder.WriteString(fmt.Sprintf("%d", generatedFile.Bytes))
		builder.WriteString(")\n")
	}
	return builder.String()
}

func projectLocalCodeInterpreterTextGenerationContext(items []domain.Item) ([]domain.Item, error) {
	out := make([]domain.Item, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Type) != "message" {
			continue
		}
		switch item.Role {
		case "system", "developer", "user", "assistant":
		default:
			return nil, domain.ErrUnsupportedShape
		}
		text := domain.MessageText(item)
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, domain.NewInputTextMessage(item.Role, text))
	}
	return out, nil
}

func buildLocalCodeInterpreterAssistantMessage(text string, containerID string, generatedFiles []localCodeInterpreterGeneratedFile) (domain.Item, string, error) {
	finalText, annotations := buildLocalCodeInterpreterAssistantTextAnnotations(text, containerID, generatedFiles)
	item, err := buildCompletedAssistantMessageWithAnnotations(finalText, annotations)
	if err != nil {
		return domain.Item{}, "", err
	}
	return item, finalText, nil
}

func buildLocalCodeInterpreterAssistantTextAnnotations(text string, containerID string, generatedFiles []localCodeInterpreterGeneratedFile) (string, []any) {
	if len(generatedFiles) == 0 || strings.TrimSpace(containerID) == "" {
		return text, nil
	}

	annotations := make([]any, 0, len(generatedFiles))
	usedRanges := make([]localCodeInterpreterAnnotationRange, 0, len(generatedFiles))
	missing := make([]localCodeInterpreterGeneratedFile, 0, len(generatedFiles))
	for _, generatedFile := range generatedFiles {
		startIndex, endIndex, ok := localCodeInterpreterFilenameMentionRange(text, generatedFile.Filename, usedRanges)
		if !ok {
			missing = append(missing, generatedFile)
			continue
		}
		annotations = append(annotations, map[string]any{
			"type":         "container_file_citation",
			"container_id": containerID,
			"file_id":      generatedFile.FileID,
			"filename":     generatedFile.Filename,
			"start_index":  startIndex,
			"end_index":    endIndex,
		})
		usedRanges = append(usedRanges, localCodeInterpreterAnnotationRange{Start: startIndex, End: endIndex})
	}

	if len(missing) == 0 {
		return text, annotations
	}

	var (
		builder   strings.Builder
		runeIndex = 0
	)
	appendText := func(value string) {
		builder.WriteString(value)
		runeIndex += utf8.RuneCountInString(value)
	}

	appendText(text)
	if strings.TrimSpace(text) != "" {
		switch {
		case strings.HasSuffix(text, "\n\n"):
		case strings.HasSuffix(text, "\n"):
			appendText("\n")
		default:
			appendText("\n\n")
		}
	}
	appendText("Generated files:\n")

	for index, generatedFile := range missing {
		appendText("- ")
		startIndex := runeIndex
		appendText(generatedFile.Filename)
		endIndex := runeIndex
		annotations = append(annotations, map[string]any{
			"type":         "container_file_citation",
			"container_id": containerID,
			"file_id":      generatedFile.FileID,
			"filename":     generatedFile.Filename,
			"start_index":  startIndex,
			"end_index":    endIndex,
		})
		if index+1 < len(missing) {
			appendText("\n")
		}
	}
	return builder.String(), annotations
}

func localCodeInterpreterFilenameMentionRange(text string, filename string, used []localCodeInterpreterAnnotationRange) (int, int, bool) {
	trimmedText := strings.TrimSpace(text)
	trimmedFilename := strings.TrimSpace(filename)
	if trimmedText == "" || trimmedFilename == "" {
		return 0, 0, false
	}

	lowerText := strings.ToLower(text)
	lowerFilename := strings.ToLower(trimmedFilename)
	searchOffset := 0
	for {
		idx := strings.Index(lowerText[searchOffset:], lowerFilename)
		if idx < 0 {
			return 0, 0, false
		}
		startByte := searchOffset + idx
		endByte := startByte + len(lowerFilename)
		startRune := utf8.RuneCountInString(text[:startByte])
		endRune := startRune + utf8.RuneCountInString(text[startByte:endByte])
		if !localCodeInterpreterAnnotationRangeOverlaps(startRune, endRune, used) {
			return startRune, endRune, true
		}
		searchOffset = endByte
	}
}

func localCodeInterpreterAnnotationRangeOverlaps(start int, end int, used []localCodeInterpreterAnnotationRange) bool {
	for _, candidate := range used {
		if start < candidate.End && end > candidate.Start {
			return true
		}
	}
	return false
}

func buildLocalCodeInterpreterCallItem(code string, containerID string, logs string, generatedFiles []localCodeInterpreterGeneratedFile, includeOutputs bool, suppressLogOutputs bool) (domain.Item, error) {
	return buildLocalCodeInterpreterCallItemWithStatus(code, containerID, logs, generatedFiles, includeOutputs, suppressLogOutputs, "completed")
}

func buildLocalCodeInterpreterCallItemWithStatus(code string, containerID string, logs string, generatedFiles []localCodeInterpreterGeneratedFile, includeOutputs bool, suppressLogOutputs bool, status string) (domain.Item, error) {
	normalizedStatus := strings.TrimSpace(status)
	if normalizedStatus == "" {
		normalizedStatus = "completed"
	}
	payload := map[string]any{
		"type":    "code_interpreter_call",
		"status":  normalizedStatus,
		"code":    code,
		"outputs": nil,
	}
	if strings.TrimSpace(containerID) != "" {
		payload["container_id"] = strings.TrimSpace(containerID)
	}
	if includeOutputs {
		outputs := make([]map[string]any, 0, 1)
		if logs != "" && !suppressLogOutputs {
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
	return domain.NewValidationError("tools", "shim-local code_interpreter execution is disabled; set responses.code_interpreter.backend to docker")
}
