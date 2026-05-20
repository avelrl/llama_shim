package modelcert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		CodexRunnerCommand: `printf '%s\n' "$CODEX_EVAL_SUITE" > "$CODEX_EVAL_OUT/suite.txt" && test "$CODEX_EVAL_SUITE" = codex-real-upstream && test "$CODEX_BASE_URL" = "$SHIM_BASE_URL/v1"`,
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
	suitePath := filepath.Join(outDir, "models", "gpu-qwen3-coder-30b", "codex", "profiles", "baseline", "suite.txt")
	raw, err := os.ReadFile(suitePath)
	if err != nil {
		t.Fatalf("expected suite artifact %s: %v", suitePath, err)
	}
	if strings.TrimSpace(string(raw)) != "codex-real-upstream" {
		t.Fatalf("expected direct candidate suite, got %q", string(raw))
	}
}

func TestRunnerCodexProfilesOptionOverridesManifest(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath, baseConfigPath := writeRunnerFixture(t, tempDir)
	outDir := filepath.Join(tempDir, "out")

	summary, err := NewRunner(RunOptions{
		ManifestPath:       manifestPath,
		BaseConfigPath:     baseConfigPath,
		OutDir:             outDir,
		RunID:              "codex-profile-override",
		SkipShim:           true,
		SkipTester:         true,
		CodexProfiles:      []string{"expanded"},
		CodexRunnerCommand: `printf '%s\n' "$CODEX_EVAL_SUITE" > "$CODEX_EVAL_OUT/suite.txt" && test "$CODEX_EVAL_SUITE" = codex-real-upstream-expanded`,
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("expected green run, got summary=%#v err=%v", summary, err)
	}
	if summary.Models[0].Codex.Status != "passed" {
		t.Fatalf("expected codex pass, got %#v", summary.Models[0])
	}
	baselinePath := filepath.Join(outDir, "models", "gpu-qwen3-coder-30b", "codex", "profiles", "baseline", "suite.txt")
	if _, err := os.Stat(baselinePath); !os.IsNotExist(err) {
		t.Fatalf("manifest baseline profile should not run when overridden, stat err=%v", err)
	}
	expandedPath := filepath.Join(outDir, "models", "gpu-qwen3-coder-30b", "codex", "profiles", "expanded", "suite.txt")
	raw, err := os.ReadFile(expandedPath)
	if err != nil {
		t.Fatalf("expected expanded suite artifact %s: %v", expandedPath, err)
	}
	if strings.TrimSpace(string(raw)) != "codex-real-upstream-expanded" {
		t.Fatalf("expected expanded suite, got %q", string(raw))
	}
}

func TestWriteGeneratedTesterModelsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.generated.yaml")
	if err := writeGeneratedTesterModelsConfig(path, "model-cert-gpu-qwen", "gpu/qwen3-coder-30b"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`name: "model-cert-gpu-qwen"`,
		`chat_model: "gpu/qwen3-coder-30b"`,
		`responses_model: "gpu/qwen3-coder-30b"`,
		`reasoning_effort: "minimal"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated tester models config missing %q:\n%s", want, text)
		}
	}
}

func TestRunTesterUsesAbsoluteOutDirForExternalTester(t *testing.T) {
	tempDir := t.TempDir()
	artifactDir := filepath.Join(tempDir, "artifacts")
	testerCheckout := filepath.Join(tempDir, "tester")
	if err := os.MkdirAll(testerCheckout, 0o755); err != nil {
		t.Fatal(err)
	}

	entry := ModelEntry{
		Model: "gpu/gpt-oss-20b",
		Tester: TesterConfig{
			Mode: "compat",
		},
	}
	runner := NewRunner(RunOptions{})
	_ = runner.runTester(context.Background(), RunOptions{
		ExternalTesterDir: testerCheckout,
		RequireTester:     true,
	}, entry, artifactDir, "http://127.0.0.1:18080")

	raw, err := os.ReadFile(filepath.Join(artifactDir, "external-tester", "command.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(artifactDir, "external-tester")
	command := string(raw)
	if !strings.Contains(command, "--base-url "+shellQuote("http://127.0.0.1:18080")) {
		t.Fatalf("tester command should pin the run-local shim base URL:\n%s", command)
	}
	if !strings.Contains(command, "--out-dir "+shellQuote(want)) {
		t.Fatalf("tester command should use absolute artifact out-dir %q:\n%s", want, command)
	}
}

func TestCodexSuiteForCertificationProfile(t *testing.T) {
	tests := map[string]string{
		"baseline":                    "codex-real-upstream",
		"expanded":                    "codex-real-upstream-expanded",
		"bench-lite":                  "codex-bench-lite",
		"codex-shim-native-websocket": "codex-shim-native-websocket",
	}
	for profile, want := range tests {
		got, err := codexSuiteForCertificationProfile(profile)
		if err != nil {
			t.Fatalf("profile %q: %v", profile, err)
		}
		if got != want {
			t.Fatalf("profile %q: got %q, want %q", profile, got, want)
		}
	}
	if _, err := codexSuiteForCertificationProfile("unknown"); err == nil {
		t.Fatal("expected unknown profile error")
	}
}

func TestIsolatedShimEnvDropsShimConfigOverridesButKeepsProviderTokens(t *testing.T) {
	env := isolatedShimEnv([]string{
		"PATH=/bin",
		"HOME=/tmp/home",
		"SHIM_ADDR=:8080",
		"SHIM_DOTENV=.env",
		"SQLITE_PATH=./.data/shim.db",
		"LOG_FILE_PATH=./.data/shim.log",
		"LLAMA_BASE_URL=http://127.0.0.1:8081",
		"RESPONSES_MODE=prefer_local",
		"DEEPSEEK_API_KEY=secret",
		"SVGUN_API_KEY=secret",
		"MODEL_CERT_PHASE=api",
	}, "/tmp/model/shim")
	text := "\n" + strings.Join(env, "\n") + "\n"
	for _, dropped := range []string{
		"\nSHIM_ADDR=",
		"\nSHIM_DOTENV=.env",
		"\nSQLITE_PATH=",
		"\nLOG_FILE_PATH=",
		"\nLLAMA_BASE_URL=",
		"\nRESPONSES_MODE=",
	} {
		if strings.Contains(text, dropped) {
			t.Fatalf("isolated shim env retained %q in:\n%s", dropped, text)
		}
	}
	for _, kept := range []string{
		"\nPATH=/bin\n",
		"\nDEEPSEEK_API_KEY=secret\n",
		"\nSVGUN_API_KEY=secret\n",
		"\nSHIM_DOTENV=/tmp/model/shim/missing.env\n",
	} {
		if !strings.Contains(text, kept) {
			t.Fatalf("isolated shim env missing %q in:\n%s", kept, text)
		}
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
