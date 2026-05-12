package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"llama_shim/internal/httpapi"
)

func TestRequestLogMiddlewareLogsBodiesAtDebug(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, err = w.Write([]byte(`{"echo":` + string(body) + `}`))
			require.NoError(t, err)
		}),
		httpapi.RequestLogMiddleware(logger, nil, nil),
		httpapi.RequestIDMiddleware,
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(`{"hello":"world"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.JSONEq(t, `{"echo":{"hello":"world"}}`, recorder.Body.String())

	output := logs.String()
	require.Contains(t, output, `"msg":"http request"`)
	require.Contains(t, output, `"msg":"http request/response bodies"`)
	require.Contains(t, output, `"response_content_type":"application/json"`)
	require.Contains(t, output, `"request_body":"{\"hello\":\"world\"}"`)
	require.Contains(t, output, `"request_body_truncated":false`)
	require.Contains(t, output, `"response_body":"{\"echo\":{\"hello\":\"world\"}}"`)
	require.Contains(t, output, `"response_body_truncated":false`)
}

func TestRequestLogMiddlewareMarksTruncatedBodies(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	large := strings.Repeat("a", 17<<10)
	handler := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			w.Header().Set("Content-Type", "text/plain")
			_, err = w.Write([]byte(large))
			require.NoError(t, err)
		}),
		httpapi.RequestLogMiddleware(logger, nil, nil),
		httpapi.RequestIDMiddleware,
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(large))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	output := logs.String()
	require.Contains(t, output, `"request_body_truncated":true`)
	require.Contains(t, output, `"response_body_truncated":true`)
}

func TestRequestLogMiddlewareOmitsSSEBodiesAtDebug(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, err := w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"secret\"}\n\n"))
			require.NoError(t, err)
		}),
		httpapi.RequestLogMiddleware(logger, nil, nil),
		httpapi.RequestIDMiddleware,
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	output := logs.String()
	require.Contains(t, output, `"response_content_type":"text/event-stream"`)
	require.Contains(t, output, `"response_body":"[text/event-stream body omitted]"`)
	require.NotContains(t, output, `response.output_text.delta`)
	require.NotContains(t, output, `"delta":"secret"`)
}

func TestRequestLogMiddlewareBoundsRequestBodyRead(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const logBodyLimit = 16 << 10
	body := &errorAfterNReadCloser{
		data:        []byte(strings.Repeat("a", logBodyLimit+64)),
		errorAfterN: logBodyLimit + 1,
	}

	handler := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}),
		httpapi.RequestLogMiddleware(logger, nil, nil),
		httpapi.RequestIDMiddleware,
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/test", body)
	req.ContentLength = int64(len(body.data))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	output := logs.String()
	require.NotContains(t, output, "[failed to read request body]")
	require.Contains(t, output, `"request_body_truncated":true`)
	require.Contains(t, output, `"request_body_bytes":16448`)
	require.Contains(t, output, `"request_body_captured_bytes":16384`)
}

func TestRequestLogMiddlewareTelemetryOmitsSensitiveRequestData(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	debugTraces := httpapi.NewDebugTraceStore(4)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, err = w.Write([]byte(`{"output":"TOOL_OUTPUT_SECRET_otel_regression"}`))
		require.NoError(t, err)
	})
	handler := httpapi.Chain(
		mux,
		httpapi.RequestIDMiddleware,
		httpapi.RequestLogMiddleware(logger, nil, debugTraces),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses?api_key=QUERY_SECRET_otel_regression",
		strings.NewReader(`{"input":"PROMPT_SECRET_otel_regression","tool_output":"TOOL_INPUT_SECRET_otel_regression"}`),
	)
	req.Header.Set("Authorization", "Bearer AUTH_SECRET_otel_regression")
	req.Header.Set("X-Provider-Authorization", "Bearer PROVIDER_SECRET_otel_regression")
	req.Header.Set("X-Client-Request-Id", "client-req-otel-test")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusCreated, recorder.Code)
	spans := spanRecorder.Ended()
	require.Len(t, spans, 1)
	spanText := telemetrySpanText(spans[0])
	require.Contains(t, spanText, "http.request.method=POST")
	require.Contains(t, spanText, "url.path=/v1/responses")
	require.Contains(t, spanText, "http.route=")
	require.Contains(t, spanText, "llama_shim.client_request_id=client-req-otel-test")

	for _, secret := range []string{
		"AUTH_SECRET_otel_regression",
		"PROVIDER_SECRET_otel_regression",
		"QUERY_SECRET_otel_regression",
		"PROMPT_SECRET_otel_regression",
		"TOOL_INPUT_SECRET_otel_regression",
		"TOOL_OUTPUT_SECRET_otel_regression",
	} {
		require.NotContains(t, spanText, secret)
	}
}

func telemetrySpanText(span sdktrace.ReadOnlySpan) string {
	var out strings.Builder
	out.WriteString(span.Name())
	status := span.Status()
	out.WriteString(status.Code.String())
	out.WriteString(status.Description)
	for _, attr := range span.Attributes() {
		_, _ = fmt.Fprintf(&out, "\n%s=%v", attr.Key, attr.Value.AsInterface())
	}
	for _, event := range span.Events() {
		out.WriteString("\n")
		out.WriteString(event.Name)
		for _, attr := range event.Attributes {
			_, _ = fmt.Fprintf(&out, "\n%s=%v", attr.Key, attr.Value.AsInterface())
		}
	}
	return out.String()
}

type errorAfterNReadCloser struct {
	data        []byte
	offset      int
	errorAfterN int
}

func (r *errorAfterNReadCloser) Read(p []byte) (int, error) {
	if r.offset >= r.errorAfterN {
		return 0, errors.New("read limit exceeded")
	}
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *errorAfterNReadCloser) Close() error {
	return nil
}
