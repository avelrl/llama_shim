package service

import (
	"context"
	"encoding/json"

	"llama_shim/internal/domain"
)

func (s *ConversationService) ListItems(ctx context.Context, query domain.ListConversationItemsQuery) (domain.ConversationItemPage, error) {
	if query.ConversationID == "" {
		return domain.ConversationItemPage{}, domain.NewValidationError("conversation_id", "conversation id is required")
	}
	if query.Limit <= 0 {
		return domain.ConversationItemPage{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
	}
	switch query.Order {
	case "", domain.ConversationItemOrderDesc:
		query.Order = domain.ConversationItemOrderDesc
	case domain.ConversationItemOrderAsc:
	default:
		return domain.ConversationItemPage{}, domain.NewValidationError("order", "order must be one of asc or desc")
	}

	return s.store.ListConversationItems(ctx, query)
}

func (s *ConversationService) GetItem(ctx context.Context, conversationID, itemID string) (domain.ConversationItem, error) {
	if conversationID == "" {
		return domain.ConversationItem{}, domain.NewValidationError("conversation_id", "conversation id is required")
	}
	if itemID == "" {
		return domain.ConversationItem{}, domain.NewValidationError("item_id", "item id is required")
	}

	item, err := s.store.GetConversationItem(ctx, conversationID, itemID)
	if err != nil {
		return domain.ConversationItem{}, MapStorageError(err)
	}
	return item, nil
}

func (s *ConversationService) DeleteItem(ctx context.Context, conversationID, itemID string) (domain.Conversation, error) {
	if conversationID == "" {
		return domain.Conversation{}, domain.NewValidationError("conversation_id", "conversation id is required")
	}
	if itemID == "" {
		return domain.Conversation{}, domain.NewValidationError("item_id", "item id is required")
	}

	conversation, _, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return domain.Conversation{}, MapStorageError(err)
	}

	updatedAt := domain.FormatTime(domain.NowUTC())
	if err := s.store.DeleteConversationItem(ctx, conversation, itemID, updatedAt); err != nil {
		return domain.Conversation{}, MapStorageError(err)
	}

	conversation.Version++
	conversation.UpdatedAt = updatedAt
	return conversation, nil
}

func (s *ConversationService) AppendItems(ctx context.Context, conversationID string, rawItems []json.RawMessage) ([]domain.ConversationItem, error) {
	if conversationID == "" {
		return nil, domain.NewValidationError("conversation_id", "conversation id is required")
	}
	if len(rawItems) == 0 {
		return nil, domain.NewValidationError("items", "items must not be empty")
	}
	if len(rawItems) > maxConversationItemsPerRequest {
		return nil, domain.NewValidationError("items", "items may include at most 20 items")
	}

	conversation, _, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, MapStorageError(err)
	}

	items, err := domain.NormalizeConversationItems(rawItems)
	if err != nil {
		return nil, err
	}
	items, err = canonicalizeConversationAppendItems(items, domain.CollectToolCallReferences(conversation.Items))
	if err != nil {
		return nil, err
	}
	items, err = domain.EnsureItemIDs(items)
	if err != nil {
		return nil, err
	}

	storedItems, err := s.store.AppendConversationItems(ctx, conversation, items, domain.FormatTime(domain.NowUTC()))
	if err != nil {
		return nil, MapStorageError(err)
	}
	return storedItems, nil
}

func canonicalizeConversationAppendItems(items []domain.Item, refs map[string]domain.ToolCallReference) ([]domain.Item, error) {
	if len(items) == 0 {
		return nil, nil
	}

	knownRefs := make(map[string]domain.ToolCallReference, len(refs))
	for callID, ref := range refs {
		knownRefs[callID] = ref
	}

	out := make([]domain.Item, 0, len(items))
	for _, item := range items {
		canonicalized, err := domain.CanonicalizeToolOutputs([]domain.Item{item}, knownRefs)
		if err != nil {
			return nil, err
		}
		out = append(out, canonicalized[0])
		for callID, ref := range domain.CollectToolCallReferences(canonicalized) {
			knownRefs[callID] = ref
		}
	}

	return out, nil
}
