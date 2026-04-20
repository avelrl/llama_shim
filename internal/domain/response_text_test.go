package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseResponseTextConfigAcceptsJSONSchemaName(t *testing.T) {
	config, err := ParseResponseTextConfig(json.RawMessage(`{
		"format":{
			"type":"json_schema",
			"name":"simple_status",
			"strict":true,
			"schema":{
				"type":"object",
				"properties":{
					"status":{"type":"string"}
				},
				"required":["status"],
				"additionalProperties":false
			}
		}
	}`))
	require.NoError(t, err)
	require.Equal(t, "json_schema", config.Format.Type)
	require.Equal(t, "simple_status", config.Format.Name)
	require.NotNil(t, config.Format.Strict)
	require.True(t, *config.Format.Strict)

	var marshaled map[string]any
	require.NoError(t, json.Unmarshal(MarshalResponseTextConfig(config), &marshaled))
	format := marshaled["format"].(map[string]any)
	require.Equal(t, "simple_status", format["name"])
}

func TestNormalizeStructuredOutputJSONTextStripsMarkdownFence(t *testing.T) {
	normalized := NormalizeStructuredOutputJSONText("```json\n{\n  \"status\": \"ok\",\n  \"value\": 42\n}\n```")
	require.JSONEq(t, `{"status":"ok","value":42}`, normalized)
}

func TestNormalizeStructuredOutputJSONTextLeavesNonJSONFenceUntouched(t *testing.T) {
	raw := "```python\nprint(42)\n```"
	require.Equal(t, raw, NormalizeStructuredOutputJSONText(raw))
}
