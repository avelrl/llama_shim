package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/llama"
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

func TestParseLocalConstrainedCustomToolRuntimeOutputRepairsApplyPatchRepeatedEnvelopes(t *testing.T) {
	t.Parallel()

	descriptor := customToolDescriptor{Name: "apply_patch", Constraint: mustApplyPatchCustomToolConstraint(t)}
	input, err := parseLocalConstrainedCustomToolRuntimeOutput(
		"```json\n{\"input\":\"*** Begin Patch\\n*** Update File: app/config.txt\\n@@ \\n mode=matrix\\n-feature=disabled\\n+feature=enabled\\n*** End Patch\\n*** Begin Patch\\n*** Update File: app/status.txt\\n@@ \\n-status=todo\\n+status=updated\\n*** End Patch\"}\n```",
		descriptor,
	)
	require.NoError(t, err)
	require.Equal(t, "*** Begin Patch\n*** Update File: app/config.txt\n@@\n mode=matrix\n-feature=disabled\n+feature=enabled\n*** Update File: app/status.txt\n@@\n-status=todo\n+status=updated\n*** End Patch\n", input)
	require.NoError(t, descriptor.Constraint.Validate(input))
}

func TestParseLocalConstrainedCustomToolRuntimeOutputRepairsApplyPatchUnprefixedContextLines(t *testing.T) {
	t.Parallel()

	descriptor := customToolDescriptor{Name: "apply_patch", Constraint: mustApplyPatchCustomToolConstraint(t)}
	input, err := parseLocalConstrainedCustomToolRuntimeOutput(
		"```json\n{\"input\":\"*** Begin Patch\\n*** Update File: mathutil.go\\n@@ func Add(a, b int) int {\\n-\\treturn a - b\\n+\\treturn a + b\\n}\\n*** End Patch\"}\n```",
		descriptor,
	)
	require.NoError(t, err)
	require.Equal(t, "*** Begin Patch\n*** Update File: mathutil.go\n@@ func Add(a, b int) int {\n-\treturn a - b\n+\treturn a + b\n }\n*** End Patch", input)
	require.NoError(t, descriptor.Constraint.Validate(input))
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

func TestFallbackConstrainedCustomToolRuntimeFallsBackFromNativeFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		firstText string
		firstErr  error
	}{
		{name: "invalid_output", firstText: "not valid"},
		{name: "upstream_4xx", firstErr: &llama.UpstreamError{StatusCode: 400, Message: "structured_outputs not supported"}},
		{name: "timeout", firstErr: &llama.TimeoutError{Message: "native constrained runtime timed out"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			constraint := mustRegexCustomToolConstraint(t)
			var captured []map[string]any
			createChatCompletionText := func(_ context.Context, body []byte) (string, error) {
				var request map[string]any
				require.NoError(t, json.Unmarshal(body, &request))
				captured = append(captured, request)
				if len(captured) == 1 {
					return tc.firstText, tc.firstErr
				}
				return `{"input":"hello 42"}`, nil
			}
			runtime := fallbackConstrainedCustomToolRuntime{
				primary: vllmRegexConstrainedCustomToolRuntime{
					createChatCompletionText: createChatCompletionText,
				},
				fallback: localConstrainedCustomToolRuntime{
					createChatCompletionText: createChatCompletionText,
				},
			}

			input, err := runtime.Generate(context.Background(), localConstrainedCustomToolRuntimeRequest{
				Model:      "test-model",
				Options:    map[string]json.RawMessage{"max_output_tokens": json.RawMessage(`32`)},
				Descriptor: customToolDescriptor{Name: "exact_text", Constraint: constraint},
			})
			require.NoError(t, err)
			require.Equal(t, "hello 42", input)
			require.Len(t, captured, 2)
			require.Contains(t, captured[0], "structured_outputs")
			require.NotContains(t, captured[0], "response_format")
			require.NotContains(t, captured[1], "structured_outputs")
			require.Contains(t, captured[1], "response_format")
			require.Contains(t, captured[1], "json_schema")
		})
	}
}

func TestConstrainedCustomToolBackendRegistrySelectsVLLMAdapter(t *testing.T) {
	t.Parallel()

	registry := defaultConstrainedCustomToolBackendRegistry()
	adapter, ok := registry.Adapter("vllm")
	require.True(t, ok)

	capability := adapter.Capability()
	require.Equal(t, "grammar_native", capability.CapabilityClass)
	require.ElementsMatch(t, []string{"grammar.regex", "grammar.lark_subset"}, capability.NativeFormats)

	deps := constrainedCustomToolRuntimeDeps{
		createChatCompletionText: func(context.Context, []byte) (string, error) {
			return "hello 42", nil
		},
	}
	runtime, ok := adapter.RuntimeFor(deps, customToolDescriptor{Name: "exact_text", Constraint: mustRegexCustomToolConstraint(t)})
	require.True(t, ok)
	require.IsType(t, vllmRegexConstrainedCustomToolRuntime{}, runtime)

	runtime, ok = adapter.RuntimeFor(deps, customToolDescriptor{Name: "math_exp", Constraint: mustLarkCustomToolConstraint(t)})
	require.True(t, ok)
	require.IsType(t, vllmGrammarConstrainedCustomToolRuntime{}, runtime)

	_, ok = adapter.RuntimeFor(deps, customToolDescriptor{Name: "plain_text"})
	require.False(t, ok)
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

func mustApplyPatchCustomToolConstraint(t *testing.T) *customToolConstraint {
	t.Helper()

	const applyPatchLark = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?

hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?

filename: /(.+)/
add_line: "+" /(.*)/ LF -> line

change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF

%import common.LF`

	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "apply_patch",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": applyPatchLark,
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	return constraint
}
