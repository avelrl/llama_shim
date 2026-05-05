package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"

	"github.com/jackc/pgx/v5"
)

type SQLiteMigrationOptions struct {
	DryRun  bool
	Replace bool
}

type SQLiteMigrationReport struct {
	DryRun                  bool                         `json:"dry_run"`
	Replace                 bool                         `json:"replace"`
	RequiresReplace         bool                         `json:"requires_replace"`
	SourceSQLitePath        string                       `json:"source_sqlite_path,omitempty"`
	TargetBackend           string                       `json:"target_backend"`
	TotalSourceRows         int64                        `json:"total_source_rows"`
	TotalTargetRowsBefore   int64                        `json:"total_target_rows_before"`
	TotalCopiedRows         int64                        `json:"total_copied_rows"`
	ReindexedVectorChunks   int64                        `json:"reindexed_vector_chunks"`
	Tables                  []SQLiteMigrationTableReport `json:"tables"`
	CodeInterpreterMigrated bool                         `json:"code_interpreter_migrated"`
}

type SQLiteMigrationTableReport struct {
	Name             string `json:"name"`
	SourceRows       int64  `json:"source_rows"`
	TargetRowsBefore int64  `json:"target_rows_before"`
	CopiedRows       int64  `json:"copied_rows"`
}

func (s *Store) MigrateSQLiteToPostgres(ctx context.Context, sqlitePath string, options SQLiteMigrationOptions) (SQLiteMigrationReport, error) {
	sqlitePath = strings.TrimSpace(sqlitePath)
	report := SQLiteMigrationReport{
		DryRun:           options.DryRun,
		Replace:          options.Replace,
		SourceSQLitePath: sqlitePath,
		TargetBackend:    "postgres",
		Tables:           make([]SQLiteMigrationTableReport, 0, len(postgresBackupTables)),
	}
	if sqlitePath == "" {
		return report, fmt.Errorf("sqlite source path is required")
	}
	if _, err := os.Stat(sqlitePath); err != nil {
		return report, fmt.Errorf("stat sqlite source: %w", err)
	}
	source, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return report, fmt.Errorf("open sqlite source: %w", err)
	}
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return report, fmt.Errorf("ping sqlite source: %w", err)
	}
	if err := validateSQLiteMigrationSource(ctx, source); err != nil {
		return report, err
	}

	conn, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		return report, fmt.Errorf("open postgres migration connection: %w", err)
	}
	defer conn.Close(ctx)

	for _, table := range postgresBackupTables {
		sourceRows, err := countSQLiteRows(ctx, source, table.Name)
		if err != nil {
			return report, err
		}
		targetRows, err := countPostgresRows(ctx, conn, table.Name)
		if err != nil {
			return report, err
		}
		tableReport := SQLiteMigrationTableReport{
			Name:             table.Name,
			SourceRows:       sourceRows,
			TargetRowsBefore: targetRows,
		}
		report.Tables = append(report.Tables, tableReport)
		report.TotalSourceRows += sourceRows
		report.TotalTargetRowsBefore += targetRows
	}
	report.RequiresReplace = report.TotalTargetRowsBefore > 0 && !options.Replace
	if options.DryRun {
		return report, nil
	}
	if report.RequiresReplace {
		return report, fmt.Errorf("target postgres migration tables are not empty; rerun with -replace to overwrite them")
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("begin sqlite to postgres migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	if options.Replace {
		if _, err := tx.Exec(ctx, `TRUNCATE `+postgresMaintenanceTableList()+` RESTART IDENTITY CASCADE`); err != nil {
			return report, fmt.Errorf("truncate postgres migration target: %w", err)
		}
	}

	for i, table := range postgresBackupTables {
		copied, err := copySQLiteTableToPostgres(ctx, source, tx, table)
		if err != nil {
			return report, err
		}
		report.Tables[i].CopiedRows = copied
		report.TotalCopiedRows += copied
	}
	if _, err := tx.Exec(ctx, `
		SELECT setval(
			pg_get_serial_sequence('vector_store_chunks', 'id'),
			GREATEST((SELECT COALESCE(MAX(id), 0) FROM vector_store_chunks), 1),
			(SELECT COALESCE(MAX(id), 0) > 0 FROM vector_store_chunks)
		)
	`); err != nil {
		return report, fmt.Errorf("reset postgres vector_store_chunks sequence: %w", err)
	}
	reindexed, err := s.reindexMigratedPGVectorChunks(ctx, tx)
	if err != nil {
		return report, err
	}
	report.ReindexedVectorChunks = reindexed
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("commit sqlite to postgres migration: %w", err)
	}
	committed = true

	if err := s.mirrorPostgresFilesToSQLiteSidecar(ctx); err != nil {
		return report, err
	}
	return report, nil
}

func validateSQLiteMigrationSource(ctx context.Context, db *sql.DB) error {
	for _, table := range postgresBackupTables {
		var count int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(1)
			FROM sqlite_master
			WHERE type IN ('table', 'view') AND name = ?
		`, table.Name).Scan(&count); err != nil {
			return fmt.Errorf("query sqlite source table %q: %w", table.Name, err)
		}
		if count == 0 {
			return fmt.Errorf("sqlite source is missing required table %q", table.Name)
		}
	}
	return nil
}

func countSQLiteRows(ctx context.Context, db *sql.DB, tableName string) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+quoteSQLiteIdentifier(tableName)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sqlite table %s: %w", tableName, err)
	}
	return count, nil
}

func countPostgresRows(ctx context.Context, conn *pgx.Conn, tableName string) (int64, error) {
	var count int64
	if err := conn.QueryRow(ctx, `SELECT COUNT(1) FROM `+quotePostgresIdentifier(tableName)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count postgres table %s: %w", tableName, err)
	}
	return count, nil
}

func copySQLiteTableToPostgres(ctx context.Context, source *sql.DB, tx pgx.Tx, table postgresBackupTable) (int64, error) {
	rows, err := source.QueryContext(ctx, sqliteMigrationSelectSQL(table))
	if err != nil {
		return 0, fmt.Errorf("query sqlite migration table %s: %w", table.Name, err)
	}
	defer rows.Close()

	sourceRows := &sqliteCopyFromSource{
		rows:    rows,
		table:   table,
		columns: table.Columns,
	}
	copied, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{table.Name},
		table.Columns,
		sourceRows,
	)
	if err != nil {
		return copied, fmt.Errorf("copy sqlite table %s to postgres: %w", table.Name, err)
	}
	if sourceRows.Err() != nil {
		return copied, fmt.Errorf("read sqlite table %s: %w", table.Name, sourceRows.Err())
	}
	return copied, nil
}

type sqliteCopyFromSource struct {
	rows       *sql.Rows
	table      postgresBackupTable
	columns    []string
	values     []any
	scanValues []any
	scanPtrs   []any
	err        error
}

func (s *sqliteCopyFromSource) Next() bool {
	if !s.rows.Next() {
		s.err = s.rows.Err()
		return false
	}
	if s.scanValues == nil {
		s.scanValues = make([]any, len(s.columns))
		s.scanPtrs = make([]any, len(s.columns))
		for i := range s.scanValues {
			s.scanPtrs[i] = &s.scanValues[i]
		}
	}
	for i := range s.scanValues {
		s.scanValues[i] = nil
	}
	if err := s.rows.Scan(s.scanPtrs...); err != nil {
		s.err = err
		return false
	}
	s.values = make([]any, len(s.columns))
	for i, column := range s.columns {
		s.values[i] = normalizeSQLiteMigrationValue(s.table.Name, column, s.scanValues[i])
	}
	return true
}

func (s *sqliteCopyFromSource) Values() ([]any, error) {
	return s.values, nil
}

func (s *sqliteCopyFromSource) Err() error {
	return s.err
}

func normalizeSQLiteMigrationValue(tableName, column string, value any) any {
	if value == nil {
		return nil
	}
	if tableName == "responses" && column == "store" {
		switch typed := value.(type) {
		case bool:
			return typed
		case int64:
			return typed != 0
		case int:
			return typed != 0
		case []byte:
			return strings.TrimSpace(string(typed)) != "0"
		case string:
			return strings.TrimSpace(typed) != "0"
		default:
			return value
		}
	}
	if tableName == "files" && column == "content" {
		switch typed := value.(type) {
		case []byte:
			return typed
		case string:
			return []byte(typed)
		default:
			return value
		}
	}
	if typed, ok := value.([]byte); ok {
		return string(typed)
	}
	return value
}

func sqliteMigrationSelectSQL(table postgresBackupTable) string {
	columns := sqliteMigrationSelectColumns(table)
	return fmt.Sprintf(
		"SELECT %s FROM %s ORDER BY %s",
		strings.Join(columns, ", "),
		quoteSQLiteIdentifier(table.Name),
		table.OrderBy,
	)
}

func sqliteMigrationSelectColumns(table postgresBackupTable) []string {
	if table.Name != "vector_store_chunks" {
		columns := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			columns = append(columns, quoteSQLiteIdentifier(column))
		}
		return columns
	}
	return []string{
		quoteSQLiteIdentifier("id"),
		quoteSQLiteIdentifier("vector_store_id"),
		quoteSQLiteIdentifier("file_id"),
		quoteSQLiteIdentifier("chunk_index"),
		quoteSQLiteIdentifier("content"),
		quoteSQLiteIdentifier("token_count"),
		"NULL AS embedding",
		"NULL AS embedding_model",
		"NULL AS embedding_dimensions",
		"NULL AS embedding_created_at",
	}
}

func quoteSQLiteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quotePostgresIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (s *Store) reindexMigratedPGVectorChunks(ctx context.Context, tx pgx.Tx) (int64, error) {
	if s == nil || s.retrieval.IndexBackend != retrieval.IndexBackendPGVector {
		return 0, nil
	}
	if s.embedder == nil {
		return 0, fmt.Errorf("pgvector migration reindex requires a configured embedder")
	}
	total := int64(0)
	lastID := int64(0)
	for {
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.content
			FROM vector_store_chunks c
			JOIN vector_store_files f
			  ON f.vector_store_id = c.vector_store_id AND f.file_id = c.file_id
			WHERE c.id > $1
			  AND c.embedding IS NULL
			  AND f.status = 'completed'
			ORDER BY c.id ASC
			LIMIT $2
		`, lastID, retrievalEmbedBatchSize)
		if err != nil {
			return total, fmt.Errorf("query migrated pgvector chunks: %w", err)
		}
		ids := make([]int64, 0, retrievalEmbedBatchSize)
		texts := make([]string, 0, retrievalEmbedBatchSize)
		for rows.Next() {
			var (
				id      int64
				content string
			)
			if err := rows.Scan(&id, &content); err != nil {
				rows.Close()
				return total, fmt.Errorf("scan migrated pgvector chunk: %w", err)
			}
			ids = append(ids, id)
			texts = append(texts, content)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return total, fmt.Errorf("iterate migrated pgvector chunks: %w", err)
		}
		rows.Close()
		if len(ids) == 0 {
			return total, nil
		}

		embeddings, _, err := s.embedTexts(ctx, texts)
		if err != nil {
			return total, fmt.Errorf("embed migrated pgvector chunks: %w", err)
		}
		createdAt := domain.NowUTC().Unix()
		for i, id := range ids {
			if _, err := tx.Exec(ctx, `
				UPDATE vector_store_chunks
				SET embedding = $2::vector,
				    embedding_model = $3,
				    embedding_dimensions = $4,
				    embedding_created_at = $5
				WHERE id = $1
			`, id, pgVectorLiteral(embeddings[i]), s.embeddingModel, len(embeddings[i]), createdAt); err != nil {
				return total, fmt.Errorf("update migrated pgvector chunk embedding: %w", err)
			}
			total++
			lastID = id
		}
	}
}
