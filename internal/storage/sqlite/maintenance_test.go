package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage"
	"llama_shim/internal/storage/sqlite"
	"llama_shim/internal/testutil"
)

func TestStoreCleanupExpiredState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	now := int64(1_742_000_000)

	expiredFile := domain.StoredFile{
		ID:        "file_expired_standalone",
		Purpose:   "assistants",
		Filename:  "expired.txt",
		Bytes:     10,
		CreatedAt: now - 100,
		ExpiresAt: int64Ptr(now - 1),
		Status:    "processed",
		Content:   []byte("expired standalone"),
	}
	expiredAttachedFile := domain.StoredFile{
		ID:        "file_expired_attached",
		Purpose:   "assistants",
		Filename:  "expired-attached.txt",
		Bytes:     20,
		CreatedAt: now - 90,
		ExpiresAt: int64Ptr(now - 5),
		Status:    "processed",
		Content:   []byte("expired attached"),
	}
	activeFile := domain.StoredFile{
		ID:        "file_active",
		Purpose:   "assistants",
		Filename:  "active.txt",
		Bytes:     30,
		CreatedAt: now - 80,
		Status:    "processed",
		Content:   []byte("still active"),
	}
	require.NoError(t, store.SaveFile(ctx, expiredFile))
	require.NoError(t, store.SaveFile(ctx, expiredAttachedFile))
	require.NoError(t, store.SaveFile(ctx, activeFile))

	expiredVectorStore := domain.StoredVectorStore{
		ID:           "vs_expired",
		Name:         "Expired Store",
		Metadata:     map[string]string{},
		CreatedAt:    now - 70,
		LastActiveAt: now - 70,
		ExpiresAt:    int64Ptr(now - 2),
	}
	activeVectorStore := domain.StoredVectorStore{
		ID:           "vs_active",
		Name:         "Active Store",
		Metadata:     map[string]string{},
		CreatedAt:    now - 60,
		LastActiveAt: now - 60,
	}
	require.NoError(t, store.SaveVectorStore(ctx, expiredVectorStore))
	require.NoError(t, store.SaveVectorStore(ctx, activeVectorStore))
	_, err := store.AttachFileToVectorStore(ctx, expiredVectorStore.ID, activeFile.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), now-50)
	require.NoError(t, err)
	_, err = store.AttachFileToVectorStore(ctx, activeVectorStore.ID, expiredAttachedFile.ID, map[string]any{}, domain.DefaultFileChunkingStrategy(), now-40)
	require.NoError(t, err)

	stats, err := store.CleanupExpiredState(ctx, now)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ExpiredVectorStoresDeleted)
	require.Equal(t, 2, stats.ExpiredFilesDeleted)

	_, err = store.GetVectorStore(ctx, expiredVectorStore.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)
	_, err = store.GetFile(ctx, expiredFile.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)
	_, err = store.GetFile(ctx, expiredAttachedFile.ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	gotActiveFile, err := store.GetFile(ctx, activeFile.ID)
	require.NoError(t, err)
	require.Equal(t, activeFile.ID, gotActiveFile.ID)

	gotActiveStore, err := store.GetVectorStore(ctx, activeVectorStore.ID)
	require.NoError(t, err)
	require.Equal(t, 0, gotActiveStore.FileCounts.Total)
}

func TestStoreCleanupResponseReplayArtifactsRetentionPolicy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	now := int64(1_777_000_000)

	oldStandalone := testStoredResponseWithReplay("resp_old", "2026-04-01T10:00:00Z", "")
	recentStandalone := testStoredResponseWithReplay("resp_recent", "2026-05-05T10:00:00Z", "")
	oldConversation := testStoredResponseWithReplay("resp_conv_old", "2026-04-01T09:00:00Z", "conv_keep")
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

func TestStoreBackupRestoreOptimizeAndVacuum(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := testutil.TempDBPath(t)
	store, err := sqlite.Open(ctx, dbPath)
	require.NoError(t, err)

	file := domain.StoredFile{
		ID:        "file_backup",
		Purpose:   "assistants",
		Filename:  "backup.txt",
		Bytes:     12,
		CreatedAt: 1_742_000_100,
		Status:    "processed",
		Content:   []byte("backup content"),
	}
	require.NoError(t, store.SaveFile(ctx, file))
	require.NoError(t, store.Optimize(ctx))
	require.NoError(t, store.Vacuum(ctx))

	backupPath := filepath.Join(t.TempDir(), "shim-backup.db")
	require.NoError(t, store.BackupTo(ctx, backupPath))
	require.NoError(t, store.Close())

	restoredPath := filepath.Join(t.TempDir(), "restored", "shim.db")
	require.NoError(t, sqlite.RestoreFromBackup(restoredPath, backupPath))
	_, err = os.Stat(restoredPath)
	require.NoError(t, err)

	restored, err := sqlite.Open(ctx, restoredPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, restored.Close())
	})

	got, err := restored.GetFile(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, file.Filename, got.Filename)
	require.Equal(t, string(file.Content), string(got.Content))
}

func int64Ptr(v int64) *int64 {
	return &v
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
