package codexeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteCodexConfigDoesNotAddQwenDeveloperInstructions(t *testing.T) {
	runner := NewRunner(Config{
		Model:    "Qwen3.6-35B-A3B",
		Provider: "eval-provider",
		BaseURL:  "http://127.0.0.1:8080/v1",
	})
	path := filepath.Join(t.TempDir(), "codex-home", "config.toml")

	if err := runner.writeCodexConfig(path); err != nil {
		t.Fatalf("writeCodexConfig() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "developer_instructions") {
		t.Fatalf("generated config unexpectedly contains developer_instructions:\n%s", raw)
	}
}

func TestWriteCodexConfigDoesNotAddDeveloperInstructionsForOtherModels(t *testing.T) {
	runner := NewRunner(Config{
		Model:    "deepseek-v4-pro",
		Provider: "eval-provider",
		BaseURL:  "http://127.0.0.1:8080/v1",
	})
	path := filepath.Join(t.TempDir(), "codex-home", "config.toml")

	if err := runner.writeCodexConfig(path); err != nil {
		t.Fatalf("writeCodexConfig() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "developer_instructions") {
		t.Fatalf("generated config unexpectedly contains developer_instructions:\n%s", raw)
	}
}

func TestWriteCodexConfigAddsApplyPatchModelCatalog(t *testing.T) {
	runner := NewRunner(Config{
		Model:              "devstack-model",
		Provider:           "eval-provider",
		BaseURL:            "http://127.0.0.1:8080/v1",
		ApplyPatchToolType: "function",
	})
	path := filepath.Join(t.TempDir(), "codex-home", "config.toml")

	if err := runner.writeCodexConfig(path); err != nil {
		t.Fatalf("writeCodexConfig() error = %v", err)
	}
	rawConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	if !strings.Contains(string(rawConfig), "model_catalog_json = ") {
		t.Fatalf("generated config does not contain model_catalog_json:\n%s", rawConfig)
	}
	rawCatalog, err := os.ReadFile(filepath.Join(filepath.Dir(path), "model-catalog.json"))
	if err != nil {
		t.Fatalf("ReadFile(catalog) error = %v", err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(rawCatalog, &catalog); err != nil {
		t.Fatalf("Unmarshal(catalog) error = %v", err)
	}
	if len(catalog.Models) != 1 {
		t.Fatalf("catalog models = %d, want 1", len(catalog.Models))
	}
	if got := catalog.Models[0]["apply_patch_tool_type"]; got != "function" {
		t.Fatalf("apply_patch_tool_type = %v, want function", got)
	}
}

func TestWriteCodexConfigCanDisableApplyPatchModelCatalog(t *testing.T) {
	runner := NewRunner(Config{
		Model:              "devstack-model",
		Provider:           "eval-provider",
		BaseURL:            "http://127.0.0.1:8080/v1",
		ApplyPatchToolType: "disabled",
	})
	path := filepath.Join(t.TempDir(), "codex-home", "config.toml")

	if err := runner.writeCodexConfig(path); err != nil {
		t.Fatalf("writeCodexConfig() error = %v", err)
	}
	rawConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	if !strings.Contains(string(rawConfig), "apply_patch_freeform = false") {
		t.Fatalf("generated config did not disable apply_patch_freeform:\n%s", rawConfig)
	}
	rawCatalog, err := os.ReadFile(filepath.Join(filepath.Dir(path), "model-catalog.json"))
	if err != nil {
		t.Fatalf("ReadFile(catalog) error = %v", err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(rawCatalog, &catalog); err != nil {
		t.Fatalf("Unmarshal(catalog) error = %v", err)
	}
	if got, ok := catalog.Models[0]["apply_patch_tool_type"]; !ok || got != nil {
		t.Fatalf("apply_patch_tool_type = %#v, want JSON null", got)
	}
}

func TestLoadSelectedTasksFiltersExplicitTaskIDsAcrossSuites(t *testing.T) {
	runner := NewRunner(Config{
		TasksDir: "testdata/tasks",
		Suite:    "codex-smoke",
		TaskIDs:  []string{"bugfix_mixed", "read_file"},
	})

	tasks, err := runner.loadSelectedTasks()
	if err != nil {
		t.Fatalf("loadSelectedTasks() error = %v", err)
	}
	if got, want := taskIDs(tasks), []string{"bugfix_mixed", "read_file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task ids = %v, want %v", got, want)
	}
}

func TestLoadSelectedTasksRerunsFailedTasksFromSummary(t *testing.T) {
	runDir := t.TempDir()
	summary := Summary{
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
			{ID: "bugfix_mixed", Status: StatusFailedChecker},
			{ID: "plan_doc", Status: StatusQuarantined},
			{ID: "multi_file", Status: StatusFailedTimeout},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	runner := NewRunner(Config{
		TasksDir:        "testdata/tasks",
		Suite:           "codex-smoke",
		RerunFailedFrom: runDir,
	})

	tasks, err := runner.loadSelectedTasks()
	if err != nil {
		t.Fatalf("loadSelectedTasks() error = %v", err)
	}
	if got, want := taskIDs(tasks), []string{"bugfix_mixed", "multi_file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task ids = %v, want %v", got, want)
	}
}

func TestLoadSelectedTasksRerunFailsWhenSummaryHasNoFailures(t *testing.T) {
	runDir := t.TempDir()
	summary := Summary{
		Tasks: []TaskResult{
			{ID: "boot", Status: StatusPassed},
			{ID: "plan_doc", Status: StatusSkipped},
		},
	}
	if err := writeJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	runner := NewRunner(Config{
		TasksDir:        "testdata/tasks",
		RerunFailedFrom: runDir,
	})

	if _, err := runner.loadSelectedTasks(); err == nil {
		t.Fatalf("expected no failed tasks error")
	}
}

func taskIDs(tasks []Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.Manifest.ID)
	}
	return ids
}
