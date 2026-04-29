package codexeval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrRunFailed = errors.New("codex eval run failed")

type Runner struct {
	config Config
}

func NewRunner(config Config) *Runner {
	return &Runner{config: normalizeConfig(config)}
}

func (runner *Runner) Run(ctx context.Context) (*Summary, error) {
	started := time.Now()
	if err := os.MkdirAll(runner.config.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create out dir: %w", err)
	}

	tasks, err := LoadTasks(runner.config.TasksDir, runner.config.Suite)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks matched suite %q in %s", runner.config.Suite, runner.config.TasksDir)
	}

	env := runner.environment(started)
	if err := writeJSON(filepath.Join(runner.config.OutDir, "environment.json"), env); err != nil {
		return nil, err
	}
	if err := runner.preflight(ctx); err != nil {
		return nil, err
	}

	summary := Summary{
		RunID:          env.RunID,
		StartedAt:      env.StartedAt,
		Environment:    env,
		Counts:         map[string]int{},
		FailureBuckets: map[string]int{},
		ArtifactRoot:   runner.config.OutDir,
	}

	for _, task := range tasks {
		fmt.Fprintf(os.Stderr, "==> codex eval task: %s\n", task.Manifest.ID)
		taskResult := runner.runTask(ctx, task)
		fmt.Fprintf(os.Stderr, "==> codex eval task complete: %s %s\n", task.Manifest.ID, taskResult.Status)
		summary.Tasks = append(summary.Tasks, taskResult)
		summary.Counts[taskResult.Status]++
		if taskResult.FailureBucket != "" {
			summary.FailureBuckets[taskResult.FailureBucket]++
		}
	}
	completed := time.Now()
	summary.CompletedAt = completed.UTC().Format(time.RFC3339)
	summary.DurationMS = completed.Sub(started).Milliseconds()
	if err := writeJSON(filepath.Join(runner.config.OutDir, "summary.json"), summary); err != nil {
		return nil, err
	}
	if err := writeSummaryMarkdown(filepath.Join(runner.config.OutDir, "summary.md"), summary); err != nil {
		return nil, err
	}
	if summary.Counts[StatusPassed] != len(summary.Tasks) {
		return &summary, ErrRunFailed
	}
	return &summary, nil
}

func normalizeConfig(config Config) Config {
	if config.TasksDir == "" {
		config.TasksDir = "internal/codexeval/testdata/tasks"
	}
	if config.Suite == "" {
		config.Suite = "codex-smoke"
	}
	if config.OutDir == "" {
		config.OutDir = filepath.Join(".tmp", "codex-eval-runs", "run-"+time.Now().UTC().Format("20060102T150405Z"))
	}
	if config.ShimBaseURL == "" {
		config.ShimBaseURL = "http://127.0.0.1:18080"
	}
	config.ShimBaseURL = strings.TrimRight(config.ShimBaseURL, "/")
	if config.BaseURL == "" {
		config.BaseURL = config.ShimBaseURL + "/v1"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	if config.HealthPath == "" {
		config.HealthPath = "/healthz"
	}
	if !strings.HasPrefix(config.HealthPath, "/") {
		config.HealthPath = "/" + config.HealthPath
	}
	if config.CodexBin == "" {
		config.CodexBin = "codex"
	}
	if config.Model == "" {
		config.Model = "devstack-model"
	}
	if config.Provider == "" {
		config.Provider = "gateway-shim"
	}
	if config.APIKeyEnv == "" {
		config.APIKeyEnv = "OPENAI_API_KEY"
	}
	if config.APIKeyValue == "" {
		config.APIKeyValue = os.Getenv(config.APIKeyEnv)
	}
	if config.ReasoningEffort == "" {
		config.ReasoningEffort = "minimal"
	}
	if config.ReasoningSummary == "" {
		config.ReasoningSummary = "none"
	}
	if config.RequestMaxRetries == 0 {
		config.RequestMaxRetries = 1
	}
	if config.StreamIdleTimeoutMS == 0 {
		config.StreamIdleTimeoutMS = 180000
	}
	return config
}

func (runner *Runner) environment(started time.Time) Environment {
	runID := filepath.Base(runner.config.OutDir)
	return Environment{
		RunID:              runID,
		StartedAt:          started.UTC().Format(time.RFC3339),
		GitCommit:          strings.TrimSpace(commandOutput(context.Background(), "", "git", "rev-parse", "--short", "HEAD")),
		GitDirty:           strings.TrimSpace(commandOutput(context.Background(), "", "git", "status", "--porcelain")) != "",
		CodexBin:           runner.config.CodexBin,
		CodexVersion:       strings.TrimSpace(commandOutput(context.Background(), "", runner.config.CodexBin, "--version")),
		Model:              runner.config.Model,
		Provider:           runner.config.Provider,
		ShimBaseURL:        runner.config.ShimBaseURL,
		BaseURL:            runner.config.BaseURL,
		APIKeyEnv:          runner.config.APIKeyEnv,
		APIKeyPresent:      runner.config.APIKeyValue != "",
		Suite:              runner.config.Suite,
		WebSockets:         runner.config.WebSockets,
		UnifiedExec:        runner.config.UnifiedExec,
		ApplyPatchFreeform: runner.config.ApplyPatchFreeform,
		ReasoningEffort:    runner.config.ReasoningEffort,
		ReasoningSummary:   runner.config.ReasoningSummary,
	}
}

func (runner *Runner) preflight(ctx context.Context) error {
	if _, err := exec.LookPath(runner.config.CodexBin); err != nil {
		return fmt.Errorf("codex binary %q not found: %w", runner.config.CodexBin, err)
	}
	if !runner.config.SkipHealthCheck {
		if err := probeHTTP(ctx, runner.config.ShimBaseURL+runner.config.HealthPath, runner.config.APIKeyValue); err != nil {
			return fmt.Errorf("shim health probe failed: %w", err)
		}
	}
	if !runner.config.SkipModelsProbe {
		if err := runner.probeModels(ctx); err != nil {
			return fmt.Errorf("models probe failed: %w", err)
		}
	}
	return nil
}

func probeHTTP(ctx context.Context, url, apiKey string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (runner *Runner) probeModels(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, runner.config.ShimBaseURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	if runner.config.APIKeyValue != "" {
		req.Header.Set("Authorization", "Bearer "+runner.config.APIKeyValue)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if _, ok := shape["data"].([]any); ok {
		return nil
	}
	if _, ok := shape["models"].([]any); ok {
		return nil
	}
	return fmt.Errorf("unexpected /v1/models shape: %s", strings.TrimSpace(string(raw)))
}

func (runner *Runner) runTask(ctx context.Context, task Task) TaskResult {
	started := time.Now()
	taskDir := filepath.Join(runner.config.OutDir, "tasks", task.Manifest.ID)
	result := TaskResult{
		ID:          task.Manifest.ID,
		Title:       task.Manifest.Title,
		Category:    task.Manifest.Category,
		Status:      StatusFailedSetup,
		ArtifactDir: taskDir,
	}
	if task.Manifest.Quarantine != nil && quarantineApplies(task.Manifest.Quarantine, runner.config.Model) {
		result.Status = StatusQuarantined
		result.FailureBucket = BucketModelNoTool
		result.QuarantineNote = task.Manifest.Quarantine.Reason
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	attempts := attemptsFor(task.Manifest, runner.config.AttemptsOverride)
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptResult := runner.runAttempt(ctx, task, attempt)
		result.Attempts = append(result.Attempts, attemptResult)
		if attemptResult.Status == StatusPassed {
			result.Status = StatusPassed
			result.FailureBucket = ""
			break
		}
		result.Status = attemptResult.Status
		result.FailureBucket = attemptResult.FailureBucket
	}
	result.DurationMS = time.Since(started).Milliseconds()
	_ = writeJSON(filepath.Join(taskDir, "task-result.json"), result)
	return result
}

func quarantineApplies(quarantine *Quarantine, model string) bool {
	if quarantine == nil {
		return false
	}
	if len(quarantine.Models) == 0 {
		return true
	}
	for _, candidate := range quarantine.Models {
		if candidate == model {
			return true
		}
	}
	return false
}

func (runner *Runner) runAttempt(ctx context.Context, task Task, attempt int) AttemptResult {
	started := time.Now()
	attemptDir := filepath.Join(runner.config.OutDir, "tasks", task.Manifest.ID, fmt.Sprintf("attempt-%02d", attempt))
	workspace := filepath.Join(attemptDir, "workspace")
	before := filepath.Join(attemptDir, "workspace-before")
	after := filepath.Join(attemptDir, "workspace-after")
	outputFile := filepath.Join(attemptDir, "codex.jsonl")
	result := AttemptResult{
		Attempt:     attempt,
		Status:      StatusFailedSetup,
		ArtifactDir: attemptDir,
	}
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		result.Error = err.Error()
		result.FailureBucket = BucketHarnessBug
		return result
	}
	fmt.Fprintf(os.Stderr, "==> codex eval attempt: %s #%d\n", task.Manifest.ID, attempt)
	if err := copyDirIfExists(filepath.Join(task.Dir, "workspace"), workspace); err != nil {
		result.Error = err.Error()
		result.FailureBucket = BucketHarnessBug
		return result
	}
	_ = copyDirIfExists(workspace, before)
	if err := writeManifestCopy(task, attemptDir); err != nil {
		result.Error = err.Error()
		result.FailureBucket = BucketHarnessBug
		return result
	}
	if err := runner.writeCodexConfig(filepath.Join(attemptDir, "codex-home", "config.toml")); err != nil {
		result.Error = err.Error()
		result.FailureBucket = BucketHarnessBug
		return result
	}

	timeout, err := task.Manifest.TimeoutDuration()
	if err != nil {
		result.Error = err.Error()
		result.FailureBucket = BucketHarnessBug
		return result
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exitCode, runErr := runner.runCodex(taskCtx, task, attemptDir, workspace, outputFile)
	result.ExitCode = exitCode
	if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
		result.Status = StatusFailedTimeout
		result.FailureBucket = BucketTimeout
		result.Error = "task timeout"
	} else if runErr != nil {
		result.Status, result.FailureBucket = classifyRunError(runErr, outputFile)
		result.Error = runErr.Error()
	}

	_ = copyDirIfExists(workspace, after)
	_ = writeGitDiff(before, after, filepath.Join(attemptDir, "git.diff"))
	checkResult, finalText, checkErr := runCheckers(ctx, task.Manifest, workspace, outputFile, task.Manifest.Env)
	result.CheckResult = checkResult
	if _, stats, _, eventErr := parseCodexEvents(outputFile); eventErr == nil {
		result.Events = stats
	}
	if checkErr != nil && result.Status == StatusFailedSetup {
		result.Status = StatusFailedSetup
		result.FailureBucket = BucketHarnessBug
		result.Error = checkErr.Error()
	}
	if runErr == nil && checkErr == nil {
		if checkResult.Passed {
			result.Status = StatusPassed
			result.FailureBucket = ""
		} else {
			result.Status, result.FailureBucket = classifyCheckFailure(checkResult, finalText)
		}
	}
	result.DurationMS = time.Since(started).Milliseconds()
	_ = writeJSON(filepath.Join(attemptDir, "checker.json"), result.CheckResult)
	if result.Status != StatusPassed {
		_ = writeFailureMarkdown(filepath.Join(attemptDir, "failure.md"), task.Manifest, result)
	}
	return result
}

func writeManifestCopy(task Task, outDir string) error {
	src := filepath.Join(task.Dir, "task.yaml")
	dst := filepath.Join(outDir, "task.yaml")
	return copyFile(src, dst, 0o644)
}

func (runner *Runner) writeCodexConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	config := fmt.Sprintf(`model = "%s"
model_provider = "%s"
approval_policy = "never"
sandbox_mode = "workspace-write"
web_search = "disabled"

[history]
persistence = "none"

[features]
apps = false
memories = false
multi_agent = false
apply_patch_freeform = %t
unified_exec = %t

[apps._default]
enabled = false
default_tools_enabled = false

[model_providers.%s]
name = "%s codex eval"
base_url = "%s"
wire_api = "responses"
env_key = "%s"
supports_websockets = %t
request_max_retries = %d
stream_max_retries = %d
stream_idle_timeout_ms = %d
`, runner.config.Model, runner.config.Provider, runner.config.ApplyPatchFreeform, runner.config.UnifiedExec,
		runner.config.Provider, runner.config.Provider, runner.config.BaseURL, runner.config.APIKeyEnv,
		runner.config.WebSockets, runner.config.RequestMaxRetries, runner.config.StreamMaxRetries, runner.config.StreamIdleTimeoutMS)
	return os.WriteFile(path, []byte(config), 0o600)
}

func (runner *Runner) runCodex(ctx context.Context, task Task, attemptDir, workspace, outputFile string) (int, error) {
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return -1, err
	}
	codexHome, err := filepath.Abs(filepath.Join(attemptDir, "codex-home"))
	if err != nil {
		return -1, err
	}
	output, err := os.Create(outputFile)
	if err != nil {
		return -1, err
	}
	defer output.Close()
	var captured bytes.Buffer
	writer := io.MultiWriter(output, &captured)
	args := []string{
		"exec",
		"--ephemeral",
		"--ignore-rules",
		"--skip-git-repo-check",
		"--json",
		"-C", workspaceAbs,
		"-m", runner.config.Model,
		"-c", fmt.Sprintf("model_provider=%q", runner.config.Provider),
		"-c", `approval_policy="never"`,
		"-c", `sandbox_mode="workspace-write"`,
		"-c", fmt.Sprintf("model_reasoning_effort=%q", runner.config.ReasoningEffort),
		"-c", fmt.Sprintf("model_reasoning_summary=%q", runner.config.ReasoningSummary),
		"-c", `web_search="disabled"`,
		"-c", `shell_environment_policy.inherit="all"`,
		task.Manifest.Prompt,
	}
	cmd := exec.CommandContext(ctx, runner.config.CodexBin, args...)
	cmd.Dir = workspaceAbs
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "CODEX_HOME="+codexHome)
	if runner.config.APIKeyValue != "" {
		cmd.Env = append(cmd.Env, runner.config.APIKeyEnv+"="+runner.config.APIKeyValue)
	}
	for key, value := range task.Manifest.Env {
		cmd.Env = append(cmd.Env, key+"="+expandTemplate(value, workspaceAbs))
	}
	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	if captured.Len() > 0 {
		return exitCode, fmt.Errorf("%w: %s", err, trimForError(captured.String()))
	}
	return exitCode, err
}

func trimForError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4096 {
		return value
	}
	return value[:4096] + "...(truncated)"
}

func classifyRunError(err error, outputFile string) (string, string) {
	raw, _ := os.ReadFile(outputFile)
	text := strings.ToLower(string(raw) + "\n" + err.Error())
	switch {
	case strings.Contains(text, "unauthorized") || strings.Contains(text, "401") || strings.Contains(text, "invalid api key"):
		return StatusFailedTransport, BucketShimAuth
	case strings.Contains(text, "unsupported call: apply_patch"):
		return StatusFailedCodexExit, BucketCodexToolMissing
	case strings.Contains(text, "failed to parse function arguments") || strings.Contains(text, "invalid arguments"):
		return StatusFailedCodexExit, BucketModelBadToolArgs
	case strings.Contains(text, "unexpected status") || strings.Contains(text, "bad gateway") || strings.Contains(text, "upstream"):
		return StatusFailedTransport, BucketUpstreamHTTP
	case strings.Contains(text, "websocket") || strings.Contains(text, "405"):
		return StatusFailedTransport, BucketShimTransport
	default:
		return StatusFailedCodexExit, BucketCodexConfig
	}
}

func classifyCheckFailure(result CheckResult, finalText string) (string, string) {
	for _, failure := range result.Failures {
		switch failure.Kind {
		case "raw_tool_markup":
			return StatusFailedRawTool, BucketRawToolMarkup
		case "final_text":
			return StatusFailedChecker, BucketCheckerDiff
		case "codex_event":
			if strings.Contains(failure.Message, "command_execution") || strings.Contains(failure.Message, "file_change") {
				return StatusFailedNoToolEvent, BucketModelNoTool
			}
			return StatusFailedChecker, BucketHarnessBug
		case "command_failed", "command_timeout":
			return StatusFailedChecker, BucketCheckerTests
		case "file_equals", "file_contains", "file_matches", "file_exists", "file_absent", "file_read":
			return StatusFailedChecker, BucketCheckerDiff
		}
	}
	if strings.TrimSpace(finalText) == "" {
		return StatusFailedNoFinal, BucketModelNoTool
	}
	return StatusFailedChecker, BucketHarnessBug
}

func commandOutput(ctx context.Context, dir, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(raw)
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func writeSummaryMarkdown(path string, summary Summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Summary\n\n")
	fmt.Fprintf(&b, "- Run: `%s`\n", summary.RunID)
	fmt.Fprintf(&b, "- Suite: `%s`\n", summary.Environment.Suite)
	fmt.Fprintf(&b, "- Model: `%s`\n", summary.Environment.Model)
	fmt.Fprintf(&b, "- Provider: `%s`\n", summary.Environment.Provider)
	fmt.Fprintf(&b, "- Duration: `%dms`\n\n", summary.DurationMS)
	fmt.Fprintf(&b, "## Counts\n\n")
	keys := sortedKeys(summary.Counts)
	for _, key := range keys {
		fmt.Fprintf(&b, "- `%s`: %d\n", key, summary.Counts[key])
	}
	fmt.Fprintf(&b, "\n## Failure Buckets\n\n")
	if len(summary.FailureBuckets) == 0 {
		fmt.Fprintf(&b, "- none\n")
	} else {
		for _, key := range sortedKeys(summary.FailureBuckets) {
			fmt.Fprintf(&b, "- `%s`: %d\n", key, summary.FailureBuckets[key])
		}
	}
	fmt.Fprintf(&b, "\n## Tasks\n\n")
	for _, task := range summary.Tasks {
		fmt.Fprintf(&b, "- `%s`: `%s`", task.ID, task.Status)
		if task.FailureBucket != "" {
			fmt.Fprintf(&b, " (`%s`)", task.FailureBucket)
		}
		fmt.Fprintf(&b, "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeFailureMarkdown(path string, manifest Manifest, attempt AttemptResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Failure\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", manifest.ID)
	fmt.Fprintf(&b, "- Attempt: `%d`\n", attempt.Attempt)
	fmt.Fprintf(&b, "- Status: `%s`\n", attempt.Status)
	fmt.Fprintf(&b, "- Bucket: `%s`\n", attempt.FailureBucket)
	if attempt.Error != "" {
		fmt.Fprintf(&b, "- Error: `%s`\n", attempt.Error)
	}
	if len(attempt.CheckResult.Failures) > 0 {
		fmt.Fprintf(&b, "\n## Checker Failures\n\n")
		for _, failure := range attempt.CheckResult.Failures {
			fmt.Fprintf(&b, "- `%s`: %s\n", failure.Kind, failure.Message)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
