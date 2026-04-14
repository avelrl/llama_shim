package sqlite

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type MaintenanceCleanupStats struct {
	ExpiredFilesDeleted        int
	ExpiredVectorStoresDeleted int
}

func (s MaintenanceCleanupStats) TotalDeleted() int {
	return s.ExpiredFilesDeleted + s.ExpiredVectorStoresDeleted
}

func (s *Store) CleanupExpiredState(ctx context.Context, now int64) (MaintenanceCleanupStats, error) {
	vectorStoreIDs, err := s.listExpiredVectorStoreIDs(ctx, now)
	if err != nil {
		return MaintenanceCleanupStats{}, err
	}
	fileIDs, err := s.listExpiredFileIDs(ctx, now)
	if err != nil {
		return MaintenanceCleanupStats{}, err
	}

	stats := MaintenanceCleanupStats{}
	for _, vectorStoreID := range vectorStoreIDs {
		if err := s.DeleteVectorStore(ctx, vectorStoreID); err != nil {
			if err == ErrNotFound {
				continue
			}
			return stats, err
		}
		stats.ExpiredVectorStoresDeleted++
	}
	for _, fileID := range fileIDs {
		if err := s.DeleteFile(ctx, fileID); err != nil {
			if err == ErrNotFound {
				continue
			}
			return stats, err
		}
		stats.ExpiredFilesDeleted++
	}
	return stats, nil
}

func (s *Store) Optimize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		return fmt.Errorf("sqlite optimize: %w", err)
	}
	return nil
}

func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("sqlite vacuum: %w", err)
	}
	return nil
}

func (s *Store) BackupTo(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("backup path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup path %q already exists", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat backup path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("sqlite checkpoint before backup: %w", err)
	}
	stmt := `VACUUM INTO ` + sqliteQuotedString(path)
	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("sqlite backup into %q: %w", path, err)
	}
	return nil
}

func RestoreFromBackup(dstPath, srcPath string) error {
	dstPath = strings.TrimSpace(dstPath)
	srcPath = strings.TrimSpace(srcPath)
	if dstPath == "" {
		return fmt.Errorf("destination sqlite path is required")
	}
	if srcPath == "" {
		return fmt.Errorf("backup source path is required")
	}
	if filepath.Clean(dstPath) == filepath.Clean(srcPath) {
		return fmt.Errorf("backup source and destination must be different")
	}
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat backup source: %w", err)
	}
	if srcInfo.IsDir() {
		return fmt.Errorf("backup source %q must be a file", srcPath)
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("create sqlite dir: %w", err)
	}

	tmpPath := dstPath + ".restore-tmp"
	if err := copyFile(tmpPath, srcPath, srcInfo.Mode()); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace sqlite file: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dstPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale sqlite sidecar %q: %w", dstPath+suffix, err)
		}
	}
	return nil
}

func (s *Store) listExpiredVectorStoreIDs(ctx context.Context, now int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM vector_stores
		WHERE expires_at IS NOT NULL AND expires_at <= ?
		ORDER BY expires_at ASC, id ASC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("list expired vector stores: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired vector store id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired vector stores: %w", err)
	}
	return ids, nil
}

func (s *Store) listExpiredFileIDs(ctx context.Context, now int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM files
		WHERE expires_at IS NOT NULL AND expires_at <= ?
		ORDER BY expires_at ASC, id ASC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("list expired files: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired file id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired files: %w", err)
	}
	return ids, nil
}

func sqliteQuotedString(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func copyFile(dstPath, srcPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open backup source: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return fmt.Errorf("create backup destination: %w", err)
	}
	defer func() {
		_ = dst.Close()
	}()

	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(dstPath)
		return fmt.Errorf("copy backup file: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = os.Remove(dstPath)
		return fmt.Errorf("sync backup destination: %w", err)
	}
	return nil
}
