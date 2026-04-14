package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/service"
	"llama_shim/internal/websearch"
)

const (
	defaultLocalWebSearchQueryLimit     = 4
	defaultLocalWebSearchCitationLimit  = 3
	defaultLocalWebSearchPageExcerptRunes = 6000
	defaultLocalWebSearchMatchExcerptRunes = 220
)

var shimLocalWebSearchFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
	"include":             {},
}

type localWebSearchConfig struct {
	ToolType          string
	SearchContextSize string
	Filters           []string
	UserLocation      map[string]string
}

type localWebSearchSource struct {
	Title string
	URL   string
}

type localWebSearchSearchResult struct {
	Query   string
	Rank    int
	Snippet string
	Title   string
	URL     string
}

type localWebSearchOpenPage struct {
	Text  string
	Title string
	URL   string
}

type localWebSearchFindInPage struct {
	Matches []string
	Pattern string
	URL     string
}

type localWebSearchRun struct {
	FindInPage *localWebSearchFindInPage
	OpenPage   *localWebSearchOpenPage
	Queries    []string
	Results    []localWebSearchSearchResult
	Sources    []localWebSearchSource
}

type localWebSearchAnnotationRange struct {
	Start int
	End   int
}

func supportsLocalWebSearch(rawFields map[string]json.RawMessage, provider websearch.Provider) bool {
	if provider == nil {
		return false
	}
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		if _, ok := shimLocalWebSearchFields[key]; ok {
			continue
		}
		return false
	}
	_, err := parseLocalWebSearchConfig(rawFields)
	return err == nil
}

func (h *responseHandler) createLocalWebSearchResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	config, err := parseLocalWebSearchConfig(rawFields)
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

	query, err := deriveLocalWebSearchQuery(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}

	run, err := h.runLocalWebSearch(ctx, config, query)
	if err != nil {
		return domain.Response{}, err
	}

	generationContext, err := buildLocalWebSearchGenerationContext(prepared, query, run)
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

	output := make([]domain.Item, 0, 4)
	searchItem, err := buildLocalWebSearchSearchItem(run)
	if err != nil {
		return domain.Response{}, err
	}
	output = append(output, searchItem)
	if run.OpenPage != nil {
		openItem, err := buildLocalWebSearchOpenPageItem(run.OpenPage)
		if err != nil {
			return domain.Response{}, err
		}
		output = append(output, openItem)
	}
	if run.FindInPage != nil {
		findItem, err := buildLocalWebSearchFindInPageItem(run.FindInPage)
		if err != nil {
			return domain.Response{}, err
		}
		output = append(output, findItem)
	}
	messageItem, finalText, err := buildLocalWebSearchAssistantMessage(outputText, run.Sources)
	if err != nil {
		return domain.Response{}, err
	}
	output = append(output, messageItem)
	response.Output = output
	response.OutputText = finalText

	response, err = h.service.FinalizeLocalResponse(input, generationContext, response)
	if err != nil {
		return domain.Response{}, err
	}
	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func parseLocalWebSearchConfig(rawFields map[string]json.RawMessage) (localWebSearchConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) != 1 {
		return localWebSearchConfig{}, domain.NewValidationError("tools", "shim-local web_search requires exactly one web_search tool")
	}

	tool := tools[0]
	for key := range tool {
		switch key {
		case "type", "search_context_size", "external_web_access", "filters", "user_location":
		default:
			return localWebSearchConfig{}, domain.NewValidationError("tools", "unsupported web_search tool field "+`"`+key+`"`+" in shim-local mode")
		}
	}

	toolType := strings.ToLower(strings.TrimSpace(asString(tool["type"])))
	switch toolType {
	case "web_search", "web_search_preview":
	default:
		return localWebSearchConfig{}, domain.NewValidationError("tools", "shim-local web_search requires tools[0].type=web_search or web_search_preview")
	}

	if toolType == "web_search" {
		if raw, ok := tool["external_web_access"]; ok && raw != nil {
			value, ok := raw.(bool)
			if !ok {
				return localWebSearchConfig{}, domain.NewValidationError("tools", "web_search.external_web_access must be a boolean")
			}
			if !value {
				return localWebSearchConfig{}, domain.ErrUnsupportedShape
			}
		}
	}

	searchContextSize := "medium"
	if rawContext, ok := tool["search_context_size"]; ok && rawContext != nil {
		searchContextSize = strings.ToLower(strings.TrimSpace(asString(rawContext)))
		switch searchContextSize {
		case "low", "medium", "high":
		default:
			return localWebSearchConfig{}, domain.NewValidationError("tools", "web_search.search_context_size must be low, medium, or high")
		}
	}

	filters, err := parseLocalWebSearchFilters(toolType, tool["filters"])
	if err != nil {
		return localWebSearchConfig{}, err
	}
	userLocation, err := parseLocalWebSearchUserLocation(tool["user_location"])
	if err != nil {
		return localWebSearchConfig{}, err
	}
	if err := parseLocalWebSearchInclude(rawFields["include"]); err != nil {
		return localWebSearchConfig{}, err
	}
	if err := validateLocalWebSearchToolChoice(rawFields["tool_choice"], toolType); err != nil {
		return localWebSearchConfig{}, err
	}
	if err := validateLocalWebSearchParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return localWebSearchConfig{}, err
	}

	return localWebSearchConfig{
		ToolType:          toolType,
		SearchContextSize: searchContextSize,
		Filters:           filters,
		UserLocation:      userLocation,
	}, nil
}

func parseLocalWebSearchFilters(toolType string, value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if toolType != "web_search" {
		return nil, domain.NewValidationError("tools", "web_search_preview does not support filters in shim-local mode")
	}

	domains := make([]string, 0)
	appendDomains := func(values []string) error {
		for _, candidate := range values {
			normalized := normalizeLocalWebSearchFilterDomain(candidate)
			if normalized == "" {
				return domain.NewValidationError("tools", "web_search.filters must not contain empty domains")
			}
			domains = append(domains, normalized)
			if len(domains) > 100 {
				return domain.NewValidationError("tools", "web_search.filters supports at most 100 domains")
			}
		}
		return nil
	}

	switch typed := value.(type) {
	case map[string]any:
		values, err := parseLocalWebSearchFilterDomainsObject(typed)
		if err != nil {
			return nil, err
		}
		if err := appendDomains(values); err != nil {
			return nil, err
		}
	case []any:
		if len(typed) == 0 {
			return nil, nil
		}
		if values, ok := parseStringArrayAny(typed); ok {
			if err := appendDomains(values); err != nil {
				return nil, err
			}
			break
		}
		for _, entry := range typed {
			object, ok := entry.(map[string]any)
			if !ok {
				return nil, domain.NewValidationError("tools", "unsupported web_search.filters shape in shim-local mode")
			}
			values, err := parseLocalWebSearchFilterDomainsObject(object)
			if err != nil {
				return nil, err
			}
			if err := appendDomains(values); err != nil {
				return nil, err
			}
		}
	default:
		return nil, domain.NewValidationError("tools", "unsupported web_search.filters shape in shim-local mode")
	}

	return dedupeLocalWebSearchStrings(domains), nil
}

func parseLocalWebSearchFilterDomainsObject(value map[string]any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if raw, ok := value["allowed_domains"]; ok {
		values, ok := parseStringArrayValue(raw)
		if !ok {
			return nil, domain.NewValidationError("tools", "web_search.filters.allowed_domains must be an array of strings")
		}
		return values, nil
	}
	filterType := strings.ToLower(strings.TrimSpace(asString(value["type"])))
	if filterType != "" && filterType != "allowed_domains" {
		return nil, domain.NewValidationError("tools", "unsupported web_search.filters.type in shim-local mode")
	}
	if raw, ok := value["domains"]; ok {
		values, ok := parseStringArrayValue(raw)
		if !ok {
			return nil, domain.NewValidationError("tools", "web_search.filters.domains must be an array of strings")
		}
		return values, nil
	}
	return nil, domain.NewValidationError("tools", "unsupported web_search.filters object in shim-local mode")
}

func parseLocalWebSearchUserLocation(value any) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, domain.NewValidationError("tools", "web_search.user_location must be an object")
	}
	locationType := strings.ToLower(strings.TrimSpace(asString(raw["type"])))
	if locationType != "" && locationType != "approximate" {
		return nil, domain.NewValidationError("tools", "web_search.user_location.type must be approximate")
	}
	out := map[string]string{}
	for _, key := range []string{"city", "country", "region", "timezone"} {
		if value := strings.TrimSpace(asString(raw[key])); value != "" {
			out[key] = value
		}
	}
	return out, nil
}

func parseLocalWebSearchInclude(raw json.RawMessage) error {
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
		case "", "web_search_call.action.sources":
		default:
			return domain.NewValidationError("include", "unsupported include value for shim-local web_search")
		}
	}
	return nil
}

func validateLocalWebSearchToolChoice(raw json.RawMessage, toolType string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var literal string
	if err := json.Unmarshal(trimmed, &literal); err == nil {
		switch strings.TrimSpace(literal) {
		case "auto", "required":
			return nil
		default:
			return domain.NewValidationError("tool_choice", "shim-local web_search supports tool_choice=auto or required")
		}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local web_search")
	}
	var choiceType string
	if err := json.Unmarshal(payload["type"], &choiceType); err != nil {
		return domain.NewValidationError("tool_choice", "unsupported tool_choice for shim-local web_search")
	}
	if strings.TrimSpace(choiceType) != toolType {
		return domain.NewValidationError("tool_choice", "shim-local web_search only supports tool_choice targeting the configured web_search tool")
	}
	return nil
}

func validateLocalWebSearchParallelToolCalls(raw json.RawMessage) error {
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

func deriveLocalWebSearchQuery(items []domain.Item) (string, error) {
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
	return "", domain.NewValidationError("input", "shim-local web_search requires a text message input")
}

func (h *responseHandler) runLocalWebSearch(ctx context.Context, config localWebSearchConfig, query string) (localWebSearchRun, error) {
	release, err := h.retrievalGate.tryAcquire()
	if err != nil {
		return localWebSearchRun{}, err
	}
	defer release()

	start := time.Now()
	plannedQueries := retrieval.PlanFileSearchQueries(query)
	if len(plannedQueries) == 0 {
		plannedQueries = []string{strings.TrimSpace(query)}
	}
	if len(plannedQueries) > h.serviceLimits.RetrievalMaxSearchQueries {
		plannedQueries = plannedQueries[:h.serviceLimits.RetrievalMaxSearchQueries]
	}

	searchResults, consultedSources, err := h.executeLocalWebSearchQueries(ctx, config, plannedQueries)
	if err != nil {
		if h.metrics != nil {
			h.metrics.IncRetrievalSearch("local_web_search", "error")
		}
		return localWebSearchRun{}, err
	}

	var (
		openPage   *localWebSearchOpenPage
		findInPage *localWebSearchFindInPage
	)
	findPattern := extractLocalWebSearchFindPattern(query)
	if shouldLocalWebSearchOpenPage(query, findPattern) {
		if page := h.openLocalWebSearchBestPage(ctx, query, searchResults); page != nil {
			openPage = page
			consultedSources = prependLocalWebSearchSource(consultedSources, localWebSearchSource{
				Title: firstNonEmpty(page.Title, page.URL),
				URL:   page.URL,
			})
			if strings.TrimSpace(findPattern) != "" {
				findInPage = &localWebSearchFindInPage{
					URL:     page.URL,
					Pattern: findPattern,
					Matches: findInPageMatches(page.Text, findPattern),
				}
			}
		}
	}

	if h.metrics != nil {
		h.metrics.IncRetrievalSearch("local_web_search", "ok")
	}
	h.logger.InfoContext(ctx, "web search",
		"request_id", RequestIDFromContext(ctx),
		"surface", "local_web_search",
		"tool_type", config.ToolType,
		"queries", plannedQueries,
		"result_count", len(searchResults),
		"opened_page", openPage != nil,
		"find_in_page", findInPage != nil,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return localWebSearchRun{
		Queries:    plannedQueries,
		Results:    searchResults,
		Sources:    consultedSources,
		OpenPage:   openPage,
		FindInPage: findInPage,
	}, nil
}

func (h *responseHandler) executeLocalWebSearchQueries(ctx context.Context, config localWebSearchConfig, queries []string) ([]localWebSearchSearchResult, []localWebSearchSource, error) {
	maxResults := localWebSearchResultsLimit(config.SearchContextSize)
	bestByURL := make(map[string]localWebSearchSearchResult)
	sourceByURL := make(map[string]localWebSearchSource)
	for _, query := range queries {
		response, err := h.webSearchProvider.Search(ctx, websearch.SearchRequest{
			Query:      query,
			MaxResults: maxResults,
		})
		if err != nil {
			return nil, nil, err
		}
		for index, result := range response.Results {
			if !matchesLocalWebSearchFilters(result.URL, config.Filters) {
				continue
			}
			key := strings.TrimSpace(result.URL)
			candidate := localWebSearchSearchResult{
				Query:   query,
				Rank:    index,
				Snippet: strings.TrimSpace(result.Snippet),
				Title:   strings.TrimSpace(result.Title),
				URL:     key,
			}
			current, ok := bestByURL[key]
			if !ok || localWebSearchSearchResultLess(candidate, current) {
				bestByURL[key] = candidate
			}
			if _, exists := sourceByURL[key]; !exists {
				sourceByURL[key] = localWebSearchSource{
					Title: firstNonEmpty(candidate.Title, candidate.URL),
					URL:   candidate.URL,
				}
			}
		}
	}

	results := make([]localWebSearchSearchResult, 0, len(bestByURL))
	for _, result := range bestByURL {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		return localWebSearchSearchResultLess(results[i], results[j])
	})
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	sources := make([]localWebSearchSource, 0, len(results))
	for _, result := range results {
		source := sourceByURL[result.URL]
		sources = append(sources, source)
	}
	return results, sources, nil
}

func localWebSearchSearchResultLess(left, right localWebSearchSearchResult) bool {
	if left.Rank == right.Rank {
		if left.Query == right.Query {
			return left.URL < right.URL
		}
		return left.Query < right.Query
	}
	return left.Rank < right.Rank
}

func buildLocalWebSearchGenerationContext(prepared service.PreparedResponseContext, query string, run localWebSearchRun) ([]domain.Item, error) {
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

	searchContext := domain.NewInputTextMessage("system", buildLocalWebSearchContextPrompt(query, run))
	out := make([]domain.Item, 0, len(prefix)+len(currentInput)+1)
	out = append(out, prefix...)
	out = append(out, searchContext)
	out = append(out, currentInput...)
	return out, nil
}

func buildLocalWebSearchContextPrompt(query string, run localWebSearchRun) string {
	var builder strings.Builder
	builder.WriteString("You have access to shim-local web search results.\n")
	builder.WriteString("Answer only from the consulted web search results and opened page excerpts below.\n")
	builder.WriteString("If the search evidence is insufficient, say so plainly.\n")
	builder.WriteString("If you rely on a source, prefer mentioning its site or page title in the answer.\n")
	builder.WriteString("Original user query: ")
	builder.WriteString(strings.TrimSpace(query))
	builder.WriteString("\n")
	if len(run.Queries) > 0 {
		builder.WriteString("Web search queries used:\n")
		for _, query := range run.Queries {
			builder.WriteString("- ")
			builder.WriteString(query)
			builder.WriteString("\n")
		}
	}
	if len(run.Results) == 0 {
		builder.WriteString("No web search results were found.\n")
		return builder.String()
	}
	builder.WriteString("\nSearch results:\n")
	for index, result := range run.Results {
		builder.WriteString("[")
		builder.WriteString(fmt.Sprintf("%d", index+1))
		builder.WriteString("] title=")
		builder.WriteString(firstNonEmpty(result.Title, "(untitled)"))
		builder.WriteString("\nurl=")
		builder.WriteString(result.URL)
		builder.WriteString("\n")
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			builder.WriteString("snippet:\n")
			builder.WriteString(snippet)
			builder.WriteString("\n")
		}
	}
	if run.OpenPage != nil {
		builder.WriteString("\nOpened page:\n")
		builder.WriteString("title=")
		builder.WriteString(firstNonEmpty(run.OpenPage.Title, "(untitled)"))
		builder.WriteString("\nurl=")
		builder.WriteString(run.OpenPage.URL)
		builder.WriteString("\ncontent excerpt:\n")
		builder.WriteString(trimLocalWebSearchRunes(strings.TrimSpace(run.OpenPage.Text), defaultLocalWebSearchPageExcerptRunes))
		builder.WriteString("\n")
	}
	if run.FindInPage != nil {
		builder.WriteString("\nfind_in_page pattern: ")
		builder.WriteString(run.FindInPage.Pattern)
		builder.WriteString("\n")
		if len(run.FindInPage.Matches) == 0 {
			builder.WriteString("No exact page matches were found.\n")
		} else {
			builder.WriteString("Page matches:\n")
			for _, match := range run.FindInPage.Matches {
				builder.WriteString("- ")
				builder.WriteString(match)
				builder.WriteString("\n")
			}
		}
	}
	return builder.String()
}

func buildLocalWebSearchSearchItem(run localWebSearchRun) (domain.Item, error) {
	sources := make([]map[string]any, 0, len(run.Sources))
	for _, source := range run.Sources {
		sources = append(sources, map[string]any{
			"type": "url",
			"url":  source.URL,
		})
	}
	action := map[string]any{
		"type":   "search",
		"query":  "",
		"queries": nil,
		"sources": sources,
	}
	if len(run.Queries) > 0 {
		action["query"] = run.Queries[0]
	}
	if len(run.Queries) > 1 {
		action["queries"] = run.Queries
	}
	raw, err := json.Marshal(map[string]any{
		"type":   "web_search_call",
		"status": "completed",
		"action": action,
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func buildLocalWebSearchOpenPageItem(openPage *localWebSearchOpenPage) (domain.Item, error) {
	raw, err := json.Marshal(map[string]any{
		"type":   "web_search_call",
		"status": "completed",
		"action": map[string]any{
			"type": "open_page",
			"url":  openPage.URL,
		},
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func buildLocalWebSearchFindInPageItem(find *localWebSearchFindInPage) (domain.Item, error) {
	raw, err := json.Marshal(map[string]any{
		"type":   "web_search_call",
		"status": "completed",
		"action": map[string]any{
			"type":    "find_in_page",
			"url":     find.URL,
			"pattern": find.Pattern,
		},
	})
	if err != nil {
		return domain.Item{}, err
	}
	return domain.NewItem(raw)
}

func buildLocalWebSearchAssistantMessage(text string, sources []localWebSearchSource) (domain.Item, string, error) {
	finalText, annotations := buildLocalWebSearchAssistantTextAnnotations(text, sources)
	item, err := buildCompletedAssistantMessageWithAnnotations(finalText, annotations)
	if err != nil {
		return domain.Item{}, "", err
	}
	return item, finalText, nil
}

func buildLocalWebSearchAssistantTextAnnotations(text string, sources []localWebSearchSource) (string, []any) {
	citedSources := topLocalWebSearchSources(sources, defaultLocalWebSearchCitationLimit)
	if len(citedSources) == 0 {
		return text, nil
	}

	finalText := strings.TrimSpace(text)
	if finalText == "" {
		finalText = "No web result summary was generated."
	}

	annotations := make([]any, 0, len(citedSources))
	usedRanges := make([]localWebSearchAnnotationRange, 0, len(citedSources))
	appendixAdded := false
	for _, source := range citedSources {
		label := strings.TrimSpace(source.Title)
		if label == "" {
			label = localWebSearchSourceLabel(source.URL)
		}
		start, end, ok := localWebSearchCitationMentionRange(finalText, label, usedRanges)
		if !ok {
			if !appendixAdded {
				finalText = strings.TrimSpace(finalText) + "\n\nSources:\n"
				appendixAdded = true
			}
			line := "- " + label
			hostLabel := localWebSearchSourceLabel(source.URL)
			if hostLabel != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(hostLabel)) {
				line += " (" + hostLabel + ")"
			}
			start = utf8.RuneCountInString(finalText) + 2
			end = start + utf8.RuneCountInString(label)
			finalText += line + "\n"
		}
		usedRanges = append(usedRanges, localWebSearchAnnotationRange{Start: start, End: end})
		annotations = append(annotations, map[string]any{
			"type":        "url_citation",
			"start_index": start,
			"end_index":   end,
			"url":         source.URL,
			"title":       label,
		})
	}

	return strings.TrimRight(finalText, "\n"), annotations
}

func topLocalWebSearchSources(sources []localWebSearchSource, limit int) []localWebSearchSource {
	if limit <= 0 || len(sources) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(sources))
	out := make([]localWebSearchSource, 0, min(limit, len(sources)))
	for _, source := range sources {
		url := strings.TrimSpace(source.URL)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		out = append(out, source)
		if len(out) == limit {
			break
		}
	}
	return out
}

func localWebSearchCitationMentionRange(text string, label string, used []localWebSearchAnnotationRange) (int, int, bool) {
	trimmedLabel := strings.TrimSpace(label)
	if strings.TrimSpace(text) == "" || trimmedLabel == "" {
		return 0, 0, false
	}
	lowerText := strings.ToLower(text)
	lowerLabel := strings.ToLower(trimmedLabel)
	searchOffset := 0
	for {
		index := strings.Index(lowerText[searchOffset:], lowerLabel)
		if index < 0 {
			return 0, 0, false
		}
		startByte := searchOffset + index
		start := utf8.RuneCountInString(text[:startByte])
		end := start + utf8.RuneCountInString(trimmedLabel)
		if !localWebSearchRangeOverlaps(start, end, used) {
			return start, end, true
		}
		searchOffset = startByte + len(trimmedLabel)
	}
}

func localWebSearchRangeOverlaps(start, end int, used []localWebSearchAnnotationRange) bool {
	for _, candidate := range used {
		if start < candidate.End && end > candidate.Start {
			return true
		}
	}
	return false
}

func localWebSearchSourceLabel(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

func localWebSearchResultsLimit(searchContextSize string) int {
	switch strings.ToLower(strings.TrimSpace(searchContextSize)) {
	case "low":
		return 5
	case "high":
		return 12
	default:
		return 8
	}
}

func shouldLocalWebSearchOpenPage(query string, findPattern string) bool {
	if strings.TrimSpace(findPattern) != "" {
		return true
	}
	lower := strings.ToLower(query)
	for _, fragment := range []string{
		"open the",
		"open page",
		"opened page",
		"page itself",
		"read the page",
		"use the opened page",
		"within that page",
		"on that page",
		"from the page",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func extractLocalWebSearchFindPattern(query string) string {
	for _, quotes := range [][2]string{
		{`"`, `"`},
		{`“`, `”`},
	} {
		start := strings.Index(query, quotes[0])
		if start < 0 {
			continue
		}
		end := strings.Index(query[start+len(quotes[0]):], quotes[1])
		if end < 0 {
			continue
		}
		pattern := strings.TrimSpace(query[start+len(quotes[0]) : start+len(quotes[0])+end])
		if pattern != "" {
			return pattern
		}
	}

	lower := strings.ToLower(query)
	findIndex := strings.Index(lower, "find ")
	inIndex := strings.Index(lower, " in ")
	if findIndex >= 0 && inIndex > findIndex+5 {
		pattern := strings.TrimSpace(query[findIndex+5 : inIndex])
		pattern = strings.TrimPrefix(strings.ToLower(pattern), "the exact phrase ")
		pattern = strings.TrimPrefix(pattern, "the phrase ")
		pattern = strings.TrimPrefix(pattern, "phrase ")
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			return pattern
		}
	}
	return ""
}

func (h *responseHandler) openLocalWebSearchBestPage(ctx context.Context, query string, results []localWebSearchSearchResult) *localWebSearchOpenPage {
	best := selectLocalWebSearchBestResult(query, results)
	if best == nil {
		return nil
	}
	page, err := h.webSearchProvider.OpenPage(ctx, best.URL)
	if err != nil {
		if h.logger != nil {
			h.logger.WarnContext(ctx, "web search open_page failed",
				"request_id", RequestIDFromContext(ctx),
				"url", best.URL,
				"err", err,
			)
		}
		return nil
	}
	return &localWebSearchOpenPage{
		Title: firstNonEmpty(page.Title, best.Title),
		Text:  page.Text,
		URL:   firstNonEmpty(page.URL, best.URL),
	}
}

func selectLocalWebSearchBestResult(query string, results []localWebSearchSearchResult) *localWebSearchSearchResult {
	if len(results) == 0 {
		return nil
	}
	lowerQuery := strings.ToLower(query)
	for _, result := range results {
		label := strings.ToLower(result.Title + " " + result.URL)
		if strings.Contains(lowerQuery, "openai") && strings.Contains(label, "openai") {
			return &result
		}
	}
	return &results[0]
}

func findInPageMatches(pageText string, pattern string) []string {
	pageText = strings.TrimSpace(pageText)
	pattern = strings.TrimSpace(pattern)
	if pageText == "" || pattern == "" {
		return nil
	}
	lowerText := strings.ToLower(pageText)
	lowerPattern := strings.ToLower(pattern)
	matches := make([]string, 0, 3)
	searchOffset := 0
	for len(matches) < 3 {
		index := strings.Index(lowerText[searchOffset:], lowerPattern)
		if index < 0 {
			break
		}
		startByte := searchOffset + index
		startRune := utf8.RuneCountInString(pageText[:startByte])
		textRunes := []rune(pageText)
		matchRunes := []rune(pageText[startByte : startByte+len(pattern)])
		from := max(0, startRune-defaultLocalWebSearchMatchExcerptRunes)
		to := min(len(textRunes), startRune+len(matchRunes)+defaultLocalWebSearchMatchExcerptRunes)
		snippet := strings.TrimSpace(string(textRunes[from:to]))
		if snippet != "" {
			matches = append(matches, snippet)
		}
		searchOffset = startByte + len(pattern)
	}
	return matches
}

func matchesLocalWebSearchFilters(rawURL string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parsed.Hostname())), "www.")
	path := strings.TrimSpace(parsed.EscapedPath())
	for _, filter := range filters {
		if localWebSearchFilterMatches(host, path, filter) {
			return true
		}
	}
	return false
}

func localWebSearchFilterMatches(host string, path string, filter string) bool {
	filter = normalizeLocalWebSearchFilterDomain(filter)
	if filter == "" {
		return false
	}
	if strings.Contains(filter, "/") {
		parts := strings.SplitN(filter, "/", 2)
		domainPart := parts[0]
		pathPart := "/" + strings.TrimPrefix(parts[1], "/")
		return localWebSearchDomainMatches(host, domainPart) && strings.HasPrefix(path, pathPart)
	}
	return localWebSearchDomainMatches(host, filter)
}

func localWebSearchDomainMatches(host string, domain string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "www.")
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func normalizeLocalWebSearchFilterDomain(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "https://")
	normalized = strings.TrimPrefix(normalized, "http://")
	return strings.TrimSuffix(normalized, "/")
}

func parseStringArrayValue(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	return parseStringArrayAny(values)
}

func parseStringArrayAny(values []any) ([]string, bool) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func dedupeLocalWebSearchStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func prependLocalWebSearchSource(sources []localWebSearchSource, source localWebSearchSource) []localWebSearchSource {
	if strings.TrimSpace(source.URL) == "" {
		return sources
	}
	out := make([]localWebSearchSource, 0, len(sources)+1)
	out = append(out, source)
	for _, existing := range sources {
		if strings.EqualFold(strings.TrimSpace(existing.URL), strings.TrimSpace(source.URL)) {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func trimLocalWebSearchRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(string(runes[:limit]))
}
