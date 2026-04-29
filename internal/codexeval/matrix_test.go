package codexeval

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderMatrixMarkdown(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run-1")
	summary := Summary{
		RunID:       "run-1",
		StartedAt:   "2026-04-29T20:20:49Z",
		CompletedAt: "2026-04-29T20:21:49Z",
		Environment: Environment{
			Model:    "model-a",
			Suite:    "codex-real-upstream",
			GitDirty: true,
		},
		Counts: map[string]int{
			StatusPassed: 2,
		},
		FailureBuckets: map[string]int{},
		Tasks: []TaskResult{
			{
				ID:     "boot",
				Status: StatusPassed,
				Attempts: []AttemptResult{
					{Attempt: 1, Status: StatusPassed},
				},
			},
			{
				ID:     "multi_file",
				Status: StatusPassed,
				Attempts: []AttemptResult{
					{Attempt: 1, Status: StatusFailedNoToolEvent, FailureBucket: BucketModelNoTool},
					{Attempt: 2, Status: StatusPassed},
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatal(err)
	}

	markdown, err := RenderMatrixMarkdown([]string{root})
	if err != nil {
		t.Fatalf("RenderMatrixMarkdown failed: %v", err)
	}
	for _, expected := range []string{
		"# Codex Eval Matrix",
		"| 2026-04-29 | `run-1` | `model-a` | `codex-real-upstream` | 2/2 | 1 | none | none | dirty git; retry-dependent: 1 |",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("matrix markdown missing %q:\n%s", expected, markdown)
		}
	}
}

func TestRenderMatrixMarkdownShowsFailures(t *testing.T) {
	root := t.TempDir()
	summary := Summary{
		RunID:     "run-failed",
		StartedAt: "2026-04-29T20:20:49Z",
		Environment: Environment{
			Model: "model-b",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{
			StatusPassed:        1,
			StatusFailedRawTool: 1,
		},
		FailureBuckets: map[string]int{
			BucketRawToolMarkup: 1,
		},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
			{ID: "plan_doc", Status: StatusFailedRawTool, FailureBucket: BucketRawToolMarkup},
		},
	}
	if err := writeJSON(filepath.Join(root, "summary.json"), summary); err != nil {
		t.Fatal(err)
	}

	markdown, err := RenderMatrixMarkdown([]string{filepath.Join(root, "summary.json")})
	if err != nil {
		t.Fatalf("RenderMatrixMarkdown failed: %v", err)
	}
	for _, expected := range []string{
		"`raw_tool_markup`: 1",
		"`plan_doc` (`raw_tool_markup`)",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("matrix markdown missing %q:\n%s", expected, markdown)
		}
	}
}
