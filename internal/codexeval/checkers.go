package codexeval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func runCheckers(ctx context.Context, manifest Manifest, workspace, outputFile string, taskEnv map[string]string) (CheckResult, string, error) {
	events, stats, finalText, err := parseCodexEvents(outputFile)
	if err != nil {
		return CheckResult{}, "", err
	}
	result := CheckResult{
		Passed:       true,
		FinalText:    finalText,
		CommandCount: stats.CommandStarted,
		FileChanges:  stats.FileChanges,
		ToolCalls:    stats.ToolCalls,
	}
	rawOutput, err := os.ReadFile(outputFile)
	if err != nil {
		return CheckResult{}, "", err
	}
	for _, marker := range rawToolMarkupMarkers() {
		if strings.Contains(string(rawOutput), marker) || strings.Contains(finalText, marker) {
			result.addFailure("raw_tool_markup", fmt.Sprintf("output contains provider-native tool marker %q", marker))
			break
		}
	}
	for _, forbidden := range manifest.Expected.ForbiddenOutput {
		if forbidden != "" && strings.Contains(string(rawOutput), forbidden) {
			result.addFailure("raw_tool_markup", fmt.Sprintf("output contains forbidden marker %q", forbidden))
		}
	}
	if manifest.Expected.FinalTextEquals != "" && finalText != manifest.Expected.FinalTextEquals {
		result.addFailure("final_text", fmt.Sprintf("final text %q does not equal %q", finalText, manifest.Expected.FinalTextEquals))
	}
	for _, expected := range manifest.Expected.FinalTextContains {
		if !strings.Contains(finalText, expected) {
			result.addFailure("final_text", fmt.Sprintf("final text %q does not contain %q", finalText, expected))
		}
	}
	for _, expected := range manifest.Expected.CodexEvents {
		if !hasCodexEvent(events, expected) {
			result.addFailure("codex_event", fmt.Sprintf("missing Codex event %q", expected))
		}
	}
	if manifest.Expected.MaxToolCalls > 0 && stats.ToolCalls > manifest.Expected.MaxToolCalls {
		result.addFailure("tool_calls", fmt.Sprintf("tool calls %d exceeds max %d", stats.ToolCalls, manifest.Expected.MaxToolCalls))
	}
	for _, file := range manifest.Expected.Files {
		checkFileExpectation(&result, workspace, file)
	}
	for _, command := range manifest.Expected.Commands {
		checkCommandExpectation(ctx, &result, workspace, command, taskEnv)
	}
	result.Passed = len(result.Failures) == 0
	return result, finalText, nil
}

func rawToolMarkupMarkers() []string {
	return []string{
		"<|tool_call",
		"<|tool_calls_section",
		"<tool_call",
		"</tool_call>",
		"<tool_code>",
		"<invoke name=",
		"<read_file>",
		"</read_file>",
		"<patch>",
		"</patch>",
		"<bash>",
		"</bash>",
	}
}

func (result *CheckResult) addFailure(kind, message string) {
	result.Passed = false
	result.Failures = append(result.Failures, CheckFailure{Kind: kind, Message: message})
}

func checkFileExpectation(result *CheckResult, workspace string, expected FileExpectation) {
	path := filepath.Join(workspace, expected.Path)
	raw, err := os.ReadFile(path)
	if expected.Absent {
		if err == nil {
			result.addFailure("file_absent", fmt.Sprintf("%s exists but should be absent", expected.Path))
		}
		return
	}
	if expected.Exists != nil && !*expected.Exists {
		if err == nil {
			result.addFailure("file_exists", fmt.Sprintf("%s exists but expected exists=false", expected.Path))
		}
		return
	}
	if err != nil {
		if expected.Exists != nil && *expected.Exists {
			result.addFailure("file_exists", fmt.Sprintf("%s does not exist: %v", expected.Path, err))
			return
		}
		result.addFailure("file_read", fmt.Sprintf("%s cannot be read: %v", expected.Path, err))
		return
	}
	content := string(raw)
	if expected.Equals != "" && content != expected.Equals {
		result.addFailure("file_equals", fmt.Sprintf("%s content mismatch", expected.Path))
	}
	if expected.Contains != "" && !strings.Contains(content, expected.Contains) {
		result.addFailure("file_contains", fmt.Sprintf("%s does not contain %q", expected.Path, expected.Contains))
	}
	if expected.Matches != "" {
		re := regexp.MustCompile(expected.Matches)
		if !re.MatchString(content) {
			result.addFailure("file_matches", fmt.Sprintf("%s does not match %q", expected.Path, expected.Matches))
		}
	}
}

func checkCommandExpectation(ctx context.Context, result *CheckResult, workspace string, expected CommandExpectation, taskEnv map[string]string) {
	timeout := 60 * time.Second
	if expected.Timeout != "" {
		parsed, err := time.ParseDuration(expected.Timeout)
		if err != nil {
			result.addFailure("command_setup", fmt.Sprintf("%s invalid timeout: %v", expected.Name, err))
			return
		}
		timeout = parsed
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "/bin/sh", "-c", expected.Command)
	cmd.Dir = workspace
	cmd.Env = os.Environ()
	for key, value := range taskEnv {
		cmd.Env = append(cmd.Env, key+"="+expandTemplate(value, workspace))
	}
	for key, value := range expected.Env {
		cmd.Env = append(cmd.Env, key+"="+expandTemplate(value, workspace))
	}
	raw, err := cmd.CombinedOutput()
	if commandCtx.Err() == context.DeadlineExceeded {
		result.addFailure("command_timeout", fmt.Sprintf("%s timed out after %s", commandName(expected), timeout))
		return
	}
	if err != nil {
		result.addFailure("command_failed", fmt.Sprintf("%s failed: %v\n%s", commandName(expected), err, strings.TrimSpace(string(raw))))
	}
}

func commandName(expected CommandExpectation) string {
	if expected.Name != "" {
		return expected.Name
	}
	return expected.Command
}

func expandTemplate(value, workspace string) string {
	value = strings.ReplaceAll(value, "${workspace}", workspace)
	return value
}
