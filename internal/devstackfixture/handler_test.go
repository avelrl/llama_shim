package devstackfixture

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandlerExposesHealthAndModels(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var health map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	require.Equal(t, "ok", health["status"])

	resp, err = server.Client().Get(server.URL + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var models map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&models))
	require.Equal(t, "list", models["object"])
	data, ok := models["data"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, data)
	require.Equal(t, DefaultModel, data[0].(map[string]any)["id"])
}

func TestHandlerChatCompletionsUsesDeterministicRules(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	payload := map[string]any{
		"model": DefaultModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Remember code 777. Reply READY."},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok := response["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "READY", message["content"])

	payload["messages"] = []map[string]any{
		{"role": "system", "content": "Remember: code=777. Reply OK."},
		{"role": "user", "content": "What is the code?"},
	}
	body, err = json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	choices, ok = response["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	message = choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "777", message["content"])
}

func TestHandlerSearchAndImageResponses(t *testing.T) {
	server := httptest.NewServer(NewHandler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/search?q=fixture+guide&format=json")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var search map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&search))
	results, ok := search["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	result := results[0].(map[string]any)
	require.Contains(t, result["url"], "/pages/web-search-guide")
	require.Equal(t, "Fixture Web Search Guide", result["title"])

	payload := map[string]any{
		"model": DefaultModel,
		"input": "Generate a tiny orange cat in a teacup.",
		"tools": []map[string]any{
			{
				"type":          "image_generation",
				"output_format": "png",
				"quality":       "low",
				"size":          "1024x1024",
			},
		},
		"tool_choice": map[string]any{
			"type": "image_generation",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err = server.Client().Post(server.URL+"/v1/responses", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	require.Equal(t, "response", response["object"])
	output, ok := response["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	item := output[0].(map[string]any)
	require.Equal(t, "image_generation_call", item["type"])
	require.Equal(t, "completed", item["status"])
	require.Equal(t, fixtureImageBase64, item["result"])
}
