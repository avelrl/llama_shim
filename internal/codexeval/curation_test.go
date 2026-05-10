package codexeval

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCurationReportDiscoversAutoRunsAndSkipsNestedProfiles(t *testing.T) {
	root := t.TempDir()
	autoDir := filepath.Join(root, "codex-eval-auto", "deepseek_full_20260504T103141Z")
	baselineDir := filepath.Join(autoDir, "profiles", "baseline")
	benchDir := filepath.Join(autoDir, "profiles", "bench-lite")

	auto := AutoReport{
		GeneratedAt: "2026-05-04T10:40:00Z",
		Status:      "failed",
		Profiles: []AutoProfileReport{
			{
				Name:        "baseline",
				Source:      baselineDir,
				Status:      StatusPassed,
				StrictClean: true,
				Counts:      map[string]int{DiagnosisOK: 11},
				Candidates: []CompareRunRef{{
					RunID:     "candidate-baseline",
					Model:     "deepseek-v4-pro",
					Suite:     "codex-real-upstream",
					StartedAt: "2026-05-04T10:31:41Z",
					Status:    StatusPassed,
					Passed:    11,
					Total:     11,
				}},
			},
			{
				Name:        "bench-lite",
				Source:      benchDir,
				Status:      "failed",
				StrictClean: false,
				Counts:      map[string]int{DiagnosisCandidateModel: 1, DiagnosisOK: 19},
				Candidates: []CompareRunRef{{
					RunID:     "candidate-bench",
					Model:     "deepseek-v4-pro",
					Suite:     "codex-bench-lite",
					StartedAt: "2026-05-04T10:50:00Z",
					Status:    "failed",
					Passed:    19,
					Total:     20,
				}},
				FailedTasks: []AutoTaskFinding{{
					Profile:   "bench-lite",
					Candidate: "candidate-bench",
					Task:      "command_pipeline",
					Diagnosis: DiagnosisCandidateModel,
					Status:    StatusFailedChecker,
					Bucket:    BucketCheckerDiff,
					Attempts:  2,
				}},
			},
		},
		Counts: map[string]int{DiagnosisCandidateModel: 1, DiagnosisOK: 30},
	}
	if err := writeJSON(filepath.Join(autoDir, "summary.json"), auto); err != nil {
		t.Fatalf("write auto summary: %v", err)
	}
	if err := writeJSON(filepath.Join(baselineDir, "summary.json"), CompareReport{
		GeneratedAt: "2026-05-04T10:41:00Z",
		Control:     CompareRunRef{RunID: "control", Passed: 20, Total: 20},
		Candidates:  []CandidateCompare{{Run: CompareRunRef{RunID: "nested-should-not-load", Model: "deepseek-v4-pro", Suite: "codex-real-upstream", Passed: 0, Total: 1}}},
		Counts:      map[string]int{DiagnosisCandidateTransport: 1},
	}); err != nil {
		t.Fatalf("write nested compare summary: %v", err)
	}

	report, err := BuildCurationReport(CurationOptions{Paths: []string{filepath.Join(root, "codex-eval-auto")}})
	if err != nil {
		t.Fatalf("build curation report: %v", err)
	}
	if report.Status != CurationStatusAttention {
		t.Fatalf("status = %q, want attention", report.Status)
	}
	if len(report.ProfileResults) != 2 {
		t.Fatalf("profile results = %d, want 2: %#v", len(report.ProfileResults), report.ProfileResults)
	}
	if report.DiagnosisCounts[DiagnosisCandidateTransport] != 0 {
		t.Fatalf("loaded nested compare unexpectedly: %#v", report.DiagnosisCounts)
	}
	if !hasRecommendation(report, "latest baseline is strict-clean") {
		t.Fatalf("missing strict-clean baseline recommendation: %#v", report.Recommendations)
	}
	if len(report.TaskTrends) != 1 || report.TaskTrends[0].Task != "command_pipeline" {
		t.Fatalf("task trends = %#v, want command_pipeline", report.TaskTrends)
	}
	markdown := RenderCurationReportMarkdown(report)
	for _, expected := range []string{
		"# Codex Eval Curation Report",
		"promote_baseline_after_log_spot_check",
		"command_pipeline",
		"Matrix Transfer Notes",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown missing %q:\n%s", expected, markdown)
		}
	}
}

func TestBuildCurationReportFiltersByModelAndSince(t *testing.T) {
	root := t.TempDir()
	oldRun := Summary{
		RunID:     "old-run",
		StartedAt: "2026-05-01T00:00:00Z",
		Environment: Environment{
			Model: "deepseek-v4-pro",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{StatusPassed: 1},
		Tasks:  []TaskResult{{ID: "boot", Status: StatusPassed, Attempts: []AttemptResult{{Attempt: 1, Status: StatusPassed}}}},
	}
	newRun := Summary{
		RunID:     "new-run",
		StartedAt: "2026-05-06T00:00:00Z",
		Environment: Environment{
			Model: "Qwen3.6-35B-A3B",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{StatusPassed: 1},
		Tasks:  []TaskResult{{ID: "boot", Status: StatusPassed, Attempts: []AttemptResult{{Attempt: 1, Status: StatusPassed}}}},
	}
	if err := writeJSON(filepath.Join(root, "runs", "old", "summary.json"), oldRun); err != nil {
		t.Fatalf("write old summary: %v", err)
	}
	if err := writeJSON(filepath.Join(root, "runs", "new", "summary.json"), newRun); err != nil {
		t.Fatalf("write new summary: %v", err)
	}

	report, err := BuildCurationReport(CurationOptions{
		Paths: []string{filepath.Join(root, "runs")},
		Model: "Qwen3.6-35B-A3B",
		Since: "2026-05-02",
	})
	if err != nil {
		t.Fatalf("build curation report: %v", err)
	}
	if len(report.ProfileResults) != 1 {
		t.Fatalf("profile results = %d, want 1", len(report.ProfileResults))
	}
	result := report.ProfileResults[0]
	if result.RunID != "new-run" || result.Model != "Qwen3.6-35B-A3B" {
		t.Fatalf("result = %#v, want new qwen run", result)
	}
	if result.Interpretation != "promote_baseline_after_log_spot_check" {
		t.Fatalf("interpretation = %q", result.Interpretation)
	}
}

func TestBuildCurationReportNormalizesProviderModelAliases(t *testing.T) {
	root := t.TempDir()
	run := Summary{
		RunID:     "kimi-run",
		StartedAt: "2026-05-07T12:00:00Z",
		Environment: Environment{
			Model: "Kimi-K2.6",
			Suite: "codex-real-upstream",
		},
		Counts: map[string]int{StatusPassed: 1},
		Tasks:  []TaskResult{{ID: "boot", Status: StatusPassed, Attempts: []AttemptResult{{Attempt: 1, Status: StatusPassed}}}},
	}
	if err := writeJSON(filepath.Join(root, "runs", "kimi", "summary.json"), run); err != nil {
		t.Fatalf("write run summary: %v", err)
	}

	report, err := BuildCurationReport(CurationOptions{
		Paths: []string{filepath.Join(root, "runs")},
		Model: "svgun/kimi-k2.6",
		ModelAliases: map[string]string{
			"Kimi-K2.6": "svgun/kimi-k2.6",
		},
	})
	if err != nil {
		t.Fatalf("build curation report: %v", err)
	}
	if len(report.ProfileResults) != 1 {
		t.Fatalf("profile results = %d, want 1", len(report.ProfileResults))
	}
	result := report.ProfileResults[0]
	if result.Model != "Kimi-K2.6" {
		t.Fatalf("raw model = %q, want Kimi-K2.6", result.Model)
	}
	if result.PublicModel != "svgun/kimi-k2.6" || result.CanonicalModel != "svgun/kimi-k2.6" {
		t.Fatalf("canonical/public = %q/%q, want svgun/kimi-k2.6", result.CanonicalModel, result.PublicModel)
	}
	if len(report.ModelSummaries) != 1 {
		t.Fatalf("model summaries = %d, want 1", len(report.ModelSummaries))
	}
	summary := report.ModelSummaries[0]
	if summary.Model != "svgun/kimi-k2.6" || summary.RawModels[0] != "Kimi-K2.6" {
		t.Fatalf("summary = %#v, want canonical model with raw model", summary)
	}
	markdown := RenderCurationReportMarkdown(report)
	if !strings.Contains(markdown, "Kimi-K2.6 -&gt; svgun/kimi-k2.6") && !strings.Contains(markdown, "Kimi-K2.6 -> svgun/kimi-k2.6") {
		t.Fatalf("markdown missing model alias display:\n%s", markdown)
	}
}

func hasRecommendation(report CurationReport, needle string) bool {
	for _, recommendation := range report.Recommendations {
		if strings.Contains(recommendation, needle) {
			return true
		}
	}
	return false
}
