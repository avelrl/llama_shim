package modelcert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"llama_shim/internal/config"
)

func TestRenderShimConfigContainsOneProviderModelAndDiagnostics(t *testing.T) {
	model := ModelEntry{
		Model: "gpu/qwen3-coder-30b",
		Provider: ProviderEntry{
			ID:            "gpu",
			BaseURL:       "http://192.168.1.130:8000",
			UpstreamModel: "coder30b",
		},
		Codex: CodexConfig{
			ContextWindow:      32768,
			ApplyPatchToolType: "freeform",
		},
	}
	base := config.Config{
		ChatCompletionsUpstreamCompatibility: []config.ChatCompletionsUpstreamCompatibilityRule{
			{Model: "Qwen*", JSONSchemaMode: "json_object_instruction"},
			{Model: "coder30b", DefaultThinking: "passthrough", JSONSchemaMode: "json_object_instruction"},
		},
		ResponsesCodexUpstreamInputCompatibility: []config.ResponsesCodexUpstreamInputCompatibilityRule{
			{Model: "Kimi-*", Mode: "stringify"},
		},
	}

	raw, err := RenderShimConfig(RenderConfigOptions{
		Model:       model,
		BaseConfig:  base,
		ArtifactDir: ".tmp/model-certification/test/models/gpu-qwen3-coder-30b",
		Port:        18123,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"addr: 127.0.0.1:18123",
		"max_entries: 4096",
		"base_url: http://192.168.1.130:8000",
		"model: qwen3-coder-30b",
		"upstream_model: coder30b",
		"model: gpu/qwen3-coder-30b",
		"model: coder30b",
		"default_thinking: passthrough",
		"json_schema_mode: json_object_instruction",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, text)
		}
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("rendered config is not valid yaml: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIM_DOTENV", filepath.Join(t.TempDir(), "missing.env"))
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("rendered config must load through config.Load: %v", err)
	}
	if loaded.Addr != "127.0.0.1:18123" {
		t.Fatalf("expected generated shim addr, got %q", loaded.Addr)
	}
	if !strings.Contains(loaded.SQLitePath, "shim.db") || !strings.Contains(loaded.LogFilePath, "shim.log") {
		t.Fatalf("expected generated sqlite/log paths, got sqlite=%q log=%q", loaded.SQLitePath, loaded.LogFilePath)
	}
	if len(loaded.LlamaProviders) != 1 || len(loaded.LlamaProviders[0].Models) != 1 {
		t.Fatalf("expected isolated provider/model config, got %#v", loaded.LlamaProviders)
	}
}
