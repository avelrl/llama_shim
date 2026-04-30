package codexeval

import (
	"os"
	"path/filepath"
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
