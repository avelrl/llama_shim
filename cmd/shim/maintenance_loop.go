package main

import (
	"context"
	"log/slog"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/sqlite"
)

func startSQLiteMaintenanceCleanupLoop(ctx context.Context, logger *slog.Logger, store *sqlite.Store, interval time.Duration) {
	if interval <= 0 || store == nil {
		return
	}

	go func() {
		runSQLiteMaintenanceCleanupSweep(ctx, logger, store)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSQLiteMaintenanceCleanupSweep(ctx, logger, store)
			}
		}
	}()
}

func runSQLiteMaintenanceCleanupSweep(ctx context.Context, logger *slog.Logger, store *sqlite.Store) {
	stats, err := store.CleanupExpiredState(ctx, domain.NowUTC().Unix())
	if err != nil {
		if logger != nil {
			logger.Warn("sqlite maintenance cleanup sweep failed", "err", err)
		}
		return
	}
	if stats.TotalDeleted() == 0 || logger == nil {
		return
	}
	logger.Info(
		"sqlite maintenance cleanup sweep completed",
		"expired_vector_stores_deleted", stats.ExpiredVectorStoresDeleted,
		"expired_files_deleted", stats.ExpiredFilesDeleted,
	)
}
