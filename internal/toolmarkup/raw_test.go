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
		{name: "xml_tool_call", text: `<tool_call><function=shell></function></tool_call>`},
		{name: "deepseek_dsml", text: "<\uFF5C\uFF5CDSML\uFF5C\uFF5Ctool_calls><\uFF5C\uFF5CDSML\uFF5C\uFF5Cinvoke name=\"read\">"},
		{name: "fenced_cli_command", text: "```json\n{\"agent\":\"cli\",\"command\":[\"bash\",\"-c\",\"cat README.md\"],\"cwd\":\"/tmp/ws\"}\n```"},
		{name: "fenced_apply_patch_command", text: "```json\n{\"command\":[\"apply_patch\",\"*** Begin Patch\\n*** End Patch\"]}\n```"},
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
