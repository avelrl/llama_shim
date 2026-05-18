package modelcert

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunnerWritesArtifactsWhenShimTesterAndCodexAreSkipped(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath, baseConfigPath := writeRunnerFixture(t, tempDir)
	outDir := filepath.Join(tempDir, "out")

	summary, err := NewRunner(RunOptions{
		ManifestPath:   manifestPath,
		BaseConfigPath: baseConfigPath,
		OutDir:         outDir,
		RunID:          "test-run",
		SkipShim:       true,
		SkipTester:     true,
		SkipCodex:      true,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "passed" {
		t.Fatalf("expected passed summary, got %#v", summary)
	}
	for _, path := range []string{
		filepath.Join(outDir, "summary.json"),
		filepath.Join(outDir, "summary.md"),
		filepath.Join(outDir, "models", "gpu-qwen3-coder-30b", "shim", "config.yaml"),
		filepath.Join(outDir, "models", "gpu-qwen3-coder-30b", "failure-notes.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
}

func TestRunnerTesterFailureSkipsCodex(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath, baseConfigPath := writeRunnerFixture(t, tempDir)
	outDir := filepath.Join(tempDir, "out")

	summary, err := NewRunner(RunOptions{
		ManifestPath:   manifestPath,
		BaseConfigPath: baseConfigPath,
		OutDir:         outDir,
		RunID:          "tester-fails",
		SkipShim:       true,
		TesterCommand:  "exit 7",
	}).Run(context.Background())
	if err != ErrRunFailed {
		t.Fatalf("expected ErrRunFailed, got summary=%#v err=%v", summary, err)
	}
	if summary.Models[0].Verdict != VerdictAPICompatFailed {
		t.Fatalf("expected api compat failure, got %#v", summary.Models[0])
	}
	if summary.Models[0].Codex.Status != "skipped" {
		t.Fatalf("expected codex to be skipped, got %#v", summary.Models[0].Codex)
	}
}

func TestRunnerGreenTesterStartsCodexPhase(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath, baseConfigPath := writeRunnerFixture(t, tempDir)
	outDir := filepath.Join(tempDir, "out")

	summary, err := NewRunner(RunOptions{
		ManifestPath:       manifestPath,
		BaseConfigPath:     baseConfigPath,
		OutDir:             outDir,
		RunID:              "codex-starts",
		SkipShim:           true,
		TesterCommand:      "true",
		CodexAutoCommand:   `mkdir -p "$CODEX_EVAL_AUTO_OUT" && printf '{"status":"passed"}\n' > "$CODEX_EVAL_AUTO_OUT/summary.json"`,
		CodexCurateCommand: "true",
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("expected green run, got summary=%#v err=%v", summary, err)
	}
	if summary.Models[0].Tester.Status != "passed" || summary.Models[0].Codex.Status != "passed" {
		t.Fatalf("expected tester and codex pass, got %#v", summary.Models[0])
	}
	if summary.Models[0].Verdict != VerdictCodexClean {
		t.Fatalf("expected codex clean verdict, got %#v", summary.Models[0])
	}
}

func writeRunnerFixture(t *testing.T, dir string) (string, string) {
	t.Helper()
	manifestPath := filepath.Join(dir, "model-certification.yaml")
	baseConfigPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(manifestPath, []byte(`
models:
  - model: gpu/qwen3-coder-30b
    provider:
      id: gpu
      base_url: http://127.0.0.1:8000
      upstream_model: coder30b
    codex:
      profiles: [baseline]
      context_window: 32768
      apply_patch_tool_type: freeform
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseConfigPath, []byte(`
llama:
  base_url: http://127.0.0.1:8000
  providers:
    - id: gpu
      base_url: http://127.0.0.1:8000
      models:
        - model: qwen3-coder-30b
          upstream_model: coder30b
responses:
  codex:
    model_metadata:
      models:
        - model: gpu/qwen3-coder-30b
          context_window: 32768
          max_context_window: 32768
          apply_patch_tool_type: freeform
          shell_type: shell_command
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath, baseConfigPath
}
