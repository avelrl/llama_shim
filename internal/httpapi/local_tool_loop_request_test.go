package httpapi

import (
	"encoding/json"
	"fmt"
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

	_, plan, toolChoice, _, err := buildLocalToolLoopTransportPlan(rawFields, tools, ServiceLimits{})

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

	_, plan, toolChoice, _, err := buildLocalToolLoopTransportPlan(rawFields, tools, ServiceLimits{})

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

func TestBuildLocalChatCompletionRequestRewritesTrailingCodexAgentEnvelope(t *testing.T) {
	contextItems := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"`+codexCLIRequestMarker+`"}`),
		mustDomainItem(t, `{"type":"message","role":"assistant","content":"{\"author\":\"/root\",\"recipient\":\"/root/project_explorer\",\"other_recipients\":[],\"content\":\"Inspect /tmp/project.\",\"trigger_turn\":true}"}`),
		mustDomainItem(t, `{"type":"message","role":"assistant","content":"{\"author\":\"/root/project_explorer\",\"recipient\":\"/root/project_explorer\",\"other_recipients\":[],\"content\":\"Return the final report.\",\"trigger_turn\":false}"}`),
	}
	rawFields := map[string]json.RawMessage{
		"model":        json.RawMessage(`"test-model"`),
		"instructions": json.RawMessage(`"` + codexCLIRequestMarker + `"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","description":"Runs a command.","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := buildLocalChatCompletionRequest(rawFields, contextItems, contextItems[1:], nil, ServiceLimits{}, true, "")

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, messages)
	last, ok := messages[len(messages)-1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user", last["role"])
	require.Equal(t, "Inspect /tmp/project.\n\nReturn the final report.", last["content"])
}

func TestRewriteTrailingCodexAgentEnvelopeMessageLeavesOrdinaryAssistantText(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "Hello"},
		{"role": "assistant", "content": "{\"content\":\"missing routing fields\",\"trigger_turn\":true}"},
	}

	rewritten := rewriteTrailingCodexAgentEnvelopeMessage(messages)

	require.Equal(t, messages, rewritten)
	require.Equal(t, "assistant", rewritten[1]["role"])
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

func TestBuildChatCompletionMessagesFromItemsPreservesCodexInteractiveSessionOutput(t *testing.T) {
	output := "Chunk ID: abc\nWall time: 1.0000 seconds\nProcess running with session ID 7\nOutput:\nREADY_FOR_STDIN\n"
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Start an interactive process."}`),
		mustDomainItem(t, `{"type":"function_call","id":"item_exec","call_id":"call_exec","name":"exec_command","arguments":"{\"cmd\":\"python3 -q\",\"tty\":true}"}`),
		mustDomainItem(t, fmt.Sprintf(`{"type":"function_call_output","call_id":"call_exec","output":%q}`, output)),
		mustDomainItem(t, `{"type":"message","role":"user","content":"Send stdin now."}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 4)
	require.Equal(t, "assistant", messages[1]["role"])
	toolCalls, ok := messages[1]["tool_calls"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_exec", toolCalls[0]["id"])
	require.Equal(t, "exec_command", toolCalls[0]["function"].(map[string]any)["name"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_exec", messages[2]["tool_call_id"])
	require.Equal(t, output, asString(messages[2]["content"]))
	require.Equal(t, "7", localToolLoopSessionIDForLog(asString(messages[2]["content"])))
	require.Equal(t, "7", localToolLoopSessionIDForLog(`{"session_id":7,"output":"READY_FOR_STDIN\n"}`))
}

func TestBuildChatCompletionMessagesFromItemsGroupsParallelToolCalls(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Inspect and test."}`),
		mustDomainItem(t, `{"type":"shell_call","id":"item_read","call_id":"call_read","action":{"commands":["cat calc.go"]},"status":"completed"}`),
		mustDomainItem(t, `{"type":"shell_call","id":"item_test","call_id":"call_test","action":{"commands":["go test ./..."]},"status":"completed"}`),
		mustDomainItem(t, `{"type":"shell_call_output","call_id":"call_read","output":"package sample\n","status":"completed"}`),
		mustDomainItem(t, `{"type":"shell_call_output","call_id":"call_test","output":"FAIL\n","status":"failed"}`),
		mustDomainItem(t, `{"type":"message","role":"user","content":"Continue."}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 5)
	require.Equal(t, "assistant", messages[1]["role"])
	toolCalls, ok := messages[1]["tool_calls"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, toolCalls, 2)
	require.Equal(t, "call_read", toolCalls[0]["id"])
	require.Equal(t, "call_test", toolCalls[1]["id"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_read", messages[2]["tool_call_id"])
	require.Equal(t, "tool", messages[3]["role"])
	require.Equal(t, "call_test", messages[3]["tool_call_id"])
	require.Equal(t, "user", messages[4]["role"])
}

func TestBuildChatCompletionMessagesFromItemsSkipsReasoningItems(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Inspect the file."}`),
		mustDomainItem(t, `{"type":"reasoning","summary":[]}`),
		mustDomainItem(t, `{"type":"function_call","id":"item_read","call_id":"call_read","name":"exec_command","arguments":"{\"cmd\":\"cat README.md\"}"}`),
		mustDomainItem(t, `{"type":"function_call_output","call_id":"call_read","output":"README contents"}`),
		mustDomainItem(t, `{"type":"message","role":"user","content":"Continue."}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 4)
	require.Equal(t, "assistant", messages[1]["role"])
	toolCalls, ok := messages[1]["tool_calls"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_read", toolCalls[0]["id"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "user", messages[3]["role"])
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

func TestBuildChatCompletionMessagesFromItemsDowngradesOrphanToolOutput(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Continue from prior context."}`),
		mustDomainItem(t, `{"type":"function_call_output","call_id":"call_orphan","output":"late result"}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "user", messages[1]["role"])
	require.NotContains(t, messages[1], "tool_call_id")
	require.Contains(t, messages[1]["content"], "without a matching preceding tool call")
	require.Contains(t, messages[1]["content"], "call_orphan")
	require.Contains(t, messages[1]["content"], "late result")
}

func TestBuildChatCompletionMessagesFromItemsWithRefsPreservesKnownToolOutput(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Continue from prior context."}`),
		mustDomainItem(t, `{"type":"function_call_output","call_id":"call_known","output":"known result"}`),
	}
	refs := map[string]domain.ToolCallReference{
		"call_known": {
			Type: "function_call",
			Name: "exec_command",
		},
	}

	messages, err := buildChatCompletionMessagesFromItemsWithRefs(items, refs)

	require.NoError(t, err)
	require.Len(t, messages, 3)
	require.Equal(t, "assistant", messages[1]["role"])
	toolCalls, ok := messages[1]["tool_calls"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_known", toolCalls[0]["id"])
	require.Equal(t, "exec_command", toolCalls[0]["function"].(map[string]any)["name"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_known", messages[2]["tool_call_id"])
	require.Equal(t, "known result", messages[2]["content"])
}

func TestBuildChatCompletionMessagesFromItemsFlushesBeforeOutOfOrderOrphanToolOutput(t *testing.T) {
	items := []domain.Item{
		mustDomainItem(t, `{"type":"message","role":"user","content":"Call add."}`),
		mustDomainItem(t, `{"type":"function_call","id":"item_123","call_id":"call_missing","name":"add","arguments":"{\"a\":40,\"b\":2}"}`),
		mustDomainItem(t, `{"type":"function_call_output","call_id":"call_orphan","output":"late unrelated result"}`),
	}

	messages, err := buildChatCompletionMessagesFromItems(items)

	require.NoError(t, err)
	require.Len(t, messages, 4)
	require.Equal(t, "assistant", messages[1]["role"])
	require.Equal(t, "tool", messages[2]["role"])
	require.Equal(t, "call_missing", messages[2]["tool_call_id"])
	require.Contains(t, messages[2]["content"], "tool output was not supplied")
	require.Equal(t, "user", messages[3]["role"])
	require.Contains(t, messages[3]["content"], "call_orphan")
	require.Contains(t, messages[3]["content"], "late unrelated result")
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

func TestParseLocalToolLoopChatCompletionRepairsApplyPatchRepeatedEnvelopes(t *testing.T) {
	descriptor := customToolDescriptor{
		Name:          "apply_patch",
		SyntheticName: syntheticCustomToolName("", "apply_patch"),
		Constraint:    mustApplyPatchCustomToolConstraint(t),
		Transport:     customToolTransportLocalConstrained,
	}
	plan := customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			ByModelName: map[string]customToolDescriptor{
				"apply_patch": descriptor,
			},
			BySynthetic: map[string]customToolDescriptor{
				descriptor.SyntheticName: descriptor,
			},
			ByCanonical: map[string]customToolDescriptor{
				canonicalCustomToolKey("", "apply_patch"): descriptor,
			},
		},
	}

	raw := []byte(`{
		"choices": [{
			"message": {
				"tool_calls": [{
					"id": "call_patch",
					"type": "function",
					"function": {
						"name": "apply_patch",
						"arguments": "{\"input\":\"*** Begin Patch\\n*** Update File: app/config.txt\\n@@ \\n mode=matrix\\n-feature=disabled\\n+feature=enabled\\n*** End Patch\\n*** Begin Patch\\n*** Update File: app/status.txt\\n@@ \\n-status=todo\\n+status=updated\\n*** End Patch\"}"
					}
				}]
			}
		}]
	}`)

	response, err := parseLocalToolLoopChatCompletion(raw, "resp_test", "test-model", "", "", plan)
	require.NoError(t, err)
	require.Len(t, response.Output, 1)
	require.Equal(t, "custom_tool_call", response.Output[0].Type)
	require.Equal(t, "*** Begin Patch\n*** Update File: app/config.txt\n@@\n mode=matrix\n-feature=disabled\n+feature=enabled\n*** Update File: app/status.txt\n@@\n-status=todo\n+status=updated\n*** End Patch", response.Output[0].Input())
}

func TestParseLocalToolLoopChatCompletionRepairsApplyPatchUnifiedDiffHunkHeaders(t *testing.T) {
	descriptor := customToolDescriptor{
		Name:          "apply_patch",
		SyntheticName: syntheticCustomToolName("", "apply_patch"),
		Constraint:    mustApplyPatchCustomToolConstraint(t),
		Transport:     customToolTransportLocalConstrained,
	}
	plan := customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			ByModelName: map[string]customToolDescriptor{
				"apply_patch": descriptor,
			},
			BySynthetic: map[string]customToolDescriptor{
				descriptor.SyntheticName: descriptor,
			},
			ByCanonical: map[string]customToolDescriptor{
				canonicalCustomToolKey("", "apply_patch"): descriptor,
			},
		},
	}

	raw := []byte(`{
		"choices": [{
			"message": {
				"tool_calls": [{
					"id": "call_patch",
					"type": "function",
					"function": {
						"name": "apply_patch",
						"arguments": "{\"input\":\"*** Begin Patch\\n*** Update File: mathutil.go\\n@@ -1,5 +1,5 @@\\n package codexsmoke\\n\\n func Add(a, b int) int {\\n-\\treturn a - b\\n+\\treturn a + b\\n }\\n*** End Patch\"}"
					}
				}]
			}
		}]
	}`)

	response, err := parseLocalToolLoopChatCompletion(raw, "resp_test", "test-model", "", "", plan)
	require.NoError(t, err)
	require.Len(t, response.Output, 1)
	require.Equal(t, "*** Begin Patch\n*** Update File: mathutil.go\n@@\n package codexsmoke\n \n func Add(a, b int) int {\n-\treturn a - b\n+\treturn a + b\n }\n*** End Patch", response.Output[0].Input())
}

func TestParseLocalToolLoopChatCompletionMapsChatUsageToResponsesUsage(t *testing.T) {
	raw := []byte(`{
		"choices": [{
			"message": {
				"content": "done"
			}
		}],
		"usage": {
			"prompt_tokens": 10,
			"prompt_tokens_details": {"cached_tokens": 3},
			"completion_tokens": 4,
			"completion_tokens_details": {"reasoning_tokens": 1},
			"total_tokens": 14
		}
	}`)

	response, err := parseLocalToolLoopChatCompletion(raw, "resp_test", "test-model", "", "", customToolTransportPlan{})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"input_tokens": 10,
		"input_tokens_details": {"cached_tokens": 3},
		"output_tokens": 4,
		"output_tokens_details": {"reasoning_tokens": 1},
		"total_tokens": 14
	}`, string(response.Usage))
}

func TestBuildLocalCustomToolLoopInstructionsAddsApplyPatchFormatHint(t *testing.T) {
	descriptor := customToolDescriptor{
		Name:       "apply_patch",
		Constraint: mustApplyPatchCustomToolConstraint(t),
	}

	instructions := buildLocalCustomToolLoopInstructions([]customToolDescriptor{descriptor})

	require.Contains(t, instructions, "Do not place a whole replacement file directly")
}

func TestBuildConstrainedCustomToolRepairPromptAddsApplyPatchFormatHint(t *testing.T) {
	descriptor := customToolDescriptor{
		Name:       "apply_patch",
		Constraint: mustApplyPatchCustomToolConstraint(t),
	}

	prompt := buildConstrainedCustomToolRepairPrompt(&constrainedCustomToolValidationError{
		Descriptor: descriptor,
		Input:      "*** Begin Patch\n*** Update File: mathutil.go\npackage codexsmoke\n*** End Patch",
	})

	require.Contains(t, prompt, "Retry by emitting the same tool call again")
	require.Contains(t, prompt, "Do not place a whole replacement file directly")
}

func TestContainsRawToolCallMarkupCatchesDeepSeekPseudoTools(t *testing.T) {
	cases := []string{
		"<\uFF5C\uFF5CDSML\uFF5C\uFF5Ctool_calls><\uFF5C\uFF5CDSML\uFF5C\uFF5Cinvoke name=\"read\">README.md</\uFF5C\uFF5CDSML\uFF5C\uFF5Cinvoke></\uFF5C\uFF5CDSML\uFF5C\uFF5Ctool_calls>",
		"```json\n" + `{"agent":"cli","command":["bash","-c","cat README.md"],"cwd":"/tmp/workspace"}` + "\n```",
		"```json\n" + `{"command":["apply_patch","*** Begin Patch\n*** End Patch"]}` + "\n```",
	}
	for _, text := range cases {
		require.True(t, containsRawToolCallMarkup(text), "expected pseudo-tool detection for %q", text)
	}
	require.False(t, containsRawToolCallMarkup("```json\n{\"command\":\"status\",\"value\":\"ok\"}\n```"))
}

func mustDomainItem(t *testing.T, raw string) domain.Item {
	t.Helper()

	item, err := domain.NewItem([]byte(raw))
	require.NoError(t, err)
	return item
}
