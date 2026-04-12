package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"llama_shim/internal/domain"
)

func (s *Store) SaveCodeInterpreterSession(ctx context.Context, session domain.CodeInterpreterSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO code_interpreter_sessions (id, backend, created_at, last_active_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			backend = excluded.backend,
			created_at = excluded.created_at,
			last_active_at = excluded.last_active_at
	`, session.ID, session.Backend, session.CreatedAt, session.LastActiveAt)
	if err != nil {
		return fmt.Errorf("save code interpreter session: %w", err)
	}
	return nil
}

func (s *Store) GetCodeInterpreterSession(ctx context.Context, id string) (domain.CodeInterpreterSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, backend, created_at, last_active_at
		FROM code_interpreter_sessions
		WHERE id = ?
	`, id)

	var session domain.CodeInterpreterSession
	if err := row.Scan(&session.ID, &session.Backend, &session.CreatedAt, &session.LastActiveAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.CodeInterpreterSession{}, ErrNotFound
		}
		return domain.CodeInterpreterSession{}, fmt.Errorf("get code interpreter session: %w", err)
	}
	return session, nil
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
