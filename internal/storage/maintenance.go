package storage

import (
	"context"
	"time"
)

const responseReplayArtifactRetentionTimeFormat = "2006-01-02T15:04:05.000000000Z"

type MaintenanceCleanupPolicy struct {
	ResponseReplayArtifactsMaxAge       time.Duration
	ResponseReplayArtifactsMaxResponses int
}

type MaintenanceCleanupStats struct {
	ExpiredFilesDeleted                   int
	ExpiredVectorStoresDeleted            int
	ResponseReplayArtifactsDeleted        int
	ResponseReplayArtifactResponsesPruned int
}

func (s MaintenanceCleanupStats) TotalDeleted() int {
	return s.ExpiredFilesDeleted + s.ExpiredVectorStoresDeleted + s.ResponseReplayArtifactsDeleted
}

type MaintenanceStore interface {
	HealthStore
	CleanupExpiredState(ctx context.Context, now int64, policy ...MaintenanceCleanupPolicy) (MaintenanceCleanupStats, error)
	Optimize(ctx context.Context) error
	Vacuum(ctx context.Context) error
	BackupTo(ctx context.Context, path string) error
}

func NormalizeMaintenanceCleanupPolicy(policy ...MaintenanceCleanupPolicy) MaintenanceCleanupPolicy {
	if len(policy) == 0 {
		return MaintenanceCleanupPolicy{}
	}
	return policy[0]
}

func (p MaintenanceCleanupPolicy) ResponseReplayArtifactsAgeCutoff(now int64) (string, bool) {
	if p.ResponseReplayArtifactsMaxAge <= 0 {
		return "", false
	}
	return time.Unix(now, 0).UTC().Add(-p.ResponseReplayArtifactsMaxAge).Format(responseReplayArtifactRetentionTimeFormat), true
}

func (p MaintenanceCleanupPolicy) ResponseReplayArtifactsCountRetentionEnabled() bool {
	return p.ResponseReplayArtifactsMaxResponses > 0
}
