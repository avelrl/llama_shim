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

func TestCompileCustomToolConstraintSupportsCommonGroupedImports(t *testing.T) {
	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "grouped_common",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: CNAME WS_INLINE SIGNED_NUMBER NEWLINE\n%import common (CNAME, WS_INLINE, SIGNED_NUMBER, NEWLINE)",
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("value -1.5\n"))
	require.NoError(t, constraint.Validate("value 2e3\n"))
	require.Error(t, constraint.Validate("value\n"))
	require.Contains(t, constraint.VLLMGrammar, `SIGNED_NUMBER ::= [-+]?(?:`)
	require.Contains(t, constraint.VLLMGrammar, `NEWLINE ::= (?:\r?\n)+`)
}

func TestCompileCustomToolConstraintSupportsCommonImportAlias(t *testing.T) {
	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "aliased_common",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: ALIASED_INT\n%import common.INT -> ALIASED_INT",
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("42"))
	require.Error(t, constraint.Validate("x"))
	require.Equal(t, strings.Join([]string{
		"root ::= ALIASED_INT",
		"ALIASED_INT ::= [0-9]+",
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

func TestCompileCustomToolConstraintSupportsCodexJSReplLark(t *testing.T) {
	const jsReplLark = `
start: pragma_source | plain_source

pragma_source: PRAGMA_LINE NEWLINE js_source
plain_source: PLAIN_JS_SOURCE

js_source: JS_SOURCE

PRAGMA_LINE: /[ \t]*\/\/ codex-js-repl:[^\r\n]*/
NEWLINE: /\r?\n/
PLAIN_JS_SOURCE: /(?:\s*)(?:[^\s{\"` + "`" + `]|` + "`" + `[^` + "`" + `]|` + "``" + `[^` + "`" + `])[\s\S]*/
JS_SOURCE: /(?:\s*)(?:[^\s{\"` + "`" + `]|` + "`" + `[^` + "`" + `]|` + "``" + `[^` + "`" + `])[\s\S]*/
`

	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "js_repl",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": jsReplLark,
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("const answer = 42;"))
	require.NoError(t, constraint.Validate("// codex-js-repl: timeout_ms=15000\nawait Promise.resolve(42);"))
	require.Error(t, constraint.Validate(`{"code":"const answer = 42;"}`))
}

func TestCompileCustomToolConstraintSupportsCodexCodeModeLark(t *testing.T) {
	const codeModeLark = `
start: pragma_source | plain_source
pragma_source: PRAGMA_LINE NEWLINE SOURCE
plain_source: SOURCE

PRAGMA_LINE: /[ \t]*\/\/ @exec:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`

	constraint, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "exec",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": codeModeLark,
		},
	}, ServiceLimits{})
	require.NoError(t, err)
	require.NotNil(t, constraint)
	require.NoError(t, constraint.Validate("echo hello"))
	require.NoError(t, constraint.Validate("// @exec: timeout_ms=15000\necho hello"))
	require.Error(t, constraint.Validate(""))
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

func TestCompileCustomToolConstraintRejectsNonCommonImport(t *testing.T) {
	_, err := compileCustomToolConstraint(map[string]any{
		"type": "custom",
		"name": "bad_import",
		"format": map[string]any{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": "start: INT\n%import not_common.INT",
		},
	}, ServiceLimits{})
	require.Error(t, err)
	require.Contains(t, err.Error(), `%import "not_common.INT" is not supported`)
}

func TestCompileCustomToolConstraintRejectsUnsupportedLarkRegexFeatures(t *testing.T) {
	for name, definition := range map[string]string{
		"lookahead":          `start: /a(?=b)/`,
		"lookbehind":         `start: /(?<=a)b/`,
		"regex_lazy":         `start: /a+?/`,
		"grammar_lazy":       `start: "a"+?`,
		"declare":            "start: VALUE\n%declare VALUE",
		"unsupported_common": "start: VALUE\n%import common.VALUE",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compileCustomToolConstraint(map[string]any{
				"type": "custom",
				"name": "bad_lark",
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": definition,
				},
			}, ServiceLimits{})
			require.Error(t, err)
		})
	}
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
