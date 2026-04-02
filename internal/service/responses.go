package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/storage/sqlite"
)

type Generator interface {
	Generate(ctx context.Context, model string, items []domain.Item, options map[string]json.RawMessage) (string, error)
	GenerateStream(ctx context.Context, model string, items []domain.Item, options map[string]json.RawMessage, onDelta func(string) error) error
}

type ResponseStore interface {
	GetResponse(ctx context.Context, id string) (domain.StoredResponse, error)
	GetResponseLineage(ctx context.Context, id string) ([]domain.StoredResponse, error)
	SaveResponse(ctx context.Context, response domain.StoredResponse) error
}

type ConversationStore interface {
	GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error)
	SaveResponseAndAppendConversation(ctx context.Context, conversation domain.Conversation, response domain.StoredResponse, input []domain.Item, output []domain.Item) error
}

type CreateResponseInput struct {
	Model              string
	Input              json.RawMessage
	Store              *bool
	Stream             *bool
	PreviousResponseID string
	ConversationID     string
	Instructions       string
	RequestJSON        string
	GenerationOptions  map[string]json.RawMessage
}

type PreparedResponseContext struct {
	NormalizedInput []domain.Item
	ContextItems    []domain.Item
	Conversation    domain.Conversation
	ToolCallRefs    map[string]domain.ToolCallReference
}

type StreamHooks struct {
	OnCreated func(response domain.Response) error
	OnDelta   func(delta string) error
}

type ResponseService struct {
	responses     ResponseStore
	conversations ConversationStore
	generator     Generator
}

func NewResponseService(responses ResponseStore, conversations ConversationStore, generator Generator) *ResponseService {
	return &ResponseService{
		responses:     responses,
		conversations: conversations,
		generator:     generator,
	}
}

func (s *ResponseService) Create(ctx context.Context, input CreateResponseInput) (domain.Response, error) {
	prepared, err := s.prepareCreate(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}
	if err := domain.ValidateLocalShimItems(prepared.ContextItems); err != nil {
		return domain.Response{}, err
	}

	outputText, err := s.generator.Generate(ctx, input.Model, prepared.ContextItems, input.GenerationOptions)
	if err != nil {
		return domain.Response{}, err
	}

	return s.completeCreate(ctx, prepared, input, outputText)
}

func (s *ResponseService) PrepareCreateContext(ctx context.Context, input CreateResponseInput) (PreparedResponseContext, error) {
	if input.Model == "" {
		return PreparedResponseContext{}, domain.NewValidationError("model", "model is required")
	}
	if input.PreviousResponseID != "" && input.ConversationID != "" {
		return PreparedResponseContext{}, domain.NewValidationError("previous_response_id", "previous_response_id and conversation are mutually exclusive")
	}

	normalizedInput, err := domain.NormalizeInput(input.Input)
	if err != nil {
		return PreparedResponseContext{}, err
	}

	var (
		baseItems    []domain.Item
		contextItems []domain.Item
		conversation domain.Conversation
		toolCallRefs map[string]domain.ToolCallReference
	)

	switch {
	case input.PreviousResponseID != "":
		lineage, err := s.responses.GetResponseLineage(ctx, input.PreviousResponseID)
		if err != nil {
			return PreparedResponseContext{}, err
		}
		for _, response := range lineage {
			baseItems = append(baseItems, response.NormalizedInputItems...)
			baseItems = append(baseItems, response.Output...)
		}
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
	toolCallRefs = domain.CollectToolCallReferences(baseItems)
	normalizedInput, err = domain.CanonicalizeToolOutputs(normalizedInput, toolCallRefs)
	if err != nil {
		return PreparedResponseContext{}, err
	}
	contextItems = domain.AppendCurrentRequestContext(baseItems, input.Instructions, normalizedInput)

	return PreparedResponseContext{
		NormalizedInput: normalizedInput,
		ContextItems:    contextItems,
		Conversation:    conversation,
		ToolCallRefs:    toolCallRefs,
	}, nil
}

func (s *ResponseService) SaveExternalResponse(ctx context.Context, prepared PreparedResponseContext, input CreateResponseInput, response domain.Response) (domain.Response, error) {
	response.Object = "response"
	if response.Model == "" {
		response.Model = input.Model
	}
	if response.PreviousResponseID == "" && input.PreviousResponseID != "" {
		response.PreviousResponseID = input.PreviousResponseID
	}
	if response.Conversation == "" && input.ConversationID != "" {
		response.Conversation = input.ConversationID
	}
	if strings.TrimSpace(response.OutputText) != "" && len(response.Output) == 0 {
		response.Output = []domain.Item{domain.NewOutputTextMessage(response.OutputText)}
	}
	if len(response.Output) == 0 {
		return domain.Response{}, domain.NewValidationError("output", "assistant output is required")
	}
	normalizedInput, err := domain.EnsureItemIDs(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	response.Output, err = domain.EnsureItemIDs(response.Output)
	if err != nil {
		return domain.Response{}, err
	}

	now := domain.FormatTime(domain.NowUTC())
	shouldStore := input.PreviousResponseID != "" || input.ConversationID != "" || input.Store == nil || *input.Store
	stored := domain.StoredResponse{
		ID:                   response.ID,
		Model:                response.Model,
		RequestJSON:          input.RequestJSON,
		NormalizedInputItems: normalizedInput,
		Output:               response.Output,
		OutputText:           response.OutputText,
		PreviousResponseID:   response.PreviousResponseID,
		ConversationID:       response.Conversation,
		Store:                shouldStore,
		CreatedAt:            now,
		CompletedAt:          now,
	}

	switch {
	case input.ConversationID != "":
		if err := s.conversations.SaveResponseAndAppendConversation(ctx, prepared.Conversation, stored, normalizedInput, response.Output); err != nil {
			return domain.Response{}, err
		}
	case shouldStore:
		if err := s.responses.SaveResponse(ctx, stored); err != nil {
			return domain.Response{}, err
		}
	}

	return response, nil
}

func (s *ResponseService) CreateStream(ctx context.Context, input CreateResponseInput, hooks StreamHooks) (domain.Response, error) {
	prepared, err := s.prepareCreate(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}
	if err := domain.ValidateLocalShimItems(prepared.ContextItems); err != nil {
		return domain.Response{}, err
	}

	created := domain.Response{
		ID:                 prepared.ResponseID,
		Object:             "response",
		Model:              input.Model,
		PreviousResponseID: input.PreviousResponseID,
		Conversation:       input.ConversationID,
		OutputText:         "",
		Output:             nil,
	}
	if hooks.OnCreated != nil {
		if err := hooks.OnCreated(created); err != nil {
			return domain.Response{}, err
		}
	}

	var builder strings.Builder
	err = s.generator.GenerateStream(ctx, input.Model, prepared.ContextItems, input.GenerationOptions, func(delta string) error {
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

	return s.completeCreate(ctx, prepared, input, outputText)
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
		ContextItems:    preparedContext.ContextItems,
		Conversation:    preparedContext.Conversation,
	}, nil
}

func (s *ResponseService) completeCreate(ctx context.Context, prepared preparedResponse, input CreateResponseInput, outputText string) (domain.Response, error) {
	response := domain.NewResponse(prepared.ResponseID, input.Model, outputText, input.PreviousResponseID, input.ConversationID)
	var err error
	prepared.NormalizedInput, err = domain.EnsureItemIDs(prepared.NormalizedInput)
	if err != nil {
		return domain.Response{}, err
	}
	response.Output, err = domain.EnsureItemIDs(response.Output)
	if err != nil {
		return domain.Response{}, err
	}
	now := domain.FormatTime(domain.NowUTC())
	stored := domain.StoredResponse{
		ID:                   response.ID,
		Model:                response.Model,
		RequestJSON:          input.RequestJSON,
		NormalizedInputItems: prepared.NormalizedInput,
		Output:               response.Output,
		OutputText:           response.OutputText,
		PreviousResponseID:   response.PreviousResponseID,
		ConversationID:       response.Conversation,
		Store:                input.Store == nil || *input.Store,
		CreatedAt:            now,
		CompletedAt:          now,
	}

	switch {
	case input.ConversationID != "":
		if err := s.conversations.SaveResponseAndAppendConversation(ctx, prepared.Conversation, stored, prepared.NormalizedInput, response.Output); err != nil {
			return domain.Response{}, err
		}
	case input.PreviousResponseID != "":
		if err := s.responses.SaveResponse(ctx, stored); err != nil {
			return domain.Response{}, err
		}
	default:
		if stored.Store {
			if err := s.responses.SaveResponse(ctx, stored); err != nil {
				return domain.Response{}, err
			}
		}
	}

	return response, nil
}

type preparedResponse struct {
	ResponseID      string
	NormalizedInput []domain.Item
	ContextItems    []domain.Item
	Conversation    domain.Conversation
}

func (s *ResponseService) Get(ctx context.Context, id string) (domain.Response, error) {
	if id == "" {
		return domain.Response{}, domain.NewValidationError("id", "response id is required")
	}

	stored, err := s.responses.GetResponse(ctx, id)
	if err != nil {
		return domain.Response{}, err
	}
	return domain.ResponseFromStored(stored), nil
}

func (s *ResponseService) GetInputItems(ctx context.Context, id string) ([]domain.Item, error) {
	if id == "" {
		return nil, domain.NewValidationError("id", "response id is required")
	}

	stored, err := s.responses.GetResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	return stored.NormalizedInputItems, nil
}

func MapStorageError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sqlite.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, sqlite.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func domainItemsFromConversation(items []domain.ConversationItem) []domain.Item {
	out := make([]domain.Item, 0, len(items))
	for _, item := range items {
		out = append(out, item.Item)
	}
	return out
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
