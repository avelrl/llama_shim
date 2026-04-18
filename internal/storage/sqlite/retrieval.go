package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"llama_shim/internal/domain"
)

func (s *Store) SaveFile(ctx context.Context, file domain.StoredFile) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (
			id, purpose, filename, bytes, created_at, expires_at, status, status_details, content
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			purpose = excluded.purpose,
			filename = excluded.filename,
			bytes = excluded.bytes,
			created_at = excluded.created_at,
			expires_at = excluded.expires_at,
			status = excluded.status,
			status_details = excluded.status_details,
			content = excluded.content
	`,
		file.ID,
		file.Purpose,
		file.Filename,
		file.Bytes,
		file.CreatedAt,
		file.ExpiresAt,
		nullableString(file.Status),
		file.StatusDetails,
		file.Content,
	)
	if err != nil {
		return fmt.Errorf("insert file: %w", err)
	}
	return nil
}

func (s *Store) GetFile(ctx context.Context, id string) (domain.StoredFile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, purpose, filename, bytes, created_at, expires_at, status, status_details, content
		FROM files
		WHERE id = ?
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
	orderDir := "DESC"
	if query.Order == domain.ListOrderAsc {
		orderDir = "ASC"
	}

	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	purpose := strings.TrimSpace(query.Purpose)
	if purpose != "" {
		where = append(where, `purpose = ?`)
		args = append(args, purpose)
	}

	if cursor := strings.TrimSpace(query.After); cursor != "" {
		cursorCreatedAt, err := s.lookupListFilesCursorCreatedAt(ctx, cursor, purpose)
		if err != nil {
			return domain.StoredFilePage{}, err
		}
		comparison := "<"
		if query.Order == domain.ListOrderAsc {
			comparison = ">"
		}
		where = append(where, `(created_at `+comparison+` ? OR (created_at = ? AND id `+comparison+` ?))`)
		args = append(args, cursorCreatedAt, cursorCreatedAt, cursor)
	}

	statement := `
		SELECT id, purpose, filename, bytes, created_at, expires_at, status, status_details
		FROM files
	`
	if len(where) != 0 {
		statement += ` WHERE ` + strings.Join(where, ` AND `)
	}
	statement += ` ORDER BY created_at ` + orderDir + `, id ` + orderDir
	statement += ` LIMIT ?`
	args = append(args, query.Limit+1)

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredFilePage{}, fmt.Errorf("list files: %w", err)
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
		return domain.StoredFilePage{}, fmt.Errorf("iterate files: %w", err)
	}

	hasMore := len(files) > query.Limit
	if hasMore {
		files = files[:query.Limit]
	}

	return domain.StoredFilePage{Files: files, HasMore: hasMore}, nil
}

func (s *Store) DeleteFile(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete file tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	affectedStores, err := queryVectorStoreChunkIDsByFile(ctx, tx, id)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete file rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	for vectorStoreID, chunkIDs := range affectedStores {
		if err := s.retrieval.DeleteVectorStoreFile(ctx, tx, deleteVectorStoreFileParams{
			VectorStoreID:   vectorStoreID,
			FileID:          id,
			CreatedAt:       domain.NowUTC().Unix(),
			RemovedChunkIDs: chunkIDs,
		}); err != nil {
			return fmt.Errorf("refresh vector store after file delete: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete file tx: %w", err)
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			metadata_json = excluded.metadata_json,
			created_at = excluded.created_at,
			last_active_at = excluded.last_active_at,
			expires_after_anchor = excluded.expires_after_anchor,
			expires_after_days = excluded.expires_after_days,
			expires_at = excluded.expires_at
	`,
		store.ID,
		store.Name,
		string(metadataJSON),
		store.CreatedAt,
		store.LastActiveAt,
		anchor,
		days,
		store.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert vector store: %w", err)
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
	if query.Order == domain.ListOrderAsc {
		orderDir = "ASC"
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, metadata_json, created_at, last_active_at, expires_after_anchor, expires_after_days, expires_at
		FROM vector_stores
		ORDER BY created_at `+orderDir+`, id `+orderDir)
	if err != nil {
		return domain.StoredVectorStorePage{}, fmt.Errorf("list vector stores: %w", err)
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
		return domain.StoredVectorStorePage{}, fmt.Errorf("iterate vector stores: %w", err)
	}

	items, hasMore, err := paginateVectorStores(stores, query.After, query.Before, query.Limit)
	if err != nil {
		return domain.StoredVectorStorePage{}, err
	}

	return domain.StoredVectorStorePage{VectorStores: items, HasMore: hasMore}, nil
}

func (s *Store) DeleteVectorStore(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete vector store tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.retrieval.DeleteVectorStore(ctx, tx, id); err != nil {
		return fmt.Errorf("delete vector store retrieval index: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM vector_stores WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vector store: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete vector store rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete vector store tx: %w", err)
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vector store file tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vector_store_files (
			vector_store_id, file_id, created_at, status, usage_bytes, last_error_json, attributes_json, chunking_strategy_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(vector_store_id, file_id) DO UPDATE SET
			created_at = excluded.created_at,
			status = excluded.status,
			usage_bytes = excluded.usage_bytes,
			last_error_json = excluded.last_error_json,
			attributes_json = excluded.attributes_json,
			chunking_strategy_json = excluded.chunking_strategy_json
	`,
		file.VectorStoreID,
		file.ID,
		file.CreatedAt,
		file.Status,
		file.UsageBytes,
		lastErrorJSON,
		string(attributesJSON),
		string(chunkingJSON),
	); err != nil {
		return fmt.Errorf("upsert vector store file: %w", err)
	}

	existingChunkIDs, err := queryVectorStoreFileChunkIDs(ctx, tx, file.VectorStoreID, file.ID)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM vector_store_chunks
		WHERE vector_store_id = ? AND file_id = ?
	`, file.VectorStoreID, file.ID); err != nil {
		return fmt.Errorf("delete existing vector store chunks: %w", err)
	}

	for i, chunk := range content {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vector_store_chunks (vector_store_id, file_id, chunk_index, content, token_count)
			VALUES (?, ?, ?, ?, ?)
		`, file.VectorStoreID, file.ID, i, chunk, countTerms(chunk)); err != nil {
			return fmt.Errorf("insert vector store chunk: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE vector_stores
		SET last_active_at = ?, expires_at = CASE
			WHEN expires_after_days IS NULL OR expires_after_anchor IS NULL THEN expires_at
			WHEN expires_after_anchor = 'last_active_at' THEN ? + (expires_after_days * 86400)
			ELSE expires_at
		END
		WHERE id = ?
	`, file.CreatedAt, file.CreatedAt, file.VectorStoreID); err != nil {
		return fmt.Errorf("touch vector store activity: %w", err)
	}

	if err := s.retrieval.IndexVectorStoreFile(ctx, tx, indexVectorStoreFileParams{
		VectorStoreID:    file.VectorStoreID,
		FileID:           file.ID,
		CreatedAt:        file.CreatedAt,
		ReplacedChunkIDs: existingChunkIDs,
	}); err != nil {
		return fmt.Errorf("index vector store file: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vector store file tx: %w", err)
	}
	return nil
}

func (s *Store) GetVectorStoreFile(ctx context.Context, vectorStoreID, fileID string) (domain.StoredVectorStoreFile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT vector_store_id, file_id, created_at, status, usage_bytes, last_error_json, attributes_json, chunking_strategy_json
		FROM vector_store_files
		WHERE vector_store_id = ? AND file_id = ?
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

	orderDir := "DESC"
	if query.Order == domain.ListOrderAsc {
		orderDir = "ASC"
	}

	statement := `
		SELECT vector_store_id, file_id, created_at, status, usage_bytes, last_error_json, attributes_json, chunking_strategy_json
		FROM vector_store_files
		WHERE vector_store_id = ?
	`
	args := []any{query.VectorStoreID}
	if status := strings.TrimSpace(query.Filter); status != "" {
		statement += ` AND status = ?`
		args = append(args, status)
	}
	statement += ` ORDER BY created_at ` + orderDir + `, file_id ` + orderDir

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return domain.StoredVectorStoreFilePage{}, fmt.Errorf("list vector store files: %w", err)
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
		return domain.StoredVectorStoreFilePage{}, fmt.Errorf("iterate vector store files: %w", err)
	}

	items, hasMore, err := paginateVectorStoreFiles(files, query.After, query.Before, query.Limit)
	if err != nil {
		return domain.StoredVectorStoreFilePage{}, err
	}
	return domain.StoredVectorStoreFilePage{Files: items, HasMore: hasMore}, nil
}

func (s *Store) DeleteVectorStoreFile(ctx context.Context, vectorStoreID, fileID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete vector store file tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	chunkIDs, err := queryVectorStoreFileChunkIDs(ctx, tx, vectorStoreID, fileID)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `
		DELETE FROM vector_store_files
		WHERE vector_store_id = ? AND file_id = ?
	`, vectorStoreID, fileID)
	if err != nil {
		return fmt.Errorf("delete vector store file: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete vector store file rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	if err := s.retrieval.DeleteVectorStoreFile(ctx, tx, deleteVectorStoreFileParams{
		VectorStoreID:   vectorStoreID,
		FileID:          fileID,
		CreatedAt:       domain.NowUTC().Unix(),
		RemovedChunkIDs: chunkIDs,
	}); err != nil {
		return fmt.Errorf("refresh vector store after file delete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete vector store file tx: %w", err)
	}
	return nil
}

func (s *Store) SearchVectorStore(ctx context.Context, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error) {
	return s.retrieval.SearchVectorStore(ctx, s, query)
}

func (s *Store) searchVectorStoreLexical(ctx context.Context, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error) {
	results, err := s.searchVectorStoreLexicalResults(ctx, query, query.ScoreThreshold)
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

func (s *Store) searchVectorStoreLexicalResults(ctx context.Context, query domain.VectorStoreSearchQuery, scoreThreshold *float64) ([]domain.VectorStoreSearchResult, error) {
	store, err := s.GetVectorStore(ctx, query.VectorStoreID)
	if err != nil {
		return nil, err
	}
	_ = store

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.file_id, f.filename, v.attributes_json, c.content
		FROM vector_store_chunks c
		JOIN files f ON f.id = c.file_id
		JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
		WHERE c.vector_store_id = ? AND v.status = 'completed'
		ORDER BY c.id ASC
	`, query.VectorStoreID)
	if err != nil {
		return nil, fmt.Errorf("query vector store chunks: %w", err)
	}
	defer rows.Close()

	bestByFile := map[string]aggregatedSearchResult{}
	for rows.Next() {
		var (
			fileID         string
			filename       string
			attributesJSON string
			content        string
		)
		if err := rows.Scan(&fileID, &filename, &attributesJSON, &content); err != nil {
			return nil, fmt.Errorf("scan vector store chunk: %w", err)
		}

		attributes := map[string]any{}
		if strings.TrimSpace(attributesJSON) != "" {
			if err := json.Unmarshal([]byte(attributesJSON), &attributes); err != nil {
				return nil, fmt.Errorf("decode vector store file attributes: %w", err)
			}
		}
		if !domain.MatchVectorStoreSearchFilter(attributes, query.Filters) {
			continue
		}

		score := chunkScore(content, query.Queries)
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
		if !exists || current.Score < score {
			current.Score = score
		}
		current.addContent(content, score)
		bestByFile[fileID] = current
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector store chunks: %w", err)
	}

	results := make([]domain.VectorStoreSearchResult, 0, len(bestByFile))
	for _, result := range bestByFile {
		result.finalizeContent()
		results = append(results, result.VectorStoreSearchResult)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].Filename == results[j].Filename {
				return results[i].FileID < results[j].FileID
			}
			return results[i].Filename < results[j].Filename
		}
		return results[i].Score > results[j].Score
	})
	return results, nil
}

func (s *Store) touchVectorStoreSearchActivity(ctx context.Context, vectorStoreID string) error {
	now := domain.NowUTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE vector_stores
		SET last_active_at = ?, expires_at = CASE
			WHEN expires_after_days IS NULL OR expires_after_anchor IS NULL THEN expires_at
			WHEN expires_after_anchor = 'last_active_at' THEN ? + (expires_after_days * 86400)
			ELSE expires_at
		END
		WHERE id = ?
	`, now, now, vectorStoreID); err != nil {
		return fmt.Errorf("touch vector store search activity: %w", err)
	}
	return nil
}

func (s *Store) getVectorStoreBase(ctx context.Context, id string) (domain.StoredVectorStore, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, metadata_json, created_at, last_active_at, expires_after_anchor, expires_after_days, expires_at
		FROM vector_stores
		WHERE id = ?
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
		WHERE vector_store_id = ?
	`, store.ID)

	if err := row.Scan(
		&store.UsageBytes,
		&store.FileCounts.InProgress,
		&store.FileCounts.Completed,
		&store.FileCounts.Failed,
		&store.FileCounts.Cancelled,
		&store.FileCounts.Total,
	); err != nil {
		return domain.StoredVectorStore{}, fmt.Errorf("scan vector store counts: %w", err)
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

func scanStoredFile(row interface{ Scan(...any) error }) (domain.StoredFile, error) {
	var (
		file          domain.StoredFile
		expiresAt     sql.NullInt64
		status        sql.NullString
		statusDetails sql.NullString
	)
	if err := row.Scan(
		&file.ID,
		&file.Purpose,
		&file.Filename,
		&file.Bytes,
		&file.CreatedAt,
		&expiresAt,
		&status,
		&statusDetails,
		&file.Content,
	); err != nil {
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

func scanStoredFileMetadata(row interface{ Scan(...any) error }) (domain.StoredFile, error) {
	var (
		file          domain.StoredFile
		expiresAt     sql.NullInt64
		status        sql.NullString
		statusDetails sql.NullString
	)
	if err := row.Scan(
		&file.ID,
		&file.Purpose,
		&file.Filename,
		&file.Bytes,
		&file.CreatedAt,
		&expiresAt,
		&status,
		&statusDetails,
	); err != nil {
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

func (s *Store) lookupListFilesCursorCreatedAt(ctx context.Context, id string, purpose string) (int64, error) {
	statement := `SELECT created_at FROM files WHERE id = ?`
	args := []any{id}
	if purpose != "" {
		statement += ` AND purpose = ?`
		args = append(args, purpose)
	}

	var createdAt int64
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup file cursor: %w", err)
	}
	return createdAt, nil
}

func scanVectorStoreBase(row interface{ Scan(...any) error }) (domain.StoredVectorStore, error) {
	var (
		store              domain.StoredVectorStore
		metadataJSON       string
		expiresAfterAnchor sql.NullString
		expiresAfterDays   sql.NullInt64
		expiresAt          sql.NullInt64
	)
	if err := row.Scan(
		&store.ID,
		&store.Name,
		&metadataJSON,
		&store.CreatedAt,
		&store.LastActiveAt,
		&expiresAfterAnchor,
		&expiresAfterDays,
		&expiresAt,
	); err != nil {
		return domain.StoredVectorStore{}, err
	}
	if strings.TrimSpace(metadataJSON) == "" {
		store.Metadata = map[string]string{}
	} else if err := json.Unmarshal([]byte(metadataJSON), &store.Metadata); err != nil {
		return domain.StoredVectorStore{}, fmt.Errorf("decode vector store metadata: %w", err)
	}
	if store.Metadata == nil {
		store.Metadata = map[string]string{}
	}
	if expiresAfterAnchor.Valid && expiresAfterDays.Valid {
		store.ExpiresAfter = &domain.VectorStoreExpirationPolicy{
			Anchor: expiresAfterAnchor.String,
			Days:   int(expiresAfterDays.Int64),
		}
	}
	if expiresAt.Valid {
		store.ExpiresAt = &expiresAt.Int64
	}
	return store, nil
}

func scanVectorStoreFile(row interface{ Scan(...any) error }) (domain.StoredVectorStoreFile, error) {
	var (
		file             domain.StoredVectorStoreFile
		lastErrorJSON    sql.NullString
		attributesJSON   string
		chunkingStrategy string
	)
	if err := row.Scan(
		&file.VectorStoreID,
		&file.ID,
		&file.CreatedAt,
		&file.Status,
		&file.UsageBytes,
		&lastErrorJSON,
		&attributesJSON,
		&chunkingStrategy,
	); err != nil {
		return domain.StoredVectorStoreFile{}, err
	}
	if lastErrorJSON.Valid {
		var payload domain.VectorStoreFileError
		if err := json.Unmarshal([]byte(lastErrorJSON.String), &payload); err != nil {
			return domain.StoredVectorStoreFile{}, fmt.Errorf("decode vector store file last error: %w", err)
		}
		file.LastError = &payload
	}
	if strings.TrimSpace(attributesJSON) == "" {
		file.Attributes = map[string]any{}
	} else if err := json.Unmarshal([]byte(attributesJSON), &file.Attributes); err != nil {
		return domain.StoredVectorStoreFile{}, fmt.Errorf("decode vector store file attributes: %w", err)
	}
	if file.Attributes == nil {
		file.Attributes = map[string]any{}
	}
	if err := json.Unmarshal([]byte(chunkingStrategy), &file.ChunkingStrategy); err != nil {
		return domain.StoredVectorStoreFile{}, fmt.Errorf("decode vector store chunking strategy: %w", err)
	}
	return file, nil
}

func paginateVectorStores(stores []domain.StoredVectorStore, after, before string, limit int) ([]domain.StoredVectorStore, bool, error) {
	start := 0
	end := len(stores)
	if cursor := strings.TrimSpace(after); cursor != "" {
		start = -1
		for i, store := range stores {
			if store.ID == cursor {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, false, ErrNotFound
		}
	}
	if cursor := strings.TrimSpace(before); cursor != "" {
		end = -1
		for i, store := range stores {
			if store.ID == cursor {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, false, ErrNotFound
		}
	}
	if start > end {
		start = end
	}
	page := stores[start:end]
	hasMore := len(page) > limit
	if len(page) > limit {
		page = page[:limit]
	}
	return page, hasMore, nil
}

func paginateVectorStoreFiles(files []domain.StoredVectorStoreFile, after, before string, limit int) ([]domain.StoredVectorStoreFile, bool, error) {
	start := 0
	end := len(files)
	if cursor := strings.TrimSpace(after); cursor != "" {
		start = -1
		for i, file := range files {
			if file.ID == cursor {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, false, ErrNotFound
		}
	}
	if cursor := strings.TrimSpace(before); cursor != "" {
		end = -1
		for i, file := range files {
			if file.ID == cursor {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, false, ErrNotFound
		}
	}
	if start > end {
		start = end
	}
	page := files[start:end]
	hasMore := len(page) > limit
	if len(page) > limit {
		page = page[:limit]
	}
	return page, hasMore, nil
}

func queryVectorStoreFileChunkIDs(ctx context.Context, tx *sql.Tx, vectorStoreID, fileID string) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM vector_store_chunks
		WHERE vector_store_id = ? AND file_id = ?
		ORDER BY id ASC
	`, vectorStoreID, fileID)
	if err != nil {
		return nil, fmt.Errorf("query vector store chunk ids: %w", err)
	}
	defer rows.Close()

	chunkIDs := make([]int64, 0, 16)
	for rows.Next() {
		var chunkID int64
		if err := rows.Scan(&chunkID); err != nil {
			return nil, fmt.Errorf("scan vector store chunk id: %w", err)
		}
		chunkIDs = append(chunkIDs, chunkID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector store chunk ids: %w", err)
	}
	return chunkIDs, nil
}

func queryVectorStoreChunkIDsByFile(ctx context.Context, tx *sql.Tx, fileID string) (map[string][]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT vector_store_id, id
		FROM vector_store_chunks
		WHERE file_id = ?
		ORDER BY vector_store_id ASC, id ASC
	`, fileID)
	if err != nil {
		return nil, fmt.Errorf("query vector store chunk ids by file: %w", err)
	}
	defer rows.Close()

	chunkIDsByStore := make(map[string][]int64)
	for rows.Next() {
		var (
			vectorStoreID string
			chunkID       int64
		)
		if err := rows.Scan(&vectorStoreID, &chunkID); err != nil {
			return nil, fmt.Errorf("scan vector store chunk id by file: %w", err)
		}
		chunkIDsByStore[vectorStoreID] = append(chunkIDsByStore[vectorStoreID], chunkID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector store chunk ids by file: %w", err)
	}
	return chunkIDsByStore, nil
}

func buildVectorStoreFileContent(raw []byte, strategy domain.FileChunkingStrategy) ([]string, string, *domain.VectorStoreFileError) {
	if !utf8.Valid(raw) {
		return nil, "failed", &domain.VectorStoreFileError{
			Code:    "unsupported_file",
			Message: "file content is not valid utf-8 text",
		}
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, "failed", &domain.VectorStoreFileError{
			Code:    "invalid_file",
			Message: "file content is empty",
		}
	}
	chunks := chunkText(text, strategy)
	if len(chunks) == 0 {
		return nil, "failed", &domain.VectorStoreFileError{
			Code:    "invalid_file",
			Message: "file content produced no searchable chunks",
		}
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
			score += 0.2 * minFloat(1, float64(totalOccurrences)/float64(matches))
		}
		if score > best {
			best = score
		}
	}
	if best > 1 {
		return 1
	}
	return best
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

func countTerms(text string) int {
	return len(tokenizeTerms(text))
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
