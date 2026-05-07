package toolmarkup

import "strings"

// ContainsPseudoToolText reports provider-native or model-invented tool syntax
// that was emitted as assistant text instead of a structured tool call.
func ContainsPseudoToolText(text string) bool {
	return FirstPseudoToolMarker(text) != ""
}

// FirstPseudoToolMarker returns the marker that made text look like a leaked
// pseudo-tool call. The returned value is for diagnostics only.
func FirstPseudoToolMarker(text string) string {
	normalized := normalizeProviderToolText(text)
	lower := strings.ToLower(normalized)
	for _, marker := range pseudoToolTextMarkers() {
		if strings.Contains(lower, marker) {
			return marker
		}
	}
	if marker := firstPseudoToolTagMarker(lower); marker != "" {
		return marker
	}
	if containsFencedJSONPseudoCommand(lower) {
		return "```json command/apply_patch block"
	}
	if containsFencedYAMLPseudoCommand(lower) {
		return "```yaml command/apply_patch block"
	}
	if containsFencedPlainApplyPatch(lower) {
		return "``` applypatch block"
	}
	return ""
}

func normalizeProviderToolText(text string) string {
	return strings.ReplaceAll(text, "\uFF5C", "|")
}

func pseudoToolTextMarkers() []string {
	return []string{
		"<|tool_call",
		"<|tool_calls_section",
		"<|mask_start|",
		"<|mask_end|",
		"<prelude>",
		"</prelude>",
		"[shell_command]",
		"[/shell_command]",
		"<tool call:",
		"[tool call:",
		"<chatcmpl-tool>",
		"</chatcmpl-tool>",
		"<function.chatcmpl.tool>",
		"</function.chatcmpl.tool>",
		"<function_call>",
		"</function_call>",
		"<function_call_output",
		"<function name=",
		"</function>",
		"<tools>",
		"</tools>",
		"<antthinking>",
		"</antthinking>",
		"<toolcall::",
		"</toolcall::",
		"<apply_patch>",
		"</apply_patch>",
		"<command>",
		"</command>",
		"<tool_call",
		"</tool_call>",
		"<tool_code_call>",
		"</tool_code_call>",
		"<tool_code_exec>",
		"</tool_code_exec>",
		"<tool_code_interpreter>",
		"</tool_code_interpreter>",
		"<tool_code>",
		"<invoke name=",
		"<read_file>",
		"</read_file>",
		"<patch>",
		"</patch>",
		"<bash>",
		"</bash>",
		"<||dsml||tool_calls",
		"</||dsml||tool_calls",
		"<||dsml||invoke",
		"</||dsml||invoke",
		"<||dsml||parameter",
		"</||dsml||parameter",
	}
}

func firstPseudoToolTagMarker(text string) string {
	for _, name := range pseudoToolTagNames() {
		if hasPseudoToolTag(text, name) {
			return "<" + name
		}
	}
	return ""
}

func pseudoToolTagNames() []string {
	return []string{
		"read_file",
		"bash",
		"command-message",
		"command-name",
		"command-output",
		"command-arg",
	}
}

func hasPseudoToolTag(text string, name string) bool {
	for _, prefix := range []string{"<" + name, "</" + name} {
		offset := 0
		for {
			idx := strings.Index(text[offset:], prefix)
			if idx < 0 {
				break
			}
			end := offset + idx + len(prefix)
			if end >= len(text) || isPseudoToolTagBoundary(text[end]) {
				return true
			}
			offset = end
		}
	}
	return false
}

func isPseudoToolTagBoundary(ch byte) bool {
	switch ch {
	case ' ', '\n', '\r', '\t', '>', '/', '=':
		return true
	default:
		return false
	}
}

func containsFencedJSONPseudoCommand(text string) bool {
	return containsFencedPseudoCommand(text, "```json", isPseudoCommandJSONBlock)
}

func containsFencedYAMLPseudoCommand(text string) bool {
	return containsFencedPseudoCommand(text, "```yaml", isPseudoCommandYAMLBlock)
}

func containsFencedPlainApplyPatch(text string) bool {
	return containsFencedPseudoCommand(text, "```", isPlainApplyPatchBlock)
}

func containsFencedPseudoCommand(text string, fence string, match func(string) bool) bool {
	offset := 0
	for {
		startRelative := strings.Index(text[offset:], fence)
		if startRelative < 0 {
			return false
		}
		blockStart := offset + startRelative + len(fence)
		blockEndRelative := strings.Index(text[blockStart:], "```")
		block := text[blockStart:]
		if blockEndRelative >= 0 {
			block = text[blockStart : blockStart+blockEndRelative]
		}
		if match(block) {
			return true
		}
		if blockEndRelative < 0 {
			return false
		}
		offset = blockStart + blockEndRelative + len("```")
	}
}

func isPseudoCommandJSONBlock(block string) bool {
	if !strings.Contains(block, `"command"`) {
		return false
	}
	if strings.Contains(block, `"agent"`) && strings.Contains(block, `"cli"`) {
		return true
	}
	if strings.Contains(block, `"apply_patch"`) {
		return true
	}
	return strings.Contains(block, `"cwd"`) &&
		(strings.Contains(block, `"bash"`) || strings.Contains(block, `"zsh"`))
}

func isPseudoCommandYAMLBlock(block string) bool {
	trimmed := strings.TrimSpace(block)
	if !strings.Contains(trimmed, "command:") {
		return false
	}
	return strings.Contains(trimmed, "apply_patch") ||
		strings.Contains(trimmed, "exec_command") ||
		strings.Contains(trimmed, "shell")
}

func isPlainApplyPatchBlock(block string) bool {
	trimmed := strings.TrimSpace(block)
	return strings.HasPrefix(trimmed, "applypatch\n") ||
		strings.HasPrefix(trimmed, "apply_patch\n") ||
		strings.Contains(trimmed, "\n*** update file:") ||
		strings.Contains(trimmed, "\n*** begin patch")
}
