package upstreamcompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeChatCompletionRequestRemapsDeveloperRole(t *testing.T) {
	upstreamBody, compatibility, err := NormalizeChatCompletionRequest([]byte(`{
		"model":"test-model",
		"messages":[
			{"role":"developer","content":"Be terse."},
			{"role":"user","content":"Say OK."}
		]
	}`), ChatCompletionOptions{Rules: []ChatCompletionRule{{Model: "test-*", RemapDeveloperRole: true}}})
	require.NoError(t, err)
	require.Equal(t, 1, compatibility.DeveloperRolesRemapped)
	require.False(t, compatibility.DefaultThinkingDisabled)
	require.False(t, compatibility.JSONSchemaDowngraded)

	var request map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &request))
	messages := request["messages"].([]any)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.NotContains(t, request, "thinking")
}

func TestNormalizeChatCompletionRequestAppliesDeepSeekCompatibility(t *testing.T) {
	upstreamBody, compatibility, err := NormalizeChatCompletionRequest([]byte(`{
		"model":"deepseek-v4-pro",
		"messages":[
			{"role":"developer","content":"Return JSON strictly."},
			{"role":"user","content":"Generate status and value."}
		],
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"simple_status",
				"strict":true,
				"schema":{
					"type":"object",
					"properties":{"status":{"type":"string"},"value":{"type":"integer"}},
					"required":["status","value"],
					"additionalProperties":false
				}
			}
		},
		"json_schema":{
			"type":"object",
			"properties":{"legacy_hint":{"type":"string"}}
		}
	}`), ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:              "deepseek-*",
		RemapDeveloperRole: true,
		DefaultThinking:    DefaultThinkingDisabled,
		JSONSchemaMode:     JSONSchemaModeObjectInstruction,
	}}})
	require.NoError(t, err)
	require.Equal(t, 1, compatibility.DeveloperRolesRemapped)
	require.True(t, compatibility.DefaultThinkingDisabled)
	require.True(t, compatibility.JSONSchemaDowngraded)

	var request map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &request))
	thinking := request["thinking"].(map[string]any)
	require.Equal(t, "disabled", thinking["type"])
	responseFormat := request["response_format"].(map[string]any)
	require.Equal(t, "json_object", responseFormat["type"])
	require.NotContains(t, responseFormat, "json_schema")
	require.NotContains(t, request, "json_schema")

	messages := request["messages"].([]any)
	first := messages[0].(map[string]any)
	require.Equal(t, "system", first["role"])
	require.Contains(t, first["content"], "JSON Schema")
	require.Contains(t, first["content"], `"status"`)
	require.Equal(t, "system", messages[1].(map[string]any)["role"])
}

func TestNormalizeChatCompletionRequestDowngradesTopLevelSchemaEnvelope(t *testing.T) {
	upstreamBody, compatibility, err := NormalizeChatCompletionRequest([]byte(`{
		"model":"Qwen3.6-35B-A3B",
		"messages":[{"role":"user","content":"Select a tool."}],
		"response_format":{
			"type":"json_schema",
			"strict":true,
			"schema":{
				"type":"object",
				"properties":{"selection":{"type":"string","enum":["shell","apply_patch"]}},
				"required":["selection"],
				"additionalProperties":false
			}
		}
	}`), ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:          "Qwen*",
		JSONSchemaMode: JSONSchemaModeObjectInstruction,
	}}})
	require.NoError(t, err)
	require.True(t, compatibility.JSONSchemaDowngraded)

	var request map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &request))
	responseFormat := request["response_format"].(map[string]any)
	require.Equal(t, "json_object", responseFormat["type"])
	require.NotContains(t, responseFormat, "json_schema")

	messages := request["messages"].([]any)
	first := messages[0].(map[string]any)
	require.Equal(t, "system", first["role"])
	require.Contains(t, first["content"], "JSON Schema")
	require.Contains(t, first["content"], `"selection"`)
}

func TestNormalizeChatCompletionRequestPreservesExplicitDeepSeekThinking(t *testing.T) {
	upstreamBody, compatibility, err := NormalizeChatCompletionRequest([]byte(`{
		"model":"deepseek-chat",
		"thinking":{"type":"enabled"},
		"messages":[{"role":"user","content":"Say OK."}]
	}`), ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:           "deepseek-*",
		DefaultThinking: DefaultThinkingDisabled,
	}}})
	require.NoError(t, err)
	require.False(t, compatibility.DefaultThinkingDisabled)

	var request map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &request))
	thinking := request["thinking"].(map[string]any)
	require.Equal(t, "enabled", thinking["type"])
}

func TestNormalizeChatCompletionRequestAppliesKimiCompatibility(t *testing.T) {
	upstreamBody, compatibility, err := NormalizeChatCompletionRequest([]byte(`{
		"model":"Kimi-K2.6",
		"messages":[
			{"role":"user","content":"Call read_file."},
			{
				"role":"assistant",
				"content":"",
				"tool_calls":[{
					"id":"call_abc",
					"type":"function",
					"function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}
				}]
			},
			{"role":"tool","tool_call_id":"call_abc","content":"ok"}
		],
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"status",
				"schema":{
					"type":"object",
					"properties":{"status":{"type":"string"}},
					"required":["status"],
					"additionalProperties":false
				}
			}
		},
		"tools":[{
			"type":"function",
			"function":{
				"name":"read_file",
				"description":"Read a file",
				"parameters":{
					"type":"object",
					"properties":{
						"path":{"type":"string"},
						"truncateMode":{"description":"How to truncate","enum":["smart","full","none"]},
						"options":{"properties":{"encoding":{"enum":["utf8","base64"]}}},
						"ranges":{"items":{"minimum":1}},
						"variantOptions":{"$ref":"#/$defs/VariantOptions","description":"Moonshot rejects ref siblings."},
						"renderedSize":{"type":"array","items":[{"type":"number"},{"type":"number"}]}
					},
					"required":["path"],
					"$defs":{"VariantOptions":{"type":"object","description":"Description stays on definition."}}
				}
			}
		}]
	}`), ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:                            "Kimi-*",
		DefaultThinking:                  DefaultThinkingPassthrough,
		DefaultMaxTokens:                 32000,
		JSONSchemaMode:                   JSONSchemaModeObjectInstruction,
		EnsureToolParameterPropertyTypes: true,
		SanitizeMoonshotToolSchema:       true,
		OmitEmptyAssistantToolContent:    true,
		RetryInvalidToolArguments:        true,
	}}})
	require.NoError(t, err)
	require.True(t, compatibility.DefaultMaxTokensApplied)
	require.True(t, compatibility.JSONSchemaDowngraded)
	require.True(t, compatibility.ToolParameterPropertyTypesEnsured)
	require.True(t, compatibility.MoonshotToolSchemaSanitized)
	require.Equal(t, 1, compatibility.EmptyAssistantToolContentOmitted)
	require.True(t, (ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:                     "Kimi-*",
		RetryInvalidToolArguments: true,
	}}}).RetryInvalidToolArguments("Kimi-K2.6"))
	require.Equal(t, InvalidToolArgumentsFallbackFinalText, (ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:                        "Kimi-*",
		InvalidToolArgumentsFallback: InvalidToolArgumentsFallbackFinalText,
	}}}).InvalidToolArgumentsFallback("Kimi-K2.6"))
	require.Equal(t, InvalidToolArgumentsFallbackNone, (ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:                        "Kimi-*",
		InvalidToolArgumentsFallback: "unknown",
	}}}).InvalidToolArgumentsFallback("Kimi-K2.6"))

	var request map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &request))
	require.NotContains(t, request, "thinking")
	require.Equal(t, float64(32000), request["max_tokens"])
	responseFormat := request["response_format"].(map[string]any)
	require.Equal(t, "json_object", responseFormat["type"])
	require.NotContains(t, responseFormat, "json_schema")

	messages := request["messages"].([]any)
	first := messages[0].(map[string]any)
	require.Equal(t, "system", first["role"])
	require.Contains(t, first["content"], "JSON Schema")
	require.Contains(t, first["content"], `"status"`)
	assistant := messages[2].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.NotContains(t, assistant, "content")

	tools := request["tools"].([]any)
	parameters := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	require.Equal(t, "string", properties["truncateMode"].(map[string]any)["type"])
	require.Equal(t, "object", properties["options"].(map[string]any)["type"])
	optionsProps := properties["options"].(map[string]any)["properties"].(map[string]any)
	require.Equal(t, "string", optionsProps["encoding"].(map[string]any)["type"])
	require.Equal(t, "array", properties["ranges"].(map[string]any)["type"])
	require.Equal(t, "number", properties["ranges"].(map[string]any)["items"].(map[string]any)["type"])
	require.Equal(t, map[string]any{"$ref": "#/$defs/VariantOptions"}, properties["variantOptions"].(map[string]any))
	require.Equal(t, map[string]any{"type": "number"}, properties["renderedSize"].(map[string]any)["items"].(map[string]any))
	defs := parameters["$defs"].(map[string]any)
	require.Equal(t, "Description stays on definition.", defs["VariantOptions"].(map[string]any)["description"])
}

func TestNormalizeChatCompletionRequestPreservesExplicitMaxTokens(t *testing.T) {
	upstreamBody, compatibility, err := NormalizeChatCompletionRequest([]byte(`{
		"model":"Kimi-K2.6",
		"max_tokens":128,
		"messages":[{"role":"user","content":"Say OK."}]
	}`), ChatCompletionOptions{Rules: []ChatCompletionRule{{
		Model:            "Kimi-*",
		DefaultMaxTokens: 32000,
	}}})
	require.NoError(t, err)
	require.False(t, compatibility.DefaultMaxTokensApplied)

	var request map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &request))
	require.Equal(t, float64(128), request["max_tokens"])
}

func TestNormalizeChatCompletionRequestDoesNotApplyUnconfiguredDeepSeekRules(t *testing.T) {
	rawBody := []byte(`{
		"model":"deepseek-chat",
		"messages":[{"role":"developer","content":"Say OK."}]
	}`)

	upstreamBody, compatibility, err := NormalizeChatCompletionRequest(rawBody, ChatCompletionOptions{})
	require.NoError(t, err)
	require.False(t, compatibility.Applied())
	require.JSONEq(t, string(rawBody), string(upstreamBody))
}
