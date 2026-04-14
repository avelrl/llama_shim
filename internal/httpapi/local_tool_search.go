package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/service"
)

const localToolSearchMaxLoadedPaths = 3
const localToolSearchMaxNamespaceTools = 3

var shimLocalToolSearchFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
}

type localToolSearchConfig struct {
	SearchTool      map[string]any
	SearchablePaths []localToolSearchPath
	LoadedToolNames map[string]string
}

type localToolSearchPath struct {
	Path            string
	Kind            string
	SearchText      string
	OutputTool      map[string]any
	LoadedFunctions []map[string]any
}

func hasLocalToolSearchRequest(rawFields map[string]json.RawMessage) bool {
	for _, tool := range decodeToolList(rawFields) {
		if strings.EqualFold(strings.TrimSpace(asString(tool["type"])), "tool_search") {
			return true
		}
	}
	return false
}

func supportsLocalToolSearch(rawFields map[string]json.RawMessage) bool {
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		if _, ok := shimLocalToolSearchFields[key]; ok {
			continue
		}
		return false
	}

	_, err := parseLocalToolSearchConfig(rawFields)
	return err == nil
}

func (h *responseHandler) createLocalToolSearchResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	config, err := parseLocalToolSearchConfig(rawFields)
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

	query, err := deriveLocalFileSearchQuery(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	searchQueries := retrieval.PlanFileSearchQueries(query)
	if len(searchQueries) == 0 {
		searchQueries = []string{strings.TrimSpace(query)}
	}

	selected := selectLocalToolSearchPaths(config.SearchablePaths, searchQueries)
	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}

	searchCallItem, err := buildLocalToolSearchCallItem(responseID, searchQueries, selected)
	if err != nil {
		return domain.Response{}, err
	}
	searchOutputItem, loadedFunctions, err := buildLocalToolSearchOutputItem(responseID, selected, config.LoadedToolNames)
	if err != nil {
		return domain.Response{}, err
	}

	response := domain.Response{
		ID:                 responseID,
		Object:             "response",
		Model:              input.Model,
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       domain.NewConversationReference(input.ConversationID),
		Output:             []domain.Item{searchCallItem, searchOutputItem},
		OutputText:         "",
	}

	loopRawFields, err := buildLocalToolSearchLoadedToolRawFields(rawFields, loadedFunctions)
	if err != nil {
		return domain.Response{}, err
	}
	loopResponse, err := h.runPreparedLocalToolLoopResponse(ctx, input, prepared, loopRawFields)
	if err != nil {
		return domain.Response{}, err
	}
	loopOutput, err := annotateLocalToolSearchNamespaces(loopResponse.Output, config.LoadedToolNames)
	if err != nil {
		return domain.Response{}, err
	}
	response.Output = append(response.Output, loopOutput...)
	response.OutputText = loopResponse.OutputText

	response, err = h.service.FinalizeLocalResponse(input, prepared.ContextItems, response)
	if err != nil {
		return domain.Response{}, err
	}
	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func parseLocalToolSearchConfig(rawFields map[string]json.RawMessage) (localToolSearchConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) == 0 {
		return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search requires one tool_search tool plus deferred functions or namespaces")
	}
	if err := validateLocalToolSearchToolChoice(rawFields["tool_choice"]); err != nil {
		return localToolSearchConfig{}, err
	}
	if err := validateLocalToolSearchParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return localToolSearchConfig{}, err
	}
	var searchToolCount int
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(asString(tool["type"])), "tool_search") {
			continue
		}
		searchToolCount++
		if searchToolCount > 1 {
			return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search supports exactly one tool_search tool")
		}
		if strings.EqualFold(strings.TrimSpace(asString(tool["execution"])), "client") {
			return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search only supports hosted/server execution; client execution remains proxy-only")
		}
	}
	if len(tools) < 2 {
		return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search requires one tool_search tool plus deferred functions or namespaces")
	}

	var (
		searchTool      map[string]any
		searchablePaths []localToolSearchPath
		loadedToolNames = make(map[string]string)
	)
	for _, tool := range tools {
		switch strings.TrimSpace(asString(tool["type"])) {
		case "tool_search":
			if searchTool != nil {
				return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search supports exactly one tool_search tool")
			}
			if strings.EqualFold(strings.TrimSpace(asString(tool["execution"])), "client") {
				return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search only supports hosted/server execution; client execution remains proxy-only")
			}
			for key := range tool {
				switch key {
				case "type", "description", "execution":
				default:
					return localToolSearchConfig{}, domain.NewValidationError("tools", "unsupported tool_search field "+`"`+key+`"`+" in shim-local mode")
				}
			}
			searchTool = cloneAnyMap(tool)
		case "function":
			if !toolBoolField(tool, "defer_loading") {
				return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search currently supports only defer_loading function tools")
			}
			functionPath, err := buildLocalToolSearchFunctionPath(tool)
			if err != nil {
				return localToolSearchConfig{}, err
			}
			searchablePaths = append(searchablePaths, functionPath)
			loadedToolNames[strings.TrimSpace(asString(functionPath.LoadedFunctions[0]["name"]))] = ""
		case "namespace":
			namespacePath, err := buildLocalToolSearchNamespacePath(tool)
			if err != nil {
				return localToolSearchConfig{}, err
			}
			searchablePaths = append(searchablePaths, namespacePath)
			for _, loaded := range namespacePath.LoadedFunctions {
				name := strings.TrimSpace(asString(loaded["name"]))
				if name == "" {
					continue
				}
				if existing, ok := loadedToolNames[name]; ok && existing != namespacePath.Path {
					return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search does not support duplicate deferred function names across namespaces")
				}
				loadedToolNames[name] = namespacePath.Path
			}
		default:
			return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search currently supports only defer_loading functions and namespaces")
		}
	}

	if searchTool == nil {
		return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search requires tools[0..n] to include a tool_search tool")
	}
	if len(searchablePaths) == 0 {
		return localToolSearchConfig{}, domain.NewValidationError("tools", "shim-local tool_search requires at least one searchable defer_loading function or namespace")
	}
	return localToolSearchConfig{
		SearchTool:      searchTool,
		SearchablePaths: searchablePaths,
		LoadedToolNames: loadedToolNames,
	}, nil
}

func validateLocalToolSearchToolChoice(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var choice string
	if err := json.Unmarshal(trimmed, &choice); err == nil {
		switch strings.TrimSpace(choice) {
		case "auto", "required":
			return nil
		default:
			return domain.NewValidationError("tool_choice", "shim-local tool_search supports tool_choice=auto or required")
		}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local tool_search")
	}
	if len(payload) != 1 {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local tool_search")
	}
	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local tool_search")
	}
	if !strings.EqualFold(strings.TrimSpace(choiceType), "tool_search") {
		return domain.NewValidationError("tool_choice", "shim-local tool_search only supports tool_choice targeting tool_search")
	}
	return nil
}

func validateLocalToolSearchParallelToolCalls(raw json.RawMessage) error {
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

func buildLocalToolSearchFunctionPath(tool map[string]any) (localToolSearchPath, error) {
	name := strings.TrimSpace(asString(tool["name"]))
	if name == "" {
		return localToolSearchPath{}, domain.NewValidationError("tools", "defer_loading function tool name is required")
	}
	outputTool := cloneAnyMap(tool)
	delete(outputTool, "namespace")
	searchText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		name,
		asString(outputTool["description"]),
	}, " ")))
	return localToolSearchPath{
		Path:            name,
		Kind:            "function",
		SearchText:      searchText,
		OutputTool:      outputTool,
		LoadedFunctions: []map[string]any{outputTool},
	}, nil
}

func buildLocalToolSearchNamespacePath(tool map[string]any) (localToolSearchPath, error) {
	name := strings.TrimSpace(asString(tool["name"]))
	if name == "" {
		return localToolSearchPath{}, domain.NewValidationError("tools", "namespace tool name is required")
	}
	rawNested, ok := tool["tools"].([]any)
	if !ok || len(rawNested) == 0 {
		return localToolSearchPath{}, domain.NewValidationError("tools", "namespace tools must be a non-empty array in shim-local tool_search mode")
	}

	loaded := make([]map[string]any, 0, len(rawNested))
	searchParts := []string{name, asString(tool["description"])}
	for _, rawTool := range rawNested {
		nested, ok := rawTool.(map[string]any)
		if !ok {
			return localToolSearchPath{}, domain.NewValidationError("tools", "namespace tools must contain objects")
		}
		if strings.TrimSpace(asString(nested["type"])) != "function" {
			return localToolSearchPath{}, domain.NewValidationError("tools", "shim-local tool_search namespaces currently support only function tools")
		}
		if !toolBoolField(nested, "defer_loading") {
			return localToolSearchPath{}, domain.NewValidationError("tools", "shim-local tool_search namespaces currently support only defer_loading functions")
		}
		functionName := strings.TrimSpace(asString(nested["name"]))
		if functionName == "" {
			return localToolSearchPath{}, domain.NewValidationError("tools", "namespace function tool name is required")
		}
		cloned := cloneAnyMap(nested)
		loaded = append(loaded, cloned)
		searchParts = append(searchParts, functionName, asString(cloned["description"]))
	}

	outputTool := cloneAnyMap(tool)
	outputTool["tools"] = loaded
	return localToolSearchPath{
		Path:            name,
		Kind:            "namespace",
		SearchText:      strings.ToLower(strings.TrimSpace(strings.Join(searchParts, " "))),
		OutputTool:      outputTool,
		LoadedFunctions: loaded,
	}, nil
}

func selectLocalToolSearchPaths(paths []localToolSearchPath, queries []string) []localToolSearchPath {
	type scoredPath struct {
		path  localToolSearchPath
		score int
	}
	scored := make([]scoredPath, 0, len(paths))
	rewrittenQueries := retrieval.RewriteSearchQueries(queries)
	if len(rewrittenQueries) == 0 {
		rewrittenQueries = queries
	}
	for _, path := range paths {
		score := scoreLocalToolSearchPath(path, rewrittenQueries)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredPath{path: path, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].path.Path < scored[j].path.Path
	})
	if len(scored) > localToolSearchMaxLoadedPaths {
		scored = scored[:localToolSearchMaxLoadedPaths]
	}
	out := make([]localToolSearchPath, 0, len(scored))
	for _, candidate := range scored {
		out = append(out, candidate.path)
	}
	return out
}

func scoreLocalToolSearchPath(path localToolSearchPath, queries []string) int {
	score := 0
	loweredPath := strings.ToLower(strings.TrimSpace(path.Path))
	for _, query := range queries {
		normalized := strings.ToLower(strings.TrimSpace(query))
		if normalized == "" {
			continue
		}
		if normalized == loweredPath {
			score += 12
		}
		if strings.Contains(path.SearchText, normalized) {
			score += 8
		}
		for _, token := range strings.Fields(normalized) {
			switch {
			case token == loweredPath:
				score += 6
			case strings.Contains(path.SearchText, token):
				score += 2
			}
		}
	}
	return score
}

func buildLocalToolSearchCallItem(responseID string, searchQueries []string, selected []localToolSearchPath) (domain.Item, error) {
	paths := make([]string, 0, len(selected))
	for _, path := range selected {
		paths = append(paths, path.Path)
	}
	raw, err := json.Marshal(map[string]any{
		"id":        "tsc_" + responseID,
		"type":      "tool_search_call",
		"execution": "server",
		"call_id":   nil,
		"status":    "completed",
		"arguments": map[string]any{
			"paths":   paths,
			"queries": append([]string(nil), searchQueries...),
		},
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func buildLocalToolSearchOutputItem(responseID string, selected []localToolSearchPath, namespaceByToolName map[string]string) (domain.Item, []map[string]any, error) {
	outputTools := make([]map[string]any, 0, len(selected))
	loadedFunctions := make([]map[string]any, 0, len(selected))
	for _, path := range selected {
		outputTool := cloneAnyMap(path.OutputTool)
		if strings.TrimSpace(asString(outputTool["type"])) == "namespace" {
			nested := make([]map[string]any, 0, minInt(len(path.LoadedFunctions), localToolSearchMaxNamespaceTools))
			for _, functionTool := range path.LoadedFunctions {
				nested = append(nested, cloneAnyMap(functionTool))
				if len(nested) >= localToolSearchMaxNamespaceTools {
					break
				}
			}
			outputTool["tools"] = nested
		}
		outputTools = append(outputTools, outputTool)
		for _, functionTool := range path.LoadedFunctions {
			cloned := cloneAnyMap(functionTool)
			delete(cloned, "defer_loading")
			if namespace := strings.TrimSpace(namespaceByToolName[strings.TrimSpace(asString(cloned["name"]))]); namespace != "" {
				cloned["namespace"] = namespace
			}
			loadedFunctions = append(loadedFunctions, cloned)
		}
	}

	raw, err := json.Marshal(map[string]any{
		"id":        "tso_" + responseID,
		"type":      "tool_search_output",
		"execution": "server",
		"call_id":   nil,
		"status":    "completed",
		"tools":     outputTools,
	})
	if err != nil {
		return domain.Item{}, nil, err
	}
	item, err := domain.NewItem(raw)
	if err != nil {
		return domain.Item{}, nil, err
	}
	return item, loadedFunctions, nil
}

func buildLocalToolSearchLoadedToolRawFields(rawFields map[string]json.RawMessage, loadedFunctions []map[string]any) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(rawFields))
	for key, value := range rawFields {
		out[key] = value
	}
	toolsRaw, err := json.Marshal(loadedFunctions)
	if err != nil {
		return nil, err
	}
	out["tools"] = toolsRaw
	if rawChoice, ok := out["tool_choice"]; ok {
		if shouldRewriteLocalToolSearchToolChoice(rawChoice, len(loadedFunctions) > 0) {
			out["tool_choice"] = json.RawMessage(`"auto"`)
		}
	}
	return out, nil
}

func shouldRewriteLocalToolSearchToolChoice(raw json.RawMessage, hasLoadedTools bool) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	var literal string
	if err := json.Unmarshal(trimmed, &literal); err == nil {
		return strings.EqualFold(strings.TrimSpace(literal), "required") && !hasLoadedTools
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return false
	}
	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(choiceType), "tool_search")
}

func (h *responseHandler) runPreparedLocalToolLoopResponse(ctx context.Context, input service.CreateResponseInput, prepared service.PreparedResponseContext, rawFields map[string]json.RawMessage) (domain.Response, error) {
	if _, err := h.service.PrepareLocalResponseText(input, prepared.ContextItems); err != nil {
		return domain.Response{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.Response{}, fmt.Errorf("generate response id: %w", err)
	}

	if response, handled, err := h.tryRunPreparedLocalConstrainedCustomToolResponse(ctx, input, prepared, rawFields, responseID); handled {
		if err == nil {
			return response, nil
		}
	}

	repairPrompt := ""
	for attempt := 1; ; attempt++ {
		chatBody, plan, err := buildLocalToolLoopChatCompletionBody(rawFields, prepared.ContextItems, prepared.NormalizedInput, prepared.ToolCallRefs, h.customToolsMode, h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired, repairPrompt)
		if err != nil {
			return domain.Response{}, err
		}

		rawResponse, err := h.proxy.client.CreateChatCompletion(ctx, chatBody)
		if err != nil {
			return domain.Response{}, err
		}

		response, err := parseLocalToolLoopChatCompletion(rawResponse, responseID, input.Model, input.PreviousResponseID, input.ConversationID, plan)
		if err == nil {
			if err := enforceToolChoiceContract(response, plan.ToolChoiceContract); err != nil {
				return domain.Response{}, err
			}
			return response, nil
		}

		var validationErr *constrainedCustomToolValidationError
		if errors.As(err, &validationErr) {
			if recovered, handled, recoverErr := h.tryRecoverPreparedLocalConstrainedCustomToolResponse(ctx, input, prepared, rawFields, responseID, plan, validationErr); handled {
				if recoverErr == nil {
					return recovered, nil
				}
				return domain.Response{}, recoverErr
			}
		}
		if errors.As(err, &validationErr) && hasConstrainedCustomTools(plan.Bridge) && attempt < maxLocalConstrainedCustomToolRepairAttempts {
			repairPrompt = buildConstrainedCustomToolRepairPrompt(validationErr)
			continue
		}
		if errors.As(err, &validationErr) {
			return domain.Response{}, buildConstrainedCustomToolRepairExhaustedError(validationErr, attempt)
		}
		return domain.Response{}, err
	}
}

func annotateLocalToolSearchNamespaces(items []domain.Item, namespaceByToolName map[string]string) ([]domain.Item, error) {
	if len(items) == 0 || len(namespaceByToolName) == 0 {
		return items, nil
	}
	out := make([]domain.Item, 0, len(items))
	for _, item := range items {
		if item.Type != "function_call" {
			out = append(out, item)
			continue
		}
		name := strings.TrimSpace(item.Name())
		namespace := strings.TrimSpace(namespaceByToolName[name])
		if namespace == "" || strings.TrimSpace(item.Namespace()) != "" {
			out = append(out, item)
			continue
		}
		updated, err := item.WithField("namespace", namespace)
		if err != nil {
			return nil, err
		}
		out = append(out, updated)
	}
	return out, nil
}

func toolBoolField(tool map[string]any, key string) bool {
	value, ok := tool[key].(bool)
	return ok && value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
