package codexeval

import (
	"fmt"
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

func TestRunCheckersFinalTextContainsFold(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"Ready."}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "boot",
		Expected: Expected{
			FinalTextContainsFold: []string{"READY"},
			CodexEvents:           []string{"turn.completed"},
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

func TestRunCheckersFileEqualsTrimSpace(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "status.txt"), []byte("status=updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(workspace, "codex.jsonl")
	if err := os.WriteFile(output, []byte(`{"type":"turn.completed"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "trim_space",
		Expected: Expected{
			Files: []FileExpectation{{Path: "status.txt", EqualsTrimSpace: "status=updated\n"}},
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

func TestRunCheckersRejectsQwenRawMarkup(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "mask", text: "<|mask_start|>tool_code\n{\"type\":\"console\"}\n<|mask_end|>"},
		{name: "ant_thinking", text: "<antThinking>\nI'll call the patch tool.\n</antThinking>"},
		{name: "tool_call_colon", text: "<toolCall::apply_patch>\ncommand: ['*** Begin Patch']\n</toolCall::apply_patch>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			output := filepath.Join(workspace, "codex.jsonl")
			raw := fmt.Sprintf(`{"type":"item.completed","item":{"type":"agent_message","text":%q}}
{"type":"turn.completed"}
`, tc.text)
			if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			manifest := Manifest{ID: "raw_marker"}
			result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
			if err != nil {
				t.Fatalf("runCheckers failed: %v", err)
			}
			if result.Passed {
				t.Fatalf("expected checker failure")
			}
			if got := result.Failures[0].Kind; got != "raw_tool_markup" {
				t.Fatalf("expected raw_tool_markup, got %s", got)
			}
		})
	}
}

func TestRunCheckersRejectsContextLeak(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"<environment_context>\n  <cwd>/repo</cwd>\n</environment_context>"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "context_leak"}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected checker failure")
	}
	if got := result.Failures[0].Kind; got != "context_leak" {
		t.Fatalf("expected context_leak, got %s", got)
	}
}

func TestRunCheckersRejectsForbiddenCodexEvent(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.started","item":{"type":"file_change"}}
{"type":"item.completed","item":{"type":"agent_message","text":"NO_EDIT_OK"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "no_edit",
		Expected: Expected{
			FinalTextContains:    []string{"NO_EDIT_OK"},
			ForbiddenCodexEvents: []string{"item.started:file_change"},
		},
	}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected checker failure")
	}
	if got := result.Failures[0].Kind; got != "forbidden_codex_event" {
		t.Fatalf("expected forbidden_codex_event, got %s", got)
	}
}

func TestRunCheckersRejectsPseudoApplyPatchMarkup(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"<apply_patch>\n<command>*** Begin Patch\n*** End Patch</command>\n</apply_patch>"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "raw_apply_patch"}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected checker failure")
	}
	if got := result.Failures[0].Kind; got != "raw_tool_markup" {
		t.Fatalf("expected raw_tool_markup, got %s", got)
	}
}

func TestRunCheckersRejectsFunctionCallMarkup(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"<function_call>\n{\"function\":\"exec_command\",\"command\":[\"cat\",\"README.md\"]}\n</function_call>"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "raw_function_call"}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected checker failure")
	}
	if got := result.Failures[0].Kind; got != "raw_tool_markup" {
		t.Fatalf("expected raw_tool_markup, got %s", got)
	}
}

func TestRunCheckersRejectsToolCodeCallMarkup(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"<tool_code_call>\nfunction {\"code\":\"cat README.md\"}\n</tool_code_call>"}}
{"type":"turn.completed"}
`
	if err := os.WriteFile(output, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "raw_tool_code_call"}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected checker failure")
	}
	if got := result.Failures[0].Kind; got != "raw_tool_markup" {
		t.Fatalf("expected raw_tool_markup, got %s", got)
	}
}

func TestRunCheckersClassifiesProviderToolMarkup(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	raw := `{"type":"item.completed","item":{"type":"agent_message","text":"<tool call: exec_command(\"cat README.md\")>"}}
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

func TestClassifyContextLeakFailure(t *testing.T) {
	status, bucket := classifyCheckFailure(CheckResult{
		Failures: []CheckFailure{{Kind: "context_leak", Message: "leaked context"}},
	}, "<environment_context>")
	if status != StatusFailedContextLeak || bucket != BucketContextLeak {
		t.Fatalf("unexpected classification: status=%s bucket=%s", status, bucket)
	}
}
