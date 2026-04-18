package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage/sqlite"
	"llama_shim/internal/testutil"

	_ "modernc.org/sqlite"
)

type fakeEmbedder struct{}

func (fakeEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "banana"):
			out = append(out, []float32{1, 0, 0})
		case strings.Contains(lower, "ocean"):
			out = append(out, []float32{0, 1, 0})
		default:
			out = append(out, []float32{0, 0, 1})
		}
	}
	return out, nil
}

type hybridTestEmbedder struct{}

func (hybridTestEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.TrimSpace(strings.ToLower(text))
		switch {
		case strings.Contains(lower, "semanticwinner"), lower == "banana nutrition":
			out = append(out, []float32{1, 0})
		default:
			out = append(out, []float32{0, 1})
		}
	}
	return out, nil
}

type rerankingTestEmbedder struct{}

func (rerankingTestEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.TrimSpace(strings.ToLower(text))
		switch {
		case lower == "banana nutrition", strings.Contains(lower, "semanticwinner"):
			out = append(out, []float32{1, 0})
		case strings.Contains(lower, "banana nutrition exact phrase"):
			out = append(out, []float32{0.8, 0.6})
		default:
			out = append(out, []float32{0, 1})
		}
	}
	return out, nil
}

type semanticV1Embedder struct{}

func (semanticV1Embedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "banana"):
			out = append(out, []float32{1, 0})
		case strings.Contains(lower, "ocean"):
			out = append(out, []float32{0, 1})
		default:
			out = append(out, []float32{0.5, 0.5})
		}
	}
	return out, nil
}

type semanticV2Embedder struct{}

func (semanticV2Embedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "banana"):
			out = append(out, []float32{1, 0, 0})
		case strings.Contains(lower, "ocean"):
			out = append(out, []float32{0, 1, 0})
		default:
			out = append(out, []float32{0, 0, 1})
		}
	}
	return out, nil
}

func TestStoreSaveResponseRoundTripAndLineage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	first := domain.StoredResponse{
		ID:                   "resp_first",
		Model:                "test-model",
		RequestJSON:          `{"input":"first"}`,
		ResponseJSON:         `{"id":"resp_first","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"model":"test-model","output":[{"id":"msg_first","type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}],"store":true,"background":false,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":"one"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "first")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "first")},
		Output:               []domain.Item{domain.NewOutputTextMessage("one")},
		OutputText:           "one",
		Store:                true,
		CreatedAt:            "2026-04-02T12:00:00Z",
		CompletedAt:          "2026-04-02T12:00:00Z",
	}
	second := domain.StoredResponse{
		ID:                   "resp_second",
		Model:                "test-model",
		RequestJSON:          `{"input":"second"}`,
		ResponseJSON:         `{"id":"resp_second","object":"response","created_at":1712059260,"status":"completed","completed_at":1712059260,"error":null,"incomplete_details":null,"model":"test-model","output":[{"id":"msg_second","type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}],"previous_response_id":"resp_first","store":true,"background":false,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":"two"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "second")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "first"), domain.NewOutputTextMessage("one"), domain.NewInputTextMessage("user", "second")},
		Output:               []domain.Item{domain.NewOutputTextMessage("two")},
		OutputText:           "two",
		PreviousResponseID:   first.ID,
		Store:                true,
		CreatedAt:            "2026-04-02T12:01:00Z",
		CompletedAt:          "2026-04-02T12:01:00Z",
	}

	require.NoError(t, store.SaveResponse(ctx, first))
	require.NoError(t, store.SaveResponse(ctx, second))

	got, err := store.GetResponse(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, got.ID)
	require.Equal(t, second.Model, got.Model)
	require.Equal(t, second.RequestJSON, got.RequestJSON)
	require.Equal(t, second.ResponseJSON, got.ResponseJSON)
	require.Equal(t, second.PreviousResponseID, got.PreviousResponseID)
	require.Equal(t, second.OutputText, got.OutputText)
	require.True(t, got.Store)
	require.Len(t, got.NormalizedInputItems, 1)
	require.Equal(t, "second", domain.MessageText(got.NormalizedInputItems[0]))
	require.Len(t, got.EffectiveInputItems, 3)
	require.Equal(t, "first", domain.MessageText(got.EffectiveInputItems[0]))
	require.Equal(t, "one", domain.MessageText(got.EffectiveInputItems[1]))
	require.Equal(t, "second", domain.MessageText(got.EffectiveInputItems[2]))
	require.Len(t, got.Output, 1)
	require.Equal(t, "two", domain.MessageText(got.Output[0]))

	lineage, err := store.GetResponseLineage(ctx, second.ID)
	require.NoError(t, err)
	require.Len(t, lineage, 2)
	require.Equal(t, []string{first.ID, second.ID}, []string{lineage[0].ID, lineage[1].ID})

	_, err = store.GetResponse(ctx, "resp_missing")
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestStoreSaveResponseReplayArtifactsRoundTripAndCascade(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	response := domain.StoredResponse{
		ID:                   "resp_artifacts",
		Model:                "test-model",
		RequestJSON:          `{"input":"draw a cat"}`,
		ResponseJSON:         `{"id":"resp_artifacts","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059201,"error":null,"incomplete_details":null,"model":"test-model","output":[{"id":"ig_test","type":"image_generation_call","status":"completed","result":"final"}],"store":true,"background":false,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "draw a cat")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "draw a cat")},
		Output:               []domain.Item{},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-14T10:00:00Z",
		CompletedAt:          "2026-04-14T10:00:01Z",
	}
	require.NoError(t, store.SaveResponse(ctx, response))

	artifacts := []domain.ResponseReplayArtifact{
		{
			ResponseID:  response.ID,
			Sequence:    7,
			EventType:   "response.image_generation_call.partial_image",
			PayloadJSON: `{"type":"response.image_generation_call.partial_image","item_id":"ig_test","output_index":0,"partial_image_index":0,"partial_image_b64":"aGVsbG8="}`,
		},
		{
			ResponseID:  response.ID,
			Sequence:    8,
			EventType:   "response.image_generation_call.partial_image",
			PayloadJSON: `{"type":"response.image_generation_call.partial_image","item_id":"ig_test","output_index":0,"partial_image_index":1,"partial_image_b64":"d29ybGQ="}`,
		},
	}
	require.NoError(t, store.SaveResponseReplayArtifacts(ctx, response.ID, artifacts))

	got, err := store.GetResponseReplayArtifacts(ctx, response.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, []int{7, 8}, []int{got[0].Sequence, got[1].Sequence})
	require.Equal(t, artifacts[0].PayloadJSON, got[0].PayloadJSON)
	require.Equal(t, artifacts[1].PayloadJSON, got[1].PayloadJSON)

	require.NoError(t, store.DeleteResponse(ctx, response.ID))

	got, err = store.GetResponseReplayArtifacts(ctx, response.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestStoreSaveResponseReplayArtifactsAppliesLimits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	response := domain.StoredResponse{
		ID:                   "resp_artifacts_limits",
		Model:                "test-model",
		RequestJSON:          `{"input":"draw a cat"}`,
		ResponseJSON:         `{"id":"resp_artifacts_limits","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059201,"error":null,"incomplete_details":null,"model":"test-model","output":[{"id":"ig_test","type":"image_generation_call","status":"completed","result":"final"}],"store":true,"background":false,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "draw a cat")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "draw a cat")},
		Output:               []domain.Item{},
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-14T10:00:00Z",
		CompletedAt:          "2026-04-14T10:00:01Z",
	}
	require.NoError(t, store.SaveResponse(ctx, response))

	const (
		maxCount       = 64
		maxPayloadSize = 1 << 20
	)
	artifacts := make([]domain.ResponseReplayArtifact, 0, maxCount+2)
	for i := 1; i <= maxCount+1; i++ {
		artifacts = append(artifacts, domain.ResponseReplayArtifact{
			ResponseID:  response.ID,
			Sequence:    i,
			EventType:   "response.image_generation_call.partial_image",
			PayloadJSON: fmt.Sprintf(`{"type":"response.image_generation_call.partial_image","item_id":"ig_test","partial_image_b64":"%d"}`, i),
		})
	}
	artifacts = append(artifacts, domain.ResponseReplayArtifact{
		ResponseID:  response.ID,
		Sequence:    maxCount + 2,
		EventType:   "response.image_generation_call.partial_image",
		PayloadJSON: strings.Repeat("a", maxPayloadSize+1),
	})

	require.NoError(t, store.SaveResponseReplayArtifacts(ctx, response.ID, artifacts))

	got, err := store.GetResponseReplayArtifacts(ctx, response.ID)
	require.NoError(t, err)
	require.Len(t, got, maxCount)
	require.Equal(t, maxCount, got[len(got)-1].Sequence)
}

func TestOpenWithOptionsRejectsUnsupportedRetrievalBackend(t *testing.T) {
	t.Parallel()

	_, err := sqlite.OpenWithOptions(context.Background(), testutil.TempDBPath(t), sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: "bogus",
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported retrieval index backend "bogus"`)
}

func TestOpenWithOptionsRejectsSQLiteVecBackendWithoutEmbedder(t *testing.T) {
	t.Parallel()

	_, err := sqlite.OpenWithOptions(context.Background(), testutil.TempDBPath(t), sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `retrieval index backend "sqlite_vec" requires a configured embedder backend`)
}

func TestStoreSearchVectorStoreSQLiteVecBackend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenWithOptions(ctx, testutil.TempDBPath(t), sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		Embedder: fakeEmbedder{},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	fileBanana := domain.StoredFile{
		ID:        "file_banana",
		Filename:  "banana.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("Banana smoothie recipe with ripe banana and yogurt.")),
		CreatedAt: 1712059200,
		Status:    "processed",
		Content:   []byte("Banana smoothie recipe with ripe banana and yogurt."),
	}
	fileOcean := domain.StoredFile{
		ID:        "file_ocean",
		Filename:  "ocean.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("Ocean tides and marine currents reference.")),
		CreatedAt: 1712059201,
		Status:    "processed",
		Content:   []byte("Ocean tides and marine currents reference."),
	}
	require.NoError(t, store.SaveFile(ctx, fileBanana))
	require.NoError(t, store.SaveFile(ctx, fileOcean))

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_semantic",
		Name:         "Semantic Store",
		Metadata:     map[string]string{},
		CreatedAt:    1712059202,
		LastActiveAt: 1712059202,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileBanana.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059203)
	require.NoError(t, err)
	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileOcean.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059204)
	require.NoError(t, err)

	page, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"banana nutrition"},
		MaxNumResults:  5,
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileBanana.ID, page.Results[0].FileID)
	require.Equal(t, "banana.txt", page.Results[0].Filename)
	require.Greater(t, page.Results[0].Score, 0.7)
}

func TestStoreSearchVectorStoreSQLiteVecReindexesOnEmbedderModelChange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := testutil.TempDBPath(t)

	store, err := sqlite.OpenWithOptions(ctx, dbPath, sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
			Embedder: retrieval.EmbedderConfig{
				Model: "embed-v1",
			},
		},
		Embedder: semanticV1Embedder{},
	})
	require.NoError(t, err)

	fileBanana := domain.StoredFile{
		ID:        "file_banana",
		Filename:  "banana.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("Banana smoothie recipe with ripe banana and yogurt.")),
		CreatedAt: 1712059200,
		Status:    "processed",
		Content:   []byte("Banana smoothie recipe with ripe banana and yogurt."),
	}
	fileOcean := domain.StoredFile{
		ID:        "file_ocean",
		Filename:  "ocean.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("Ocean tides and marine currents reference.")),
		CreatedAt: 1712059201,
		Status:    "processed",
		Content:   []byte("Ocean tides and marine currents reference."),
	}
	require.NoError(t, store.SaveFile(ctx, fileBanana))
	require.NoError(t, store.SaveFile(ctx, fileOcean))

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_semantic",
		Name:         "Semantic Store",
		Metadata:     map[string]string{},
		CreatedAt:    1712059202,
		LastActiveAt: 1712059202,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))
	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileBanana.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059203)
	require.NoError(t, err)
	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileOcean.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059204)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	rawDB, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer rawDB.Close()

	var countV1 int
	require.NoError(t, rawDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM vector_store_chunk_embeddings
		WHERE embedding_model = 'embed-v1' AND embedding_dimensions = 2
	`).Scan(&countV1))
	require.Greater(t, countV1, 0)

	store, err = sqlite.OpenWithOptions(ctx, dbPath, sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
			Embedder: retrieval.EmbedderConfig{
				Model: "embed-v2",
			},
		},
		Embedder: semanticV2Embedder{},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, store.Close())
	}()

	page, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"banana nutrition"},
		MaxNumResults:  5,
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileBanana.ID, page.Results[0].FileID)

	var (
		countV2  int
		countOld int
	)
	require.NoError(t, rawDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM vector_store_chunk_embeddings e
		JOIN vector_store_chunks c ON c.id = e.chunk_id
		WHERE c.vector_store_id = ? AND e.embedding_model = 'embed-v2' AND e.embedding_dimensions = 3
	`, vectorStore.ID).Scan(&countV2))
	require.NoError(t, rawDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM vector_store_chunk_embeddings e
		JOIN vector_store_chunks c ON c.id = e.chunk_id
		WHERE c.vector_store_id = ? AND e.embedding_model = 'embed-v1'
	`, vectorStore.ID).Scan(&countOld))
	require.Greater(t, countV2, 0)
	require.Zero(t, countOld)
}

func TestStoreSearchVectorStoreSQLiteVecHybridRanking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenWithOptions(ctx, testutil.TempDBPath(t), sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		Embedder: hybridTestEmbedder{},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	fileSemantic := domain.StoredFile{
		ID:        "file_semantic",
		Filename:  "semantic.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("semanticwinner banana orchard notes")),
		CreatedAt: 1712059200,
		Status:    "processed",
		Content:   []byte("semanticwinner banana orchard notes"),
	}
	fileLexical := domain.StoredFile{
		ID:        "file_lexical",
		Filename:  "lexical.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("banana nutrition facts nutrition calories")),
		CreatedAt: 1712059201,
		Status:    "processed",
		Content:   []byte("banana nutrition facts nutrition calories"),
	}
	require.NoError(t, store.SaveFile(ctx, fileSemantic))
	require.NoError(t, store.SaveFile(ctx, fileLexical))

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_hybrid",
		Name:         "Hybrid Store",
		Metadata:     map[string]string{},
		CreatedAt:    1712059202,
		LastActiveAt: 1712059202,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileSemantic.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059203)
	require.NoError(t, err)
	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileLexical.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059204)
	require.NoError(t, err)

	page, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID: vectorStore.ID,
		Queries:       []string{"banana nutrition"},
		MaxNumResults: 5,
		Ranker:        "none",
		HybridSearch: &domain.VectorStoreHybridSearchOptions{
			EmbeddingWeight: 10,
			TextWeight:      1,
		},
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileSemantic.ID, page.Results[0].FileID)
	require.Equal(t, "semantic.txt", page.Results[0].Filename)

	page, err = store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID: vectorStore.ID,
		Queries:       []string{"banana nutrition"},
		MaxNumResults: 5,
		Ranker:        "none",
		HybridSearch: &domain.VectorStoreHybridSearchOptions{
			EmbeddingWeight: 1,
			TextWeight:      10,
		},
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileLexical.ID, page.Results[0].FileID)
	require.Equal(t, "lexical.txt", page.Results[0].Filename)
}

func TestStoreSearchVectorStoreSQLiteVecReranking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.OpenWithOptions(ctx, testutil.TempDBPath(t), sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: retrieval.IndexBackendSQLiteVec,
		},
		Embedder: rerankingTestEmbedder{},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	fileSemantic := domain.StoredFile{
		ID:        "file_semantic_rerank",
		Filename:  "semantic.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("semanticwinner banana orchard notes")),
		CreatedAt: 1712059300,
		Status:    "processed",
		Content:   []byte("semanticwinner banana orchard notes"),
	}
	fileReranked := domain.StoredFile{
		ID:        "file_reranked",
		Filename:  "banana-nutrition.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("banana nutrition exact phrase and calories")),
		CreatedAt: 1712059301,
		Status:    "processed",
		Content:   []byte("banana nutrition exact phrase and calories"),
	}
	require.NoError(t, store.SaveFile(ctx, fileSemantic))
	require.NoError(t, store.SaveFile(ctx, fileReranked))

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_rerank",
		Name:         "Rerank Store",
		Metadata:     map[string]string{},
		CreatedAt:    1712059302,
		LastActiveAt: 1712059302,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileSemantic.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059303)
	require.NoError(t, err)
	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileReranked.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712059304)
	require.NoError(t, err)

	page, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"banana nutrition"},
		MaxNumResults:  5,
		Ranker:         "none",
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileSemantic.ID, page.Results[0].FileID)

	page, err = store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"banana nutrition"},
		MaxNumResults:  5,
		Ranker:         "auto",
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileReranked.ID, page.Results[0].FileID)
	require.Greater(t, page.Results[0].Score, page.Results[1].Score)
}

func TestStoreSaveChatCompletionRoundTripAndList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	first := domain.StoredChatCompletion{
		ID:           "chatcmpl_first",
		Model:        "gpt-5.4",
		Metadata:     map[string]string{"topic": "alpha"},
		RequestJSON:  `{"model":"gpt-5.4","store":true,"metadata":{"topic":"alpha"},"messages":[{"role":"user","content":"first"}]}`,
		ResponseJSON: `{"id":"chatcmpl_first","object":"chat.completion","created":1712059200,"model":"gpt-5.4","metadata":{"topic":"alpha"},"choices":[{"index":0,"message":{"role":"assistant","content":"one"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059200,
	}
	second := domain.StoredChatCompletion{
		ID:           "chatcmpl_second",
		Model:        "gpt-5.4",
		Metadata:     map[string]string{"topic": "beta"},
		RequestJSON:  `{"model":"gpt-5.4","store":true,"metadata":{"topic":"beta"},"messages":[{"role":"user","content":"second"}]}`,
		ResponseJSON: `{"id":"chatcmpl_second","object":"chat.completion","created":1712059260,"model":"gpt-5.4","metadata":{"topic":"beta"},"choices":[{"index":0,"message":{"role":"assistant","content":"two"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059260,
	}
	third := domain.StoredChatCompletion{
		ID:           "chatcmpl_third",
		Model:        "gpt-4o-mini",
		Metadata:     map[string]string{"topic": "alpha"},
		RequestJSON:  `{"model":"gpt-4o-mini","store":true,"metadata":{"topic":"alpha"},"messages":[{"role":"user","content":"third"}]}`,
		ResponseJSON: `{"id":"chatcmpl_third","object":"chat.completion","created":1712059320,"model":"gpt-4o-mini","metadata":{"topic":"alpha"},"choices":[{"index":0,"message":{"role":"assistant","content":"three"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059320,
	}

	require.NoError(t, store.SaveChatCompletion(ctx, first))
	require.NoError(t, store.SaveChatCompletion(ctx, second))
	require.NoError(t, store.SaveChatCompletion(ctx, third))

	got, err := store.GetChatCompletion(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, got.ID)
	require.Equal(t, second.Model, got.Model)
	require.Equal(t, second.Metadata, got.Metadata)
	require.Equal(t, second.RequestJSON, got.RequestJSON)
	require.Equal(t, second.ResponseJSON, got.ResponseJSON)
	require.Equal(t, second.CreatedAt, got.CreatedAt)

	page, err := store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		Model:    "gpt-5.4",
		Metadata: map[string]string{"topic": "alpha"},
		Limit:    10,
		Order:    domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Completions, 1)
	require.Equal(t, first.ID, page.Completions[0].ID)

	page, err = store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		Limit: 1,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Len(t, page.Completions, 1)
	require.Equal(t, first.ID, page.Completions[0].ID)

	page, err = store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		After: page.Completions[0].ID,
		Limit: 2,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Completions, 2)
	require.Equal(t, []string{second.ID, third.ID}, []string{page.Completions[0].ID, page.Completions[1].ID})

	_, err = store.GetChatCompletion(ctx, "chatcmpl_missing")
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestStoreUpdateAndDeleteChatCompletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	completion := domain.StoredChatCompletion{
		ID:           "chatcmpl_update_me",
		Model:        "gpt-5.4",
		Metadata:     map[string]string{"topic": "alpha"},
		RequestJSON:  `{"model":"gpt-5.4","store":true,"metadata":{"topic":"alpha"},"messages":[{"role":"user","content":"first"}]}`,
		ResponseJSON: `{"id":"chatcmpl_update_me","object":"chat.completion","created":1712059200,"model":"gpt-5.4","metadata":{"topic":"alpha"},"choices":[{"index":0,"message":{"role":"assistant","content":"one"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059200,
	}

	require.NoError(t, store.SaveChatCompletion(ctx, completion))

	updated, err := store.UpdateChatCompletionMetadata(ctx, completion.ID, map[string]string{"topic": "beta", "owner": "shim"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"topic": "beta", "owner": "shim"}, updated.Metadata)
	require.Equal(t, map[string]string{"topic": "beta", "owner": "shim"}, extractStoredChatCompletionMetadata(t, updated.ResponseJSON))

	got, err := store.GetChatCompletion(ctx, completion.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"topic": "beta", "owner": "shim"}, got.Metadata)
	require.Equal(t, map[string]string{"topic": "beta", "owner": "shim"}, extractStoredChatCompletionMetadata(t, got.ResponseJSON))

	require.NoError(t, store.DeleteChatCompletion(ctx, completion.ID))

	_, err = store.GetChatCompletion(ctx, completion.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	err = store.DeleteChatCompletion(ctx, completion.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func extractStoredChatCompletionMetadata(t *testing.T, responseJSON string) map[string]string {
	t.Helper()

	var payload struct {
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(responseJSON), &payload))
	return payload.Metadata
}

func TestStoreSaveResponseUpsertsLifecyclePayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	initial := domain.StoredResponse{
		ID:                   "resp_upsert",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"background":true,"input":"ping"}`,
		ResponseJSON:         `{"id":"resp_upsert","object":"response","created_at":1712059200,"status":"in_progress","completed_at":null,"error":null,"incomplete_details":null,"model":"test-model","output":[],"store":true,"background":true,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "ping")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "ping")},
		Output:               nil,
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-02T12:00:00Z",
		CompletedAt:          "",
	}
	updated := initial
	updated.ResponseJSON = `{"id":"resp_upsert","object":"response","created_at":1712059200,"status":"cancelled","completed_at":null,"error":null,"incomplete_details":null,"model":"test-model","output":[],"store":true,"background":true,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":""}`
	updated.CompletedAt = ""

	require.NoError(t, store.SaveResponse(ctx, initial))
	require.NoError(t, store.SaveResponse(ctx, updated))

	got, err := store.GetResponse(ctx, initial.ID)
	require.NoError(t, err)
	require.Equal(t, updated.ResponseJSON, got.ResponseJSON)
	require.Equal(t, updated.CompletedAt, got.CompletedAt)
}

func TestStoreSaveCodeInterpreterSessionRoundTripAndTouch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	session := domain.CodeInterpreterSession{
		ID:                  "cntr_test",
		Backend:             "docker",
		Status:              "running",
		Name:                "Test Container",
		MemoryLimit:         "4g",
		ExpiresAfterMinutes: 45,
		CreatedAt:           "2026-04-12T10:00:00Z",
		LastActiveAt:        "2026-04-12T10:00:00Z",
	}
	require.NoError(t, store.SaveCodeInterpreterSession(ctx, session))

	got, err := store.GetCodeInterpreterSession(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, session, got)

	require.NoError(t, store.TouchCodeInterpreterSession(ctx, session.ID, "2026-04-12T10:05:00Z"))
	got, err = store.GetCodeInterpreterSession(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, "2026-04-12T10:05:00Z", got.LastActiveAt)

	require.NoError(t, store.DeleteCodeInterpreterSession(ctx, session.ID))
	_, err = store.GetCodeInterpreterSession(ctx, session.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestStoreSaveCodeInterpreterContainerFileRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	session := domain.CodeInterpreterSession{
		ID:                  "cntr_test",
		Backend:             "docker",
		Status:              "running",
		Name:                "Test Container",
		MemoryLimit:         "1g",
		ExpiresAfterMinutes: 20,
		CreatedAt:           "2026-04-12T10:00:00Z",
		LastActiveAt:        "2026-04-12T10:00:00Z",
	}
	require.NoError(t, store.SaveCodeInterpreterSession(ctx, session))
	require.NoError(t, store.SaveFile(ctx, domain.StoredFile{
		ID:        "file_test",
		Filename:  "codes.txt",
		Purpose:   "user_data",
		Bytes:     3,
		CreatedAt: 1712059200,
		Status:    "processed",
		Content:   []byte("777"),
	}))

	saved, err := store.SaveCodeInterpreterContainerFile(ctx, domain.CodeInterpreterContainerFile{
		ID:                "cfile_test",
		ContainerID:       session.ID,
		BackingFileID:     "file_test",
		DeleteBackingFile: true,
		Path:              "/mnt/data/codes.txt",
		Source:            "user",
		Bytes:             3,
		CreatedAt:         1712059200,
	})
	require.NoError(t, err)
	require.Equal(t, "cfile_test", saved.ID)

	got, err := store.GetCodeInterpreterContainerFile(ctx, session.ID, saved.ID)
	require.NoError(t, err)
	require.Equal(t, saved, got)

	byPath, err := store.GetCodeInterpreterContainerFileByPath(ctx, session.ID, saved.Path)
	require.NoError(t, err)
	require.Equal(t, saved, byPath)

	page, err := store.ListCodeInterpreterContainerFiles(ctx, domain.ListCodeInterpreterContainerFilesQuery{
		ContainerID: session.ID,
		Limit:       10,
		Order:       domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, page.Files, 1)
	require.Equal(t, saved, page.Files[0])

	refCount, err := store.CountCodeInterpreterContainerFileBackingReferences(ctx, saved.BackingFileID)
	require.NoError(t, err)
	require.Equal(t, 1, refCount)

	require.NoError(t, store.DeleteCodeInterpreterContainerFile(ctx, session.ID, saved.ID))
	_, err = store.GetCodeInterpreterContainerFile(ctx, session.ID, saved.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	refCount, err = store.CountCodeInterpreterContainerFileBackingReferences(ctx, saved.BackingFileID)
	require.NoError(t, err)
	require.Zero(t, refCount)
}

func TestStoreCreateConversationAppendAndPaginateItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	createdAt := "2026-04-02T12:10:00Z"
	completedAt := "2026-04-02T12:11:00Z"
	conversation := domain.Conversation{
		ID:        "conv_test",
		Object:    "conversation",
		Metadata:  map[string]string{"topic": "demo"},
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Items: []domain.Item{
			domain.NewInputTextMessage("system", "You are a test assistant."),
			domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		},
	}

	require.NoError(t, store.CreateConversation(ctx, conversation))

	input := []domain.Item{domain.NewInputTextMessage("user", "What is the code? Reply with just the number.")}
	output := []domain.Item{domain.NewOutputTextMessage("777")}
	response := domain.StoredResponse{
		ID:                   "resp_conv_followup",
		Model:                "test-model",
		RequestJSON:          `{"conversation":"conv_test"}`,
		NormalizedInputItems: input,
		EffectiveInputItems:  append(append([]domain.Item{}, conversation.Items...), input...),
		Output:               output,
		OutputText:           "777",
		ConversationID:       conversation.ID,
		Store:                true,
		CreatedAt:            completedAt,
		CompletedAt:          completedAt,
	}

	require.NoError(t, store.SaveResponseAndAppendConversation(ctx, conversation, response, input, output))

	gotConversation, gotItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, conversation.ID, gotConversation.ID)
	require.Equal(t, map[string]string{"topic": "demo"}, gotConversation.Metadata)
	require.Equal(t, 2, gotConversation.Version)
	require.Len(t, gotItems, 4)
	require.Equal(t, []string{"seed", "seed", "response_input", "response_output"}, []string{
		gotItems[0].Source,
		gotItems[1].Source,
		gotItems[2].Source,
		gotItems[3].Source,
	})
	require.Equal(t, []int{0, 1, 2, 3}, []int{
		gotItems[0].Seq,
		gotItems[1].Seq,
		gotItems[2].Seq,
		gotItems[3].Seq,
	})
	require.Equal(t, "You are a test assistant.", domain.MessageText(gotItems[0].Item))
	require.Equal(t, "Remember: code=777. Reply OK.", domain.MessageText(gotItems[1].Item))
	require.Equal(t, "What is the code? Reply with just the number.", domain.MessageText(gotItems[2].Item))
	require.Equal(t, "777", domain.MessageText(gotItems[3].Item))
	require.NotEmpty(t, gotItems[2].ID)
	require.NotEmpty(t, gotItems[3].ID)

	page, err := store.ListConversationItems(ctx, domain.ListConversationItemsQuery{
		ConversationID: conversation.ID,
		After:          gotItems[1].ID,
		Limit:          2,
		Order:          domain.ConversationItemOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Items, 2)
	require.Equal(t, []int{2, 3}, []int{page.Items[0].Seq, page.Items[1].Seq})
	require.Equal(t, "response_input", page.Items[0].Source)
	require.Equal(t, "response_output", page.Items[1].Source)

	appended, err := store.AppendConversationItems(ctx, gotConversation, []domain.Item{
		domain.NewInputTextMessage("user", "Another turn"),
		domain.NewInputTextMessage("user", "And one more"),
	}, completedAt)
	require.NoError(t, err)
	require.Len(t, appended, 2)
	require.Equal(t, "append", appended[0].Source)
	require.Equal(t, 4, appended[0].Seq)
	require.Equal(t, 5, appended[1].Seq)

	gotItem, err := store.GetConversationItem(ctx, conversation.ID, appended[1].ID)
	require.NoError(t, err)
	require.Equal(t, appended[1].ID, gotItem.ID)
	require.Equal(t, "And one more", domain.MessageText(gotItem.Item))
}

func TestStoreDeleteConversationItemAllowsAppendAfterMidSequenceGap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	createdAt := "2026-04-02T12:10:00Z"
	completedAt := "2026-04-02T12:11:00Z"
	deletedAt := "2026-04-02T12:12:00Z"
	appendedAt := "2026-04-02T12:13:00Z"
	conversation := domain.Conversation{
		ID:        "conv_delete_test",
		Object:    "conversation",
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Items: []domain.Item{
			domain.NewInputTextMessage("system", "You are a test assistant."),
			domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		},
	}

	require.NoError(t, store.CreateConversation(ctx, conversation))

	input := []domain.Item{domain.NewInputTextMessage("user", "What is the code? Reply with just the number.")}
	output := []domain.Item{domain.NewOutputTextMessage("777")}
	response := domain.StoredResponse{
		ID:                   "resp_delete_followup",
		Model:                "test-model",
		RequestJSON:          `{"conversation":"conv_delete_test"}`,
		NormalizedInputItems: input,
		EffectiveInputItems:  append(append([]domain.Item{}, conversation.Items...), input...),
		Output:               output,
		OutputText:           "777",
		ConversationID:       conversation.ID,
		Store:                true,
		CreatedAt:            completedAt,
		CompletedAt:          completedAt,
	}

	require.NoError(t, store.SaveResponseAndAppendConversation(ctx, conversation, response, input, output))

	gotConversation, gotItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Len(t, gotItems, 4)

	require.NoError(t, store.DeleteConversationItem(ctx, gotConversation, gotItems[0].ID, deletedAt))

	_, err = store.GetConversationItem(ctx, conversation.ID, gotItems[0].ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	updatedConversation, updatedItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, 3, updatedConversation.Version)
	require.Len(t, updatedItems, 3)
	require.Equal(t, []int{1, 2, 3}, []int{
		updatedItems[0].Seq,
		updatedItems[1].Seq,
		updatedItems[2].Seq,
	})

	appended, err := store.AppendConversationItems(ctx, updatedConversation, []domain.Item{
		domain.NewInputTextMessage("user", "After delete"),
	}, appendedAt)
	require.NoError(t, err)
	require.Len(t, appended, 1)
	require.Equal(t, 4, appended[0].Seq)
}

func TestStoreSaveFileAttachVectorStoreAndSearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	file := domain.StoredFile{
		ID:        "file_alpha",
		Filename:  "alpha.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("OpenAI retrieval layer stores chunks for local file search.")),
		CreatedAt: 1712059200,
		Status:    "processed",
		Content:   []byte("OpenAI retrieval layer stores chunks for local file search."),
	}
	require.NoError(t, store.SaveFile(ctx, file))

	gotFile, err := store.GetFile(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, file.Filename, gotFile.Filename)
	require.Equal(t, file.Purpose, gotFile.Purpose)

	filePage, err := store.ListFiles(ctx, domain.ListFilesQuery{
		Purpose: "assistants",
		Limit:   10,
		Order:   domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, filePage.Files, 1)
	require.Equal(t, file.ID, filePage.Files[0].ID)

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_alpha",
		Name:         "Local Search",
		Metadata:     map[string]string{"topic": "demo"},
		CreatedAt:    1712059201,
		LastActiveAt: 1712059201,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	attached, err := store.AttachFileToVectorStore(
		ctx,
		vectorStore.ID,
		file.ID,
		map[string]any{"topic": "docs", "priority": float64(1)},
		domain.DefaultFileChunkingStrategy(),
		1712059202,
	)
	require.NoError(t, err)
	require.Equal(t, "completed", attached.Status)
	require.Nil(t, attached.LastError)
	require.Equal(t, file.Bytes, attached.UsageBytes)

	storedVectorStore, err := store.GetVectorStore(ctx, vectorStore.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", storedVectorStore.Status)
	require.Equal(t, 1, storedVectorStore.FileCounts.Completed)
	require.Equal(t, 1, storedVectorStore.FileCounts.Total)
	require.Equal(t, file.Bytes, storedVectorStore.UsageBytes)

	vectorStoreFile, err := store.GetVectorStoreFile(ctx, vectorStore.ID, file.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"topic": "docs", "priority": float64(1)}, vectorStoreFile.Attributes)

	vectorStoreFilePage, err := store.ListVectorStoreFiles(ctx, domain.ListVectorStoreFilesQuery{
		VectorStoreID: vectorStore.ID,
		Filter:        "completed",
		Limit:         10,
		Order:         domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, vectorStoreFilePage.Files, 1)
	require.Equal(t, file.ID, vectorStoreFilePage.Files[0].ID)

	searchPage, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"local file search"},
		MaxNumResults:  10,
		RawSearchQuery: "local file search",
	})
	require.NoError(t, err)
	require.False(t, searchPage.HasMore)
	require.Len(t, searchPage.Results, 1)
	require.Equal(t, file.ID, searchPage.Results[0].FileID)
	require.Contains(t, searchPage.Results[0].Content[0].Text, "local file search")

	require.NoError(t, store.DeleteVectorStoreFile(ctx, vectorStore.ID, file.ID))

	afterDeletePage, err := store.ListVectorStoreFiles(ctx, domain.ListVectorStoreFilesQuery{
		VectorStoreID: vectorStore.ID,
		Limit:         10,
		Order:         domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Empty(t, afterDeletePage.Files)
}

func TestStoreAttachBinaryFileToVectorStoreFailsIndexing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	file := domain.StoredFile{
		ID:        "file_binary",
		Filename:  "binary.bin",
		Purpose:   "assistants",
		Bytes:     3,
		CreatedAt: 1712059300,
		Status:    "processed",
		Content:   []byte{0xff, 0xfe, 0xfd},
	}
	require.NoError(t, store.SaveFile(ctx, file))

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_binary",
		Name:         "Binary Search",
		Metadata:     map[string]string{},
		CreatedAt:    1712059301,
		LastActiveAt: 1712059301,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	attached, err := store.AttachFileToVectorStore(
		ctx,
		vectorStore.ID,
		file.ID,
		map[string]any{},
		domain.DefaultFileChunkingStrategy(),
		1712059302,
	)
	require.NoError(t, err)
	require.Equal(t, "failed", attached.Status)
	require.NotNil(t, attached.LastError)
	require.Equal(t, "unsupported_file", attached.LastError.Code)

	searchPage, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"anything"},
		MaxNumResults:  10,
		RawSearchQuery: "anything",
	})
	require.NoError(t, err)
	require.Empty(t, searchPage.Results)
}

func TestStoreSearchVectorStoreReturnsMultipleTopChunksPerFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	file := domain.StoredFile{
		ID:        "file_codes",
		Filename:  "codes.txt",
		Purpose:   "assistants",
		Bytes:     int64(len("code decoy placeholder actual code 777 backup code note")),
		CreatedAt: 1712059303,
		Status:    "processed",
		Content:   []byte("code decoy placeholder actual code 777 backup code note"),
	}
	require.NoError(t, store.SaveFile(ctx, file))

	vectorStore := domain.StoredVectorStore{
		ID:           "vs_codes",
		Name:         "Codes Search",
		Metadata:     map[string]string{},
		CreatedAt:    1712059304,
		LastActiveAt: 1712059304,
	}
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	_, err := store.AttachFileToVectorStore(
		ctx,
		vectorStore.ID,
		file.ID,
		map[string]any{},
		domain.FileChunkingStrategy{
			Type: "static",
			Static: &domain.StaticChunkingStrategy{
				MaxChunkSizeTokens: 3,
				ChunkOverlapTokens: 0,
			},
		},
		1712059305,
	)
	require.NoError(t, err)

	searchPage, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"code"},
		MaxNumResults:  5,
		RawSearchQuery: "code",
	})
	require.NoError(t, err)
	require.Len(t, searchPage.Results, 1)
	require.Equal(t, file.ID, searchPage.Results[0].FileID)
	require.Len(t, searchPage.Results[0].Content, 3)
	require.Equal(t, "code decoy placeholder", searchPage.Results[0].Content[0].Text)
	require.Equal(t, "actual code 777", searchPage.Results[0].Content[1].Text)
	require.Equal(t, "backup code note", searchPage.Results[0].Content[2].Text)
}

func openTestStore(t *testing.T, ctx context.Context) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(ctx, testutil.TempDBPath(t))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}
