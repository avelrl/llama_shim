package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/migrations"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

const defaultBusyTimeout = 5 * time.Second

type Store struct {
	db        *sql.DB
	retrieval retrievalBackend
}

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

func Open(ctx context.Context, path string) (*Store, error) {
	return OpenWithOptions(ctx, path, OpenOptions{})
}

func OpenWithOptions(ctx context.Context, path string, options OpenOptions) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	options, err := normalizeOpenOptions(options)
	if err != nil {
		return nil, fmt.Errorf("normalize open options: %w", err)
	}
	retrievalBackend, err := newRetrievalBackendWithOptions(options.Retrieval, options.Embedder)
	if err != nil {
		return nil, fmt.Errorf("build retrieval backend: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	store := &Store{
		db:        db,
		retrieval: retrievalBackend,
	}
	if err := store.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) PingContext(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) configure(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", defaultBusyTimeout.Milliseconds()),
	}

	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply sqlite pragma %q: %w", stmt, err)
		}
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version := entry.Name()
		applied, err := s.isMigrationApplied(ctx, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		body, err := migrations.FS.ReadFile(version)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}

		if err := s.applyMigration(ctx, version, string(body)); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) isMigrationApplied(ctx context.Context, version string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
		return false, fmt.Errorf("query schema_migrations: %w", err)
	}
	return count > 0, nil
}

func (s *Store) applyMigration(ctx context.Context, version, sqlBody string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, sqlBody); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, domain.FormatTime(domain.NowUTC())); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	return nil
}
