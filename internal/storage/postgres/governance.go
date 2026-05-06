package postgres

import (
	"context"
	"fmt"
	"time"

	"llama_shim/internal/storage"
)

var postgresGovernancePurgeTables = []string{
	"response_replay_artifacts",
	"conversation_items",
	"chat_completion_messages",
	"vector_store_chunks",
	"vector_store_files",
	"responses",
	"conversations",
	"chat_completions",
	"vector_stores",
	"files",
}

func (s *Store) GovernancePurge(ctx context.Context, options storage.GovernancePurgeOptions) (storage.GovernancePurgeReport, error) {
	options = storage.NormalizeGovernancePurgeOptions(options)
	report := storage.GovernancePurgeReport{
		Object:    "governance.purge_report",
		Backend:   storage.BackendPostgres,
		Scope:     options.Scope,
		DryRun:    options.DryRun,
		Applied:   !options.DryRun,
		BatchSize: options.BatchSize,
		StartedAt: options.StartedAt,
		OutOfScope: []string{
			"debug logs and request/response body capture files",
			"eval and smoke-test artifacts under ignored artifact directories",
			"operator-created Postgres logical backups, cluster backups, and PITR archives",
			"upstream-provider state already transmitted outside the shim",
		},
	}
	if options.Scope != storage.GovernancePurgeScopeAllLocalState {
		return report, fmt.Errorf("unsupported governance purge scope %q", options.Scope)
	}

	primary, err := s.governancePurgeTables(ctx, postgresGovernancePurgeTables, options)
	report.Primary = primary
	if err != nil {
		report.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return report, err
	}

	if s.Store != nil {
		sidecarReport, sidecarErr := s.Store.GovernancePurge(ctx, options)
		report.Sidecar = &storage.GovernancePurgeSidecarReport{
			Included: true,
			Reason:   "postgres mode uses the configured SQLite sidecar for file mirrors and instance-local code-interpreter runtime state",
			Section:  sidecarReport.Primary,
		}
		if sidecarErr != nil {
			report.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return report, fmt.Errorf("purge sqlite sidecar: %w", sidecarErr)
		}
	}
	report.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return report, nil
}

func (s *Store) governancePurgeTables(ctx context.Context, tables []string, options storage.GovernancePurgeOptions) (storage.GovernancePurgeSection, error) {
	section := storage.GovernancePurgeSection{
		Tables: make([]storage.GovernancePurgeTableReport, 0, len(tables)),
	}
	for _, table := range tables {
		tableReport, err := s.governancePurgeTable(ctx, table, options)
		section.Tables = append(section.Tables, tableReport)
		section.MatchedTotal += tableReport.Matched
		section.DeletedTotal += tableReport.Deleted
		if err != nil {
			return section, err
		}
	}
	return section, nil
}

func (s *Store) governancePurgeTable(ctx context.Context, table string, options storage.GovernancePurgeOptions) (storage.GovernancePurgeTableReport, error) {
	report := storage.GovernancePurgeTableReport{Name: table}
	quoted := quotePostgresIdentifier(table)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoted).Scan(&report.Matched); err != nil {
		return report, fmt.Errorf("count postgres governance purge table %s: %w", table, err)
	}
	if options.DryRun || report.Matched == 0 {
		return report, nil
	}
	for {
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE ctid IN (SELECT ctid FROM %s LIMIT $1)`,
			quoted,
			quoted,
		), options.BatchSize)
		if err != nil {
			return report, fmt.Errorf("purge postgres table %s: %w", table, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return report, fmt.Errorf("count postgres governance purge table %s rows affected: %w", table, err)
		}
		if affected == 0 {
			break
		}
		report.Deleted += affected
	}
	return report, nil
}
