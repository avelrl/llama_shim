package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/domain"
)

const (
	responseReplayArtifactsMaxCount             = 64
	responseReplayArtifactsMaxPayloadBytes      = 1 << 20 // 1 MiB
	responseReplayArtifactsMaxTotalPayloadBytes = 8 << 20 // 8 MiB
)

func (s *Store) SaveResponseReplayArtifacts(ctx context.Context, responseID string, artifacts []domain.ResponseReplayArtifact) error {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin response replay artifacts tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM response_replay_artifacts WHERE response_id = ?`, responseID); err != nil {
		return fmt.Errorf("delete prior response replay artifacts: %w", err)
	}
	if len(artifacts) == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit empty response replay artifacts tx: %w", err)
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
		) VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare response replay artifacts insert: %w", err)
	}
	defer func() {
		_ = stmt.Close()
	}()

	nextSequence := 1
	totalPayloadBytes := 0
	writtenCount := 0
	for _, artifact := range normalized {
		payload := artifact.PayloadJSON
		if payload == "" || len(payload) > responseReplayArtifactsMaxPayloadBytes {
			continue
		}
		if writtenCount >= responseReplayArtifactsMaxCount {
			break
		}
		if totalPayloadBytes+len(payload) > responseReplayArtifactsMaxTotalPayloadBytes {
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
			return fmt.Errorf("insert response replay artifact: %w", err)
		}
		totalPayloadBytes += len(payload)
		writtenCount++
		nextSequence = sequence + 1
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit response replay artifacts tx: %w", err)
	}
	return nil
}

func (s *Store) GetResponseReplayArtifacts(ctx context.Context, responseID string) ([]domain.ResponseReplayArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT response_id, sequence_number, event_type, payload_json
		FROM response_replay_artifacts
		WHERE response_id = ?
		ORDER BY sequence_number ASC
	`, strings.TrimSpace(responseID))
	if err != nil {
		return nil, fmt.Errorf("query response replay artifacts: %w", err)
	}
	defer rows.Close()

	artifacts := make([]domain.ResponseReplayArtifact, 0)
	for rows.Next() {
		var artifact domain.ResponseReplayArtifact
		if err := rows.Scan(&artifact.ResponseID, &artifact.Sequence, &artifact.EventType, &artifact.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan response replay artifact: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate response replay artifacts: %w", err)
	}
	if artifacts == nil {
		return []domain.ResponseReplayArtifact{}, nil
	}
	return artifacts, nil
}

func (s *Store) deleteResponseReplayArtifacts(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, responseID string) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM response_replay_artifacts WHERE response_id = ?`, strings.TrimSpace(responseID)); err != nil {
		return fmt.Errorf("delete response replay artifacts: %w", err)
	}
	return nil
}
