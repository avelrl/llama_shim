package modelcert

import (
	"strconv"
	"strings"
	"testing"
)

func TestDiagnosticsClassifiesShimFixCandidates(t *testing.T) {
	model := ModelSummary{
		Model:            "svgun/qwen-3.6",
		Verdict:          VerdictAPICompatFailed,
		Tester:           StageSummary{Status: "failed", ExitCode: 1},
		FailureNotesPath: ".tmp/model-certification/test/models/svgun-qwen-3.6/failure-notes.md",
	}
	logMatches := []string{
		`12:{"level":"WARN","msg":"raw_tool_markup detected"}`,
		`13:{"level":"ERROR","msg":"unsupported input shape"}`,
	}

	candidates := BuildFixCandidates(model, logMatches, nil)
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", candidates)
	}
	if candidates[0].Owner != "shim" {
		t.Fatalf("expected shim owner, got %#v", candidates[0])
	}
	if candidates[0].Category != "request_shape_incompatibility" {
		t.Fatalf("expected request shape category, got %#v", candidates[0])
	}
}

func TestTraceSummaryGroupsFailures(t *testing.T) {
	summary := BuildTraceSummary([]DebugTrace{
		{
			RequestID:       "req_1",
			ClientRequestID: "modelcert:run:model:tester:case",
			Surface:         "responses",
			Provider:        "gpu",
			PublicModel:     "gpu/qwen3-coder-30b",
			UpstreamModel:   "coder30b",
			SelectedBackend: "proxy",
			FinalStatus:     502,
			DurationMS:      35000,
			BackendFailure:  &DebugTraceBackendFailure{Class: "transport_error", ClientStatus: 502},
		},
	})
	if summary.TraceCount != 1 {
		t.Fatalf("expected one trace, got %#v", summary)
	}
	if summary.ByFailureClass["transport_error"] != 1 {
		t.Fatalf("expected transport error grouping, got %#v", summary.ByFailureClass)
	}
	if len(summary.FailedRequests) != 1 || len(summary.SlowRequests) != 1 {
		t.Fatalf("expected failed and slow request digests, got %#v", summary)
	}
}

func TestDiagnosticsIgnoresSuccessfulCapabilityNoise(t *testing.T) {
	matches, err := WriteLogDiagnosticsFromStringForTest(`# not json
{"level":"INFO","msg":"shim listening","responses_constrained_decoding_backend":"shim_validate_repair","responses_web_search_timeout":10000000000}
{"level":"DEBUG","msg":"http request/response bodies","path":"/debug/capabilities","response_body":"{\"structured_outputs\":{\"enabled\":true},\"backend_failure_policy\":[{\"client_code\":\"upstream_timeout\"}]}"}
{"level":"INFO","msg":"http request","path":"/healthz","status":200}
{"level":"INFO","msg":"http request","path":"/readyz","status":200,"client_request_id":"modelcert:run:model:shim:readyz"}
{"level":"INFO","msg":"http request","path":"/v1/models","status":200}
{"level":"DEBUG","msg":"http request/response bodies","path":"/v1/responses","request_body":"contains 403 json_schema transport_error in a prompt","response_body":"[text/event-stream body omitted]"}
{"level":"DEBUG","msg":"normalized chat completion request for upstream compatibility","request_cleanup_hooks":["chat_completions.json_schema_to_json_object_instruction"],"json_schema_downgraded":true}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no high-signal matches for successful capability noise, got %#v", matches)
	}

	candidates := BuildFixCandidates(ModelSummary{
		Model:            "gpu/qwen3-coder-30b",
		Verdict:          VerdictAPICompatPassed,
		Tester:           StageSummary{Status: "passed"},
		Codex:            StageSummary{Status: "skipped"},
		FailureNotesPath: ".tmp/model-certification/test/failure-notes.md",
	}, []string{`1:json_schema upstream_timeout`}, []DebugTrace{{RequestID: "req_ok", FinalStatus: 200}})
	if len(candidates) != 0 {
		t.Fatalf("expected no fix candidates for passed run without failed traces, got %#v", candidates)
	}
}

func TestDiagnosticsClassifiesConstraintFailureAsModelOwned(t *testing.T) {
	model := ModelSummary{
		Model:            "gpu/qwen3-coder-30b",
		Verdict:          VerdictCodexFailed,
		Codex:            StageSummary{Status: "failed", ExitCode: 1},
		FailureNotesPath: ".tmp/model-certification/test/models/gpu-qwen3-coder-30b/failure-notes.md",
	}
	logMatches := []string{
		`42:{"level":"ERROR","msg":"upstream invalid response","err":"shim-local constrained custom tool apply_patch failed to satisfy its constraint after 3 attempts"}`,
	}
	traces := []DebugTrace{{
		RequestID:      "req_failed",
		FinalStatus:    502,
		BackendFailure: &DebugTraceBackendFailure{Class: "malformed_backend_response", ClientStatus: 502},
	}}

	candidates := BuildFixCandidates(model, logMatches, traces)
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", candidates)
	}
	if candidates[0].Owner != "model" {
		t.Fatalf("expected model owner, got %#v", candidates[0])
	}
	if candidates[0].Category != "model_tool_use_failure" {
		t.Fatalf("expected model tool-use category, got %#v", candidates[0])
	}
}

func TestDiagnosticsUsesResponseBodyForHTTPBodyLogSignals(t *testing.T) {
	matches, err := WriteLogDiagnosticsFromStringForTest(`{"level":"DEBUG","msg":"http request/response bodies","path":"/v1/responses","status":500,"request_body":"contains 403 json_schema quota in prompt text","response_body":"{\"error\":{\"message\":\"internal server error\",\"type\":\"internal_error\"}}"}`)
	if err != nil {
		t.Fatal(err)
	}
	signals := detectSignals(matches, nil)
	for _, noisy := range []string{"provider_auth_failure", "json_schema_or_structured_output", "rate_limit_or_quota"} {
		if slicesContainsString(signals, noisy) {
			t.Fatalf("expected request-body text not to create %s, got %#v", noisy, signals)
		}
	}
}

func TestDiagnosticsClassifiesRawMarkupWithoutAuthIDFalsePositive(t *testing.T) {
	matches, err := WriteLogDiagnosticsFromStringForTest(`{"level":"ERROR","msg":"unhandled error","request_id":"req_019e3f12-ffad-748c-a508-f8a6be8f4036","err":"chat completion assistant content contained raw tool-call markup"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected raw markup error to be high signal, got %#v", matches)
	}
	signals := detectSignals(matches, []DebugTrace{{
		RequestID:   "req_019e3f12-ffad-748c-a508-f8a6be8f4036",
		FinalStatus: 500,
	}})
	if !slicesContainsString(signals, "raw_tool_markup") {
		t.Fatalf("expected raw_tool_markup signal, got %#v", signals)
	}
	if slicesContainsString(signals, "provider_auth_failure") {
		t.Fatalf("expected request id digits not to create provider auth signal, got %#v", signals)
	}
}

func TestDiagnosticsClassifiesRealAuthStatus(t *testing.T) {
	signals := detectSignals([]string{
		`1:{"level":"INFO","msg":"http request","path":"/v1/responses","status":401}`,
		`2:{"level":"DEBUG","msg":"http request/response bodies","response_body":"{\"error\":{\"type\":\"authentication_error\",\"message\":\"unauthorized\"}}"}`,
	}, nil)
	if !slicesContainsString(signals, "provider_auth_failure") {
		t.Fatalf("expected provider auth signal, got %#v", signals)
	}
}

func TestFailedTraceCanDowngradeCodexCleanVerdict(t *testing.T) {
	got := applyDiagnosticVerdict(
		ModelSummary{Verdict: VerdictCodexClean},
		[]DebugTrace{{RequestID: "req_failed", FinalStatus: 502}},
	)
	if got.Verdict != VerdictCodexRetryDependent {
		t.Fatalf("expected retry-dependent verdict for passed Codex with failed trace, got %q", got.Verdict)
	}
	got = applyDiagnosticVerdict(
		ModelSummary{Verdict: VerdictCodexClean},
		[]DebugTrace{{RequestID: "req_slow", FinalStatus: 200, DurationMS: 45000}},
	)
	if got.Verdict != VerdictCodexClean {
		t.Fatalf("expected slow-only trace to keep clean verdict, got %q", got.Verdict)
	}
}

func TestRedactRemovesBearerAndAPIKeys(t *testing.T) {
	redacted := Redact(`Authorization: Bearer sk-secret OPENAI_API_KEY=sk-other`)
	if redacted == `Authorization: Bearer sk-secret OPENAI_API_KEY=sk-other` {
		t.Fatal("expected redaction to change the input")
	}
	if redacted == "" || redacted == " " {
		t.Fatalf("redaction removed too much: %q", redacted)
	}
}

func WriteLogDiagnosticsFromStringForTest(raw string) ([]string, error) {
	lines := strings.Split(raw, "\n")
	matches := make([]string, 0)
	for idx, line := range lines {
		if isNoisySuccessfulLogLine(line) {
			continue
		}
		if highSignalLogPattern.MatchString(line) {
			matches = append(matches, Redact(line))
			matches[len(matches)-1] = strconv.Itoa(idx+1) + ":" + matches[len(matches)-1]
		}
	}
	return matches, nil
}

func slicesContainsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
