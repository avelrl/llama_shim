package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"llama_shim/internal/codexeval"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "matrix" {
		runMatrix(os.Args[2:])
		return
	}

	var config codexeval.Config
	flag.StringVar(&config.TasksDir, "tasks-dir", envString("CODEX_EVAL_TASKS_DIR", "internal/codexeval/testdata/tasks"), "task manifest directory")
	flag.StringVar(&config.Suite, "suite", envString("CODEX_EVAL_SUITE", "codex-smoke"), "task suite")
	flag.StringVar(&config.OutDir, "out", envString("CODEX_EVAL_OUT", ""), "artifact output directory")
	flag.StringVar(&config.ShimBaseURL, "shim-base-url", envString("SHIM_BASE_URL", "http://127.0.0.1:18080"), "shim base URL")
	flag.StringVar(&config.BaseURL, "base-url", envString("CODEX_BASE_URL", ""), "Codex provider base URL")
	flag.StringVar(&config.HealthPath, "health-path", envString("CODEX_EVAL_HEALTH_PATH", "/healthz"), "shim health path")
	flag.StringVar(&config.CodexBin, "codex-bin", envString("CODEX_BIN", "codex"), "Codex CLI binary")
	flag.StringVar(&config.Model, "model", envStringFallback("CODEX_MODEL", "MODEL", "devstack-model"), "Codex model")
	flag.StringVar(&config.Provider, "provider", envString("CODEX_PROVIDER", "gateway-shim"), "Codex provider id")
	flag.StringVar(&config.APIKeyEnv, "api-key-env", envString("CODEX_API_KEY_ENV", "OPENAI_API_KEY"), "Codex provider API key env var name")
	flag.StringVar(&config.APIKeyValue, "api-key", envString("CODEX_API_KEY", ""), "API key value; prefer environment variables")
	flag.IntVar(&config.AttemptsOverride, "attempts", envInt("CODEX_EVAL_ATTEMPTS", 0), "override task attempts; 0 uses manifest")
	flag.StringVar(&config.ReasoningEffort, "reasoning-effort", envString("CODEX_EVAL_REASONING_EFFORT", "minimal"), "Codex reasoning effort")
	flag.StringVar(&config.ReasoningSummary, "reasoning-summary", envString("CODEX_EVAL_REASONING_SUMMARY", "none"), "Codex reasoning summary")
	flag.BoolVar(&config.WebSockets, "websockets", envBool("CODEX_EVAL_WEBSOCKETS", false), "enable Codex provider WebSocket support")
	flag.BoolVar(&config.UnifiedExec, "unified-exec", envBool("CODEX_EVAL_UNIFIED_EXEC", true), "enable Codex unified exec feature")
	flag.BoolVar(&config.ApplyPatchFreeform, "apply-patch-freeform", envBool("CODEX_EVAL_APPLY_PATCH_FREEFORM", true), "enable Codex apply_patch freeform feature")
	flag.BoolVar(&config.SkipHealthCheck, "skip-health-check", envBool("CODEX_EVAL_SKIP_HEALTH_CHECK", false), "skip shim health preflight")
	flag.BoolVar(&config.SkipModelsProbe, "skip-models-probe", envBool("CODEX_EVAL_SKIP_MODELS_PROBE", false), "skip /v1/models preflight")
	flag.IntVar(&config.RequestMaxRetries, "request-max-retries", envInt("CODEX_EVAL_REQUEST_MAX_RETRIES", 1), "Codex provider request retries")
	flag.IntVar(&config.StreamMaxRetries, "stream-max-retries", envInt("CODEX_EVAL_STREAM_MAX_RETRIES", 0), "Codex provider stream retries")
	flag.IntVar(&config.StreamIdleTimeoutMS, "stream-idle-timeout-ms", envInt("CODEX_EVAL_STREAM_IDLE_TIMEOUT_MS", 180000), "Codex provider stream idle timeout")
	flag.Parse()

	if config.APIKeyValue == "" && config.APIKeyEnv != "" {
		config.APIKeyValue = os.Getenv(config.APIKeyEnv)
	}
	if config.OutDir == "" {
		config.OutDir = "run-" + time.Now().UTC().Format("20060102T150405Z")
		config.OutDir = ".tmp/codex-eval-runs/" + config.OutDir
	}

	summary, err := codexeval.NewRunner(config).Run(context.Background())
	if summary != nil {
		fmt.Printf("codex eval summary: %s/summary.json\n", strings.TrimRight(config.OutDir, "/"))
	}
	if err != nil {
		if errors.Is(err, codexeval.ErrRunFailed) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "codex eval failed: %v\n", err)
		os.Exit(1)
	}
}

func runMatrix(args []string) {
	flags := flag.NewFlagSet("matrix", flag.ExitOnError)
	out := flags.String("out", envString("CODEX_EVAL_MATRIX_OUT", ""), "write markdown matrix to this file instead of stdout")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "codex eval matrix failed: %v\n", err)
		os.Exit(2)
	}
	markdown, err := codexeval.RenderMatrixMarkdown(flags.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex eval matrix failed: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*out) == "" {
		fmt.Print(markdown)
		return
	}
	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "codex eval matrix failed: %v\n", err)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(*out, []byte(markdown), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "codex eval matrix failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("codex eval matrix: %s\n", *out)
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envStringFallback(primary, secondary, fallback string) string {
	if value := os.Getenv(primary); value != "" {
		return value
	}
	if value := os.Getenv(secondary); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
