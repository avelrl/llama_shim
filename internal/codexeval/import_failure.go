package codexeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const importedRegressionSuite = "codex-regression-import"

type ImportFailureOptions struct {
	RunPath  string
	TaskID   string
	OutID    string
	TasksDir string
	Attempt  int
}

type ImportFailureResult struct {
	TaskDir    string `json:"task_dir"`
	TaskID     string `json:"task_id"`
	SourceRun  string `json:"source_run"`
	SourceTask string `json:"source_task"`
	Attempt    int    `json:"attempt"`
}

func ImportFailure(options ImportFailureOptions) (ImportFailureResult, error) {
	options.TasksDir = strings.TrimSpace(options.TasksDir)
	if options.TasksDir == "" {
		options.TasksDir = "internal/codexeval/testdata/tasks"
	}
	if strings.TrimSpace(options.RunPath) == "" {
		return ImportFailureResult{}, fmt.Errorf("run path is required")
	}
	if !taskIDPattern.MatchString(options.TaskID) {
		return ImportFailureResult{}, fmt.Errorf("invalid source task id %q", options.TaskID)
	}
	if !taskIDPattern.MatchString(options.OutID) {
		return ImportFailureResult{}, fmt.Errorf("invalid output task id %q", options.OutID)
	}
	if filepath.IsAbs(options.OutID) || strings.ContainsAny(options.OutID, `/\`) {
		return ImportFailureResult{}, fmt.Errorf("output task id must be a task id, not a path")
	}

	summary, runDir, err := readBundleSummary(options.RunPath)
	if err != nil {
		return ImportFailureResult{}, err
	}
	task, ok := findTaskResult(summary.Tasks, options.TaskID)
	if !ok {
		return ImportFailureResult{}, fmt.Errorf("task %q not found in %s", options.TaskID, options.RunPath)
	}
	if task.Status == StatusPassed || task.Status == StatusSkipped || task.Status == StatusQuarantined {
		return ImportFailureResult{}, fmt.Errorf("task %q status is %q; import-failure only imports failed tasks", options.TaskID, task.Status)
	}
	attempt, ok := selectFailedAttempt(task, options.Attempt)
	if !ok {
		return ImportFailureResult{}, fmt.Errorf("task %q has no failed attempt matching attempt %d", options.TaskID, options.Attempt)
	}

	taskDir := filepath.Join(options.TasksDir, options.OutID)
	if _, err := os.Stat(taskDir); err == nil {
		return ImportFailureResult{}, fmt.Errorf("output task %q already exists at %s", options.OutID, taskDir)
	} else if !os.IsNotExist(err) {
		return ImportFailureResult{}, err
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return ImportFailureResult{}, err
	}

	attemptDir := resolveAttemptDir(runDir, task.ID, attempt)
	if err := writeImportedTaskSkeleton(taskDir, summary, task, attempt); err != nil {
		return ImportFailureResult{}, err
	}
	if err := copyDirIfExists(filepath.Join(attemptDir, "workspace-before"), filepath.Join(taskDir, "workspace")); err != nil {
		return ImportFailureResult{}, fmt.Errorf("copy workspace: %w", err)
	}
	artifactsDir := filepath.Join(taskDir, "import_artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return ImportFailureResult{}, err
	}
	if err := copyImportArtifactFile(filepath.Join(attemptDir, "task.yaml"), filepath.Join(artifactsDir, "source_task.yaml"), runDir, attemptDir); err != nil {
		return ImportFailureResult{}, err
	}
	if err := copyImportArtifactFile(filepath.Join(attemptDir, "git.diff"), filepath.Join(artifactsDir, "git.diff"), runDir, attemptDir); err != nil {
		return ImportFailureResult{}, err
	}
	if err := copyDirIfExists(filepath.Join(attemptDir, "workspace-before"), filepath.Join(artifactsDir, "workspace-before")); err != nil {
		return ImportFailureResult{}, fmt.Errorf("copy workspace-before artifact: %w", err)
	}
	if err := copyDirIfExists(filepath.Join(attemptDir, "workspace-after"), filepath.Join(artifactsDir, "workspace-after")); err != nil {
		return ImportFailureResult{}, fmt.Errorf("copy workspace-after artifact: %w", err)
	}
	if err := writeImportAttemptArtifacts(artifactsDir, summary, task, attempt, runDir, attemptDir); err != nil {
		return ImportFailureResult{}, err
	}
	if err := writeImportReadme(filepath.Join(artifactsDir, "README.md"), summary, task, attempt); err != nil {
		return ImportFailureResult{}, err
	}

	return ImportFailureResult{
		TaskDir:    taskDir,
		TaskID:     options.OutID,
		SourceRun:  summary.RunID,
		SourceTask: task.ID,
		Attempt:    attempt.Attempt,
	}, nil
}

func findTaskResult(tasks []TaskResult, id string) (TaskResult, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return TaskResult{}, false
}

func selectFailedAttempt(task TaskResult, requested int) (AttemptResult, bool) {
	var selected AttemptResult
	for _, attempt := range task.Attempts {
		if attempt.Status == StatusPassed {
			continue
		}
		if requested > 0 && attempt.Attempt != requested {
			continue
		}
		selected = attempt
	}
	return selected, selected.Attempt != 0
}

func writeImportedTaskSkeleton(taskDir string, summary Summary, task TaskResult, attempt AttemptResult) error {
	content := fmt.Sprintf(`id: %s
title: Imported regression from %s
category: regression
suites:
  - %s
timeout: 300s
attempts: 1
tags:
  - imported
  - source-%s
prompt: |
  TODO: Minimize this imported Codex failure into a deterministic prompt.
  Source run: %s
  Source model: %s
  Source task: %s
  Source attempt: %d

  Start from the committed files in workspace/. Replace this prompt with the
  smallest task that reproduces the failure class without provider-specific
  noise or local paths.
expected:
  final_text_contains:
    - TODO_IMPORT_FINAL_MARKER
`, taskIDForYAML(filepath.Base(taskDir)),
		task.ID,
		importedRegressionSuite,
		task.ID,
		emptyAsUnknown(summary.RunID),
		emptyAsUnknown(summary.Environment.Model),
		task.ID,
		attempt.Attempt,
	)
	return os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte(content), 0o644)
}

func taskIDForYAML(id string) string {
	if !taskIDPattern.MatchString(id) {
		return "imported_regression"
	}
	return id
}

func emptyAsUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func copyImportArtifactFile(src, dst, runDir, attemptDir string) error {
	raw, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return os.WriteFile(dst, nil, 0o644)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(sanitizeImportedText(string(raw), runDir, attemptDir)), 0o644)
}

func writeImportAttemptArtifacts(artifactsDir string, summary Summary, task TaskResult, attempt AttemptResult, runDir, attemptDir string) error {
	if err := writeImportFinalText(filepath.Join(artifactsDir, "final_text.txt"), attempt, runDir, attemptDir); err != nil {
		return err
	}
	if err := writeImportCheckerFailures(filepath.Join(artifactsDir, "checker_failures.md"), attempt, runDir, attemptDir); err != nil {
		return err
	}
	source := map[string]any{
		"run_id":         summary.RunID,
		"model":          summary.Environment.Model,
		"provider":       summary.Environment.Provider,
		"suite":          summary.Environment.Suite,
		"task_id":        task.ID,
		"task_status":    task.Status,
		"bucket":         task.FailureBucket,
		"attempt":        attempt.Attempt,
		"attempt_status": attempt.Status,
		"attempt_bucket": attempt.FailureBucket,
		"exit_code":      attempt.ExitCode,
		"events": map[string]any{
			"total":             attempt.Events.Total,
			"types":             attempt.Events.Types,
			"agent_messages":    attempt.Events.AgentMessages,
			"command_started":   attempt.Events.CommandStarted,
			"command_completed": attempt.Events.CommandComplete,
			"file_changes":      attempt.Events.FileChanges,
			"tool_calls":        attempt.Events.ToolCalls,
			"turn_completed":    attempt.Events.TurnCompleted,
			"turn_failed":       attempt.Events.TurnFailed,
		},
	}
	raw, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(filepath.Join(artifactsDir, "source.json"), raw, 0o644)
}

func writeImportFinalText(path string, attempt AttemptResult, runDir, attemptDir string) error {
	return os.WriteFile(path, []byte(sanitizeImportedText(attempt.CheckResult.FinalText, runDir, attemptDir)), 0o644)
}

func writeImportCheckerFailures(path string, attempt AttemptResult, runDir, attemptDir string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Imported Checker Failures\n\n")
	if len(attempt.CheckResult.Failures) == 0 {
		fmt.Fprintf(&b, "No checker failures were recorded on the selected attempt.\n")
		return os.WriteFile(path, []byte(b.String()), 0o644)
	}
	for _, failure := range attempt.CheckResult.Failures {
		fmt.Fprintf(&b, "- `%s`: %s\n", failure.Kind, sanitizeImportedText(failure.Message, runDir, attemptDir))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeImportReadme(path string, summary Summary, task TaskResult, attempt AttemptResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Imported Regression Artifacts\n\n")
	fmt.Fprintf(&b, "- Source run: `%s`\n", emptyAsUnknown(summary.RunID))
	fmt.Fprintf(&b, "- Source model: `%s`\n", emptyAsUnknown(summary.Environment.Model))
	fmt.Fprintf(&b, "- Source task: `%s`\n", task.ID)
	fmt.Fprintf(&b, "- Source attempt: `%d`\n", attempt.Attempt)
	fmt.Fprintf(&b, "- Source status: `%s`\n", attempt.Status)
	if attempt.FailureBucket != "" {
		fmt.Fprintf(&b, "- Source bucket: `%s`\n", attempt.FailureBucket)
	}
	fmt.Fprintf(&b, "\n## Before Commit Checklist\n\n")
	fmt.Fprintf(&b, "- Replace `task.yaml` TODO prompt and checker with a minimized deterministic task.\n")
	fmt.Fprintf(&b, "- Remove provider-specific chatter that is not needed to reproduce the failure.\n")
	fmt.Fprintf(&b, "- Remove secrets, local absolute paths, raw `.tmp` paths, and unrelated generated files.\n")
	fmt.Fprintf(&b, "- Keep only the smallest workspace fixture required by the regression.\n")
	fmt.Fprintf(&b, "- Move the task from `%s` into the intended suite only after it is deterministic.\n", importedRegressionSuite)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func sanitizeImportedText(value, runDir, attemptDir string) string {
	replacements := []struct {
		old string
		new string
	}{
		{filepath.ToSlash(attemptDir), "<attempt-dir>"},
		{filepath.ToSlash(runDir), "<run-dir>"},
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		replacements = append(replacements, struct {
			old string
			new string
		}{filepath.ToSlash(home), "<home>"})
	}
	value = filepath.ToSlash(value)
	for _, replacement := range replacements {
		if replacement.old == "" {
			continue
		}
		value = strings.ReplaceAll(value, replacement.old, replacement.new)
	}
	return value
}
