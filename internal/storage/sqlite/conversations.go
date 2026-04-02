package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
)

func (s *Store) CreateConversation(ctx context.Context, conversation domain.Conversation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create conversation tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversations(id, version, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, conversation.ID, conversation.Version, conversation.CreatedAt, conversation.UpdatedAt); err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}

	for seq, item := range conversation.Items {
		itemID := item.ID()
		if itemID == "" {
			var err error
			itemID, err = domain.NewPrefixedID("item")
			if err != nil {
				return fmt.Errorf("generate conversation item id: %w", err)
			}
			item, err = item.WithID(itemID)
			if err != nil {
				return fmt.Errorf("assign conversation item id: %w", err)
			}
		}
		if err := insertConversationItem(ctx, tx, domain.ConversationItem{
			ID:             itemID,
			ConversationID: conversation.ID,
			Seq:            seq,
			Source:         "seed",
			Role:           item.Role,
			ItemType:       item.Type,
			Item:           item,
			CreatedAt:      conversation.CreatedAt,
		}); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create conversation: %w", err)
	}
	return nil
}

func (s *Store) GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error) {
	var conversation domain.Conversation
	err := s.db.QueryRowContext(ctx, `
		SELECT id, version, created_at, updated_at
		FROM conversations
		WHERE id = ?
	`, id).Scan(&conversation.ID, &conversation.Version, &conversation.CreatedAt, &conversation.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Conversation{}, nil, ErrNotFound
		}
		return domain.Conversation{}, nil, fmt.Errorf("select conversation: %w", err)
	}
	conversation.Object = "conversation"

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, seq, source, COALESCE(role, ''), item_type, item_json, created_at
		FROM conversation_items
		WHERE conversation_id = ?
		ORDER BY seq ASC
	`, id)
	if err != nil {
		return domain.Conversation{}, nil, fmt.Errorf("select conversation items: %w", err)
	}
	defer rows.Close()

	items, err := scanConversationItems(rows)
	if err != nil {
		return domain.Conversation{}, nil, err
	}
	conversation.Items = make([]domain.Item, 0, len(items))
	for _, item := range items {
		conversation.Items = append(conversation.Items, item.Item)
	}

	return conversation, items, nil
}

func (s *Store) ListConversationItems(ctx context.Context, query domain.ListConversationItemsQuery) (domain.ConversationItemPage, error) {
	if err := s.ensureConversationExists(ctx, query.ConversationID); err != nil {
		return domain.ConversationItemPage{}, err
	}

	cursorSeq := -1
	if query.After != "" {
		var err error
		cursorSeq, err = s.lookupConversationItemSeq(ctx, query.ConversationID, query.After)
		if err != nil {
			return domain.ConversationItemPage{}, err
		}
	}

	order := strings.ToUpper(query.Order)
	whereParts := []string{"conversation_id = ?"}
	args := []any{query.ConversationID}
	if query.After != "" {
		operator := ">"
		if query.Order == domain.ConversationItemOrderDesc {
			operator = "<"
		}
		whereParts = append(whereParts, fmt.Sprintf("seq %s ?", operator))
		args = append(args, cursorSeq)
	}
	args = append(args, query.Limit+1)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, conversation_id, seq, source, COALESCE(role, ''), item_type, item_json, created_at
		FROM conversation_items
		WHERE %s
		ORDER BY seq %s
		LIMIT ?
	`, strings.Join(whereParts, " AND "), order), args...)
	if err != nil {
		return domain.ConversationItemPage{}, fmt.Errorf("select paged conversation items: %w", err)
	}
	defer rows.Close()

	items, err := scanConversationItems(rows)
	if err != nil {
		return domain.ConversationItemPage{}, err
	}

	page := domain.ConversationItemPage{
		Items: make([]domain.ConversationItem, 0, min(len(items), query.Limit)),
	}
	if len(items) > query.Limit {
		page.HasMore = true
		items = items[:query.Limit]
	}
	page.Items = append(page.Items, items...)

	return page, nil
}

func (s *Store) SaveResponseAndAppendConversation(
	ctx context.Context,
	conversation domain.Conversation,
	response domain.StoredResponse,
	input []domain.Item,
	output []domain.Item,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin conversation append tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	versionResult, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?
	`, response.CompletedAt, conversation.ID, conversation.Version)
	if err != nil {
		return fmt.Errorf("update conversation version: %w", err)
	}
	affected, err := versionResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("conversation version rows affected: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}

	if err := insertResponse(ctx, tx, response); err != nil {
		return err
	}

	nextSeq := len(conversation.Items)
	appendItems := domain.BuildConversationAppendItems(nextSeq, input, output)
	for i := range appendItems {
		appendItems[i].ConversationID = conversation.ID
		appendItems[i].CreatedAt = response.CompletedAt
		itemID := appendItems[i].Item.ID()
		if itemID == "" {
			var err error
			itemID, err = domain.NewPrefixedID("item")
			if err != nil {
				return fmt.Errorf("generate appended conversation item id: %w", err)
			}
			appendItems[i].Item, err = appendItems[i].Item.WithID(itemID)
			if err != nil {
				return fmt.Errorf("assign appended conversation item id: %w", err)
			}
		}
		appendItems[i].ID = itemID
		if err := insertConversationItem(ctx, tx, appendItems[i]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit conversation append: %w", err)
	}
	return nil
}

func insertConversationItem(ctx context.Context, tx *sql.Tx, item domain.ConversationItem) error {
	itemJSON, err := domain.MarshalStoredItem(item.Item)
	if err != nil {
		return fmt.Errorf("marshal conversation item: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_items(id, conversation_id, seq, source, role, item_type, item_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.ConversationID, item.Seq, item.Source, nullableString(item.Role), item.ItemType, string(itemJSON), item.CreatedAt); err != nil {
		return fmt.Errorf("insert conversation item: %w", err)
	}
	return nil
}

func (s *Store) ensureConversationExists(ctx context.Context, conversationID string) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM conversations
		WHERE id = ?
	`, conversationID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("select conversation existence: %w", err)
	}
	return nil
}

func (s *Store) lookupConversationItemSeq(ctx context.Context, conversationID, itemID string) (int, error) {
	var seq int
	if err := s.db.QueryRowContext(ctx, `
		SELECT seq
		FROM conversation_items
		WHERE conversation_id = ? AND id = ?
	`, conversationID, itemID).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, domain.NewValidationError("after", "after must reference an existing item in the conversation")
		}
		return 0, fmt.Errorf("select conversation item cursor: %w", err)
	}
	return seq, nil
}

func scanConversationItems(rows *sql.Rows) ([]domain.ConversationItem, error) {
	items := make([]domain.ConversationItem, 0, 8)
	for rows.Next() {
		var (
			item     domain.ConversationItem
			itemJSON string
		)
		if err := rows.Scan(
			&item.ID,
			&item.ConversationID,
			&item.Seq,
			&item.Source,
			&item.Role,
			&item.ItemType,
			&itemJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan conversation item: %w", err)
		}
		storedItem, err := domain.UnmarshalStoredItem([]byte(itemJSON))
		if err != nil {
			return nil, fmt.Errorf("unmarshal conversation item: %w", err)
		}
		item.Item = storedItem
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation items: %w", err)
	}
	return items, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
