package httpapi

import (
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

	input, err := parseLocalConstrainedCustomToolRuntimeOutput(
		"```json\n{\"input\":\"hello 42\"}\n```",
		customToolDescriptor{Name: "exact_text", Constraint: constraint},
	)
	require.NoError(t, err)
	require.Equal(t, "hello 42", input)
}
