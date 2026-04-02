package service

import (
	"context"

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
