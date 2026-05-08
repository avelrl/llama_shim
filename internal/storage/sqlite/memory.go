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

func (s *Store) SaveMemoryNote(ctx context.Context, note domain.MemoryNote) error {
	metadataJSON, err := marshalMemoryMetadata(note.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_notes (
			id, scope, session_id, text, source, source_response_id, metadata_json, created_at, updated_at, last_used_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope = excluded.scope,
			session_id = excluded.session_id,
			text = excluded.text,
			source = excluded.source,
			source_response_id = excluded.source_response_id,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at,
			last_used_at = excluded.last_used_at
	`,
		strings.TrimSpace(note.ID),
		strings.TrimSpace(note.Scope),
		strings.TrimSpace(note.SessionID),
		note.Text,
		strings.TrimSpace(note.Source),
		strings.TrimSpace(note.SourceResponseID),
		metadataJSON,
		note.CreatedAt,
		note.UpdatedAt,
		note.LastUsedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert memory note: %w", err)
	}
	return nil
}

func (s *Store) ListMemoryNotes(ctx context.Context, query domain.ListMemoryNotesQuery) ([]domain.MemoryNote, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 8
	}

	var (
		rows *sql.Rows
		err  error
	)
	sessionID := strings.TrimSpace(query.SessionID)
	if query.IncludeGlobal && sessionID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, scope, session_id, text, source, source_response_id, metadata_json, created_at, updated_at, last_used_at
			FROM memory_notes
			WHERE scope = 'global' OR (scope = 'session' AND session_id = ?)
			ORDER BY updated_at DESC, id DESC
			LIMIT ?
		`, sessionID, limit)
	} else if query.IncludeGlobal {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, scope, session_id, text, source, source_response_id, metadata_json, created_at, updated_at, last_used_at
			FROM memory_notes
			WHERE scope = 'global'
			ORDER BY updated_at DESC, id DESC
			LIMIT ?
		`, limit)
	} else if sessionID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, scope, session_id, text, source, source_response_id, metadata_json, created_at, updated_at, last_used_at
			FROM memory_notes
			WHERE scope = 'session' AND session_id = ?
			ORDER BY updated_at DESC, id DESC
			LIMIT ?
		`, sessionID, limit)
	} else {
		return []domain.MemoryNote{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query memory notes: %w", err)
	}
	defer rows.Close()

	notes := make([]domain.MemoryNote, 0)
	for rows.Next() {
		note, err := scanMemoryNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory notes: %w", err)
	}
	return notes, nil
}

func (s *Store) TouchMemoryNotes(ctx context.Context, ids []string, lastUsedAt string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory touch tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	stmt, err := tx.PrepareContext(ctx, `UPDATE memory_notes SET last_used_at = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare memory touch: %w", err)
	}
	defer func() {
		_ = stmt.Close()
	}()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, lastUsedAt, id); err != nil {
			return fmt.Errorf("touch memory note %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memory touch tx: %w", err)
	}
	return nil
}

func scanMemoryNote(row interface{ Scan(...any) error }) (domain.MemoryNote, error) {
	var note domain.MemoryNote
	var metadataJSON string
	if err := row.Scan(
		&note.ID,
		&note.Scope,
		&note.SessionID,
		&note.Text,
		&note.Source,
		&note.SourceResponseID,
		&metadataJSON,
		&note.CreatedAt,
		&note.UpdatedAt,
		&note.LastUsedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.MemoryNote{}, ErrNotFound
		}
		return domain.MemoryNote{}, fmt.Errorf("scan memory note: %w", err)
	}
	metadata, err := unmarshalMemoryMetadata(metadataJSON)
	if err != nil {
		return domain.MemoryNote{}, err
	}
	note.Metadata = metadata
	return note, nil
}

func marshalMemoryMetadata(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal memory metadata: %w", err)
	}
	return string(raw), nil
}

func unmarshalMemoryMetadata(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal memory metadata: %w", err)
	}
	if metadata == nil {
		return map[string]string{}, nil
	}
	return metadata, nil
}
