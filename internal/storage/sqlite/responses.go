package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"llama_shim/internal/domain"
)

func (s *Store) SaveResponse(ctx context.Context, response domain.StoredResponse) error {
	inputJSON, err := domain.MarshalStoredItems(response.NormalizedInputItems)
	if err != nil {
		return fmt.Errorf("marshal normalized input items: %w", err)
	}
	outputJSON, err := domain.MarshalStoredItems(response.Output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO responses (
			id, model, request_json, normalized_input_items_json, output_json, output_text,
			previous_response_id, conversation_id, store, created_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		response.ID,
		response.Model,
		response.RequestJSON,
		string(inputJSON),
		string(outputJSON),
		response.OutputText,
		nullableString(response.PreviousResponseID),
		nullableString(response.ConversationID),
		boolToInt(response.Store),
		response.CreatedAt,
		response.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("insert response: %w", err)
	}

	return nil
}

func (s *Store) GetResponse(ctx context.Context, id string) (domain.StoredResponse, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, request_json, normalized_input_items_json, output_json, output_text,
		       COALESCE(previous_response_id, ''), COALESCE(conversation_id, ''), store, created_at, completed_at
		FROM responses
		WHERE id = ?
	`, id)

	response, err := scanStoredResponse(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredResponse{}, ErrNotFound
		}
		return domain.StoredResponse{}, err
	}

	return response, nil
}

func (s *Store) GetResponseLineage(ctx context.Context, id string) ([]domain.StoredResponse, error) {
	lineage := make([]domain.StoredResponse, 0, 8)
	seen := map[string]struct{}{}
	currentID := id

	for currentID != "" {
		if _, ok := seen[currentID]; ok {
			return nil, fmt.Errorf("response lineage cycle detected for %s", currentID)
		}
		seen[currentID] = struct{}{}

		response, err := s.GetResponse(ctx, currentID)
		if err != nil {
			return nil, err
		}
		lineage = append(lineage, response)
		currentID = response.PreviousResponseID
	}

	slices.Reverse(lineage)
	return lineage, nil
}

func scanStoredResponse(row interface{ Scan(...any) error }) (domain.StoredResponse, error) {
	var (
		response   domain.StoredResponse
		inputJSON  string
		outputJSON string
		storeInt   int
	)
	if err := row.Scan(
		&response.ID,
		&response.Model,
		&response.RequestJSON,
		&inputJSON,
		&outputJSON,
		&response.OutputText,
		&response.PreviousResponseID,
		&response.ConversationID,
		&storeInt,
		&response.CreatedAt,
		&response.CompletedAt,
	); err != nil {
		return domain.StoredResponse{}, err
	}
	items, err := domain.UnmarshalStoredItems([]byte(inputJSON))
	if err != nil {
		return domain.StoredResponse{}, fmt.Errorf("unmarshal normalized input items: %w", err)
	}
	response.NormalizedInputItems = items

	outputItems, err := domain.UnmarshalStoredItems([]byte(outputJSON))
	if err != nil {
		return domain.StoredResponse{}, fmt.Errorf("unmarshal output: %w", err)
	}
	response.Output = outputItems
	response.Store = storeInt != 0
	return response, nil
}
