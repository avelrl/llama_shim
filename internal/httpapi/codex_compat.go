package httpapi

import (
	"encoding/json"
	"strings"

	"llama_shim/internal/domain"
)

const (
	codexCLIRequestMarker  = "You are a coding agent running in the Codex CLI"
	codexCompatibilityHint = "Codex compatibility rules for this environment: use exec_command in the sandbox by default for normal workspace reads, writes, and local commands. Only request sandbox_permissions=require_escalated for real sandbox limits such as network access, GUI apps, destructive actions, or writes outside the workspace; if approval policy is never, do not request escalation. For file edits, use the apply_patch tool directly for existing files and use exec_command mainly for reads, builds, tests, and other shell tasks. Never invoke apply_patch through exec_command. The exec_command.cmd field must be a single shell string, not an argv array. Avoid rewriting an existing file with heredocs, `cat > file`, `echo ... | base64 -d`, or similar whole-file shell writes when a targeted patch is possible. When using exec_command, prefer structured arguments such as workdir instead of `cd ... &&`. For Go tests that fail because the default Go build cache is outside writable roots, rerun with GOCACHE and GOTMPDIR under /tmp, then continue diagnosing the actual test failure. For test or lint commands, set a generous yield_time_ms so the command can finish without extra polling. After the final tool result, always send a brief assistant message to the user and do not end the turn with only reasoning or plan updates."
)

const (
	codexExecCommandToolHint = "Codex rule: cmd must be a single shell string, not an argv array. Do not use exec_command to run apply_patch; use the apply_patch tool directly for file edits."
	codexApplyPatchToolHint  = "Codex rule: use this tool directly for existing-file edits instead of invoking apply_patch through exec_command or shell wrappers."
)

func shouldApplyCodexCompatibility(rawFields map[string]json.RawMessage, tools []map[string]any, enabled bool) bool {
	if !enabled {
		return false
	}
	return isCodexCLIRequestWithTools(rawFields, tools) && hasFunctionToolNamed(tools, "exec_command")
}

func appendCodexCompatibilityInstructions(instructions string) string {
	if strings.Contains(instructions, codexCompatibilityHint) {
		return instructions
	}
	if strings.TrimSpace(instructions) == "" {
		return codexCompatibilityHint
	}
	return strings.TrimRight(instructions, "\n") + "\n\n" + codexCompatibilityHint
}

func augmentCodexToolDescriptions(tools []map[string]any) []map[string]any {
	if len(tools) == 0 {
		return tools
	}

	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			out = append(out, tool)
			continue
		}
		rewritten := mapsClone(tool)
		if !strings.EqualFold(strings.TrimSpace(asString(rewritten["type"])), "function") {
			out = append(out, rewritten)
			continue
		}

		switch strings.TrimSpace(asString(rewritten["name"])) {
		case "exec_command":
			rewritten["description"] = appendToolDescriptionHint(asString(rewritten["description"]), codexExecCommandToolHint)
		case "apply_patch":
			rewritten["description"] = appendToolDescriptionHint(asString(rewritten["description"]), codexApplyPatchToolHint)
		}
		out = append(out, rewritten)
	}
	return out
}

func appendToolDescriptionHint(description, hint string) string {
	description = strings.TrimSpace(description)
	if hint == "" || strings.Contains(description, hint) {
		return description
	}
	if description == "" {
		return hint
	}
	return description + " " + hint
}

func mapsClone(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func injectCodexCompatibilityContext(items []domain.Item, currentInputLen int) []domain.Item {
	if currentInputLen < 0 || currentInputLen > len(items) {
		currentInputLen = 0
	}
	insertAt := len(items) - currentInputLen
	hintItem := domain.NewInputTextMessage("system", codexCompatibilityHint)

	out := make([]domain.Item, 0, len(items)+1)
	out = append(out, items[:insertAt]...)
	out = append(out, hintItem)
	out = append(out, items[insertAt:]...)
	return out
}

func decodeToolList(rawFields map[string]json.RawMessage) []map[string]any {
	rawTools, ok := rawFields["tools"]
	if !ok {
		return nil
	}
	var tools []map[string]any
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return nil
	}
	return tools
}

func normalizeUpstreamResponseBody(raw []byte, plan customToolTransportPlan) ([]byte, error) {
	return remapCustomToolResponseBody(raw, plan)
}
