package codexeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	bundleTextLimit     = 4000
	bundleManifestLimit = 8000
	bundleDiffLimit     = 12000
)

func RenderFailureBundleMarkdown(path string) (string, error) {
	summary, runDir, err := readBundleSummary(path)
	if err != nil {
		return "", err
	}

	failed := make([]TaskResult, 0)
	for _, task := range summary.Tasks {
		if task.Status == StatusPassed || task.Status == StatusSkipped || task.Status == StatusQuarantined {
			continue
		}
		failed = append(failed, task)
	}
	if len(failed) == 0 {
		return "", fmt.Errorf("no failed tasks in %s", path)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Failure Bundle\n\n")
	fmt.Fprintf(&b, "- Run: `%s`\n", summary.RunID)
	fmt.Fprintf(&b, "- Suite: `%s`\n", summary.Environment.Suite)
	fmt.Fprintf(&b, "- Model: `%s`\n", summary.Environment.Model)
	fmt.Fprintf(&b, "- Provider: `%s`\n", summary.Environment.Provider)
	fmt.Fprintf(&b, "- Failed tasks: `%d`\n\n", len(failed))

	for _, task := range failed {
		writeTaskFailureBundle(&b, runDir, task)
	}
	return b.String(), nil
}

func readBundleSummary(path string) (Summary, string, error) {
	summaryPath := path
	if info, err := os.Stat(summaryPath); err == nil && info.IsDir() {
		summaryPath = filepath.Join(summaryPath, "summary.json")
	}
	raw, err := os.ReadFile(summaryPath)
	if err != nil {
		return Summary{}, "", fmt.Errorf("read %s: %w", summaryPath, err)
	}
	var summary Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return Summary{}, "", fmt.Errorf("parse %s: %w", summaryPath, err)
	}
	if strings.TrimSpace(summary.RunID) == "" {
		return Summary{}, "", fmt.Errorf("%s missing run_id", summaryPath)
	}
	return summary, filepath.Dir(summaryPath), nil
}

func writeTaskFailureBundle(b *strings.Builder, runDir string, task TaskResult) {
	fmt.Fprintf(b, "## Task `%s`\n\n", task.ID)
	fmt.Fprintf(b, "- Status: `%s`\n", task.Status)
	if task.FailureBucket != "" {
		fmt.Fprintf(b, "- Bucket: `%s`\n", task.FailureBucket)
	}
	fmt.Fprintf(b, "- Attempts: `%d`\n", len(task.Attempts))
	if task.ArtifactDir != "" {
		fmt.Fprintf(b, "- Artifact: `%s`\n", displayArtifactPath(runDir, task.ArtifactDir))
	}
	fmt.Fprintf(b, "\n")

	for _, attempt := range task.Attempts {
		if attempt.Status == StatusPassed {
			continue
		}
		writeAttemptFailureBundle(b, runDir, task.ID, attempt)
	}
}

func writeAttemptFailureBundle(b *strings.Builder, runDir, taskID string, attempt AttemptResult) {
	attemptDir := resolveAttemptDir(runDir, taskID, attempt)
	fmt.Fprintf(b, "### Attempt `%02d`\n\n", attempt.Attempt)
	fmt.Fprintf(b, "- Status: `%s`\n", attempt.Status)
	if attempt.FailureBucket != "" {
		fmt.Fprintf(b, "- Bucket: `%s`\n", attempt.FailureBucket)
	}
	if attempt.ExitCode != 0 {
		fmt.Fprintf(b, "- Exit code: `%d`\n", attempt.ExitCode)
	}
	if attempt.Error != "" {
		fmt.Fprintf(b, "- Error: `%s`\n", truncateInline(attempt.Error, 500))
	}
	fmt.Fprintf(b, "- Artifact: `%s`\n", displayArtifactPath(runDir, attemptDir))
	fmt.Fprintf(b, "\n")

	writeEventSummary(b, attempt.Events)
	writeCheckerFailures(b, attempt.CheckResult)

	if attempt.CheckResult.FinalText != "" {
		writeBundleCodeBlock(b, "Final Text", "text", attempt.CheckResult.FinalText, bundleTextLimit)
	}
	writeBundleFileIfExists(b, "Task Manifest", "yaml", filepath.Join(attemptDir, "task.yaml"), bundleManifestLimit)
	writeBundleFileIfExists(b, "Git Diff", "diff", filepath.Join(attemptDir, "git.diff"), bundleDiffLimit)
	writeBundleFileIfExists(b, "Failure Notes", "markdown", filepath.Join(attemptDir, "failure.md"), bundleManifestLimit)
}

func writeEventSummary(b *strings.Builder, stats CodexEventStats) {
	fmt.Fprintf(b, "#### Codex Event Summary\n\n")
	fmt.Fprintf(b, "- Total events: `%d`\n", stats.Total)
	fmt.Fprintf(b, "- Event types: `%s`\n", strings.Join(stats.Types, ", "))
	fmt.Fprintf(b, "- Agent messages: `%d`\n", stats.AgentMessages)
	fmt.Fprintf(b, "- Commands: `%d started`, `%d completed`\n", stats.CommandStarted, stats.CommandComplete)
	fmt.Fprintf(b, "- File changes: `%d`\n", stats.FileChanges)
	fmt.Fprintf(b, "- Tool calls: `%d`\n", stats.ToolCalls)
	fmt.Fprintf(b, "- Turn completed: `%t`\n", stats.TurnCompleted)
	fmt.Fprintf(b, "- Turn failed: `%t`\n\n", stats.TurnFailed)
}

func writeCheckerFailures(b *strings.Builder, check CheckResult) {
	if len(check.Failures) == 0 {
		return
	}
	fmt.Fprintf(b, "#### Checker Failures\n\n")
	for _, failure := range check.Failures {
		fmt.Fprintf(b, "- `%s`: %s\n", failure.Kind, failure.Message)
	}
	fmt.Fprintf(b, "\n")
}

func writeBundleFileIfExists(b *strings.Builder, title, info, path string, limit int) {
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return
	}
	writeBundleCodeBlock(b, title, info, string(raw), limit)
}

func writeBundleCodeBlock(b *strings.Builder, title, info, value string, limit int) {
	fmt.Fprintf(b, "#### %s\n\n", title)
	fmt.Fprintf(b, "````%s\n%s\n````\n\n", info, truncateBlock(value, limit))
}

func resolveAttemptDir(runDir, taskID string, attempt AttemptResult) string {
	if attempt.ArtifactDir != "" {
		if filepath.IsAbs(attempt.ArtifactDir) {
			return attempt.ArtifactDir
		}
		if _, err := os.Stat(attempt.ArtifactDir); err == nil {
			return attempt.ArtifactDir
		}
		return filepath.Join(runDir, attempt.ArtifactDir)
	}
	return filepath.Join(runDir, "tasks", taskID, fmt.Sprintf("attempt-%02d", attempt.Attempt))
}

func displayArtifactPath(runDir, path string) string {
	if rel, err := filepath.Rel(runDir, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func truncateBlock(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...(truncated)"
}

func truncateInline(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", `\n`))
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}
