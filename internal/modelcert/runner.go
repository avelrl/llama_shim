package modelcert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"llama_shim/internal/config"
)

var ErrRunFailed = errors.New("model certification run failed")

type Runner struct {
	options RunOptions
	client  *http.Client
}

func NewRunner(options RunOptions) *Runner {
	return &Runner{
		options: options,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (r *Runner) Run(ctx context.Context) (Summary, error) {
	opts := r.options
	if strings.TrimSpace(opts.ManifestPath) == "" {
		opts.ManifestPath = "configs/model-certification.yaml"
	}
	if strings.TrimSpace(opts.BaseConfigPath) == "" {
		opts.BaseConfigPath = "config.yaml"
	}
	if strings.TrimSpace(opts.RunID) == "" {
		opts.RunID = time.Now().UTC().Format("20060102T150405Z")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		opts.OutDir = filepath.Join(".tmp", "model-certification", "cert-"+opts.RunID)
	}
	if strings.TrimSpace(opts.ShimCommand) == "" {
		opts.ShimCommand = "go run ./cmd/shim"
	}
	if strings.TrimSpace(opts.CodexRunnerCommand) == "" {
		opts.CodexRunnerCommand = "bash ./scripts/codex-eval-runner.sh"
	}

	manifest, err := LoadManifest(opts.ManifestPath)
	if err != nil {
		return Summary{}, err
	}
	baseConfig, err := config.Load(opts.BaseConfigPath)
	if err != nil {
		return Summary{}, fmt.Errorf("load base config: %w", err)
	}
	selected, err := SelectModels(manifest, opts.Models)
	if err != nil {
		return Summary{}, err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return Summary{}, err
	}
	summary := Summary{
		Object:      "modelcert.summary",
		Status:      "passed",
		RunID:       opts.RunID,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		OutDir:      opts.OutDir,
	}

	var runFailed bool
	for _, entry := range selected {
		completed, err := CompleteModelFromBase(entry, baseConfig)
		if len(opts.CodexProfiles) > 0 {
			completed.Codex.Profiles = append([]string(nil), opts.CodexProfiles...)
		}
		modelSummary := r.runModel(ctx, opts, baseConfig, completed, err)
		if isAttentionVerdict(modelSummary.Verdict) {
			runFailed = true
		}
		summary.Models = append(summary.Models, modelSummary)
	}
	for _, modelSummary := range summary.Models {
		if modelSummary.ArtifactDir == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(modelSummary.ArtifactDir, "fix-candidates.json"))
		if err == nil {
			var candidates []FixCandidate
			if json.Unmarshal(raw, &candidates) == nil {
				summary.FixCandidates = append(summary.FixCandidates, candidates...)
			}
		}
	}
	if runFailed {
		summary.Status = "attention"
	}
	if err := writeJSONFile(filepath.Join(opts.OutDir, "summary.json"), summary); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(opts.OutDir, "summary.md"), []byte(RenderSummaryMarkdown(summary)), 0o644); err != nil {
		return summary, err
	}
	if err := writeJSONFile(filepath.Join(opts.OutDir, "fix-candidates.json"), summary.FixCandidates); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(opts.OutDir, "fix-candidates.md"), []byte(RenderFixCandidatesMarkdown(summary.FixCandidates)), 0o644); err != nil {
		return summary, err
	}
	if runFailed {
		return summary, ErrRunFailed
	}
	return summary, nil
}

func (r *Runner) runModel(ctx context.Context, opts RunOptions, baseConfig config.Config, entry ModelEntry, setupErr error) (result ModelSummary) {
	started := time.Now()
	slug := Slugify(entry.Model)
	artifactDir := filepath.Join(opts.OutDir, "models", slug)
	shimDir := filepath.Join(artifactDir, "shim")
	_ = os.MkdirAll(shimDir, 0o755)
	summary := ModelSummary{
		Model:         entry.Model,
		Slug:          slug,
		Verdict:       VerdictConfigured,
		ProviderID:    entry.Provider.ID,
		UpstreamModel: entry.Provider.UpstreamModel,
		ArtifactDir:   artifactDir,
		Artifacts:     map[string]string{},
		StartedAt:     started.UTC().Format(time.RFC3339Nano),
	}
	_, providerModel, _ := strings.Cut(entry.Model, "/")
	summary.ProviderModel = providerModel
	defer func() {
		summary.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		summary.DurationMS = time.Since(started).Milliseconds()
		_ = writeJSONFile(filepath.Join(artifactDir, "summary.json"), summary)
		_ = os.WriteFile(filepath.Join(artifactDir, "summary.md"), []byte(RenderModelMarkdown(summary)), 0o644)
		result = summary
	}()
	if setupErr != nil {
		summary.Verdict = VerdictNeedsOperatorReview
		summary.PossibleOwner = "environment"
		summary.Signals = []string{"manifest_or_config_error"}
		summary.Tester = StageSummary{Status: "skipped", Error: setupErr.Error()}
		summary.Codex = StageSummary{Status: "skipped"}
		_ = WriteFailureNotes(summary, nil, nil, filepath.Join(artifactDir, "failure-notes.md"))
		return summary
	}
	port := 18080
	if !opts.SkipShim {
		var err error
		port, err = freePort()
		if err != nil {
			summary.Verdict = VerdictShimStartFailed
			summary.PossibleOwner = "environment"
			summary.Tester = StageSummary{Status: "skipped", Error: err.Error()}
			summary.Codex = StageSummary{Status: "skipped"}
			return summary
		}
	}
	configRaw, err := RenderShimConfig(RenderConfigOptions{
		Model:       entry,
		BaseConfig:  baseConfig,
		ArtifactDir: artifactDir,
		Port:        port,
	})
	if err != nil {
		summary.Verdict = VerdictNeedsOperatorReview
		summary.PossibleOwner = "shim"
		summary.Tester = StageSummary{Status: "skipped", Error: err.Error()}
		summary.Codex = StageSummary{Status: "skipped"}
		return summary
	}
	configPath := filepath.Join(shimDir, "config.yaml")
	if err := os.WriteFile(configPath, configRaw, 0o644); err != nil {
		summary.Verdict = VerdictNeedsOperatorReview
		summary.PossibleOwner = "environment"
		summary.Tester = StageSummary{Status: "skipped", Error: err.Error()}
		summary.Codex = StageSummary{Status: "skipped"}
		return summary
	}
	summary.ConfigPath = configPath
	summary.ShimBaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	summary.Artifacts["config"] = configPath
	if err := writeModelEnv(filepath.Join(artifactDir, "model.env"), entry, summary.ShimBaseURL); err != nil {
		summary.Signals = append(summary.Signals, "model_env_write_failed")
	}

	var proc *exec.Cmd
	if !opts.SkipShim {
		proc, err = r.startShim(ctx, opts.ShimCommand, configPath, shimDir)
		if err != nil {
			summary.Verdict = VerdictShimStartFailed
			summary.PossibleOwner = "environment"
			summary.Tester = StageSummary{Status: "skipped", Error: err.Error()}
			summary.Codex = StageSummary{Status: "skipped"}
			return summary
		}
		defer stopProcess(proc)
		if err := r.waitOK(ctx, summary.ShimBaseURL+"/healthz", clientRequestID(opts.RunID, slug, "shim", "healthz")); err != nil {
			summary.Verdict = VerdictShimStartFailed
			summary.PossibleOwner = "environment"
			summary.HealthStatus = 0
			summary.Tester = StageSummary{Status: "skipped", Error: err.Error()}
			summary.Codex = StageSummary{Status: "skipped"}
			return summary
		}
	}

	summary.HealthStatus, _ = r.capture(summary.ShimBaseURL+"/healthz", filepath.Join(artifactDir, "healthz.json"), clientRequestID(opts.RunID, slug, "shim", "healthz-capture"))
	summary.ReadyzStatus, _ = r.capture(summary.ShimBaseURL+"/readyz", filepath.Join(artifactDir, "readyz.json"), clientRequestID(opts.RunID, slug, "shim", "readyz"))
	summary.CapabilitiesStatus, _ = r.capture(summary.ShimBaseURL+"/debug/capabilities", filepath.Join(artifactDir, "capabilities.json"), clientRequestID(opts.RunID, slug, "shim", "capabilities"))
	requireReadyz := true
	if entry.Readiness.RequireReadyz != nil {
		requireReadyz = *entry.Readiness.RequireReadyz
	}
	if !opts.SkipShim && requireReadyz && summary.ReadyzStatus/100 != 2 {
		summary.Verdict = VerdictShimStartFailed
		summary.PossibleOwner = "provider"
		summary.Tester = StageSummary{Status: "skipped", Error: fmt.Sprintf("/readyz returned HTTP %d", summary.ReadyzStatus)}
		summary.Codex = StageSummary{Status: "skipped"}
		summary = r.finalizeDiagnostics(summary, nil)
		return summary
	}

	if opts.SkipTester {
		summary.Tester = StageSummary{Status: "skipped"}
	} else {
		testerStatus := r.runTester(ctx, opts, entry, artifactDir, summary.ShimBaseURL)
		summary.Tester = testerStatus
		if testerStatus.Status != "passed" {
			summary.Verdict = VerdictAPICompatFailed
			summary.Codex = StageSummary{Status: "skipped"}
			summary = r.finalizeDiagnostics(summary, nil)
			return summary
		}
		summary.Verdict = VerdictAPICompatPassed
	}

	if opts.SkipCodex || entry.Codex.Skip || len(entry.Codex.Profiles) == 0 {
		summary.Codex = StageSummary{Status: "skipped"}
		summary = r.finalizeDiagnostics(summary, nil)
		return summary
	}
	codexStatus := r.runCodex(ctx, opts, entry, artifactDir, summary.ShimBaseURL, configPath)
	summary.Codex = codexStatus
	if codexStatus.Status != "passed" {
		summary.Verdict = VerdictCodexFailed
	} else if codexLooksRetryDependent(filepath.Join(artifactDir, "codex", "summary.json")) {
		summary.Verdict = VerdictCodexRetryDependent
	} else {
		summary.Verdict = VerdictCodexClean
	}
	summary = r.finalizeDiagnostics(summary, nil)
	return summary
}

func (r *Runner) startShim(ctx context.Context, shimCommand string, configPath string, shimDir string) (*exec.Cmd, error) {
	stdout, err := os.Create(filepath.Join(shimDir, "shim.stdout.log"))
	if err != nil {
		return nil, err
	}
	stderr, err := os.Create(filepath.Join(shimDir, "shim.stderr.log"))
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	command := strings.TrimSpace(shimCommand) + " -config " + shellQuote(configPath)
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = isolatedShimEnv(os.Environ(), shimDir)
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	_ = stdout.Close()
	_ = stderr.Close()
	return cmd, nil
}

func (r *Runner) runTester(ctx context.Context, opts RunOptions, entry ModelEntry, artifactDir string, shimBaseURL string) StageSummary {
	testerDir := filepath.Join(artifactDir, "external-tester")
	_ = os.MkdirAll(testerDir, 0o755)
	testerOutDir := testerDir
	if abs, err := filepath.Abs(testerOutDir); err == nil {
		testerOutDir = abs
	}
	command := strings.TrimSpace(opts.TesterCommand)
	if command == "" {
		command = strings.TrimSpace(entry.Tester.Command)
	}
	if command == "" && opts.ExternalTesterDir != "" {
		modelsConfig := firstNonEmpty(opts.TesterModelsConfig, entry.Tester.ModelsConfig, "configs/models_llama_shim.yaml")
		suiteConfig := firstNonEmpty(opts.TesterSuiteConfig, entry.Tester.SuiteConfig, "configs/suite_llama_shim.yaml")
		capabilitiesConfig := firstNonEmpty(opts.TesterCapabilities, entry.Tester.CapabilitiesConfig, "configs/capabilities_llama_shim.yaml")
		profile := firstNonEmpty(opts.TesterProfile, entry.Tester.Profile)
		if profile == "" {
			profile = "model-cert-" + Slugify(entry.Model)
			modelsConfig = filepath.Join(testerDir, "models.generated.yaml")
			if abs, err := filepath.Abs(modelsConfig); err == nil {
				modelsConfig = abs
			}
			if err := writeGeneratedTesterModelsConfig(modelsConfig, profile, entry.Model); err != nil {
				if opts.RequireTester {
					return StageSummary{Status: "failed", Path: testerDir, Error: err.Error()}
				}
				return StageSummary{Status: "skipped", Path: testerDir}
			}
		}
		command = fmt.Sprintf("cd %s && go run . --no-tui --base-url %s --models %s --suite %s --capabilities %s --profile %s --mode %s --out-dir %s --json",
			shellQuote(opts.ExternalTesterDir),
			shellQuote(shimBaseURL),
			shellQuote(modelsConfig),
			shellQuote(suiteConfig),
			shellQuote(capabilitiesConfig),
			shellQuote(profile),
			shellQuote(defaultString(entry.Tester.Mode, "compat")),
			shellQuote(testerOutDir),
		)
	}
	if command == "" {
		if opts.RequireTester {
			return StageSummary{Status: "failed", Path: testerDir, Error: "MODEL_CERT_TESTER_CMD or MODEL_CERT_EXTERNAL_TESTER_DIR is required"}
		}
		return StageSummary{Status: "skipped", Path: testerDir}
	}
	env := []string{
		"BASE_URL=" + shimBaseURL,
		"OPENAI_BASE_URL=" + shimBaseURL + "/v1",
		"OPENAI_API_KEY=shim-dev-key",
		"SHIM_BASE_URL=" + shimBaseURL,
		"MODEL_CERT_MODEL=" + entry.Model,
		"TESTER_MODEL=" + entry.Model,
		"MODEL_CERT_ARTIFACT_DIR=" + artifactDir,
		"MODEL_ARTIFACT_DIR=" + artifactDir,
		"MODEL_CERT_EXTERNAL_TESTER_DIR=" + opts.ExternalTesterDir,
	}
	status := runShell(ctx, command, testerDir, env)
	status.Path = testerDir
	return status
}

func writeGeneratedTesterModelsConfig(path string, profile string, model string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw := fmt.Sprintf("profiles:\n  - name: %q\n    chat_model: %q\n    responses_model: %q\n    reasoning_effort: %q\n    temperature: 0\n    extra: {}\n",
		profile,
		model,
		model,
		"minimal",
	)
	return os.WriteFile(path, []byte(raw), 0o644)
}

func (r *Runner) runCodex(ctx context.Context, opts RunOptions, entry ModelEntry, artifactDir string, shimBaseURL string, configPath string) StageSummary {
	codexDir := filepath.Join(artifactDir, "codex")
	_ = os.MkdirAll(codexDir, 0o755)

	directSummary := codexDirectSummary{
		Object: "modelcert.codex_summary",
		Status: "passed",
		Model:  entry.Model,
	}
	finalStatus := StageSummary{Status: "passed", Path: codexDir}
	attempts := entry.Codex.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	for _, profile := range entry.Codex.Profiles {
		suite, err := codexSuiteForCertificationProfile(profile)
		profileDir := filepath.Join(codexDir, "profiles", Slugify(profile))
		profileSummary := codexDirectProfile{
			Profile: profile,
			Suite:   suite,
			Path:    profileDir,
		}
		if err != nil {
			profileSummary.Status = "failed"
			profileSummary.Error = err.Error()
			directSummary.Status = "failed"
			finalStatus = StageSummary{Status: "failed", Path: codexDir, Error: err.Error()}
			directSummary.Profiles = append(directSummary.Profiles, profileSummary)
			continue
		}
		env := []string{
			"SHIM_BASE_URL=" + shimBaseURL,
			"CODEX_BASE_URL=" + strings.TrimRight(shimBaseURL, "/") + "/v1",
			"CODEX_PROVIDER=gateway-shim",
			"CODEX_API_KEY_ENV=GW_API_KEY",
			"CODEX_API_KEY=shim-dev-key",
			"GW_API_KEY=shim-dev-key",
			"CODEX_MODEL=" + entry.Model,
			"CODEX_EVAL_SUITE=" + suite,
			"CODEX_EVAL_OUT=" + profileDir,
			"CODEX_EVAL_ATTEMPTS=" + strconv.Itoa(attempts),
			"CODEX_EVAL_REASONING_EFFORT=" + defaultString(entry.Codex.ReasoningEffort, "minimal"),
			"CODEX_EVAL_APPLY_PATCH_TOOL_TYPE=" + defaultString(entry.Codex.ApplyPatchToolType, "freeform"),
			"CONFIG=" + configPath,
		}
		status := runShell(ctx, opts.CodexRunnerCommand, profileDir, env)
		profileSummary.Status = status.Status
		profileSummary.ExitCode = status.ExitCode
		profileSummary.Error = status.Error
		directSummary.Profiles = append(directSummary.Profiles, profileSummary)
		if status.Status != "passed" {
			directSummary.Status = "failed"
			finalStatus.Status = "failed"
			finalStatus.ExitCode = status.ExitCode
			finalStatus.Error = fmt.Sprintf("codex profile %q failed: %s", profile, status.Error)
		}
	}
	_ = writeJSONFile(filepath.Join(codexDir, "summary.json"), directSummary)
	_ = os.WriteFile(filepath.Join(codexDir, "summary.md"), []byte(renderCodexDirectMarkdown(directSummary)), 0o644)
	return finalStatus
}

type codexDirectSummary struct {
	Object   string               `json:"object"`
	Status   string               `json:"status"`
	Model    string               `json:"model"`
	Profiles []codexDirectProfile `json:"profiles"`
}

type codexDirectProfile struct {
	Profile  string `json:"profile"`
	Suite    string `json:"suite"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Path     string `json:"path"`
	Error    string `json:"error,omitempty"`
}

func codexSuiteForCertificationProfile(profile string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(profile))
	switch normalized {
	case "baseline", "real-upstream":
		return "codex-real-upstream", nil
	case "expanded", "real-upstream-expanded":
		return "codex-real-upstream-expanded", nil
	case "bench-lite", "bench_lite", "benchlite":
		return "codex-bench-lite", nil
	default:
		if strings.HasPrefix(normalized, "codex-") {
			return normalized, nil
		}
		return "", fmt.Errorf("unknown Codex certification profile %q", profile)
	}
}

func renderCodexDirectMarkdown(summary codexDirectSummary) string {
	var b strings.Builder
	b.WriteString("# Codex Certification\n\n")
	b.WriteString("- model: `" + summary.Model + "`\n")
	b.WriteString("- status: `" + summary.Status + "`\n\n")
	b.WriteString("| Profile | Suite | Status | Exit | Path |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, profile := range summary.Profiles {
		exit := ""
		if profile.ExitCode != 0 {
			exit = strconv.Itoa(profile.ExitCode)
		}
		b.WriteString("| `" + profile.Profile + "` | `" + profile.Suite + "` | `" + profile.Status + "` | `" + exit + "` | `" + profile.Path + "` |\n")
	}
	return b.String()
}

func (r *Runner) finalizeDiagnostics(summary ModelSummary, extraSignals []string) ModelSummary {
	shimLog := filepath.Join(summary.ArtifactDir, "shim", "shim.log")
	logDiagnosticsPath := filepath.Join(summary.ArtifactDir, "shim-log-diagnostics.md")
	logMatches, _ := WriteLogDiagnostics(shimLog, logDiagnosticsPath)
	traces := r.fetchTraces(summary.ShimBaseURL, summary.Model, summary.Slug)
	_, traceSummaryPath, _ := WriteTraceArtifacts(summary.ArtifactDir, traces)
	failureNotesPath := filepath.Join(summary.ArtifactDir, "failure-notes.md")
	summary.TraceSummaryPath = traceSummaryPath
	summary.FailureNotesPath = failureNotesPath
	signals := detectSignals(logMatches, traces)
	signals = append(signals, extraSignals...)
	summary.Signals = compactStrings(signals)
	summary = applyDiagnosticVerdict(summary, traces)
	summary.PossibleOwner = inferOwner(summary.Signals)
	if summary.PossibleOwner == "" && isAttentionVerdict(summary.Verdict) {
		summary.PossibleOwner = "unknown"
	}
	_ = WriteFailureNotes(summary, logMatches, traces, failureNotesPath)
	candidates := BuildFixCandidates(summary, logMatches, traces)
	_ = writeJSONFile(filepath.Join(summary.ArtifactDir, "fix-candidates.json"), candidates)
	_ = os.WriteFile(filepath.Join(summary.ArtifactDir, "fix-candidates.md"), []byte(RenderFixCandidatesMarkdown(candidates)), 0o644)
	return summary
}

func (r *Runner) fetchTraces(baseURL string, model string, slug string) []DebugTrace {
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/debug/traces?limit=4096", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("X-Client-Request-Id", clientRequestID(r.options.RunID, slug, "diagnostics", "traces"))
	resp, err := r.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil
	}
	var list TraceList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil
	}
	return list.Data
}

func (r *Runner) waitOK(ctx context.Context, url string, clientID string) error {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		status, err := r.request(ctx, http.MethodGet, url, nil, clientID, io.Discard)
		if err == nil && status/100 == 2 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("HTTP %d", status)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s did not become ready: %w", url, lastErr)
}

func (r *Runner) capture(url string, outputPath string, clientID string) (int, error) {
	var buf bytes.Buffer
	status, err := r.request(context.Background(), http.MethodGet, url, nil, clientID, &buf)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return status, err
	}
	return status, os.WriteFile(outputPath, buf.Bytes(), 0o644)
}

func (r *Runner) request(ctx context.Context, method string, url string, body io.Reader, clientID string, out io.Writer) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}
	if clientID != "" {
		req.Header.Set("X-Client-Request-Id", clientID)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		_, _ = io.Copy(out, resp.Body)
	}
	return resp.StatusCode, nil
}

func runShell(ctx context.Context, command string, outDir string, extraEnv []string) StageSummary {
	_ = os.MkdirAll(outDir, 0o755)
	_ = os.WriteFile(filepath.Join(outDir, "command.sh"), []byte(command+"\n"), 0o644)
	stdout, err := os.Create(filepath.Join(outDir, "stdout.log"))
	if err != nil {
		return StageSummary{Status: "failed", Error: err.Error()}
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(outDir, "stderr.log"))
	if err != nil {
		return StageSummary{Status: "failed", Error: err.Error()}
	}
	defer stderr.Close()
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), extraEnv...)
	err = cmd.Run()
	if err != nil {
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return StageSummary{Status: "failed", ExitCode: exitCode, Error: err.Error()}
	}
	return StageSummary{Status: "passed"}
}

func isolatedShimEnv(baseEnv []string, shimDir string) []string {
	out := make([]string, 0, len(baseEnv)+1)
	for _, item := range baseEnv {
		key, _, ok := strings.Cut(item, "=")
		if !ok || shouldDropIsolatedShimEnv(key) {
			continue
		}
		out = append(out, item)
	}
	out = append(out, "SHIM_DOTENV="+filepath.ToSlash(filepath.Join(shimDir, "missing.env")))
	return out
}

func shouldDropIsolatedShimEnv(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return true
	}
	if key == "CONFIG" {
		return true
	}
	for _, prefix := range []string{
		"SHIM_",
		"SQLITE_",
		"STORAGE_",
		"POSTGRES_",
		"LLAMA_",
		"LOG_",
		"CHAT_COMPLETIONS_",
		"RESPONSES_",
		"RETRIEVAL_",
		"UI_",
	} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func writeModelEnv(path string, entry ModelEntry, shimBaseURL string) error {
	var b strings.Builder
	b.WriteString("MODEL=" + entry.Model + "\n")
	b.WriteString("PROVIDER_ID=" + entry.Provider.ID + "\n")
	b.WriteString("PROVIDER_BASE_URL=" + entry.Provider.BaseURL + "\n")
	b.WriteString("PROVIDER_BEARER_TOKEN_ENV=" + entry.Provider.BearerTokenEnv + "\n")
	b.WriteString("UPSTREAM_MODEL=" + entry.Provider.UpstreamModel + "\n")
	b.WriteString("SHIM_BASE_URL=" + shimBaseURL + "\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %s", listener.Addr())
	}
	return addr.Port, nil
}

func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func clientRequestID(runID string, slug string, stage string, caseID string) string {
	value := "modelcert:" + runID + ":" + slug + ":" + stage + ":" + caseID
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func codexLooksRetryDependent(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "retry_dependent") || strings.Contains(lower, "retry-dependent")
}

func isAttentionVerdict(verdict string) bool {
	switch verdict {
	case VerdictShimStartFailed, VerdictAPICompatFailed, VerdictCodexFailed, VerdictNeedsOperatorReview:
		return true
	default:
		return false
	}
}

func Slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "model"
	}
	return out
}
