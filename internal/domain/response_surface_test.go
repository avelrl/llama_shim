package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHydrateResponseRequestSurfaceSanitizesMCPTools(t *testing.T) {
	response := Response{}
	hydrated := HydrateResponseRequestSurface(response, `{
		"tools": [
			{
				"type": "mcp",
				"server_label": "dmcp",
				"server_url": "https://dmcp.example.com/sse",
				"authorization": "secret-token",
				"headers": {
					"Authorization": "Bearer other-secret",
					"X-Test": "value"
				},
				"require_approval": "never",
				"allowed_tools": ["roll"]
			},
			{
				"type": "mcp",
				"server_label": "google_calendar",
				"connector_id": "connector_googlecalendar",
				"authorization": "connector-secret",
				"headers": {
					"X-Test": "ignored"
				}
			}
		]
	}`)

	var tools []map[string]any
	require.NoError(t, json.Unmarshal(hydrated.Tools, &tools))
	require.Len(t, tools, 2)

	require.Equal(t, "mcp", tools[0]["type"])
	require.Equal(t, "dmcp", tools[0]["server_label"])
	require.Equal(t, "never", tools[0]["require_approval"])
	require.Equal(t, []any{"roll"}, tools[0]["allowed_tools"])
	require.NotContains(t, tools[0], "authorization")
	require.NotContains(t, tools[0], "headers")
	require.NotContains(t, tools[0], "server_url")

	require.Equal(t, "connector_googlecalendar", tools[1]["connector_id"])
	require.NotContains(t, tools[1], "authorization")
	require.NotContains(t, tools[1], "headers")
}

func TestHydrateResponseRequestSurfaceHydratesContinuationFields(t *testing.T) {
	hydrated := HydrateResponseRequestSurface(Response{}, `{
		"previous_response_id": "resp_prev",
		"conversation": {"id":"conv_123"}
	}`)

	require.Equal(t, "resp_prev", hydrated.PreviousResponseID)
	require.NotNil(t, hydrated.Conversation)
	require.Equal(t, "conv_123", hydrated.Conversation.ID)
}

func TestHydrateResponseContinuationJSONPatchesMissingContinuationFields(t *testing.T) {
	raw, err := HydrateResponseContinuationJSON([]byte(`{
		"id":"resp_123",
		"object":"response",
		"created_at":1741900000,
		"status":"completed",
		"model":"test-model",
		"output":[{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],
		"previous_response_id": null,
		"conversation": null,
		"output_text":"OK"
	}`), `{
		"previous_response_id":"resp_prev",
		"conversation":"conv_123"
	}`)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "resp_prev", payload["previous_response_id"])

	conversation, ok := payload["conversation"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "conv_123", conversation["id"])
}
