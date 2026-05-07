package toolmarkup

import "testing"

func TestContainsPseudoToolTextProviderMarkers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
	}{
		{name: "codex_tool_call", text: `<|tool_calls_section_begin|>{"command":"cat README.md"}`},
		{name: "qwen_tool_call", text: `<toolCall::apply_patch>*** Begin Patch</toolCall::apply_patch>`},
		{name: "qwen_chatcmpl_tool", text: `<chatcmpl-tool>{"name":"exec_command","arguments":{"cmd":"cat README.md"}}</chatcmpl-tool>`},
		{name: "qwen_function_chatcmpl_tool", text: `<function.chatcmpl.tool><parameter=arguments>{"command":["ls","-la"]}</parameter></function.chatcmpl.tool>`},
		{name: "qwen_tools_block", text: `<tools>{"call":"cat","arguments":{"file":"README.md"}}</tools>`},
		{name: "qwen_shell_command_block", text: `[shell_command]{"command":"find . -maxdepth 2 -type f"}[/shell_command]`},
		{name: "qwen_xml_function_shell", text: `<function name="shell"><parameter=command>cat README.md</parameter></function>`},
		{name: "qwen_tool_code_exec", text: `<tool_code_exec><parameter=command>cat README.md</parameter></tool_code_exec>`},
		{name: "qwen_tool_code_interpreter", text: `<tool_code_interpreter>code='''print("hello")'''</tool_code_interpreter>`},
		{name: "xml_tool_call", text: `<tool_call><function=shell></function></tool_call>`},
		{name: "deepseek_dsml", text: "<\uFF5C\uFF5CDSML\uFF5C\uFF5Ctool_calls><\uFF5C\uFF5CDSML\uFF5C\uFF5Cinvoke name=\"read\">"},
		{name: "deepseek_read_file_with_attribute", text: `<read_file path=mathutil.go><failing to read>`},
		{name: "deepseek_bash_with_attribute", text: `<bash command="find . -name 'mathutil.go'">./mathutil.go</bash>`},
		{name: "codex_command_message", text: `<command-message>exec_command is running...</command-message>`},
		{name: "codex_command_name", text: `<command-name>/bin/bash -c 'ls -la'</command-name>`},
		{name: "codex_command_output", text: `<command-output>total 0</command-output>`},
		{name: "fenced_cli_command", text: "```json\n{\"agent\":\"cli\",\"command\":[\"bash\",\"-c\",\"cat README.md\"],\"cwd\":\"/tmp/ws\"}\n```"},
		{name: "fenced_apply_patch_command", text: "```json\n{\"command\":[\"apply_patch\",\"*** Begin Patch\\n*** End Patch\"]}\n```"},
		{name: "fenced_yaml_apply_patch_command", text: "```yaml\ncommand: apply_patch\nargs:\n  patch: |\n    *** Begin Patch\n    *** End Patch\n```"},
		{name: "fenced_plain_apply_patch", text: "```\nApplyPatch\n*** Update File: mathutil.py\n@@\n-    return a - b\n+    return a + b\n*** End Patch\n```"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !ContainsPseudoToolText(tc.text) {
				t.Fatalf("expected pseudo-tool text detection")
			}
		})
	}
}

func TestContainsPseudoToolTextAllowsOrdinaryJSON(t *testing.T) {
	t.Parallel()

	cases := []string{
		`{"command":"status","value":"ok"}`,
		"```json\n{\"command\":\"status\",\"value\":\"ok\"}\n```",
		"```bash\ncat README.md\n```",
	}
	for _, text := range cases {
		if ContainsPseudoToolText(text) {
			t.Fatalf("unexpected pseudo-tool detection for %q", text)
		}
	}
}
