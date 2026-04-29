package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"llama_shim/internal/compactor"
	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/storage"
)

type Generator interface {
	Generate(ctx context.Context, model string, items []domain.Item, options map[string]json.RawMessage) (string, error)
	GenerateStream(ctx context.Context, model string, items []domain.Item, options map[string]json.RawMessage, onDelta func(string) error) error
}

type ResponseStore = storage.ResponseStore
type ConversationStore = storage.ResponseConversationStore

type CreateResponseInput struct {
	Model              string
	Input              json.RawMessage
	TextConfig         json.RawMessage
	Metadata           json.RawMessage
	ContextManagement  json.RawMessage
	Store              *bool
	Stream             *bool
	Background         *bool
	ForceShadowStore   bool
	PreviousResponseID string
	ConversationID     string
	Instructions       string
	RequestJSON        string
	GenerationOptions  map[string]json.RawMessage
}

type PreparedResponseContext struct {
	NormalizedInput []domain.Item
	EffectiveInput  []domain.Item
	ContextItems    []domain.Item
	Conversation    domain.Conversation
	ToolCallRefs    map[string]domain.ToolCallReference
}

type StreamHooks struct {
	OnCreated func(response domain.Response, outputPrefix []domain.Item) error
	OnDelta   func(delta string) error
}

type ResponseService struct {
	responses     ResponseStore
	conversations ConversationStore
	generator     Generator
	compactor     compactor.Compactor
	limits        ResponseServiceLimits
}

type ResponseServiceLimits struct {
	StoredLineageMaxItems          int
	LocalToolOutputSummaryMaxBytes int
}

const (
	defaultStoredResponseLineageMaxItems   = 128
	defaultLocalToolOutputSummaryMaxBytes  = 64 << 10
	localToolOutputSummaryTruncationNotice = "\n\n[truncated by shim local tool output summary limit]"
)

func NewResponseService(responses ResponseStore, conversations ConversationStore, generator Generator) *ResponseService {
	return NewResponseServiceWithLimits(responses, conversations, generator, ResponseServiceLimits{})
}

func NewResponseServiceWithLimits(responses ResponseStore, conversations ConversationStore, generator Generator, limits ResponseServiceLimits) *ResponseService {
	return &ResponseService{
		responses:     responses,
		conversations: conversations,
		generator:     generator,
		compactor:     compactor.Heuristic{},
		limits:        normalizeResponseServiceLimits(limits),
	}
}

func normalizeResponseServiceLimits(limits ResponseServiceLimits) ResponseServiceLimits {
	if limits.StoredLineageMaxItems <= 0 {
		limits.StoredLineageMaxItems = defaultStoredResponseLineageMaxItems
	}
	if limits.LocalToolOutputSummaryMaxBytes <= 0 {
		limits.LocalToolOutputSummaryMaxBytes = defaultLocalToolOutputSummaryMaxBytes
	}
	return limits
}

func (s *ResponseService) SetCompactor(next compactor.Compactor) {
	if next == nil {
		s.compactor = compactor.Heuristic{}
		return
	}
	s.compactor = next
}

func (s *ResponseService) Create(ctx context.Context, input CreateResponseInput) (domain.Response, error) {
	prepared, err := s.prepareCreate(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}
	prepared, err = s.applyAutomaticCompaction(ctx, input, prepared)
	if err != nil {
		return domain.Response{}, err
	}
	generationContext, hasToolOutput, err := buildLocalTextGenerationContext(prepared.ContextItems, s.limits.LocalToolOutputSummaryMaxBytes)
	if err != nil {
		return domain.Response{}, err
	}
	if _, err := s.PrepareLocalResponseText(input, generationContext); err != nil {
		return domain.Response{}, err
	}

	outputText, err := s.generateLocalResponseText(ctx, input, generationContext, hasToolOutput)
	if err != nil {
		return domain.Response{}, err
	}

	return s.completeCreate(ctx, prepared, generationContext, input, outputText)
}

func (s *ResponseService) TryCreatePreparedLocalTextResponse(ctx context.Context, input CreateResponseInput, prepared PreparedResponseContext, responseID string) (domain.Response, bool, error) {
	generationContext, hasToolOutput, err := buildLocalTextGenerationContext(prepared.ContextItems, s.limits.LocalToolOutputSummaryMaxBytes)
	if err != nil {
		return domain.Response{}, false, err
	}
	if !hasToolOutput {
		return domain.Response{}, false, nil
	}
	if _, err := s.PrepareLocalResponseText(input, generationContext); err != nil {
		return domain.Response{}, true, err
	}

	outputText, err := s.generateLocalResponseText(ctx, input, generationContext, hasToolOutput)
	if err != nil {
		return domain.Response{}, true, err
	}
	if strings.TrimSpace(outputText) == "" {
		return domain.Response{}, true, &llama.InvalidResponseError{Message: "llama content was empty"}
	}
	if strings.TrimSpace(responseID) == "" {
		responseID, err = domain.NewPrefixedID("resp")
		if err != nil {
			return domain.Response{}, true, fmt.Errorf("generate response id: %w", err)
		}
	}
	response := domain.NewResponse(responseID, input.Model, outputText, input.PreviousResponseID, input.ConversationID, domain.NowUTC().Unix())
	response = domain.HydrateResponseRequestSurface(response, input.RequestJSON)
	return response, true, nil
}

func (s *ResponseService) PrepareCreateContext(ctx context.Context, input CreateResponseInput) (PreparedResponseContext, error) {
	return s.prepareResponseContext(ctx, input, true, true)
}

func (s *ResponseService) CountInputTokens(ctx context.Context, input CreateResponseInput) (domain.ResponseInputTokens, error) {
	prepared, err := s.prepareResponseContext(ctx, input, false, false)
	if err != nil {
		return domain.ResponseInputTokens{}, err
	}
	count, err := domain.EstimateSyntheticTokenCount(prepared.ContextItems)
	if err != nil {
		return domain.ResponseInputTokens{}, err
	}
	return domain.ResponseInputTokens{
		Object:      "response.input_tokens",
		InputTokens: count,
	}, nil
}

func (s *ResponseService) Compact(ctx context.Context, input CreateResponseInput) (domain.ResponseCompaction, error) {
	prepared, err := s.prepareResponseContext(ctx, input, true, false)
	if err != nil {
		return domain.ResponseCompaction{}, err
	}

	result, err := s.compactor.Compact(ctx, prepared.ContextItems)
	if err != nil {
		return domain.ResponseCompaction{}, err
	}
	output := compactionResponseOutput(result)

	inputTokens, err := domain.EstimateSyntheticTokenCount(prepared.ContextItems)
	if err != nil {
		return domain.ResponseCompaction{}, err
	}
	outputTokens, err := domain.EstimateSyntheticTokenCount(output)
	if err != nil {
		return domain.ResponseCompaction{}, err
	}
	id, err := domain.NewPrefixedID("resp")
	if err != nil {
		return domain.ResponseCompaction{}, fmt.Errorf("generate compaction response id: %w", err)
	}

	return domain.ResponseCompaction{
		ID:        id,
		Object:    "response.compaction",
		CreatedAt: domain.NowUTC().Unix(),
		Output:    output,
		Usage:     domain.BuildSyntheticUsage(inputTokens, outputTokens),
	}, nil
}

func compactionResponseOutput(result compactor.Result) []domain.Item {
	if len(result.Output) > 0 {
		return append([]domain.Item(nil), result.Output...)
	}
	return []domain.Item{result.Item}
}

func (s *ResponseService) prepareResponseContext(ctx context.Context, input CreateResponseInput, requireModel bool, requireInput bool) (PreparedResponseContext, error) {
	if requireModel && input.Model == "" {
		return PreparedResponseContext{}, domain.NewValidationError("model", "model is required")
	}
	if input.PreviousResponseID != "" && input.ConversationID != "" {
		return PreparedResponseContext{}, domain.NewValidationError("previous_response_id", "previous_response_id and conversation are mutually exclusive")
	}
	if _, err := domain.NormalizeResponseMetadata(input.Metadata); err != nil {
		return PreparedResponseContext{}, err
	}

	normalizedInput := make([]domain.Item, 0)
	if requireInput || hasRequestInput(input.Input) {
		items, err := domain.NormalizeInput(input.Input)
		if err != nil {
			return PreparedResponseContext{}, err
		}
		items, err = domain.ExpandSyntheticCompactionItems(items)
		if err != nil {
			return PreparedResponseContext{}, err
		}
		normalizedInput = items
	}

	var (
		baseItems    []domain.Item
		contextItems []domain.Item
		conversation domain.Conversation
		toolCallRefs map[string]domain.ToolCallReference
		err          error
	)

	switch {
	case input.PreviousResponseID != "":
		lineage, err := s.responses.GetResponseLineage(ctx, input.PreviousResponseID, s.limits.StoredLineageMaxItems)
		if err != nil {
			return PreparedResponseContext{}, err
		}
		baseItems = buildLineageContextItems(lineage)
	case input.ConversationID != "":
		var items []domain.ConversationItem
		conversation, items, err = s.conversations.GetConversation(ctx, input.ConversationID)
		if err != nil {
			return PreparedResponseContext{}, err
		}
		baseItems = domainItemsFromConversation(items)
	default:
		baseItems = nil
	}
	baseItems, err = domain.ExpandSyntheticCompactionItems(baseItems)
	if err != nil {
		return PreparedResponseContext{}, err
	}
	toolCallRefs = domain.CollectToolCallReferences(baseItems)
	normalizedInput, err = domain.CanonicalizeToolOutputs(normalizedInput, toolCallRefs)
	if err != nil {
		return PreparedResponseContext{}, err
	}
	contextItems = domain.AppendCurrentRequestContext(baseItems, input.Instructions, normalizedInput)
	effectiveInput := make([]domain.Item, 0, len(baseItems)+len(normalizedInput))
	effectiveInput = append(effectiveInput, baseItems...)
	effectiveInput = append(effectiveInput, normalizedInput...)

	return PreparedResponseContext{
		NormalizedInput: normalizedInput,
		EffectiveInput:  effectiveInput,
		ContextItems:    contextItems,
		Conversation:    conversation,
		ToolCallRefs:    toolCallRefs,
	}, nil
}

func hasRequestInput(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func (s *ResponseService) SaveExternalResponse(ctx context.Context, prepared PreparedResponseContext, input CreateResponseInput, response domain.Response) (domain.Response, error) {
	response.Object = "response"
	if response.Model == "" {
		response.Model = input.Model
	}
	if response.PreviousResponseID == "" && input.PreviousResponseID != "" {
		response.PreviousResponseID = input.PreviousResponseID
	}
	if response.Conversation == nil && input.ConversationID != "" {
		response.Conversation = domain.NewConversationReference(input.ConversationID)
	}
	if len(bytes.TrimSpace(response.Text)) == 0 || bytes.Equal(bytes.TrimSpace(response.Text), []byte("null")) {
		response.Text = domain.InferResponseTextConfigFromRequestJSON(input.RequestJSON)
	}
	if response.Store == nil {
		response.Store = domain.BoolPtr(input.Store == nil || *input.Store)
	}
	if response.Background == nil {
		response.Background = domain.BoolPtr(input.Background != nil && *input.Background)
	}
	if response.Metadata == nil {
		metadata, err := domain.NormalizeResponseMetadata(input.Metadata)
		if err != nil {
			return domain.Response{}, err
		}
		response.Metadata = metadata
	}
	response = domain.HydrateResponseRequestSurface(response, input.RequestJSON)

	now := domain.NowUTC()
	if response.CreatedAt == 0 {
		response.CreatedAt = now.Unix()
	}
	if response.Status == "" {
		switch {
		case strings.TrimSpace(response.OutputText) != "", len(response.Output) > 0:
			response.Status = "completed"
		default:
			response.Status = "in_progress"
		}
	}
	if !strings.EqualFold(response.Status, "completed") {
		response.CompletedAt = nil
	}
	if response.CompletedAt == nil && strings.EqualFold(response.Status, "completed") {
		response.CompletedAt = domain.Int64Ptr(now.Unix())
	}
	if strings.TrimSpace(response.OutputText) != "" && len(response.Output) == 0 {
		response.Output = []domain.Item{domain.NewOutputTextMessage(response.OutputText)}
	}
	if responseRequiresOutput(response) && len(response.Output) == 0 {
		return domain.Response{}, domain.NewValidationError("output", "assistant output is required")
	}
	normalizedInput, err := domain.EnsureItemIDs(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	effectiveInput, err := buildStoredEffectiveInputItems(prepared.EffectiveInput, normalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	if len(response.Output) > 0 {
		response.Output, err = domain.EnsureItemIDs(response.Output)
		if err != nil {
			return domain.Response{}, err
		}
	}

	publicStore := input.Store == nil || *input.Store
	persistShadow := input.ForceShadowStore || input.PreviousResponseID != "" || input.ConversationID != "" || publicStore
	responseJSON, err := marshalResponseJSON(response)
	if err != nil {
		return domain.Response{}, err
	}
	stored := domain.StoredResponse{
		ID:                   response.ID,
		Model:                response.Model,
		RequestJSON:          input.RequestJSON,
		ResponseJSON:         responseJSON,
		NormalizedInputItems: normalizedInput,
		EffectiveInputItems:  effectiveInput,
		Output:               response.Output,
		OutputText:           response.OutputText,
		PreviousResponseID:   response.PreviousResponseID,
		ConversationID:       domain.ConversationReferenceID(response.Conversation),
		Store:                publicStore,
		CreatedAt:            formatUnixTime(response.CreatedAt),
		CompletedAt:          formatResponseCompletedAt(response),
	}

	switch {
	case input.ConversationID != "":
		if err := s.conversations.SaveResponseAndAppendConversation(ctx, prepared.Conversation, stored, normalizedInput, response.Output); err != nil {
			return domain.Response{}, err
		}
	case persistShadow:
		if err := s.responses.SaveResponse(ctx, stored); err != nil {
			return domain.Response{}, err
		}
	}

	return response, nil
}

func (s *ResponseService) SaveReplayArtifacts(ctx context.Context, responseID string, artifacts []domain.ResponseReplayArtifact) error {
	if strings.TrimSpace(responseID) == "" || len(artifacts) == 0 {
		return nil
	}
	return s.responses.SaveResponseReplayArtifacts(ctx, responseID, artifacts)
}

func (s *ResponseService) GetReplayArtifacts(ctx context.Context, responseID string) ([]domain.ResponseReplayArtifact, error) {
	if strings.TrimSpace(responseID) == "" {
		return nil, nil
	}
	return s.responses.GetResponseReplayArtifacts(ctx, responseID)
}

func (s *ResponseService) CreateStream(ctx context.Context, input CreateResponseInput, hooks StreamHooks) (domain.Response, error) {
	prepared, err := s.prepareCreate(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}
	prepared, err = s.applyAutomaticCompaction(ctx, input, prepared)
	if err != nil {
		return domain.Response{}, err
	}
	generationContext, hasToolOutput, err := buildLocalTextGenerationContext(prepared.ContextItems, s.limits.LocalToolOutputSummaryMaxBytes)
	if err != nil {
		return domain.Response{}, err
	}
	textConfig, err := s.PrepareLocalResponseText(input, generationContext)
	if err != nil {
		return domain.Response{}, err
	}
	metadata, err := domain.NormalizeResponseMetadata(input.Metadata)
	if err != nil {
		return domain.Response{}, err
	}

	created := domain.Response{
		ID:                 prepared.ResponseID,
		Object:             "response",
		CreatedAt:          prepared.CreatedAt.Unix(),
		Status:             "in_progress",
		Model:              input.Model,
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       domain.NewConversationReference(input.ConversationID),
		Background:         domain.BoolPtr(input.Background != nil && *input.Background),
		Store:              domain.BoolPtr(input.Store == nil || *input.Store),
		Text:               domain.MarshalResponseTextConfig(textConfig),
		Metadata:           metadata,
		OutputText:         "",
		Output:             []domain.Item{},
	}
	created = domain.HydrateResponseRequestSurface(created, input.RequestJSON)
	if hooks.OnCreated != nil {
		outputPrefix := append([]domain.Item(nil), prepared.OutputPrefix...)
		if err := hooks.OnCreated(created, outputPrefix); err != nil {
			return domain.Response{}, err
		}
	}

	if hasToolOutput {
		outputText, err := s.generateLocalResponseText(ctx, input, generationContext, hasToolOutput)
		if err != nil {
			return domain.Response{}, err
		}
		if strings.TrimSpace(outputText) == "" {
			return domain.Response{}, &llama.InvalidResponseError{Message: "llama stream content was empty"}
		}
		if hooks.OnDelta != nil {
			if err := hooks.OnDelta(outputText); err != nil {
				return domain.Response{}, err
			}
		}
		return s.completeCreate(ctx, prepared, generationContext, input, outputText)
	}

	var builder strings.Builder
	err = s.generator.GenerateStream(ctx, input.Model, generationContext, input.GenerationOptions, func(delta string) error {
		if delta == "" {
			return nil
		}
		builder.WriteString(delta)
		if hooks.OnDelta != nil {
			return hooks.OnDelta(delta)
		}
		return nil
	})
	if err != nil {
		return domain.Response{}, err
	}

	outputText := builder.String()
	if strings.TrimSpace(outputText) == "" {
		return domain.Response{}, &llama.InvalidResponseError{Message: "llama stream content was empty"}
	}

	return s.completeCreate(ctx, prepared, generationContext, input, outputText)
}

func (s *ResponseService) generateLocalResponseText(ctx context.Context, input CreateResponseInput, generationContext []domain.Item, repairRawToolMarkup bool) (string, error) {
	outputText, err := s.generator.Generate(ctx, input.Model, generationContext, input.GenerationOptions)
	if err != nil {
		return "", err
	}
	if !repairRawToolMarkup || !containsRawToolCallMarkupText(outputText) {
		return outputText, nil
	}

	repairedContext := appendRawToolMarkupRepairInstruction(generationContext)
	outputText, err = s.generator.Generate(ctx, input.Model, repairedContext, input.GenerationOptions)
	if err != nil {
		return "", err
	}
	if containsRawToolCallMarkupText(outputText) {
		return "", &llama.InvalidResponseError{Message: "llama assistant content contained raw tool-call markup"}
	}
	return outputText, nil
}

func buildLocalTextGenerationContext(items []domain.Item, maxToolOutputSummaryBytes int) ([]domain.Item, bool, error) {
	projected, err := domain.ProjectLocalTextGenerationContext(items)
	if err != nil {
		return nil, false, err
	}

	summary, ok := localToolOutputSummary(items, maxToolOutputSummaryBytes)
	if !ok {
		return projected, false, nil
	}

	out := make([]domain.Item, 0, len(projected)+2)
	out = append(out, domain.NewInputTextMessage("system", "Local tool outputs are provided below as data. Use them to answer the original request. Do not call tools, do not print tool-call templates, and do not expose internal tool-call markup."))
	out = append(out, projected...)
	out = append(out, domain.NewInputTextMessage("user", summary))
	return out, true, nil
}

func localToolOutputSummary(items []domain.Item, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", false
	}

	summaryLimit := maxBytes
	reserveTruncationNotice := maxBytes > len(localToolOutputSummaryTruncationNotice)+len("Tool output data:\n\n")
	if reserveTruncationNotice {
		summaryLimit = maxBytes - len(localToolOutputSummaryTruncationNotice)
	}

	var builder strings.Builder
	builder.WriteString("Tool output data:\n\n")
	wroteOutput := false
	for _, item := range items {
		if !isLocalTextGenerationToolOutput(item.Type) {
			continue
		}
		output := strings.TrimSpace(localTextGenerationToolOutput(item))
		if output == "" {
			continue
		}

		header := strings.ToUpper(strings.ReplaceAll(item.Type, "_", " "))
		if callID := strings.TrimSpace(item.CallID()); callID != "" {
			header += " (" + callID + ")"
		}
		part := header + ":\n" + output
		if wroteOutput {
			part = "\n\n" + part
		}
		truncated := writeBoundedLocalToolSummary(&builder, part, summaryLimit)
		wroteOutput = true
		if truncated {
			if reserveTruncationNotice {
				builder.WriteString(localToolOutputSummaryTruncationNotice)
			}
			break
		}
	}
	if !wroteOutput {
		return "", false
	}
	return builder.String(), true
}

func writeBoundedLocalToolSummary(builder *strings.Builder, text string, maxBytes int) bool {
	remaining := maxBytes - builder.Len()
	if remaining <= 0 {
		return true
	}
	if len(text) <= remaining {
		builder.WriteString(text)
		return false
	}
	builder.WriteString(text[:remaining])
	return true
}

func isLocalTextGenerationToolOutput(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call_output", "custom_tool_call_output", "shell_call_output", "apply_patch_call_output":
		return true
	default:
		return false
	}
}

func localTextGenerationToolOutput(item domain.Item) string {
	switch strings.TrimSpace(item.Type) {
	case "apply_patch_call_output":
		status := strings.TrimSpace(item.StringField("status"))
		output := strings.TrimSpace(stringifyLocalGenerationOutput(item.OutputRaw()))
		if status == "" {
			return output
		}
		if output == "" {
			return "status: " + status
		}
		return "status: " + status + "\n" + output
	default:
		return stringifyLocalGenerationOutput(item.OutputRaw())
	}
}

func stringifyLocalGenerationOutput(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err == nil {
			return text
		}
	}

	var parts []map[string]any
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			text := strings.TrimSpace(localGenerationStringValue(part["text"]))
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(text)
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}

	compact, err := domain.CompactJSON(trimmed)
	if err != nil {
		return string(trimmed)
	}
	return compact
}

func localGenerationStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func appendRawToolMarkupRepairInstruction(items []domain.Item) []domain.Item {
	out := append([]domain.Item(nil), items...)
	out = append(out, domain.NewInputTextMessage("system", "The previous draft attempted to print internal tool-call markup as plain text. Discard that draft. Produce only the final plain-text answer from the available tool output. Do not call tools and do not print tool markers."))
	return out
}

func containsRawToolCallMarkupText(text string) bool {
	for _, marker := range rawToolCallMarkupTextMarkers() {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func rawToolCallMarkupTextMarkers() []string {
	return []string{
		"<|tool_call",
		"<|tool_calls_section",
		"<tool_call",
		"</tool_call>",
		"<tool_code>",
		"<invoke name=",
		"<read_file>",
		"</read_file>",
		"<patch>",
		"</patch>",
		"<bash>",
		"</bash>",
	}
}

func (s *ResponseService) CreateWarmup(ctx context.Context, input CreateResponseInput) (domain.Response, error) {
	prepared, err := s.prepareCreate(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}
	prepared, err = s.applyAutomaticCompaction(ctx, input, prepared)
	if err != nil {
		return domain.Response{}, err
	}
	metadata, err := domain.NormalizeResponseMetadata(input.Metadata)
	if err != nil {
		return domain.Response{}, err
	}

	now := domain.NowUTC()
	store := input.Store == nil || *input.Store
	response := domain.Response{
		ID:                 prepared.ResponseID,
		Object:             "response",
		CreatedAt:          prepared.CreatedAt.Unix(),
		Status:             "completed",
		CompletedAt:        domain.Int64Ptr(now.Unix()),
		Model:              input.Model,
		Output:             []domain.Item{},
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       domain.NewConversationReference(input.ConversationID),
		Background:         domain.BoolPtr(false),
		Store:              domain.BoolPtr(store),
		Text:               domain.InferResponseTextConfigFromRequestJSON(input.RequestJSON),
		Metadata:           metadata,
		OutputText:         "",
	}
	response = domain.HydrateResponseRequestSurface(response, input.RequestJSON)

	prepared.NormalizedInput, err = domain.EnsureItemIDs(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	prepared.EffectiveInput, err = buildStoredEffectiveInputItems(prepared.EffectiveInput, prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	responseJSON, err := marshalResponseJSON(response)
	if err != nil {
		return domain.Response{}, err
	}
	stored := domain.StoredResponse{
		ID:                   response.ID,
		Model:                response.Model,
		RequestJSON:          input.RequestJSON,
		ResponseJSON:         responseJSON,
		NormalizedInputItems: prepared.NormalizedInput,
		EffectiveInputItems:  prepared.EffectiveInput,
		Output:               response.Output,
		OutputText:           response.OutputText,
		PreviousResponseID:   response.PreviousResponseID,
		ConversationID:       domain.ConversationReferenceID(response.Conversation),
		Store:                store,
		CreatedAt:            formatUnixTime(response.CreatedAt),
		CompletedAt:          formatResponseCompletedAt(response),
	}
	persistShadow := input.ForceShadowStore || input.PreviousResponseID != "" || input.ConversationID != "" || store

	switch {
	case input.ConversationID != "":
		if err := s.conversations.SaveResponseAndAppendConversation(ctx, prepared.Conversation, stored, prepared.NormalizedInput, response.Output); err != nil {
			return domain.Response{}, err
		}
	case persistShadow:
		if err := s.responses.SaveResponse(ctx, stored); err != nil {
			return domain.Response{}, err
		}
	}

	return response, nil
}

func (s *ResponseService) prepareCreate(ctx context.Context, input CreateResponseInput) (preparedResponse, error) {
	preparedContext, err := s.PrepareCreateContext(ctx, input)
	if err != nil {
		return preparedResponse{}, err
	}

	responseID, err := domain.NewPrefixedID("resp")
	if err != nil {
		return preparedResponse{}, fmt.Errorf("generate response id: %w", err)
	}

	return preparedResponse{
		ResponseID:      responseID,
		NormalizedInput: preparedContext.NormalizedInput,
		EffectiveInput:  preparedContext.EffectiveInput,
		ContextItems:    preparedContext.ContextItems,
		Conversation:    preparedContext.Conversation,
		CreatedAt:       domain.NowUTC(),
	}, nil
}

func (s *ResponseService) completeCreate(ctx context.Context, prepared preparedResponse, generationContext []domain.Item, input CreateResponseInput, outputText string) (domain.Response, error) {
	response := domain.NewResponse(prepared.ResponseID, input.Model, outputText, input.PreviousResponseID, input.ConversationID, prepared.CreatedAt.Unix())
	var err error
	response, err = s.FinalizeLocalResponse(input, generationContext, response)
	if err != nil {
		return domain.Response{}, err
	}
	metadata, err := domain.NormalizeResponseMetadata(input.Metadata)
	if err != nil {
		return domain.Response{}, err
	}
	response.Metadata = metadata
	response.Store = domain.BoolPtr(input.Store == nil || *input.Store)
	response.Background = domain.BoolPtr(input.Background != nil && *input.Background)
	response = domain.HydrateResponseRequestSurface(response, input.RequestJSON)
	prepared.NormalizedInput, err = domain.EnsureItemIDs(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	prepared.EffectiveInput, err = buildStoredEffectiveInputItems(prepared.EffectiveInput, prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	if len(prepared.OutputPrefix) > 0 {
		response.Output = append(append([]domain.Item(nil), prepared.OutputPrefix...), response.Output...)
	}
	response.Output, err = domain.EnsureItemIDs(response.Output)
	if err != nil {
		return domain.Response{}, err
	}
	responseJSON, err := marshalResponseJSON(response)
	if err != nil {
		return domain.Response{}, err
	}
	stored := domain.StoredResponse{
		ID:                   response.ID,
		Model:                response.Model,
		RequestJSON:          input.RequestJSON,
		ResponseJSON:         responseJSON,
		NormalizedInputItems: prepared.NormalizedInput,
		EffectiveInputItems:  prepared.EffectiveInput,
		Output:               response.Output,
		OutputText:           response.OutputText,
		PreviousResponseID:   response.PreviousResponseID,
		ConversationID:       domain.ConversationReferenceID(response.Conversation),
		Store:                input.Store == nil || *input.Store,
		CreatedAt:            formatUnixTime(response.CreatedAt),
		CompletedAt:          formatResponseCompletedAt(response),
	}
	persistShadow := input.ForceShadowStore || input.PreviousResponseID != "" || input.ConversationID != "" || stored.Store

	switch {
	case input.ConversationID != "":
		if err := s.conversations.SaveResponseAndAppendConversation(ctx, prepared.Conversation, stored, prepared.NormalizedInput, response.Output); err != nil {
			return domain.Response{}, err
		}
	case persistShadow:
		if err := s.responses.SaveResponse(ctx, stored); err != nil {
			return domain.Response{}, err
		}
	}

	return response, nil
}

type preparedResponse struct {
	ResponseID      string
	NormalizedInput []domain.Item
	EffectiveInput  []domain.Item
	ContextItems    []domain.Item
	OutputPrefix    []domain.Item
	Conversation    domain.Conversation
	CreatedAt       time.Time
}

func (s *ResponseService) applyAutomaticCompaction(ctx context.Context, input CreateResponseInput, prepared preparedResponse) (preparedResponse, error) {
	threshold, ok, err := parseCompactionThreshold(input.ContextManagement)
	if err != nil || !ok {
		return prepared, err
	}

	inputTokens, err := domain.EstimateSyntheticTokenCount(prepared.ContextItems)
	if err != nil {
		return prepared, err
	}
	if inputTokens <= threshold {
		return prepared, nil
	}

	baseItems := priorEffectiveItems(prepared.EffectiveInput, prepared.NormalizedInput)
	if len(baseItems) == 0 {
		return prepared, nil
	}

	result, err := s.compactor.Compact(ctx, baseItems)
	if err != nil {
		return prepared, err
	}

	prepared.EffectiveInput = make([]domain.Item, 0, 1+len(prepared.NormalizedInput))
	prepared.EffectiveInput = append(prepared.EffectiveInput, result.Item)
	prepared.EffectiveInput = append(prepared.EffectiveInput, prepared.NormalizedInput...)
	prepared.ContextItems = domain.AppendCurrentRequestContext(result.Expanded, input.Instructions, prepared.NormalizedInput)
	prepared.OutputPrefix = append(prepared.OutputPrefix, result.Item)
	return prepared, nil
}

func parseCompactionThreshold(raw json.RawMessage) (int, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false, nil
	}

	var entries []struct {
		Type             string `json:"type"`
		CompactThreshold int    `json:"compact_threshold"`
	}
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return 0, false, domain.NewValidationError("context_management", "context_management must be an array")
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Type) != "compaction" {
			continue
		}
		if entry.CompactThreshold <= 0 {
			return 0, false, domain.NewValidationError("context_management", "context_management compaction policy requires compact_threshold > 0")
		}
		return entry.CompactThreshold, true, nil
	}
	return 0, false, nil
}

func priorEffectiveItems(effectiveInput, normalizedInput []domain.Item) []domain.Item {
	if len(effectiveInput) == 0 {
		return nil
	}
	if len(normalizedInput) == 0 {
		return append([]domain.Item(nil), effectiveInput...)
	}
	if len(effectiveInput) <= len(normalizedInput) {
		return nil
	}
	return append([]domain.Item(nil), effectiveInput[:len(effectiveInput)-len(normalizedInput)]...)
}

func (s *ResponseService) Get(ctx context.Context, id string) (domain.Response, error) {
	if id == "" {
		return domain.Response{}, domain.NewValidationError("id", "response id is required")
	}

	stored, err := s.responses.GetResponse(ctx, id)
	if err != nil {
		return domain.Response{}, err
	}
	if !stored.Store {
		return domain.Response{}, ErrNotFound
	}
	return domain.ResponseFromStored(stored), nil
}

func (s *ResponseService) HasPreviousResponse(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, domain.NewValidationError("id", "response id is required")
	}

	if _, err := s.responses.GetResponse(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

func (s *ResponseService) GetInputItems(ctx context.Context, id string) ([]domain.Item, error) {
	if id == "" {
		return nil, domain.NewValidationError("id", "response id is required")
	}

	stored, err := s.responses.GetResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	if !stored.Store {
		return nil, ErrNotFound
	}
	if len(stored.EffectiveInputItems) > 0 {
		return stored.EffectiveInputItems, nil
	}
	return s.reconstructStoredInputItems(ctx, stored)
}

func (s *ResponseService) Delete(ctx context.Context, id string) (domain.ResponseDeletion, error) {
	if id == "" {
		return domain.ResponseDeletion{}, domain.NewValidationError("id", "response id is required")
	}
	stored, err := s.responses.GetResponse(ctx, id)
	if err != nil {
		return domain.ResponseDeletion{}, err
	}
	if !stored.Store {
		return domain.ResponseDeletion{}, ErrNotFound
	}
	if err := s.responses.DeleteResponse(ctx, id); err != nil {
		return domain.ResponseDeletion{}, err
	}
	return domain.ResponseDeletion{
		ID:      id,
		Object:  "response",
		Deleted: true,
	}, nil
}

func (s *ResponseService) Refresh(ctx context.Context, response domain.Response) (domain.Response, error) {
	if response.ID == "" {
		return domain.Response{}, domain.NewValidationError("id", "response id is required")
	}

	stored, err := s.responses.GetResponse(ctx, response.ID)
	if err != nil {
		return domain.Response{}, err
	}
	current := domain.ResponseFromStored(stored)
	response = mergeStoredResponseLifecycle(response, current)

	if strings.TrimSpace(response.OutputText) != "" && len(response.Output) == 0 {
		response.Output = []domain.Item{domain.NewOutputTextMessage(response.OutputText)}
	}
	if responseRequiresOutput(response) && len(response.Output) == 0 {
		return domain.Response{}, domain.NewValidationError("output", "assistant output is required")
	}
	if len(response.Output) > 0 {
		response.Output, err = domain.EnsureItemIDs(response.Output)
		if err != nil {
			return domain.Response{}, err
		}
	}

	responseJSON, err := marshalResponseJSON(response)
	if err != nil {
		return domain.Response{}, err
	}

	stored.Model = response.Model
	stored.ResponseJSON = responseJSON
	stored.Output = response.Output
	stored.OutputText = response.OutputText
	stored.PreviousResponseID = response.PreviousResponseID
	stored.ConversationID = domain.ConversationReferenceID(response.Conversation)
	if response.CreatedAt != 0 {
		stored.CreatedAt = formatUnixTime(response.CreatedAt)
	}
	stored.CompletedAt = formatResponseCompletedAt(response)

	if err := s.responses.SaveResponse(ctx, stored); err != nil {
		return domain.Response{}, err
	}

	return response, nil
}

func MapStorageError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, storage.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func domainItemsFromConversation(items []domain.ConversationItem) []domain.Item {
	out := make([]domain.Item, 0, len(items))
	lastTrustedCompaction := -1
	for _, item := range items {
		out = append(out, item.Item)
		if isTrustedConversationCompaction(item) {
			lastTrustedCompaction = len(out) - 1
		}
	}
	if lastTrustedCompaction >= 0 {
		return append([]domain.Item(nil), out[lastTrustedCompaction:]...)
	}
	return out
}

func buildLineageContextItems(lineage []domain.StoredResponse) []domain.Item {
	out := make([]domain.Item, 0, len(lineage)*2)
	for _, response := range lineage {
		out = append(out, response.NormalizedInputItems...)
		outputStart := len(out)
		out = append(out, response.Output...)
		lastTrustedCompaction := -1
		for idx, item := range response.Output {
			if item.Type == "compaction" {
				lastTrustedCompaction = idx
			}
		}
		if lastTrustedCompaction >= 0 {
			trimStart := outputStart + lastTrustedCompaction
			if trimStart > 0 {
				out = append([]domain.Item(nil), out[trimStart:]...)
			}
		}
	}
	return out
}

func isTrustedConversationCompaction(item domain.ConversationItem) bool {
	return item.Item.Type == "compaction" && item.Source == "response_output"
}

func buildStoredEffectiveInputItems(effectiveInput, normalizedInput []domain.Item) ([]domain.Item, error) {
	if len(effectiveInput) == 0 {
		return domain.EnsureItemIDs(normalizedInput)
	}

	out := append([]domain.Item(nil), effectiveInput...)
	if len(normalizedInput) > 0 && len(out) >= len(normalizedInput) {
		copy(out[len(out)-len(normalizedInput):], normalizedInput)
	}
	return domain.EnsureItemIDs(out)
}

func (s *ResponseService) reconstructStoredInputItems(ctx context.Context, stored domain.StoredResponse) ([]domain.Item, error) {
	if stored.PreviousResponseID != "" {
		lineage, err := s.responses.GetResponseLineage(ctx, stored.PreviousResponseID, s.limits.StoredLineageMaxItems)
		if err == nil {
			items := make([]domain.Item, 0, len(stored.NormalizedInputItems)+(len(lineage)*2))
			for _, response := range lineage {
				items = append(items, response.NormalizedInputItems...)
				items = append(items, response.Output...)
			}
			items = append(items, stored.NormalizedInputItems...)
			return domain.EnsureItemIDs(items)
		}
	}

	if stored.ConversationID != "" {
		_, conversationItems, err := s.conversations.GetConversation(ctx, stored.ConversationID)
		if err == nil {
			if items, ok := conversationInputItemsPrefix(conversationItems, stored); ok {
				return domain.EnsureItemIDs(items)
			}
		}
	}

	return domain.EnsureItemIDs(stored.NormalizedInputItems)
}

func conversationInputItemsPrefix(items []domain.ConversationItem, stored domain.StoredResponse) ([]domain.Item, bool) {
	if len(items) == 0 {
		return nil, false
	}

	inputIDs := make(map[string]struct{}, len(stored.NormalizedInputItems))
	for _, item := range stored.NormalizedInputItems {
		id := strings.TrimSpace(item.ID())
		if id != "" {
			inputIDs[id] = struct{}{}
		}
	}
	lastInputIndex := -1
	if len(inputIDs) > 0 {
		for idx, item := range items {
			if _, ok := inputIDs[item.ID]; ok {
				lastInputIndex = idx
			}
		}
	}
	if lastInputIndex >= 0 {
		return domainItemsFromConversation(items[:lastInputIndex+1]), true
	}

	outputIDs := make(map[string]struct{}, len(stored.Output))
	for _, item := range stored.Output {
		id := strings.TrimSpace(item.ID())
		if id != "" {
			outputIDs[id] = struct{}{}
		}
	}
	if len(outputIDs) > 0 {
		for idx, item := range items {
			if _, ok := outputIDs[item.ID]; ok {
				return domainItemsFromConversation(items[:idx]), true
			}
		}
	}

	return nil, false
}

func MapGeneratorError(err error) error {
	if err == nil {
		return nil
	}
	var (
		timeoutErr     *llama.TimeoutError
		upstreamErr    *llama.UpstreamError
		invalidRespErr *llama.InvalidResponseError
	)
	switch {
	case errors.As(err, &timeoutErr):
		return ErrUpstreamTimeout
	case errors.As(err, &upstreamErr), errors.As(err, &invalidRespErr):
		return ErrUpstreamFailure
	default:
		return err
	}
}

func responseRequiresOutput(response domain.Response) bool {
	return response.Status == "" || strings.EqualFold(strings.TrimSpace(response.Status), "completed")
}

func marshalResponseJSON(response domain.Response) (string, error) {
	raw, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("marshal response json: %w", err)
	}
	return string(raw), nil
}

func formatUnixTime(unixSeconds int64) string {
	return domain.FormatTime(time.Unix(unixSeconds, 0).UTC())
}

func formatResponseCompletedAt(response domain.Response) string {
	if response.CompletedAt == nil {
		return ""
	}
	return formatUnixTime(*response.CompletedAt)
}

func mergeStoredResponseLifecycle(next domain.Response, current domain.Response) domain.Response {
	if next.ID == "" {
		next.ID = current.ID
	}
	if next.Object == "" {
		next.Object = "response"
	}
	if next.CreatedAt == 0 {
		next.CreatedAt = current.CreatedAt
	}
	if next.Status == "" {
		next.Status = current.Status
	}
	if next.CompletedAt == nil {
		next.CompletedAt = current.CompletedAt
	}
	if rawMessageEmpty(next.Error) {
		next.Error = current.Error
	}
	if rawMessageEmpty(next.IncompleteDetails) {
		next.IncompleteDetails = current.IncompleteDetails
	}
	if next.Model == "" {
		next.Model = current.Model
	}
	if len(next.Output) == 0 {
		next.Output = current.Output
	}
	if next.PreviousResponseID == "" {
		next.PreviousResponseID = current.PreviousResponseID
	}
	if next.Conversation == nil {
		next.Conversation = current.Conversation
	}
	if next.Background == nil {
		next.Background = current.Background
	}
	if next.Store == nil {
		next.Store = current.Store
	}
	if len(bytes.TrimSpace(next.Text)) == 0 || bytes.Equal(bytes.TrimSpace(next.Text), []byte("null")) {
		next.Text = current.Text
	}
	if rawMessageEmpty(next.Usage) {
		next.Usage = current.Usage
	}
	if rawMessageEmpty(next.Instructions) {
		next.Instructions = current.Instructions
	}
	if rawMessageEmpty(next.MaxOutputTokens) {
		next.MaxOutputTokens = current.MaxOutputTokens
	}
	if rawMessageEmpty(next.MaxToolCalls) {
		next.MaxToolCalls = current.MaxToolCalls
	}
	if rawMessageEmpty(next.ParallelToolCalls) {
		next.ParallelToolCalls = current.ParallelToolCalls
	}
	if rawMessageEmpty(next.Prompt) {
		next.Prompt = current.Prompt
	}
	if rawMessageEmpty(next.PromptCacheKey) {
		next.PromptCacheKey = current.PromptCacheKey
	}
	if rawMessageEmpty(next.PromptCacheRetention) {
		next.PromptCacheRetention = current.PromptCacheRetention
	}
	if rawMessageEmpty(next.Reasoning) {
		next.Reasoning = current.Reasoning
	}
	if rawMessageEmpty(next.SafetyIdentifier) {
		next.SafetyIdentifier = current.SafetyIdentifier
	}
	if rawMessageEmpty(next.ServiceTier) {
		next.ServiceTier = current.ServiceTier
	}
	if rawMessageEmpty(next.Temperature) {
		next.Temperature = current.Temperature
	}
	if rawMessageEmpty(next.ToolChoice) {
		next.ToolChoice = current.ToolChoice
	}
	if rawMessageEmpty(next.Tools) {
		next.Tools = current.Tools
	}
	if rawMessageEmpty(next.TopLogprobs) {
		next.TopLogprobs = current.TopLogprobs
	}
	if rawMessageEmpty(next.TopP) {
		next.TopP = current.TopP
	}
	if rawMessageEmpty(next.Truncation) {
		next.Truncation = current.Truncation
	}
	if rawMessageEmpty(next.User) {
		next.User = current.User
	}
	if next.Metadata == nil {
		next.Metadata = current.Metadata
	}
	if next.Metadata == nil {
		next.Metadata = map[string]string{}
	}
	if next.OutputText == "" {
		next.OutputText = current.OutputText
	}
	return next
}

func rawMessageEmpty(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0
}
