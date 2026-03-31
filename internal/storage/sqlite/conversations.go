package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
		itemID, err := domain.NewPrefixedID("item")
		if err != nil {
			return fmt.Errorf("generate conversation item id: %w", err)
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

	items := make([]domain.ConversationItem, 0, 8)
	conversation.Items = make([]domain.MessageItem, 0, 8)
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
			return domain.Conversation{}, nil, fmt.Errorf("scan conversation item: %w", err)
		}
		if err := json.Unmarshal([]byte(itemJSON), &item.Item); err != nil {
			return domain.Conversation{}, nil, fmt.Errorf("unmarshal conversation item: %w", err)
		}
		items = append(items, item)
		conversation.Items = append(conversation.Items, item.Item)
	}
	if err := rows.Err(); err != nil {
		return domain.Conversation{}, nil, fmt.Errorf("iterate conversation items: %w", err)
	}

	return conversation, items, nil
}

func (s *Store) SaveResponseAndAppendConversation(
	ctx context.Context,
	conversation domain.Conversation,
	response domain.StoredResponse,
	input []domain.MessageItem,
	assistant domain.MessageItem,
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
	appendItems := domain.BuildConversationAppendItems(nextSeq, input, assistant)
	for i := range appendItems {
		appendItems[i].ConversationID = conversation.ID
		appendItems[i].CreatedAt = response.CompletedAt
		itemID, err := domain.NewPrefixedID("item")
		if err != nil {
			return fmt.Errorf("generate appended conversation item id: %w", err)
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
	itemJSON, err := json.Marshal(item.Item)
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
