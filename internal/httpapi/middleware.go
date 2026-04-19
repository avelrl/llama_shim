package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

type contextKey string

const requestIDKey contextKey = "request_id"
const clientRequestIDKey contextKey = "client_request_id"
const authSubjectKey contextKey = "auth_subject"
const requestRateLimitKeyCtx contextKey = "request_rate_limit"
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
		clientRequestID := strings.TrimSpace(r.Header.Get("X-Client-Request-Id"))
		if clientRequestID != "" {
			if len(clientRequestID) > 512 || !isASCII(clientRequestID) {
				WriteError(w, http.StatusBadRequest, "invalid_request_error", "X-Client-Request-Id must contain only ASCII characters and be at most 512 characters long", "")
				return
			}
		}
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = domain.MustNewRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		if clientRequestID != "" {
			ctx = setClientRequestID(ctx, clientRequestID)
		}
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

func RequestLogMiddleware(logger *slog.Logger, metrics *Metrics) func(http.Handler) http.Handler {
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
			route := requestRouteLabel(r)
			if metrics != nil {
				metrics.ObserveHTTPRequest(r.Method, route, recorder.status, time.Since(start))
			}
			logger.InfoContext(r.Context(), "http request",
				"request_id", RequestIDFromContext(r.Context()),
				"client_request_id", ClientRequestIDFromContext(r.Context()),
				"auth_subject", AuthSubjectFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"route", route,
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

func JSONBodyLimitMiddleware(limit int64) func(http.Handler) http.Handler {
	limit = max(limit, 1)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.ContentLength == 0 {
				next.ServeHTTP(w, r)
				return
			}
			contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
			if strings.HasPrefix(contentType, "multipart/form-data") {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

func StaticBearerAuthMiddleware(cfg StaticBearerAuthConfig, metrics *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled() || isHealthPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			token, ok := extractBearerToken(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				if metrics != nil {
					metrics.IncAuthFailure("missing_bearer")
				}
				WriteJSON(w, http.StatusUnauthorized, apiErrorPayload{
					Error: newAPIError("authentication_error", "missing or invalid bearer token", "", ""),
				})
				return
			}

			subject, ok := cfg.subjectForToken(token)
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				if metrics != nil {
					metrics.IncAuthFailure("invalid_bearer")
				}
				WriteJSON(w, http.StatusUnauthorized, apiErrorPayload{
					Error: newAPIError("authentication_error", "invalid bearer token", "", ""),
				})
				return
			}

			cloned := r.Clone(setAuthSubject(r.Context(), subject))
			cloned.Header = r.Header.Clone()
			cloned.Header.Del("Authorization")
			next.ServeHTTP(w, cloned)
		})
	}
}

func RateLimitMiddleware(cfg RateLimitConfig, metrics *Metrics, metricsPath string) func(http.Handler) http.Handler {
	limiter := newTokenBucketLimiter(cfg)
	metricsPath = strings.TrimSpace(metricsPath)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil || isHealthPath(r.URL.Path) || (metricsPath != "" && r.URL.Path == metricsPath) {
				next.ServeHTTP(w, r)
				return
			}

			decision := limiter.allow(requestRateLimitKey(r), time.Now())
			w.Header().Set("X-RateLimit-Limit-Requests", strconv.Itoa(decision.Limit))
			w.Header().Set("X-RateLimit-Remaining-Requests", strconv.Itoa(decision.Remaining))
			w.Header().Set("X-RateLimit-Reset-Requests", formatRateLimitReset(decision.Reset))
			ctx := setRequestRateLimit(r.Context(), decision)
			if !decision.Allowed {
				if metrics != nil {
					metrics.IncRateLimitReject("http_requests")
				}
				WriteJSON(w, http.StatusTooManyRequests, apiErrorPayload{
					Error: newAPIError("rate_limit_error", "request rate limit exceeded", "", "rate_limit_exceeded"),
				})
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isHealthPath(path string) bool {
	switch strings.TrimSpace(path) {
	case "/healthz", "/readyz":
		return true
	default:
		return false
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

	limitedReader := io.LimitReader(r.Body, maxDebugLogBodyBytes+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return capturedBody{
			text:       "[failed to read request body]",
			truncated:  false,
			totalBytes: 0,
		}
	}
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))

	truncated := false
	totalBytes := len(body)
	captured := body
	if len(body) > maxDebugLogBodyBytes {
		captured = body[:maxDebugLogBodyBytes]
		truncated = true
		if r.ContentLength > int64(totalBytes) {
			totalBytes = int(r.ContentLength)
		}
	}
	return capturedBody{
		text:          formatBodyForLog(captured, truncated),
		totalBytes:    totalBytes,
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
