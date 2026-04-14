package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
)

type apiErrorPayload struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, errType, message, param string) {
	WriteJSON(w, status, apiErrorPayload{
		Error: newAPIError(errType, message, param, ""),
	})
}

func MapError(ctx context.Context, logger *slog.Logger, err error) (int, apiError) {
	if err == nil {
		return http.StatusOK, apiError{}
	}

	logDetailedError(ctx, logger, err)
	mappedErr := service.MapStorageError(err)
	mappedErr = service.MapGeneratorError(mappedErr)

	var validationErr *domain.ValidationError
	var toolChoiceErr *toolChoiceIncompatibleBackendError
	var rateLimitErr *rateLimitExceededError
	switch {
	case errors.As(mappedErr, &validationErr):
		return http.StatusBadRequest, newAPIError("invalid_request_error", validationErr.Message, validationErr.Param, "")
	case errors.As(mappedErr, &toolChoiceErr):
		return http.StatusNotImplemented, newAPIError("server_error", toolChoiceErr.Error(), "tool_choice", "tool_choice_incompatible_backend")
	case errors.As(mappedErr, &rateLimitErr):
		return http.StatusTooManyRequests, newAPIError("rate_limit_error", rateLimitErr.Error(), "", rateLimitErr.Code)
	case errors.Is(mappedErr, sqlite.ErrNotFound), errors.Is(mappedErr, service.ErrNotFound):
		return http.StatusNotFound, newAPIError("not_found_error", mappedErr.Error(), "", "")
	case errors.Is(mappedErr, sqlite.ErrConflict), errors.Is(mappedErr, service.ErrConflict):
		return http.StatusConflict, newAPIError("conflict_error", "conversation state changed during generation, retry the request", "", "")
	case errors.Is(mappedErr, service.ErrUpstreamTimeout):
		return http.StatusGatewayTimeout, newAPIError("upstream_timeout_error", "llama.cpp request timed out", "", "")
	case errors.Is(mappedErr, service.ErrUpstreamFailure):
		return http.StatusBadGateway, newAPIError("upstream_error", "llama.cpp request failed", "", "")
	default:
		logger.ErrorContext(ctx, "unhandled error", "err", err)
		return http.StatusInternalServerError, newAPIError("internal_error", "internal server error", "", "")
	}
}

func newAPIError(errType, message, param, code string) apiError {
	return apiError{
		Message: message,
		Type:    errType,
		Param:   optionalString(param),
		Code:    optionalString(code),
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func canonicalizeAPIErrorBody(status int, body []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, err
	}

	rawError, ok := payload["error"]
	if !ok {
		return nil, false, nil
	}

	errorPayload, ok := rawError.(map[string]any)
	if !ok {
		return nil, false, nil
	}

	canonical, ok := normalizeAPIErrorPayload(status, errorPayload)
	if !ok {
		return nil, false, nil
	}

	payload["error"] = canonical
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

func normalizeAPIErrorPayload(status int, errorPayload map[string]any) (map[string]any, bool) {
	fields := map[string]any{
		"message": errorPayload["message"],
		"type":    errorPayload["type"],
		"param":   errorPayload["param"],
		"code":    errorPayload["code"],
	}
	if embedded, ok := extractEmbeddedAPIError(asString(errorPayload["message"])); ok {
		fields = mergeAPIErrorFields(fields, embedded)
	}

	message := strings.TrimSpace(asString(fields["message"]))
	if message == "" {
		return nil, false
	}

	return map[string]any{
		"message": message,
		"type":    normalizeAPIErrorType(status, asString(fields["type"])),
		"param":   normalizeAPIErrorParam(fields["param"]),
		"code":    normalizeAPIErrorCode(fields["code"]),
	}, true
}

func mergeAPIErrorFields(base, override map[string]any) map[string]any {
	merged := map[string]any{
		"message": base["message"],
		"type":    base["type"],
		"param":   base["param"],
		"code":    base["code"],
	}
	for _, key := range []string{"message", "type", "param", "code"} {
		value, ok := override[key]
		if !ok || !hasAPIErrorValue(value) {
			continue
		}
		merged[key] = value
	}
	return merged
}

func hasAPIErrorValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return true
	}
}

func extractEmbeddedAPIError(message string) (map[string]any, bool) {
	embeddedJSON, ok := extractEmbeddedJSONObject(message)
	if !ok {
		return nil, false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(embeddedJSON), &payload); err != nil {
		return nil, false
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		return nil, false
	}
	return errorPayload, true
}

func extractEmbeddedJSONObject(message string) (string, bool) {
	start := strings.Index(message, `{"error"`)
	if start < 0 {
		start = strings.Index(message, `{"message"`)
	}
	if start < 0 {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(message); i++ {
		ch := message[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return message[start : i+1], true
			}
		}
	}
	return "", false
}

func normalizeAPIErrorType(status int, raw string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case "invalid_request_error", "server_error", "not_found_error", "conflict_error", "rate_limit_error", "authentication_error", "permission_error":
		return normalized
	case "bad request", "bad_request":
		return "invalid_request_error"
	case "not found", "not_found":
		return "not_found_error"
	case "conflict":
		return "conflict_error"
	case "too many requests", "rate limit", "rate_limit":
		return "rate_limit_error"
	case "unauthorized":
		return "authentication_error"
	case "forbidden":
		return "permission_error"
	case "server error", "internal server error":
		return "server_error"
	default:
		switch status {
		case http.StatusNotFound:
			return "not_found_error"
		case http.StatusConflict:
			return "conflict_error"
		case http.StatusTooManyRequests:
			return "rate_limit_error"
		default:
			if status >= 500 {
				return "server_error"
			}
			return "invalid_request_error"
		}
	}
}

func normalizeAPIErrorParam(raw any) any {
	param, ok := raw.(string)
	if !ok || strings.TrimSpace(param) == "" {
		return nil
	}
	return param
}

func normalizeAPIErrorCode(raw any) any {
	switch value := raw.(type) {
	case nil:
		return nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil
		}
		if _, err := strconv.Atoi(trimmed); err == nil {
			return nil
		}
		return trimmed
	case float64:
		return nil
	case float32:
		return nil
	case int, int8, int16, int32, int64:
		return nil
	case uint, uint8, uint16, uint32, uint64:
		return nil
	default:
		return nil
	}
}

func logDetailedError(ctx context.Context, logger *slog.Logger, err error) {
	var upstreamErr *llama.UpstreamError
	if errors.As(err, &upstreamErr) {
		logger.ErrorContext(ctx, "upstream request failed",
			"request_id", RequestIDFromContext(ctx),
			"status", upstreamErr.StatusCode,
			"body", truncateForLog(upstreamErr.Message, 2048),
		)
		return
	}

	var timeoutErr *llama.TimeoutError
	if errors.As(err, &timeoutErr) {
		logger.ErrorContext(ctx, "upstream request timed out",
			"request_id", RequestIDFromContext(ctx),
			"err", timeoutErr.Error(),
		)
	}
}

func truncateForLog(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}
