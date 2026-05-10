package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvidenceRegistryListsLatestKnownArtifactSummaries(t *testing.T) {
	root := t.TempDir()
	writeEvidenceSummary(t, root, "v4-preflight-smoke/default_20260510T010203Z/summary.json", map[string]any{
		"object":       "v4_preflight_smoke.summary",
		"status":       "passed",
		"artifact_dir": filepath.ToSlash(filepath.Join(root, "v4-preflight-smoke/default_20260510T010203Z")),
		"model":        "deepseek/deepseek-v4-pro",
		"statuses": map[string]any{
			"readyz": "200",
		},
	})
	writeEvidenceSummary(t, root, "v4-preflight-smoke/default_20260509T010203Z/summary.json", map[string]any{
		"status": "failed",
		"model":  "deepseek/deepseek-v4-pro",
		"failures": []any{
			map[string]any{"message": "transport"},
		},
	})
	writeEvidenceSummary(t, root, "codex-eval-auto/deepseek_full_20260510T020203Z/summary.json", map[string]any{
		"generated_at": "2026-05-10T02:02:03Z",
		"status":       "passed",
		"profiles": []any{
			map[string]any{"name": "baseline"},
		},
		"counts": map[string]any{
			"ok": 3,
		},
	})

	registry := NewEvidenceRegistry(EvidenceConfig{
		Enabled:    true,
		Root:       root,
		MaxEntries: 5,
		StaleAfter: 24 * time.Hour,
	})
	list := registry.List(time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC))

	require.Equal(t, evidenceObjectList, list.Object)
	require.Equal(t, slashPath(root), list.Root)
	require.Len(t, list.Data, 3)
	require.Len(t, list.LatestByKind, 2)
	require.Equal(t, "codex_eval_auto:deepseek_full_20260510T020203Z", list.Data[0].ID)
	require.Equal(t, "passed", list.Data[0].Status)
	require.Equal(t, "deepseek", list.Data[0].Model)
	require.Equal(t, "v4_preflight_smoke:default_20260510T010203Z", list.Data[1].ID)
	require.Equal(t, "deepseek/deepseek-v4-pro", list.Data[1].Model)
	require.NotNil(t, list.Data[1].Metrics["statuses"])
	require.Equal(t, 1, list.Data[2].FailureCount)
}

func TestEvidenceDetailReturnsBoundedSummaryJSON(t *testing.T) {
	root := t.TempDir()
	writeEvidenceSummary(t, root, "v4-provider-matrix-curation/curation-20260510T010203Z/summary.json", map[string]any{
		"object":  "v4_provider_matrix_curation.summary",
		"status":  "passed",
		"verdict": "release_gate_ok",
	})
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "v4-provider-matrix-curation/curation-20260510T010203Z/summary.md"),
		[]byte("# summary\n"),
		0o600,
	))

	registry := NewEvidenceRegistry(EvidenceConfig{Enabled: true, Root: root})
	detail, ok := registry.Detail("v4_provider_matrix_curation:curation-20260510T010203Z", time.Date(2026, 5, 10, 2, 0, 0, 0, time.UTC))

	require.True(t, ok)
	require.Equal(t, evidenceObjectDetail, detail.Object)
	require.Equal(t, "release_gate_ok", detail.Evidence.Verdict)
	require.NotEmpty(t, detail.Evidence.SummaryMDPath)
	require.Equal(t, "v4_provider_matrix_curation.summary", detail.Summary["object"])
}

func writeEvidenceSummary(t *testing.T, root string, relPath string, payload map[string]any) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o700))
	data, err := json.MarshalIndent(payload, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fullPath, data, 0o600))
}
