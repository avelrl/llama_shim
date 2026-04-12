package config_test

import (
	"log/slog"
	"os"
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
  file_path: ./tmp/shim.log
responses:
  mode: prefer_upstream
  custom_tools:
    mode: bridge
  codex:
    enable_compatibility: true
    force_tool_choice_required: true
  code_interpreter:
    backend: docker
    python_binary: /opt/homebrew/bin/python3
    docker:
      binary: /usr/local/bin/docker
      image: ghcr.io/acme/llama-shim-code-interpreter:latest
      memory_limit: 512m
      cpu_limit: "1.5"
      pids_limit: 96
    execution_timeout: 45s
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
	require.Equal(t, "./tmp/shim.log", cfg.LogFilePath)
	require.Equal(t, config.ResponsesModePreferUpstream, cfg.ResponsesMode)
	require.Equal(t, "bridge", cfg.ResponsesCustomToolsMode)
	require.True(t, cfg.ResponsesCodexEnableCompatibility)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Equal(t, config.ResponsesCodeInterpreterBackendDocker, cfg.ResponsesCodeInterpreterBackend)
	require.Equal(t, "/opt/homebrew/bin/python3", cfg.ResponsesCodeInterpreterPythonBinary)
	require.Equal(t, "/usr/local/bin/docker", cfg.ResponsesCodeInterpreterDockerBinary)
	require.Equal(t, "ghcr.io/acme/llama-shim-code-interpreter:latest", cfg.ResponsesCodeInterpreterDockerImage)
	require.Equal(t, "512m", cfg.ResponsesCodeInterpreterDockerMemory)
	require.Equal(t, "1.5", cfg.ResponsesCodeInterpreterDockerCPU)
	require.Equal(t, 96, cfg.ResponsesCodeInterpreterDockerPids)
	require.Equal(t, 45*time.Second, cfg.ResponsesCodeInterpreterTimeout)
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
  mode: prefer_upstream
  codex:
    enable_compatibility: false
    force_tool_choice_required: false
  code_interpreter:
    backend: disabled
    python_binary: python3
    docker:
      binary: docker
      image: python:3.12-slim
      memory_limit: 256m
      cpu_limit: "0.5"
      pids_limit: 64
    execution_timeout: 20s
`)

	t.Setenv("SHIM_ADDR", ":7070")
	t.Setenv("LLAMA_TIMEOUT", "25s")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("LOG_FILE_PATH", "./override.log")
	t.Setenv("RESPONSES_MODE", "local_only")
	t.Setenv("RESPONSES_CODEX_ENABLE_COMPATIBILITY", "true")
	t.Setenv("RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED", "true")
	t.Setenv("RESPONSES_CODE_INTERPRETER_BACKEND", "unsafe_host")
	t.Setenv("RESPONSES_CODE_INTERPRETER_PYTHON_BINARY", "/usr/bin/python3")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_BINARY", "/usr/bin/docker")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_IMAGE", "python:3.12-alpine")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_MEMORY_LIMIT", "768m")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_CPU_LIMIT", "2")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_PIDS_LIMIT", "128")
	t.Setenv("RESPONSES_CODE_INTERPRETER_EXECUTION_TIMEOUT", "33s")

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, ":7070", cfg.Addr)
	require.Equal(t, 25*time.Second, cfg.LlamaTimeout)
	require.Equal(t, slog.LevelWarn, cfg.LogLevel)
	require.Equal(t, "./override.log", cfg.LogFilePath)
	require.Equal(t, config.ResponsesModeLocalOnly, cfg.ResponsesMode)
	require.True(t, cfg.ResponsesCodexEnableCompatibility)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Equal(t, config.ResponsesCodeInterpreterBackendUnsafeHost, cfg.ResponsesCodeInterpreterBackend)
	require.Equal(t, "/usr/bin/python3", cfg.ResponsesCodeInterpreterPythonBinary)
	require.Equal(t, "/usr/bin/docker", cfg.ResponsesCodeInterpreterDockerBinary)
	require.Equal(t, "python:3.12-alpine", cfg.ResponsesCodeInterpreterDockerImage)
	require.Equal(t, "768m", cfg.ResponsesCodeInterpreterDockerMemory)
	require.Equal(t, "2", cfg.ResponsesCodeInterpreterDockerCPU)
	require.Equal(t, 128, cfg.ResponsesCodeInterpreterDockerPids)
	require.Equal(t, 33*time.Second, cfg.ResponsesCodeInterpreterTimeout)
}

func TestLoadUsesCodexSafeDefaults(t *testing.T) {
	tempDir := t.TempDir()
	previousWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, config.ResponsesModePreferLocal, cfg.ResponsesMode)
	require.Equal(t, "auto", cfg.ResponsesCustomToolsMode)
	require.True(t, cfg.ResponsesCodexEnableCompatibility)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Equal(t, config.ResponsesCodeInterpreterBackendDisabled, cfg.ResponsesCodeInterpreterBackend)
	require.Equal(t, "python3", cfg.ResponsesCodeInterpreterPythonBinary)
	require.Equal(t, "docker", cfg.ResponsesCodeInterpreterDockerBinary)
	require.Equal(t, "python:3.12-slim", cfg.ResponsesCodeInterpreterDockerImage)
	require.Equal(t, "256m", cfg.ResponsesCodeInterpreterDockerMemory)
	require.Equal(t, "0.5", cfg.ResponsesCodeInterpreterDockerCPU)
	require.Equal(t, 64, cfg.ResponsesCodeInterpreterDockerPids)
	require.Equal(t, 20*time.Second, cfg.ResponsesCodeInterpreterTimeout)
}

func TestLoadSupportsLegacyUnsafeHostAlias(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
responses:
  code_interpreter:
    enable_unsafe_host_executor: true
`)

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, config.ResponsesCodeInterpreterBackendUnsafeHost, cfg.ResponsesCodeInterpreterBackend)
}
