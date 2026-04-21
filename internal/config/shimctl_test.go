package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
)

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()

	previous, existed := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		var err error
		if existed {
			err = os.Setenv(key, previous)
		} else {
			err = os.Unsetenv(key)
		}
		require.NoError(t, err)
	})
}

func disableSharedDotEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SHIM_DOTENV", filepath.Join(t.TempDir(), "missing.env"))
}

func TestLoadShimctlFromYAMLFile(t *testing.T) {
	disableSharedDotEnv(t)
	unsetEnvForTest(t, "LLAMA_BASE_URL")
	unsetEnvForTest(t, "SHIMCTL_PROBE_BEARER_TOKEN")
	unsetEnvForTest(t, "SHIMCTL_PROBE_MODEL")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_TOKEN")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_MODEL")

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
sqlite:
  path: ./tmp/shimctl.db
llama:
  base_url: http://127.0.0.1:9191
  timeout: 22s
  max_concurrent_requests: 5
  max_queue_wait: 11s
  http:
    max_idle_conns: 44
    max_idle_conns_per_host: 22
    max_conns_per_host: 9
    idle_conn_timeout: 70s
    dial_timeout: 6s
    keep_alive: 19s
    tls_handshake_timeout: 7s
    expect_continue_timeout: 1300ms
probe:
  count: 4
  request_timeout: 9s
  bearer_token: shimctl-probe-secret
  model: unsloth/Kimi-K2.5
retrieval:
  index:
    backend: lexical
  embedder:
    backend: openai_compatible
    base_url: http://127.0.0.1:8082
    model: text-embedding-3-small
`)

	cfg, err := config.LoadShimctl(configPath)
	require.NoError(t, err)
	require.Equal(t, "./tmp/shimctl.db", cfg.SQLitePath)
	require.Equal(t, "http://127.0.0.1:9191", cfg.LlamaBaseURL)
	require.Equal(t, 22*time.Second, cfg.LlamaTimeout)
	require.Equal(t, 5, cfg.LlamaMaxConcurrentRequests)
	require.Equal(t, 11*time.Second, cfg.LlamaMaxQueueWait)
	require.Equal(t, 4, cfg.ProbeCount)
	require.Equal(t, 9*time.Second, cfg.ProbeRequestTimeout)
	require.Equal(t, "shimctl-probe-secret", cfg.ProbeBearerToken)
	require.Equal(t, "unsloth/Kimi-K2.5", cfg.ProbeModel)
	require.Equal(t, 44, cfg.LlamaHTTPMaxIdleConns)
	require.Equal(t, 22, cfg.LlamaHTTPMaxIdleConnsPerHost)
	require.Equal(t, 9, cfg.LlamaHTTPMaxConnsPerHost)
	require.Equal(t, 70*time.Second, cfg.LlamaHTTPIdleConnTimeout)
	require.Equal(t, 6*time.Second, cfg.LlamaHTTPDialTimeout)
	require.Equal(t, 19*time.Second, cfg.LlamaHTTPKeepAlive)
	require.Equal(t, 7*time.Second, cfg.LlamaHTTPTLSHandshakeTimeout)
	require.Equal(t, 1300*time.Millisecond, cfg.LlamaHTTPExpectContinueTimeout)
	require.Equal(t, "lexical", cfg.RetrievalIndexBackend)
	require.Equal(t, "openai_compatible", cfg.RetrievalEmbedderBackend)
	require.Equal(t, "http://127.0.0.1:8082", cfg.RetrievalEmbedderBaseURL)
	require.Equal(t, "text-embedding-3-small", cfg.RetrievalEmbedderModel)
	require.Equal(t, configPath, cfg.ConfigFile)
}

func TestLoadShimctlReadsSharedDotEnv(t *testing.T) {
	unsetEnvForTest(t, "LLAMA_BASE_URL")
	unsetEnvForTest(t, "SHIMCTL_PROBE_BEARER_TOKEN")
	unsetEnvForTest(t, "SHIMCTL_PROBE_MODEL")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_TOKEN")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_MODEL")

	tempDir := t.TempDir()
	writeFile(t, filepath.Join(tempDir, ".env"), `
LLAMA_BASE_URL=http://127.0.0.1:9999
SHIMCTL_PROBE_BEARER_TOKEN=dotenv-probe-secret
SHIMCTL_PROBE_MODEL=dotenv-model
`)

	configPath := filepath.Join(tempDir, "config.yaml")
	writeFile(t, configPath, `
llama:
  base_url: http://127.0.0.1:8081
probe:
  bearer_token: yaml-probe-secret
  model: yaml-model
`)

	t.Setenv("SHIM_DOTENV", filepath.Join(tempDir, ".env"))

	cfg, err := config.LoadShimctl(configPath)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:9999", cfg.LlamaBaseURL)
	require.Equal(t, "dotenv-probe-secret", cfg.ProbeBearerToken)
	require.Equal(t, "dotenv-model", cfg.ProbeModel)
}

func TestLoadShimctlEnvWinsOverSharedDotEnv(t *testing.T) {
	unsetEnvForTest(t, "LLAMA_BASE_URL")
	unsetEnvForTest(t, "SHIMCTL_PROBE_BEARER_TOKEN")
	unsetEnvForTest(t, "SHIMCTL_PROBE_MODEL")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_TOKEN")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_MODEL")

	tempDir := t.TempDir()
	writeFile(t, filepath.Join(tempDir, ".env"), `
SHIMCTL_PROBE_BEARER_TOKEN=dotenv-probe-secret
SHIMCTL_PROBE_MODEL=dotenv-model
`)

	configPath := filepath.Join(tempDir, "config.yaml")
	writeFile(t, configPath, `
probe:
  bearer_token: yaml-probe-secret
  model: yaml-model
`)

	t.Setenv("SHIM_DOTENV", filepath.Join(tempDir, ".env"))
	t.Setenv("SHIMCTL_PROBE_BEARER_TOKEN", "process-probe-secret")
	t.Setenv("SHIMCTL_PROBE_MODEL", "process-model")

	cfg, err := config.LoadShimctl(configPath)
	require.NoError(t, err)
	require.Equal(t, "process-probe-secret", cfg.ProbeBearerToken)
	require.Equal(t, "process-model", cfg.ProbeModel)
}

func TestLoadShimctlUsesDefaults(t *testing.T) {
	disableSharedDotEnv(t)
	unsetEnvForTest(t, "LLAMA_BASE_URL")
	unsetEnvForTest(t, "SHIMCTL_PROBE_BEARER_TOKEN")
	unsetEnvForTest(t, "SHIMCTL_PROBE_MODEL")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_TOKEN")
	unsetEnvForTest(t, "LLAMA_STARTUP_CALIBRATION_MODEL")

	tempDir := t.TempDir()
	previousWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	cfg, err := config.LoadShimctl("")
	require.NoError(t, err)
	require.Equal(t, "./data/shim.db", cfg.SQLitePath)
	require.Equal(t, "http://127.0.0.1:8081", cfg.LlamaBaseURL)
	require.Equal(t, 60*time.Second, cfg.LlamaTimeout)
	require.Equal(t, 4, cfg.LlamaMaxConcurrentRequests)
	require.Equal(t, time.Duration(0), cfg.LlamaMaxQueueWait)
	require.Equal(t, 3, cfg.ProbeCount)
	require.Equal(t, 8*time.Second, cfg.ProbeRequestTimeout)
	require.Empty(t, cfg.ProbeBearerToken)
	require.Empty(t, cfg.ProbeModel)
}
