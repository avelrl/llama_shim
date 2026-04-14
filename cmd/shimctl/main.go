package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"llama_shim/internal/config"
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
	configPath := root.String("config", "", "path to YAML config file")
	if err := root.Parse(args); err != nil {
		return err
	}

	rest := root.Args()
	if len(rest) == 0 {
		printUsage(stderr)
		return errors.New("maintenance command is required")
	}

	cfg, err := config.Load(*configPath)
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
	case "backup":
		return runBackup(cfg, rest[1:], stdout, stderr)
	case "restore":
		return runRestore(cfg, rest[1:], stdout, stderr)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown maintenance command %q", rest[0])
	}
}

func runCleanup(cfg config.Config, stdout io.Writer) error {
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

func runOptimize(cfg config.Config, stdout io.Writer) error {
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

func runVacuum(cfg config.Config, stdout io.Writer) error {
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

func runBackup(cfg config.Config, args []string, stdout, stderr io.Writer) error {
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

func runRestore(cfg config.Config, args []string, stdout, stderr io.Writer) error {
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

func openStore(cfg config.Config) (*sqlite.Store, error) {
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
	_, _ = fmt.Fprintln(w, "usage: shimctl [-config path] <cleanup|optimize|vacuum|backup|restore> [flags]")
}
