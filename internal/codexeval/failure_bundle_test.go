package codexeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderFailureBundleMarkdown(t *testing.T) {
	runDir := t.TempDir()
	attemptDir := filepath.Join(runDir, "tasks", "basic_patch", "attempt-01")
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(attemptDir, "task.yaml"), "id: basic_patch\nprompt: fix it\n")
	mustWrite(t, filepath.Join(attemptDir, "git.diff"), "diff --git a/smoke_target.txt b/smoke_target.txt\n")
	mustWrite(t, filepath.Join(attemptDir, "failure.md"), "# Codex Eval Failure\n\n- failed\n")
	summary := Summary{
		RunID: "run-test",
		Environment: Environment{
			Suite:    "codex-real-upstream",
			Model:    "test-model",
			Provider: "gateway-shim",
		},
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
			{
				ID:            "basic_patch",
				Status:        StatusFailedChecker,
				FailureBucket: BucketCheckerDiff,
				ArtifactDir:   filepath.Join(runDir, "tasks", "basic_patch"),
				Attempts: []AttemptResult{
					{
						Attempt:       1,
						Status:        StatusFailedChecker,
						FailureBucket: BucketCheckerDiff,
						ArtifactDir:   attemptDir,
						CheckResult: CheckResult{
							FinalText: "PATCHED",
							Failures:  []CheckFailure{{Kind: "file_equals", Message: "smoke_target.txt content mismatch"}},
						},
						Events: CodexEventStats{
							Total:           3,
							Types:           []string{"item.completed", "turn.completed"},
							AgentMessages:   1,
							CommandStarted:  1,
							CommandComplete: 1,
							TurnCompleted:   true,
						},
					},
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatal(err)
	}

	markdown, err := RenderFailureBundleMarkdown(runDir)
	if err != nil {
		t.Fatalf("RenderFailureBundleMarkdown() error = %v", err)
	}
	for _, expected := range []string{
		"# Codex Eval Failure Bundle",
		"- Run: `run-test`",
		"## Task `basic_patch`",
		"- Bucket: `checker_diff`",
		"`file_equals`: smoke_target.txt content mismatch",
		"#### Task Manifest",
		"#### Git Diff",
		"tasks/basic_patch/attempt-01",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("bundle missing %q:\n%s", expected, markdown)
		}
	}
	if strings.Contains(markdown, "## Task `boot`") {
		t.Fatalf("bundle included passed task:\n%s", markdown)
	}
}

func TestRenderFailureBundleMarkdownRejectsGreenRun(t *testing.T) {
	runDir := t.TempDir()
	summary := Summary{
		RunID: "run-green",
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderFailureBundleMarkdown(runDir); err == nil {
		t.Fatalf("expected no failed tasks error")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
