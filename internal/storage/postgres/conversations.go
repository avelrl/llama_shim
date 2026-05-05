package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
)

func (s *Store) CreateConversation(ctx context.Context, conversation domain.Conversation) error {
	metadataJSON, err := domain.MarshalConversationMetadata(conversation.Metadata)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres create conversation tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversations(id, version, metadata_json, created_at, updated_at)
		VALUES ($1, $2, $3::jsonb, $4, $5)
	`, conversation.ID, conversation.Version, metadataJSON, conversation.CreatedAt, conversation.UpdatedAt); err != nil {
		return fmt.Errorf("insert postgres conversation: %w", err)
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
		if err := insertPostgresConversationItem(ctx, tx, domain.ConversationItem{
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
		return fmt.Errorf("commit postgres create conversation: %w", err)
	}
	return nil
}

func (s *Store) GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error) {
	var (
		conversation domain.Conversation
		metadataJSON []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, version, COALESCE(metadata_json, '{}'::jsonb), created_at, updated_at
		FROM conversations
		WHERE id = $1
	`, id).Scan(&conversation.ID, &conversation.Version, &metadataJSON, &conversation.CreatedAt, &conversation.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Conversation{}, nil, ErrNotFound
		}
		return domain.Conversation{}, nil, fmt.Errorf("select postgres conversation: %w", err)
	}
	conversation.Object = "conversation"
	conversation.Metadata, err = domain.UnmarshalConversationMetadata(string(metadataJSON))
	if err != nil {
		return domain.Conversation{}, nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, seq, source, COALESCE(role, ''), item_type, item_json, created_at
		FROM conversation_items
		WHERE conversation_id = $1
		ORDER BY seq ASC
	`, id)
	if err != nil {
		return domain.Conversation{}, nil, fmt.Errorf("select postgres conversation items: %w", err)
	}
	defer rows.Close()

	items, err := scanPostgresConversationItems(rows)
	if err != nil {
		return domain.Conversation{}, nil, err
	}
	conversation.Items = make([]domain.Item, 0, len(items))
	for _, item := range items {
		conversation.Items = append(conversation.Items, item.Item)
	}

	return conversation, items, nil
}

func (s *Store) GetConversationItem(ctx context.Context, conversationID, itemID string) (domain.ConversationItem, error) {
	if err := s.ensurePostgresConversationExists(ctx, conversationID); err != nil {
		return domain.ConversationItem{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, seq, source, COALESCE(role, ''), item_type, item_json, created_at
		FROM conversation_items
		WHERE conversation_id = $1 AND id = $2
	`, conversationID, itemID)
	if err != nil {
		return domain.ConversationItem{}, fmt.Errorf("select postgres conversation item: %w", err)
	}
	defer rows.Close()

	items, err := scanPostgresConversationItems(rows)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	if len(items) == 0 {
		return domain.ConversationItem{}, ErrNotFound
	}
	return items[0], nil
}

func (s *Store) ListConversationItems(ctx context.Context, query domain.ListConversationItemsQuery) (domain.ConversationItemPage, error) {
	if err := s.ensurePostgresConversationExists(ctx, query.ConversationID); err != nil {
		return domain.ConversationItemPage{}, err
	}
	if query.Limit < 1 {
		query.Limit = 20
	}

	cursorSeq := -1
	if query.After != "" {
		var err error
		cursorSeq, err = s.lookupPostgresConversationItemSeq(ctx, query.ConversationID, query.After)
		if err != nil {
			return domain.ConversationItemPage{}, err
		}
	}

	order := strings.ToUpper(query.Order)
	if order != "DESC" {
		order = "ASC"
	}

	whereParts := []string{"conversation_id = $1"}
	args := []any{query.ConversationID}
	if query.After != "" {
		operator := ">"
		if query.Order == domain.ConversationItemOrderDesc {
			operator = "<"
		}
		whereParts = append(whereParts, fmt.Sprintf("seq %s $2", operator))
		args = append(args, cursorSeq)
	}
	limitPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, query.Limit+1)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, conversation_id, seq, source, COALESCE(role, ''), item_type, item_json, created_at
		FROM conversation_items
		WHERE %s
		ORDER BY seq %s
		LIMIT %s
	`, strings.Join(whereParts, " AND "), order, limitPlaceholder), args...)
	if err != nil {
		return domain.ConversationItemPage{}, fmt.Errorf("select paged postgres conversation items: %w", err)
	}
	defer rows.Close()

	items, err := scanPostgresConversationItems(rows)
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
		return fmt.Errorf("begin postgres conversation append tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	versionResult, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET version = version + 1, updated_at = $1
		WHERE id = $2 AND version = $3
	`, response.CompletedAt, conversation.ID, conversation.Version)
	if err != nil {
		return fmt.Errorf("update postgres conversation version: %w", err)
	}
	affected, err := versionResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres conversation version rows affected: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}

	if err := insertPostgresResponse(ctx, tx, response, false); err != nil {
		return err
	}

	nextSeq, err := nextPostgresConversationItemSeq(ctx, tx, conversation.ID)
	if err != nil {
		return err
	}
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
		if err := insertPostgresConversationItem(ctx, tx, appendItems[i]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres conversation append: %w", err)
	}
	return nil
}

func (s *Store) AppendConversationItems(
	ctx context.Context,
	conversation domain.Conversation,
	items []domain.Item,
	createdAt string,
) ([]domain.ConversationItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin postgres conversation item append tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	versionResult, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET version = version + 1, updated_at = $1
		WHERE id = $2 AND version = $3
	`, createdAt, conversation.ID, conversation.Version)
	if err != nil {
		return nil, fmt.Errorf("update postgres conversation version: %w", err)
	}
	affected, err := versionResult.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("postgres conversation version rows affected: %w", err)
	}
	if affected == 0 {
		return nil, ErrConflict
	}

	storedItems := make([]domain.ConversationItem, 0, len(items))
	nextSeq, err := nextPostgresConversationItemSeq(ctx, tx, conversation.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		itemID := item.ID()
		if itemID == "" {
			var err error
			itemID, err = domain.NewPrefixedID("item")
			if err != nil {
				return nil, fmt.Errorf("generate appended conversation item id: %w", err)
			}
			item, err = item.WithID(itemID)
			if err != nil {
				return nil, fmt.Errorf("assign appended conversation item id: %w", err)
			}
		}

		storedItem := domain.ConversationItem{
			ID:             itemID,
			ConversationID: conversation.ID,
			Seq:            nextSeq,
			Source:         "append",
			Role:           item.Role,
			ItemType:       item.Type,
			Item:           item,
			CreatedAt:      createdAt,
		}
		if err := insertPostgresConversationItem(ctx, tx, storedItem); err != nil {
			return nil, err
		}
		storedItems = append(storedItems, storedItem)
		nextSeq++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit postgres conversation item append: %w", err)
	}
	return storedItems, nil
}

func (s *Store) DeleteConversationItem(
	ctx context.Context,
	conversation domain.Conversation,
	itemID string,
	updatedAt string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres conversation item delete tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	versionResult, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET version = version + 1, updated_at = $1
		WHERE id = $2 AND version = $3
	`, updatedAt, conversation.ID, conversation.Version)
	if err != nil {
		return fmt.Errorf("update postgres conversation version: %w", err)
	}
	affected, err := versionResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres conversation version rows affected: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}

	deleteResult, err := tx.ExecContext(ctx, `
		DELETE FROM conversation_items
		WHERE conversation_id = $1 AND id = $2
	`, conversation.ID, itemID)
	if err != nil {
		return fmt.Errorf("delete postgres conversation item: %w", err)
	}
	affected, err = deleteResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres conversation item delete rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres conversation item delete: %w", err)
	}
	return nil
}

func insertPostgresConversationItem(ctx context.Context, tx *sql.Tx, item domain.ConversationItem) error {
	itemJSON, err := domain.MarshalStoredItem(item.Item)
	if err != nil {
		return fmt.Errorf("marshal conversation item: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_items(id, conversation_id, seq, source, role, item_type, item_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, item.ID, item.ConversationID, item.Seq, item.Source, nullableString(item.Role), item.ItemType, string(itemJSON), item.CreatedAt); err != nil {
		return fmt.Errorf("insert postgres conversation item: %w", err)
	}
	return nil
}

func nextPostgresConversationItemSeq(ctx context.Context, tx *sql.Tx, conversationID string) (int, error) {
	var nextSeq int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), -1) + 1
		FROM conversation_items
		WHERE conversation_id = $1
	`, conversationID).Scan(&nextSeq); err != nil {
		return 0, fmt.Errorf("select next postgres conversation item seq: %w", err)
	}
	return nextSeq, nil
}

func (s *Store) ensurePostgresConversationExists(ctx context.Context, conversationID string) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM conversations
		WHERE id = $1
	`, conversationID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("select postgres conversation existence: %w", err)
	}
	return nil
}

func (s *Store) lookupPostgresConversationItemSeq(ctx context.Context, conversationID, itemID string) (int, error) {
	var seq int
	if err := s.db.QueryRowContext(ctx, `
		SELECT seq
		FROM conversation_items
		WHERE conversation_id = $1 AND id = $2
	`, conversationID, itemID).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, domain.NewValidationError("after", "after must reference an existing item in the conversation")
		}
		return 0, fmt.Errorf("select postgres conversation item cursor: %w", err)
	}
	return seq, nil
}

func scanPostgresConversationItems(rows *sql.Rows) ([]domain.ConversationItem, error) {
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
			return nil, fmt.Errorf("scan postgres conversation item: %w", err)
		}
		storedItem, err := domain.UnmarshalStoredItem([]byte(itemJSON))
		if err != nil {
			return nil, fmt.Errorf("unmarshal conversation item: %w", err)
		}
		item.Item = storedItem
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres conversation items: %w", err)
	}
	return items, nil
}
