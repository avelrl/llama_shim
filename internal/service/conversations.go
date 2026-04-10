package service

import (
	"context"
	"encoding/json"
	"fmt"

	"llama_shim/internal/domain"
)

const maxConversationItemsPerRequest = 20

type ConversationCreator interface {
	CreateConversation(ctx context.Context, conversation domain.Conversation) error
	GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error)
	ListConversationItems(ctx context.Context, query domain.ListConversationItemsQuery) (domain.ConversationItemPage, error)
	AppendConversationItems(ctx context.Context, conversation domain.Conversation, items []domain.Item, createdAt string) ([]domain.ConversationItem, error)
	GetConversationItem(ctx context.Context, conversationID, itemID string) (domain.ConversationItem, error)
	DeleteConversationItem(ctx context.Context, conversation domain.Conversation, itemID, updatedAt string) error
}

type ConversationService struct {
	store ConversationCreator
}

type CreateConversationInput struct {
	Items    []json.RawMessage
	Metadata json.RawMessage
}

func NewConversationService(store ConversationCreator) *ConversationService {
	return &ConversationService{store: store}
}

func (s *ConversationService) Get(ctx context.Context, id string) (domain.Conversation, error) {
	if id == "" {
		return domain.Conversation{}, domain.NewValidationError("conversation_id", "conversation id is required")
	}

	conversation, _, err := s.store.GetConversation(ctx, id)
	if err != nil {
		return domain.Conversation{}, MapStorageError(err)
	}
	return conversation, nil
}

func (s *ConversationService) Create(ctx context.Context, input CreateConversationInput) (domain.Conversation, error) {
	items := make([]domain.Item, 0, len(input.Items))
	if len(input.Items) > 0 {
		if len(input.Items) > maxConversationItemsPerRequest {
			return domain.Conversation{}, domain.NewValidationError("items", "items may include at most 20 items")
		}
		var err error
		items, err = domain.NormalizeConversationItems(input.Items)
		if err != nil {
			return domain.Conversation{}, err
		}
		items, err = domain.EnsureItemIDs(items)
		if err != nil {
			return domain.Conversation{}, err
		}
	}
	metadata, err := domain.NormalizeConversationMetadata(input.Metadata)
	if err != nil {
		return domain.Conversation{}, err
	}

	conversationID, err := domain.NewPrefixedID("conv")
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("generate conversation id: %w", err)
	}

	now := domain.FormatTime(domain.NowUTC())
	conversation := domain.Conversation{
		ID:        conversationID,
		Object:    "conversation",
		Metadata:  metadata,
		Items:     items,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.CreateConversation(ctx, conversation); err != nil {
		return domain.Conversation{}, err
	}
	return conversation, nil
}
