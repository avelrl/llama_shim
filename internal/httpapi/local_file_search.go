package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/service"
)

const defaultLocalFileSearchResultsLimit = 20
const localFileSearchCitationLimit = 3

var shimLocalFileSearchFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
	"include":             {},
}

type localFileSearchConfig struct {
	VectorStoreIDs []string
	Filters        *domain.VectorStoreSearchFilter
	MaxNumResults  int
	Ranker         string
	ScoreThreshold *float64
	HybridSearch   *domain.VectorStoreHybridSearchOptions
	IncludeResults bool
}

type localFileSearchResult struct {
	Attributes    map[string]any
	FileID        string
	Filename      string
	Score         float64
	Snippets      []string
	Text          string
	VectorStoreID string
}

type localFileSearchAnnotationRange struct {
	Start int
	End   int
}

func isLocalFileSearchToolRequest(rawFields map[string]json.RawMessage) bool {
	tools := decodeToolList(rawFields)
	if len(tools) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(asString(tools[0]["type"])), "file_search")
}

func supportsLocalFileSearch(rawFields map[string]json.RawMessage) bool {
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		if _, ok := shimLocalFileSearchFields[key]; ok {
			continue
		}
		return false
	}

	_, err := parseLocalFileSearchConfig(rawFields)
	return err == nil
}

func (h *responseHandler) createLocalFileSearchResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	config, err := parseLocalFileSearchConfig(rawFields)
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
		searchQueries = []string{query}
	}
	if len(searchQueries) > h.serviceLimits.RetrievalMaxSearchQueries {
		searchQueries = searchQueries[:h.serviceLimits.RetrievalMaxSearchQueries]
	}

	results, err := h.searchLocalFileSearchResults(ctx, config, searchQueries)
	if err != nil {
		return domain.Response{}, err
	}

	generationContext, err := buildLocalFileSearchGenerationContext(prepared, query, searchQueries, results, h.serviceLimits.RetrievalMaxGroundingChunks)
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

	fileSearchItem, err := buildLocalFileSearchCallItem(searchQueries, results, config.IncludeResults)
	if err != nil {
		return domain.Response{}, err
	}
	messageItem, finalText, err := buildLocalFileSearchAssistantMessage(outputText, results)
	if err != nil {
		return domain.Response{}, err
	}
	response.Output = []domain.Item{fileSearchItem, messageItem}
	response.OutputText = finalText

	response, err = h.service.FinalizeLocalResponse(input, generationContext, response)
	if err != nil {
		return domain.Response{}, err
	}

	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func parseLocalFileSearchConfig(rawFields map[string]json.RawMessage) (localFileSearchConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) != 1 {
		return localFileSearchConfig{}, domain.NewValidationError("tools", "shim-local file_search requires exactly one file_search tool")
	}

	tool := tools[0]
	for key := range tool {
		switch key {
		case "type", "vector_store_ids", "filters", "max_num_results", "ranking_options":
		default:
			return localFileSearchConfig{}, domain.NewValidationError("tools", "unsupported file_search tool field "+`"`+key+`"`+" in shim-local mode")
		}
	}

	if strings.TrimSpace(asString(tool["type"])) != "file_search" {
		return localFileSearchConfig{}, domain.NewValidationError("tools", "shim-local file_search requires tools[0].type=file_search")
	}

	vectorStoreIDs, err := parseLocalFileSearchVectorStoreIDs(tool["vector_store_ids"])
	if err != nil {
		return localFileSearchConfig{}, err
	}

	filters, err := normalizeLocalFileSearchFilters(tool["filters"])
	if err != nil {
		return localFileSearchConfig{}, err
	}

	maxNumResults, err := parseLocalFileSearchMaxResults(tool["max_num_results"])
	if err != nil {
		return localFileSearchConfig{}, err
	}

	ranker, scoreThreshold, hybridSearch, err := parseLocalFileSearchRankingOptions(tool["ranking_options"])
	if err != nil {
		return localFileSearchConfig{}, err
	}

	includeResults, err := parseLocalFileSearchInclude(rawFields["include"])
	if err != nil {
		return localFileSearchConfig{}, err
	}
	if err := validateLocalFileSearchToolChoice(rawFields["tool_choice"]); err != nil {
		return localFileSearchConfig{}, err
	}
	if err := validateLocalFileSearchParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return localFileSearchConfig{}, err
	}

	return localFileSearchConfig{
		VectorStoreIDs: vectorStoreIDs,
		Filters:        filters,
		MaxNumResults:  maxNumResults,
		Ranker:         ranker,
		ScoreThreshold: scoreThreshold,
		HybridSearch:   hybridSearch,
		IncludeResults: includeResults,
	}, nil
}

func parseLocalFileSearchVectorStoreIDs(value any) ([]string, error) {
	rawIDs, ok := value.([]any)
	if !ok || len(rawIDs) == 0 {
		return nil, domain.NewValidationError("tools", "file_search.vector_store_ids must be a non-empty array")
	}

	out := make([]string, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		id := strings.TrimSpace(asString(rawID))
		if id == "" {
			return nil, domain.NewValidationError("tools", "file_search.vector_store_ids must not contain empty values")
		}
		out = append(out, id)
	}
	return out, nil
}

func normalizeLocalFileSearchFilters(value any) (*domain.VectorStoreSearchFilter, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, domain.NewValidationError("tools", "file_search.filters must be valid JSON")
	}
	return domain.NormalizeVectorStoreSearchFilter(raw, "tools")
}

func parseLocalFileSearchMaxResults(value any) (int, error) {
	if value == nil {
		return defaultLocalFileSearchResultsLimit, nil
	}

	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, domain.NewValidationError("tools", "file_search.max_num_results must be an integer")
	}

	maxNumResults := int(number)
	if maxNumResults < 1 || maxNumResults > 50 {
		return 0, domain.NewValidationError("tools", "file_search.max_num_results must be between 1 and 50")
	}
	return maxNumResults, nil
}

func parseLocalFileSearchRankingOptions(value any) (string, *float64, *domain.VectorStoreHybridSearchOptions, error) {
	if value == nil {
		return "", nil, nil, nil
	}

	options, ok := value.(map[string]any)
	if !ok {
		return "", nil, nil, domain.NewValidationError("tools", "file_search.ranking_options must be an object")
	}
	for key := range options {
		switch key {
		case "ranker", "score_threshold", "hybrid_search":
		default:
			return "", nil, nil, domain.NewValidationError("tools", "unsupported file_search.ranking_options field "+`"`+key+`"`+" in shim-local mode")
		}
	}

	ranker := ""
	if rawRanker, ok := options["ranker"]; ok && rawRanker != nil {
		ranker = strings.TrimSpace(asString(rawRanker))
		switch ranker {
		case "", "auto", "none", "default_2024_08_21", "default-2024-08-21":
		default:
			return "", nil, nil, domain.NewValidationError("tools", "unsupported file_search.ranking_options.ranker")
		}
	}

	rawThreshold, ok := options["score_threshold"]
	var scoreThreshold *float64
	if !ok || rawThreshold == nil {
		scoreThreshold = nil
	} else {
		thresholdValue, ok := rawThreshold.(float64)
		if !ok {
			return "", nil, nil, domain.NewValidationError("tools", "file_search.ranking_options.score_threshold must be a number")
		}
		if thresholdValue < 0 || thresholdValue > 1 {
			return "", nil, nil, domain.NewValidationError("tools", "file_search.ranking_options.score_threshold must be between 0 and 1")
		}
		scoreThreshold = &thresholdValue
	}

	var hybridSearch *domain.VectorStoreHybridSearchOptions
	if rawHybrid, ok := options["hybrid_search"]; ok && rawHybrid != nil {
		raw, err := json.Marshal(rawHybrid)
		if err != nil {
			return "", nil, nil, domain.NewValidationError("tools", "file_search.ranking_options.hybrid_search must be valid JSON")
		}
		hybridSearch, err = parseHybridSearchOptions(raw, "tools.file_search.ranking_options.hybrid_search")
		if err != nil {
			return "", nil, nil, err
		}
	}

	return ranker, scoreThreshold, hybridSearch, nil
}

func parseLocalFileSearchInclude(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil
	}

	var includes []string
	if err := json.Unmarshal(trimmed, &includes); err != nil {
		return false, domain.NewValidationError("include", "include must be an array of strings")
	}

	includeResults := false
	for _, include := range includes {
		switch strings.TrimSpace(include) {
		case "":
		case "file_search_call.results":
			includeResults = true
		default:
			return false, domain.NewValidationError("include", "unsupported include value for shim-local file_search")
		}
	}
	return includeResults, nil
}

func validateLocalFileSearchToolChoice(raw json.RawMessage) error {
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
			return domain.NewValidationError("tool_choice", "shim-local file_search supports tool_choice=auto or required")
		}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local file_search")
	}

	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local file_search")
	}
	if strings.TrimSpace(choiceType) != "file_search" {
		return domain.NewValidationError("tool_choice", "shim-local file_search only supports tool_choice targeting file_search")
	}
	return nil
}

func validateLocalFileSearchParallelToolCalls(raw json.RawMessage) error {
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

func deriveLocalFileSearchQuery(items []domain.Item) (string, error) {
	projected, err := domain.ProjectLocalTextGenerationContext(items)
	if err != nil {
		return "", err
	}

	for i := len(projected) - 1; i >= 0; i-- {
		if projected[i].Type != "message" || projected[i].Role != "user" {
			continue
		}
		if text := strings.TrimSpace(domain.MessageText(projected[i])); text != "" {
			return text, nil
		}
	}
	for i := len(projected) - 1; i >= 0; i-- {
		if projected[i].Type != "message" {
			continue
		}
		if text := strings.TrimSpace(domain.MessageText(projected[i])); text != "" {
			return text, nil
		}
	}

	return "", domain.NewValidationError("input", "shim-local file_search requires a text message input")
}

func (h *responseHandler) searchLocalFileSearchResults(ctx context.Context, config localFileSearchConfig, queries []string) ([]localFileSearchResult, error) {
	release, err := h.retrievalGate.tryAcquire()
	if err != nil {
		return nil, err
	}
	defer release()

	type resultKey struct {
		VectorStoreID string
		FileID        string
	}

	start := time.Now()
	bestByFile := map[resultKey]localFileSearchResult{}
	for _, vectorStoreID := range config.VectorStoreIDs {
		page, err := h.proxy.store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
			VectorStoreID:  vectorStoreID,
			Queries:        queries,
			Filters:        config.Filters,
			MaxNumResults:  config.MaxNumResults,
			Ranker:         config.Ranker,
			ScoreThreshold: config.ScoreThreshold,
			HybridSearch:   config.HybridSearch,
			RawSearchQuery: retrieval.SearchQueryPayload(queries),
		})
		if err != nil {
			if h.metrics != nil {
				h.metrics.IncRetrievalSearch("local_file_search", "error")
			}
			return nil, err
		}
		for _, result := range page.Results {
			key := resultKey{VectorStoreID: vectorStoreID, FileID: result.FileID}
			candidate := localFileSearchResult{
				Attributes:    cloneLocalFileSearchAttributes(result.Attributes),
				FileID:        result.FileID,
				Filename:      result.Filename,
				Score:         result.Score,
				Snippets:      localFileSearchSnippets(result.Content),
				Text:          joinLocalFileSearchContent(result.Content),
				VectorStoreID: vectorStoreID,
			}
			if current, ok := bestByFile[key]; ok && current.Score >= candidate.Score {
				continue
			}
			bestByFile[key] = candidate
		}
	}

	results := make([]localFileSearchResult, 0, len(bestByFile))
	for _, result := range bestByFile {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].Filename == results[j].Filename {
				if results[i].VectorStoreID == results[j].VectorStoreID {
					return results[i].FileID < results[j].FileID
				}
				return results[i].VectorStoreID < results[j].VectorStoreID
			}
			return results[i].Filename < results[j].Filename
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > config.MaxNumResults {
		results = results[:config.MaxNumResults]
	}
	if h.metrics != nil {
		h.metrics.IncRetrievalSearch("local_file_search", "ok")
	}
	h.logger.InfoContext(ctx, "retrieval search",
		"request_id", RequestIDFromContext(ctx),
		"surface", "local_file_search",
		"vector_store_ids", config.VectorStoreIDs,
		"queries", queries,
		"result_count", len(results),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return results, nil
}

func buildLocalFileSearchGenerationContext(prepared service.PreparedResponseContext, query string, searchQueries []string, results []localFileSearchResult, maxChunks int) ([]domain.Item, error) {
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

	searchContext := domain.NewInputTextMessage("system", buildLocalFileSearchContextPrompt(query, searchQueries, results, maxChunks))
	out := make([]domain.Item, 0, len(prefix)+len(currentInput)+1)
	out = append(out, prefix...)
	out = append(out, searchContext)
	out = append(out, currentInput...)
	return out, nil
}

func buildLocalFileSearchContextPrompt(query string, searchQueries []string, results []localFileSearchResult, maxChunks int) string {
	var builder strings.Builder
	builder.WriteString("You have access to shim-local file search results.\n")
	builder.WriteString("Use only the retrieved snippets below as local knowledge for this turn.\n")
	builder.WriteString("If the snippets do not answer the request, say so plainly.\n")
	builder.WriteString("Original user query: ")
	builder.WriteString(query)
	builder.WriteString("\n")
	if len(searchQueries) > 0 {
		builder.WriteString("Search queries used:\n")
		for _, searchQuery := range searchQueries {
			builder.WriteString("- ")
			builder.WriteString(searchQuery)
			builder.WriteString("\n")
		}
	}
	if len(results) == 0 {
		builder.WriteString("No matching local file search results were found.\n")
		return builder.String()
	}

	if maxChunks <= 0 {
		maxChunks = 20
	}
	remainingChunks := maxChunks
	omittedChunks := 0
	for idx, result := range results {
		snippets := localFileSearchContextSnippets(result)
		if len(snippets) == 0 {
			continue
		}
		if remainingChunks <= 0 {
			omittedChunks += len(snippets)
			continue
		}
		if len(snippets) > remainingChunks {
			omittedChunks += len(snippets) - remainingChunks
			snippets = snippets[:remainingChunks]
		}
		remainingChunks -= len(snippets)

		builder.WriteString("\n[")
		builder.WriteString(fmt.Sprintf("%d", idx+1))
		builder.WriteString("] filename=")
		builder.WriteString(result.Filename)
		builder.WriteString(" file_id=")
		builder.WriteString(result.FileID)
		builder.WriteString(" vector_store_id=")
		builder.WriteString(result.VectorStoreID)
		builder.WriteString(" score=")
		builder.WriteString(fmt.Sprintf("%.4f", result.Score))
		builder.WriteString("\n")
		for snippetIndex, snippet := range snippets {
			builder.WriteString("snippet ")
			builder.WriteString(fmt.Sprintf("%d", snippetIndex+1))
			builder.WriteString(":\n")
			builder.WriteString(snippet)
			builder.WriteString("\n")
		}
	}
	if omittedChunks > 0 {
		builder.WriteString("\nAdditional local search chunks were omitted to stay within the grounding context budget.\n")
	}
	return builder.String()
}

func buildLocalFileSearchCallItem(queries []string, results []localFileSearchResult, includeResults bool) (domain.Item, error) {
	payload := map[string]any{
		"type":    "file_search_call",
		"status":  "completed",
		"queries": queries,
		"results": nil,
	}
	if includeResults {
		encodedResults := make([]map[string]any, 0, len(results))
		for _, result := range results {
			content := make([]map[string]any, 0, len(result.Snippets))
			for _, snippet := range localFileSearchContextSnippets(result) {
				content = append(content, map[string]any{
					"type": "text",
					"text": snippet,
				})
			}
			encodedResults = append(encodedResults, map[string]any{
				"attributes":      cloneLocalFileSearchAttributes(result.Attributes),
				"content":         content,
				"file_id":         result.FileID,
				"filename":        result.Filename,
				"score":           result.Score,
				"vector_store_id": result.VectorStoreID,
			})
		}
		payload["results"] = encodedResults
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func buildCompletedAssistantMessage(text string) (domain.Item, error) {
	return buildCompletedAssistantMessageWithAnnotations(text, nil)
}

func buildLocalFileSearchAssistantMessage(text string, results []localFileSearchResult) (domain.Item, string, error) {
	finalText, annotations := buildLocalFileSearchAssistantTextAnnotations(text, results)
	item, err := buildCompletedAssistantMessageWithAnnotations(finalText, annotations)
	if err != nil {
		return domain.Item{}, "", err
	}
	return item, finalText, nil
}

func buildLocalFileSearchAssistantTextAnnotations(text string, results []localFileSearchResult) (string, []any) {
	citedFiles := topLocalFileSearchCitationResults(results, localFileSearchCitationLimit)
	if len(citedFiles) == 0 {
		return text, nil
	}

	annotations := make([]any, 0, len(citedFiles))
	usedRanges := make([]localFileSearchAnnotationRange, 0, len(citedFiles))
	fallbackIndex := utf8.RuneCountInString(text)
	for _, result := range citedFiles {
		index, ok := localFileSearchFilenameMentionIndex(text, result.Filename, usedRanges)
		if !ok {
			index = fallbackIndex
		} else {
			usedRanges = append(usedRanges, localFileSearchAnnotationRange{
				Start: index,
				End:   index + utf8.RuneCountInString(strings.TrimSpace(result.Filename)),
			})
		}
		annotations = append(annotations, map[string]any{
			"type":     "file_citation",
			"index":    index,
			"file_id":  result.FileID,
			"filename": result.Filename,
		})
	}
	return text, annotations
}

func buildCompletedAssistantMessageWithAnnotations(text string, annotations []any) (domain.Item, error) {
	if annotations == nil {
		annotations = []any{}
	}
	raw, err := json.Marshal(map[string]any{
		"type":   "message",
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]any{
			{
				"type":        "output_text",
				"text":        text,
				"annotations": annotations,
			},
		},
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func cloneLocalFileSearchAttributes(attributes map[string]any) map[string]any {
	if len(attributes) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(attributes))
	for key, value := range attributes {
		out[key] = value
	}
	return out
}

func joinLocalFileSearchContent(content []domain.VectorStoreSearchResultContent) string {
	return strings.Join(localFileSearchSnippets(content), "\n\n")
}

func localFileSearchSnippets(content []domain.VectorStoreSearchResultContent) []string {
	parts := make([]string, 0, len(content))
	for _, part := range content {
		if part.Type != "text" {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return parts
}

func localFileSearchContextSnippets(result localFileSearchResult) []string {
	if len(result.Snippets) > 0 {
		return result.Snippets
	}
	if text := strings.TrimSpace(result.Text); text != "" {
		return []string{text}
	}
	return nil
}

func topLocalFileSearchCitationResults(results []localFileSearchResult, limit int) []localFileSearchResult {
	if limit <= 0 || len(results) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(results))
	out := make([]localFileSearchResult, 0, min(limit, len(results)))
	for _, result := range results {
		fileID := strings.TrimSpace(result.FileID)
		filename := strings.TrimSpace(result.Filename)
		if fileID == "" || filename == "" {
			continue
		}
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}
		out = append(out, result)
		if len(out) == limit {
			break
		}
	}
	return out
}

func localFileSearchFilenameMentionIndex(text string, filename string, used []localFileSearchAnnotationRange) (int, bool) {
	trimmedText := strings.TrimSpace(text)
	trimmedFilename := strings.TrimSpace(filename)
	if trimmedText == "" || trimmedFilename == "" {
		return 0, false
	}

	lowerText := strings.ToLower(text)
	lowerFilename := strings.ToLower(trimmedFilename)
	searchOffset := 0
	for {
		idx := strings.Index(lowerText[searchOffset:], lowerFilename)
		if idx < 0 {
			return 0, false
		}
		startByte := searchOffset + idx
		endByte := startByte + len(lowerFilename)
		startRune := utf8.RuneCountInString(text[:startByte])
		endRune := startRune + utf8.RuneCountInString(text[startByte:endByte])
		if !localFileSearchAnnotationRangeOverlaps(startRune, endRune, used) {
			return startRune, true
		}
		searchOffset = endByte
	}
}

func localFileSearchAnnotationRangeOverlaps(start int, end int, used []localFileSearchAnnotationRange) bool {
	for _, candidate := range used {
		if start < candidate.End && end > candidate.Start {
			return true
		}
	}
	return false
}
