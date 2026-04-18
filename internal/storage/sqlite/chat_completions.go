package sqlite

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

	_, err = s.db.ExecContext(ctx, `
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
		completion.ResponseJSON,
		completion.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert chat completion: %w", err)
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

	statement := `
		SELECT id, model, metadata_json, request_json, response_json, created_at
		FROM chat_completions
	`
	args := make([]any, 0, 1)
	if strings.TrimSpace(query.Model) != "" {
		statement += ` WHERE model = ?`
		args = append(args, strings.TrimSpace(query.Model))
	}
	statement += ` ORDER BY created_at ` + orderDir + `, id ` + orderDir

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredChatCompletionPage{}, fmt.Errorf("list chat completions: %w", err)
	}
	defer rows.Close()

	after := strings.TrimSpace(query.After)
	seenAfter := after == ""
	page := make([]domain.StoredChatCompletion, 0, storedChatCompletionListCapacity(query.Limit))
	hasMore := false
	for rows.Next() {
		completion, err := scanStoredChatCompletion(rows)
		if err != nil {
			return domain.StoredChatCompletionPage{}, err
		}
		if !matchesMetadataFilter(completion.Metadata, query.Metadata) {
			continue
		}
		if !seenAfter {
			if completion.ID == after {
				seenAfter = true
			}
			continue
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
	if !seenAfter {
		return domain.StoredChatCompletionPage{}, ErrNotFound
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

func matchesMetadataFilter(metadata map[string]string, filter map[string]string) bool {
	if len(filter) == 0 {
		return true
	}
	for key, expected := range filter {
		if metadata[key] != expected {
			return false
		}
	}
	return true
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
