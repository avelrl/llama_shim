package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama_shim/internal/config"
	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
	"llama_shim/internal/storage/postgres"
	"llama_shim/internal/storage/sqlite"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "shimctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	root := flag.NewFlagSet("shimctl", flag.ContinueOnError)
	root.SetOutput(stderr)
	configPath := root.String("config", "", "path to shared YAML config file")
	if err := root.Parse(args); err != nil {
		return err
	}

	rest := root.Args()
	if len(rest) == 0 {
		printUsage(stderr)
		return errors.New("maintenance command is required")
	}

	cfg, err := config.LoadShimctl(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	switch rest[0] {
	case "cleanup":
		return runCleanup(cfg, stdout)
	case "codex":
		return runCodex(cfg, rest[1:], stdout, stderr)
	case "optimize":
		return runOptimize(cfg, stdout)
	case "vacuum":
		return runVacuum(cfg, stdout)
	case "probe":
		return runProbe(cfg, rest[1:], stdout, stderr)
	case "backup":
		return runBackup(cfg, rest[1:], stdout, stderr)
	case "restore":
		return runRestore(cfg, rest[1:], stdout, stderr)
	case "migrate":
		return runMigrate(cfg, rest[1:], stdout, stderr)
	case "governance":
		return runGovernance(cfg, rest[1:], stdout, stderr)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown maintenance command %q", rest[0])
	}
}

func runCleanup(cfg config.ShimctlConfig, stdout io.Writer) error {
	store, err := openMaintenanceStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	stats, err := store.CleanupExpiredState(context.Background(), retrievalNowUnix(), cfg.MaintenanceCleanupPolicy())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		stdout,
		"cleanup completed: expired_vector_stores_deleted=%d expired_files_deleted=%d response_replay_artifact_responses_pruned=%d response_replay_artifacts_deleted=%d\n",
		stats.ExpiredVectorStoresDeleted,
		stats.ExpiredFilesDeleted,
		stats.ResponseReplayArtifactResponsesPruned,
		stats.ResponseReplayArtifactsDeleted,
	)
	return err
}

func runOptimize(cfg config.ShimctlConfig, stdout io.Writer) error {
	store, err := openMaintenanceStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Optimize(context.Background()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "optimize completed")
	return err
}

func runVacuum(cfg config.ShimctlConfig, stdout io.Writer) error {
	store, err := openMaintenanceStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Vacuum(context.Background()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "vacuum completed")
	return err
}

func runBackup(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", "", "path to write the backup SQLite file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return errors.New("backup requires -out")
	}

	store, err := openMaintenanceStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.BackupTo(context.Background(), *outPath); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "backup completed: %s\n", *outPath)
	return err
}

func runRestore(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fromPath := fs.String("from", "", "path to the backup file to restore from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromPath == "" {
		return errors.New("restore requires -from")
	}

	switch cfg.StorageBackend {
	case config.StorageBackendSQLite:
		if err := sqlite.RestoreFromBackup(cfg.SQLitePath, *fromPath); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "restore completed: %s\n", cfg.SQLitePath)
		return err
	case config.StorageBackendPostgres:
		store, err := openPostgresMaintenanceStore(cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.RestoreFromBackup(context.Background(), *fromPath); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, "restore completed: backend=postgres")
		return err
	default:
		return fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}

func runMigrate(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("migrate requires a migration name")
	}
	switch args[0] {
	case "sqlite-to-postgres":
		return runMigrateSQLiteToPostgres(cfg, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown migration %q", args[0])
	}
}

func runGovernance(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("governance requires a command")
	}
	switch args[0] {
	case "purge":
		return runGovernancePurge(cfg, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown governance command %q", args[0])
	}
}

func runGovernancePurge(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("governance purge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "purge all local shim-owned state for the active storage backend")
	dryRun := fs.Bool("dry-run", false, "report affected rows without deleting; this is also the default unless -apply is set")
	apply := fs.Bool("apply", false, "delete matching local state")
	confirm := fs.String("confirm", "", `required with -apply; must be "purge-all-local-state"`)
	batchSize := fs.Int("batch-size", 500, "maximum rows to delete per table batch")
	auditOut := fs.String("audit-out", "", "optional path for a JSON audit report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*all {
		return errors.New(`governance purge requires -all`)
	}
	if *apply && *dryRun {
		return errors.New("governance purge cannot combine -apply and -dry-run")
	}
	if *batchSize <= 0 {
		return errors.New("governance purge requires -batch-size > 0")
	}
	effectiveDryRun := true
	if *apply {
		effectiveDryRun = false
		if strings.TrimSpace(*confirm) != "purge-all-local-state" {
			return errors.New(`governance purge -apply requires -confirm purge-all-local-state`)
		}
	}

	store, err := openMaintenanceStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	report, purgeErr := store.GovernancePurge(context.Background(), storage.GovernancePurgeOptions{
		Scope:     storage.GovernancePurgeScopeAllLocalState,
		DryRun:    effectiveDryRun,
		BatchSize: *batchSize,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if report.Object != "" {
		if err := writeGovernancePurgeReport(stdout, report); err != nil {
			return err
		}
		if path := strings.TrimSpace(*auditOut); path != "" {
			if err := writeGovernancePurgeReportFile(path, report); err != nil {
				return err
			}
		}
	}
	return purgeErr
}

func runMigrateSQLiteToPostgres(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate sqlite-to-postgres", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sourcePath := fs.String("sqlite", cfg.SQLitePath, "source SQLite database path")
	targetSidecarPath := fs.String("target-sidecar", "", "SQLite sidecar path to use while opening the target Postgres store")
	dryRun := fs.Bool("dry-run", false, "count source and target rows without writing")
	replace := fs.Bool("replace", false, "truncate Postgres-owned migration tables before copying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.PostgresDSN) == "" {
		return errors.New("migrate sqlite-to-postgres requires postgres.dsn")
	}
	source := strings.TrimSpace(*sourcePath)
	if source == "" {
		return errors.New("migrate sqlite-to-postgres requires -sqlite")
	}
	targetSidecar := strings.TrimSpace(*targetSidecarPath)
	if targetSidecar == "" {
		targetSidecar = defaultMigrationTargetSidecarPath(source)
	}
	if filepath.Clean(targetSidecar) == filepath.Clean(source) {
		return errors.New("target sidecar path must differ from the source SQLite path")
	}

	store, err := openPostgresMigrationStore(cfg, targetSidecar)
	if err != nil {
		return err
	}
	defer store.Close()

	report, err := store.MigrateSQLiteToPostgres(context.Background(), source, postgres.SQLiteMigrationOptions{
		DryRun:  *dryRun,
		Replace: *replace,
	})
	if err == nil || len(report.Tables) != 0 {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(report); encodeErr != nil {
			return fmt.Errorf("encode migration report: %w", encodeErr)
		}
	}
	return err
}

func runProbe(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", cfg.ProbeModel, "override probe model")
	probeCount := fs.Int("probe-count", cfg.ProbeCount, "number of probe requests to run")
	requestTimeout := fs.Duration("request-timeout", cfg.ProbeRequestTimeout, "per-probe timeout budget")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := llama.NewClientWithOptions(cfg.LlamaBaseURL, cfg.LlamaTimeout, llama.ClientOptions{
		MaxConcurrentRequests:         cfg.LlamaMaxConcurrentRequests,
		MaxQueueWait:                  cfg.LlamaMaxQueueWait,
		Transport:                     buildLlamaTransportOptions(cfg),
		StartupCalibrationBearerToken: cfg.ProbeBearerToken,
	})

	snapshot := client.RunStartupCalibration(context.Background(), llama.StartupCalibrationOptions{
		Enabled:              true,
		ProbeCount:           *probeCount,
		RequestTimeout:       *requestTimeout,
		Model:                *model,
		UpstreamTimeout:      cfg.LlamaTimeout,
		CurrentMaxConcurrent: cfg.LlamaMaxConcurrentRequests,
		CurrentMaxQueueWait:  cfg.LlamaMaxQueueWait,
		Progress: func(event llama.StartupCalibrationProgressEvent) {
			printProbeProgress(stderr, event)
		},
	})

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("encode probe result: %w", err)
	}
	printProbeSummary(stderr, snapshot)
	if snapshot.Status == "failed" {
		if snapshot.Error == "" {
			return errors.New("probe failed")
		}
		return errors.New(snapshot.Error)
	}
	return nil
}

func openMaintenanceStore(cfg config.ShimctlConfig) (storage.MaintenanceStore, error) {
	switch cfg.StorageBackend {
	case config.StorageBackendSQLite:
		return openSQLiteMaintenanceStore(cfg)
	case config.StorageBackendPostgres:
		return openPostgresMaintenanceStore(cfg)
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}

func openSQLiteMaintenanceStore(cfg config.ShimctlConfig) (*sqlite.Store, error) {
	ctx := context.Background()
	embedder, err := retrieval.NewEmbedder(retrieval.EmbedderConfig{
		Backend: cfg.RetrievalEmbedderBackend,
		BaseURL: cfg.RetrievalEmbedderBaseURL,
		Model:   cfg.RetrievalEmbedderModel,
	})
	if err != nil {
		return nil, fmt.Errorf("build retrieval embedder: %w", err)
	}
	store, err := sqlite.OpenWithOptions(ctx, cfg.SQLitePath, sqlite.OpenOptions{
		Retrieval: cfg.RetrievalConfig(),
		Embedder:  embedder,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return store, nil
}

func openPostgresMaintenanceStore(cfg config.ShimctlConfig) (*postgres.Store, error) {
	ctx := context.Background()
	retrievalCfg := retrieval.Config{IndexBackend: retrieval.IndexBackendLexical}
	var embedder retrieval.Embedder
	if cfg.RetrievalIndexBackend == retrieval.IndexBackendPGVector && cfg.RetrievalPGVectorANNEnabled {
		retrievalCfg = cfg.RetrievalConfig()
		embedder = maintenanceOnlyEmbedder{}
	}
	store, err := postgres.OpenWithOptions(ctx, cfg.PostgresDSN, postgres.OpenOptions{
		SQLitePath: cfg.SQLitePath,
		Retrieval:  retrievalCfg,
		Embedder:   embedder,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return store, nil
}

func openPostgresMigrationStore(cfg config.ShimctlConfig, targetSidecarPath string) (*postgres.Store, error) {
	ctx := context.Background()
	retrievalCfg := cfg.RetrievalConfig()
	if retrievalCfg.IndexBackend != retrieval.IndexBackendPGVector {
		retrievalCfg = retrieval.Config{IndexBackend: retrieval.IndexBackendLexical}
	}
	options := postgres.OpenOptions{
		SQLitePath: targetSidecarPath,
		Retrieval:  retrievalCfg,
	}
	if retrievalCfg.IndexBackend == retrieval.IndexBackendPGVector {
		embedder, err := retrieval.NewEmbedder(options.Retrieval.Embedder)
		if err != nil {
			return nil, fmt.Errorf("build retrieval embedder: %w", err)
		}
		options.Embedder = embedder
	}
	store, err := postgres.OpenWithOptions(ctx, cfg.PostgresDSN, options)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return store, nil
}

type maintenanceOnlyEmbedder struct{}

func (maintenanceOnlyEmbedder) EmbedTexts(context.Context, []string) ([][]float32, error) {
	return nil, fmt.Errorf("maintenance-only retrieval embedder cannot embed text")
}

func defaultMigrationTargetSidecarPath(sourcePath string) string {
	dir := filepath.Dir(sourcePath)
	base := filepath.Base(sourcePath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = base
	}
	if ext == "" {
		ext = ".db"
	}
	return filepath.Join(dir, stem+".postgres-sidecar"+ext)
}

func retrievalNowUnix() int64 {
	return time.Now().UTC().Unix()
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: shimctl [-config path-to-config.yaml] <cleanup|codex|optimize|vacuum|probe|backup|restore|migrate|governance> [flags]")
}

func writeGovernancePurgeReport(w io.Writer, report storage.GovernancePurgeReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode governance purge report: %w", err)
	}
	return nil
}

func writeGovernancePurgeReportFile(path string, report storage.GovernancePurgeReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create governance audit dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create governance audit report: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if err := writeGovernancePurgeReport(file, report); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync governance audit report: %w", err)
	}
	committed = true
	return nil
}

func printProbeProgress(w io.Writer, event llama.StartupCalibrationProgressEvent) {
	if w == nil {
		return
	}

	status := "failed"
	if event.Success {
		status = "ok"
	}

	switch event.Step {
	case "models":
		_, _ = fmt.Fprintf(
			w,
			"[probe] %s %s step=models result=%s status=%d duration_ms=%d models=%d",
			event.Method,
			event.Path,
			status,
			event.StatusCode,
			event.DurationMS,
			event.ModelsCount,
		)
	case "probe":
		_, _ = fmt.Fprintf(
			w,
			"[probe] %s %s step=probe result=%s probe=%d/%d profile=%s model=%s max_tokens=%d status=%d duration_ms=%d",
			event.Method,
			event.Path,
			status,
			event.ProbeIndex,
			event.ProbeCount,
			event.ProbeProfile,
			event.Model,
			event.MaxTokens,
			event.StatusCode,
			event.DurationMS,
		)
	default:
		_, _ = fmt.Fprintf(
			w,
			"[probe] step=%s result=%s status=%d duration_ms=%d",
			event.Step,
			status,
			event.StatusCode,
			event.DurationMS,
		)
	}
	if event.ResponsePreview != "" {
		_, _ = fmt.Fprintf(w, " preview=%q", event.ResponsePreview)
	}
	if event.Error != "" {
		_, _ = fmt.Fprintf(w, " error=%q", event.Error)
	}
	_, _ = fmt.Fprintln(w)
}

func printProbeSummary(w io.Writer, snapshot llama.StartupCalibrationSnapshot) {
	if w == nil {
		return
	}

	_, _ = fmt.Fprintf(
		w,
		"[probe] finished status=%s model=%s successful_probes=%d/%d",
		snapshot.Status,
		snapshot.Model,
		snapshot.SuccessfulProbes,
		snapshot.ProbeCount,
	)
	if snapshot.ObservedLatency != nil {
		_, _ = fmt.Fprintf(
			w,
			" latency_ms[min=%d p50=%d avg=%d max=%d]",
			snapshot.ObservedLatency.Min,
			snapshot.ObservedLatency.P50,
			snapshot.ObservedLatency.Avg,
			snapshot.ObservedLatency.Max,
		)
	}
	if snapshot.Error != "" {
		_, _ = fmt.Fprintf(w, " error=%q", snapshot.Error)
	}
	_, _ = fmt.Fprintln(w)
}

func buildLlamaTransportOptions(cfg config.ShimctlConfig) llama.TransportOptions {
	return llama.TransportOptions{
		MaxIdleConns:          cfg.LlamaHTTPMaxIdleConns,
		MaxIdleConnsPerHost:   cfg.LlamaHTTPMaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.LlamaHTTPMaxConnsPerHost,
		IdleConnTimeout:       cfg.LlamaHTTPIdleConnTimeout,
		DialTimeout:           cfg.LlamaHTTPDialTimeout,
		KeepAlive:             cfg.LlamaHTTPKeepAlive,
		TLSHandshakeTimeout:   cfg.LlamaHTTPTLSHandshakeTimeout,
		ExpectContinueTimeout: cfg.LlamaHTTPExpectContinueTimeout,
	}
}
