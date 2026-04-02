package service

import (
	"context"
	"encoding/json"
	"fmt"

	"llama_shim/internal/domain"
)

type ConversationCreator interface {
	CreateConversation(ctx context.Context, conversation domain.Conversation) error
	GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error)
	ListConversationItems(ctx context.Context, query domain.ListConversationItemsQuery) (domain.ConversationItemPage, error)
}

type ConversationService struct {
	store ConversationCreator
}

type CreateConversationInput struct {
	Items []json.RawMessage
}

func NewConversationService(store ConversationCreator) *ConversationService {
	return &ConversationService{store: store}
}

func (s *ConversationService) Create(ctx context.Context, input CreateConversationInput) (domain.Conversation, error) {
	items, err := domain.NormalizeConversationItems(input.Items)
	if err != nil {
		return domain.Conversation{}, err
	}
	items, err = domain.EnsureItemIDs(items)
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
