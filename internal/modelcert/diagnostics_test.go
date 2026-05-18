package modelcert

import "testing"

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

func TestRedactRemovesBearerAndAPIKeys(t *testing.T) {
	redacted := Redact(`Authorization: Bearer sk-secret OPENAI_API_KEY=sk-other`)
	if redacted == `Authorization: Bearer sk-secret OPENAI_API_KEY=sk-other` {
		t.Fatal("expected redaction to change the input")
	}
	if redacted == "" || redacted == " " {
		t.Fatalf("redaction removed too much: %q", redacted)
	}
}
