package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"llama_shim/internal/modelcert"
)

func main() {
	var opts modelcert.RunOptions
	models := flag.String("models", envString("MODEL_CERT_MODELS", ""), "comma-separated public provider/model aliases to certify; empty means all manifest models")
	flag.StringVar(&opts.ManifestPath, "manifest", envString("MODEL_CERT_MANIFEST", "configs/model-certification.yaml"), "model certification manifest")
	flag.StringVar(&opts.BaseConfigPath, "config", envString("CONFIG", "config.yaml"), "base shim config used for provider and Codex metadata defaults")
	flag.StringVar(&opts.OutDir, "out", envString("MODEL_CERT_OUT", ""), "artifact output directory")
	flag.StringVar(&opts.RunID, "run-id", envString("MODEL_CERT_RUN_ID", ""), "stable run id")
	flag.StringVar(&opts.ExternalTesterDir, "external-tester-dir", envString("MODEL_CERT_EXTERNAL_TESTER_DIR", ""), "optional openai-compatible-tester checkout")
	flag.StringVar(&opts.TesterCommand, "tester-command", envString("MODEL_CERT_TESTER_CMD", ""), "optional exact external tester shell command")
	flag.StringVar(&opts.ShimCommand, "shim-command", envString("MODEL_CERT_SHIM_CMD", "go run ./cmd/shim"), "shim start command; -config is appended")
	flag.StringVar(&opts.CodexAutoCommand, "codex-auto-command", envString("MODEL_CERT_CODEX_AUTO_CMD", "bash ./scripts/codex-eval-auto.sh"), "Codex auto runner command")
	flag.StringVar(&opts.CodexCurateCommand, "codex-curate-command", envString("MODEL_CERT_CODEX_CURATE_CMD", "bash ./scripts/codex-eval-curate.sh"), "Codex curation command; auto dir is appended")
	flag.BoolVar(&opts.SkipShim, "skip-shim", envBool("MODEL_CERT_SKIP_SHIM", false), "render artifacts without starting a shim")
	flag.BoolVar(&opts.SkipTester, "skip-tester", envBool("MODEL_CERT_SKIP_TESTER", false), "skip the external API compatibility tester")
	flag.BoolVar(&opts.SkipCodex, "skip-codex", envBool("MODEL_CERT_SKIP_CODEX", false), "skip Codex eval phase")
	flag.BoolVar(&opts.RequireTester, "require-tester", envBool("MODEL_CERT_REQUIRE_TESTER", true), "fail model when no external tester command is configured")
	flag.Parse()
	opts.Models = parseCSV(*models)

	summary, err := modelcert.NewRunner(opts).Run(context.Background())
	if summary.OutDir != "" {
		fmt.Printf("model certification report: %s/summary.md\n", strings.TrimRight(summary.OutDir, "/"))
		fmt.Printf("model certification summary: %s/summary.json\n", strings.TrimRight(summary.OutDir, "/"))
		fmt.Printf("model certification: %s\n", strings.TrimRight(summary.OutDir, "/"))
	}
	if err != nil {
		if errors.Is(err, modelcert.ErrRunFailed) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "model certification failed: %v\n", err)
		os.Exit(1)
	}
}

func parseCSV(value string) []string {
	value = strings.ReplaceAll(value, ",", " ")
	fields := strings.Fields(value)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func envString(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
