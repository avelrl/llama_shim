package codexeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportFailureCreatesTaskSkeleton(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run")
	attemptDir := filepath.Join(runDir, "tasks", "basic_patch", "attempt-02")
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(attemptDir, "workspace-before"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(attemptDir, "workspace-after"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(attemptDir, "task.yaml"), "id: basic_patch\nprompt: fix it\n")
	mustWrite(t, filepath.Join(attemptDir, "git.diff"), "diff --git a/workspace-before/smoke_target.txt b/workspace-after/smoke_target.txt\n")
	mustWrite(t, filepath.Join(attemptDir, "workspace-before", "smoke_target.txt"), "status = TODO\n")
	mustWrite(t, filepath.Join(attemptDir, "workspace-after", "smoke_target.txt"), "status = wrong\n")
	summary := Summary{
		RunID: "run-import",
		Environment: Environment{
			Model:    "model-a",
			Provider: "gateway-shim",
			Suite:    "codex-real-upstream",
		},
		Tasks: []TaskResult{
			{
				ID:            "basic_patch",
				Status:        StatusFailedChecker,
				FailureBucket: BucketCheckerDiff,
				Attempts: []AttemptResult{
					{
						Attempt:       1,
						Status:        StatusFailedTimeout,
						FailureBucket: BucketTimeout,
						ArtifactDir:   filepath.Join(runDir, "tasks", "basic_patch", "attempt-01"),
					},
					{
						Attempt:       2,
						Status:        StatusFailedChecker,
						FailureBucket: BucketCheckerDiff,
						ArtifactDir:   attemptDir,
						CheckResult: CheckResult{
							FinalText: "PATCHED",
							Failures: []CheckFailure{
								{Kind: "file_equals", Message: "smoke_target.txt content mismatch"},
							},
						},
					},
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatal(err)
	}

	tasksDir := filepath.Join(root, "tasks")
	result, err := ImportFailure(ImportFailureOptions{
		RunPath:  runDir,
		TaskID:   "basic_patch",
		OutID:    "imported_basic_patch",
		TasksDir: tasksDir,
	})
	if err != nil {
		t.Fatalf("ImportFailure failed: %v", err)
	}
	if result.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", result.Attempt)
	}
	taskDir := filepath.Join(tasksDir, "imported_basic_patch")
	for _, path := range []string{
		"task.yaml",
		"workspace/smoke_target.txt",
		"import_artifacts/source_task.yaml",
		"import_artifacts/git.diff",
		"import_artifacts/workspace-before/smoke_target.txt",
		"import_artifacts/workspace-after/smoke_target.txt",
		"import_artifacts/final_text.txt",
		"import_artifacts/checker_failures.md",
		"import_artifacts/README.md",
	} {
		if _, err := os.Stat(filepath.Join(taskDir, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	rawManifest, err := os.ReadFile(filepath.Join(taskDir, "task.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawManifest), "TODO_IMPORT_FINAL_MARKER") {
		t.Fatalf("imported manifest missing TODO checker:\n%s", rawManifest)
	}
	tasks, err := LoadTasks(tasksDir, importedRegressionSuite)
	if err != nil {
		t.Fatalf("LoadTasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Manifest.ID != "imported_basic_patch" {
		t.Fatalf("loaded tasks = %#v", tasks)
	}
}

func TestImportFailureRejectsPassedTask(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run")
	summary := Summary{
		RunID: "run-green",
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatal(err)
	}
	_, err := ImportFailure(ImportFailureOptions{
		RunPath:  runDir,
		TaskID:   "boot",
		OutID:    "imported_boot",
		TasksDir: filepath.Join(root, "tasks"),
	})
	if err == nil {
		t.Fatalf("expected passed task rejection")
	}
}

func TestImportFailureRejectsUnsafeOutputID(t *testing.T) {
	_, err := ImportFailure(ImportFailureOptions{
		RunPath:  "run",
		TaskID:   "boot",
		OutID:    "../escape",
		TasksDir: "tasks",
	})
	if err == nil {
		t.Fatalf("expected unsafe output id rejection")
	}
}
