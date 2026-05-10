package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"llama_shim/internal/config"
	"llama_shim/internal/testutil"

	"github.com/stretchr/testify/require"
)

func TestEvidenceEndpointSharesIngressAuth(t *testing.T) {
	root := t.TempDir()
	writeEvidenceSummary(t, root, "upstream-provider-routing-smoke/deepseek_20260510T010203Z/summary.json", map[string]any{
		"status": "passed",
		"model":  "deepseek/deepseek-v4-pro",
	})
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		AuthMode:     config.ShimAuthModeStaticBearer,
		BearerTokens: []string{"shim-secret"},
		EvidenceRoot: root,
	})

	status, _, unauthorized := rawRequestWithHeaders(t, app, http.MethodGet, "/debug/evidence", nil, nil)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "authentication_error", asStringAny(unauthorized["error"].(map[string]any)["type"]))

	status, _, authorized := rawRequestWithHeaders(t, app, http.MethodGet, "/debug/evidence", nil, map[string]string{
		"Authorization": "Bearer shim-secret",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "shim.evidence_list", asStringAny(authorized["object"]))
	require.Len(t, authorized["data"].([]any), 1)

	id := authorized["data"].([]any)[0].(map[string]any)["id"].(string)
	status, _, detail := rawRequestWithHeaders(t, app, http.MethodGet, "/debug/evidence/"+url.PathEscape(id), nil, map[string]string{
		"Authorization": "Bearer shim-secret",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "shim.evidence", asStringAny(detail["object"]))

	status, _, capabilities := rawRequestWithHeaders(t, app, http.MethodGet, "/debug/capabilities", nil, map[string]string{
		"Authorization": "Bearer shim-secret",
	})
	require.Equal(t, http.StatusOK, status)
	runtime := capabilities["runtime"].(map[string]any)
	ops := runtime["ops"].(map[string]any)
	evidence := ops["evidence"].(map[string]any)
	require.Equal(t, true, evidence["enabled"])
	require.Equal(t, filepath.ToSlash(filepath.Clean(root)), asStringAny(evidence["root"]))
}

func TestEvidenceEndpointCanBeDisabled(t *testing.T) {
	disabled := false
	app := testutil.NewTestAppWithOptions(t, testutil.TestAppOptions{
		EvidenceEnabled: &disabled,
	})

	status, payload := rawRequest(t, app, http.MethodGet, "/debug/evidence", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found_error", asStringAny(payload["error"].(map[string]any)["type"]))
}

func writeEvidenceSummary(t *testing.T, root string, relPath string, payload map[string]any) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o700))
	data, err := json.MarshalIndent(payload, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fullPath, data, 0o600))
}
