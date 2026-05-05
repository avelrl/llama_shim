package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
	"llama_shim/internal/storage/sqlite"
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
	require.Positive(t, countPostgresVectorStoreChunks(t, store, ctx, vectorStoreA.ID, fileNeedle.ID))

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
	require.Zero(t, countPostgresVectorStoreChunks(t, store, ctx, vectorStoreA.ID, fileNeedle.ID))
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

func TestOpenWithOptionsConcurrentMigration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run Postgres storage tests")
	}

	ctx := context.Background()
	adminDB, schema, scopedDSN := createPostgresTestSchema(t, ctx, dsn)
	t.Cleanup(func() {
		_, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quotePostgresIdent(schema)+` CASCADE`)
		require.NoError(t, err)
		require.NoError(t, adminDB.Close())
	})

	const workers = 4
	sidecarRoot := t.TempDir()
	errs := make(chan error, workers)
	stores := make(chan *Store, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			store, err := OpenWithOptions(ctx, scopedDSN, OpenOptions{
				SQLitePath: filepath.Join(sidecarRoot, fmt.Sprintf("sidecar-%d.db", worker)),
				Retrieval:  retrieval.Config{IndexBackend: retrieval.IndexBackendLexical},
			})
			if err != nil {
				errs <- err
				return
			}
			stores <- store
		}(i)
	}
	wg.Wait()
	close(errs)
	close(stores)

	for err := range errs {
		require.NoError(t, err)
	}
	for store := range stores {
		require.NoError(t, store.Close())
	}

	for _, version := range []string{postgresSchemaMigrationVersion, postgresStateMigrationVersion} {
		var migrationRows int
		err := adminDB.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM `+quotePostgresIdent(schema)+`.schema_migrations WHERE version = $1`,
			version,
		).Scan(&migrationRows)
		require.NoError(t, err)
		require.Equal(t, 1, migrationRows)
	}
}

func TestStoreDurableResponsesConversationsAndChatCompletions(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	createdAt := "2026-05-05T10:00:00Z"
	completedAt := "2026-05-05T10:00:01Z"
	firstResponse := domain.StoredResponse{
		ID:                   prefix + "resp_first",
		Model:                "test-model",
		RequestJSON:          `{"input":"first"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "first")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "first")},
		Output:               []domain.Item{domain.NewOutputTextMessage("one")},
		OutputText:           "one",
		Store:                true,
		CreatedAt:            createdAt,
		CompletedAt:          completedAt,
		ResponseJSON:         `{"id":"` + prefix + `resp_first","object":"response","status":"completed","output":[]}`,
	}
	require.NoError(t, store.SaveResponse(ctx, firstResponse))
	require.NoError(t, store.SaveResponseReplayArtifacts(ctx, firstResponse.ID, []domain.ResponseReplayArtifact{
		{ResponseID: firstResponse.ID, Sequence: 2, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
		{ResponseID: firstResponse.ID, Sequence: 1, EventType: "response.created", PayloadJSON: `{"type":"response.created"}`},
	}))

	secondResponse := firstResponse
	secondResponse.ID = prefix + "resp_second"
	secondResponse.RequestJSON = `{"input":"second","previous_response_id":"` + firstResponse.ID + `"}`
	secondResponse.NormalizedInputItems = []domain.Item{domain.NewInputTextMessage("user", "second")}
	secondResponse.EffectiveInputItems = []domain.Item{domain.NewInputTextMessage("user", "first"), domain.NewOutputTextMessage("one"), domain.NewInputTextMessage("user", "second")}
	secondResponse.Output = []domain.Item{domain.NewOutputTextMessage("two")}
	secondResponse.OutputText = "two"
	secondResponse.PreviousResponseID = firstResponse.ID
	secondResponse.ResponseJSON = `{"id":"` + prefix + `resp_second","object":"response","status":"completed","output":[]}`
	require.NoError(t, store.SaveResponse(ctx, secondResponse))

	gotSecond, err := store.GetResponse(ctx, secondResponse.ID)
	require.NoError(t, err)
	require.Equal(t, secondResponse.ID, gotSecond.ID)
	require.Equal(t, firstResponse.ID, gotSecond.PreviousResponseID)
	require.Equal(t, "two", gotSecond.OutputText)

	lineage, err := store.GetResponseLineage(ctx, secondResponse.ID, 0)
	require.NoError(t, err)
	require.Len(t, lineage, 2)
	require.Equal(t, []string{firstResponse.ID, secondResponse.ID}, []string{lineage[0].ID, lineage[1].ID})

	artifacts, err := store.GetResponseReplayArtifacts(ctx, firstResponse.ID)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	require.Equal(t, []int{1, 2}, []int{artifacts[0].Sequence, artifacts[1].Sequence})

	conversation := domain.Conversation{
		ID:        prefix + "conv",
		Object:    "conversation",
		Metadata:  map[string]string{"topic": "postgres"},
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Items: []domain.Item{
			domain.NewInputTextMessage("system", "You are a test assistant."),
			domain.NewInputTextMessage("user", "Remember 777."),
		},
	}
	require.NoError(t, store.CreateConversation(ctx, conversation))

	conversationResponse := secondResponse
	conversationResponse.ID = prefix + "resp_conversation"
	conversationResponse.ConversationID = conversation.ID
	conversationResponse.PreviousResponseID = ""
	require.NoError(t, store.SaveResponseAndAppendConversation(
		ctx,
		conversation,
		conversationResponse,
		[]domain.Item{domain.NewInputTextMessage("user", "What is the code?")},
		[]domain.Item{domain.NewOutputTextMessage("777")},
	))

	gotConversation, gotItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, 2, gotConversation.Version)
	require.Equal(t, map[string]string{"topic": "postgres"}, gotConversation.Metadata)
	require.Len(t, gotItems, 4)
	require.Equal(t, []string{"seed", "seed", "response_input", "response_output"}, []string{
		gotItems[0].Source,
		gotItems[1].Source,
		gotItems[2].Source,
		gotItems[3].Source,
	})

	page, err := store.ListConversationItems(ctx, domain.ListConversationItemsQuery{
		ConversationID: conversation.ID,
		After:          gotItems[1].ID,
		Limit:          2,
		Order:          domain.ConversationItemOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Items, 2)
	require.Equal(t, "response_input", page.Items[0].Source)

	appended, err := store.AppendConversationItems(ctx, gotConversation, []domain.Item{
		domain.NewInputTextMessage("user", "append"),
	}, completedAt)
	require.NoError(t, err)
	require.Len(t, appended, 1)
	require.Equal(t, 4, appended[0].Seq)

	chat := domain.StoredChatCompletion{
		ID:           prefix + "chatcmpl",
		Model:        "test-chat-model",
		Metadata:     map[string]string{"suite": "postgres", "kind": "durable"},
		RequestJSON:  `{"model":"test-chat-model","messages":[{"role":"system","content":"Be terse."},{"role":"user","content":"hi"}]}`,
		ResponseJSON: `{"id":"` + prefix + `chatcmpl","object":"chat.completion","created":1714910000,"model":"test-chat-model","choices":[]}`,
		CreatedAt:    1714910000,
	}
	require.NoError(t, store.SaveChatCompletion(ctx, chat))

	gotChat, err := store.GetChatCompletion(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, chat.ID, gotChat.ID)
	require.Equal(t, map[string]string{"suite": "postgres", "kind": "durable"}, gotChat.Metadata)
	require.Contains(t, gotChat.ResponseJSON, `"metadata"`)

	messagePage, err := store.ListChatCompletionMessages(ctx, chat.ID, domain.ListStoredChatCompletionMessagesQuery{
		Limit: 1,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.True(t, messagePage.HasMore)
	require.Len(t, messagePage.Messages, 1)
	require.Equal(t, chat.ID+"-0", messagePage.Messages[0].ID)

	listPage, err := store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		Model:    "test-chat-model",
		Metadata: map[string]string{"suite": "postgres"},
		Limit:    1,
		Order:    domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, listPage.Completions, 1)
	require.Equal(t, chat.ID, listPage.Completions[0].ID)

	updatedChat, err := store.UpdateChatCompletionMetadata(ctx, chat.ID, map[string]string{"suite": "updated"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"suite": "updated"}, updatedChat.Metadata)
	require.Contains(t, updatedChat.ResponseJSON, `"updated"`)

	require.NoError(t, store.DeleteChatCompletion(ctx, chat.ID))
	_, err = store.GetChatCompletion(ctx, chat.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestStoreStateSharedAcrossPostgresInstances(t *testing.T) {
	primary, secondary, ctx, prefix := openPostgresTestStorePair(t, retrieval.IndexBackendLexical)

	createdAt := "2026-05-05T11:00:00Z"
	completedAt := "2026-05-05T11:00:01Z"
	response := domain.StoredResponse{
		ID:                   prefix + "resp_shared",
		Model:                "test-model",
		RequestJSON:          `{"input":"shared"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "shared")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "shared")},
		Output:               []domain.Item{domain.NewOutputTextMessage("shared-output")},
		OutputText:           "shared-output",
		Store:                true,
		CreatedAt:            createdAt,
		CompletedAt:          completedAt,
	}
	require.NoError(t, primary.SaveResponse(ctx, response))
	require.NoError(t, primary.SaveResponseReplayArtifacts(ctx, response.ID, []domain.ResponseReplayArtifact{
		{ResponseID: response.ID, Sequence: 1, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
	}))

	gotResponse, err := secondary.GetResponse(ctx, response.ID)
	require.NoError(t, err)
	require.Equal(t, response.OutputText, gotResponse.OutputText)
	artifacts, err := secondary.GetResponseReplayArtifacts(ctx, response.ID)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	conversation := domain.Conversation{
		ID:        prefix + "conv_shared",
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Items:     []domain.Item{domain.NewInputTextMessage("user", "seed")},
	}
	require.NoError(t, primary.CreateConversation(ctx, conversation))
	gotConversation, gotItems, err := secondary.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, conversation.ID, gotConversation.ID)
	require.Len(t, gotItems, 1)
	_, err = secondary.AppendConversationItems(ctx, gotConversation, []domain.Item{domain.NewInputTextMessage("user", "from secondary")}, completedAt)
	require.NoError(t, err)
	updatedConversation, updatedItems, err := primary.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, 2, updatedConversation.Version)
	require.Len(t, updatedItems, 2)

	chat := domain.StoredChatCompletion{
		ID:           prefix + "chat_shared",
		Model:        "test-chat-model",
		Metadata:     map[string]string{"shared": "true"},
		RequestJSON:  `{"model":"test-chat-model","messages":[{"role":"user","content":"hello"}]}`,
		ResponseJSON: `{"id":"` + prefix + `chat_shared","object":"chat.completion","created":1714910100,"model":"test-chat-model","choices":[]}`,
		CreatedAt:    1714910100,
	}
	require.NoError(t, primary.SaveChatCompletion(ctx, chat))
	gotChat, err := secondary.GetChatCompletion(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, chat.ID, gotChat.ID)
	messages, err := secondary.ListChatCompletionMessages(ctx, chat.ID, domain.ListStoredChatCompletionMessagesQuery{
		Limit: 10,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 1)
}

func TestStoreCodeInterpreterStateStaysSQLiteSidecarLocal(t *testing.T) {
	primary, secondary, ctx, prefix := openPostgresTestStorePair(t, retrieval.IndexBackendLexical)

	file := testStoredFile(prefix+"file_ci_sidecar", "sidecar.txt", "assistants", "sidecar content", 1712060400)
	require.NoError(t, primary.SaveFile(ctx, file))
	_, err := secondary.GetFile(ctx, file.ID)
	require.NoError(t, err)

	session := domain.CodeInterpreterSession{
		ID:                  prefix + "cntr_sidecar",
		Owner:               "owner-a",
		Backend:             "docker",
		Status:              "running",
		Name:                "Sidecar",
		MemoryLimit:         "1g",
		ExpiresAfterMinutes: 20,
		CreatedAt:           "2026-05-05T12:20:00Z",
		LastActiveAt:        "2026-05-05T12:20:00Z",
	}
	require.NoError(t, primary.SaveCodeInterpreterSession(ctx, session))
	containerFile, err := primary.SaveCodeInterpreterContainerFile(ctx, domain.CodeInterpreterContainerFile{
		ID:                prefix + "cfile_sidecar",
		ContainerID:       session.ID,
		BackingFileID:     file.ID,
		DeleteBackingFile: true,
		Path:              "/workspace/sidecar.txt",
		Source:            "generated",
		Bytes:             file.Bytes,
		CreatedAt:         file.CreatedAt,
	})
	require.NoError(t, err)

	gotSession, err := primary.GetCodeInterpreterSession(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, session.ID, gotSession.ID)
	gotContainerFile, err := primary.GetCodeInterpreterContainerFile(ctx, session.ID, containerFile.ID)
	require.NoError(t, err)
	require.Equal(t, containerFile.ID, gotContainerFile.ID)

	_, err = secondary.GetCodeInterpreterSession(ctx, session.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = secondary.GetCodeInterpreterContainerFile(ctx, session.ID, containerFile.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestStorePostgresMaintenanceCleanup(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	expiredFile := testStoredFile(prefix+"file_expired", "expired.txt", "assistants", "expired content", 1712060000)
	expiredFile.ExpiresAt = domain.Int64Ptr(1712060005)
	activeFile := testStoredFile(prefix+"file_active", "active.txt", "assistants", "active content", 1712060001)
	activeFile.ExpiresAt = domain.Int64Ptr(1712060999)
	require.NoError(t, store.SaveFile(ctx, expiredFile))
	require.NoError(t, store.SaveFile(ctx, activeFile))

	expiredVectorStore := testVectorStore(prefix+"vs_expired", "Expired", 1712060002)
	expiredVectorStore.ExpiresAt = domain.Int64Ptr(1712060005)
	activeVectorStore := testVectorStore(prefix+"vs_active", "Active", 1712060003)
	activeVectorStore.ExpiresAt = domain.Int64Ptr(1712060999)
	require.NoError(t, store.SaveVectorStore(ctx, expiredVectorStore))
	require.NoError(t, store.SaveVectorStore(ctx, activeVectorStore))
	_, err := store.AttachFileToVectorStore(ctx, expiredVectorStore.ID, expiredFile.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), 1712060004)
	require.NoError(t, err)

	stats, err := store.CleanupExpiredState(ctx, 1712060010)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ExpiredVectorStoresDeleted)
	require.Equal(t, 1, stats.ExpiredFilesDeleted)
	require.Equal(t, 2, stats.TotalDeleted())

	_, err = store.GetVectorStore(ctx, expiredVectorStore.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = store.GetFile(ctx, expiredFile.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)

	_, err = store.GetVectorStore(ctx, activeVectorStore.ID)
	require.NoError(t, err)
	_, err = store.GetFile(ctx, activeFile.ID)
	require.NoError(t, err)

	require.NoError(t, store.Optimize(ctx))
	require.NoError(t, store.Vacuum(ctx))
}

func TestStorePostgresCleanupResponseReplayArtifactsRetentionPolicy(t *testing.T) {
	store, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)
	now := int64(1_777_000_000)

	oldStandalone := testStoredResponseWithReplay(prefix+"resp_old", "2026-04-01T10:00:00Z", "")
	recentStandalone := testStoredResponseWithReplay(prefix+"resp_recent", "2026-05-05T10:00:00Z", "")
	oldConversation := testStoredResponseWithReplay(prefix+"resp_conv_old", "2026-04-01T09:00:00Z", prefix+"conv_keep")
	for _, response := range []domain.StoredResponse{oldStandalone, recentStandalone, oldConversation} {
		require.NoError(t, store.SaveResponse(ctx, response))
		require.NoError(t, store.SaveResponseReplayArtifacts(ctx, response.ID, []domain.ResponseReplayArtifact{
			{ResponseID: response.ID, Sequence: 1, EventType: "response.created", PayloadJSON: `{"type":"response.created"}`},
			{ResponseID: response.ID, Sequence: 2, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
		}))
	}

	stats, err := store.CleanupExpiredState(ctx, now, storage.MaintenanceCleanupPolicy{
		ResponseReplayArtifactsMaxAge: 24 * time.Hour,
	})
	require.NoError(t, err)
	require.Equal(t, 2, stats.ResponseReplayArtifactsDeleted)
	require.Equal(t, 1, stats.ResponseReplayArtifactResponsesPruned)

	gotOldResponse, err := store.GetResponse(ctx, oldStandalone.ID)
	require.NoError(t, err)
	require.Equal(t, oldStandalone.ID, gotOldResponse.ID)
	oldArtifacts, err := store.GetResponseReplayArtifacts(ctx, oldStandalone.ID)
	require.NoError(t, err)
	require.Empty(t, oldArtifacts)
	recentArtifacts, err := store.GetResponseReplayArtifacts(ctx, recentStandalone.ID)
	require.NoError(t, err)
	require.Len(t, recentArtifacts, 2)
	conversationArtifacts, err := store.GetResponseReplayArtifacts(ctx, oldConversation.ID)
	require.NoError(t, err)
	require.Len(t, conversationArtifacts, 2)

	require.NoError(t, store.SaveResponseReplayArtifacts(ctx, oldStandalone.ID, []domain.ResponseReplayArtifact{
		{ResponseID: oldStandalone.ID, Sequence: 1, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
	}))
	stats, err = store.CleanupExpiredState(ctx, now, storage.MaintenanceCleanupPolicy{
		ResponseReplayArtifactsMaxResponses: 1,
	})
	require.NoError(t, err)
	require.Equal(t, 1, stats.ResponseReplayArtifactsDeleted)
	require.Equal(t, 1, stats.ResponseReplayArtifactResponsesPruned)

	oldArtifacts, err = store.GetResponseReplayArtifacts(ctx, oldStandalone.ID)
	require.NoError(t, err)
	require.Empty(t, oldArtifacts)
	recentArtifacts, err = store.GetResponseReplayArtifacts(ctx, recentStandalone.ID)
	require.NoError(t, err)
	require.Len(t, recentArtifacts, 2)
	conversationArtifacts, err = store.GetResponseReplayArtifacts(ctx, oldConversation.ID)
	require.NoError(t, err)
	require.Len(t, conversationArtifacts, 2)
}

func TestStorePostgresBackupRestoreRoundTrip(t *testing.T) {
	source, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)
	target, _, _ := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	file := testStoredFile(prefix+"file_backup", "backup.txt", "assistants", "backup content keyword", 1712060100)
	require.NoError(t, source.SaveFile(ctx, file))
	vectorStore := testVectorStore(prefix+"vs_backup", "Backup", 1712060101)
	require.NoError(t, source.SaveVectorStore(ctx, vectorStore))
	_, err := source.AttachFileToVectorStore(ctx, vectorStore.ID, file.ID, map[string]any{"suite": "backup"}, domain.DefaultFileChunkingStrategy(), 1712060102)
	require.NoError(t, err)

	response := domain.StoredResponse{
		ID:                   prefix + "resp_backup",
		Model:                "test-model",
		RequestJSON:          `{"input":"backup"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "backup")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "backup")},
		Output:               []domain.Item{domain.NewOutputTextMessage("restored")},
		OutputText:           "restored",
		Store:                true,
		CreatedAt:            "2026-05-05T12:00:00Z",
		CompletedAt:          "2026-05-05T12:00:01Z",
		ResponseJSON:         `{"id":"` + prefix + `resp_backup","object":"response","status":"completed","output":[]}`,
	}
	require.NoError(t, source.SaveResponse(ctx, response))
	require.NoError(t, source.SaveResponseReplayArtifacts(ctx, response.ID, []domain.ResponseReplayArtifact{
		{ResponseID: response.ID, Sequence: 1, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
	}))

	conversation := domain.Conversation{
		ID:        prefix + "conv_backup",
		Object:    "conversation",
		Version:   1,
		Metadata:  map[string]string{"suite": "backup"},
		CreatedAt: "2026-05-05T12:00:00Z",
		UpdatedAt: "2026-05-05T12:00:00Z",
		Items:     []domain.Item{domain.NewInputTextMessage("user", "seed")},
	}
	require.NoError(t, source.CreateConversation(ctx, conversation))

	chat := domain.StoredChatCompletion{
		ID:           prefix + "chat_backup",
		Model:        "test-chat-model",
		Metadata:     map[string]string{"suite": "backup"},
		RequestJSON:  `{"model":"test-chat-model","messages":[{"role":"user","content":"hello"}]}`,
		ResponseJSON: `{"id":"` + prefix + `chat_backup","object":"chat.completion","created":1714910200,"model":"test-chat-model","choices":[]}`,
		CreatedAt:    1714910200,
	}
	require.NoError(t, source.SaveChatCompletion(ctx, chat))

	backupPath := filepath.Join(t.TempDir(), "postgres-backup.sql")
	require.NoError(t, source.BackupTo(ctx, backupPath))
	require.FileExists(t, backupPath)

	require.NoError(t, target.RestoreFromBackup(ctx, backupPath))

	gotFile, err := target.GetFile(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, file.Content, gotFile.Content)

	searchPage, err := target.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  vectorStore.ID,
		Queries:        []string{"keyword"},
		MaxNumResults:  3,
		RawSearchQuery: "keyword",
	})
	require.NoError(t, err)
	require.Len(t, searchPage.Results, 1)
	require.Equal(t, file.ID, searchPage.Results[0].FileID)

	gotResponse, err := target.GetResponse(ctx, response.ID)
	require.NoError(t, err)
	require.Equal(t, response.OutputText, gotResponse.OutputText)
	artifacts, err := target.GetResponseReplayArtifacts(ctx, response.ID)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	gotConversation, items, err := target.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"suite": "backup"}, gotConversation.Metadata)
	require.Len(t, items, 1)

	gotChat, err := target.GetChatCompletion(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"suite": "backup"}, gotChat.Metadata)
	messages, err := target.ListChatCompletionMessages(ctx, chat.ID, domain.ListStoredChatCompletionMessagesQuery{
		Limit: 10,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 1)
}

func TestStoreMigrateSQLiteToPostgres(t *testing.T) {
	target, ctx, prefix := openPostgresTestStore(t, retrieval.IndexBackendLexical, nil)

	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := sqlite.Open(ctx, sourcePath)
	require.NoError(t, err)
	fixture := seedSQLiteMigrationFixture(t, ctx, source, prefix)
	require.NoError(t, source.Close())

	dryRun, err := target.MigrateSQLiteToPostgres(ctx, sourcePath, SQLiteMigrationOptions{DryRun: true})
	require.NoError(t, err)
	require.True(t, dryRun.DryRun)
	require.False(t, dryRun.RequiresReplace)
	require.False(t, dryRun.CodeInterpreterMigrated)
	filesTable, err := migrationReportTable(dryRun, "files")
	require.NoError(t, err)
	require.Equal(t, int64(1), filesTable.SourceRows)
	chunksTable, err := migrationReportTable(dryRun, "vector_store_chunks")
	require.NoError(t, err)
	require.Positive(t, chunksTable.SourceRows)
	_, err = target.GetFile(ctx, fixture.fileID)
	require.ErrorIs(t, err, storage.ErrNotFound)

	existing := testStoredFile(prefix+"file_existing", "existing.txt", "assistants", "existing target", 1712060300)
	require.NoError(t, target.SaveFile(ctx, existing))
	nonEmpty, err := target.MigrateSQLiteToPostgres(ctx, sourcePath, SQLiteMigrationOptions{})
	require.ErrorContains(t, err, "target postgres migration tables are not empty")
	require.True(t, nonEmpty.RequiresReplace)
	_, err = target.GetFile(ctx, existing.ID)
	require.NoError(t, err)

	report, err := target.MigrateSQLiteToPostgres(ctx, sourcePath, SQLiteMigrationOptions{Replace: true})
	require.NoError(t, err)
	require.False(t, report.DryRun)
	require.True(t, report.Replace)
	require.Equal(t, report.TotalSourceRows, report.TotalCopiedRows)
	require.False(t, report.CodeInterpreterMigrated)
	_, err = target.GetFile(ctx, existing.ID)
	require.ErrorIs(t, err, storage.ErrNotFound)

	gotFile, err := target.GetFile(ctx, fixture.fileID)
	require.NoError(t, err)
	require.Equal(t, []byte("sqlite migration keyword content"), gotFile.Content)
	sidecarFile, err := target.SQLiteSidecar().GetFile(ctx, fixture.fileID)
	require.NoError(t, err)
	require.Equal(t, gotFile.Content, sidecarFile.Content)

	searchPage, err := target.SearchVectorStore(ctx, domain.VectorStoreSearchQuery{
		VectorStoreID:  fixture.vectorStoreID,
		Queries:        []string{"keyword"},
		MaxNumResults:  3,
		RawSearchQuery: "keyword",
	})
	require.NoError(t, err)
	require.Len(t, searchPage.Results, 1)
	require.Equal(t, fixture.fileID, searchPage.Results[0].FileID)

	gotResponse, err := target.GetResponse(ctx, fixture.responseID)
	require.NoError(t, err)
	require.Equal(t, "migrated response", gotResponse.OutputText)
	artifacts, err := target.GetResponseReplayArtifacts(ctx, fixture.responseID)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	gotConversation, items, err := target.GetConversation(ctx, fixture.conversationID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"suite": "sqlite-migration"}, gotConversation.Metadata)
	require.Len(t, items, 1)

	gotChat, err := target.GetChatCompletion(ctx, fixture.chatCompletionID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"suite": "sqlite-migration"}, gotChat.Metadata)
	messages, err := target.ListChatCompletionMessages(ctx, fixture.chatCompletionID, domain.ListStoredChatCompletionMessagesQuery{
		Limit: 10,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 1)

	_, err = target.GetCodeInterpreterSession(ctx, fixture.codeInterpreterSessionID)
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = target.GetCodeInterpreterContainerFile(ctx, fixture.codeInterpreterSessionID, fixture.codeInterpreterContainerFileID)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func migrationReportTable(report SQLiteMigrationReport, name string) (SQLiteMigrationTableReport, error) {
	for _, table := range report.Tables {
		if table.Name == name {
			return table, nil
		}
	}
	return SQLiteMigrationTableReport{}, fmt.Errorf("migration report table %q not found", name)
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

type sqliteMigrationFixture struct {
	fileID                         string
	vectorStoreID                  string
	responseID                     string
	conversationID                 string
	chatCompletionID               string
	codeInterpreterSessionID       string
	codeInterpreterContainerFileID string
}

func seedSQLiteMigrationFixture(t *testing.T, ctx context.Context, source *sqlite.Store, prefix string) sqliteMigrationFixture {
	t.Helper()

	file := testStoredFile(prefix+"file_sqlite_migrate", "migrate.txt", "assistants", "sqlite migration keyword content", 1712060200)
	require.NoError(t, source.SaveFile(ctx, file))
	session := domain.CodeInterpreterSession{
		ID:                  prefix + "cntr_sqlite_migrate",
		Owner:               "owner-a",
		Backend:             "docker",
		Status:              "running",
		Name:                "SQLite Migration Sidecar",
		MemoryLimit:         "1g",
		ExpiresAfterMinutes: 20,
		CreatedAt:           "2026-05-05T12:10:00Z",
		LastActiveAt:        "2026-05-05T12:10:00Z",
	}
	require.NoError(t, source.SaveCodeInterpreterSession(ctx, session))
	containerFile, err := source.SaveCodeInterpreterContainerFile(ctx, domain.CodeInterpreterContainerFile{
		ID:                prefix + "cfile_sqlite_migrate",
		ContainerID:       session.ID,
		BackingFileID:     file.ID,
		DeleteBackingFile: true,
		Path:              "/workspace/migrate.txt",
		Source:            "generated",
		Bytes:             file.Bytes,
		CreatedAt:         file.CreatedAt,
	})
	require.NoError(t, err)
	vectorStore := testVectorStore(prefix+"vs_sqlite_migrate", "SQLite Migration", 1712060201)
	require.NoError(t, source.SaveVectorStore(ctx, vectorStore))
	_, err = source.AttachFileToVectorStore(ctx, vectorStore.ID, file.ID, map[string]any{"suite": "sqlite-migration"}, domain.DefaultFileChunkingStrategy(), 1712060202)
	require.NoError(t, err)

	response := domain.StoredResponse{
		ID:                   prefix + "resp_sqlite_migrate",
		Model:                "test-model",
		RequestJSON:          `{"input":"migrate"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "migrate")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "migrate")},
		Output:               []domain.Item{domain.NewOutputTextMessage("migrated response")},
		OutputText:           "migrated response",
		Store:                true,
		CreatedAt:            "2026-05-05T12:10:00Z",
		CompletedAt:          "2026-05-05T12:10:01Z",
		ResponseJSON:         `{"id":"` + prefix + `resp_sqlite_migrate","object":"response","status":"completed","output":[]}`,
	}
	require.NoError(t, source.SaveResponse(ctx, response))
	require.NoError(t, source.SaveResponseReplayArtifacts(ctx, response.ID, []domain.ResponseReplayArtifact{
		{ResponseID: response.ID, Sequence: 1, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
	}))

	conversation := domain.Conversation{
		ID:        prefix + "conv_sqlite_migrate",
		Object:    "conversation",
		Version:   1,
		Metadata:  map[string]string{"suite": "sqlite-migration"},
		CreatedAt: "2026-05-05T12:10:00Z",
		UpdatedAt: "2026-05-05T12:10:00Z",
		Items:     []domain.Item{domain.NewInputTextMessage("user", "seed")},
	}
	require.NoError(t, source.CreateConversation(ctx, conversation))

	chat := domain.StoredChatCompletion{
		ID:           prefix + "chat_sqlite_migrate",
		Model:        "test-chat-model",
		Metadata:     map[string]string{"suite": "sqlite-migration"},
		RequestJSON:  `{"model":"test-chat-model","messages":[{"role":"user","content":"hello"}]}`,
		ResponseJSON: `{"id":"` + prefix + `chat_sqlite_migrate","object":"chat.completion","created":1714910300,"model":"test-chat-model","choices":[]}`,
		CreatedAt:    1714910300,
	}
	require.NoError(t, source.SaveChatCompletion(ctx, chat))

	return sqliteMigrationFixture{
		fileID:                         file.ID,
		vectorStoreID:                  vectorStore.ID,
		responseID:                     response.ID,
		conversationID:                 conversation.ID,
		chatCompletionID:               chat.ID,
		codeInterpreterSessionID:       session.ID,
		codeInterpreterContainerFileID: containerFile.ID,
	}
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

func openPostgresTestStorePair(t *testing.T, indexBackend string) (*Store, *Store, context.Context, string) {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run Postgres storage tests")
	}

	ctx := context.Background()
	adminDB, schema, scopedDSN := createPostgresTestSchema(t, ctx, dsn)
	sidecarRoot := t.TempDir()
	openOptions := func(name string) OpenOptions {
		return OpenOptions{
			SQLitePath: filepath.Join(sidecarRoot, name+".db"),
			Retrieval:  retrieval.Config{IndexBackend: indexBackend},
		}
	}
	primary, err := OpenWithOptions(ctx, scopedDSN, openOptions("primary"))
	require.NoError(t, err)
	secondary, err := OpenWithOptions(ctx, scopedDSN, openOptions("secondary"))
	require.NoError(t, err)
	require.NoError(t, primary.PingContext(ctx))
	require.NoError(t, secondary.PingContext(ctx))

	prefix := postgresTestPrefix(t)
	t.Cleanup(func() {
		require.NoError(t, primary.Close())
		require.NoError(t, secondary.Close())
		_, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quotePostgresIdent(schema)+` CASCADE`)
		require.NoError(t, err)
		require.NoError(t, adminDB.Close())
	})
	return primary, secondary, ctx, prefix
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

func countPostgresVectorStoreChunks(t *testing.T, store *Store, ctx context.Context, vectorStoreID, fileID string) int {
	t.Helper()
	var count int
	err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM vector_store_chunks
		WHERE vector_store_id = $1 AND file_id = $2
	`, vectorStoreID, fileID).Scan(&count)
	require.NoError(t, err)
	return count
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

func testStoredResponseWithReplay(id, createdAt, conversationID string) domain.StoredResponse {
	return domain.StoredResponse{
		ID:                   id,
		Model:                "test-model",
		RequestJSON:          `{"input":"hello"}`,
		ResponseJSON:         `{"id":"` + id + `","object":"response","status":"completed","output":[]}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "hello")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "hello")},
		Output:               []domain.Item{domain.NewOutputTextMessage("ok")},
		OutputText:           "ok",
		ConversationID:       conversationID,
		Store:                true,
		CreatedAt:            createdAt,
		CompletedAt:          createdAt,
	}
}
