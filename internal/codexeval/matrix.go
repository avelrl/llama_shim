package codexeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func RenderMatrixMarkdown(paths []string) (string, error) {
	summaries, err := loadMatrixSummaries(paths)
	if err != nil {
		return "", err
	}
	sort.Slice(summaries, func(i, j int) bool {
		left := summaries[i].StartedAt
		right := summaries[j].StartedAt
		if left != right {
			return left < right
		}
		return summaries[i].RunID < summaries[j].RunID
	})

	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Matrix\n\n")
	fmt.Fprintf(&b, "Generated from %d run(s).\n\n", len(summaries))
	fmt.Fprintf(&b, "| Date | Run | Model | Suite | Result | Retries | Failure buckets | Failed tasks | Notes |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | ---: | ---: | --- | --- | --- |\n")
	for _, summary := range summaries {
		passed := summary.Counts[StatusPassed]
		total := len(summary.Tasks)
		failedTasks := matrixFailedTasks(summary)
		fmt.Fprintf(
			&b,
			"| %s | `%s` | `%s` | `%s` | %d/%d | %d | %s | %s | %s |\n",
			matrixDate(summary),
			summary.RunID,
			summary.Environment.Model,
			summary.Environment.Suite,
			passed,
			total,
			matrixRetryCount(summary),
			matrixBucketText(summary.FailureBuckets),
			matrixListText(failedTasks),
			matrixNotes(summary),
		)
	}
	return b.String(), nil
}

func loadMatrixSummaries(paths []string) ([]Summary, error) {
	if len(paths) == 0 {
		paths = []string{filepath.Join(".tmp", "codex-eval-runs")}
	}
	var summaries []Summary
	for _, path := range paths {
		loaded, err := loadMatrixSummariesFromPath(path)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, loaded...)
	}
	if len(summaries) == 0 {
		return nil, fmt.Errorf("no summary.json files found")
	}
	return summaries, nil
}

func loadMatrixSummariesFromPath(path string) ([]Summary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		summary, err := readMatrixSummary(path)
		if err != nil {
			return nil, err
		}
		return []Summary{summary}, nil
	}
	summaryPath := filepath.Join(path, "summary.json")
	if _, err := os.Stat(summaryPath); err == nil {
		summary, err := readMatrixSummary(summaryPath)
		if err != nil {
			return nil, err
		}
		return []Summary{summary}, nil
	}

	var summaries []Summary
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childSummary := filepath.Join(path, entry.Name(), "summary.json")
		if _, err := os.Stat(childSummary); err != nil {
			continue
		}
		summary, err := readMatrixSummary(childSummary)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func readMatrixSummary(path string) (Summary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", path, err)
	}
	var summary Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return Summary{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(summary.RunID) == "" {
		return Summary{}, fmt.Errorf("%s missing run_id", path)
	}
	return summary, nil
}

func matrixDate(summary Summary) string {
	for _, value := range []string{summary.StartedAt, summary.CompletedAt} {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC().Format("2006-01-02")
		}
	}
	return ""
}

func matrixRetryCount(summary Summary) int {
	retries := 0
	for _, task := range summary.Tasks {
		if task.Status != StatusPassed {
			continue
		}
		for _, attempt := range task.Attempts {
			if attempt.Status != StatusPassed {
				retries++
				break
			}
		}
	}
	return retries
}

func matrixFailedTasks(summary Summary) []string {
	var failed []string
	for _, task := range summary.Tasks {
		if task.Status == StatusPassed || task.Status == StatusSkipped || task.Status == StatusQuarantined {
			continue
		}
		if task.FailureBucket == "" {
			failed = append(failed, "`"+task.ID+"`")
			continue
		}
		failed = append(failed, fmt.Sprintf("`%s` (`%s`)", task.ID, task.FailureBucket))
	}
	return failed
}

func matrixBucketText(buckets map[string]int) string {
	if len(buckets) == 0 {
		return "none"
	}
	keys := sortedKeys(buckets)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, fmt.Sprintf("`%s`: %d", key, buckets[key]))
	}
	return strings.Join(values, ", ")
}

func matrixListText(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func matrixNotes(summary Summary) string {
	var notes []string
	if summary.Environment.GitDirty {
		notes = append(notes, "dirty git")
	}
	if retryCount := matrixRetryCount(summary); retryCount > 0 {
		notes = append(notes, fmt.Sprintf("retry-dependent: %d", retryCount))
	}
	if len(summary.FailureBuckets) == 0 && summary.Counts[StatusPassed] == len(summary.Tasks) && matrixRetryCount(summary) == 0 {
		notes = append(notes, "strict-clean")
	}
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "; ")
}
