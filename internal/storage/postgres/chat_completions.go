package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	responseJSON, err := ensurePostgresStoredChatCompletionResponseMetadata(completion.ResponseJSON, completion.Metadata)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres chat completion tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO chat_completions (
			id, model, metadata_json, request_json, response_json, created_at
		) VALUES ($1, $2, $3::jsonb, $4, $5, $6)
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
		return fmt.Errorf("insert postgres chat completion: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_completion_messages WHERE completion_id = $1`, completion.ID); err != nil {
		return fmt.Errorf("delete prior postgres chat completion messages: %w", err)
	}
	if len(messages) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO chat_completion_messages (
				completion_id, sequence_number, message_id, message_json
			) VALUES ($1, $2, $3, $4)
		`)
		if err != nil {
			return fmt.Errorf("prepare postgres chat completion message insert: %w", err)
		}
		defer func() {
			_ = stmt.Close()
		}()
		for _, message := range messages {
			if _, err := stmt.ExecContext(ctx, completion.ID, message.Sequence, message.ID, message.MessageJSON); err != nil {
				return fmt.Errorf("insert postgres chat completion message: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres chat completion tx: %w", err)
	}
	return nil
}

func (s *Store) GetChatCompletion(ctx context.Context, id string) (domain.StoredChatCompletion, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, metadata_json, request_json, response_json, created_at
		FROM chat_completions
		WHERE id = $1
	`, id)

	completion, err := scanPostgresStoredChatCompletion(row, true)
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
		statement, args := buildPostgresStoredChatCompletionCursorLookup(query)
		if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&afterCreated); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.StoredChatCompletionPage{}, ErrNotFound
			}
			return domain.StoredChatCompletionPage{}, fmt.Errorf("lookup postgres chat completion cursor: %w", err)
		}
	}

	statement, args := buildPostgresStoredChatCompletionListQuery(query, afterCreated)
	limitPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	statement += ` ORDER BY created_at ` + orderDir + `, id ` + orderDir + ` LIMIT ` + limitPlaceholder
	args = append(args, storedPostgresChatCompletionFetchLimit(query.Limit))

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredChatCompletionPage{}, fmt.Errorf("list postgres chat completions: %w", err)
	}
	defer rows.Close()

	page := make([]domain.StoredChatCompletion, 0, storedPostgresChatCompletionListCapacity(query.Limit))
	hasMore := false
	for rows.Next() {
		completion, err := scanPostgresStoredChatCompletion(rows, false)
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
		return domain.StoredChatCompletionPage{}, fmt.Errorf("iterate postgres chat completions: %w", err)
	}

	return domain.StoredChatCompletionPage{
		Completions: page,
		HasMore:     hasMore,
	}, nil
}

func (s *Store) UpdateChatCompletionMetadata(ctx context.Context, id string, metadata map[string]string) (domain.StoredChatCompletion, error) {
	completion, err := s.GetChatCompletion(ctx, id)
	if err != nil {
		return domain.StoredChatCompletion{}, err
	}

	completion.Metadata = metadata
	responseJSON, err := patchPostgresStoredChatCompletionResponseMetadata(completion.ResponseJSON, metadata)
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
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("delete postgres chat completion: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete postgres chat completion rows affected: %w", err)
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
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM chat_completions WHERE id = $1`, completionID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredChatCompletionMessagePage{}, ErrNotFound
		}
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("lookup postgres chat completion: %w", err)
	}

	var indexedSequence int
	if err := s.db.QueryRowContext(ctx, `SELECT sequence_number FROM chat_completion_messages WHERE completion_id = $1 LIMIT 1`, completionID).Scan(&indexedSequence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.listLegacyPostgresChatCompletionMessages(ctx, completionID, query)
		}
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("lookup postgres chat completion message index: %w", err)
	}

	after := strings.TrimSpace(query.After)
	statement := `
		SELECT sequence_number, message_id, message_json
		FROM chat_completion_messages
		WHERE completion_id = $1
	`
	args := []any{completionID}
	if after != "" {
		var afterSequence int
		cursorStatement := `
			SELECT sequence_number
			FROM chat_completion_messages
			WHERE completion_id = $1 AND message_id = $2
			ORDER BY sequence_number ` + orderDir + `
			LIMIT 1
		`
		if err := s.db.QueryRowContext(ctx, cursorStatement, completionID, after).Scan(&afterSequence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.StoredChatCompletionMessagePage{}, ErrNotFound
			}
			return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("lookup postgres chat completion message cursor: %w", err)
		}
		if query.Order == domain.ChatCompletionOrderDesc {
			statement += ` AND sequence_number < $2`
		} else {
			statement += ` AND sequence_number > $2`
		}
		args = append(args, afterSequence)
	}
	limitPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	statement += ` ORDER BY sequence_number ` + orderDir + ` LIMIT ` + limitPlaceholder
	args = append(args, storedPostgresChatCompletionFetchLimit(query.Limit))

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("list postgres chat completion messages: %w", err)
	}
	defer rows.Close()

	messages := make([]domain.StoredChatCompletionMessage, 0, storedPostgresChatCompletionListCapacity(query.Limit))
	hasMore := false
	for rows.Next() {
		message, err := scanPostgresStoredChatCompletionMessage(rows)
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
		return domain.StoredChatCompletionMessagePage{}, fmt.Errorf("iterate postgres chat completion messages: %w", err)
	}
	return domain.StoredChatCompletionMessagePage{
		Messages: messages,
		HasMore:  hasMore,
	}, nil
}

func (s *Store) listLegacyPostgresChatCompletionMessages(ctx context.Context, completionID string, query domain.ListStoredChatCompletionMessagesQuery) (domain.StoredChatCompletionMessagePage, error) {
	completion, err := s.GetChatCompletion(ctx, completionID)
	if err != nil {
		return domain.StoredChatCompletionMessagePage{}, err
	}
	messages, err := domain.StoredChatCompletionMessagesFromRequestJSON(completion.ID, completion.RequestJSON)
	if err != nil {
		return domain.StoredChatCompletionMessagePage{}, err
	}
	return pagePostgresStoredChatCompletionMessages(messages, query)
}

func pagePostgresStoredChatCompletionMessages(messages []domain.StoredChatCompletionMessage, query domain.ListStoredChatCompletionMessagesQuery) (domain.StoredChatCompletionMessagePage, error) {
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

func buildPostgresStoredChatCompletionCursorLookup(query domain.ListStoredChatCompletionsQuery) (string, []any) {
	conditions, args := postgresStoredChatCompletionListConditions(query, 1)
	statement := `
		SELECT created_at
		FROM chat_completions
		WHERE id = $1
	`
	cursorArgs := []any{strings.TrimSpace(query.After)}
	if len(conditions) > 0 {
		statement += ` AND ` + strings.Join(conditions, ` AND `)
		cursorArgs = append(cursorArgs, args...)
	}
	statement += ` LIMIT 1`
	return statement, cursorArgs
}

func buildPostgresStoredChatCompletionListQuery(query domain.ListStoredChatCompletionsQuery, afterCreated int64) (string, []any) {
	conditions, args := postgresStoredChatCompletionListConditions(query, 0)
	if after := strings.TrimSpace(query.After); after != "" {
		createdAtA := fmt.Sprintf("$%d", len(args)+1)
		createdAtB := fmt.Sprintf("$%d", len(args)+2)
		idPlaceholder := fmt.Sprintf("$%d", len(args)+3)
		if query.Order == domain.ChatCompletionOrderDesc {
			conditions = append(conditions, fmt.Sprintf(`(created_at < %s OR (created_at = %s AND id < %s))`, createdAtA, createdAtB, idPlaceholder))
		} else {
			conditions = append(conditions, fmt.Sprintf(`(created_at > %s OR (created_at = %s AND id > %s))`, createdAtA, createdAtB, idPlaceholder))
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

func postgresStoredChatCompletionListConditions(query domain.ListStoredChatCompletionsQuery, placeholderOffset int) ([]string, []any) {
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if model := strings.TrimSpace(query.Model); model != "" {
		args = append(args, model)
		conditions = append(conditions, fmt.Sprintf(`model = $%d`, placeholderOffset+len(args)))
	}
	if len(query.Metadata) > 0 {
		metadataJSON, err := json.Marshal(query.Metadata)
		if err == nil {
			args = append(args, string(metadataJSON))
			conditions = append(conditions, fmt.Sprintf(`metadata_json @> $%d::jsonb`, placeholderOffset+len(args)))
		}
	}
	return conditions, args
}

func storedPostgresChatCompletionListCapacity(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > 128 {
		return 128
	}
	return limit
}

func storedPostgresChatCompletionFetchLimit(limit int) int {
	if limit < 1 {
		return 2
	}
	maxInt := int(^uint(0) >> 1)
	if limit >= maxInt {
		return maxInt
	}
	return limit + 1
}

func scanPostgresStoredChatCompletion(row interface{ Scan(...any) error }, includeRequest bool) (domain.StoredChatCompletion, error) {
	var (
		completion   domain.StoredChatCompletion
		metadataJSON []byte
	)
	if includeRequest {
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
	} else {
		if err := row.Scan(
			&completion.ID,
			&completion.Model,
			&metadataJSON,
			&completion.ResponseJSON,
			&completion.CreatedAt,
		); err != nil {
			return domain.StoredChatCompletion{}, err
		}
	}

	if len(metadataJSON) == 0 {
		completion.Metadata = map[string]string{}
		return completion, nil
	}

	if err := json.Unmarshal(metadataJSON, &completion.Metadata); err != nil {
		return domain.StoredChatCompletion{}, fmt.Errorf("decode chat completion metadata: %w", err)
	}
	if completion.Metadata == nil {
		completion.Metadata = map[string]string{}
	}
	return completion, nil
}

func scanPostgresStoredChatCompletionMessage(row interface{ Scan(...any) error }) (domain.StoredChatCompletionMessage, error) {
	var message domain.StoredChatCompletionMessage
	if err := row.Scan(&message.Sequence, &message.ID, &message.MessageJSON); err != nil {
		return domain.StoredChatCompletionMessage{}, err
	}
	return message, nil
}

func patchPostgresStoredChatCompletionResponseMetadata(responseJSON string, metadata map[string]string) (string, error) {
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

func ensurePostgresStoredChatCompletionResponseMetadata(responseJSON string, metadata map[string]string) (string, error) {
	normalized := normalizePostgresStoredChatCompletionMetadata(metadata)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(responseJSON), &payload); err != nil {
		return "", fmt.Errorf("decode chat completion response metadata: %w", err)
	}
	if rawMetadata, ok := payload["metadata"]; ok && postgresStoredChatCompletionMetadataMatches(rawMetadata, normalized) {
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

func postgresStoredChatCompletionMetadataMatches(raw json.RawMessage, metadata map[string]string) bool {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return false
	}
	var existing map[string]string
	if err := json.Unmarshal(raw, &existing); err != nil {
		return false
	}
	existing = normalizePostgresStoredChatCompletionMetadata(existing)
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

func normalizePostgresStoredChatCompletionMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		normalized[key] = value
	}
	return normalized
}
