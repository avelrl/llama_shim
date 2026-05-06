package storage

import (
	"context"
	"strings"
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
	GovernancePurge(ctx context.Context, options GovernancePurgeOptions) (GovernancePurgeReport, error)
	Optimize(ctx context.Context) error
	Vacuum(ctx context.Context) error
	BackupTo(ctx context.Context, path string) error
}

const (
	GovernancePurgeScopeAllLocalState = "all_local_state"
)

type GovernancePurgeOptions struct {
	Scope     string
	DryRun    bool
	BatchSize int
	StartedAt string
}

type GovernancePurgeReport struct {
	Object      string                        `json:"object"`
	Backend     string                        `json:"backend"`
	Scope       string                        `json:"scope"`
	DryRun      bool                          `json:"dry_run"`
	Applied     bool                          `json:"applied"`
	BatchSize   int                           `json:"batch_size"`
	StartedAt   string                        `json:"started_at,omitempty"`
	CompletedAt string                        `json:"completed_at,omitempty"`
	Primary     GovernancePurgeSection        `json:"primary"`
	Sidecar     *GovernancePurgeSidecarReport `json:"sqlite_sidecar,omitempty"`
	OutOfScope  []string                      `json:"out_of_scope"`
}

type GovernancePurgeSidecarReport struct {
	Included bool                   `json:"included"`
	Reason   string                 `json:"reason,omitempty"`
	Section  GovernancePurgeSection `json:"section"`
}

type GovernancePurgeSection struct {
	MatchedTotal int64                        `json:"matched_total"`
	DeletedTotal int64                        `json:"deleted_total"`
	Tables       []GovernancePurgeTableReport `json:"tables"`
}

type GovernancePurgeTableReport struct {
	Name    string `json:"name"`
	Matched int64  `json:"matched"`
	Deleted int64  `json:"deleted"`
}

func NormalizeMaintenanceCleanupPolicy(policy ...MaintenanceCleanupPolicy) MaintenanceCleanupPolicy {
	if len(policy) == 0 {
		return MaintenanceCleanupPolicy{}
	}
	return policy[0]
}

func NormalizeGovernancePurgeOptions(options GovernancePurgeOptions) GovernancePurgeOptions {
	options.Scope = strings.TrimSpace(options.Scope)
	if options.Scope == "" {
		options.Scope = GovernancePurgeScopeAllLocalState
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 500
	}
	return options
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
