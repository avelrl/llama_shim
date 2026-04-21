package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func disableSharedDotEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SHIM_DOTENV", filepath.Join(t.TempDir(), "missing.env"))
}

func TestRunProbeOutputsSnapshotAndHonorsOverrides(t *testing.T) {
	disableSharedDotEnv(t)
	var seenModelsAuth string
	var seenChatAuth string
	var seenModelsPath string
	var seenModelsCount int
	var seenProbeModels []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			seenModelsCount++
			seenModelsPath = r.URL.Path
			seenModelsAuth = r.Header.Get("Authorization")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "fallback-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			seenChatAuth = r.Header.Get("Authorization")
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			seenProbeModels = append(seenProbeModels, payload["model"].(string))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": "OK",
						},
					},
				},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	configPath := writeShimctlConfig(t, `
llama:
  base_url: `+upstream.URL+`
  timeout: 3s
probe:
  count: 1
  request_timeout: 150ms
  bearer_token: startup-probe-secret
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"-config", configPath,
		"probe",
		"-model", "override-model",
		"-probe-count", "2",
		"-request-timeout", "250ms",
	}, &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, "/v1/models", seenModelsPath)
	require.Equal(t, 1, seenModelsCount)
	require.Equal(t, "Bearer startup-probe-secret", seenModelsAuth)
	require.Equal(t, "Bearer startup-probe-secret", seenChatAuth)
	require.Equal(t, []string{"override-model", "override-model"}, seenProbeModels)
	require.Contains(t, stderr.String(), "[probe] GET /v1/models step=models result=ok status=200")
	require.Contains(t, stderr.String(), "probe=1/2")
	require.Contains(t, stderr.String(), "probe=2/2")
	require.Contains(t, stderr.String(), "preview=\"OK\"")
	require.Contains(t, stderr.String(), "[probe] finished status=completed model=override-model successful_probes=2/2")

	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &snapshot))
	require.Equal(t, "completed", snapshot["status"])
	require.Equal(t, "override-model", snapshot["model"])
	require.Equal(t, float64(2), snapshot["probe_count"])
	require.Equal(t, float64(2), snapshot["successful_probes"])
	require.Equal(t, true, snapshot["models_ready"])
}

func TestRunProbePrintsSnapshotOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			http.Error(w, "probe failed", http.StatusGatewayTimeout)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	configPath := writeShimctlConfig(t, `
llama:
  base_url: `+upstream.URL+`
  timeout: 2s
probe:
  count: 1
  request_timeout: 100ms
  bearer_token: startup-probe-secret
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{"-config", configPath, "probe"}, &stdout, &stderr)
	require.Error(t, err)
	require.Contains(t, stderr.String(), "[probe] GET /v1/models step=models result=ok status=200")
	require.Contains(t, stderr.String(), "step=probe result=failed")
	require.Contains(t, stderr.String(), "status=504")
	require.Contains(t, stderr.String(), "[probe] finished status=failed")

	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &snapshot))
	require.Equal(t, "failed", snapshot["status"])
	_, hasSuccessfulProbes := snapshot["successful_probes"]
	require.False(t, hasSuccessfulProbes)
	require.NotEmpty(t, snapshot["error"])
}

func TestRunProbePrintsFullAssistantContent(t *testing.T) {
	disableSharedDotEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": "VERDICT: FEASIBLE\nROUTE: A -> C -> B\nTOTAL_MINUTES: 80\nWHY: Dependencies hold and the return still fits before the deadline.",
						},
					},
				},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	configPath := writeShimctlConfig(t, `
llama:
  base_url: `+upstream.URL+`
  timeout: 3s
probe:
  count: 1
  request_timeout: 150ms
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{"-config", configPath, "probe"}, &stdout, &stderr)
	require.NoError(t, err)
	require.Contains(t, stderr.String(), "preview=\"VERDICT: FEASIBLE\\nROUTE: A -> C -> B\\nTOTAL_MINUTES: 80\\nWHY: Dependencies hold and the return still fits before the deadline.\"")
}

func TestRunProbePrintsTypedAssistantContent(t *testing.T) {
	disableSharedDotEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":     "chatcmpl-test",
				"object": "chat.completion",
				"model":  "test-model",
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": []map[string]any{
								{"type": "text", "text": "ANALYSIS: stable"},
								{"type": "output_text", "text": "\nRECOMMENDATION: keep current gate"},
							},
						},
					},
				},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	configPath := writeShimctlConfig(t, `
llama:
  base_url: `+upstream.URL+`
  timeout: 3s
probe:
  count: 1
  request_timeout: 150ms
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{"-config", configPath, "probe"}, &stdout, &stderr)
	require.NoError(t, err)
	require.Contains(t, stderr.String(), "preview=\"ANALYSIS: stable\\nRECOMMENDATION: keep current gate\"")
	require.NotContains(t, stderr.String(), "preview=\"{\\\"id\\\":\\\"chatcmpl-test\\\"")
}

func writeShimctlConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf("%s\n", body)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
