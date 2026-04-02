package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

type contextKey string

const requestIDKey contextKey = "request_id"
const maxDebugLogBodyBytes = 16 << 10

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
			requestBody := ""
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
			)
			if captureBodies {
				logger.DebugContext(r.Context(), "http request/response bodies",
					"request_id", RequestIDFromContext(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"request_body", requestBody,
					"response_body", recorder.bodyString(),
				)
			}
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status        int
	body          []byte
	captureBody   bool
	bodyTruncated bool
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.captureBody && len(p) > 0 {
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
	return formatBodyForLog(r.body, r.bodyTruncated)
}

func captureRequestBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return "[failed to read request body]"
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	truncated := false
	if len(body) > maxDebugLogBodyBytes {
		body = body[:maxDebugLogBodyBytes]
		truncated = true
	}
	return formatBodyForLog(body, truncated)
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
