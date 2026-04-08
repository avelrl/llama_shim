package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

type contextKey string

const requestIDKey contextKey = "request_id"
const maxDebugLogBodyBytes = 16 << 10
const omittedSSEBodyLog = "[text/event-stream body omitted]"

type capturedBody struct {
	text          string
	totalBytes    int
	capturedBytes int
	truncated     bool
}

func RequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	wrapped := handler
	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](wrapped)
	}
	return wrapped
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = domain.MustNewRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ForwardHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := llama.ContextWithForwardHeaders(r.Context(), r.Header)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RecoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(r.Context(), "panic recovered", "panic", recovered, "request_id", RequestIDFromContext(r.Context()))
					WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error", "")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func RequestLogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			captureBodies := logger.Enabled(r.Context(), slog.LevelDebug)
			requestBody := capturedBody{}
			if captureBodies {
				requestBody = captureRequestBody(r)
			}

			recorder := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
				captureBody:    captureBodies,
			}
			next.ServeHTTP(recorder, r)
			logger.InfoContext(r.Context(), "http request",
				"request_id", RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"response_content_type", recorder.Header().Get("Content-Type"),
			)
			if captureBodies {
				attrs := []any{
					"request_id", RequestIDFromContext(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
				}
				attrs = appendBodyLogAttrs(attrs, "request", requestBody)
				attrs = appendBodyLogAttrs(attrs, "response", recorder.capturedBody())
				logger.DebugContext(r.Context(), "http request/response bodies", attrs...)
			}
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status         int
	body           []byte
	totalBodyBytes int
	captureBody    bool
	bodyTruncated  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	r.totalBodyBytes += len(p)
	if r.captureBody && !r.omitBodyFromLogs() && len(p) > 0 {
		remaining := maxDebugLogBodyBytes - len(r.body)
		switch {
		case remaining > 0 && len(p) > remaining:
			r.body = append(r.body, p[:remaining]...)
			r.bodyTruncated = true
		case remaining > 0:
			r.body = append(r.body, p...)
		default:
			r.bodyTruncated = true
		}
	}
	return r.ResponseWriter.Write(p)
}

func (r *statusRecorder) Flush() {
	flusher, ok := r.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) bodyString() string {
	if r.omitBodyFromLogs() {
		return omittedSSEBodyLog
	}
	return formatBodyForLog(r.body, r.bodyTruncated)
}

func (r *statusRecorder) capturedBody() capturedBody {
	capturedBytes := len(r.body)
	if r.omitBodyFromLogs() {
		capturedBytes = 0
	}
	return capturedBody{
		text:          r.bodyString(),
		totalBytes:    r.totalBodyBytes,
		capturedBytes: capturedBytes,
		truncated:     r.bodyTruncated,
	}
}

func (r *statusRecorder) omitBodyFromLogs() bool {
	return strings.Contains(strings.ToLower(r.Header().Get("Content-Type")), "text/event-stream")
}

func captureRequestBody(r *http.Request) capturedBody {
	if r.Body == nil {
		return capturedBody{}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return capturedBody{
			text:       "[failed to read request body]",
			truncated:  false,
			totalBytes: 0,
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	truncated := false
	captured := body
	if len(body) > maxDebugLogBodyBytes {
		captured = body[:maxDebugLogBodyBytes]
		truncated = true
	}
	return capturedBody{
		text:          formatBodyForLog(captured, truncated),
		totalBytes:    len(body),
		capturedBytes: len(captured),
		truncated:     truncated,
	}
}

func appendBodyLogAttrs(attrs []any, prefix string, body capturedBody) []any {
	return append(attrs,
		prefix+"_body", body.text,
		prefix+"_body_bytes", body.totalBytes,
		prefix+"_body_captured_bytes", body.capturedBytes,
		prefix+"_body_truncated", body.truncated,
	)
}

func formatBodyForLog(body []byte, truncated bool) string {
	if len(body) == 0 {
		return ""
	}
	if !utf8.Valid(body) {
		if truncated {
			return "[non-utf8 body omitted]...(truncated)"
		}
		return "[non-utf8 body omitted]"
	}

	value := string(body)
	if truncated {
		return value + "...(truncated)"
	}
	return value
}
