package main

import (
	"context"
	"log/slog"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage"
)

func startStorageMaintenanceCleanupLoop(ctx context.Context, logger *slog.Logger, store storage.MaintenanceStore, interval time.Duration) {
	if interval <= 0 || store == nil {
		return
	}

	go func() {
		runStorageMaintenanceCleanupSweep(ctx, logger, store)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runStorageMaintenanceCleanupSweep(ctx, logger, store)
			}
		}
	}()
}

func runStorageMaintenanceCleanupSweep(ctx context.Context, logger *slog.Logger, store storage.MaintenanceStore) {
	stats, err := store.CleanupExpiredState(ctx, domain.NowUTC().Unix())
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
	)
}
