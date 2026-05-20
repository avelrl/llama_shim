package modelcert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var highSignalLogPattern = regexp.MustCompile(`(?i)"level":"(WARN|ERROR)"|"status":(4|5)[0-9][0-9]|"status_code":(4|5)[0-9][0-9]|"upstream_status":(4|5)[0-9][0-9]|upstream[_ ]timeout|transport[_ ]error|raw[_ -]tool[_ -]call[_ -]markup|raw_tool_markup|failed_raw_tool_markup|invalid tool|tool arguments|failed to satisfy its constraint|apply_patch failed to satisfy|json_schema|schema|unsupported input shape|readiness|catalog|authentication|unauthorized|forbidden|permission_denied|panic|context deadline|empty reply|rate[_ ]limit|quota`)

func WriteTraceArtifacts(dir string, traces []DebugTrace) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	tracesPath := filepath.Join(dir, "traces.json")
	traceList := TraceList{Object: "list", Data: traces}
	if err := writeJSONFile(tracesPath, traceList); err != nil {
		return "", "", err
	}
	summary := BuildTraceSummary(traces)
	summaryPath := filepath.Join(dir, "traces-summary.json")
	if err := writeJSONFile(summaryPath, summary); err != nil {
		return "", "", err
	}
	return tracesPath, summaryPath, nil
}

func BuildTraceSummary(traces []DebugTrace) TraceSummary {
	summary := TraceSummary{
		Object:          "modelcert.trace_summary",
		TraceCount:      len(traces),
		BySurface:       map[string]int{},
		ByFinalStatus:   map[string]int{},
		ByProvider:      map[string]int{},
		ByPublicModel:   map[string]int{},
		ByUpstreamModel: map[string]int{},
		ByBackend:       map[string]int{},
		ByFailureClass:  map[string]int{},
	}
	for _, trace := range traces {
		increment(summary.BySurface, trace.Surface)
		increment(summary.ByFinalStatus, strconv.Itoa(trace.FinalStatus))
		increment(summary.ByProvider, trace.Provider)
		increment(summary.ByPublicModel, trace.PublicModel)
		increment(summary.ByUpstreamModel, trace.UpstreamModel)
		increment(summary.ByBackend, trace.SelectedBackend)
		digest := TraceDigest{
			RequestID:       trace.RequestID,
			ClientRequestID: trace.ClientRequestID,
			Surface:         trace.Surface,
			Status:          trace.FinalStatus,
			DurationMS:      trace.DurationMS,
		}
		if trace.BackendFailure != nil {
			increment(summary.ByFailureClass, trace.BackendFailure.Class)
			digest.FailureClass = trace.BackendFailure.Class
		}
		if trace.FinalStatus >= 400 {
			summary.FailedRequests = append(summary.FailedRequests, digest)
		}
		if trace.DurationMS >= 30000 {
			summary.SlowRequests = append(summary.SlowRequests, digest)
		}
	}
	slices.SortFunc(summary.FailedRequests, func(left, right TraceDigest) int {
		return strings.Compare(left.RequestID, right.RequestID)
	})
	slices.SortFunc(summary.SlowRequests, func(left, right TraceDigest) int {
		return strings.Compare(left.RequestID, right.RequestID)
	})
	return summary
}

func WriteLogDiagnostics(logPath string, outPath string) ([]string, error) {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.WriteFile(outPath, []byte("# Shim Log Diagnostics\n\nShim log was not found.\n"), 0o644); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	matches := make([]string, 0)
	for idx, line := range lines {
		if isNoisySuccessfulLogLine(line) {
			continue
		}
		if highSignalLogPattern.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%d:%s", idx+1, Redact(line)))
		}
	}
	var b strings.Builder
	b.WriteString("# Shim Log Diagnostics\n\n")
	b.WriteString("- Source: `" + filepath.ToSlash(logPath) + "`\n")
	b.WriteString(fmt.Sprintf("- Bytes: `%d`\n\n", len(raw)))
	b.WriteString("## High-Signal Matches\n\n")
	if len(matches) == 0 {
		b.WriteString("No high-signal diagnostics matched.\n")
	} else {
		b.WriteString("```text\n")
		limit := min(len(matches), 120)
		for _, match := range matches[:limit] {
			b.WriteString(match)
			b.WriteByte('\n')
		}
		if len(matches) > limit {
			b.WriteString(fmt.Sprintf("... %d additional matches omitted ...\n", len(matches)-limit))
		}
		b.WriteString("```\n")
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return matches, nil
}

func BuildFixCandidates(model ModelSummary, logMatches []string, traces []DebugTrace) []FixCandidate {
	if firstFailingStage(model) == "none" && !hasFailedOrSlowTrace(traces) {
		return nil
	}
	signals := detectSignals(logMatches, traces)
	if len(signals) == 0 {
		return nil
	}
	owner := inferOwner(signals)
	category := inferCategory(signals)
	description := "Review model certification evidence before changing shim behavior."
	if owner == "shim" {
		description = "Evidence suggests a possible shim-owned request shaping, parsing, retry, or compatibility repair."
	}
	return []FixCandidate{{
		Model:       model.Model,
		Stage:       firstFailingStage(model),
		Owner:       owner,
		Category:    category,
		Confidence:  "medium",
		Signals:     signals,
		Artifact:    model.FailureNotesPath,
		Description: description,
	}}
}

func WriteFailureNotes(model ModelSummary, logMatches []string, traces []DebugTrace, outPath string) error {
	signals := detectSignals(logMatches, traces)
	owner := inferOwner(signals)
	if owner == "" {
		owner = "unknown"
	}
	var b strings.Builder
	b.WriteString("# Model Certification Failure Notes\n\n")
	b.WriteString("- Model: `" + model.Model + "`\n")
	b.WriteString("- Verdict: `" + model.Verdict + "`\n")
	b.WriteString("- First failing stage: `" + firstFailingStage(model) + "`\n")
	b.WriteString("- Possible owner: `" + owner + "`\n")
	b.WriteString("- Artifact dir: `" + filepath.ToSlash(model.ArtifactDir) + "`\n\n")
	b.WriteString("## Stage Status\n\n")
	b.WriteString(fmt.Sprintf("- Tester: `%s`", model.Tester.Status))
	if model.Tester.ExitCode != 0 {
		b.WriteString(fmt.Sprintf(" exit `%d`", model.Tester.ExitCode))
	}
	if model.Tester.Error != "" {
		b.WriteString(" - " + Redact(model.Tester.Error))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("- Codex: `%s`", model.Codex.Status))
	if model.Codex.ExitCode != 0 {
		b.WriteString(fmt.Sprintf(" exit `%d`", model.Codex.ExitCode))
	}
	if model.Codex.Error != "" {
		b.WriteString(" - " + Redact(model.Codex.Error))
	}
	b.WriteString("\n\n")
	b.WriteString("## Signals\n\n")
	if len(signals) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, signal := range signals {
			b.WriteString("- `" + signal + "`\n")
		}
	}
	b.WriteString("\n## Failed Trace IDs\n\n")
	wroteTrace := false
	for _, trace := range traces {
		if trace.FinalStatus < 400 && trace.BackendFailure == nil {
			continue
		}
		wroteTrace = true
		failureClass := ""
		if trace.BackendFailure != nil {
			failureClass = trace.BackendFailure.Class
		}
		b.WriteString(fmt.Sprintf("- `%s` client=`%s` status=`%d` surface=`%s` failure=`%s`\n",
			trace.RequestID,
			trace.ClientRequestID,
			trace.FinalStatus,
			trace.Surface,
			failureClass,
		))
	}
	if !wroteTrace {
		b.WriteString("- none\n")
	}
	b.WriteString("\n## Shim Log Matches\n\n")
	if len(logMatches) == 0 {
		b.WriteString("No high-signal log matches.\n")
	} else {
		b.WriteString("```text\n")
		limit := min(len(logMatches), 80)
		for _, match := range logMatches[:limit] {
			b.WriteString(match)
			b.WriteByte('\n')
		}
		if len(logMatches) > limit {
			b.WriteString(fmt.Sprintf("... %d additional matches omitted ...\n", len(logMatches)-limit))
		}
		b.WriteString("```\n")
	}
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

func isNoisySuccessfulLogLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, `"level":"warn"`) || strings.Contains(lower, `"level":"error"`) {
		return false
	}
	if strings.Contains(lower, `"status":4`) || strings.Contains(lower, `"status":5`) {
		return false
	}
	if strings.Contains(lower, `"backend_failure"`) {
		return false
	}
	if strings.Contains(lower, `"level":"debug"`) && strings.Contains(lower, `"msg":"http request/response bodies"`) {
		return !strings.Contains(lower, `"response_body":"{\"error`) &&
			!strings.Contains(lower, `"response_body":"{\\\"error`)
	}
	if strings.Contains(lower, `"level":"debug"`) &&
		strings.Contains(lower, `"msg":"normalized chat completion request for upstream compatibility"`) {
		return true
	}
	if strings.Contains(lower, `"status":200`) &&
		(strings.Contains(lower, `"path":"/healthz"`) ||
			strings.Contains(lower, `"path":"/readyz"`) ||
			strings.Contains(lower, `"path":"/v1/models"`)) {
		return true
	}
	return strings.Contains(lower, `"msg":"shim listening"`) ||
		strings.Contains(lower, `"path":"/debug/capabilities"`) ||
		strings.Contains(lower, `"route":"/debug/capabilities"`)
}

func hasFailedOrSlowTrace(traces []DebugTrace) bool {
	for _, trace := range traces {
		if trace.FinalStatus >= 400 || trace.BackendFailure != nil || trace.DurationMS >= 30000 {
			return true
		}
	}
	return false
}

func hasFailedTrace(traces []DebugTrace) bool {
	for _, trace := range traces {
		if trace.FinalStatus >= 400 || trace.BackendFailure != nil {
			return true
		}
	}
	return false
}

func applyDiagnosticVerdict(summary ModelSummary, traces []DebugTrace) ModelSummary {
	if summary.Verdict == VerdictCodexClean && hasFailedTrace(traces) {
		summary.Verdict = VerdictCodexRetryDependent
	}
	return summary
}

func detectSignals(logMatches []string, traces []DebugTrace) []string {
	seen := map[string]struct{}{}
	add := func(signal string) {
		if signal == "" {
			return
		}
		seen[signal] = struct{}{}
	}
	for _, match := range logMatches {
		lower := diagnosticSignalText(match)
		switch {
		case containsRawToolMarkupSignal(lower):
			add("raw_tool_markup")
		case strings.Contains(lower, "invalid tool") || strings.Contains(lower, "tool arguments"):
			add("invalid_tool_arguments")
		case strings.Contains(lower, "failed to satisfy its constraint") || strings.Contains(lower, "apply_patch failed to satisfy"):
			add("model_tool_use_failure")
		case strings.Contains(lower, "json_schema") || strings.Contains(lower, "schema"):
			add("json_schema_or_structured_output")
		case strings.Contains(lower, "unsupported input shape"):
			add("request_shape_incompatibility")
		case strings.Contains(lower, "timeout") || strings.Contains(lower, "context deadline"):
			add("upstream_timeout")
		case strings.Contains(lower, "transport") || strings.Contains(lower, "empty reply"):
			add("transport_error")
		case strings.Contains(lower, "readiness") || strings.Contains(lower, "catalog"):
			add("readiness_catalog_mismatch")
		case containsDiagnosticHTTPStatus(lower, 401) || containsDiagnosticHTTPStatus(lower, 403) ||
			strings.Contains(lower, "authentication") ||
			strings.Contains(lower, "unauthorized") ||
			strings.Contains(lower, "forbidden") ||
			strings.Contains(lower, "permission_denied"):
			add("provider_auth_failure")
		case containsDiagnosticHTTPStatus(lower, 429) || strings.Contains(lower, "rate") || strings.Contains(lower, "quota"):
			add("rate_limit_or_quota")
		case strings.Contains(lower, "panic"):
			add("shim_panic")
		case strings.Contains(lower, "failed to initialize samplers"):
			add("upstream_sampler_error")
		}
	}
	for _, trace := range traces {
		if trace.BackendFailure != nil {
			add(trace.BackendFailure.Class)
			if trace.BackendFailure.UpstreamStatus == 401 || trace.BackendFailure.UpstreamStatus == 403 {
				add("provider_auth_failure")
			}
			if trace.BackendFailure.UpstreamStatus == 429 {
				add("rate_limit_or_quota")
			}
		}
		if trace.FinalStatus >= 500 {
			add("server_error")
		} else if trace.FinalStatus >= 400 {
			add("client_error")
		}
	}
	out := make([]string, 0, len(seen))
	for signal := range seen {
		out = append(out, signal)
	}
	slices.Sort(out)
	return out
}

func inferOwner(signals []string) string {
	for _, signal := range signals {
		switch signal {
		case "shim_panic", "request_shape_incompatibility", "raw_tool_markup", "invalid_tool_arguments", "json_schema_or_structured_output":
			return "shim"
		case "model_tool_use_failure", "malformed_backend_response":
			return "model"
		case "provider_auth_failure", "rate_limit_or_quota", "readiness_catalog_mismatch", "transport_error", "upstream_timeout", "upstream_sampler_error":
			return "provider"
		}
	}
	if len(signals) > 0 {
		return "model"
	}
	return ""
}

func inferCategory(signals []string) string {
	for _, preferred := range []string{
		"request_shape_incompatibility",
		"raw_tool_markup",
		"invalid_tool_arguments",
		"model_tool_use_failure",
		"json_schema_or_structured_output",
		"upstream_timeout",
		"transport_error",
		"upstream_sampler_error",
		"readiness_catalog_mismatch",
	} {
		if slices.Contains(signals, preferred) {
			return preferred
		}
	}
	if len(signals) > 0 {
		return signals[0]
	}
	return "unknown"
}

func diagnosticSignalText(match string) string {
	lower := strings.ToLower(match)
	if !strings.Contains(lower, `"msg":"http request/response bodies"`) {
		return lower
	}
	if idx := strings.Index(lower, `"response_body"`); idx >= 0 {
		return lower[idx:]
	}
	return lower
}

func containsRawToolMarkupSignal(text string) bool {
	return strings.Contains(text, "raw_tool_markup") ||
		strings.Contains(text, "raw tool-call markup") ||
		strings.Contains(text, "raw tool call markup") ||
		strings.Contains(text, "raw-tool-call-markup")
}

func containsDiagnosticHTTPStatus(text string, code int) bool {
	codeText := strconv.Itoa(code)
	for _, marker := range []string{
		`"status":` + codeText,
		`"status":"` + codeText + `"`,
		`"status_code":` + codeText,
		`"status_code":"` + codeText + `"`,
		`"upstream_status":` + codeText,
		`"upstream_status":"` + codeText + `"`,
		`"http_status":` + codeText,
		`"http_status":"` + codeText + `"`,
		`status=` + codeText,
		`http ` + codeText,
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func firstFailingStage(model ModelSummary) string {
	if model.HealthStatus != 0 && model.HealthStatus/100 != 2 {
		return "shim_health"
	}
	if model.ReadyzStatus != 0 && model.ReadyzStatus/100 != 2 {
		return "shim_readiness"
	}
	if model.Tester.Status == "failed" {
		return "external_tester"
	}
	if model.Codex.Status == "failed" {
		return "codex"
	}
	return "none"
}

func Redact(value string) string {
	if value == "" {
		return ""
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s"']+`),
		regexp.MustCompile(`(?i)("authorization"\s*:\s*"bearer\s+)[^"]+(")`),
		regexp.MustCompile(`(?i)(api[_-]?key["'=:\s]+)[A-Za-z0-9_\-\.]+`),
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]+`),
	}
	out := value
	for _, pattern := range patterns {
		out = pattern.ReplaceAllString(out, `${1}<redacted>${2}`)
	}
	return out
}

func increment(values map[string]int, key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	values[key]++
}

func writeJSONFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
