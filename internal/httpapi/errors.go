package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
)

type apiErrorPayload struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, errType, message, param string) {
	WriteJSON(w, status, apiErrorPayload{
		Error: apiError{
			Type:    errType,
			Message: message,
			Param:   param,
		},
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
	switch {
	case errors.As(mappedErr, &validationErr):
		return http.StatusBadRequest, apiError{
			Type:    "invalid_request_error",
			Message: validationErr.Message,
			Param:   validationErr.Param,
		}
	case errors.Is(mappedErr, sqlite.ErrNotFound), errors.Is(mappedErr, service.ErrNotFound):
		return http.StatusNotFound, apiError{
			Type:    "not_found_error",
			Message: mappedErr.Error(),
		}
	case errors.Is(mappedErr, sqlite.ErrConflict), errors.Is(mappedErr, service.ErrConflict):
		return http.StatusConflict, apiError{
			Type:    "conflict_error",
			Message: "conversation state changed during generation, retry the request",
		}
	case errors.Is(mappedErr, service.ErrUpstreamTimeout):
		return http.StatusGatewayTimeout, apiError{
			Type:    "upstream_timeout_error",
			Message: "llama.cpp request timed out",
		}
	case errors.Is(mappedErr, service.ErrUpstreamFailure):
		return http.StatusBadGateway, apiError{
			Type:    "upstream_error",
			Message: "llama.cpp request failed",
		}
	default:
		logger.ErrorContext(ctx, "unhandled error", "err", err)
		return http.StatusInternalServerError, apiError{
			Type:    "internal_error",
			Message: "internal server error",
		}
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
