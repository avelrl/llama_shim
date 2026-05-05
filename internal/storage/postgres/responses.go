package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"llama_shim/internal/domain"
)

const (
	postgresResponseReplayArtifactsMaxCount             = 64
	postgresResponseReplayArtifactsMaxPayloadBytes      = 1 << 20 // 1 MiB
	postgresResponseReplayArtifactsMaxTotalPayloadBytes = 8 << 20 // 8 MiB
)

func (s *Store) SaveResponse(ctx context.Context, response domain.StoredResponse) error {
	if err := insertPostgresResponse(ctx, s.db, response, true); err != nil {
		return err
	}
	return nil
}

func (s *Store) GetResponse(ctx context.Context, id string) (domain.StoredResponse, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, request_json, normalized_input_items_json, effective_input_items_json, output_json, output_text,
		       COALESCE(previous_response_id, ''), COALESCE(conversation_id, ''), store, created_at, completed_at,
		       COALESCE(response_json, '')
		FROM responses
		WHERE id = $1
	`, id)

	response, err := scanPostgresStoredResponse(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredResponse{}, ErrNotFound
		}
		return domain.StoredResponse{}, err
	}

	return response, nil
}

func (s *Store) GetResponseLineage(ctx context.Context, id string, maxItems int) ([]domain.StoredResponse, error) {
	lineage := make([]domain.StoredResponse, 0, 8)
	seen := map[string]struct{}{}
	currentID := id

	for currentID != "" {
		if _, ok := seen[currentID]; ok {
			return nil, fmt.Errorf("response lineage cycle detected for %s", currentID)
		}
		seen[currentID] = struct{}{}

		response, err := s.getResponseLineageNode(ctx, currentID)
		if err != nil {
			return nil, err
		}
		lineage = append(lineage, response)
		if maxItems > 0 && len(lineage) >= maxItems {
			break
		}
		currentID = response.PreviousResponseID
	}

	slices.Reverse(lineage)
	return lineage, nil
}

func (s *Store) getResponseLineageNode(ctx context.Context, id string) (domain.StoredResponse, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, normalized_input_items_json, effective_input_items_json, output_json, output_text,
		       COALESCE(previous_response_id, ''), COALESCE(conversation_id, ''), store, created_at, completed_at
		FROM responses
		WHERE id = $1
	`, id)

	response, err := scanPostgresStoredResponseLineageNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredResponse{}, ErrNotFound
		}
		return domain.StoredResponse{}, err
	}
	return response, nil
}

func (s *Store) DeleteResponse(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM responses WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete postgres response: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete postgres response rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveResponseReplayArtifacts(ctx context.Context, responseID string, artifacts []domain.ResponseReplayArtifact) error {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres response replay artifacts tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM response_replay_artifacts WHERE response_id = $1`, responseID); err != nil {
		return fmt.Errorf("delete prior postgres response replay artifacts: %w", err)
	}
	if len(artifacts) == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit empty postgres response replay artifacts tx: %w", err)
		}
		return nil
	}

	normalized := append([]domain.ResponseReplayArtifact(nil), artifacts...)
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].Sequence < normalized[j].Sequence
	})

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO response_replay_artifacts (
			response_id, sequence_number, event_type, payload_json
		) VALUES ($1, $2, $3, $4)
	`)
	if err != nil {
		return fmt.Errorf("prepare postgres response replay artifacts insert: %w", err)
	}
	defer func() {
		_ = stmt.Close()
	}()

	nextSequence := 1
	totalPayloadBytes := 0
	writtenCount := 0
	for _, artifact := range normalized {
		payload := artifact.PayloadJSON
		if payload == "" || len(payload) > postgresResponseReplayArtifactsMaxPayloadBytes {
			continue
		}
		if writtenCount >= postgresResponseReplayArtifactsMaxCount {
			break
		}
		if totalPayloadBytes+len(payload) > postgresResponseReplayArtifactsMaxTotalPayloadBytes {
			break
		}
		sequence := artifact.Sequence
		if sequence <= 0 {
			sequence = nextSequence
		}
		if _, err := stmt.ExecContext(
			ctx,
			responseID,
			sequence,
			strings.TrimSpace(artifact.EventType),
			payload,
		); err != nil {
			return fmt.Errorf("insert postgres response replay artifact: %w", err)
		}
		totalPayloadBytes += len(payload)
		writtenCount++
		nextSequence = sequence + 1
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres response replay artifacts tx: %w", err)
	}
	return nil
}

func (s *Store) GetResponseReplayArtifacts(ctx context.Context, responseID string) ([]domain.ResponseReplayArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT response_id, sequence_number, event_type, payload_json
		FROM response_replay_artifacts
		WHERE response_id = $1
		ORDER BY sequence_number ASC
	`, strings.TrimSpace(responseID))
	if err != nil {
		return nil, fmt.Errorf("query postgres response replay artifacts: %w", err)
	}
	defer rows.Close()

	artifacts := make([]domain.ResponseReplayArtifact, 0)
	for rows.Next() {
		var artifact domain.ResponseReplayArtifact
		if err := rows.Scan(&artifact.ResponseID, &artifact.Sequence, &artifact.EventType, &artifact.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan postgres response replay artifact: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres response replay artifacts: %w", err)
	}
	if artifacts == nil {
		return []domain.ResponseReplayArtifact{}, nil
	}
	return artifacts, nil
}

func insertPostgresResponse(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, response domain.StoredResponse, upsert bool) error {
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

	statement := `
		INSERT INTO responses (
			id, model, request_json, normalized_input_items_json, effective_input_items_json, output_json, output_text,
			previous_response_id, conversation_id, store, created_at, completed_at, response_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	if upsert {
		statement += `
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
		`
	}

	if _, err := exec.ExecContext(ctx,
		statement,
		response.ID,
		response.Model,
		response.RequestJSON,
		string(inputJSON),
		string(effectiveInputJSON),
		string(outputJSON),
		response.OutputText,
		nullableString(response.PreviousResponseID),
		nullableString(response.ConversationID),
		response.Store,
		response.CreatedAt,
		response.CompletedAt,
		nullableString(response.ResponseJSON),
	); err != nil {
		return fmt.Errorf("insert postgres response: %w", err)
	}
	return nil
}

func scanPostgresStoredResponse(row interface{ Scan(...any) error }) (domain.StoredResponse, error) {
	var (
		response           domain.StoredResponse
		inputJSON          string
		effectiveInputJSON string
		outputJSON         string
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
		&response.Store,
		&response.CreatedAt,
		&response.CompletedAt,
		&response.ResponseJSON,
	); err != nil {
		return domain.StoredResponse{}, err
	}
	return hydratePostgresStoredResponseItems(response, inputJSON, effectiveInputJSON, outputJSON)
}

func scanPostgresStoredResponseLineageNode(row interface{ Scan(...any) error }) (domain.StoredResponse, error) {
	var (
		response           domain.StoredResponse
		inputJSON          string
		effectiveInputJSON string
		outputJSON         string
	)
	if err := row.Scan(
		&response.ID,
		&response.Model,
		&inputJSON,
		&effectiveInputJSON,
		&outputJSON,
		&response.OutputText,
		&response.PreviousResponseID,
		&response.ConversationID,
		&response.Store,
		&response.CreatedAt,
		&response.CompletedAt,
	); err != nil {
		return domain.StoredResponse{}, err
	}
	return hydratePostgresStoredResponseItems(response, inputJSON, effectiveInputJSON, outputJSON)
}

func hydratePostgresStoredResponseItems(response domain.StoredResponse, inputJSON, effectiveInputJSON, outputJSON string) (domain.StoredResponse, error) {
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
	return response, nil
}
