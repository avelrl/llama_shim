package modelcert

import (
	"os"
	"path/filepath"
	"testing"

	"llama_shim/internal/config"
)

func TestLoadManifestNormalizesAndSelectsModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-certification.yaml")
	raw := []byte(`
models:
  - model: deepseek/deepseek-v4-pro
    tester:
      mode: ""
    codex:
      profiles: [baseline, "", bench-lite]
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Models[0].Tester.Mode; got != "compat" {
		t.Fatalf("expected default tester mode compat, got %q", got)
	}
	if got := manifest.Models[0].Codex.Profiles; len(got) != 2 || got[0] != "baseline" || got[1] != "bench-lite" {
		t.Fatalf("unexpected profiles: %#v", got)
	}
	selected, err := SelectModels(manifest, []string{"deepseek/deepseek-v4-pro"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 {
		t.Fatalf("expected one selected model, got %d", len(selected))
	}
}

func TestCompleteModelFromBaseFillsProviderAndCodexDefaults(t *testing.T) {
	cfg := config.Config{
		LlamaProviders: []config.LlamaProvider{
			{
				ID:             "deepseek",
				BaseURL:        "https://api.deepseek.com",
				BearerTokenEnv: "DEEPSEEK_API_KEY",
				Models: []config.LlamaProviderModel{
					{Model: "deepseek-v4-pro"},
				},
			},
		},
		ResponsesCodexModelMetadata: []config.ResponsesCodexModelMetadata{
			{
				Model:              "deepseek/deepseek-v4-pro",
				ContextWindow:      1000000,
				ApplyPatchToolType: "freeform",
				ShellType:          "shell_command",
			},
		},
	}

	model, err := CompleteModelFromBase(ModelEntry{Model: "deepseek/deepseek-v4-pro"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if model.Provider.ID != "deepseek" {
		t.Fatalf("provider id was not filled: %#v", model.Provider)
	}
	if model.Provider.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("provider base url was not filled: %#v", model.Provider)
	}
	if model.Provider.UpstreamModel != "deepseek-v4-pro" {
		t.Fatalf("upstream model was not defaulted: %#v", model.Provider)
	}
	if model.Codex.ContextWindow != 1000000 {
		t.Fatalf("codex context window was not filled: %#v", model.Codex)
	}
	if len(model.Codex.Profiles) != 3 {
		t.Fatalf("expected default codex profiles, got %#v", model.Codex.Profiles)
	}
}
