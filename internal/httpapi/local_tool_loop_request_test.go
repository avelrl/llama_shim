package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
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

func TestBuildLocalToolLoopTransportPlanConvertsShellToolChoiceToChatShape(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`{"type":"shell"}`),
	}
	tools := []map[string]any{
		{
			"type": "shell",
			"environment": map[string]any{
				"type": "local",
			},
		},
	}

	_, plan, toolChoice, _, err := buildLocalToolLoopTransportPlan(rawFields, tools, ServiceLimits{}, false)

	require.NoError(t, err)
	require.Equal(t, toolChoiceContractRequiredNamedFunction, plan.ToolChoiceContract.Mode)
	require.Equal(t, localBuiltinShellToolType, plan.ToolChoiceContract.Name)

	payload, ok := toolChoice.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", payload["type"])

	function, ok := payload["function"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, localBuiltinShellSyntheticName, function["name"])
}

func TestBuildChatCompletionMessagesFromItemsUsesResponsesCallIDForToolCalls(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Call add."}`),
		mustDomainItem(t, `{"type":"function_call","id":"item_123","call_id":"call_abc","name":"add","arguments":"{\"a\":40,\"b\":2}"}`),
		mustDomainItem(t, `{"type":"function_call_output","call_id":"call_abc","output":"{\"result\":42}"}`),
		mustDomainItem(t, `{"type":"message","role":"user","content":"Reply with the result."}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 4)

	toolCalls, ok := messages[1]["tool_calls"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_abc", toolCalls[0]["id"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_abc", messages[2]["tool_call_id"])
}

func TestBuildChatCompletionMessagesFromItemsSynthesizesMissingToolOutput(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Call add."}`),
		mustDomainItem(t, `{"type":"function_call","id":"item_123","call_id":"call_missing","name":"add","arguments":"{\"a\":40,\"b\":2}"}`),
		mustDomainItem(t, `{"type":"message","role":"user","content":"Continue."}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 4)
	require.Equal(t, "assistant", messages[1]["role"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_missing", messages[2]["tool_call_id"])
	require.Contains(t, messages[2]["content"], "tool output was not supplied")
	require.Equal(t, "user", messages[3]["role"])
}

func TestBuildChatCompletionMessagesFromItemsSynthesizesMissingApplyPatchOutput(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Patch the file."}`),
		mustDomainItem(t, `{"type":"apply_patch_call","id":"item_patch","call_id":"call_patch","status":"completed","operation":{"type":"update_file","path":"smoke_target.txt"}}`),
		mustDomainItem(t, `{"type":"message","role":"user","content":"Use another edit path."}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 4)
	require.Equal(t, "assistant", messages[1]["role"])
	toolCalls, ok := messages[1]["tool_calls"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, localBuiltinApplyPatchSyntheticName, toolCalls[0]["function"].(map[string]any)["name"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_patch", messages[2]["tool_call_id"])
	require.Contains(t, messages[2]["content"], "tool output was not supplied")
	require.Equal(t, "user", messages[3]["role"])
}

func mustDomainItem(t *testing.T, raw string) domain.Item {
	t.Helper()

	item, err := domain.NewItem([]byte(raw))
	require.NoError(t, err)
	return item
}
