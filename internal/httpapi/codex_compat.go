package httpapi

import (
	"encoding/json"
	"strings"

	"llama_shim/internal/domain"
)

const (
	codexCLIRequestMarker  = "You are a coding agent running in the Codex CLI"
	codexCompatibilityHint = "Codex compatibility rules for this environment: use exec_command in the sandbox by default for normal workspace reads, writes, and local commands. Only request sandbox_permissions=require_escalated for real sandbox limits such as network access, GUI apps, destructive actions, or writes outside the workspace. For file edits, prefer apply_patch for existing files and use exec_command mainly for reads, builds, tests, and other shell tasks. Avoid rewriting an existing file with heredocs, `cat > file`, `echo ... | base64 -d`, or similar whole-file shell writes when a targeted patch is possible. When using exec_command, prefer structured arguments such as workdir instead of `cd ... &&`. For test or lint commands, set a generous yield_time_ms so the command can finish without extra polling. After the final tool result, always send a brief assistant message to the user and do not end the turn with only reasoning or plan updates."
)

func shouldApplyCodexCompatibility(rawFields map[string]json.RawMessage, tools []map[string]any) bool {
	return isCodexCLIRequest(rawFields) && hasFunctionToolNamed(tools, "exec_command")
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

func normalizeUpstreamResponseBody(raw []byte, plan customToolTransportPlan, codexCompat bool) ([]byte, error) {
	body, err := remapCustomToolResponseBody(raw, plan)
	if err != nil {
		return nil, err
	}
	if !codexCompat {
		return body, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !normalizeCodexResponseObject(payload) {
		return body, nil
	}
	return json.Marshal(payload)
}

func normalizeCodexCompletedEventPayload(payload map[string]any) bool {
	responsePayload, ok := payload["response"].(map[string]any)
	if !ok {
		return false
	}
	return normalizeCodexResponseObject(responsePayload)
}

func normalizeCodexResponseObject(response map[string]any) bool {
	output, ok := response["output"].([]any)
	if !ok || len(output) == 0 {
		return false
	}

	changed := false
	for _, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if sanitizeExecCommandToolCallItem(item) {
			changed = true
		}
	}

	hasAssistantMessage := false
	for _, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if assistantMessageText(item) != "" {
			hasAssistantMessage = true
			break
		}
	}

	filtered := output[:0]
	if hasAssistantMessage {
		filtered = append(filtered, output...)
	} else {
		for _, entry := range output {
			item, ok := entry.(map[string]any)
			if ok && isRedundantCompletedUpdatePlanCall(item) {
				changed = true
				continue
			}
			filtered = append(filtered, entry)
		}
	}

	response["output"] = filtered

	assistantText := ""
	hasToolCalls := false
	for _, entry := range filtered {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if assistantText == "" {
			assistantText = assistantMessageText(item)
		}
		if isToolCallItem(item) {
			hasToolCalls = true
		}
	}

	if strings.TrimSpace(asString(response["output_text"])) == "" {
		switch {
		case assistantText != "":
			response["output_text"] = assistantText
			changed = true
		case !hasToolCalls:
			if summaryText := reasoningSummaryText(filtered); summaryText != "" {
				response["output_text"] = summaryText
				response["output"] = append(filtered, syntheticAssistantMessage(summaryText))
				changed = true
			}
		}
	}

	return changed
}

func sanitizeExecCommandToolCallItem(item map[string]any) bool {
	if strings.TrimSpace(asString(item["type"])) != "function_call" {
		return false
	}
	if strings.TrimSpace(asString(item["name"])) != "exec_command" {
		return false
	}

	rawArguments := strings.TrimSpace(asString(item["arguments"]))
	if rawArguments == "" {
		return false
	}

	var arguments map[string]any
	if err := json.Unmarshal([]byte(rawArguments), &arguments); err != nil {
		return false
	}
	changed := normalizeExecCommandArguments(arguments)
	if !shouldRelaxExecCommandEscalation(arguments) {
		if !changed {
			return false
		}
	} else {
		delete(arguments, "sandbox_permissions")
		delete(arguments, "justification")
		delete(arguments, "prefix_rule")
		changed = true
	}

	normalized, err := json.Marshal(arguments)
	if err != nil {
		return false
	}
	item["arguments"] = string(normalized)
	return changed
}

func normalizeExecCommandArguments(arguments map[string]any) bool {
	changed := false
	cmd := strings.TrimSpace(asString(arguments["cmd"]))
	if cmd == "" {
		return false
	}

	if workdir := strings.TrimSpace(asString(arguments["workdir"])); workdir == "" {
		if parsedWorkdir, rest, ok := splitLeadingWorkdir(cmd); ok {
			arguments["workdir"] = parsedWorkdir
			cmd = rest
			changed = true
		}
	}

	normalizedCmd := stripTrailingShellRedirection(cmd)
	if normalizedCmd != cmd {
		cmd = normalizedCmd
		changed = true
	}

	if arguments["cmd"] != cmd {
		arguments["cmd"] = cmd
		changed = true
	}

	if shouldSetLongerExecYield(cmd) {
		if _, ok := arguments["yield_time_ms"]; !ok {
			arguments["yield_time_ms"] = 30000
			changed = true
		}
		if _, ok := arguments["max_output_tokens"]; !ok {
			arguments["max_output_tokens"] = 6000
			changed = true
		}
	}

	return changed
}

func shouldRelaxExecCommandEscalation(arguments map[string]any) bool {
	if !strings.EqualFold(strings.TrimSpace(asString(arguments["sandbox_permissions"])), "require_escalated") {
		return false
	}
	return !commandLikelyNeedsEscalation(strings.ToLower(strings.TrimSpace(asString(arguments["cmd"]))))
}

func splitLeadingWorkdir(cmd string) (string, string, bool) {
	trimmed := strings.TrimSpace(cmd)
	if !strings.HasPrefix(trimmed, "cd ") {
		return "", "", false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "cd "))
	sep := strings.Index(rest, "&&")
	if sep <= 0 {
		return "", "", false
	}

	workdir := strings.TrimSpace(rest[:sep])
	command := strings.TrimSpace(rest[sep+2:])
	if workdir == "" || command == "" {
		return "", "", false
	}
	if strings.ContainsAny(workdir, "|;><$()`") {
		return "", "", false
	}
	if len(workdir) >= 2 {
		if (workdir[0] == '\'' && workdir[len(workdir)-1] == '\'') || (workdir[0] == '"' && workdir[len(workdir)-1] == '"') {
			workdir = workdir[1 : len(workdir)-1]
		}
	}
	if strings.TrimSpace(workdir) == "" {
		return "", "", false
	}
	return workdir, command, true
}

func stripTrailingShellRedirection(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	for _, suffix := range []string{"2>&1", "1>/dev/null", ">/dev/null"} {
		if strings.HasSuffix(trimmed, suffix) {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
		}
	}
	return trimmed
}

func shouldSetLongerExecYield(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	switch {
	case strings.HasPrefix(lower, "go test"):
		return true
	case strings.HasPrefix(lower, "go vet"):
		return true
	case strings.HasPrefix(lower, "go list"):
		return true
	case strings.HasPrefix(lower, "pytest"):
		return true
	case strings.HasPrefix(lower, "cargo test"):
		return true
	case strings.HasPrefix(lower, "npm test"):
		return true
	case strings.HasPrefix(lower, "npm run test"):
		return true
	case strings.HasPrefix(lower, "pnpm test"):
		return true
	case strings.HasPrefix(lower, "yarn test"):
		return true
	case strings.HasPrefix(lower, "make test"):
		return true
	case strings.HasPrefix(lower, "make lint"):
		return true
	default:
		return false
	}
}

func commandLikelyNeedsEscalation(cmd string) bool {
	if cmd == "" {
		return false
	}
	if strings.Contains(cmd, "://") {
		return true
	}

	for _, marker := range []string{
		"sudo ",
		"curl ",
		"wget ",
		"git clone",
		"git pull",
		"git fetch",
		"git push",
		"gh ",
		"npm install",
		"pnpm install",
		"yarn add",
		"go get ",
		"go install ",
		"pip install",
		"brew ",
		"apt ",
		"apt-get ",
		"dnf ",
		"yum ",
		"docker ",
		"podman ",
		"kubectl ",
		"ssh ",
		"scp ",
		"rsync ",
		"open ",
		"xdg-open ",
		"osascript ",
		"rm ",
		" git reset",
		"git reset",
		"git checkout --",
		"chmod ",
		"chown ",
		"mount ",
		"umount ",
		"mkfs ",
		" dd ",
	} {
		if strings.Contains(cmd, marker) {
			return true
		}
	}

	return false
}

func isToolCallItem(item map[string]any) bool {
	switch strings.TrimSpace(asString(item["type"])) {
	case "function_call", "custom_tool_call":
		return true
	default:
		return false
	}
}

func isRedundantCompletedUpdatePlanCall(item map[string]any) bool {
	if strings.TrimSpace(asString(item["type"])) != "function_call" {
		return false
	}
	if strings.TrimSpace(asString(item["name"])) != "update_plan" {
		return false
	}

	var payload struct {
		Plan []struct {
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(asString(item["arguments"])), &payload); err != nil {
		return false
	}
	if len(payload.Plan) == 0 {
		return false
	}
	for _, step := range payload.Plan {
		if !strings.EqualFold(strings.TrimSpace(step.Status), "completed") {
			return false
		}
	}
	return true
}

func reasoningSummaryText(output []any) string {
	for _, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok || strings.TrimSpace(asString(item["type"])) != "reasoning" {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok {
			continue
		}
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(asString(part["type"])) != "reasoning_text" {
				continue
			}
			text := compactReasoningSummary(asString(part["text"]))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func compactReasoningSummary(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}

	lower := strings.ToLower(text)
	for _, marker := range []string{" let me ", " now let me ", " i will now ", " i'll now "} {
		if idx := strings.Index(lower, marker); idx > 0 {
			text = strings.TrimSpace(text[:idx])
			break
		}
	}
	text = strings.TrimSpace(strings.TrimRight(text, "."))
	if text == "" {
		return ""
	}
	return text + "."
}

func assistantMessageText(item map[string]any) string {
	if strings.TrimSpace(asString(item["type"])) != "message" {
		return ""
	}
	if strings.TrimSpace(asString(item["role"])) != "assistant" {
		return ""
	}
	content, ok := item["content"].([]any)
	if !ok {
		return ""
	}
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(part["type"])) != "output_text" {
			continue
		}
		text := strings.TrimSpace(asString(part["text"]))
		if text != "" {
			return text
		}
	}
	return ""
}

func syntheticAssistantMessage(text string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{
				"type": "output_text",
				"text": text,
			},
		},
	}
}
