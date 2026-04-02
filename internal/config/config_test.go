package config_test

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
)

func TestLoadFromYAMLFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
shim:
  addr: ":9090"
  read_timeout: 5s
  write_timeout: 30s
  idle_timeout: 45s
sqlite:
  path: ./tmp/test.db
llama:
  base_url: http://127.0.0.1:9091
  timeout: 12s
log:
  level: debug
responses:
  custom_tools:
    mode: bridge
  codex:
    force_tool_choice_required: true
`)

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.Addr)
	require.Equal(t, "./tmp/test.db", cfg.SQLitePath)
	require.Equal(t, "http://127.0.0.1:9091", cfg.LlamaBaseURL)
	require.Equal(t, 12*time.Second, cfg.LlamaTimeout)
	require.Equal(t, 5*time.Second, cfg.ReadTimeout)
	require.Equal(t, 30*time.Second, cfg.WriteTimeout)
	require.Equal(t, 45*time.Second, cfg.IdleTimeout)
	require.Equal(t, slog.LevelDebug, cfg.LogLevel)
	require.Equal(t, "bridge", cfg.ResponsesCustomToolsMode)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Equal(t, configPath, cfg.ConfigFile)
}

func TestEnvOverridesYAMLFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
shim:
  addr: ":9090"
llama:
  timeout: 12s
log:
  level: info
responses:
  codex:
    force_tool_choice_required: false
`)

	t.Setenv("SHIM_ADDR", ":7070")
	t.Setenv("LLAMA_TIMEOUT", "25s")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED", "true")

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, ":7070", cfg.Addr)
	require.Equal(t, 25*time.Second, cfg.LlamaTimeout)
	require.Equal(t, slog.LevelWarn, cfg.LogLevel)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
}
