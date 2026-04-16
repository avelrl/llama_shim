package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
)

func (s *Store) SaveCodeInterpreterSession(ctx context.Context, session domain.CodeInterpreterSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO code_interpreter_sessions (
			id, owner, backend, status, name, memory_limit, expires_after_minutes, created_at, last_active_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			owner = excluded.owner,
			backend = excluded.backend,
			status = excluded.status,
			name = excluded.name,
			memory_limit = excluded.memory_limit,
			expires_after_minutes = excluded.expires_after_minutes,
			created_at = excluded.created_at,
			last_active_at = excluded.last_active_at
	`,
		session.ID,
		session.Owner,
		session.Backend,
		session.Status,
		session.Name,
		session.MemoryLimit,
		session.ExpiresAfterMinutes,
		session.CreatedAt,
		session.LastActiveAt,
	)
	if err != nil {
		return fmt.Errorf("save code interpreter session: %w", err)
	}
	return nil
}

func (s *Store) GetCodeInterpreterSession(ctx context.Context, id string) (domain.CodeInterpreterSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner, backend, status, name, memory_limit, expires_after_minutes, created_at, last_active_at
		FROM code_interpreter_sessions
		WHERE id = ?
	`, id)

	var session domain.CodeInterpreterSession
	if err := row.Scan(
		&session.ID,
		&session.Owner,
		&session.Backend,
		&session.Status,
		&session.Name,
		&session.MemoryLimit,
		&session.ExpiresAfterMinutes,
		&session.CreatedAt,
		&session.LastActiveAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.CodeInterpreterSession{}, ErrNotFound
		}
		return domain.CodeInterpreterSession{}, fmt.Errorf("get code interpreter session: %w", err)
	}
	return session, nil
}

func (s *Store) ListCodeInterpreterSessions(ctx context.Context, query domain.ListCodeInterpreterSessionsQuery) (domain.CodeInterpreterSessionPage, error) {
	orderDir := "DESC"
	if query.Order == domain.ListOrderAsc {
		orderDir = "ASC"
	}
	nameFilter := strings.TrimSpace(query.Name)
	ownerFilter := strings.TrimSpace(query.Owner)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, backend, status, name, memory_limit, expires_after_minutes, created_at, last_active_at
		FROM code_interpreter_sessions
		ORDER BY created_at `+orderDir+`, id `+orderDir)
	if err != nil {
		return domain.CodeInterpreterSessionPage{}, fmt.Errorf("list code interpreter sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]domain.CodeInterpreterSession, 0, query.Limit+1)
	for rows.Next() {
		var session domain.CodeInterpreterSession
		if err := rows.Scan(
			&session.ID,
			&session.Owner,
			&session.Backend,
			&session.Status,
			&session.Name,
			&session.MemoryLimit,
			&session.ExpiresAfterMinutes,
			&session.CreatedAt,
			&session.LastActiveAt,
		); err != nil {
			return domain.CodeInterpreterSessionPage{}, fmt.Errorf("scan code interpreter session: %w", err)
		}
		if ownerFilter != "" && session.Owner != ownerFilter {
			continue
		}
		if nameFilter != "" && !strings.EqualFold(strings.TrimSpace(session.Name), nameFilter) {
			continue
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return domain.CodeInterpreterSessionPage{}, fmt.Errorf("iterate code interpreter sessions: %w", err)
	}

	items, hasMore, err := paginateCodeInterpreterSessions(sessions, query.After, query.Limit)
	if err != nil {
		return domain.CodeInterpreterSessionPage{}, err
	}
	return domain.CodeInterpreterSessionPage{Sessions: items, HasMore: hasMore}, nil
}

func (s *Store) TouchCodeInterpreterSession(ctx context.Context, id string, lastActiveAt string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE code_interpreter_sessions
		SET last_active_at = ?
		WHERE id = ?
	`, lastActiveAt, id)
	if err != nil {
		return fmt.Errorf("touch code interpreter session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("touch code interpreter session rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func paginateCodeInterpreterSessions(sessions []domain.CodeInterpreterSession, after string, limit int) ([]domain.CodeInterpreterSession, bool, error) {
	start := 0
	if cursor := strings.TrimSpace(after); cursor != "" {
		found := false
		for i, session := range sessions {
			if session.ID == cursor {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, false, domain.NewValidationError("after", "after must reference an existing container")
		}
	}

	if start >= len(sessions) {
		return []domain.CodeInterpreterSession{}, false, nil
	}
	end := start + limit
	if end > len(sessions) {
		end = len(sessions)
	}
	items := append([]domain.CodeInterpreterSession(nil), sessions[start:end]...)
	return items, end < len(sessions), nil
}

func (s *Store) DeleteCodeInterpreterSession(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM code_interpreter_sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete code interpreter session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete code interpreter session rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
