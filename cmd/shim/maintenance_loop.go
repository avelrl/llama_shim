package main

import (
	"context"
	"log/slog"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage"
)

func startStorageMaintenanceCleanupLoop(ctx context.Context, logger *slog.Logger, store storage.MaintenanceStore, interval time.Duration, policy storage.MaintenanceCleanupPolicy) {
	if interval <= 0 || store == nil {
		return
	}

	go func() {
		runStorageMaintenanceCleanupSweep(ctx, logger, store, policy)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runStorageMaintenanceCleanupSweep(ctx, logger, store, policy)
			}
		}
	}()
}

func runStorageMaintenanceCleanupSweep(ctx context.Context, logger *slog.Logger, store storage.MaintenanceStore, policy storage.MaintenanceCleanupPolicy) {
	stats, err := store.CleanupExpiredState(ctx, domain.NowUTC().Unix(), policy)
	if err != nil {
		if logger != nil {
			logger.Warn("storage maintenance cleanup sweep failed", "err", err)
		}
		return
	}
	if stats.TotalDeleted() == 0 || logger == nil {
		return
	}
	logger.Info(
		"storage maintenance cleanup sweep completed",
		"expired_vector_stores_deleted", stats.ExpiredVectorStoresDeleted,
		"expired_files_deleted", stats.ExpiredFilesDeleted,
		"response_replay_artifact_responses_pruned", stats.ResponseReplayArtifactResponsesPruned,
		"response_replay_artifacts_deleted", stats.ResponseReplayArtifactsDeleted,
	)
}
