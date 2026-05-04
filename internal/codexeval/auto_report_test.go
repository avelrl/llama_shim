package codexeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAutoReportClassifiesStrictAndRetryProfiles(t *testing.T) {
	root := t.TempDir()
	writeAutoCompareReport(t, filepath.Join(root, "baseline"), CompareReport{
		GeneratedAt: "2026-05-04T00:00:00Z",
		Control: CompareRunRef{
			RunID: "control", Model: "devstack-model", Suite: "codex-core", Scope: "control", Status: StatusPassed, Passed: 1, Total: 1,
		},
		Candidates: []CandidateCompare{
			{
				Run: CompareRunRef{
					RunID: "candidate-model-a", Model: "model-a", Suite: "codex-real-upstream", Scope: "real-stable", Status: StatusPassed, Passed: 1, Total: 1,
				},
				Counts: map[string]int{DiagnosisOK: 1},
				Tasks: []TaskCompare{
					{ID: "read_file", ControlStatus: StatusPassed, CandidateStatus: StatusPassed, CandidateAttempts: 1, Diagnosis: DiagnosisOK},
				},
			},
		},
		Counts: map[string]int{DiagnosisOK: 1},
	})
	writeAutoCompareReport(t, filepath.Join(root, "bench-lite"), CompareReport{
		GeneratedAt: "2026-05-04T00:00:00Z",
		Control: CompareRunRef{
			RunID: "control", Model: "devstack-model", Suite: "codex-bench-lite", Scope: "control", Status: StatusPassed, Passed: 1, Total: 1,
		},
		Candidates: []CandidateCompare{
			{
				Run: CompareRunRef{
					RunID: "candidate-model-a", Model: "model-a", Suite: "codex-bench-lite", Scope: "custom", Status: StatusPassed, Passed: 1, Total: 1,
				},
				Counts: map[string]int{DiagnosisRetryDependent: 1},
				Tasks: []TaskCompare{
					{ID: "patch_after_context", ControlStatus: StatusPassed, CandidateStatus: StatusPassed, CandidateAttempts: 2, RetryDependent: true, Diagnosis: DiagnosisRetryDependent},
				},
			},
		},
		Counts: map[string]int{DiagnosisRetryDependent: 1},
	})

	report, err := BuildAutoReport([]string{filepath.Join(root, "bench-lite"), filepath.Join(root, "baseline")})
	if err != nil {
		t.Fatalf("BuildAutoReport failed: %v", err)
	}
	if report.Status != StatusPassed {
		t.Fatalf("report status = %q, want passed", report.Status)
	}
	if got := len(report.Profiles); got != 2 {
		t.Fatalf("profiles = %d, want 2", got)
	}
	if report.Profiles[0].Name != "baseline" {
		t.Fatalf("first profile = %q, want baseline", report.Profiles[0].Name)
	}
	if !report.Profiles[0].StrictClean {
		t.Fatalf("baseline should be strict-clean")
	}
	if report.Profiles[1].StrictClean {
		t.Fatalf("bench-lite should not be strict-clean")
	}
	if report.Profiles[1].RetryDependent != 1 {
		t.Fatalf("bench-lite retry count = %d, want 1", report.Profiles[1].RetryDependent)
	}
	if AutoReportShouldFail(report, AutoStrictAll) {
		t.Fatalf("retry-dependent green report should not fail all-profile strictness")
	}
	markdown := RenderAutoReportMarkdown(report)
	for _, expected := range []string{
		"| `baseline` | `passed` | `codex-core` 1/1 | `model-a` 1/1 | `ok`: 1 | strict-clean |",
		"| `bench-lite` | `passed` | `codex-bench-lite` 1/1 | `model-a` 1/1 | `retry_dependent`: 1 | retry-dependent: 1 |",
		"| `bench-lite` | `candidate-model-a` | `patch_after_context` | `retry_dependent` | `passed` | `` | 2 |",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown missing %q:\n%s", expected, markdown)
		}
	}
}

func TestAutoReportShouldFailOnBaselineFailure(t *testing.T) {
	report := AutoReport{
		Status: StatusPassed,
		Profiles: []AutoProfileReport{
			{Name: "baseline", Status: "failed"},
			{Name: "bench-lite", Status: StatusPassed},
		},
	}
	if !AutoReportShouldFail(report, AutoStrictBaseline) {
		t.Fatalf("baseline strictness should fail when baseline failed")
	}
	if !AutoReportShouldFail(report, AutoStrictAll) {
		t.Fatalf("all strictness should fail when a profile failed")
	}
	if AutoReportShouldFail(report, AutoStrictNone) {
		t.Fatalf("none strictness should not fail")
	}
}

func TestBuildAutoReportSurfacesRetryDependentCoverageTasks(t *testing.T) {
	root := t.TempDir()
	writeAutoCompareReport(t, filepath.Join(root, "expanded"), CompareReport{
		GeneratedAt: "2026-05-04T00:00:00Z",
		Control: CompareRunRef{
			RunID: "control", Model: "devstack-model", Suite: "codex-core", Scope: "control", Status: StatusPassed, Passed: 1, Total: 1,
		},
		Candidates: []CandidateCompare{
			{
				Run: CompareRunRef{
					RunID: "candidate-model-a", Model: "model-a", Suite: "codex-real-upstream-expanded", Scope: "real-expanded", Status: StatusPassed, Passed: 1, Total: 1,
				},
				Counts: map[string]int{DiagnosisRetryDependent: 1},
				Coverage: []TaskCompare{
					{ID: "bugfix_mixed", CandidateStatus: StatusPassed, CandidateAttempts: 2, Diagnosis: DiagnosisCandidateOnlyTask},
				},
			},
		},
		Counts: map[string]int{DiagnosisRetryDependent: 1},
	})

	report, err := BuildAutoReport([]string{filepath.Join(root, "expanded")})
	if err != nil {
		t.Fatalf("BuildAutoReport failed: %v", err)
	}
	if got := len(report.Profiles[0].RetryTasks); got != 1 {
		t.Fatalf("retry tasks = %d, want 1", got)
	}
	markdown := RenderAutoReportMarkdown(report)
	expected := "| `expanded` | `candidate-model-a` | `bugfix_mixed` | `retry_dependent` | `passed` | `` | 2 |"
	if !strings.Contains(markdown, expected) {
		t.Fatalf("markdown missing %q:\n%s", expected, markdown)
	}
}

func writeAutoCompareReport(t *testing.T, dir string, report CompareReport) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), raw, 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
}
