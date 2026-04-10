package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"llama_shim/internal/domain"
)

func insertResponse(ctx context.Context, tx *sql.Tx, response domain.StoredResponse) error {
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

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO responses (
			id, model, request_json, normalized_input_items_json, effective_input_items_json, output_json, output_text,
			previous_response_id, conversation_id, store, created_at, completed_at, response_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
	); err != nil {
		return fmt.Errorf("insert response: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
