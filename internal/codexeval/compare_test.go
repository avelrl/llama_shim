package codexeval

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCompareMarkdownClassifiesCandidateFailures(t *testing.T) {
	root := t.TempDir()
	controlDir := filepath.Join(root, "control")
	candidateDir := filepath.Join(root, "candidate")
	control := Summary{
		RunID: "control",
		Environment: Environment{
			Model: "devstack-model",
			Suite: "codex-core",
		},
		Counts: map[string]int{StatusPassed: 3},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
			{ID: "basic_patch", Status: StatusPassed},
			{ID: "multi_file", Status: StatusPassed},
		},
	}
	candidate := Summary{
		RunID: "candidate",
		Environment: Environment{
			Model: "qwen-test",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{
			StatusPassed:        2,
			StatusFailedRawTool: 1,
		},
		FailureBuckets: map[string]int{BucketRawToolMarkup: 1},
		Tasks: []TaskResult{
			{
				ID:     "boot",
				Status: StatusPassed,
				Attempts: []AttemptResult{
					{Attempt: 1, Status: StatusPassed},
				},
			},
			{
				ID:            "basic_patch",
				Status:        StatusFailedRawTool,
				FailureBucket: BucketRawToolMarkup,
				Attempts: []AttemptResult{
					{Attempt: 1, Status: StatusFailedRawTool, FailureBucket: BucketRawToolMarkup},
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
	if err := writeJSON(filepath.Join(controlDir, "summary.json"), control); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(candidateDir, "summary.json"), candidate); err != nil {
		t.Fatal(err)
	}

	markdown, err := RenderCompareMarkdown(controlDir, []string{candidateDir})
	if err != nil {
		t.Fatalf("RenderCompareMarkdown failed: %v", err)
	}
	for _, expected := range []string{
		"`candidate_tool_contract`: 1",
		"`retry_dependent`: 1",
		"| `basic_patch` | `passed` | `failed_raw_tool_markup / raw_tool_markup` | `candidate_tool_contract` | `raw_tool_markup` | 1 |",
		"| `multi_file` | `passed` | `passed` | `retry_dependent` | `` | 2 |",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("compare markdown missing %q:\n%s", expected, markdown)
		}
	}
}

func TestRenderCompareMarkdownSeparatesCoverageDifferences(t *testing.T) {
	root := t.TempDir()
	controlDir := filepath.Join(root, "control")
	candidateDir := filepath.Join(root, "candidate")
	control := Summary{
		RunID: "control",
		Environment: Environment{
			Model: "devstack-model",
			Suite: "codex-core",
		},
		Counts: map[string]int{StatusPassed: 2},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
			{ID: "command_timeout", Status: StatusPassed},
		},
	}
	candidate := Summary{
		RunID: "candidate",
		Environment: Environment{
			Model: "real-model",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{StatusPassed: 2},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed, Attempts: []AttemptResult{{Attempt: 1, Status: StatusPassed}}},
			{ID: "bugfix_mixed", Status: StatusPassed, Attempts: []AttemptResult{{Attempt: 1, Status: StatusPassed}}},
		},
	}
	if err := writeJSON(filepath.Join(controlDir, "summary.json"), control); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(candidateDir, "summary.json"), candidate); err != nil {
		t.Fatal(err)
	}

	report, err := BuildCompareReport(controlDir, []string{candidateDir})
	if err != nil {
		t.Fatalf("BuildCompareReport failed: %v", err)
	}
	if got := report.Candidates[0].Counts[DiagnosisOK]; got != 1 {
		t.Fatalf("ok diagnosis count = %d, want 1", got)
	}
	if len(report.Candidates[0].Coverage) != 2 {
		t.Fatalf("coverage count = %d, want 2", len(report.Candidates[0].Coverage))
	}
	markdown := RenderCompareReportMarkdown(report)
	for _, expected := range []string{
		"### Coverage Differences",
		"| `command_timeout` | `passed` | `` | `candidate_missing_task` |",
		"| `bugfix_mixed` | `` | `passed` | `candidate_only_task` |",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("compare markdown missing %q:\n%s", expected, markdown)
		}
	}
}

func TestBuildCompareReportClassifiesControlFailure(t *testing.T) {
	root := t.TempDir()
	controlDir := filepath.Join(root, "control")
	candidateDir := filepath.Join(root, "candidate")
	control := Summary{
		RunID: "control",
		Environment: Environment{
			Model: "devstack-model",
			Suite: "codex-core",
		},
		Counts: map[string]int{StatusFailedChecker: 1},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusFailedChecker, FailureBucket: BucketHarnessBug},
		},
	}
	candidate := Summary{
		RunID: "candidate",
		Environment: Environment{
			Model: "real-model",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{StatusFailedTransport: 1},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusFailedTransport, FailureBucket: BucketUpstreamHTTP},
		},
	}
	if err := writeJSON(filepath.Join(controlDir, "summary.json"), control); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(candidateDir, "summary.json"), candidate); err != nil {
		t.Fatal(err)
	}

	report, err := BuildCompareReport(controlDir, []string{candidateDir})
	if err != nil {
		t.Fatalf("BuildCompareReport failed: %v", err)
	}
	if got := report.Candidates[0].Tasks[0].Diagnosis; got != DiagnosisControlFailed {
		t.Fatalf("diagnosis = %q, want %q", got, DiagnosisControlFailed)
	}
}
