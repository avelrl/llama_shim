package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

func TestBuildUpstreamInputItemsPreservesRawItems(t *testing.T) {
	items := []domain.Item{
		domain.NewInputTextMessage("system", "You are a test assistant."),
		domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		domain.NewInputTextMessage("user", "What is the code? Reply with just the number."),
	}

	got := buildUpstreamInputItems(items)

	require.Len(t, got, 3)

	first, err := domain.NewItem(got[0])
	require.NoError(t, err)
	second, err := domain.NewItem(got[1])
	require.NoError(t, err)
	third, err := domain.NewItem(got[2])
	require.NoError(t, err)

	require.Equal(t, "system", first.Role)
	require.Equal(t, "user", second.Role)
	require.Equal(t, "user", third.Role)
	require.Equal(t, "You are a test assistant.", domain.MessageText(first))
	require.Equal(t, "Remember: code=777. Reply OK.", domain.MessageText(second))
	require.Equal(t, "What is the code? Reply with just the number.", domain.MessageText(third))
}

func TestPrepareShadowStoreKeepsMixedInputItems(t *testing.T) {
	request := CreateResponseRequest{
		Model: "test-model",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_1","name":"add","arguments":"{\"a\":1,\"b\":2}"},
			{"type":"function_call_output","call_id":"call_1","output":"{\"result\":3}"}
		]`),
	}

	prepared, input, ok := prepareShadowStore(request, `{"model":"test-model"}`)

	require.True(t, ok)
	require.Equal(t, "test-model", input.Model)
	require.Len(t, prepared.NormalizedInput, 2)
	require.Equal(t, "function_call", prepared.NormalizedInput[0].Type)
	require.Equal(t, "function_call_output", prepared.NormalizedInput[1].Type)
}

func TestShouldFallbackLocalState(t *testing.T) {
	require.True(t, shouldFallbackLocalState(&llama.UpstreamError{
		StatusCode: 500,
		Message:    "backend failed",
	}))
	require.False(t, shouldFallbackLocalState(domain.NewValidationError("input", "input is required")))
}

func TestRemapCustomToolsPayloadRewritesCustomToolsAndSpecificToolChoice(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"model":       json.RawMessage(`"test-model"`),
		"tool_choice": json.RawMessage(`{"type":"custom","name":"code_exec"}`),
		"tools": json.RawMessage(`[
			{"type":"custom","name":"code_exec","description":"Executes arbitrary Python code"}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false)

	require.NoError(t, err)
	require.True(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)

	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, syntheticCustomToolName("", "code_exec"), tool["name"])
	require.Equal(t, "Executes arbitrary Python code", tool["description"])

	parameters, ok := tool["parameters"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", parameters["type"])
	require.Equal(t, false, parameters["additionalProperties"])

	properties, ok := parameters["properties"].(map[string]any)
	require.True(t, ok)
	inputProp, ok := properties["input"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", inputProp["type"])

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolChoice["type"])
	require.Equal(t, syntheticCustomToolName("", "code_exec"), toolChoice["name"])
}

func TestRemapCustomToolsPayloadRejectsUnsupportedGrammar(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[
			{"type":"custom","name":"code_exec","grammar":{"syntax":"lark","definition":"start: /.+/"}}
		]`),
	}

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "tools", validationErr.Param)
}

func TestRemapCustomToolsPayloadAcceptsCustomToolAlias(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`{"type":"custom_tool","name":"code_exec"}`),
		"tools": json.RawMessage(`[
			{"type":"custom_tool","name":"code_exec","description":"Executes arbitrary Python code"}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false)

	require.NoError(t, err)
	require.True(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, syntheticCustomToolName("", "code_exec"), tool["name"])

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolChoice["type"])
	require.Equal(t, syntheticCustomToolName("", "code_exec"), toolChoice["name"])
}

func TestRemapCustomToolsPayloadDropsDisabledWebSearchTool(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
			{"type":"web_search","external_web_access":false}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false)

	require.NoError(t, err)
	require.False(t, plan.BridgeActive())
	require.Equal(t, []string{"web_search"}, plan.DroppedBuiltinTools)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "exec_command", tool["name"])
}

func TestRemapCustomToolsPayloadRejectsSupportedWebSearchBuiltIn(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[
			{"type":"web_search","external_web_access":true}
		]`),
	}

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "tools", validationErr.Param)
}

func TestRemapCustomToolsPayloadForcesRequiredToolChoiceForCodexAuto(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "required", payload["tool_choice"])
}

func TestRemapCustomToolsPayloadKeepsAutoToolChoiceForNonCodexRequest(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a normal assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "auto", payload["tool_choice"])
}

func TestRemapCustomToolsPayloadAppendsCodexCompatibilityHint(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", false)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Contains(t, payload["instructions"], codexCompatibilityHint)
}

func TestNormalizeUpstreamResponseBodyDowngradesSafeExecCommandEscalation(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"model":"test-model",
		"output_text":"",
		"output":[
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"exec_command",
				"arguments":"{\"cmd\":\"cd /tmp/snake_test && go test ./game -v 2>&1\",\"sandbox_permissions\":\"require_escalated\",\"justification\":\"Need approval to run tests\"}",
				"status":"completed"
			}
		]
	}`)

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{}, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	output := payload["output"].([]any)
	item := output[0].(map[string]any)

	var arguments map[string]any
	require.NoError(t, json.Unmarshal([]byte(item["arguments"].(string)), &arguments))
	require.Equal(t, "go test ./game -v", arguments["cmd"])
	require.Equal(t, "/tmp/snake_test", arguments["workdir"])
	require.Equal(t, float64(30000), arguments["yield_time_ms"])
	require.Equal(t, float64(6000), arguments["max_output_tokens"])
	require.NotContains(t, arguments, "sandbox_permissions")
	require.NotContains(t, arguments, "justification")
}

func TestNormalizeUpstreamResponseBodyKeepsExecCommandEscalationForNetworkCommand(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"model":"test-model",
		"output_text":"",
		"output":[
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"exec_command",
				"arguments":"{\"cmd\":\"curl -I https://example.com\",\"sandbox_permissions\":\"require_escalated\",\"justification\":\"Need network\"}",
				"status":"completed"
			}
		]
	}`)

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{}, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	output := payload["output"].([]any)
	item := output[0].(map[string]any)
	require.Contains(t, item["arguments"].(string), `"sandbox_permissions":"require_escalated"`)
}

func TestNormalizeUpstreamResponseBodyDropsCompletedPlanLoopAndSynthesizesMessage(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"model":"test-model",
		"output_text":"",
		"output":[
			{
				"id":"rs_1",
				"type":"reasoning",
				"status":"completed",
				"content":[{"type":"reasoning_text","text":"All tasks are complete. Let me provide a summary to the user."}]
			},
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"update_plan",
				"arguments":"{\"plan\":[{\"status\":\"completed\",\"step\":\"done\"}]}",
				"status":"completed"
			}
		]
	}`)

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{}, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "All tasks are complete.", payload["output_text"])

	output := payload["output"].([]any)
	require.Len(t, output, 2)
	require.Equal(t, "reasoning", output[0].(map[string]any)["type"])
	require.Equal(t, "message", output[1].(map[string]any)["type"])
	require.Equal(t, "assistant", output[1].(map[string]any)["role"])
}

func TestRemapCustomToolResponseBodyRestoresOnlyCustomTools(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"output_text":"",
		"output":[
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				"arguments":"{\"input\":\"print(\\\"hello world\\\")\"}",
				"status":"completed"
			},
			{
				"type":"function_call",
				"call_id":"call_2",
				"name":"add",
				"arguments":"{\"a\":1,\"b\":2}",
				"status":"completed"
			}
		]
	}`)

	body, err := remapCustomToolResponseBody(raw, customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			BySynthetic: map[string]customToolDescriptor{
				"shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d": {
					Name:          "code_exec",
					SyntheticName: "shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				},
			},
			ByCanonical: map[string]customToolDescriptor{
				canonicalCustomToolKey("", "code_exec"): {
					Name:          "code_exec",
					SyntheticName: "shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				},
			},
		},
	})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	output, ok := payload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 2)

	customCall, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", customCall["type"])
	require.Equal(t, "fc_1", customCall["id"])
	require.Equal(t, "call_1", customCall["call_id"])
	require.Equal(t, "code_exec", customCall["name"])
	require.Equal(t, `print("hello world")`, customCall["input"])
	require.Equal(t, "completed", customCall["status"])

	functionCall, ok := output[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", functionCall["type"])
	require.Equal(t, "add", functionCall["name"])
}
