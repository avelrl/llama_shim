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
	for _, marker := range pseudoToolTextMarkers() {
		if strings.Contains(normalized, marker) {
			return marker
		}
	}
	if containsFencedJSONPseudoCommand(normalized) {
		return "```json command/apply_patch block"
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
		"<tool call:",
		"[Tool call:",
		"<function_call>",
		"</function_call>",
		"<function_call_output",
		"<FUNCTION_CALL_OUTPUT",
		"<antThinking>",
		"</antThinking>",
		"<toolCall::",
		"</toolCall::",
		"<apply_patch>",
		"</apply_patch>",
		"<command>",
		"</command>",
		"<tool_call",
		"</tool_call>",
		"<tool_code_call>",
		"</tool_code_call>",
		"<tool_code>",
		"<invoke name=",
		"<read_file>",
		"</read_file>",
		"<patch>",
		"</patch>",
		"<bash>",
		"</bash>",
		"<||DSML||tool_calls",
		"</||DSML||tool_calls",
		"<||DSML||invoke",
		"</||DSML||invoke",
		"<||DSML||parameter",
		"</||DSML||parameter",
	}
}

func containsFencedJSONPseudoCommand(text string) bool {
	lower := strings.ToLower(text)
	offset := 0
	for {
		startRelative := strings.Index(lower[offset:], "```json")
		if startRelative < 0 {
			return false
		}
		blockStart := offset + startRelative + len("```json")
		blockEndRelative := strings.Index(lower[blockStart:], "```")
		block := lower[blockStart:]
		if blockEndRelative >= 0 {
			block = lower[blockStart : blockStart+blockEndRelative]
		}
		if isPseudoCommandJSONBlock(block) {
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
