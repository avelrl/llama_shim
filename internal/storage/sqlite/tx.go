package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"llama_shim/internal/domain"
)

func insertResponse(ctx context.Context, tx *sql.Tx, response domain.StoredResponse) error {
	inputJSON, err := json.Marshal(response.NormalizedInputItems)
	if err != nil {
		return fmt.Errorf("marshal normalized input items: %w", err)
	}
	outputJSON, err := json.Marshal(response.Output)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
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
