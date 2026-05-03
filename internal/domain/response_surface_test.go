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

func TestHydrateResponseRequestSurfaceAddsFunctionToolStrictDefault(t *testing.T) {
	hydrated := HydrateResponseRequestSurface(Response{}, `{
		"tools": [
			{
				"type": "function",
				"name": "lookup",
				"parameters": {"type":"object","properties":{}}
			}
		]
	}`)

	var tools []map[string]any
	require.NoError(t, json.Unmarshal(hydrated.Tools, &tools))
	require.Len(t, tools, 1)
	require.Equal(t, false, tools[0]["strict"])
}

func TestResponseMarshalJSONUsesOpenAICompatibleDefaults(t *testing.T) {
	response := NewResponse("resp_test", "test-model", "OK", "", "", 1741900000)
	response = HydrateResponseRequestSurface(response, `{}`)

	raw, err := json.Marshal(response)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Contains(t, payload, "previous_response_id")
	require.Nil(t, payload["previous_response_id"])
	require.Equal(t, float64(0), payload["frequency_penalty"])
	require.Equal(t, float64(0), payload["presence_penalty"])
	require.Equal(t, "default", payload["service_tier"])
	require.Equal(t, float64(0), payload["top_logprobs"])

	output, ok := payload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	message, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", message["status"])

	content, ok := message["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	part, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{}, part["annotations"])
	require.Equal(t, []any{}, part["logprobs"])
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
