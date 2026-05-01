package codexeval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"llama_shim/internal/toolmarkup"
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
	for _, marker := range contextLeakMarkers() {
		if strings.Contains(string(rawOutput), marker) || strings.Contains(finalText, marker) {
			result.addFailure("context_leak", fmt.Sprintf("output contains internal context marker %q", marker))
			break
		}
	}
	if marker := firstRawToolMarkupMarker(string(rawOutput), finalText); marker != "" {
		result.addFailure("raw_tool_markup", fmt.Sprintf("output contains provider-native tool marker %q", marker))
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
	for _, expected := range manifest.Expected.FinalTextContainsFold {
		if !containsFold(finalText, expected) {
			result.addFailure("final_text", fmt.Sprintf("final text %q does not contain %q (case-insensitive)", finalText, expected))
		}
	}
	for _, expected := range manifest.Expected.CodexEvents {
		if !hasCodexEvent(events, expected) {
			result.addFailure("codex_event", fmt.Sprintf("missing Codex event %q", expected))
		}
	}
	for _, forbidden := range manifest.Expected.ForbiddenCodexEvents {
		if hasCodexEvent(events, forbidden) {
			result.addFailure("forbidden_codex_event", fmt.Sprintf("unexpected Codex event %q", forbidden))
		}
	}
	if manifest.Expected.MinCommandExecutions > 0 && stats.CommandStarted < manifest.Expected.MinCommandExecutions {
		result.addFailure("command_count", fmt.Sprintf("command executions %d below min %d", stats.CommandStarted, manifest.Expected.MinCommandExecutions))
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
	if len(manifest.Expected.RequestShapes) > 0 {
		checkRequestShapeExpectations(&result, filepath.Dir(outputFile), manifest.Expected.RequestShapes)
	}
	result.Passed = len(result.Failures) == 0
	return result, finalText, nil
}

func contextLeakMarkers() []string {
	return []string{
		"<environment_context>",
		"</environment_context>",
		"<permissions instructions>",
		"</permissions instructions>",
	}
}

func firstRawToolMarkupMarker(values ...string) string {
	for _, value := range values {
		if marker := toolmarkup.FirstPseudoToolMarker(value); marker != "" {
			return marker
		}
	}
	return ""
}

func containsFold(value, expected string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(expected))
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
	if expected.EqualsTrimSpace != "" && strings.TrimSpace(content) != strings.TrimSpace(expected.EqualsTrimSpace) {
		result.addFailure("file_equals", fmt.Sprintf("%s trimmed content mismatch", expected.Path))
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

func checkRequestShapeExpectations(result *CheckResult, attemptDir string, expectations []RequestShapeExpectation) {
	artifactPath := filepath.Join(attemptDir, requestShapeArtifactName)
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		result.addFailure("request_shape", fmt.Sprintf("cannot read %s: %v", requestShapeArtifactName, err))
		return
	}
	var artifact requestShapeArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		result.addFailure("request_shape", fmt.Sprintf("cannot parse %s: %v", requestShapeArtifactName, err))
		return
	}
	for _, expectation := range expectations {
		checkRequestShapeExpectation(result, artifact.Requests, expectation)
	}
}

func checkRequestShapeExpectation(result *CheckResult, requests []CapturedRequestShape, expectation RequestShapeExpectation) {
	minCount := expectation.MinCount
	if minCount == 0 {
		minCount = 1
	}
	matches := make([]CapturedRequestShape, 0)
	for _, request := range requests {
		if requestShapeMatchesFilter(request, expectation) {
			matches = append(matches, request)
		}
	}
	label := expectation.Name
	if label == "" {
		label = requestShapeExpectationLabel(expectation)
	}
	if len(matches) < minCount {
		result.addFailure("request_shape", fmt.Sprintf("%s matched %d request(s), want at least %d", label, len(matches), minCount))
		return
	}
	var firstMismatch string
	for _, request := range matches {
		if mismatch := requestShapeDetailMismatch(request, expectation); mismatch == "" {
			return
		} else if firstMismatch == "" {
			firstMismatch = mismatch
		}
	}
	result.addFailure("request_shape", fmt.Sprintf("%s matched transport/path filters but failed detail checks: %s", label, firstMismatch))
}

func requestShapeMatchesFilter(request CapturedRequestShape, expectation RequestShapeExpectation) bool {
	if expectation.Transport != "" && request.Transport != expectation.Transport {
		return false
	}
	if expectation.Method != "" && !strings.EqualFold(request.Method, expectation.Method) {
		return false
	}
	if expectation.Path != "" && request.Path != expectation.Path {
		return false
	}
	if expectation.Type != "" && request.Type != expectation.Type {
		return false
	}
	if expectation.Model != "" && request.Model != expectation.Model {
		return false
	}
	return true
}

func requestShapeDetailMismatch(request CapturedRequestShape, expectation RequestShapeExpectation) string {
	headers := lowerStringSetFromMap(request.Headers)
	bodyFields := stringSet(request.BodyFields)
	toolNames := stringSet(request.ToolNames)
	toolTypes := stringSet(request.ToolTypes)
	inputItemTypes := stringSet(request.InputItemTypes)
	for _, header := range expectation.RequiredHeaders {
		if !headers[strings.ToLower(header)] {
			return fmt.Sprintf("missing header %q", header)
		}
	}
	for _, header := range expectation.ForbiddenHeaders {
		if headers[strings.ToLower(header)] {
			return fmt.Sprintf("unexpected header %q", header)
		}
	}
	for _, header := range expectation.RedactedHeaders {
		value, ok := request.Headers[strings.ToLower(header)]
		if !ok {
			return fmt.Sprintf("missing redacted header %q", header)
		}
		if value != "[REDACTED]" {
			return fmt.Sprintf("header %q was not redacted", header)
		}
	}
	for _, field := range expectation.RequiredBodyFields {
		if !bodyFields[field] {
			return fmt.Sprintf("missing body field %q", field)
		}
	}
	for _, field := range expectation.ForbiddenBodyFields {
		if bodyFields[field] {
			return fmt.Sprintf("unexpected body field %q", field)
		}
	}
	if expectation.MinTools > 0 && len(request.ToolNames) < expectation.MinTools {
		return fmt.Sprintf("tool count %d below min %d", len(request.ToolNames), expectation.MinTools)
	}
	for _, name := range expectation.RequiredToolNames {
		if !toolNames[name] {
			return fmt.Sprintf("missing tool name %q", name)
		}
	}
	for _, name := range expectation.ForbiddenToolNames {
		if toolNames[name] {
			return fmt.Sprintf("unexpected tool name %q", name)
		}
	}
	for _, typ := range expectation.RequiredToolTypes {
		if !toolTypes[typ] {
			return fmt.Sprintf("missing tool type %q", typ)
		}
	}
	for _, typ := range expectation.RequiredInputItemTypes {
		if !inputItemTypes[typ] {
			return fmt.Sprintf("missing input item type %q", typ)
		}
	}
	if expectation.Stream != nil && !boolPointerEquals(request.Stream, *expectation.Stream) {
		return fmt.Sprintf("stream = %v, want %v", pointerBoolString(request.Stream), *expectation.Stream)
	}
	if expectation.Store != nil && !boolPointerEquals(request.Store, *expectation.Store) {
		return fmt.Sprintf("store = %v, want %v", pointerBoolString(request.Store), *expectation.Store)
	}
	if expectation.Generate != nil && !boolPointerEquals(request.Generate, *expectation.Generate) {
		return fmt.Sprintf("generate = %v, want %v", pointerBoolString(request.Generate), *expectation.Generate)
	}
	if expectation.ToolChoicePresent != nil && request.ToolChoicePresent != *expectation.ToolChoicePresent {
		return fmt.Sprintf("tool_choice_present = %v, want %v", request.ToolChoicePresent, *expectation.ToolChoicePresent)
	}
	if expectation.PreviousResponseIDPresent != nil && request.PreviousResponseIDPresent != *expectation.PreviousResponseIDPresent {
		return fmt.Sprintf("previous_response_id_present = %v, want %v", request.PreviousResponseIDPresent, *expectation.PreviousResponseIDPresent)
	}
	if request.BodyTruncated {
		return "captured body was truncated"
	}
	if request.BodyInvalidJSON != "" && len(expectation.RequiredBodyFields) > 0 {
		return "captured body was not valid JSON: " + request.BodyInvalidJSON
	}
	return ""
}

func requestShapeExpectationLabel(expectation RequestShapeExpectation) string {
	parts := []string{"request shape"}
	if expectation.Transport != "" {
		parts = append(parts, expectation.Transport)
	}
	if expectation.Method != "" {
		parts = append(parts, expectation.Method)
	}
	if expectation.Path != "" {
		parts = append(parts, expectation.Path)
	}
	if expectation.Type != "" {
		parts = append(parts, expectation.Type)
	}
	return strings.Join(parts, " ")
}

func lowerStringSetFromMap(values map[string]string) map[string]bool {
	result := make(map[string]bool, len(values))
	for key := range values {
		result[strings.ToLower(key)] = true
	}
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func boolPointerEquals(value *bool, expected bool) bool {
	return value != nil && *value == expected
}

func pointerBoolString(value *bool) string {
	if value == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%v", *value)
}
