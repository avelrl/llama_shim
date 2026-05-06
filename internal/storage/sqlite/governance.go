package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"llama_shim/internal/storage"
)

var sqliteGovernancePurgeTables = []string{
	"code_interpreter_container_files",
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
	"code_interpreter_sessions",
}

func (s *Store) GovernancePurge(ctx context.Context, options storage.GovernancePurgeOptions) (storage.GovernancePurgeReport, error) {
	options = storage.NormalizeGovernancePurgeOptions(options)
	report := storage.GovernancePurgeReport{
		Object:    "governance.purge_report",
		Backend:   storage.BackendSQLite,
		Scope:     options.Scope,
		DryRun:    options.DryRun,
		Applied:   !options.DryRun,
		BatchSize: options.BatchSize,
		StartedAt: options.StartedAt,
		OutOfScope: []string{
			"debug logs and request/response body capture files",
			"eval and smoke-test artifacts under ignored artifact directories",
			"operator-created SQLite/Postgres backups and PITR archives",
			"upstream-provider state already transmitted outside the shim",
		},
	}
	if options.Scope != storage.GovernancePurgeScopeAllLocalState {
		return report, fmt.Errorf("unsupported governance purge scope %q", options.Scope)
	}

	section, err := s.governancePurgeTables(ctx, sqliteGovernancePurgeTables, options)
	report.Primary = section
	report.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return report, err
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
	quoted := quoteSQLiteIdentifier(table)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoted).Scan(&report.Matched); err != nil {
		return report, fmt.Errorf("count sqlite governance purge table %s: %w", table, err)
	}
	if options.DryRun || report.Matched == 0 {
		return report, nil
	}
	for {
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s LIMIT ?)`,
			quoted,
			quoted,
		), options.BatchSize)
		if err != nil {
			return report, fmt.Errorf("purge sqlite table %s: %w", table, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return report, fmt.Errorf("count sqlite governance purge table %s rows affected: %w", table, err)
		}
		if affected == 0 {
			break
		}
		report.Deleted += affected
	}
	return report, nil
}

func quoteSQLiteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
