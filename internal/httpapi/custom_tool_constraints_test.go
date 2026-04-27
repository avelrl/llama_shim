package httpapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompileCustomToolConstraintRegex(t *testing.T) {
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
	}, ServiceLimits{})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("4 + 4"))
	require.NoError(t, constraint.Validate("4 + 4 + 4"))
	require.Error(t, constraint.Validate("4+4"))
	require.Equal(t, strings.Join([]string{
		"root ::= expr",
		"INT ::= [0-9]+",
		"term ::= INT",
		"SP ::= \" \"",
		"ADD ::= \"+\"",
		"expr ::= term (SP ADD SP term)* | term",
	}, "\n"), constraint.VLLMGrammar)
}

func TestCompileCustomToolConstraintSupportsCommonLFImport(t *testing.T) {
	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "line_pair",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: \"a\" LF \"b\"\n%import common.LF",
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("a\nb"))
	require.Error(t, constraint.Validate("ab"))
	require.Equal(t, strings.Join([]string{
		"root ::= \"a\" LF \"b\"",
		`LF ::= \n`,
	}, "\n"), constraint.VLLMGrammar)
}

func TestCompileCustomToolConstraintSupportsCodexApplyPatchLark(t *testing.T) {
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
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"))
	require.Error(t, constraint.Validate("*** Begin Patch\n*** Add File: hello.txt\n+hello\n"))
	require.Contains(t, constraint.VLLMGrammar, `LF ::= \n`)
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
	}, ServiceLimits{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "recursive lark rule")
}

func TestCompileCustomToolConstraintRejectsOversizedDefinition(t *testing.T) {
	limits := normalizeServiceLimits(ServiceLimits{})

	_, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "exact_text",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "regex",
			"definition": strings.Repeat("a", int(limits.CustomToolGrammarDefinitionBytes)+1),
		},
	}, limits)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the shim-local constrained limit")
}

func TestCompileCustomToolConstraintRejectsOversizedCompiledPattern(t *testing.T) {
	_, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "exact_text",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "regex",
			"definition": `hello [0-9]+`,
		},
	}, ServiceLimits{
		CustomToolGrammarDefinitionBytes: 1 << 20,
		CustomToolCompiledPatternBytes:   8,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "compiled regex grammar exceeds the shim-local constrained limit")
}
