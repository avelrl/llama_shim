package codexeval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestShapeCaptureProxyRedactsAndForwards(t *testing.T) {
	var upstreamAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuthorization = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("upstream path = %s, want /v1/responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response"}`))
	}))
	defer upstream.Close()

	artifactPath := filepath.Join(t.TempDir(), requestShapeArtifactName)
	capture, err := startRequestShapeCapture(upstream.URL+"/v1", artifactPath)
	if err != nil {
		t.Fatalf("startRequestShapeCapture() error = %v", err)
	}
	defer capture.Close()

	req, err := http.NewRequest(http.MethodPost, capture.ProviderBaseURL()+"/responses", stringsReader(`{
		"model":"devstack-model",
		"input":[{"type":"message"}],
		"tools":[
			{"type":"function","name":"exec_command"},
			{"type":"custom","name":"apply_patch","format":{"type":"grammar"}}
		],
		"stream":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Client-Request-Id", "req_123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	_ = resp.Body.Close()
	if upstreamAuthorization != "Bearer secret" {
		t.Fatalf("upstream authorization = %q, want raw bearer", upstreamAuthorization)
	}
	if err := capture.WriteArtifact(); err != nil {
		t.Fatalf("WriteArtifact() error = %v", err)
	}

	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	var artifact requestShapeArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	if len(artifact.Requests) != 1 {
		t.Fatalf("captured requests = %d, want 1", len(artifact.Requests))
	}
	request := artifact.Requests[0]
	if request.Headers["authorization"] != "[REDACTED]" {
		t.Fatalf("authorization header = %q, want redacted", request.Headers["authorization"])
	}
	if request.Headers["x-client-request-id"] != "req_123" {
		t.Fatalf("x-client-request-id = %q", request.Headers["x-client-request-id"])
	}
	if request.Model != "devstack-model" {
		t.Fatalf("model = %q", request.Model)
	}
	if !containsString(request.ToolNames, "exec_command") {
		t.Fatalf("tool names = %v, want exec_command", request.ToolNames)
	}
	if !containsString(request.ToolNameTypes, "exec_command:function") {
		t.Fatalf("tool name/types = %v, want exec_command:function", request.ToolNameTypes)
	}
	if !containsString(request.ToolNameTypes, "apply_patch:custom") {
		t.Fatalf("tool name/types = %v, want apply_patch:custom", request.ToolNameTypes)
	}
	if request.Stream == nil || !*request.Stream {
		t.Fatalf("stream = %v, want true", request.Stream)
	}
}

func TestRunCheckersRequestShapes(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	if err := os.WriteFile(output, []byte(`{"type":"item.completed","item":{"type":"agent_message","text":"OK"}}
{"type":"turn.completed"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	stream := true
	artifact := requestShapeArtifact{
		Version: 1,
		Requests: []CapturedRequestShape{{
			Transport:         "http",
			Method:            http.MethodPost,
			Path:              "/v1/responses",
			Headers:           map[string]string{"authorization": "[REDACTED]"},
			BodyFields:        []string{"input", "model", "stream", "tools"},
			Model:             "devstack-model",
			Stream:            &stream,
			ToolNames:         []string{"exec_command"},
			ToolTypes:         []string{"function"},
			ToolNameTypes:     []string{"exec_command:function"},
			InputItemTypes:    []string{"message"},
			InputItemCount:    1,
			ToolChoicePresent: false,
		}},
	}
	if err := writeJSON(filepath.Join(workspace, requestShapeArtifactName), artifact); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "request_shape_http",
		Expected: Expected{
			FinalTextContains: []string{"OK"},
			RequestShapes: []RequestShapeExpectation{{
				Transport:              "http",
				Method:                 http.MethodPost,
				Path:                   "/v1/responses",
				Model:                  "devstack-model",
				RequiredHeaders:        []string{"authorization"},
				RedactedHeaders:        []string{"authorization"},
				RequiredBodyFields:     []string{"input", "model", "tools"},
				RequiredToolNames:      []string{"exec_command"},
				RequiredToolTypes:      []string{"function"},
				RequiredToolNameTypes:  []string{"exec_command:function"},
				RequiredInputItemTypes: []string{"message"},
				Stream:                 &stream,
			}},
		},
	}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, got %#v", result.Failures)
	}
}

func TestRunCheckersRequestShapesFailsOnMissingHeader(t *testing.T) {
	workspace := t.TempDir()
	output := filepath.Join(workspace, "codex.jsonl")
	if err := os.WriteFile(output, []byte(`{"type":"turn.completed"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(workspace, requestShapeArtifactName), requestShapeArtifact{
		Version:  1,
		Requests: []CapturedRequestShape{{Transport: "http", Method: http.MethodPost, Path: "/v1/responses"}},
	}); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ID: "request_shape_http",
		Expected: Expected{
			RequestShapes: []RequestShapeExpectation{{
				Transport:       "http",
				Method:          http.MethodPost,
				Path:            "/v1/responses",
				RequiredHeaders: []string{"authorization"},
			}},
		},
	}
	result, _, err := runCheckers(t.Context(), manifest, workspace, output, nil)
	if err != nil {
		t.Fatalf("runCheckers failed: %v", err)
	}
	if result.Passed {
		t.Fatalf("expected failure")
	}
	if got := result.Failures[0].Kind; got != "request_shape" {
		t.Fatalf("failure kind = %s, want request_shape", got)
	}
}

func stringsReader(value string) *strings.Reader {
	return strings.NewReader(value)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
