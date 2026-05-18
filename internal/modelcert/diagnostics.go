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

var highSignalLogPattern = regexp.MustCompile(`(?i)"level":"(WARN|ERROR)"|"status":(4|5)[0-9][0-9]|upstream[_ ]timeout|transport[_ ]error|raw_tool_markup|failed_raw_tool_markup|invalid tool|tool arguments|json_schema|schema|unsupported input shape|readiness|catalog|401|403|404|408|409|422|429|500|502|503|504|panic|context deadline|empty reply|rate[_ ]limit|quota`)

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

func detectSignals(logMatches []string, traces []DebugTrace) []string {
	seen := map[string]struct{}{}
	add := func(signal string) {
		if signal == "" {
			return
		}
		seen[signal] = struct{}{}
	}
	for _, match := range logMatches {
		lower := strings.ToLower(match)
		switch {
		case strings.Contains(lower, "raw_tool_markup"):
			add("raw_tool_markup")
		case strings.Contains(lower, "invalid tool") || strings.Contains(lower, "tool arguments"):
			add("invalid_tool_arguments")
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
		case strings.Contains(lower, "401") || strings.Contains(lower, "403"):
			add("provider_auth_failure")
		case strings.Contains(lower, "429") || strings.Contains(lower, "rate") || strings.Contains(lower, "quota"):
			add("rate_limit_or_quota")
		case strings.Contains(lower, "panic"):
			add("shim_panic")
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
		case "provider_auth_failure", "rate_limit_or_quota", "readiness_catalog_mismatch", "transport_error", "upstream_timeout":
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
		"json_schema_or_structured_output",
		"upstream_timeout",
		"transport_error",
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
