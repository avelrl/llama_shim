package codexeval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTasksSmokeSuite(t *testing.T) {
	tasks, err := LoadTasks("testdata/tasks", "codex-smoke")
	if err != nil {
		t.Fatalf("LoadTasks failed: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("expected smoke tasks")
	}
	for _, task := range tasks {
		if !task.Manifest.InSuite("codex-smoke") {
			t.Fatalf("loaded task outside suite: %s", task.Manifest.ID)
		}
	}
}

func TestManifestRejectsAbsoluteFilePath(t *testing.T) {
	manifest := Manifest{
		ID:       "bad_task",
		Suites:   []string{"codex-smoke"},
		Timeout:  "1s",
		Attempts: 1,
		Prompt:   "do it",
		Expected: Expected{
			Files: []FileExpectation{{Path: "/tmp/escape", Contains: "x"}},
		},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestRunCheckers(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello llama-shim-42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"thread.started","thread_id":"t1"}
{"type":"item.started","item":{"type":"command_execution"}}
{"type":"item.completed","item":{"type":"command_execution","status":"completed"}}
{"type":"item.completed","item":{"type":"agent_message","text":"READ_OK"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "read_file",
		Expected: Expected{
			FinalTextContains: []string{"READ_OK"},
			Files:             []FileExpectation{{Path: "README.md", Contains: "llama-shim-42"}},
			CodexEvents:       []string{"item.started:command_execution", "turn.completed"},
		},
	}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected checker pass, got %#v", result.Failures)
	}
}

func TestRunCheckersClassifiesProviderToolMarkup(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"<bash>cat README.md</bash>"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "raw_markup",
		Expected: Expected{
			FinalTextContains: []string{"DONE"},
		},
	}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected checker failure")
	}
	if result.Failures[0].Kind != "raw_tool_markup" {
		t.Fatalf("expected raw tool markup failure, got %#v", result.Failures)
	}
}

func TestClassifyFinalTextFailureIsCheckerDiff(t *testing.T) {
	status, bucket := classifyCheckFailure(CheckResult{
		Failures: []CheckFailure{{Kind: "final_text", Message: "missing sentinel"}},
	}, "done")
	if status != StatusFailedChecker || bucket != BucketCheckerDiff {
		t.Fatalf("unexpected classification: status=%s bucket=%s", status, bucket)
	}
}
