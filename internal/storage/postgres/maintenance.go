package postgres

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama_shim/internal/storage"

	"github.com/jackc/pgx/v5"
)

const postgresLogicalBackupHeader = "-- llama_shim postgres logical backup v1"

type postgresBackupTable struct {
	Name    string
	Columns []string
	OrderBy string
}

var postgresBackupTables = []postgresBackupTable{
	{
		Name:    "files",
		Columns: []string{"id", "purpose", "filename", "bytes", "created_at", "expires_at", "status", "status_details", "content"},
		OrderBy: "id",
	},
	{
		Name:    "vector_stores",
		Columns: []string{"id", "name", "metadata_json", "created_at", "last_active_at", "expires_after_anchor", "expires_after_days", "expires_at"},
		OrderBy: "id",
	},
	{
		Name:    "vector_store_files",
		Columns: []string{"vector_store_id", "file_id", "created_at", "status", "usage_bytes", "last_error_json", "attributes_json", "chunking_strategy_json"},
		OrderBy: "vector_store_id, file_id",
	},
	{
		Name:    "vector_store_chunks",
		Columns: []string{"id", "vector_store_id", "file_id", "chunk_index", "content", "token_count", "embedding", "embedding_model", "embedding_dimensions", "embedding_created_at"},
		OrderBy: "id",
	},
	{
		Name:    "responses",
		Columns: []string{"id", "model", "request_json", "normalized_input_items_json", "effective_input_items_json", "output_json", "output_text", "previous_response_id", "conversation_id", "store", "created_at", "completed_at", "response_json"},
		OrderBy: "id",
	},
	{
		Name:    "response_replay_artifacts",
		Columns: []string{"response_id", "sequence_number", "event_type", "payload_json"},
		OrderBy: "response_id, sequence_number",
	},
	{
		Name:    "conversations",
		Columns: []string{"id", "version", "metadata_json", "created_at", "updated_at"},
		OrderBy: "id",
	},
	{
		Name:    "conversation_items",
		Columns: []string{"id", "conversation_id", "seq", "source", "role", "item_type", "item_json", "created_at"},
		OrderBy: "conversation_id, seq, id",
	},
	{
		Name:    "chat_completions",
		Columns: []string{"id", "model", "metadata_json", "request_json", "response_json", "created_at"},
		OrderBy: "id",
	},
	{
		Name:    "chat_completion_messages",
		Columns: []string{"completion_id", "sequence_number", "message_id", "message_json"},
		OrderBy: "completion_id, sequence_number",
	},
}

var postgresBackupTableByHeader = func() map[string]postgresBackupTable {
	out := make(map[string]postgresBackupTable, len(postgresBackupTables))
	for _, table := range postgresBackupTables {
		out[postgresBackupTableHeader(table)] = table
	}
	return out
}()

func (s *Store) CleanupExpiredState(ctx context.Context, now int64, policies ...storage.MaintenanceCleanupPolicy) (storage.MaintenanceCleanupStats, error) {
	vectorStoreIDs, err := s.listExpiredVectorStoreIDs(ctx, now)
	if err != nil {
		return storage.MaintenanceCleanupStats{}, err
	}
	fileIDs, err := s.listExpiredFileIDs(ctx, now)
	if err != nil {
		return storage.MaintenanceCleanupStats{}, err
	}

	stats := storage.MaintenanceCleanupStats{}
	for _, vectorStoreID := range vectorStoreIDs {
		if err := s.DeleteVectorStore(ctx, vectorStoreID); err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return stats, err
		}
		stats.ExpiredVectorStoresDeleted++
	}
	for _, fileID := range fileIDs {
		if err := s.DeleteFile(ctx, fileID); err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return stats, err
		}
		stats.ExpiredFilesDeleted++
	}
	policy := storage.NormalizeMaintenanceCleanupPolicy(policies...)
	replayArtifactsDeleted, replayArtifactResponses, err := s.cleanupResponseReplayArtifacts(ctx, now, policy)
	if err != nil {
		return stats, err
	}
	stats.ResponseReplayArtifactsDeleted = replayArtifactsDeleted
	stats.ResponseReplayArtifactResponsesPruned = replayArtifactResponses
	return stats, nil
}

func (s *Store) Optimize(ctx context.Context) error {
	if err := s.ensurePGVectorANNIndex(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `ANALYZE `+postgresMaintenanceTableList()); err != nil {
		return fmt.Errorf("postgres analyze: %w", err)
	}
	if s.Store != nil {
		if err := s.Store.Optimize(ctx); err != nil {
			return fmt.Errorf("sqlite sidecar optimize: %w", err)
		}
	}
	return nil
}

func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM (ANALYZE) `+postgresMaintenanceTableList()); err != nil {
		return fmt.Errorf("postgres vacuum: %w", err)
	}
	if s.Store != nil {
		if err := s.Store.Vacuum(ctx); err != nil {
			return fmt.Errorf("sqlite sidecar vacuum: %w", err)
		}
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

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create postgres backup: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()

	writer := bufio.NewWriter(file)
	conn, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		return fmt.Errorf("open postgres backup connection: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := fmt.Fprintf(writer, "%s\n-- created_at=%s\n", postgresLogicalBackupHeader, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("write postgres backup header: %w", err)
	}
	for _, table := range postgresBackupTables {
		if _, err := fmt.Fprintln(writer, postgresBackupTableHeader(table)); err != nil {
			return fmt.Errorf("write postgres backup table header: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush postgres backup before copy: %w", err)
		}
		if _, err := conn.PgConn().CopyTo(ctx, writer, postgresCopyToSQL(table)); err != nil {
			return fmt.Errorf("copy postgres table %s to backup: %w", table.Name, err)
		}
		if _, err := fmt.Fprintln(writer, `\.`); err != nil {
			return fmt.Errorf("write postgres backup table terminator: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush postgres backup: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync postgres backup: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) RestoreFromBackup(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("backup source path is required")
	}
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open postgres backup source: %w", err)
	}
	defer source.Close()

	conn, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		return fmt.Errorf("open postgres restore connection: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `BEGIN`); err != nil {
		return fmt.Errorf("begin postgres restore: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.Exec(context.Background(), `ROLLBACK`)
		}
	}()

	if _, err := conn.Exec(ctx, `TRUNCATE `+postgresMaintenanceTableList()+` RESTART IDENTITY CASCADE`); err != nil {
		return fmt.Errorf("truncate postgres restore tables: %w", err)
	}
	if err := restorePostgresBackupSections(ctx, conn, source); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `
		SELECT setval(
			pg_get_serial_sequence('vector_store_chunks', 'id'),
			GREATEST((SELECT COALESCE(MAX(id), 0) FROM vector_store_chunks), 1),
			(SELECT COALESCE(MAX(id), 0) > 0 FROM vector_store_chunks)
		)
	`); err != nil {
		return fmt.Errorf("reset postgres vector_store_chunks sequence: %w", err)
	}
	if _, err := conn.Exec(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit postgres restore: %w", err)
	}
	committed = true

	if err := s.mirrorPostgresFilesToSQLiteSidecar(ctx); err != nil {
		return err
	}
	return nil
}

func restorePostgresBackupSections(ctx context.Context, conn *pgx.Conn, source io.Reader) error {
	reader := bufio.NewReader(source)
	headerSeen := false
	expectedTableIndex := 0
	seenTables := map[string]struct{}{}

	for {
		line, err := readPostgresBackupLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "--") {
			if trimmed == postgresLogicalBackupHeader {
				headerSeen = true
			}
			continue
		}

		if !headerSeen {
			return fmt.Errorf("postgres backup header is missing")
		}
		table, ok := postgresBackupTableByHeader[trimmed]
		if !ok {
			return fmt.Errorf("unsupported postgres backup section %q", trimmed)
		}
		if expectedTableIndex >= len(postgresBackupTables) || postgresBackupTables[expectedTableIndex].Name != table.Name {
			return fmt.Errorf("postgres backup section %q is out of order", table.Name)
		}
		if _, exists := seenTables[table.Name]; exists {
			return fmt.Errorf("duplicate postgres backup section %q", table.Name)
		}
		seenTables[table.Name] = struct{}{}
		expectedTableIndex++

		if err := restorePostgresBackupTable(ctx, conn, reader, table); err != nil {
			return err
		}
	}

	if !headerSeen {
		return fmt.Errorf("postgres backup header is missing")
	}
	if expectedTableIndex != len(postgresBackupTables) {
		return fmt.Errorf("postgres backup is incomplete: got %d table sections, want %d", expectedTableIndex, len(postgresBackupTables))
	}
	return nil
}

func restorePostgresBackupTable(ctx context.Context, conn *pgx.Conn, reader *bufio.Reader, table postgresBackupTable) error {
	pipeReader, pipeWriter := io.Pipe()
	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)
		for {
			line, err := readPostgresBackupLine(reader)
			if err != nil {
				if errors.Is(err, io.EOF) {
					_ = pipeWriter.CloseWithError(fmt.Errorf("postgres backup section %s missing terminator", table.Name))
					errCh <- fmt.Errorf("postgres backup section %s missing terminator", table.Name)
					return
				}
				_ = pipeWriter.CloseWithError(err)
				errCh <- err
				return
			}
			if strings.TrimRight(line, "\r\n") == `\.` {
				_ = pipeWriter.Close()
				errCh <- nil
				return
			}
			if _, err := io.WriteString(pipeWriter, line); err != nil {
				_ = pipeWriter.CloseWithError(err)
				errCh <- err
				return
			}
		}
	}()

	_, copyErr := conn.PgConn().CopyFrom(ctx, pipeReader, postgresCopyFromSQL(table))
	if copyErr != nil {
		_ = pipeReader.CloseWithError(copyErr)
	}
	readErr := <-errCh
	if copyErr != nil {
		return fmt.Errorf("copy postgres backup table %s into database: %w", table.Name, copyErr)
	}
	if readErr != nil {
		return fmt.Errorf("read postgres backup table %s: %w", table.Name, readErr)
	}
	return nil
}

func (s *Store) mirrorPostgresFilesToSQLiteSidecar(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, purpose, filename, bytes, created_at, expires_at, status, status_details, content
		FROM files
		ORDER BY id ASC
	`)
	if err != nil {
		return fmt.Errorf("list postgres files for sidecar mirror: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		file, err := scanStoredFile(rows)
		if err != nil {
			return err
		}
		if err := s.Store.SaveFile(ctx, file); err != nil {
			return fmt.Errorf("mirror restored postgres file to sqlite sidecar: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate restored postgres files for sidecar mirror: %w", err)
	}
	return nil
}

func (s *Store) listExpiredVectorStoreIDs(ctx context.Context, now int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM vector_stores
		WHERE expires_at IS NOT NULL AND expires_at <= $1
		ORDER BY expires_at ASC, id ASC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("list expired postgres vector stores: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired postgres vector store id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired postgres vector stores: %w", err)
	}
	return ids, nil
}

func (s *Store) listExpiredFileIDs(ctx context.Context, now int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM files
		WHERE expires_at IS NOT NULL AND expires_at <= $1
		ORDER BY expires_at ASC, id ASC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("list expired postgres files: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired postgres file id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired postgres files: %w", err)
	}
	return ids, nil
}

func (s *Store) cleanupResponseReplayArtifacts(ctx context.Context, now int64, policy storage.MaintenanceCleanupPolicy) (int, int, error) {
	totalArtifactsDeleted := 0
	totalResponsesPruned := 0

	if cutoff, ok := policy.ResponseReplayArtifactsAgeCutoff(now); ok {
		artifactsDeleted, responsesPruned, err := s.deleteStandaloneResponseReplayArtifactsByAge(ctx, cutoff)
		if err != nil {
			return totalArtifactsDeleted, totalResponsesPruned, err
		}
		totalArtifactsDeleted += artifactsDeleted
		totalResponsesPruned += responsesPruned
	}
	if policy.ResponseReplayArtifactsCountRetentionEnabled() {
		artifactsDeleted, responsesPruned, err := s.deleteStandaloneResponseReplayArtifactsBeyondMaxResponses(ctx, policy.ResponseReplayArtifactsMaxResponses)
		if err != nil {
			return totalArtifactsDeleted, totalResponsesPruned, err
		}
		totalArtifactsDeleted += artifactsDeleted
		totalResponsesPruned += responsesPruned
	}
	return totalArtifactsDeleted, totalResponsesPruned, nil
}

func (s *Store) deleteStandaloneResponseReplayArtifactsByAge(ctx context.Context, cutoffCreatedAt string) (int, int, error) {
	responsesPruned, err := s.countStandaloneResponseIDsWithReplayArtifactsBefore(ctx, cutoffCreatedAt)
	if err != nil {
		return 0, 0, err
	}
	if responsesPruned == 0 {
		return 0, 0, nil
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM response_replay_artifacts rra
		USING responses r
		WHERE r.id = rra.response_id
		  AND COALESCE(r.conversation_id, '') = ''
		  AND r.created_at <> ''
		  AND r.created_at < $1
	`, cutoffCreatedAt)
	if err != nil {
		return 0, 0, fmt.Errorf("delete aged postgres response replay artifacts: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count aged postgres response replay artifacts deleted: %w", err)
	}
	return int(affected), responsesPruned, nil
}

func (s *Store) deleteStandaloneResponseReplayArtifactsBeyondMaxResponses(ctx context.Context, maxResponses int) (int, int, error) {
	responsesPruned, err := s.countStandaloneResponseIDsWithReplayArtifactsBeyondMaxResponses(ctx, maxResponses)
	if err != nil {
		return 0, 0, err
	}
	if responsesPruned == 0 {
		return 0, 0, nil
	}
	result, err := s.db.ExecContext(ctx, `
		WITH pruned AS (
			SELECT id
			FROM (
				SELECT r.id,
				       row_number() OVER (ORDER BY r.created_at DESC, r.id DESC) AS retained_rank
				FROM responses r
				WHERE COALESCE(r.conversation_id, '') = ''
				  AND r.created_at <> ''
				  AND EXISTS (
					SELECT 1
					FROM response_replay_artifacts rra
					WHERE rra.response_id = r.id
				  )
			) ranked
			WHERE retained_rank > $1
		)
		DELETE FROM response_replay_artifacts rra
		USING pruned
		WHERE rra.response_id = pruned.id
	`, maxResponses)
	if err != nil {
		return 0, 0, fmt.Errorf("delete excess postgres response replay artifacts: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count excess postgres response replay artifacts deleted: %w", err)
	}
	return int(affected), responsesPruned, nil
}

func (s *Store) countStandaloneResponseIDsWithReplayArtifactsBefore(ctx context.Context, cutoffCreatedAt string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT DISTINCT r.id
			FROM responses r
			JOIN response_replay_artifacts rra ON rra.response_id = r.id
			WHERE COALESCE(r.conversation_id, '') = ''
			  AND r.created_at <> ''
			  AND r.created_at < $1
		) aged
	`, cutoffCreatedAt).Scan(&count); err != nil {
		return 0, fmt.Errorf("count aged postgres response replay artifact responses: %w", err)
	}
	return count, nil
}

func (s *Store) countStandaloneResponseIDsWithReplayArtifactsBeyondMaxResponses(ctx context.Context, maxResponses int) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT id
			FROM (
				SELECT r.id,
				       row_number() OVER (ORDER BY r.created_at DESC, r.id DESC) AS retained_rank
				FROM responses r
				WHERE COALESCE(r.conversation_id, '') = ''
				  AND r.created_at <> ''
				  AND EXISTS (
					SELECT 1
					FROM response_replay_artifacts rra
					WHERE rra.response_id = r.id
				  )
			) ranked
			WHERE retained_rank > $1
		) excess
	`, maxResponses).Scan(&count); err != nil {
		return 0, fmt.Errorf("count excess postgres response replay artifact responses: %w", err)
	}
	return count, nil
}

func postgresBackupTableHeader(table postgresBackupTable) string {
	return fmt.Sprintf("COPY %s (%s) FROM stdin;", table.Name, strings.Join(table.Columns, ", "))
}

func postgresCopyToSQL(table postgresBackupTable) string {
	return fmt.Sprintf(
		"COPY (SELECT %s FROM %s ORDER BY %s) TO STDOUT",
		strings.Join(table.Columns, ", "),
		table.Name,
		table.OrderBy,
	)
}

func postgresCopyFromSQL(table postgresBackupTable) string {
	return fmt.Sprintf("COPY %s (%s) FROM STDIN", table.Name, strings.Join(table.Columns, ", "))
}

func postgresMaintenanceTableList() string {
	names := make([]string, 0, len(postgresBackupTables))
	for _, table := range postgresBackupTables {
		names = append(names, table.Name)
	}
	return strings.Join(names, ", ")
}

func readPostgresBackupLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return line, nil
		}
		return "", err
	}
	return line, nil
}
