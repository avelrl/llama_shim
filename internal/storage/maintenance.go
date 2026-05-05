package storage

import "context"

type MaintenanceCleanupStats struct {
	ExpiredFilesDeleted        int
	ExpiredVectorStoresDeleted int
}

func (s MaintenanceCleanupStats) TotalDeleted() int {
	return s.ExpiredFilesDeleted + s.ExpiredVectorStoresDeleted
}

type MaintenanceStore interface {
	HealthStore
	CleanupExpiredState(ctx context.Context, now int64) (MaintenanceCleanupStats, error)
	Optimize(ctx context.Context) error
	Vacuum(ctx context.Context) error
	BackupTo(ctx context.Context, path string) error
}
