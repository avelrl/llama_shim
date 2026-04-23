package compactor

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestNormalizeConfigDefaultsToHeuristic(t *testing.T) {
	cfg, err := NormalizeConfig(Config{})
	require.NoError(t, err)
	require.Equal(t, BackendHeuristic, cfg.Backend)
	require.Empty(t, cfg.BaseURL)
	require.Empty(t, cfg.Model)
	require.Zero(t, cfg.Timeout)
	require.Zero(t, cfg.MaxOutputTokens)
	require.Zero(t, cfg.RetainedItems)
	require.Zero(t, cfg.MaxInputRunes)
}

func TestModelAssistedTextCompactorBuildsStructuredOpaqueState(t *testing.T) {
	t.Parallel()

	var capturedRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest))
		_, _ = w.Write([]byte(`{
			"choices":[{
				"message":{
					"content":"{\"summary\":\"The user wants compaction shipped.\",\"key_facts\":[\"Repo is llama_shim\"],\"constraints\":[\"Keep OpenAI surface opaque\"],\"open_loops\":[\"Wire tests\"],\"recent_tool_state\":[\"No tools pending\"]}"
				}
			}]
		}`))
	}))
	defer server.Close()

	compactor, err := New(Config{
		Backend:         BackendModelAssistedText,
		BaseURL:         server.URL,
		Model:           "compact-model",
		Timeout:         time.Second,
		MaxOutputTokens: 500,
		RetainedItems:   1,
		MaxInputRunes:   20000,
	})
	require.NoError(t, err)

	result, err := compactor.Compact(context.Background(), []domain.Item{
		domain.NewInputTextMessage("user", "Remember repo llama_shim."),
		domain.NewOutputTextMessage("OK."),
		domain.NewInputTextMessage("user", "Now implement compaction."),
	})
	require.NoError(t, err)

	require.Equal(t, "compact-model", capturedRequest["model"])
	require.Equal(t, float64(500), capturedRequest["max_tokens"])
	messages, ok := capturedRequest["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 2)
	userMessage, ok := messages[1].(map[string]any)
	require.True(t, ok)
	require.Contains(t, userMessage["content"], "Remember repo llama_shim")

	require.Equal(t, "compaction", result.Item.Type)
	require.Len(t, result.Expanded, 2)
	require.Contains(t, domain.MessageText(result.Expanded[0]), "The user wants compaction shipped.")
	require.Contains(t, domain.MessageText(result.Expanded[0]), "Repo is llama_shim")
	require.Contains(t, domain.MessageText(result.Expanded[0]), "Wire tests")
	require.Equal(t, "Now implement compaction.", domain.MessageText(result.Expanded[1]))
}

func TestModelAssistedTextCompactorFallsBackToHeuristic(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not json"}}]}`))
	}))
	defer server.Close()

	compactor, err := New(Config{
		Backend:       BackendModelAssistedText,
		BaseURL:       server.URL,
		Model:         "compact-model",
		Timeout:       time.Second,
		RetainedItems: 1,
		Logger:        slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	require.NoError(t, err)

	result, err := compactor.Compact(context.Background(), []domain.Item{
		domain.NewInputTextMessage("user", "Remember launch code 1234."),
	})
	require.NoError(t, err)
	require.Equal(t, "compaction", result.Item.Type)
	require.Len(t, result.Expanded, 1)
	require.Contains(t, domain.MessageText(result.Expanded[0]), "launch code 1234")
	require.Contains(t, logs.String(), "model-assisted compaction failed")
}

func TestExtractJSONObjectAcceptsFencedJSON(t *testing.T) {
	raw := extractJSONObject("```json\n{\"summary\":\"ok\"}\n```")
	require.True(t, json.Valid(raw))
	require.True(t, strings.Contains(string(raw), `"summary":"ok"`))
}

func TestParseModelStateAcceptsObjectLists(t *testing.T) {
	state, err := parseModelState(`{
		"summary": "User confirmed memory of Project ALPHA.",
		"key_facts": [
			{"id": "F001", "fact": "Project codename is ALPHA"},
			{"id": "F002", "fact": "Constraint: Do not change OpenAPI"}
		],
		"constraints": [
			{"id": "C001", "constraint": "Do not modify the existing OpenAPI specification"}
		],
		"open_loops": [
			{"id": "OL001", "task": "Add tests for the compaction process"}
		],
		"recent_tool_state": []
	}`)
	require.NoError(t, err)
	require.Equal(t, "User confirmed memory of Project ALPHA.", state.Summary)
	require.Equal(t, []string{
		"F001: Project codename is ALPHA",
		"F002: Constraint: Do not change OpenAPI",
	}, state.KeyFacts)
	require.Equal(t, []string{"C001: Do not modify the existing OpenAPI specification"}, state.Constraints)
	require.Equal(t, []string{"OL001: Add tests for the compaction process"}, state.OpenLoops)
}
