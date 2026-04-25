package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLocalConstrainedToolSelectionOutputAcceptsMarkdownFencedJSON(t *testing.T) {
	t.Parallel()

	selection, err := parseLocalConstrainedToolSelectionOutput(
		"```json\n{\"selection\":\"tool_math\"}\n```",
		[]localConstrainedToolCandidate{{SelectionID: "tool_math"}},
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "tool_math", selection.SelectionID)
}

func TestParseLocalConstrainedCustomToolRuntimeOutputAcceptsMarkdownFencedJSON(t *testing.T) {
	t.Parallel()

	constraint := mustRegexCustomToolConstraint(t)

	input, err := parseLocalConstrainedCustomToolRuntimeOutput(
		"```json\n{\"input\":\"hello 42\"}\n```",
		customToolDescriptor{Name: "exact_text", Constraint: constraint},
	)
	require.NoError(t, err)
	require.Equal(t, "hello 42", input)
}

func TestLocalConstrainedCustomToolRuntimeShapesJSONSchemaHintAndValidates(t *testing.T) {
	t.Parallel()

	constraint := mustRegexCustomToolConstraint(t)
	var captured map[string]any
	runtime := localConstrainedCustomToolRuntime{
		createChatCompletionText: func(_ context.Context, body []byte) (string, error) {
			require.NoError(t, json.Unmarshal(body, &captured))
			return `{"input":"hello 42"}`, nil
		},
	}

	input, err := runtime.Generate(context.Background(), localConstrainedCustomToolRuntimeRequest{
		Model:      "test-model",
		Options:    map[string]json.RawMessage{"max_output_tokens": json.RawMessage(`32`)},
		Descriptor: customToolDescriptor{Name: "exact_text", Constraint: constraint},
	})
	require.NoError(t, err)
	require.Equal(t, "hello 42", input)
	require.Equal(t, "test-model", captured["model"])
	require.Equal(t, float64(32), captured["max_tokens"])

	responseFormat := captured["response_format"].(map[string]any)
	require.Equal(t, "json_schema", responseFormat["type"])
	require.Equal(t, true, responseFormat["strict"])
	schema := responseFormat["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	inputProperty := properties["input"].(map[string]any)
	require.Equal(t, constraint.Anchored, inputProperty["pattern"])
	require.Equal(t, schema, captured["json_schema"])
}

func TestVLLMRegexConstrainedCustomToolRuntimeShapesStructuredOutputsAndValidates(t *testing.T) {
	t.Parallel()

	constraint := mustRegexCustomToolConstraint(t)
	var captured map[string]any
	runtime := vllmRegexConstrainedCustomToolRuntime{
		createChatCompletionText: func(_ context.Context, body []byte) (string, error) {
			require.NoError(t, json.Unmarshal(body, &captured))
			return "hello 42\n", nil
		},
	}

	input, err := runtime.Generate(context.Background(), localConstrainedCustomToolRuntimeRequest{
		Model: "test-model",
		Options: map[string]json.RawMessage{
			"max_output_tokens": json.RawMessage(`32`),
			"response_format":   json.RawMessage(`{"type":"json_object"}`),
			"json_schema":       json.RawMessage(`{"type":"object"}`),
		},
		Descriptor: customToolDescriptor{Name: "exact_text", Constraint: constraint},
	})
	require.NoError(t, err)
	require.Equal(t, "hello 42", input)
	require.Equal(t, "test-model", captured["model"])
	require.Equal(t, float64(32), captured["max_tokens"])
	require.NotContains(t, captured, "response_format")
	require.NotContains(t, captured, "json_schema")

	structuredOutputs := captured["structured_outputs"].(map[string]any)
	require.Equal(t, constraint.Anchored, structuredOutputs["regex"])
}

func TestVLLMGrammarConstrainedCustomToolRuntimeShapesStructuredOutputsAndValidates(t *testing.T) {
	t.Parallel()

	constraint := mustLarkCustomToolConstraint(t)
	var captured map[string]any
	runtime := vllmGrammarConstrainedCustomToolRuntime{
		createChatCompletionText: func(_ context.Context, body []byte) (string, error) {
			require.NoError(t, json.Unmarshal(body, &captured))
			return "4 + 4\n", nil
		},
	}

	input, err := runtime.Generate(context.Background(), localConstrainedCustomToolRuntimeRequest{
		Model: "test-model",
		Options: map[string]json.RawMessage{
			"max_output_tokens": json.RawMessage(`32`),
			"response_format":   json.RawMessage(`{"type":"json_object"}`),
			"json_schema":       json.RawMessage(`{"type":"object"}`),
		},
		Descriptor: customToolDescriptor{Name: "math_exp", Constraint: constraint},
	})
	require.NoError(t, err)
	require.Equal(t, "4 + 4", input)
	require.Equal(t, "test-model", captured["model"])
	require.Equal(t, float64(32), captured["max_tokens"])
	require.NotContains(t, captured, "response_format")
	require.NotContains(t, captured, "json_schema")

	structuredOutputs := captured["structured_outputs"].(map[string]any)
	require.Equal(t, constraint.VLLMGrammar, structuredOutputs["grammar"])
}

func mustRegexCustomToolConstraint(t *testing.T) *customToolConstraint {
	t.Helper()

	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "exact_text",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "regex",
			"definition": `hello [0-9]+`,
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	return constraint
}

func mustLarkCustomToolConstraint(t *testing.T) *customToolConstraint {
	t.Helper()

	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "math_exp",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	return constraint
}
