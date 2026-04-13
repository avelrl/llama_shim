package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
)

func (s *Store) SaveCodeInterpreterContainerFile(ctx context.Context, file domain.CodeInterpreterContainerFile) (domain.CodeInterpreterContainerFile, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("begin code interpreter container file tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var existingID string
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM code_interpreter_container_files
		WHERE container_id = ? AND path = ?
	`, file.ContainerID, file.Path).Scan(&existingID)
	switch {
	case err == nil:
		if strings.TrimSpace(file.ID) == "" {
			file.ID = existingID
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("lookup code interpreter container file by path: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM code_interpreter_container_files
		WHERE container_id = ? AND (path = ? OR id = ?)
	`, file.ContainerID, file.Path, file.ID); err != nil {
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("delete conflicting code interpreter container files: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO code_interpreter_container_files (
			id, container_id, backing_file_id, path, source, bytes, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, file.ID, file.ContainerID, file.BackingFileID, file.Path, file.Source, file.Bytes, file.CreatedAt); err != nil {
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("insert code interpreter container file: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("commit code interpreter container file tx: %w", err)
	}
	return file, nil
}

func (s *Store) GetCodeInterpreterContainerFile(ctx context.Context, containerID string, id string) (domain.CodeInterpreterContainerFile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, container_id, backing_file_id, path, source, bytes, created_at
		FROM code_interpreter_container_files
		WHERE container_id = ? AND id = ?
	`, containerID, id)

	var file domain.CodeInterpreterContainerFile
	if err := row.Scan(&file.ID, &file.ContainerID, &file.BackingFileID, &file.Path, &file.Source, &file.Bytes, &file.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.CodeInterpreterContainerFile{}, ErrNotFound
		}
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("get code interpreter container file: %w", err)
	}
	return file, nil
}

func (s *Store) ListCodeInterpreterContainerFiles(ctx context.Context, query domain.ListCodeInterpreterContainerFilesQuery) (domain.CodeInterpreterContainerFilePage, error) {
	orderDir := "DESC"
	if query.Order == domain.ListOrderAsc {
		orderDir = "ASC"
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, container_id, backing_file_id, path, source, bytes, created_at
		FROM code_interpreter_container_files
		WHERE container_id = ?
		ORDER BY created_at `+orderDir+`, id `+orderDir, query.ContainerID)
	if err != nil {
		return domain.CodeInterpreterContainerFilePage{}, fmt.Errorf("list code interpreter container files: %w", err)
	}
	defer rows.Close()

	files := make([]domain.CodeInterpreterContainerFile, 0, query.Limit+1)
	for rows.Next() {
		var file domain.CodeInterpreterContainerFile
		if err := rows.Scan(&file.ID, &file.ContainerID, &file.BackingFileID, &file.Path, &file.Source, &file.Bytes, &file.CreatedAt); err != nil {
			return domain.CodeInterpreterContainerFilePage{}, fmt.Errorf("scan code interpreter container file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return domain.CodeInterpreterContainerFilePage{}, fmt.Errorf("iterate code interpreter container files: %w", err)
	}

	items, hasMore, err := paginateCodeInterpreterContainerFiles(files, query.After, query.Limit)
	if err != nil {
		return domain.CodeInterpreterContainerFilePage{}, err
	}
	return domain.CodeInterpreterContainerFilePage{Files: items, HasMore: hasMore}, nil
}

func (s *Store) DeleteCodeInterpreterContainerFile(ctx context.Context, containerID string, id string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM code_interpreter_container_files
		WHERE container_id = ? AND id = ?
	`, containerID, id)
	if err != nil {
		return fmt.Errorf("delete code interpreter container file: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete code interpreter container file rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func paginateCodeInterpreterContainerFiles(files []domain.CodeInterpreterContainerFile, after string, limit int) ([]domain.CodeInterpreterContainerFile, bool, error) {
	start := 0
	if cursor := strings.TrimSpace(after); cursor != "" {
		found := false
		for i, file := range files {
			if file.ID == cursor {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, false, domain.NewValidationError("after", "after must reference an existing container file")
		}
	}

	if start >= len(files) {
		return []domain.CodeInterpreterContainerFile{}, false, nil
	}
	end := start + limit
	if end > len(files) {
		end = len(files)
	}
	items := append([]domain.CodeInterpreterContainerFile(nil), files[start:end]...)
	return items, end < len(files), nil
}
