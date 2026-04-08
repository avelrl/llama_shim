package httpapi

import (
	"encoding/json"
	"net/http"
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

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

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
	require.Equal(t, "code_exec", tool["name"])
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
	require.Equal(t, "code_exec", toolChoice["name"])
}

func TestRemapCustomToolsPayloadRejectsUnsupportedGrammar(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[
			{"type":"custom","name":"code_exec","grammar":{"syntax":"lark","definition":"start: /.+/"}}
		]`),
	}

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

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

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

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
	require.Equal(t, "code_exec", tool["name"])
	parameters, ok := tool["parameters"].(map[string]any)
	require.True(t, ok)
	properties, ok := parameters["properties"].(map[string]any)
	require.True(t, ok)
	inputProp, ok := properties["input"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, inputProp["description"], "Escape any inner double quotes")

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolChoice["type"])
	require.Equal(t, "code_exec", toolChoice["name"])
	require.Contains(t, payload["instructions"], customToolBridgeHintPrefix)
	require.Contains(t, payload["instructions"], "Available bridged custom tools: code_exec.")
	require.Equal(t, toolChoiceContractRequiredNamedCustom, plan.ToolChoiceContract.Mode)
	require.Equal(t, "code_exec", plan.ToolChoiceContract.Name)
}

func TestRemapCustomToolsPayloadDropsDisabledWebSearchTool(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
			{"type":"web_search","external_web_access":false}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

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

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

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

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", true, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "required", payload["tool_choice"])
	require.Equal(t, toolChoiceContractRequiredAny, plan.ToolChoiceContract.Mode)
}

func TestRemapCustomToolsPayloadKeepsAutoToolChoiceForNonCodexRequest(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a normal assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", true, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "auto", payload["tool_choice"])
}

func TestRemapCustomToolsPayloadCapturesRequiredToolChoiceContract(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`"required"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"add","parameters":{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}}
		]`),
	}

	_, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)
	require.Equal(t, toolChoiceContractRequiredAny, plan.ToolChoiceContract.Mode)
}

func TestShouldRetryToolChoiceWithAutoBody(t *testing.T) {
	plan := customToolTransportPlan{
		ToolChoiceContract: toolChoiceContract{Mode: toolChoiceContractRequiredAny},
	}

	require.True(t, shouldRetryToolChoiceWithAutoBody(http.StatusNotImplemented, []byte(`{"error":{"message":"Only 'auto' tool_choice is supported in response API with Harmony"}}`), plan))
	require.False(t, shouldRetryToolChoiceWithAutoBody(http.StatusNotImplemented, []byte(`{"error":{"message":"different error"}}`), plan))
}

func TestEnforceToolChoiceContractRejectsAssistantText(t *testing.T) {
	err := enforceToolChoiceContract(domain.Response{
		OutputText: "AUTO_FALLBACK_TEXT",
		Output:     []domain.Item{domain.NewOutputTextMessage("AUTO_FALLBACK_TEXT")},
	}, toolChoiceContract{Mode: toolChoiceContractRequiredAny})

	var incompatErr *toolChoiceIncompatibleBackendError
	require.ErrorAs(t, err, &incompatErr)
	require.Contains(t, incompatErr.Error(), "required tool call")
}

func TestEnforceToolChoiceContractAcceptsMatchingFunctionCall(t *testing.T) {
	item, err := domain.NewItem([]byte(`{"type":"function_call","call_id":"call_1","name":"add","arguments":"{\"a\":1,\"b\":2}"}`))
	require.NoError(t, err)

	err = enforceToolChoiceContract(domain.Response{
		Output: []domain.Item{item},
	}, toolChoiceContract{Mode: toolChoiceContractRequiredNamedFunction, Name: "add"})

	require.NoError(t, err)
}

func TestRemapCustomToolsPayloadAppendsCodexCompatibilityHint(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","description":"Runs a command in a PTY.","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
			{"type":"function","name":"apply_patch","description":"Patch files.","parameters":{"type":"object"}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", true, false)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Contains(t, payload["instructions"], codexCompatibilityHint)

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)

	execCommand, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Contains(t, execCommand["description"], "single shell string")
	require.Contains(t, execCommand["description"], "apply_patch tool directly")

	applyPatch, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.Contains(t, applyPatch["description"], "use this tool directly")
}

func TestRemapCustomToolsPayloadSkipsCodexCompatibilityWhenDisabled(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", false, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "required", payload["tool_choice"])
	require.NotContains(t, payload["instructions"], codexCompatibilityHint)
}

func TestRemapCustomToolsPayloadKeepsAutoWithoutCompatAndWithoutForce(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "auto", payload["tool_choice"])
	require.NotContains(t, payload["instructions"], codexCompatibilityHint)
}

func TestNormalizeUpstreamResponseBodyLeavesExecCommandUntouched(t *testing.T) {
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

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	output := payload["output"].([]any)
	item := output[0].(map[string]any)
	require.Contains(t, item["arguments"].(string), `"sandbox_permissions":"require_escalated"`)
	require.Contains(t, item["arguments"].(string), `"justification":"Need approval to run tests"`)
}

func TestNormalizeUpstreamResponseBodyDoesNotSynthesizeAssistantMessage(t *testing.T) {
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

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "", payload["output_text"])

	output := payload["output"].([]any)
	require.Len(t, output, 2)
	require.Equal(t, "reasoning", output[0].(map[string]any)["type"])
	require.Equal(t, "function_call", output[1].(map[string]any)["type"])
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
				"name":"code_exec",
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
			ByModelName: map[string]customToolDescriptor{
				"code_exec": {
					Name:          "code_exec",
					SyntheticName: "shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				},
			},
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

func TestRemapCustomToolsPayloadRejectsDuplicateBridgeNamesAcrossNamespaces(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[
			{"type":"custom","namespace":"shell","name":"exec"},
			{"type":"custom","namespace":"python","name":"exec"}
		]`),
	}

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "tools", validationErr.Param)
}

func TestRemapCustomToolResponseBodyRecoversPlaceholderMessageFromReasoning(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"id":          "resp_152511",
		"object":      "response",
		"output_text": "",
		"output": []map[string]any{
			{
				"id":   "rs_resp_152511",
				"type": "reasoning",
				"summary": []map[string]any{
					{
						"type": "summary_text",
						"text": "The user wants me to use the `code_exec` tool to print \"hello world\" to the console.\n" +
							"I should not answer directly, but instead emit a tool call.\n\n" +
							"Plan:\n1. Formulate the Python code: `print(\"hello world\")`.\n" +
							"2. Format it as a JSON string for the `code_exec` tool's `input` parameter.\n" +
							"3. Call the `code_exec` tool.",
					},
				},
			},
			{
				"id":     "msg_903606",
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": "<|tool_response|><|tool_response|><|tool_response|>\n"},
				},
			},
		},
	})
	require.NoError(t, err)

	body, err := remapCustomToolResponseBody(raw, customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			ByModelName: map[string]customToolDescriptor{
				"code_exec": {
					Name:          "code_exec",
					SyntheticName: syntheticCustomToolName("", "code_exec"),
				},
			},
			BySynthetic: map[string]customToolDescriptor{
				syntheticCustomToolName("", "code_exec"): {
					Name:          "code_exec",
					SyntheticName: syntheticCustomToolName("", "code_exec"),
				},
			},
			ByCanonical: map[string]customToolDescriptor{
				canonicalCustomToolKey("", "code_exec"): {
					Name:          "code_exec",
					SyntheticName: syntheticCustomToolName("", "code_exec"),
				},
			},
		},
	})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	output := payload["output"].([]any)
	require.Len(t, output, 2)

	recovered := output[1].(map[string]any)
	require.Equal(t, "custom_tool_call", recovered["type"])
	require.Equal(t, "code_exec", recovered["name"])
	require.Equal(t, `print("hello world")`, recovered["input"])
	require.Equal(t, "msg_903606", recovered["id"])
	require.Equal(t, "call_903606", recovered["call_id"])
}

func TestBuildUpstreamResponsesBodyReplaysBridgeCustomToolsWithoutCurrentTools(t *testing.T) {
	call, err := domain.NewItem([]byte(`{
		"id":"ctc_1",
		"type":"custom_tool_call",
		"call_id":"call_1",
		"name":"code_exec",
		"input":"print(\"hello world\")",
		"status":"completed"
	}`))
	require.NoError(t, err)
	call.Meta = &domain.ItemMeta{
		Transport:     "bridge",
		SyntheticName: syntheticCustomToolName("", "code_exec"),
		CanonicalType: "custom_tool_call",
		ToolName:      "code_exec",
	}

	output, err := domain.NewItem([]byte(`{
		"type":"custom_tool_call_output",
		"call_id":"call_1",
		"output":"tool says hi"
	}`))
	require.NoError(t, err)

	refs := domain.CollectToolCallReferences([]domain.Item{call})
	body, plan, err := buildUpstreamResponsesBody(
		map[string]json.RawMessage{
			"model": json.RawMessage(`"test-model"`),
		},
		[]domain.Item{call, output},
		[]domain.Item{output},
		refs,
		"bridge",
		false,
		false,
	)
	require.NoError(t, err)
	require.True(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	input, ok := payload["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	callItem, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", callItem["type"])
	require.Equal(t, "code_exec", callItem["name"])
	require.Equal(t, `{"input":"print(\"hello world\")"}`, callItem["arguments"])

	outputItem, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call_output", outputItem["type"])
	require.Equal(t, "call_1", outputItem["call_id"])
	require.Equal(t, "tool says hi", outputItem["output"])
}
