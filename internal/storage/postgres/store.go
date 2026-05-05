package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
	"llama_shim/internal/storage/sqlite"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type OpenOptions struct {
	SQLitePath string
	Retrieval  retrieval.Config
	Embedder   retrieval.Embedder
}

type Store struct {
	*sqlite.Store

	db             *sql.DB
	retrieval      retrieval.Config
	embedder       retrieval.Embedder
	embeddingModel string
}

var _ storage.Store = (*Store)(nil)

var ErrNotFound = storage.ErrNotFound

const (
	retrievalEmbedBatchSize        = 128
	maxContentItemsPerResult       = 3
	maxLexicalCandidateChunks      = 1000
	minSemanticCandidateChunks     = 50
	maxSemanticCandidateChunks     = 1000
	postgresSchemaMigrationVersion = "0001_postgres_object_storage_alpha"
)

type scanner interface {
	Scan(...any) error
}

type rankedSearchContent struct {
	text  string
	score float64
	order int
}

type aggregatedSearchResult struct {
	domain.VectorStoreSearchResult
	bestDistance     float64
	contentRanks     []rankedSearchContent
	seenContentText  map[string]struct{}
	nextContentOrder int
}

type vectorStoreChunk struct {
	ID      int64
	Content string
}

func OpenWithOptions(ctx context.Context, dsn string, options OpenOptions) (*Store, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("postgres dsn is required")
	}
	sqlitePath := strings.TrimSpace(options.SQLitePath)
	if sqlitePath == "" {
		return nil, fmt.Errorf("sqlite sidecar path is required")
	}
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite sidecar dir: %w", err)
	}

	cfg, err := retrieval.NormalizeConfig(options.Retrieval)
	if err != nil {
		return nil, fmt.Errorf("normalize retrieval config: %w", err)
	}
	switch cfg.IndexBackend {
	case retrieval.IndexBackendLexical, retrieval.IndexBackendPGVector:
	default:
		return nil, fmt.Errorf("postgres storage supports retrieval index backend %q or %q, got %q", retrieval.IndexBackendLexical, retrieval.IndexBackendPGVector, cfg.IndexBackend)
	}

	embedder := options.Embedder
	if cfg.IndexBackend == retrieval.IndexBackendPGVector {
		if embedder == nil {
			embedder, err = retrieval.NewEmbedder(cfg.Embedder)
			if err != nil {
				return nil, fmt.Errorf("build retrieval embedder: %w", err)
			}
		}
		if embedder == nil {
			return nil, fmt.Errorf("retrieval index backend %q requires a configured embedder backend", cfg.IndexBackend)
		}
	}

	sidecar, err := sqlite.OpenWithOptions(ctx, sqlitePath, sqlite.OpenOptions{
		Retrieval: retrieval.Config{IndexBackend: retrieval.IndexBackendLexical},
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite sidecar: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		_ = sidecar.Close()
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	store := &Store{
		Store:          sidecar,
		db:             db,
		retrieval:      cfg,
		embedder:       embedder,
		embeddingModel: cfg.Embedder.Model,
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		_ = sidecar.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) SQLiteSidecar() *sqlite.Store {
	if s == nil {
		return nil
	}
	return s.Store
}

func (s *Store) Close() error {
	var errs []error
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.Store != nil {
		if err := s.Store.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Store) PingContext(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store is not open")
	}
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	if s.Store != nil {
		return s.Store.PingContext(ctx)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, postgresObjectStorageSchema); err != nil {
		return fmt.Errorf("apply postgres object storage schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, applied_at)
		VALUES ($1, now())
		ON CONFLICT(version) DO NOTHING
	`, postgresSchemaMigrationVersion); err != nil {
		return fmt.Errorf("record postgres migration: %w", err)
	}
	return nil
}

func (s *Store) SaveFile(ctx context.Context, file domain.StoredFile) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (
			id, purpose, filename, bytes, created_at, expires_at, status, status_details, content
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT(id) DO UPDATE SET
			purpose = excluded.purpose,
			filename = excluded.filename,
			bytes = excluded.bytes,
			created_at = excluded.created_at,
			expires_at = excluded.expires_at,
			status = excluded.status,
			status_details = excluded.status_details,
			content = excluded.content
	`, file.ID, file.Purpose, file.Filename, file.Bytes, file.CreatedAt, file.ExpiresAt, nullableString(file.Status), file.StatusDetails, file.Content)
	if err != nil {
		return fmt.Errorf("upsert postgres file: %w", err)
	}
	if s.Store != nil {
		if err := s.Store.SaveFile(ctx, file); err != nil {
			return fmt.Errorf("mirror file to sqlite sidecar: %w", err)
		}
	}
	return nil
}

func (s *Store) GetFile(ctx context.Context, id string) (domain.StoredFile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, purpose, filename, bytes, created_at, expires_at, status, status_details, content
		FROM files
		WHERE id = $1
	`, id)
	file, err := scanStoredFile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredFile{}, ErrNotFound
		}
		return domain.StoredFile{}, err
	}
	return file, nil
}

func (s *Store) ListFiles(ctx context.Context, query domain.ListFilesQuery) (domain.StoredFilePage, error) {
	orderDir, comparison := "DESC", "<"
	if query.Order == domain.ListOrderAsc {
		orderDir, comparison = "ASC", ">"
	}

	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	purpose := strings.TrimSpace(query.Purpose)
	if purpose != "" {
		args = append(args, purpose)
		where = append(where, fmt.Sprintf("purpose = $%d", len(args)))
	}
	if cursor := strings.TrimSpace(query.After); cursor != "" {
		cursorCreatedAt, err := s.lookupFileCursorCreatedAt(ctx, cursor, purpose)
		if err != nil {
			return domain.StoredFilePage{}, err
		}
		args = append(args, cursorCreatedAt, cursorCreatedAt, cursor)
		where = append(where, fmt.Sprintf("(created_at %s $%d OR (created_at = $%d AND id %s $%d))", comparison, len(args)-2, len(args)-1, comparison, len(args)))
	}

	statement := `
		SELECT id, purpose, filename, bytes, created_at, expires_at, status, status_details
		FROM files
	`
	if len(where) != 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, query.Limit+1)
	statement += fmt.Sprintf(" ORDER BY created_at %s, id %s LIMIT $%d", orderDir, orderDir, len(args))

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredFilePage{}, fmt.Errorf("list postgres files: %w", err)
	}
	defer rows.Close()

	files := make([]domain.StoredFile, 0, query.Limit+1)
	for rows.Next() {
		file, err := scanStoredFileMetadata(rows)
		if err != nil {
			return domain.StoredFilePage{}, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return domain.StoredFilePage{}, fmt.Errorf("iterate postgres files: %w", err)
	}
	hasMore := len(files) > query.Limit
	if hasMore {
		files = files[:query.Limit]
	}
	return domain.StoredFilePage{Files: files, HasMore: hasMore}, nil
}

func (s *Store) DeleteFile(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete postgres file: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete postgres file rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	if s.Store != nil {
		if err := s.Store.DeleteFile(ctx, id); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("delete sqlite sidecar file: %w", err)
		}
	}
	return nil
}

func (s *Store) SaveVectorStore(ctx context.Context, store domain.StoredVectorStore) error {
	metadataJSON, err := json.Marshal(store.Metadata)
	if err != nil {
		return fmt.Errorf("marshal vector store metadata: %w", err)
	}
	var anchor any
	var days any
	if store.ExpiresAfter != nil {
		anchor = store.ExpiresAfter.Anchor
		days = store.ExpiresAfter.Days
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO vector_stores (
			id, name, metadata_json, created_at, last_active_at, expires_after_anchor, expires_after_days, expires_at
		) VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7, $8)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			metadata_json = excluded.metadata_json,
			created_at = excluded.created_at,
			last_active_at = excluded.last_active_at,
			expires_after_anchor = excluded.expires_after_anchor,
			expires_after_days = excluded.expires_after_days,
			expires_at = excluded.expires_at
	`, store.ID, store.Name, string(metadataJSON), store.CreatedAt, store.LastActiveAt, anchor, days, store.ExpiresAt)
	if err != nil {
		return fmt.Errorf("upsert postgres vector store: %w", err)
	}
	return nil
}

func (s *Store) AttachFileToVectorStore(ctx context.Context, vectorStoreID, fileID string, attributes map[string]any, strategy domain.FileChunkingStrategy, createdAt int64) (domain.StoredVectorStoreFile, error) {
	if _, err := s.getVectorStoreBase(ctx, vectorStoreID); err != nil {
		return domain.StoredVectorStoreFile{}, err
	}
	file, err := s.GetFile(ctx, fileID)
	if err != nil {
		return domain.StoredVectorStoreFile{}, err
	}
	chunks, status, lastError := buildVectorStoreFileContent(file.Content, strategy)
	usageBytes := int64(0)
	if status == "completed" {
		usageBytes = file.Bytes
	}
	attachment := domain.StoredVectorStoreFile{
		ID:               file.ID,
		CreatedAt:        createdAt,
		VectorStoreID:    vectorStoreID,
		Status:           status,
		UsageBytes:       usageBytes,
		LastError:        lastError,
		Attributes:       attributes,
		ChunkingStrategy: strategy,
	}
	if err := s.SaveVectorStoreFile(ctx, attachment, chunks); err != nil {
		return domain.StoredVectorStoreFile{}, err
	}
	return attachment, nil
}

func (s *Store) GetVectorStore(ctx context.Context, id string) (domain.StoredVectorStore, error) {
	base, err := s.getVectorStoreBase(ctx, id)
	if err != nil {
		return domain.StoredVectorStore{}, err
	}
	return s.hydrateVectorStore(ctx, base)
}

func (s *Store) ListVectorStores(ctx context.Context, query domain.ListVectorStoresQuery) (domain.StoredVectorStorePage, error) {
	orderDir := "DESC"
	afterComparison, beforeComparison := "<", ">"
	if query.Order == domain.ListOrderAsc {
		orderDir, afterComparison, beforeComparison = "ASC", ">", "<"
	}

	where := make([]string, 0, 2)
	args := make([]any, 0, 6)
	if cursor := strings.TrimSpace(query.After); cursor != "" {
		createdAt, err := s.lookupVectorStoreCursorCreatedAt(ctx, cursor)
		if err != nil {
			return domain.StoredVectorStorePage{}, err
		}
		args = append(args, createdAt, createdAt, cursor)
		where = append(where, fmt.Sprintf("(created_at %s $%d OR (created_at = $%d AND id %s $%d))", afterComparison, len(args)-2, len(args)-1, afterComparison, len(args)))
	}
	if cursor := strings.TrimSpace(query.Before); cursor != "" {
		createdAt, err := s.lookupVectorStoreCursorCreatedAt(ctx, cursor)
		if err != nil {
			return domain.StoredVectorStorePage{}, err
		}
		args = append(args, createdAt, createdAt, cursor)
		where = append(where, fmt.Sprintf("(created_at %s $%d OR (created_at = $%d AND id %s $%d))", beforeComparison, len(args)-2, len(args)-1, beforeComparison, len(args)))
	}
	statement := `
		SELECT id, name, metadata_json, created_at, last_active_at, expires_after_anchor, expires_after_days, expires_at
		FROM vector_stores
	`
	if len(where) != 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, query.Limit+1)
	statement += fmt.Sprintf(" ORDER BY created_at %s, id %s LIMIT $%d", orderDir, orderDir, len(args))

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredVectorStorePage{}, fmt.Errorf("list postgres vector stores: %w", err)
	}
	defer rows.Close()

	stores := make([]domain.StoredVectorStore, 0, query.Limit+1)
	for rows.Next() {
		store, err := scanVectorStoreBase(rows)
		if err != nil {
			return domain.StoredVectorStorePage{}, err
		}
		hydrated, err := s.hydrateVectorStore(ctx, store)
		if err != nil {
			return domain.StoredVectorStorePage{}, err
		}
		stores = append(stores, hydrated)
	}
	if err := rows.Err(); err != nil {
		return domain.StoredVectorStorePage{}, fmt.Errorf("iterate postgres vector stores: %w", err)
	}
	hasMore := len(stores) > query.Limit
	if hasMore {
		stores = stores[:query.Limit]
	}
	return domain.StoredVectorStorePage{VectorStores: stores, HasMore: hasMore}, nil
}

func (s *Store) DeleteVectorStore(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM vector_stores WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete postgres vector store: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete postgres vector store rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveVectorStoreFile(ctx context.Context, file domain.StoredVectorStoreFile, content []string) error {
	attributesJSON, err := json.Marshal(file.Attributes)
	if err != nil {
		return fmt.Errorf("marshal vector store file attributes: %w", err)
	}
	chunkingJSON, err := json.Marshal(file.ChunkingStrategy)
	if err != nil {
		return fmt.Errorf("marshal vector store chunking strategy: %w", err)
	}
	var lastErrorJSON any
	if file.LastError != nil {
		encoded, err := json.Marshal(file.LastError)
		if err != nil {
			return fmt.Errorf("marshal vector store file last error: %w", err)
		}
		lastErrorJSON = string(encoded)
	}

	var embeddings [][]float32
	if s.retrieval.IndexBackend == retrieval.IndexBackendPGVector && file.Status == "completed" && len(content) != 0 {
		embeddings, _, err = s.embedTexts(ctx, content)
		if err != nil {
			return fmt.Errorf("embed vector store file chunks: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres vector store file tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vector_store_files (
			vector_store_id, file_id, created_at, status, usage_bytes, last_error_json, attributes_json, chunking_strategy_json
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb)
		ON CONFLICT(vector_store_id, file_id) DO UPDATE SET
			created_at = excluded.created_at,
			status = excluded.status,
			usage_bytes = excluded.usage_bytes,
			last_error_json = excluded.last_error_json,
			attributes_json = excluded.attributes_json,
			chunking_strategy_json = excluded.chunking_strategy_json
	`, file.VectorStoreID, file.ID, file.CreatedAt, file.Status, file.UsageBytes, lastErrorJSON, string(attributesJSON), string(chunkingJSON)); err != nil {
		return fmt.Errorf("upsert postgres vector store file: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM vector_store_chunks
		WHERE vector_store_id = $1 AND file_id = $2
	`, file.VectorStoreID, file.ID); err != nil {
		return fmt.Errorf("delete postgres vector store chunks: %w", err)
	}

	for i, chunk := range content {
		if len(embeddings) > i {
			embeddingLiteral := pgVectorLiteral(embeddings[i])
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO vector_store_chunks (
					vector_store_id, file_id, chunk_index, content, token_count,
					embedding, embedding_model, embedding_dimensions, embedding_created_at
				) VALUES ($1, $2, $3, $4, $5, $6::vector, $7, $8, $9)
			`, file.VectorStoreID, file.ID, i, chunk, countTerms(chunk), embeddingLiteral, s.embeddingModel, len(embeddings[i]), file.CreatedAt); err != nil {
				return fmt.Errorf("insert postgres vector store chunk embedding: %w", err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vector_store_chunks (vector_store_id, file_id, chunk_index, content, token_count)
			VALUES ($1, $2, $3, $4, $5)
		`, file.VectorStoreID, file.ID, i, chunk, countTerms(chunk)); err != nil {
			return fmt.Errorf("insert postgres vector store chunk: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE vector_stores
		SET last_active_at = $1,
		    expires_at = CASE
		      WHEN expires_after_days IS NULL OR expires_after_anchor IS NULL THEN expires_at
		      WHEN expires_after_anchor = 'last_active_at' THEN $1 + (expires_after_days * 86400)
		      ELSE expires_at
		    END
		WHERE id = $2
	`, file.CreatedAt, file.VectorStoreID); err != nil {
		return fmt.Errorf("touch postgres vector store activity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres vector store file tx: %w", err)
	}
	return nil
}

func (s *Store) GetVectorStoreFile(ctx context.Context, vectorStoreID, fileID string) (domain.StoredVectorStoreFile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT vector_store_id, file_id, created_at, status, usage_bytes, last_error_json, attributes_json, chunking_strategy_json
		FROM vector_store_files
		WHERE vector_store_id = $1 AND file_id = $2
	`, vectorStoreID, fileID)
	file, err := scanVectorStoreFile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredVectorStoreFile{}, ErrNotFound
		}
		return domain.StoredVectorStoreFile{}, err
	}
	return file, nil
}

func (s *Store) ListVectorStoreFiles(ctx context.Context, query domain.ListVectorStoreFilesQuery) (domain.StoredVectorStoreFilePage, error) {
	if _, err := s.getVectorStoreBase(ctx, query.VectorStoreID); err != nil {
		return domain.StoredVectorStoreFilePage{}, err
	}
	orderDir, afterComparison, beforeComparison := "DESC", "<", ">"
	if query.Order == domain.ListOrderAsc {
		orderDir, afterComparison, beforeComparison = "ASC", ">", "<"
	}
	where := []string{"vector_store_id = $1"}
	args := []any{query.VectorStoreID}
	if status := strings.TrimSpace(query.Filter); status != "" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if cursor := strings.TrimSpace(query.After); cursor != "" {
		createdAt, err := s.lookupVectorStoreFileCursorCreatedAt(ctx, query.VectorStoreID, cursor)
		if err != nil {
			return domain.StoredVectorStoreFilePage{}, err
		}
		args = append(args, createdAt, createdAt, cursor)
		where = append(where, fmt.Sprintf("(created_at %s $%d OR (created_at = $%d AND file_id %s $%d))", afterComparison, len(args)-2, len(args)-1, afterComparison, len(args)))
	}
	if cursor := strings.TrimSpace(query.Before); cursor != "" {
		createdAt, err := s.lookupVectorStoreFileCursorCreatedAt(ctx, query.VectorStoreID, cursor)
		if err != nil {
			return domain.StoredVectorStoreFilePage{}, err
		}
		args = append(args, createdAt, createdAt, cursor)
		where = append(where, fmt.Sprintf("(created_at %s $%d OR (created_at = $%d AND file_id %s $%d))", beforeComparison, len(args)-2, len(args)-1, beforeComparison, len(args)))
	}
	args = append(args, query.Limit+1)
	statement := fmt.Sprintf(`
		SELECT vector_store_id, file_id, created_at, status, usage_bytes, last_error_json, attributes_json, chunking_strategy_json
		FROM vector_store_files
		WHERE %s
		ORDER BY created_at %s, file_id %s
		LIMIT $%d
	`, strings.Join(where, " AND "), orderDir, orderDir, len(args))
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredVectorStoreFilePage{}, fmt.Errorf("list postgres vector store files: %w", err)
	}
	defer rows.Close()

	files := make([]domain.StoredVectorStoreFile, 0, query.Limit+1)
	for rows.Next() {
		file, err := scanVectorStoreFile(rows)
		if err != nil {
			return domain.StoredVectorStoreFilePage{}, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return domain.StoredVectorStoreFilePage{}, fmt.Errorf("iterate postgres vector store files: %w", err)
	}
	hasMore := len(files) > query.Limit
	if hasMore {
		files = files[:query.Limit]
	}
	return domain.StoredVectorStoreFilePage{Files: files, HasMore: hasMore}, nil
}

func (s *Store) DeleteVectorStoreFile(ctx context.Context, vectorStoreID, fileID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM vector_store_files
		WHERE vector_store_id = $1 AND file_id = $2
	`, vectorStoreID, fileID)
	if err != nil {
		return fmt.Errorf("delete postgres vector store file: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete postgres vector store file rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SearchVectorStore(ctx context.Context, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error) {
	var (
		results []domain.VectorStoreSearchResult
		err     error
	)
	if s.retrieval.IndexBackend == retrieval.IndexBackendPGVector {
		if query.HybridSearch != nil {
			results, err = s.searchVectorStoreHybridResults(ctx, query)
		} else {
			results, err = s.searchVectorStoreSemanticResults(ctx, query, query.ScoreThreshold)
			if err == nil && len(results) == 0 {
				results, err = s.searchVectorStoreLexicalResults(ctx, query, query.ScoreThreshold)
			}
		}
	} else {
		results, err = s.searchVectorStoreLexicalResults(ctx, query, query.ScoreThreshold)
	}
	if err != nil {
		return domain.VectorStoreSearchPage{}, err
	}
	if len(results) > query.MaxNumResults {
		results = results[:query.MaxNumResults]
	}
	if err := s.touchVectorStoreSearchActivity(ctx, query.VectorStoreID); err != nil {
		return domain.VectorStoreSearchPage{}, err
	}
	return domain.VectorStoreSearchPage{
		SearchQuery: query.RawSearchQuery,
		Results:     results,
		HasMore:     false,
		NextPage:    nil,
	}, nil
}

func (s *Store) RetrievalIndexCapabilities() retrieval.IndexCapabilities {
	if s == nil {
		return retrieval.IndexCapabilities{}
	}
	if s.retrieval.IndexBackend == retrieval.IndexBackendPGVector {
		return retrieval.IndexCapabilitiesForConfig(s.retrieval, s.embedder != nil)
	}
	return retrieval.IndexCapabilitiesForConfig(retrieval.Config{IndexBackend: retrieval.IndexBackendLexical}, false)
}

func (s *Store) searchVectorStoreSemanticResults(ctx context.Context, query domain.VectorStoreSearchQuery, scoreThreshold *float64) ([]domain.VectorStoreSearchResult, error) {
	if _, err := s.getVectorStoreBase(ctx, query.VectorStoreID); err != nil {
		return nil, err
	}
	queryEmbeddings, _, err := s.embedTexts(ctx, query.Queries)
	if err != nil {
		return nil, fmt.Errorf("embed search query: %w", err)
	}
	if len(queryEmbeddings) == 0 {
		return []domain.VectorStoreSearchResult{}, nil
	}

	bestByFile := map[string]aggregatedSearchResult{}
	limit := semanticCandidateLimit(query.MaxNumResults)
	for _, embedding := range queryEmbeddings {
		rows, err := s.db.QueryContext(ctx, `
			SELECT c.id, c.file_id, f.filename, v.attributes_json, c.content, (c.embedding <=> $2::vector)::float8 AS distance
			FROM vector_store_chunks c
			JOIN files f ON f.id = c.file_id
			JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
			WHERE c.vector_store_id = $1
			  AND v.status = 'completed'
			  AND c.embedding IS NOT NULL
			ORDER BY c.embedding <=> $2::vector, c.id ASC
			LIMIT $3
		`, query.VectorStoreID, pgVectorLiteral(embedding), limit)
		if err != nil {
			return nil, fmt.Errorf("query pgvector search: %w", err)
		}
		if err := scanSearchRows(rows, query, scoreThreshold, bestByFile, true); err != nil {
			return nil, err
		}
	}
	return aggregatedSearchResults(bestByFile), nil
}

func (s *Store) searchVectorStoreLexicalResults(ctx context.Context, query domain.VectorStoreSearchQuery, scoreThreshold *float64) ([]domain.VectorStoreSearchResult, error) {
	if _, err := s.getVectorStoreBase(ctx, query.VectorStoreID); err != nil {
		return nil, err
	}
	terms := uniqueQueryTerms(query.Queries)
	if len(terms) == 0 {
		return []domain.VectorStoreSearchResult{}, nil
	}
	where := []string{"c.vector_store_id = $1", "v.status = 'completed'"}
	args := []any{query.VectorStoreID}
	likeParts := make([]string, 0, len(terms)*2)
	for _, term := range terms {
		args = append(args, "%"+term+"%")
		likeParts = append(likeParts, fmt.Sprintf("lower(c.content) LIKE $%d", len(args)))
		args = append(args, "%"+term+"%")
		likeParts = append(likeParts, fmt.Sprintf("lower(f.filename) LIKE $%d", len(args)))
	}
	where = append(where, "("+strings.Join(likeParts, " OR ")+")")
	args = append(args, maxLexicalCandidateChunks)
	statement := fmt.Sprintf(`
		SELECT c.id, c.file_id, f.filename, v.attributes_json, c.content, NULL::float8 AS distance
		FROM vector_store_chunks c
		JOIN files f ON f.id = c.file_id
		JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
		WHERE %s
		ORDER BY c.id ASC
		LIMIT $%d
	`, strings.Join(where, " AND "), len(args))
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query postgres lexical vector search: %w", err)
	}
	bestByFile := map[string]aggregatedSearchResult{}
	if err := scanSearchRows(rows, query, scoreThreshold, bestByFile, false); err != nil {
		return nil, err
	}
	return aggregatedSearchResults(bestByFile), nil
}

func (s *Store) searchVectorStoreHybridResults(ctx context.Context, query domain.VectorStoreSearchQuery) ([]domain.VectorStoreSearchResult, error) {
	options := *query.HybridSearch
	if options.EmbeddingWeight == 0 && options.TextWeight == 0 {
		options.EmbeddingWeight = 1
		options.TextWeight = 1
	}
	semantic, err := s.searchVectorStoreSemanticResults(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	lexical, err := s.searchVectorStoreLexicalResults(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	total := options.EmbeddingWeight + options.TextWeight
	combined := map[string]domain.VectorStoreSearchResult{}
	for _, result := range semantic {
		result.Score = result.Score * options.EmbeddingWeight / total
		combined[result.FileID] = result
	}
	for _, result := range lexical {
		weighted := result.Score * options.TextWeight / total
		current, exists := combined[result.FileID]
		if !exists {
			result.Score = weighted
			combined[result.FileID] = result
			continue
		}
		current.Score += weighted
		current.Content = mergeSearchContent(current.Content, result.Content)
		combined[result.FileID] = current
	}
	results := make([]domain.VectorStoreSearchResult, 0, len(combined))
	for _, result := range combined {
		if query.ScoreThreshold != nil && result.Score < *query.ScoreThreshold {
			continue
		}
		results = append(results, result)
	}
	sortSearchResults(results)
	return results, nil
}

func scanSearchRows(rows *sql.Rows, query domain.VectorStoreSearchQuery, scoreThreshold *float64, bestByFile map[string]aggregatedSearchResult, semantic bool) error {
	defer rows.Close()
	for rows.Next() {
		var (
			chunkID        int64
			fileID         string
			filename       string
			attributesJSON []byte
			content        string
			distance       sql.NullFloat64
		)
		if err := rows.Scan(&chunkID, &fileID, &filename, &attributesJSON, &content, &distance); err != nil {
			return fmt.Errorf("scan postgres vector search row: %w", err)
		}
		_ = chunkID
		attributes := map[string]any{}
		if len(attributesJSON) != 0 {
			if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
				return fmt.Errorf("decode vector store file attributes: %w", err)
			}
		}
		if !domain.MatchVectorStoreSearchFilter(attributes, query.Filters) {
			continue
		}

		score := chunkScore(content, query.Queries)
		if semantic && distance.Valid {
			score = 1 - distance.Float64
			if score < 0 {
				score = 0
			}
			if score > 1 {
				score = 1
			}
		}
		if scoreThreshold != nil && score < *scoreThreshold {
			continue
		}
		if score <= 0 {
			continue
		}

		current, exists := bestByFile[fileID]
		if !exists {
			current = newAggregatedSearchResult(fileID, filename, attributes)
		}
		if semantic && distance.Valid {
			if !exists || current.bestDistance > distance.Float64 {
				current.bestDistance = distance.Float64
				current.Score = score
			}
		} else if !exists || current.Score < score {
			current.Score = score
		}
		current.addContent(content, score)
		bestByFile[fileID] = current
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate postgres vector search rows: %w", err)
	}
	return nil
}

func (s *Store) embedTexts(ctx context.Context, texts []string) ([][]float32, int, error) {
	if len(texts) == 0 {
		return nil, 0, nil
	}
	if s.embedder == nil {
		return nil, 0, fmt.Errorf("pgvector retrieval requires a configured embedder")
	}
	embeddings := make([][]float32, 0, len(texts))
	dims := 0
	for start := 0; start < len(texts); start += retrievalEmbedBatchSize {
		end := start + retrievalEmbedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := s.embedder.EmbedTexts(ctx, texts[start:end])
		if err != nil {
			return nil, 0, err
		}
		if len(batch) != end-start {
			return nil, 0, fmt.Errorf("embedder returned %d vectors for %d texts", len(batch), end-start)
		}
		for _, embedding := range batch {
			if len(embedding) == 0 {
				return nil, 0, fmt.Errorf("embedder returned empty vector")
			}
			if dims == 0 {
				dims = len(embedding)
			} else if len(embedding) != dims {
				return nil, 0, fmt.Errorf("embedder returned inconsistent vector dimensions: got %d, want %d", len(embedding), dims)
			}
			embeddings = append(embeddings, embedding)
		}
	}
	return embeddings, dims, nil
}

func (s *Store) touchVectorStoreSearchActivity(ctx context.Context, vectorStoreID string) error {
	now := domain.NowUTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE vector_stores
		SET last_active_at = $1,
		    expires_at = CASE
		      WHEN expires_after_days IS NULL OR expires_after_anchor IS NULL THEN expires_at
		      WHEN expires_after_anchor = 'last_active_at' THEN $1 + (expires_after_days * 86400)
		      ELSE expires_at
		    END
		WHERE id = $2
	`, now, vectorStoreID); err != nil {
		return fmt.Errorf("touch postgres vector store search activity: %w", err)
	}
	return nil
}

func (s *Store) getVectorStoreBase(ctx context.Context, id string) (domain.StoredVectorStore, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, metadata_json, created_at, last_active_at, expires_after_anchor, expires_after_days, expires_at
		FROM vector_stores
		WHERE id = $1
	`, id)
	store, err := scanVectorStoreBase(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StoredVectorStore{}, ErrNotFound
		}
		return domain.StoredVectorStore{}, err
	}
	return store, nil
}

func (s *Store) hydrateVectorStore(ctx context.Context, store domain.StoredVectorStore) (domain.StoredVectorStore, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(usage_bytes), 0),
			COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END), 0),
			COUNT(1)
		FROM vector_store_files
		WHERE vector_store_id = $1
	`, store.ID)
	if err := row.Scan(&store.UsageBytes, &store.FileCounts.InProgress, &store.FileCounts.Completed, &store.FileCounts.Failed, &store.FileCounts.Cancelled, &store.FileCounts.Total); err != nil {
		return domain.StoredVectorStore{}, fmt.Errorf("scan postgres vector store counts: %w", err)
	}
	now := domain.NowUTC().Unix()
	switch {
	case store.ExpiresAt != nil && *store.ExpiresAt <= now:
		store.Status = "expired"
	case store.FileCounts.InProgress > 0:
		store.Status = "in_progress"
	default:
		store.Status = "completed"
	}
	return store, nil
}

func (s *Store) lookupFileCursorCreatedAt(ctx context.Context, id, purpose string) (int64, error) {
	statement := `SELECT created_at FROM files WHERE id = $1`
	args := []any{id}
	if purpose != "" {
		statement += ` AND purpose = $2`
		args = append(args, purpose)
	}
	var createdAt int64
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup postgres file cursor: %w", err)
	}
	return createdAt, nil
}

func (s *Store) lookupVectorStoreCursorCreatedAt(ctx context.Context, id string) (int64, error) {
	var createdAt int64
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM vector_stores WHERE id = $1`, id).Scan(&createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup postgres vector store cursor: %w", err)
	}
	return createdAt, nil
}

func (s *Store) lookupVectorStoreFileCursorCreatedAt(ctx context.Context, vectorStoreID, fileID string) (int64, error) {
	var createdAt int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT created_at
		FROM vector_store_files
		WHERE vector_store_id = $1 AND file_id = $2
	`, vectorStoreID, fileID).Scan(&createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup postgres vector store file cursor: %w", err)
	}
	return createdAt, nil
}

func scanStoredFile(row scanner) (domain.StoredFile, error) {
	var (
		file          domain.StoredFile
		expiresAt     sql.NullInt64
		status        sql.NullString
		statusDetails sql.NullString
	)
	if err := row.Scan(&file.ID, &file.Purpose, &file.Filename, &file.Bytes, &file.CreatedAt, &expiresAt, &status, &statusDetails, &file.Content); err != nil {
		return domain.StoredFile{}, err
	}
	if expiresAt.Valid {
		file.ExpiresAt = &expiresAt.Int64
	}
	if status.Valid {
		file.Status = status.String
	}
	if statusDetails.Valid {
		file.StatusDetails = &statusDetails.String
	}
	return file, nil
}

func scanStoredFileMetadata(row scanner) (domain.StoredFile, error) {
	var (
		file          domain.StoredFile
		expiresAt     sql.NullInt64
		status        sql.NullString
		statusDetails sql.NullString
	)
	if err := row.Scan(&file.ID, &file.Purpose, &file.Filename, &file.Bytes, &file.CreatedAt, &expiresAt, &status, &statusDetails); err != nil {
		return domain.StoredFile{}, err
	}
	if expiresAt.Valid {
		file.ExpiresAt = &expiresAt.Int64
	}
	if status.Valid {
		file.Status = status.String
	}
	if statusDetails.Valid {
		file.StatusDetails = &statusDetails.String
	}
	return file, nil
}

func scanVectorStoreBase(row scanner) (domain.StoredVectorStore, error) {
	var (
		store              domain.StoredVectorStore
		metadataJSON       []byte
		expiresAfterAnchor sql.NullString
		expiresAfterDays   sql.NullInt64
		expiresAt          sql.NullInt64
	)
	if err := row.Scan(&store.ID, &store.Name, &metadataJSON, &store.CreatedAt, &store.LastActiveAt, &expiresAfterAnchor, &expiresAfterDays, &expiresAt); err != nil {
		return domain.StoredVectorStore{}, err
	}
	if len(metadataJSON) == 0 {
		store.Metadata = map[string]string{}
	} else if err := json.Unmarshal(metadataJSON, &store.Metadata); err != nil {
		return domain.StoredVectorStore{}, fmt.Errorf("decode vector store metadata: %w", err)
	}
	if store.Metadata == nil {
		store.Metadata = map[string]string{}
	}
	if expiresAfterAnchor.Valid && expiresAfterDays.Valid {
		store.ExpiresAfter = &domain.VectorStoreExpirationPolicy{Anchor: expiresAfterAnchor.String, Days: int(expiresAfterDays.Int64)}
	}
	if expiresAt.Valid {
		store.ExpiresAt = &expiresAt.Int64
	}
	return store, nil
}

func scanVectorStoreFile(row scanner) (domain.StoredVectorStoreFile, error) {
	var (
		file             domain.StoredVectorStoreFile
		lastErrorJSON    []byte
		attributesJSON   []byte
		chunkingStrategy []byte
	)
	if err := row.Scan(&file.VectorStoreID, &file.ID, &file.CreatedAt, &file.Status, &file.UsageBytes, &lastErrorJSON, &attributesJSON, &chunkingStrategy); err != nil {
		return domain.StoredVectorStoreFile{}, err
	}
	if len(lastErrorJSON) != 0 {
		var payload domain.VectorStoreFileError
		if err := json.Unmarshal(lastErrorJSON, &payload); err != nil {
			return domain.StoredVectorStoreFile{}, fmt.Errorf("decode vector store file last error: %w", err)
		}
		file.LastError = &payload
	}
	if len(attributesJSON) == 0 {
		file.Attributes = map[string]any{}
	} else if err := json.Unmarshal(attributesJSON, &file.Attributes); err != nil {
		return domain.StoredVectorStoreFile{}, fmt.Errorf("decode vector store file attributes: %w", err)
	}
	if file.Attributes == nil {
		file.Attributes = map[string]any{}
	}
	if err := json.Unmarshal(chunkingStrategy, &file.ChunkingStrategy); err != nil {
		return domain.StoredVectorStoreFile{}, fmt.Errorf("decode vector store chunking strategy: %w", err)
	}
	return file, nil
}

func buildVectorStoreFileContent(raw []byte, strategy domain.FileChunkingStrategy) ([]string, string, *domain.VectorStoreFileError) {
	if !utf8.Valid(raw) {
		return nil, "failed", &domain.VectorStoreFileError{Code: "unsupported_file", Message: "file content is not valid utf-8 text"}
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, "failed", &domain.VectorStoreFileError{Code: "invalid_file", Message: "file content is empty"}
	}
	chunks := chunkText(text, strategy)
	if len(chunks) == 0 {
		return nil, "failed", &domain.VectorStoreFileError{Code: "invalid_file", Message: "file content produced no searchable chunks"}
	}
	return chunks, "completed", nil
}

func chunkText(text string, strategy domain.FileChunkingStrategy) []string {
	static := strategy.Static
	if static == nil || static.MaxChunkSizeTokens <= 0 {
		defaultStrategy := domain.DefaultFileChunkingStrategy()
		static = defaultStrategy.Static
	}
	terms := tokenizeTerms(text)
	if len(terms) == 0 {
		return nil
	}
	step := static.MaxChunkSizeTokens - static.ChunkOverlapTokens
	if step <= 0 {
		step = static.MaxChunkSizeTokens
	}
	chunks := make([]string, 0, (len(terms)/step)+1)
	for start := 0; start < len(terms); start += step {
		end := start + static.MaxChunkSizeTokens
		if end > len(terms) {
			end = len(terms)
		}
		chunk := strings.TrimSpace(strings.Join(terms[start:end], " "))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(terms) {
			break
		}
	}
	return chunks
}

func chunkScore(content string, queries []string) float64 {
	contentTerms := tokenizeTerms(content)
	if len(contentTerms) == 0 {
		return 0
	}
	contentSet := make(map[string]int, len(contentTerms))
	for _, term := range contentTerms {
		contentSet[term]++
	}
	best := 0.0
	for _, query := range queries {
		queryTerms := tokenizeTerms(query)
		if len(queryTerms) == 0 {
			continue
		}
		unique := map[string]struct{}{}
		matches := 0
		totalOccurrences := 0
		for _, term := range queryTerms {
			if _, seen := unique[term]; seen {
				continue
			}
			unique[term] = struct{}{}
			if count := contentSet[term]; count > 0 {
				matches++
				totalOccurrences += count
			}
		}
		if len(unique) == 0 {
			continue
		}
		score := (float64(matches) / float64(len(unique))) * 0.8
		if matches > 0 {
			score += 0.2 * math.Min(1, float64(totalOccurrences)/float64(matches))
		}
		if score > best {
			best = score
		}
	}
	return math.Min(1, best)
}

func tokenizeTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func uniqueQueryTerms(queries []string) []string {
	seen := map[string]struct{}{}
	terms := make([]string, 0, len(queries))
	for _, query := range queries {
		for _, term := range tokenizeTerms(query) {
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			terms = append(terms, term)
		}
	}
	return terms
}

func countTerms(text string) int {
	return len(tokenizeTerms(text))
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func pgVectorLiteral(values []float32) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatFloat(float64(value), 'g', -1, 32))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func semanticCandidateLimit(maxResults int) int {
	limit := maxResults * maxContentItemsPerResult * 8
	if limit < minSemanticCandidateChunks {
		limit = minSemanticCandidateChunks
	}
	if limit > maxSemanticCandidateChunks {
		limit = maxSemanticCandidateChunks
	}
	return limit
}

func newAggregatedSearchResult(fileID, filename string, attributes map[string]any) aggregatedSearchResult {
	return aggregatedSearchResult{
		VectorStoreSearchResult: domain.VectorStoreSearchResult{
			FileID:     fileID,
			Filename:   filename,
			Attributes: attributes,
			Content:    []domain.VectorStoreSearchResultContent{},
		},
		bestDistance:    math.MaxFloat64,
		contentRanks:    []rankedSearchContent{},
		seenContentText: map[string]struct{}{},
	}
}

func (r *aggregatedSearchResult) addContent(text string, score float64) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if _, exists := r.seenContentText[trimmed]; exists {
		return
	}
	r.seenContentText[trimmed] = struct{}{}
	r.contentRanks = append(r.contentRanks, rankedSearchContent{text: trimmed, score: score, order: r.nextContentOrder})
	r.nextContentOrder++
	sort.Slice(r.contentRanks, func(i, j int) bool {
		if r.contentRanks[i].score == r.contentRanks[j].score {
			return r.contentRanks[i].order < r.contentRanks[j].order
		}
		return r.contentRanks[i].score > r.contentRanks[j].score
	})
	if len(r.contentRanks) > maxContentItemsPerResult {
		r.contentRanks = r.contentRanks[:maxContentItemsPerResult]
	}
}

func (r *aggregatedSearchResult) finalizeContent() {
	content := make([]domain.VectorStoreSearchResultContent, 0, len(r.contentRanks))
	for _, candidate := range r.contentRanks {
		content = append(content, domain.VectorStoreSearchResultContent{Type: "text", Text: candidate.text})
	}
	r.Content = content
}

func aggregatedSearchResults(bestByFile map[string]aggregatedSearchResult) []domain.VectorStoreSearchResult {
	results := make([]domain.VectorStoreSearchResult, 0, len(bestByFile))
	for _, result := range bestByFile {
		result.finalizeContent()
		results = append(results, result.VectorStoreSearchResult)
	}
	sortSearchResults(results)
	return results
}

func sortSearchResults(results []domain.VectorStoreSearchResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].Filename == results[j].Filename {
				return results[i].FileID < results[j].FileID
			}
			return results[i].Filename < results[j].Filename
		}
		return results[i].Score > results[j].Score
	})
}

func mergeSearchContent(a, b []domain.VectorStoreSearchResultContent) []domain.VectorStoreSearchResultContent {
	out := append([]domain.VectorStoreSearchResultContent(nil), a...)
	seen := map[string]struct{}{}
	for _, item := range out {
		seen[item.Text] = struct{}{}
	}
	for _, item := range b {
		if _, exists := seen[item.Text]; exists {
			continue
		}
		seen[item.Text] = struct{}{}
		out = append(out, item)
		if len(out) >= maxContentItemsPerResult {
			break
		}
	}
	return out
}

const postgresObjectStorageSchema = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS files (
	id TEXT PRIMARY KEY,
	purpose TEXT NOT NULL,
	filename TEXT NOT NULL,
	bytes BIGINT NOT NULL,
	created_at BIGINT NOT NULL,
	expires_at BIGINT NULL,
	status TEXT NULL,
	status_details TEXT NULL,
	content BYTEA NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pg_files_created_at ON files(created_at, id);
CREATE INDEX IF NOT EXISTS idx_pg_files_purpose_created_at ON files(purpose, created_at, id);

CREATE TABLE IF NOT EXISTS vector_stores (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at BIGINT NOT NULL,
	last_active_at BIGINT NOT NULL,
	expires_after_anchor TEXT NULL,
	expires_after_days BIGINT NULL,
	expires_at BIGINT NULL
);

CREATE INDEX IF NOT EXISTS idx_pg_vector_stores_created_at ON vector_stores(created_at, id);

CREATE TABLE IF NOT EXISTS vector_store_files (
	vector_store_id TEXT NOT NULL REFERENCES vector_stores(id) ON DELETE CASCADE,
	file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	created_at BIGINT NOT NULL,
	status TEXT NOT NULL,
	usage_bytes BIGINT NOT NULL,
	last_error_json JSONB NULL,
	attributes_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	chunking_strategy_json JSONB NOT NULL,
	PRIMARY KEY (vector_store_id, file_id)
);

CREATE INDEX IF NOT EXISTS idx_pg_vector_store_files_store_created_at
	ON vector_store_files(vector_store_id, created_at, file_id);
CREATE INDEX IF NOT EXISTS idx_pg_vector_store_files_store_status_created_at
	ON vector_store_files(vector_store_id, status, created_at, file_id);

CREATE TABLE IF NOT EXISTS vector_store_chunks (
	id BIGSERIAL PRIMARY KEY,
	vector_store_id TEXT NOT NULL REFERENCES vector_stores(id) ON DELETE CASCADE,
	file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	chunk_index INTEGER NOT NULL,
	content TEXT NOT NULL,
	token_count INTEGER NOT NULL,
	embedding vector NULL,
	embedding_model TEXT NULL,
	embedding_dimensions INTEGER NULL,
	embedding_created_at BIGINT NULL,
	UNIQUE(vector_store_id, file_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_pg_vector_store_chunks_store_file
	ON vector_store_chunks(vector_store_id, file_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_pg_vector_store_chunks_store_id
	ON vector_store_chunks(vector_store_id, id);
`
