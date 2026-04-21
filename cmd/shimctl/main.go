package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"llama_shim/internal/config"
	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
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
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown maintenance command %q", rest[0])
	}
}

func runCleanup(cfg config.ShimctlConfig, stdout io.Writer) error {
	store, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	stats, err := store.CleanupExpiredState(context.Background(), retrievalNowUnix())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		stdout,
		"cleanup completed: expired_vector_stores_deleted=%d expired_files_deleted=%d\n",
		stats.ExpiredVectorStoresDeleted,
		stats.ExpiredFilesDeleted,
	)
	return err
}

func runOptimize(cfg config.ShimctlConfig, stdout io.Writer) error {
	store, err := openStore(cfg)
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
	store, err := openStore(cfg)
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

	store, err := openStore(cfg)
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
	fromPath := fs.String("from", "", "path to the backup SQLite file to restore from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromPath == "" {
		return errors.New("restore requires -from")
	}

	if err := sqlite.RestoreFromBackup(cfg.SQLitePath, *fromPath); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "restore completed: %s\n", cfg.SQLitePath)
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

func openStore(cfg config.ShimctlConfig) (*sqlite.Store, error) {
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
		Retrieval: retrieval.Config{
			IndexBackend: cfg.RetrievalIndexBackend,
			Embedder: retrieval.EmbedderConfig{
				Backend: cfg.RetrievalEmbedderBackend,
				BaseURL: cfg.RetrievalEmbedderBaseURL,
				Model:   cfg.RetrievalEmbedderModel,
			},
		},
		Embedder: embedder,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return store, nil
}

func retrievalNowUnix() int64 {
	return time.Now().UTC().Unix()
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: shimctl [-config path-to-config.yaml] <cleanup|optimize|vacuum|probe|backup|restore> [flags]")
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
