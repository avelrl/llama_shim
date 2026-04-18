package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
)

type OpenOptions struct {
	Retrieval retrieval.Config
	Embedder  retrieval.Embedder
}

type indexVectorStoreFileParams struct {
	VectorStoreID string
	FileID        string
	CreatedAt     int64
}

type retrievalBackend interface {
	Name() string
	IndexVectorStoreFile(ctx context.Context, tx *sql.Tx, params indexVectorStoreFileParams) error
	RefreshVectorStore(ctx context.Context, tx *sql.Tx, vectorStoreID string, createdAt int64) error
	DeleteVectorStore(ctx context.Context, tx *sql.Tx, vectorStoreID string) error
	SearchVectorStore(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error)
}

type lexicalRetrievalBackend struct{}
type sqliteVecRetrievalBackend struct {
	embedder retrieval.Embedder
	model    string
}

const hybridRRFK = 60.0
const retrievalEmbedBatchSize = 128
const retrievalMaxContentItemsPerResult = 3

type vectorStoreChunk struct {
	ID      int64
	Content string
}

type sqlExecContext interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type localRerankProfile struct {
	baseWeight     float64
	keywordWeight  float64
	phraseWeight   float64
	filenameWeight float64
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

var (
	latestLocalRerankProfile = localRerankProfile{
		baseWeight:     0.75,
		keywordWeight:  0.15,
		phraseWeight:   0.07,
		filenameWeight: 0.03,
	}
	legacyLocalRerankProfile = localRerankProfile{
		baseWeight:     0.82,
		keywordWeight:  0.12,
		phraseWeight:   0.04,
		filenameWeight: 0.02,
	}
)

func (lexicalRetrievalBackend) Name() string {
	return retrieval.IndexBackendLexical
}

func (lexicalRetrievalBackend) IndexVectorStoreFile(context.Context, *sql.Tx, indexVectorStoreFileParams) error {
	return nil
}

func (lexicalRetrievalBackend) RefreshVectorStore(context.Context, *sql.Tx, string, int64) error {
	return nil
}

func (lexicalRetrievalBackend) DeleteVectorStore(context.Context, *sql.Tx, string) error {
	return nil
}

func (lexicalRetrievalBackend) SearchVectorStore(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error) {
	return store.searchVectorStoreLexical(ctx, query)
}

func normalizeOpenOptions(options OpenOptions) (OpenOptions, error) {
	cfg, err := retrieval.NormalizeConfig(options.Retrieval)
	if err != nil {
		return OpenOptions{}, err
	}
	options.Retrieval = cfg
	return options, nil
}

func newRetrievalBackend(cfg retrieval.Config) (retrievalBackend, error) {
	return newRetrievalBackendWithOptions(cfg, nil)
}

func newRetrievalBackendWithOptions(cfg retrieval.Config, embedder retrieval.Embedder) (retrievalBackend, error) {
	switch cfg.IndexBackend {
	case retrieval.IndexBackendLexical:
		return lexicalRetrievalBackend{}, nil
	case retrieval.IndexBackendSQLiteVec:
		if embedder == nil {
			var err error
			embedder, err = retrieval.NewEmbedder(cfg.Embedder)
			if err != nil {
				return nil, err
			}
		}
		if embedder == nil {
			return nil, fmt.Errorf("retrieval index backend %q requires a configured embedder backend", cfg.IndexBackend)
		}
		return sqliteVecRetrievalBackend{
			embedder: embedder,
			model:    cfg.Embedder.Model,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported retrieval index backend %q", cfg.IndexBackend)
	}
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
	r.contentRanks = append(r.contentRanks, rankedSearchContent{
		text:  trimmed,
		score: score,
		order: r.nextContentOrder,
	})
	r.nextContentOrder++
	sort.Slice(r.contentRanks, func(i, j int) bool {
		if r.contentRanks[i].score == r.contentRanks[j].score {
			return r.contentRanks[i].order < r.contentRanks[j].order
		}
		return r.contentRanks[i].score > r.contentRanks[j].score
	})
	if len(r.contentRanks) > retrievalMaxContentItemsPerResult {
		r.contentRanks = r.contentRanks[:retrievalMaxContentItemsPerResult]
	}
}

func (r *aggregatedSearchResult) finalizeContent() {
	if len(r.contentRanks) == 0 {
		r.Content = []domain.VectorStoreSearchResultContent{}
		return
	}
	content := make([]domain.VectorStoreSearchResultContent, 0, len(r.contentRanks))
	for _, candidate := range r.contentRanks {
		content = append(content, domain.VectorStoreSearchResultContent{
			Type: "text",
			Text: candidate.text,
		})
	}
	r.Content = content
}

func (sqliteVecRetrievalBackend) Name() string {
	return retrieval.IndexBackendSQLiteVec
}

func (b sqliteVecRetrievalBackend) IndexVectorStoreFile(ctx context.Context, tx *sql.Tx, params indexVectorStoreFileParams) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, content
		FROM vector_store_chunks
		WHERE vector_store_id = ? AND file_id = ?
		ORDER BY chunk_index ASC
	`, params.VectorStoreID, params.FileID)
	if err != nil {
		return fmt.Errorf("query vector store chunks for embeddings: %w", err)
	}
	defer rows.Close()

	chunks := make([]vectorStoreChunk, 0, 8)
	for rows.Next() {
		var item vectorStoreChunk
		if err := rows.Scan(&item.ID, &item.Content); err != nil {
			return fmt.Errorf("scan vector store chunk for embeddings: %w", err)
		}
		chunks = append(chunks, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate vector store chunks for embeddings: %w", err)
	}
	if len(chunks) == 0 {
		return b.RefreshVectorStore(ctx, tx, params.VectorStoreID, params.CreatedAt)
	}

	if _, err := b.upsertChunkEmbeddings(ctx, tx, chunks, params.CreatedAt); err != nil {
		return err
	}
	return b.RefreshVectorStore(ctx, tx, params.VectorStoreID, params.CreatedAt)
}

func (b sqliteVecRetrievalBackend) RefreshVectorStore(ctx context.Context, tx *sql.Tx, vectorStoreID string, createdAt int64) error {
	return b.refreshVectorStoreVec0Index(ctx, tx, vectorStoreID, b.model, 0, createdAt)
}

func (b sqliteVecRetrievalBackend) DeleteVectorStore(ctx context.Context, tx *sql.Tx, vectorStoreID string) error {
	tableName := semanticIndexTableName(vectorStoreID)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, tableName)); err != nil {
		return fmt.Errorf("drop semantic vec0 index table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM vector_store_semantic_index_meta
		WHERE vector_store_id = ?
	`, vectorStoreID); err != nil {
		return fmt.Errorf("delete semantic index metadata: %w", err)
	}
	return nil
}

func (b sqliteVecRetrievalBackend) SearchVectorStore(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error) {
	rerankProfile := localRerankProfileForRanker(query.Ranker)
	scoreThreshold := query.ScoreThreshold
	if rerankProfile != nil {
		scoreThreshold = nil
	}

	var (
		results []domain.VectorStoreSearchResult
		err     error
	)
	if query.HybridSearch != nil {
		results, err = b.searchVectorStoreHybridResults(ctx, store, query, scoreThreshold)
	} else {
		results, err = b.searchVectorStoreSemanticResults(ctx, store, query, scoreThreshold)
	}
	if err != nil {
		return domain.VectorStoreSearchPage{}, err
	}
	if rerankProfile != nil {
		results = rerankVectorStoreResults(results, query.Queries, *rerankProfile, query.ScoreThreshold)
	}
	if len(results) > query.MaxNumResults {
		results = results[:query.MaxNumResults]
	}

	if err := store.touchVectorStoreSearchActivity(ctx, query.VectorStoreID); err != nil {
		return domain.VectorStoreSearchPage{}, err
	}

	return domain.VectorStoreSearchPage{
		SearchQuery: query.RawSearchQuery,
		Results:     results,
		HasMore:     false,
		NextPage:    nil,
	}, nil
}

func (b sqliteVecRetrievalBackend) searchVectorStoreSemanticResults(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery, scoreThreshold *float64) ([]domain.VectorStoreSearchResult, error) {
	if _, err := store.GetVectorStore(ctx, query.VectorStoreID); err != nil {
		return nil, err
	}
	queryEmbeddings, queryDims, err := b.embedTexts(ctx, query.Queries)
	if err != nil {
		return nil, fmt.Errorf("embed search query: %w", err)
	}
	if len(queryEmbeddings) == 0 {
		return []domain.VectorStoreSearchResult{}, nil
	}
	if err := b.ensureCurrentVectorStoreEmbeddings(ctx, store, query.VectorStoreID, queryDims); err != nil {
		return nil, err
	}
	chunkCount, err := b.ensureCurrentVectorStoreVec0Index(ctx, store, query.VectorStoreID, queryDims)
	if err != nil {
		return nil, err
	}
	if chunkCount == 0 {
		return []domain.VectorStoreSearchResult{}, nil
	}

	bestByFile := map[string]aggregatedSearchResult{}
	for _, queryEmbedding := range queryEmbeddings {
		queryBlob, _, err := encodeFloat32Vector(queryEmbedding)
		if err != nil {
			return nil, fmt.Errorf("encode query embedding: %w", err)
		}

		rows, err := store.db.QueryContext(ctx, `
			SELECT
				t.chunk_id,
				c.file_id,
				f.filename,
				v.attributes_json,
				c.content,
				t.distance
			FROM "`+semanticIndexTableName(query.VectorStoreID)+`" t
			JOIN vector_store_chunks c ON c.id = t.chunk_id
			JOIN files f ON f.id = c.file_id
			JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
			WHERE t.embedding MATCH vec_f32(?)
			  AND k = ?
			  AND c.vector_store_id = ?
			  AND v.status = 'completed'
			ORDER BY t.distance ASC, c.id ASC
		`, queryBlob, chunkCount, query.VectorStoreID)
		if err != nil {
			return nil, fmt.Errorf("query semantic vector store search: %w", err)
		}

		for rows.Next() {
			var (
				chunkID        int64
				fileID         string
				filename       string
				attributesJSON string
				content        string
				distance       float64
			)
			if err := rows.Scan(&chunkID, &fileID, &filename, &attributesJSON, &content, &distance); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan semantic search row: %w", err)
			}
			_ = chunkID

			attributes := map[string]any{}
			if strings.TrimSpace(attributesJSON) != "" {
				if err := json.Unmarshal([]byte(attributesJSON), &attributes); err != nil {
					rows.Close()
					return nil, fmt.Errorf("decode vector store file attributes: %w", err)
				}
			}
			if !domain.MatchVectorStoreSearchFilter(attributes, query.Filters) {
				continue
			}

			score := 1 - distance
			if score < 0 {
				score = 0
			}
			if score > 1 {
				score = 1
			}
			if scoreThreshold != nil && score < *scoreThreshold {
				continue
			}

			current, exists := bestByFile[fileID]
			if !exists {
				current = newAggregatedSearchResult(fileID, filename, attributes)
			}
			if !exists || current.bestDistance > distance {
				current.bestDistance = distance
				current.Score = score
			}
			current.addContent(content, score)
			bestByFile[fileID] = current
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate semantic search rows: %w", err)
		}
		rows.Close()
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

func (b sqliteVecRetrievalBackend) embedTexts(ctx context.Context, texts []string) ([][]float32, int, error) {
	if len(texts) == 0 {
		return nil, 0, nil
	}

	embeddings := make([][]float32, 0, len(texts))
	dims := 0
	for start := 0; start < len(texts); start += retrievalEmbedBatchSize {
		end := start + retrievalEmbedBatchSize
		if end > len(texts) {
			end = len(texts)
		}

		batch, err := b.embedder.EmbedTexts(ctx, texts[start:end])
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

func (b sqliteVecRetrievalBackend) upsertChunkEmbeddings(ctx context.Context, exec sqlExecContext, chunks []vectorStoreChunk, createdAt int64) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}

	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Content)
	}

	embeddings, dims, err := b.embedTexts(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed vector store chunks: %w", err)
	}
	for i, embedding := range embeddings {
		blob, encodedDims, err := encodeFloat32Vector(embedding)
		if err != nil {
			return 0, fmt.Errorf("encode chunk embedding %d: %w", i, err)
		}
		if encodedDims != dims {
			return 0, fmt.Errorf("encoded vector dimensions %d do not match embedder dimensions %d", encodedDims, dims)
		}
		if _, err := exec.ExecContext(ctx, `
			INSERT INTO vector_store_chunk_embeddings (
				chunk_id, embedding, embedding_model, embedding_dimensions, created_at
			) VALUES (?, vec_f32(?), ?, ?, ?)
			ON CONFLICT(chunk_id) DO UPDATE SET
				embedding = excluded.embedding,
				embedding_model = excluded.embedding_model,
				embedding_dimensions = excluded.embedding_dimensions,
				created_at = excluded.created_at
		`, chunks[i].ID, blob, b.model, dims, createdAt); err != nil {
			return 0, fmt.Errorf("upsert chunk embedding: %w", err)
		}
	}
	return dims, nil
}

func (b sqliteVecRetrievalBackend) ensureCurrentVectorStoreEmbeddings(ctx context.Context, store *Store, vectorStoreID string, expectedDims int) error {
	rows, err := store.db.QueryContext(ctx, `
		SELECT c.id, c.content, e.embedding_model, e.embedding_dimensions
		FROM vector_store_chunks c
		JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
		LEFT JOIN vector_store_chunk_embeddings e ON e.chunk_id = c.id
		WHERE c.vector_store_id = ? AND v.status = 'completed'
		ORDER BY c.chunk_index ASC, c.id ASC
	`, vectorStoreID)
	if err != nil {
		return fmt.Errorf("query current vector store embeddings: %w", err)
	}
	defer rows.Close()

	stale := make([]vectorStoreChunk, 0, 8)
	for rows.Next() {
		var (
			chunk          vectorStoreChunk
			embeddingModel sql.NullString
			embeddingDims  sql.NullInt64
		)
		if err := rows.Scan(&chunk.ID, &chunk.Content, &embeddingModel, &embeddingDims); err != nil {
			return fmt.Errorf("scan current vector store embedding row: %w", err)
		}
		if !embeddingModel.Valid || !embeddingDims.Valid || embeddingModel.String != b.model || int(embeddingDims.Int64) != expectedDims {
			stale = append(stale, chunk)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate current vector store embeddings: %w", err)
	}
	if len(stale) == 0 {
		return nil
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vector store embedding refresh tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	actualDims, err := b.upsertChunkEmbeddings(ctx, tx, stale, domain.NowUTC().Unix())
	if err != nil {
		return err
	}
	if actualDims != expectedDims {
		return fmt.Errorf("reindexed vector store chunks with dimensions %d, expected %d", actualDims, expectedDims)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vector store embedding refresh tx: %w", err)
	}
	return nil
}

func (b sqliteVecRetrievalBackend) ensureCurrentVectorStoreVec0Index(ctx context.Context, store *Store, vectorStoreID string, expectedDims int) (int, error) {
	tableName := semanticIndexTableName(vectorStoreID)
	row := store.db.QueryRowContext(ctx, `
		SELECT embedding_model, embedding_dimensions, chunk_count
		FROM vector_store_semantic_index_meta
		WHERE vector_store_id = ?
	`, vectorStoreID)

	var (
		model      string
		dims       int
		chunkCount int
	)
	err := row.Scan(&model, &dims, &chunkCount)
	switch {
	case err == nil && model == b.model && dims == expectedDims:
		exists, err := store.sqliteTableExists(ctx, tableName)
		if err != nil {
			return 0, fmt.Errorf("check semantic vec0 table existence: %w", err)
		}
		if exists {
			return chunkCount, nil
		}
	case err != nil && err != sql.ErrNoRows:
		return 0, fmt.Errorf("query semantic index metadata: %w", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin semantic vec0 refresh tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	chunkCount, err = b.refreshVectorStoreVec0IndexCount(ctx, tx, vectorStoreID, b.model, expectedDims, domain.NowUTC().Unix())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit semantic vec0 refresh tx: %w", err)
	}
	return chunkCount, nil
}

func (b sqliteVecRetrievalBackend) refreshVectorStoreVec0Index(ctx context.Context, tx *sql.Tx, vectorStoreID, model string, expectedDims int, createdAt int64) error {
	chunkCount, err := b.refreshVectorStoreVec0IndexCount(ctx, tx, vectorStoreID, model, expectedDims, createdAt)
	if err != nil {
		return err
	}
	_ = chunkCount
	return nil
}

func (b sqliteVecRetrievalBackend) refreshVectorStoreVec0IndexCount(ctx context.Context, tx *sql.Tx, vectorStoreID, model string, expectedDims int, createdAt int64) (int, error) {
	tableName := semanticIndexTableName(vectorStoreID)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, tableName)); err != nil {
		return 0, fmt.Errorf("drop semantic vec0 table: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, CAST(e.embedding AS BLOB), e.embedding_dimensions
		FROM vector_store_chunk_embeddings e
		JOIN vector_store_chunks c ON c.id = e.chunk_id
		JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
		WHERE c.vector_store_id = ? AND v.status = 'completed' AND e.embedding_model = ?
		ORDER BY c.chunk_index ASC, c.id ASC
	`, vectorStoreID, model)
	if err != nil {
		return 0, fmt.Errorf("query semantic vec0 source rows: %w", err)
	}
	defer rows.Close()

	dims := expectedDims
	chunkCount := 0
	tableCreated := false
	for rows.Next() {
		var (
			chunkID int64
			blob    []byte
			rowDims int
		)
		if err := rows.Scan(&chunkID, &blob, &rowDims); err != nil {
			return 0, fmt.Errorf("scan semantic vec0 source row: %w", err)
		}
		if dims == 0 {
			dims = rowDims
		}
		if rowDims != dims {
			return 0, fmt.Errorf("semantic vec0 source dimensions mismatch: got %d, want %d", rowDims, dims)
		}
		if !tableCreated {
			if expectedDims != 0 && dims != expectedDims {
				return 0, fmt.Errorf("semantic vec0 rebuild dimensions %d do not match expected %d", dims, expectedDims)
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
				CREATE VIRTUAL TABLE "%s" USING vec0(
					chunk_id INTEGER PRIMARY KEY,
					embedding float[%d] distance_metric=cosine
				)
			`, tableName, dims)); err != nil {
				return 0, fmt.Errorf("create semantic vec0 table: %w", err)
			}
			tableCreated = true
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO "%s"(chunk_id, embedding) VALUES (?, vec_f32(?))
		`, tableName), chunkID, blob); err != nil {
			return 0, fmt.Errorf("insert semantic vec0 row: %w", err)
		}
		chunkCount++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate semantic vec0 source rows: %w", err)
	}

	if chunkCount == 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM vector_store_semantic_index_meta
			WHERE vector_store_id = ?
		`, vectorStoreID); err != nil {
			return 0, fmt.Errorf("delete empty semantic index metadata: %w", err)
		}
		return 0, nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vector_store_semantic_index_meta (
			vector_store_id, embedding_model, embedding_dimensions, chunk_count, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(vector_store_id) DO UPDATE SET
			embedding_model = excluded.embedding_model,
			embedding_dimensions = excluded.embedding_dimensions,
			chunk_count = excluded.chunk_count,
			updated_at = excluded.updated_at
	`, vectorStoreID, model, dims, chunkCount, createdAt); err != nil {
		return 0, fmt.Errorf("upsert semantic index metadata: %w", err)
	}

	return chunkCount, nil
}

func semanticIndexTableName(vectorStoreID string) string {
	sum := sha256.Sum256([]byte(vectorStoreID))
	return "vector_store_semantic_" + hex.EncodeToString(sum[:12])
}

func encodeFloat32Vector(vector []float32) ([]byte, int, error) {
	if len(vector) == 0 {
		return nil, 0, fmt.Errorf("vector must not be empty")
	}
	buf := make([]byte, len(vector)*4)
	for i, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, 0, fmt.Errorf("vector contains non-finite value")
		}
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf, len(vector), nil
}

func (b sqliteVecRetrievalBackend) searchVectorStoreHybridResults(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery, scoreThreshold *float64) ([]domain.VectorStoreSearchResult, error) {
	semanticResults, err := b.searchVectorStoreSemanticResults(ctx, store, query, nil)
	if err != nil {
		return nil, err
	}
	lexicalResults, err := store.searchVectorStoreLexicalResults(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	return fuseHybridSearchResults(semanticResults, lexicalResults, query.HybridSearch, scoreThreshold), nil
}

func fuseHybridSearchResults(semanticResults, lexicalResults []domain.VectorStoreSearchResult, hybrid *domain.VectorStoreHybridSearchOptions, scoreThreshold *float64) []domain.VectorStoreSearchResult {
	type scoredHybridResult struct {
		domain.VectorStoreSearchResult
		score float64
	}

	if hybrid == nil {
		return nil
	}

	semanticRanks := make(map[string]int, len(semanticResults))
	semanticByFile := make(map[string]domain.VectorStoreSearchResult, len(semanticResults))
	for i, result := range semanticResults {
		semanticRanks[result.FileID] = i + 1
		semanticByFile[result.FileID] = result
	}

	lexicalRanks := make(map[string]int, len(lexicalResults))
	lexicalByFile := make(map[string]domain.VectorStoreSearchResult, len(lexicalResults))
	for i, result := range lexicalResults {
		lexicalRanks[result.FileID] = i + 1
		lexicalByFile[result.FileID] = result
	}

	maxPossible := 0.0
	if len(semanticResults) > 0 && hybrid.EmbeddingWeight > 0 {
		maxPossible += hybrid.EmbeddingWeight / (hybridRRFK + 1)
	}
	if len(lexicalResults) > 0 && hybrid.TextWeight > 0 {
		maxPossible += hybrid.TextWeight / (hybridRRFK + 1)
	}
	if maxPossible <= 0 {
		return []domain.VectorStoreSearchResult{}
	}

	bestByFile := make(map[string]scoredHybridResult, len(semanticResults)+len(lexicalResults))
	mergeResult := func(fileID string) {
		semanticRank, hasSemantic := semanticRanks[fileID]
		lexicalRank, hasLexical := lexicalRanks[fileID]
		if !hasSemantic && !hasLexical {
			return
		}

		rawScore := 0.0
		semanticContribution := 0.0
		lexicalContribution := 0.0
		if hasSemantic && hybrid.EmbeddingWeight > 0 {
			semanticContribution = hybrid.EmbeddingWeight / (hybridRRFK + float64(semanticRank))
			rawScore += semanticContribution
		}
		if hasLexical && hybrid.TextWeight > 0 {
			lexicalContribution = hybrid.TextWeight / (hybridRRFK + float64(lexicalRank))
			rawScore += lexicalContribution
		}

		score := rawScore / maxPossible
		if score > 1 {
			score = 1
		}
		if scoreThreshold != nil && score < *scoreThreshold {
			return
		}

		base := semanticByFile[fileID]
		if !hasSemantic || (hasLexical && lexicalContribution > semanticContribution) {
			base = lexicalByFile[fileID]
		}
		base.Score = score
		bestByFile[fileID] = scoredHybridResult{
			VectorStoreSearchResult: base,
			score:                   score,
		}
	}

	for fileID := range semanticByFile {
		mergeResult(fileID)
	}
	for fileID := range lexicalByFile {
		mergeResult(fileID)
	}

	results := make([]domain.VectorStoreSearchResult, 0, len(bestByFile))
	for _, result := range bestByFile {
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
	return results
}

func localRerankProfileForRanker(ranker string) *localRerankProfile {
	switch strings.TrimSpace(ranker) {
	case "none":
		return nil
	case "", "auto":
		profile := latestLocalRerankProfile
		return &profile
	case "default_2024_08_21", "default-2024-08-21":
		profile := legacyLocalRerankProfile
		return &profile
	default:
		return nil
	}
}

func rerankVectorStoreResults(results []domain.VectorStoreSearchResult, queries []string, profile localRerankProfile, scoreThreshold *float64) []domain.VectorStoreSearchResult {
	if len(results) == 0 {
		return nil
	}

	type rerankedResult struct {
		domain.VectorStoreSearchResult
		score float64
	}

	reranked := make([]rerankedResult, 0, len(results))
	for _, result := range results {
		contentText := joinedSearchResultText(result.Content)
		keywordScore := chunkScore(contentText, queries)
		phraseScore := queryPhraseMatchScore(contentText, queries)
		filenameScore := queryFilenameMatchScore(result.Filename, queries)

		score := (profile.baseWeight * clamp01(result.Score)) +
			(profile.keywordWeight * keywordScore) +
			(profile.phraseWeight * phraseScore) +
			(profile.filenameWeight * filenameScore)
		score = clamp01(score)
		if scoreThreshold != nil && score < *scoreThreshold {
			continue
		}

		result.Score = score
		reranked = append(reranked, rerankedResult{
			VectorStoreSearchResult: result,
			score:                   score,
		})
	}

	sort.Slice(reranked, func(i, j int) bool {
		if reranked[i].score == reranked[j].score {
			if reranked[i].Filename == reranked[j].Filename {
				return reranked[i].FileID < reranked[j].FileID
			}
			return reranked[i].Filename < reranked[j].Filename
		}
		return reranked[i].score > reranked[j].score
	})

	out := make([]domain.VectorStoreSearchResult, 0, len(reranked))
	for _, result := range reranked {
		out = append(out, result.VectorStoreSearchResult)
	}
	return out
}

func joinedSearchResultText(content []domain.VectorStoreSearchResultContent) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "text" {
			text := strings.TrimSpace(item.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func queryPhraseMatchScore(content string, queries []string) float64 {
	normalizedContent := normalizeSearchText(content)
	if normalizedContent == "" {
		return 0
	}
	best := 0.0
	for _, query := range queries {
		normalizedQuery := normalizeSearchText(query)
		if normalizedQuery == "" {
			continue
		}
		if strings.Contains(normalizedContent, normalizedQuery) {
			best = 1
			continue
		}
		queryTerms := tokenizeTerms(normalizedQuery)
		if len(queryTerms) == 0 {
			continue
		}
		adjacentMatches := 0
		for i := 0; i < len(queryTerms)-1; i++ {
			needle := queryTerms[i] + " " + queryTerms[i+1]
			if strings.Contains(normalizedContent, needle) {
				adjacentMatches++
			}
		}
		score := float64(adjacentMatches) / float64(maxInt(1, len(queryTerms)-1))
		if score > best {
			best = score
		}
	}
	return clamp01(best)
}

func queryFilenameMatchScore(filename string, queries []string) float64 {
	normalizedFilename := normalizeSearchText(filename)
	if normalizedFilename == "" {
		return 0
	}
	best := 0.0
	for _, query := range queries {
		queryTerms := tokenizeTerms(query)
		if len(queryTerms) == 0 {
			continue
		}
		matches := 0
		unique := map[string]struct{}{}
		for _, term := range queryTerms {
			if _, ok := unique[term]; ok {
				continue
			}
			unique[term] = struct{}{}
			if strings.Contains(normalizedFilename, term) {
				matches++
			}
		}
		if len(unique) == 0 {
			continue
		}
		score := float64(matches) / float64(len(unique))
		if score > best {
			best = score
		}
	}
	return clamp01(best)
}

func normalizeSearchText(text string) string {
	return strings.Join(tokenizeTerms(text), " ")
}

func clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
