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
	effectiveInputJSON, err := domain.MarshalStoredItems(response.EffectiveInputItems)
	if err != nil {
		return fmt.Errorf("marshal effective input items: %w", err)
	}
	outputJSON, err := domain.MarshalStoredItems(response.Output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO responses (
			id, model, request_json, normalized_input_items_json, effective_input_items_json, output_json, output_text,
			previous_response_id, conversation_id, store, created_at, completed_at, response_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			model = excluded.model,
			request_json = excluded.request_json,
			normalized_input_items_json = excluded.normalized_input_items_json,
			effective_input_items_json = excluded.effective_input_items_json,
			output_json = excluded.output_json,
			output_text = excluded.output_text,
			previous_response_id = excluded.previous_response_id,
			conversation_id = excluded.conversation_id,
			store = excluded.store,
			created_at = excluded.created_at,
			completed_at = excluded.completed_at,
			response_json = excluded.response_json
	`,
		response.ID,
		response.Model,
		response.RequestJSON,
		string(inputJSON),
		string(effectiveInputJSON),
		string(outputJSON),
		response.OutputText,
		nullableString(response.PreviousResponseID),
		nullableString(response.ConversationID),
		boolToInt(response.Store),
		response.CreatedAt,
		response.CompletedAt,
		nullableString(response.ResponseJSON),
	)
	if err != nil {
		return fmt.Errorf("insert response: %w", err)
	}

	return nil
}

func (s *Store) GetResponse(ctx context.Context, id string) (domain.StoredResponse, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, request_json, normalized_input_items_json, effective_input_items_json, output_json, output_text,
		       COALESCE(previous_response_id, ''), COALESCE(conversation_id, ''), store, created_at, completed_at,
		       COALESCE(response_json, '')
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

func (s *Store) DeleteResponse(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM responses WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete response: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete response rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanStoredResponse(row interface{ Scan(...any) error }) (domain.StoredResponse, error) {
	var (
		response           domain.StoredResponse
		inputJSON          string
		effectiveInputJSON string
		outputJSON         string
		storeInt           int
	)
	if err := row.Scan(
		&response.ID,
		&response.Model,
		&response.RequestJSON,
		&inputJSON,
		&effectiveInputJSON,
		&outputJSON,
		&response.OutputText,
		&response.PreviousResponseID,
		&response.ConversationID,
		&storeInt,
		&response.CreatedAt,
		&response.CompletedAt,
		&response.ResponseJSON,
	); err != nil {
		return domain.StoredResponse{}, err
	}
	items, err := domain.UnmarshalStoredItems([]byte(inputJSON))
	if err != nil {
		return domain.StoredResponse{}, fmt.Errorf("unmarshal normalized input items: %w", err)
	}
	response.NormalizedInputItems = items
	effectiveItems, err := domain.UnmarshalStoredItems([]byte(effectiveInputJSON))
	if err != nil {
		return domain.StoredResponse{}, fmt.Errorf("unmarshal effective input items: %w", err)
	}
	response.EffectiveInputItems = effectiveItems

	outputItems, err := domain.UnmarshalStoredItems([]byte(outputJSON))
	if err != nil {
		return domain.StoredResponse{}, fmt.Errorf("unmarshal output: %w", err)
	}
	response.Output = outputItems
	response.Store = storeInt != 0
	return response, nil
}
