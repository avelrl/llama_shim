package httpapi

import (
	"context"
	"log/slog"
	"time"

	"llama_shim/internal/domain"
)

func StartLocalCodeInterpreterCleanupLoop(ctx context.Context, logger *slog.Logger, runtime LocalCodeInterpreterRuntimeConfig, files LocalCodeInterpreterFileStore, sessions LocalCodeInterpreterSessionStore, interval time.Duration) {
	if interval <= 0 {
		return
	}

	manager := newLocalCodeInterpreterContainerManager(runtime, files, sessions)
	if !manager.enabled() {
		return
	}

	go func() {
		sweepLocalCodeInterpreterExpiredContainers(ctx, logger, manager)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepLocalCodeInterpreterExpiredContainers(ctx, logger, manager)
			}
		}
	}()
}

func sweepLocalCodeInterpreterExpiredContainers(ctx context.Context, logger *slog.Logger, manager localCodeInterpreterContainerManager) {
	after := ""
	for {
		page, err := manager.sessions.ListCodeInterpreterSessions(ctx, domain.ListCodeInterpreterSessionsQuery{
			After: after,
			Limit: maxLocalCodeInterpreterContainersListLimit,
			Order: domain.ListOrderAsc,
		})
		if err != nil {
			if logger != nil {
				logger.Warn("code interpreter cleanup sweep failed", "err", err)
			}
			return
		}
		if len(page.Sessions) == 0 {
			return
		}

		for _, session := range page.Sessions {
			wasExpired := session.Status == "running" && localCodeInterpreterContainerExpired(session)
			updated, err := manager.expireIfNeeded(ctx, session)
			if err != nil {
				if logger != nil {
					logger.Warn("code interpreter container cleanup failed", "container_id", session.ID, "err", err)
				}
				continue
			}
			if wasExpired && logger != nil {
				logger.Info("expired code interpreter container", "container_id", updated.ID, "backend", updated.Backend)
			}
		}

		if !page.HasMore {
			return
		}
		after = page.Sessions[len(page.Sessions)-1].ID
	}
}
