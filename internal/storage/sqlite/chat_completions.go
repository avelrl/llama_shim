package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/domain"
)

func (s *Store) SaveChatCompletion(ctx context.Context, completion domain.StoredChatCompletion) error {
	metadataJSON, err := json.Marshal(completion.Metadata)
	if err != nil {
		return fmt.Errorf("marshal chat completion metadata: %w", err)
	}
	messages, err := domain.StoredChatCompletionMessagesFromRequestJSON(completion.ID, completion.RequestJSON)
	if err != nil {
		return fmt.Errorf("normalize chat completion messages: %w", err)
	}
	responseJSON, err := ensureStoredChatCompletionResponseMetadata(completion.ResponseJSON, completion.Metadata)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chat completion tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO chat_completions (
			id, model, metadata_json, request_json, response_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			model = excluded.model,
			metadata_json = excluded.metadata_json,
			request_json = excluded.request_json,
			response_json = excluded.response_json,
			created_at = excluded.created_at
	`,
		completion.ID,
		completion.Model,
		string(metadataJSON),
		completion.RequestJSON,
		responseJSON,
		completion.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert chat completion: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_completion_messages WHERE completion_id = ?`, completion.ID); err != nil {
		return fmt.Errorf("delete prior chat completion messages: %w", err)
	}
	if len(messages) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO chat_completion_messages (
				completion_id, sequence_number, message_id, message_json
			) VALUES (?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("prepare chat completion message insert: %w", err)
		}
		defer func() {
			_ = stmt.Close()
		}()
		for _, message := range messages {
			if _, err := stmt.ExecContext(ctx, completion.ID, message.Sequence, message.ID, message.MessageJSON); err != nil {
				return fmt.Errorf("insert chat completion message: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chat completion tx: %w", err)
	}
	return nil
}

func (s *Store) GetChatCompletion(ctx context.Context, id string) (domain.StoredChatCompletion, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, metadata_json, request_json, response_json, created_at
		FROM chat_completions
		WHERE id = ?
	`, id)

	completion, err := scanStoredChatCompletion(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredChatCompletion{}, ErrNotFound
		}
		return domain.StoredChatCompletion{}, err
	}

	return completion, nil
}

func (s *Store) ListChatCompletions(ctx context.Context, query domain.ListStoredChatCompletionsQuery) (domain.StoredChatCompletionPage, error) {
	if query.Limit < 1 {
		query.Limit = 20
	}
	orderDir := "ASC"
	if query.Order == domain.ChatCompletionOrderDesc {
		orderDir = "DESC"
	}

	after := strings.TrimSpace(query.After)
	var afterCreated int64
	if after != "" {
		statement, args := buildStoredChatCompletionCursorLookup(query)
		if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&afterCreated); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.StoredChatCompletionPage{}, ErrNotFound
			}
			return domain.StoredChatCompletionPage{}, fmt.Errorf("lookup chat completion cursor: %w", err)
		}
	}

	statement, args := buildStoredChatCompletionListQuery(query, afterCreated)
	statement += ` ORDER BY created_at ` + orderDir + `, id ` + orderDir + ` LIMIT ?`
	args = append(args, storedChatCompletionFetchLimit(query.Limit))

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredChatCompletionPage{}, fmt.Errorf("list chat completions: %w", err)
	}
	defer rows.Close()

	page := make([]domain.StoredChatCompletion, 0, storedChatCompletionListCapacity(query.Limit))
	hasMore := false
	for rows.Next() {
		completion, err := scanStoredChatCompletionListRow(rows)
		if err != nil {
			return domain.StoredChatCompletionPage{}, err
		}
		if len(page) >= query.Limit {
			hasMore = true
			break
		}
		page = append(page, completion)
	}
	if err := rows.Err(); err != nil {
		return domain.StoredChatCompletionPage{}, fmt.Errorf("iterate chat completions: %w", err)
	}

	return domain.StoredChatCompletionPage{
		Completions: page,
		HasMore:     hasMore,
	}, nil
}

func storedChatCompletionListCapacity(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > 128 {
		return 128
	}
	return limit
}

func storedChatCompletionFetchLimit(limit int) int {
	if limit < 1 {
		return 2
	}
	maxInt := int(^uint(0) >> 1)
	if limit >= maxInt {
		return maxInt
	}
	return limit + 1
}

func (s *Store) UpdateChatCompletionMetadata(ctx context.Context, id string, metadata map[string]string) (domain.StoredChatCompletion, error) {
	completion, err := s.GetChatCompletion(ctx, id)
	if err != nil {
		return domain.StoredChatCompletion{}, err
	}

	completion.Metadata = metadata
	responseJSON, err := patchStoredChatCompletionResponseMetadata(completion.ResponseJSON, metadata)
	if err != nil {
		return domain.StoredChatCompletion{}, err
	}
	completion.ResponseJSON = responseJSON

	if err := s.SaveChatCompletion(ctx, completion); err != nil {
		return domain.StoredChatCompletion{}, err
	}

	return completion, nil
}

func (s *Store) DeleteChatCompletion(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM chat_completions
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("delete chat completion: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete chat completion rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListChatCompletionMessages(ctx context.Context, completionID string, query domain.ListStoredChatCompletionMessagesQuery) (domain.StoredChatCompletionMessagePage, error) {
	completionID = strings.TrimSpace(completionID)
	if completionID == "" {
		return domain.StoredChatCompletionMessagePage{}, ErrNotFound
	}
	if query.Limit < 1 {
		query.Limit = 20
	}
	orderDir := "ASC"
	if query.Order == domain.ChatCompletionOrderDesc {
		orderDir = "DESC"
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM chat_completions WHERE id = ?`, completionID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredChatCompletionMessagePage{}, ErrNotFound
		}
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("lookup chat completion: %w", err)
	}

	var indexedSequence int
	if err := s.db.QueryRowContext(ctx, `SELECT sequence_number FROM chat_completion_messages WHERE completion_id = ? LIMIT 1`, completionID).Scan(&indexedSequence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.listLegacyChatCompletionMessages(ctx, completionID, query)
		}
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("lookup chat completion message index: %w", err)
	}

	after := strings.TrimSpace(query.After)
	statement := `
		SELECT sequence_number, message_id, message_json
		FROM chat_completion_messages
		WHERE completion_id = ?
	`
	args := []any{completionID}
	if after != "" {
		var afterSequence int
		cursorStatement := `
			SELECT sequence_number
			FROM chat_completion_messages
			WHERE completion_id = ? AND message_id = ?
			ORDER BY sequence_number ` + orderDir + `
			LIMIT 1
		`
		if err := s.db.QueryRowContext(ctx, cursorStatement, completionID, after).Scan(&afterSequence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.StoredChatCompletionMessagePage{}, ErrNotFound
			}
			return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("lookup chat completion message cursor: %w", err)
		}
		if query.Order == domain.ChatCompletionOrderDesc {
			statement += ` AND sequence_number < ?`
		} else {
			statement += ` AND sequence_number > ?`
		}
		args = append(args, afterSequence)
	}
	statement += ` ORDER BY sequence_number ` + orderDir + ` LIMIT ?`
	args = append(args, storedChatCompletionFetchLimit(query.Limit))

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("list chat completion messages: %w", err)
	}
	defer rows.Close()

	messages := make([]domain.StoredChatCompletionMessage, 0, storedChatCompletionListCapacity(query.Limit))
	hasMore := false
	for rows.Next() {
		message, err := scanStoredChatCompletionMessage(rows)
		if err != nil {
			return domain.StoredChatCompletionMessagePage{}, err
		}
		if len(messages) >= query.Limit {
			hasMore = true
			break
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("iterate chat completion messages: %w", err)
	}
	return domain.StoredChatCompletionMessagePage{
		Messages: messages,
		HasMore:  hasMore,
	}, nil
}

func (s *Store) listLegacyChatCompletionMessages(ctx context.Context, completionID string, query domain.ListStoredChatCompletionMessagesQuery) (domain.StoredChatCompletionMessagePage, error) {
	completion, err := s.GetChatCompletion(ctx, completionID)
	if err != nil {
		return domain.StoredChatCompletionMessagePage{}, err
	}
	messages, err := domain.StoredChatCompletionMessagesFromRequestJSON(completion.ID, completion.RequestJSON)
	if err != nil {
		return domain.StoredChatCompletionMessagePage{}, err
	}
	return pageStoredChatCompletionMessages(messages, query)
}

func pageStoredChatCompletionMessages(messages []domain.StoredChatCompletionMessage, query domain.ListStoredChatCompletionMessagesQuery) (domain.StoredChatCompletionMessagePage, error) {
	if query.Limit < 1 {
		query.Limit = 20
	}
	if query.Order == domain.ChatCompletionOrderDesc {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}

	start := 0
	if after := strings.TrimSpace(query.After); after != "" {
		start = -1
		for i, message := range messages {
			if message.ID == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return domain.StoredChatCompletionMessagePage{}, ErrNotFound
		}
	}

	if start > len(messages) {
		start = len(messages)
	}
	end := start + query.Limit
	hasMore := end < len(messages)
	if end > len(messages) {
		end = len(messages)
	}
	return domain.StoredChatCompletionMessagePage{
		Messages: messages[start:end],
		HasMore:  hasMore,
	}, nil
}

func buildStoredChatCompletionCursorLookup(query domain.ListStoredChatCompletionsQuery) (string, []any) {
	conditions, args := storedChatCompletionListConditions(query)
	statement := `
		SELECT created_at
		FROM chat_completions
		WHERE id = ?
	`
	cursorArgs := []any{strings.TrimSpace(query.After)}
	if len(conditions) > 0 {
		statement += ` AND ` + strings.Join(conditions, ` AND `)
		cursorArgs = append(cursorArgs, args...)
	}
	statement += ` LIMIT 1`
	return statement, cursorArgs
}

func buildStoredChatCompletionListQuery(query domain.ListStoredChatCompletionsQuery, afterCreated int64) (string, []any) {
	conditions, args := storedChatCompletionListConditions(query)
	if after := strings.TrimSpace(query.After); after != "" {
		if query.Order == domain.ChatCompletionOrderDesc {
			conditions = append(conditions, `(created_at < ? OR (created_at = ? AND id < ?))`)
		} else {
			conditions = append(conditions, `(created_at > ? OR (created_at = ? AND id > ?))`)
		}
		args = append(args, afterCreated, afterCreated, after)
	}

	statement := `
		SELECT id, model, metadata_json, response_json, created_at
		FROM chat_completions
	`
	if len(conditions) > 0 {
		statement += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	return statement, args
}

func storedChatCompletionListConditions(query domain.ListStoredChatCompletionsQuery) ([]string, []any) {
	conditions := make([]string, 0, 1+len(query.Metadata))
	args := make([]any, 0, 1+len(query.Metadata)*2)
	if model := strings.TrimSpace(query.Model); model != "" {
		conditions = append(conditions, `model = ?`)
		args = append(args, model)
	}
	keys := make([]string, 0, len(query.Metadata))
	for key := range query.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		conditions = append(conditions, `EXISTS (
			SELECT 1 FROM json_each(chat_completions.metadata_json)
			WHERE key = ? AND value = ?
		)`)
		args = append(args, key, query.Metadata[key])
	}
	return conditions, args
}

func scanStoredChatCompletion(row interface{ Scan(...any) error }) (domain.StoredChatCompletion, error) {
	var (
		completion   domain.StoredChatCompletion
		metadataJSON string
	)
	if err := row.Scan(
		&completion.ID,
		&completion.Model,
		&metadataJSON,
		&completion.RequestJSON,
		&completion.ResponseJSON,
		&completion.CreatedAt,
	); err != nil {
		return domain.StoredChatCompletion{}, err
	}

	if strings.TrimSpace(metadataJSON) == "" {
		completion.Metadata = map[string]string{}
		return completion, nil
	}

	if err := json.Unmarshal([]byte(metadataJSON), &completion.Metadata); err != nil {
		return domain.StoredChatCompletion{}, fmt.Errorf("decode chat completion metadata: %w", err)
	}
	if completion.Metadata == nil {
		completion.Metadata = map[string]string{}
	}
	return completion, nil
}

func scanStoredChatCompletionListRow(row interface{ Scan(...any) error }) (domain.StoredChatCompletion, error) {
	var (
		completion   domain.StoredChatCompletion
		metadataJSON string
	)
	if err := row.Scan(
		&completion.ID,
		&completion.Model,
		&metadataJSON,
		&completion.ResponseJSON,
		&completion.CreatedAt,
	); err != nil {
		return domain.StoredChatCompletion{}, err
	}

	if strings.TrimSpace(metadataJSON) == "" {
		completion.Metadata = map[string]string{}
		return completion, nil
	}

	if err := json.Unmarshal([]byte(metadataJSON), &completion.Metadata); err != nil {
		return domain.StoredChatCompletion{}, fmt.Errorf("decode chat completion metadata: %w", err)
	}
	if completion.Metadata == nil {
		completion.Metadata = map[string]string{}
	}
	return completion, nil
}

func scanStoredChatCompletionMessage(row interface{ Scan(...any) error }) (domain.StoredChatCompletionMessage, error) {
	var message domain.StoredChatCompletionMessage
	if err := row.Scan(&message.Sequence, &message.ID, &message.MessageJSON); err != nil {
		return domain.StoredChatCompletionMessage{}, err
	}
	return message, nil
}

func patchStoredChatCompletionResponseMetadata(responseJSON string, metadata map[string]string) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(responseJSON), &payload); err != nil {
		return "", fmt.Errorf("decode chat completion response metadata patch: %w", err)
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload["metadata"] = metadata
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode chat completion response metadata patch: %w", err)
	}
	return string(raw), nil
}

func ensureStoredChatCompletionResponseMetadata(responseJSON string, metadata map[string]string) (string, error) {
	normalized := normalizeStoredChatCompletionMetadata(metadata)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(responseJSON), &payload); err != nil {
		return "", fmt.Errorf("decode chat completion response metadata: %w", err)
	}
	if rawMetadata, ok := payload["metadata"]; ok && storedChatCompletionMetadataMatches(rawMetadata, normalized) {
		return responseJSON, nil
	}
	rawMetadata, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode chat completion response metadata: %w", err)
	}
	payload["metadata"] = rawMetadata
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode chat completion response metadata patch: %w", err)
	}
	return string(raw), nil
}

func storedChatCompletionMetadataMatches(raw json.RawMessage, metadata map[string]string) bool {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return false
	}
	var existing map[string]string
	if err := json.Unmarshal(raw, &existing); err != nil {
		return false
	}
	existing = normalizeStoredChatCompletionMetadata(existing)
	if len(existing) != len(metadata) {
		return false
	}
	for key, value := range metadata {
		if existing[key] != value {
			return false
		}
	}
	return true
}

func normalizeStoredChatCompletionMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		normalized[key] = value
	}
	return normalized
}
