package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage"
	"llama_shim/internal/storage/sqlite"
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

func TestRunGovernancePurgeDryRunAndApplyRequiresConfirmAndWritesAudit(t *testing.T) {
	disableSharedDotEnv(t)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "shim.db")
	store, err := sqlite.Open(ctx, dbPath)
	require.NoError(t, err)
	seedShimctlGovernancePurgeState(t, ctx, store)
	require.NoError(t, store.Close())

	configPath := writeShimctlConfig(t, `
sqlite:
  path: `+dbPath+`
storage:
  backend: sqlite
`)

	dryRunAuditPath := filepath.Join(t.TempDir(), "dry-run-audit.json")
	var dryRunStdout bytes.Buffer
	var dryRunStderr bytes.Buffer
	err = run([]string{
		"-config", configPath,
		"governance", "purge",
		"-all",
		"-batch-size", "1",
		"-audit-out", dryRunAuditPath,
	}, &dryRunStdout, &dryRunStderr)
	require.NoError(t, err)
	require.Empty(t, dryRunStderr.String())

	var dryRunReport storage.GovernancePurgeReport
	require.NoError(t, json.Unmarshal(dryRunStdout.Bytes(), &dryRunReport))
	require.Equal(t, "governance.purge_report", dryRunReport.Object)
	require.Equal(t, storage.BackendSQLite, dryRunReport.Backend)
	require.True(t, dryRunReport.DryRun)
	require.False(t, dryRunReport.Applied)
	require.Greater(t, dryRunReport.Primary.MatchedTotal, int64(0))
	require.Zero(t, dryRunReport.Primary.DeletedTotal)

	auditBytes, err := os.ReadFile(dryRunAuditPath)
	require.NoError(t, err)
	var auditReport storage.GovernancePurgeReport
	require.NoError(t, json.Unmarshal(auditBytes, &auditReport))
	require.Equal(t, dryRunReport.Primary.MatchedTotal, auditReport.Primary.MatchedTotal)

	var missingConfirmStdout bytes.Buffer
	var missingConfirmStderr bytes.Buffer
	err = run([]string{
		"-config", configPath,
		"governance", "purge",
		"-all",
		"-apply",
	}, &missingConfirmStdout, &missingConfirmStderr)
	require.ErrorContains(t, err, "requires -confirm purge-all-local-state")
	require.Empty(t, missingConfirmStdout.String())

	applyAuditPath := filepath.Join(t.TempDir(), "apply-audit.json")
	var applyStdout bytes.Buffer
	var applyStderr bytes.Buffer
	err = run([]string{
		"-config", configPath,
		"governance", "purge",
		"-all",
		"-apply",
		"-confirm", "purge-all-local-state",
		"-batch-size", "1",
		"-audit-out", applyAuditPath,
	}, &applyStdout, &applyStderr)
	require.NoError(t, err)
	require.Empty(t, applyStderr.String())

	var applyReport storage.GovernancePurgeReport
	require.NoError(t, json.Unmarshal(applyStdout.Bytes(), &applyReport))
	require.False(t, applyReport.DryRun)
	require.True(t, applyReport.Applied)
	require.Greater(t, applyReport.Primary.DeletedTotal, int64(0))
	require.FileExists(t, applyAuditPath)

	store, err = sqlite.Open(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	_, err = store.GetResponse(ctx, "resp_shimctl_governance")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestDefaultMigrationTargetSidecarPath(t *testing.T) {
	require.Equal(t, ".data/shim.postgres-sidecar.db", defaultMigrationTargetSidecarPath(".data/shim.db"))
	require.Equal(t, "shim.postgres-sidecar.db", defaultMigrationTargetSidecarPath("shim"))
}

func seedShimctlGovernancePurgeState(t *testing.T, ctx context.Context, store *sqlite.Store) {
	t.Helper()
	response := domain.StoredResponse{
		ID:                   "resp_shimctl_governance",
		Model:                "test-model",
		RequestJSON:          `{"input":"governance"}`,
		ResponseJSON:         `{"id":"resp_shimctl_governance","object":"response","status":"completed","output":[]}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "governance")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "governance")},
		Output:               []domain.Item{domain.NewOutputTextMessage("ok")},
		OutputText:           "ok",
		Store:                true,
		CreatedAt:            "2026-05-06T09:00:00Z",
		CompletedAt:          "2026-05-06T09:00:01Z",
	}
	require.NoError(t, store.SaveResponse(ctx, response))
	require.NoError(t, store.SaveResponseReplayArtifacts(ctx, response.ID, []domain.ResponseReplayArtifact{
		{ResponseID: response.ID, Sequence: 1, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
	}))
}

func writeShimctlConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf("%s\n", body)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
