package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
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
	SearchVectorStore(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error)
}

type lexicalRetrievalBackend struct{}
type sqliteVecRetrievalBackend struct {
	embedder retrieval.Embedder
	model    string
}

const hybridRRFK = 60.0

type localRerankProfile struct {
	baseWeight     float64
	keywordWeight  float64
	phraseWeight   float64
	filenameWeight float64
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

	type chunk struct {
		ID      int64
		Content string
	}
	chunks := make([]chunk, 0, 8)
	for rows.Next() {
		var item chunk
		if err := rows.Scan(&item.ID, &item.Content); err != nil {
			return fmt.Errorf("scan vector store chunk for embeddings: %w", err)
		}
		chunks = append(chunks, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate vector store chunks for embeddings: %w", err)
	}
	if len(chunks) == 0 {
		return nil
	}

	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Content)
	}

	embeddings, err := b.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed vector store chunks: %w", err)
	}
	if len(embeddings) != len(chunks) {
		return fmt.Errorf("embedder returned %d vectors for %d chunks", len(embeddings), len(chunks))
	}

	for i, embedding := range embeddings {
		blob, dims, err := encodeFloat32Vector(embedding)
		if err != nil {
			return fmt.Errorf("encode chunk embedding %d: %w", i, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vector_store_chunk_embeddings (
				chunk_id, embedding, embedding_model, embedding_dimensions, created_at
			) VALUES (?, vec_f32(?), ?, ?, ?)
			ON CONFLICT(chunk_id) DO UPDATE SET
				embedding = excluded.embedding,
				embedding_model = excluded.embedding_model,
				embedding_dimensions = excluded.embedding_dimensions,
				created_at = excluded.created_at
		`, chunks[i].ID, blob, b.model, dims, params.CreatedAt); err != nil {
			return fmt.Errorf("upsert chunk embedding: %w", err)
		}
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
	queryEmbeddings, err := b.embedder.EmbedTexts(ctx, query.Queries)
	if err != nil {
		return nil, fmt.Errorf("embed search query: %w", err)
	}

	type scoredResult struct {
		domain.VectorStoreSearchResult
		bestDistance float64
	}

	bestByFile := map[string]scoredResult{}
	for _, queryEmbedding := range queryEmbeddings {
		queryBlob, _, err := encodeFloat32Vector(queryEmbedding)
		if err != nil {
			return nil, fmt.Errorf("encode query embedding: %w", err)
		}

		rows, err := store.db.QueryContext(ctx, `
			SELECT
				c.file_id,
				f.filename,
				v.attributes_json,
				c.content,
				vec_distance_cosine(e.embedding, vec_f32(?)) AS distance
			FROM vector_store_chunk_embeddings e
			JOIN vector_store_chunks c ON c.id = e.chunk_id
			JOIN files f ON f.id = c.file_id
			JOIN vector_store_files v ON v.vector_store_id = c.vector_store_id AND v.file_id = c.file_id
			WHERE c.vector_store_id = ? AND v.status = 'completed'
		`, queryBlob, query.VectorStoreID)
		if err != nil {
			return nil, fmt.Errorf("query semantic vector store search: %w", err)
		}

		for rows.Next() {
			var (
				fileID         string
				filename       string
				attributesJSON string
				content        string
				distance       float64
			)
			if err := rows.Scan(&fileID, &filename, &attributesJSON, &content, &distance); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan semantic search row: %w", err)
			}

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
			if exists && current.bestDistance <= distance {
				continue
			}
			bestByFile[fileID] = scoredResult{
				VectorStoreSearchResult: domain.VectorStoreSearchResult{
					FileID:     fileID,
					Filename:   filename,
					Score:      score,
					Attributes: attributes,
					Content: []domain.VectorStoreSearchResultContent{
						{Type: "text", Text: content},
					},
				},
				bestDistance: distance,
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate semantic search rows: %w", err)
		}
		rows.Close()
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
	return results, nil
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
	case "", "auto", "default-2024-11-15":
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
		contentText := firstSearchResultText(result.Content)
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

func firstSearchResultText(content []domain.VectorStoreSearchResultContent) string {
	for _, item := range content {
		if item.Type == "text" {
			return item.Text
		}
	}
	return ""
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
