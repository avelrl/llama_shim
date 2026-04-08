package httpapi

import "testing"

import "github.com/stretchr/testify/require"

func TestCompileCustomToolConstraintRegex(t *testing.T) {
	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "exact_text",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "regex",
			"definition": `hello [0-9]+`,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("hello 42"))
	require.Error(t, constraint.Validate("hello world"))
}

func TestCompileCustomToolConstraintSupportedLark(t *testing.T) {
	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "math_exp",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: expr\nexpr: term (SP ADD SP term)* -> add\n| term\nterm: INT\nSP: \" \"\nADD: \"+\"\n%import common.INT",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("4 + 4"))
	require.NoError(t, constraint.Validate("4 + 4 + 4"))
	require.Error(t, constraint.Validate("4+4"))
}

func TestCompileCustomToolConstraintRejectsRecursiveLark(t *testing.T) {
	_, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "math_exp",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: expr\nexpr: expr ADD INT | INT\nADD: \"+\"\n%import common.INT",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "recursive lark rule")
}
