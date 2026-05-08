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
	"strings"
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

func TestRunCodexConfigPrintsCurrentProviderTOML(t *testing.T) {
	disableSharedDotEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"codex",
		"config",
		"-model", "deepseek/deepseek-v4-pro",
		"-provider", "gateway-shim",
		"-base-url", "http://127.0.0.1:8080/v1",
		"-api-key-env", "GW_API_KEY",
		"-supports-websockets",
		"-model-context-window", "200000",
	}, &stdout, &stderr)
	require.NoError(t, err)

	out := stdout.String()
	require.Contains(t, out, `model = "deepseek/deepseek-v4-pro"`)
	require.Contains(t, out, `model_context_window = 200000`)
	require.Contains(t, out, `model_provider = "gateway-shim"`)
	require.Contains(t, out, `[model_providers.gateway-shim]`)
	require.Contains(t, out, `base_url = "http://127.0.0.1:8080/v1"`)
	require.Contains(t, out, `env_key = "GW_API_KEY"`)
	require.Contains(t, out, `wire_api = "responses"`)
	require.Contains(t, out, `supports_websockets = true`)
	require.NotContains(t, out, "shim-dev-key")
}

func TestRunCodexConfigWritesCodexHome(t *testing.T) {
	disableSharedDotEnv(t)
	codexHome := filepath.Join(t.TempDir(), "codex-home")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"codex",
		"config",
		"-model", "svgun/kimi-k2.6",
		"-base-url", "http://127.0.0.1:8080/v1",
		"-codex-home", codexHome,
	}, &stdout, &stderr)
	require.NoError(t, err)
	require.Contains(t, stdout.String(), "codex config written:")

	raw, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	require.NoError(t, err)
	require.Contains(t, string(raw), `model = "svgun/kimi-k2.6"`)
	require.Contains(t, string(raw), `env_key = "GW_API_KEY"`)
}

func TestRunCodexDoctorPassesAndWritesArtifacts(t *testing.T) {
	disableSharedDotEnv(t)
	t.Setenv("GW_API_KEY", "shim-dev-key")
	model := "deepseek/deepseek-v4-pro"
	codexBin := writeFakeCodexBinary(t, "codex 0.99.0")
	var seenAuth []string
	var seenResponseModel string

	shim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "ok"}))
		case r.Method == http.MethodGet && r.URL.Path == "/readyz":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "ready"}))
		case r.Method == http.MethodGet && r.URL.Path == "/debug/capabilities":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "shim.capabilities",
				"surfaces": map[string]any{
					"responses": map[string]any{"enabled": true},
				},
				"upstream_provider_routing": map[string]any{
					"enabled": true,
					"providers": []map[string]any{
						{"id": "deepseek", "models": []string{model}},
					},
				},
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": model, "object": "model"}},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			seenResponseModel = payload["model"].(string)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":     "resp_codex_doctor",
				"object": "response",
				"model":  model,
				"status": "completed",
				"output": []any{},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer shim.Close()

	outDir := filepath.Join(t.TempDir(), "doctor")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"codex",
		"doctor",
		"-model", model,
		"-provider", "gateway-shim",
		"-base-url", shim.URL + "/v1",
		"-api-key-env", "GW_API_KEY",
		"-codex-bin", codexBin,
		"-out", outDir,
	}, &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, model, seenResponseModel)
	require.NotEmpty(t, seenAuth)
	for _, auth := range seenAuth {
		require.Equal(t, "Bearer shim-dev-key", auth)
	}

	var report map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, "passed", report["status"])
	require.Equal(t, model, report["model"])
	require.Equal(t, true, report["api_key_present"])
	require.Equal(t, "codex 0.99.0", report["codex_version"])
	require.FileExists(t, filepath.Join(outDir, "summary.json"))
	require.FileExists(t, filepath.Join(outDir, "summary.md"))
	require.FileExists(t, filepath.Join(outDir, "env.sh"))
	configRaw, err := os.ReadFile(filepath.Join(outDir, "codex-home", "config.toml"))
	require.NoError(t, err)
	require.Contains(t, string(configRaw), `model = "deepseek/deepseek-v4-pro"`)
	envRaw, err := os.ReadFile(filepath.Join(outDir, "env.sh"))
	require.NoError(t, err)
	require.Contains(t, string(envRaw), "CODEX_EVAL_MODELS='deepseek/deepseek-v4-pro'")
	require.NotContains(t, string(envRaw), "shim-dev-key")
}

func TestRunCodexDoctorReportsActionableFailures(t *testing.T) {
	disableSharedDotEnv(t)
	model := "svgun/qwen-3.6"
	codexBin := writeFakeCodexBinary(t, "codex 0.99.0")
	shim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "ok"}))
		case r.Method == http.MethodGet && r.URL.Path == "/readyz":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "ready"}))
		case r.Method == http.MethodGet && r.URL.Path == "/debug/capabilities":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "shim.capabilities",
				"surfaces": map[string]any{
					"responses": map[string]any{"enabled": true},
				},
				"upstream_provider_routing": map[string]any{
					"enabled":   true,
					"providers": []map[string]any{{"id": "svgun", "models": []string{"svgun/kimi-k2.6"}}},
				},
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "svgun/kimi-k2.6", "object": "model"}},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer shim.Close()

	outDir := filepath.Join(t.TempDir(), "doctor")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"codex",
		"doctor",
		"-model", model,
		"-base-url", shim.URL + "/v1",
		"-codex-bin", codexBin,
		"-out", outDir,
		"-skip-direct-response",
	}, &stdout, &stderr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "codex doctor failed")

	var report map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, "failed", report["status"])
	checks := report["checks"].([]any)
	joined, err := json.Marshal(checks)
	require.NoError(t, err)
	text := string(joined)
	require.Contains(t, text, `"name":"api_key_env"`)
	require.Contains(t, text, `"status":"failed"`)
	require.Contains(t, text, "provider/model alias is not advertised")
	require.Contains(t, text, "model is not listed by /v1/models")
	require.FileExists(t, filepath.Join(outDir, "summary.md"))
}

func TestRunProviderDoctorPassesHealthyRoutedConfig(t *testing.T) {
	disableSharedDotEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-secret")
	configPath := writeShimctlConfig(t, `
llama:
  providers:
    - id: deepseek
      base_url: https://api.deepseek.com/v1
      bearer_token_env: DEEPSEEK_API_KEY
      models:
        - model: deepseek-v4-pro
          upstream_model: deepseek-chat
chat_completions:
  upstream_compatibility:
    models:
      - model: deepseek-chat
        remap_developer_role: true
responses:
  upstream_transport: chat_completions
  upstream_tool_compatibility:
    models:
      - model: deepseek-chat
        disabled_tools: [image_generation]
  codex:
    enable_compatibility: true
    upstream_input_compatibility:
      models:
        - model: deepseek-chat
          mode: stringify
    model_metadata:
      models:
        - model: deepseek/deepseek-v4-pro
          context_window: 1000000
          max_context_window: 1000000
          shell_type: shell_command
          apply_patch_tool_type: freeform
`)

	outDir := filepath.Join(t.TempDir(), "provider-doctor")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"-config", configPath,
		"provider", "doctor",
		"-strict-env",
		"-require-matrix",
		"-strict-codex-metadata",
		"-matrix-models", "deepseek/deepseek-v4-pro",
		"-out", outDir,
	}, &stdout, &stderr)
	require.NoError(t, err)

	var report map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, "passed", report["status"])
	summary := report["summary"].(map[string]any)
	require.Equal(t, float64(1), summary["provider_count"])
	require.Equal(t, float64(1), summary["model_count"])
	require.Equal(t, float64(0), summary["errors"])
	require.FileExists(t, filepath.Join(outDir, "summary.json"))
	require.FileExists(t, filepath.Join(outDir, "summary.md"))
	require.NotContains(t, stdout.String(), "deepseek-secret")
	require.Contains(t, stdout.String(), `"target_kind": "resolved_upstream_model"`)
}

func TestRunProviderDoctorReportsConfigDrift(t *testing.T) {
	disableSharedDotEnv(t)
	configPath := writeShimctlConfig(t, `
llama:
  providers:
    - id: qwen
      base_url: not-a-url
      bearer_token_env: QWEN_API_KEY
      models:
        - model: coder
          upstream_model: Qwen3.6-Coder
    - id: remote
      base_url: https://remote.example/v1
      models:
        - model: coder
chat_completions:
  upstream_compatibility:
    models:
      - model: qwen/coder
responses:
  codex:
    enable_compatibility: true
    model_metadata:
      models:
        - model: qwen/coder
          context_window: 0
          max_context_window: 0
          shell_type: disabled
        - model: qwen/coder
          context_window: 128000
          max_context_window: 128000
          shell_type: shell_command
          apply_patch_tool_type: freeform
`)

	outDir := filepath.Join(t.TempDir(), "provider-doctor")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"-config", configPath,
		"provider", "doctor",
		"-strict-env",
		"-strict-codex-metadata",
		"-matrix-models", "qwen/coder",
		"-out", outDir,
	}, &stdout, &stderr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider doctor failed")

	var report map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, "failed", report["status"])
	text := stdout.String()
	require.Contains(t, text, `"code": "provider_base_url_invalid"`)
	require.Contains(t, text, `"code": "provider_token_env_unset"`)
	require.Contains(t, text, `"code": "provider_token_env_missing"`)
	require.Contains(t, text, `"code": "compatibility_rule_uses_public_alias"`)
	require.Contains(t, text, `"code": "codex_metadata_duplicate"`)
	require.FileExists(t, filepath.Join(outDir, "summary.md"))
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

func writeFakeCodexBinary(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %s\n", shellQuote(version))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o700))
	require.True(t, strings.HasPrefix(version, "codex "))
	return path
}
