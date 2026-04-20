package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildLocalToolLoopTransportPlanConvertsNamedFunctionToolChoiceToChatShape(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`{"type":"function","name":"add"}`),
	}
	tools := []map[string]any{
		{
			"type":        "function",
			"name":        "add",
			"description": "Add two integers",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "integer"},
					"b": map[string]any{"type": "integer"},
				},
				"required": []string{"a", "b"},
			},
		},
	}

	_, plan, toolChoice, _, err := buildLocalToolLoopTransportPlan(rawFields, tools, ServiceLimits{}, false)

	require.NoError(t, err)
	require.Equal(t, toolChoiceContractRequiredNamedFunction, plan.ToolChoiceContract.Mode)
	require.Equal(t, "add", plan.ToolChoiceContract.Name)

	payload, ok := toolChoice.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", payload["type"])

	function, ok := payload["function"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "add", function["name"])
	require.NotContains(t, payload, "name")
}
