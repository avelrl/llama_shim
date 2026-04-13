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
	if _, err := store.GetVectorStore(ctx, query.VectorStoreID); err != nil {
		return domain.VectorStoreSearchPage{}, err
	}
	queryEmbeddings, err := b.embedder.EmbedTexts(ctx, query.Queries)
	if err != nil {
		return domain.VectorStoreSearchPage{}, fmt.Errorf("embed search query: %w", err)
	}

	type scoredResult struct {
		domain.VectorStoreSearchResult
		bestDistance float64
	}

	bestByFile := map[string]scoredResult{}
	for _, queryEmbedding := range queryEmbeddings {
		queryBlob, _, err := encodeFloat32Vector(queryEmbedding)
		if err != nil {
			return domain.VectorStoreSearchPage{}, fmt.Errorf("encode query embedding: %w", err)
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
			return domain.VectorStoreSearchPage{}, fmt.Errorf("query semantic vector store search: %w", err)
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
				return domain.VectorStoreSearchPage{}, fmt.Errorf("scan semantic search row: %w", err)
			}

			attributes := map[string]any{}
			if strings.TrimSpace(attributesJSON) != "" {
				if err := json.Unmarshal([]byte(attributesJSON), &attributes); err != nil {
					rows.Close()
					return domain.VectorStoreSearchPage{}, fmt.Errorf("decode vector store file attributes: %w", err)
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
			if query.ScoreThreshold != nil && score < *query.ScoreThreshold {
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
			return domain.VectorStoreSearchPage{}, fmt.Errorf("iterate semantic search rows: %w", err)
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
	if len(results) > query.MaxNumResults {
		results = results[:query.MaxNumResults]
	}

	now := domain.NowUTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		UPDATE vector_stores
		SET last_active_at = ?, expires_at = CASE
			WHEN expires_after_days IS NULL OR expires_after_anchor IS NULL THEN expires_at
			WHEN expires_after_anchor = 'last_active_at' THEN ? + (expires_after_days * 86400)
			ELSE expires_at
		END
		WHERE id = ?
	`, now, now, query.VectorStoreID); err != nil {
		return domain.VectorStoreSearchPage{}, fmt.Errorf("touch vector store search activity: %w", err)
	}

	return domain.VectorStoreSearchPage{
		SearchQuery: query.RawSearchQuery,
		Results:     results,
		HasMore:     false,
		NextPage:    nil,
	}, nil
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
