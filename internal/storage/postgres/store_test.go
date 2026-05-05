package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
)

type pgvectorTestEmbedder struct{}

func (pgvectorTestEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
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

func TestOpenWithOptionsRejectsSQLiteOnlyRetrievalBackends(t *testing.T) {
	ctx := context.Background()
	_, err := OpenWithOptions(ctx, "postgres://example.invalid/llama_shim", OpenOptions{
		SQLitePath: filepath.Join(t.TempDir(), "sidecar.db"),
		Retrieval:  retrieval.Config{IndexBackend: retrieval.IndexBackendSQLiteFTS5},
	})
	require.ErrorContains(t, err, `postgres storage supports retrieval index backend "lexical" or "pgvector"`)
}

func TestOpenWithOptionsPGVectorRequiresEmbedder(t *testing.T) {
	ctx := context.Background()
	_, err := OpenWithOptions(ctx, "postgres://example.invalid/llama_shim", OpenOptions{
		SQLitePath: filepath.Join(t.TempDir(), "sidecar.db"),
		Retrieval:  retrieval.Config{IndexBackend: retrieval.IndexBackendPGVector},
	})
	require.ErrorContains(t, err, `retrieval index backend "pgvector" requires a configured embedder backend`)
}

func TestStoreFilePaginationSkipsContentAndMirrorsSidecar(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	files := []domain.StoredFile{
		testStoredFile(prefix+"file_a", "a.txt", "assistants", "aaaaa", 1712059200),
		testStoredFile(prefix+"file_b", "b.txt", "assistants", "bbbbb", 1712059201),
		testStoredFile(prefix+"file_c", "c.txt", "fine-tune", "ccccc", 1712059202),
	}
	for _, file := range files {
		require.NoError(t, store.SaveFile(ctx, file))
	}

	sidecarFile, err := store.SQLiteSidecar().GetFile(ctx, files[0].ID)
	require.NoError(t, err)
	require.Equal(t, files[0].Content, sidecarFile.Content)

	firstPage, err := store.ListFiles(ctx, domain.ListFilesQuery{
		Purpose: "assistants",
		Limit:   1,
		Order:   domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, firstPage.Files, 1)
	require.Equal(t, files[0].ID, firstPage.Files[0].ID)
	require.Empty(t, firstPage.Files[0].Content)
	require.True(t, firstPage.HasMore)

	secondPage, err := store.ListFiles(ctx, domain.ListFilesQuery{
		Purpose: "assistants",
		After:   files[0].ID,
		Limit:   1,
		Order:   domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, secondPage.Files, 1)
	require.Equal(t, files[1].ID, secondPage.Files[0].ID)
	require.Empty(t, secondPage.Files[0].Content)
	require.False(t, secondPage.HasMore)

	descPage, err := store.ListFiles(ctx, domain.ListFilesQuery{
		Purpose: "assistants",
		After:   files[1].ID,
		Limit:   1,
		Order:   domain.ListOrderDesc,
	})
	require.NoError(t, err)
	require.Len(t, descPage.Files, 1)
	require.Equal(t, files[0].ID, descPage.Files[0].ID)
	require.False(t, descPage.HasMore)

	_, err = store.ListFiles(ctx, domain.ListFilesQuery{
		Purpose: "assistants",
		After:   prefix + "missing_file",
		Limit:   1,
		Order:   domain.ListOrderAsc,
	})
	require.ErrorIs(t, err, storage.ErrNotFound)

	require.NoError(t, store.DeleteFile(ctx, files[0].ID))
	_, err = store.GetFile(ctx, files[0].ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = store.SQLiteSidecar().GetFile(ctx, files[0].ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestStoreVectorStoreLexicalLifecycleAndPagination(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	fileNeedle := testStoredFile(prefix+"file_needle", "needle.txt", "assistants", "local retrieval keyword orionpepper code 777", 1712059300)
	fileDecoy := testStoredFile(prefix+"file_decoy", "decoy.txt", "assistants", "ordinary notes without the lookup token", 1712059301)
	require.NoError(t, store.SaveFile(ctx, fileNeedle))
	require.NoError(t, store.SaveFile(ctx, fileDecoy))

	vectorStoreA := testVectorStore(prefix+"vs_a", "Lexical A", 1712059302)
	vectorStoreB := testVectorStore(prefix+"vs_b", "Lexical B", 1712059303)
	require.NoError(t, store.SaveVectorStore(ctx, vectorStoreA))
	require.NoError(t, store.SaveVectorStore(ctx, vectorStoreB))

	storePage, err := store.ListVectorStores(ctx, domain.ListVectorStoresQuery{
		After: vectorStoreA.ID,
		Limit: 1,
		Order: domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, storePage.VectorStores, 1)
	require.Equal(t, vectorStoreB.ID, storePage.VectorStores[0].ID)
	require.False(t, storePage.HasMore)

	attachedNeedle, err := store.AttachFileToVectorStore(
		ctx,
		vectorStoreA.ID,
		fileNeedle.ID,
		map[string]any{"topic": "docs", "priority": float64(1)},
		domain.DefaultFileChunkingStrategy(),
		1712059304,
	)
	require.NoError(t, err)
	require.Equal(t, "completed", attachedNeedle.Status)

	attachedDecoy, err := store.AttachFileToVectorStore(
		ctx,
		vectorStoreA.ID,
		fileDecoy.ID,
		map[string]any{"topic": "misc"},
		domain.DefaultFileChunkingStrategy(),
		1712059305,
	)
	require.NoError(t, err)
	require.Equal(t, "completed", attachedDecoy.Status)

	hydrated, err := store.GetVectorStore(ctx, vectorStoreA.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", hydrated.Status)
	require.Equal(t, 2, hydrated.FileCounts.Completed)
	require.Equal(t, 2, hydrated.FileCounts.Total)
	require.Equal(t, fileNeedle.Bytes+fileDecoy.Bytes, hydrated.UsageBytes)

	filePage, err := store.ListVectorStoreFiles(ctx, domain.ListVectorStoreFilesQuery{
		VectorStoreID: vectorStoreA.ID,
		Filter:        "completed",
		Limit:         1,
		Order:         domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, filePage.Files, 1)
	require.Equal(t, fileNeedle.ID, filePage.Files[0].ID)
	require.True(t, filePage.HasMore)

	nextFilePage, err := store.ListVectorStoreFiles(ctx, domain.ListVectorStoreFilesQuery{
		VectorStoreID: vectorStoreA.ID,
		After:         fileNeedle.ID,
		Filter:        "completed",
		Limit:         1,
		Order:         domain.ListOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, nextFilePage.Files, 1)
	require.Equal(t, fileDecoy.ID, nextFilePage.Files[0].ID)
	require.False(t, nextFilePage.HasMore)

	searchPage, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStoreA.ID,
		Queries:        []string{"orionpepper"},
		MaxNumResults:  10,
		RawSearchQuery: "orionpepper",
	})
	require.NoError(t, err)
	require.Len(t, searchPage.Results, 1)
	require.Equal(t, fileNeedle.ID, searchPage.Results[0].FileID)
	require.Contains(t, searchPage.Results[0].Content[0].Text, "orionpepper")

	require.NoError(t, store.DeleteVectorStoreFile(ctx, vectorStoreA.ID, fileNeedle.ID))
	afterDelete, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStoreA.ID,
		Queries:        []string{"orionpepper"},
		MaxNumResults:  10,
		RawSearchQuery: "orionpepper",
	})
	require.NoError(t, err)
	require.Empty(t, afterDelete.Results)

	require.NoError(t, store.DeleteVectorStore(ctx, vectorStoreB.ID))
	_, err = store.GetVectorStore(ctx, vectorStoreB.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestStoreAttachBinaryFileToVectorStoreFailsIndexing(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	file := domain.StoredFile{
		ID:        prefix + "file_binary",
		Filename:  "binary.bin",
		Purpose:   "assistants",
		Bytes:     3,
		CreatedAt: 1712059400,
		Status:    "processed",
		Content:   []byte{0xff, 0xfe, 0xfd},
	}
	require.NoError(t, store.SaveFile(ctx, file))

	vectorStore := testVectorStore(prefix+"vs_binary", "Binary", 1712059401)
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))

	attached, err := store.AttachFileToVectorStore(
		ctx,
		vectorStore.ID,
		file.ID,
		map[string]any{},
		domain.DefaultFileChunkingStrategy(),
		1712059402,
	)
	require.NoError(t, err)
	require.Equal(t, "failed", attached.Status)
	require.NotNil(t, attached.LastError)
	require.Equal(t, "unsupported_file", attached.LastError.Code)
	require.Zero(t, attached.UsageBytes)

	searchPage, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"anything"},
		MaxNumResults:  10,
		RawSearchQuery: "anything",
	})
	require.NoError(t, err)
	require.Empty(t, searchPage.Results)
}

func TestStorePGVectorSemanticAndHybridSearch(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendPGVector, pgvectorTestEmbedder{})

	caps := store.RetrievalIndexCapabilities()
	require.True(t, caps.SemanticSearch)
	require.True(t, caps.HybridSearch)
	require.False(t, caps.LocalRerank)
	require.False(t, caps.LazyRepair)

	fileSemantic := testStoredFile(prefix+"file_semantic", "semantic.txt", "assistants", "semanticwinner banana orchard notes", 1712059500)
	fileLexical := testStoredFile(prefix+"file_lexical", "lexical.txt", "assistants", "banana nutrition facts nutrition calories", 1712059501)
	require.NoError(t, store.SaveFile(ctx, fileSemantic))
	require.NoError(t, store.SaveFile(ctx, fileLexical))

	vectorStore := testVectorStore(prefix+"vs_pgvector", "PGVector", 1712059502)
	require.NoError(t, store.SaveVectorStore(ctx, vectorStore))
	_, err := store.AttachFileToVectorStore(ctx, vectorStore.ID, fileSemantic.ID, map[string]any{"kind": "semantic"}, domain.DefaultFileChunkingStrategy(), 1712059503)
	require.NoError(t, err)
	_, err = store.AttachFileToVectorStore(ctx, vectorStore.ID, fileLexical.ID, map[string]any{"kind": "lexical"}, domain.DefaultFileChunkingStrategy(), 1712059504)
	require.NoError(t, err)

	page, err := store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"banana nutrition"},
		MaxNumResults:  5,
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileSemantic.ID, page.Results[0].FileID)

	page, err = store.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID: vectorStore.ID,
		Queries:       []string{"banana nutrition"},
		MaxNumResults: 5,
		HybridSearch: &domain.VectorStoreHybridSearchOptions{
			EmbeddingWeight: 1,
			TextWeight:      10,
		},
		RawSearchQuery: "banana nutrition",
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.Results)
	require.Equal(t, fileLexical.ID, page.Results[0].FileID)
}

func openPostgresTestStore(t *testing.T, indexBackend string, embedder retrieval.Embedder) (*Store, context.Context, string) {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run Postgres storage tests")
	}

	ctx := context.Background()
	adminDB, schema, scopedDSN := createPostgresTestSchema(t, ctx, dsn)
	options := OpenOptions{
		SQLitePath: filepath.Join(t.TempDir(), "sidecar.db"),
		Retrieval:  retrieval.Config{IndexBackend: indexBackend},
		Embedder:   embedder,
	}
	if indexBackend == retrieval.IndexBackendPGVector {
		options.Retrieval.Embedder = retrieval.EmbedderConfig{
			Backend: retrieval.EmbedderBackendOpenAICompatible,
			Model:   "pgtest-embedding",
		}
	}

	store, err := OpenWithOptions(ctx, scopedDSN, options)
	require.NoError(t, err)
	require.NoError(t, store.PingContext(ctx))

	prefix := postgresTestPrefix(t)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
		_, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quotePostgresIdent(schema)+` CASCADE`)
		require.NoError(t, err)
		require.NoError(t, adminDB.Close())
	})
	return store, ctx, prefix
}

func createPostgresTestSchema(t *testing.T, ctx context.Context, dsn string) (*sql.DB, string, string) {
	t.Helper()

	adminDB, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))

	schema := "pgtest_" + fmt.Sprintf("%d", time.Now().UnixNano())
	_, err = adminDB.ExecContext(ctx, `CREATE SCHEMA `+quotePostgresIdent(schema))
	require.NoError(t, err)
	return adminDB, schema, postgresDSNWithSearchPath(dsn, schema)
}

func postgresDSNWithSearchPath(dsn, schema string) string {
	parsed, err := url.Parse(dsn)
	if err == nil && parsed.Scheme != "" {
		q := parsed.Query()
		q.Set("search_path", schema+",public")
		parsed.RawQuery = q.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema + ",public"
}

func quotePostgresIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func postgresTestPrefix(t *testing.T) string {
	t.Helper()
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, t.Name())
	if len(name) > 48 {
		name = name[:48]
	}
	return fmt.Sprintf("pgtest_%s_%d_", name, time.Now().UnixNano())
}

func testStoredFile(id, filename, purpose, content string, createdAt int64) domain.StoredFile {
	return domain.StoredFile{
		ID:        id,
		Filename:  filename,
		Purpose:   purpose,
		Bytes:     int64(len(content)),
		CreatedAt: createdAt,
		Status:    "processed",
		Content:   []byte(content),
	}
}

func testVectorStore(id, name string, createdAt int64) domain.StoredVectorStore {
	return domain.StoredVectorStore{
		ID:           id,
		Name:         name,
		Metadata:     map[string]string{"scope": "postgres-hardening"},
		CreatedAt:    createdAt,
		LastActiveAt: createdAt,
	}
}
