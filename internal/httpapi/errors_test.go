package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/httpapi"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
)

func TestMapError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	status, payload := httpapi.MapError(context.Background(), logger, domain.NewValidationError("input", "input is required"))
	require.Equal(t, 400, status)
	require.Equal(t, "invalid_request_error", payload.Type)
	require.NotNil(t, payload.Param)
	require.Equal(t, "input", *payload.Param)
	require.Nil(t, payload.Code)

	status, payload = httpapi.MapError(context.Background(), logger, domain.ErrUnsupportedShape)
	require.Equal(t, 400, status)
	require.Equal(t, "invalid_request_error", payload.Type)
	require.Equal(t, "unsupported input shape", payload.Message)
	require.NotNil(t, payload.Param)
	require.Equal(t, "input", *payload.Param)

	status, payload = httpapi.MapError(context.Background(), logger, service.ErrUpstreamTimeout)
	require.Equal(t, 504, status)
	require.Equal(t, "upstream_timeout_error", payload.Type)
	require.Equal(t, "upstream request timed out", payload.Message)

	status, payload = httpapi.MapError(context.Background(), logger, &llama.UpstreamError{
		StatusCode: 400,
		Message:    `{"error":"bad request"}`,
	})
	require.Equal(t, 502, status)
	require.Equal(t, "upstream_error", payload.Type)
	require.Equal(t, "upstream request failed", payload.Message)

	status, payload = httpapi.MapError(context.Background(), logger, &llama.UpstreamError{
		StatusCode: http.StatusUnauthorized,
		Message:    `{"error":{"message":"Incorrect API key provided"}}`,
	})
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "authentication_error", payload.Type)
	require.Equal(t, "upstream_auth_failure", *payload.Code)

	status, payload = httpapi.MapError(context.Background(), logger, &llama.UpstreamError{
		StatusCode: http.StatusTooManyRequests,
		Message:    `{"error":{"message":"Rate limit reached for requests"}}`,
	})
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", payload.Type)
	require.Equal(t, "upstream_rate_limit", *payload.Code)
}

func TestWriteErrorUsesCanonicalOpenAIShape(t *testing.T) {
	recorder := httptest.NewRecorder()

	httpapi.WriteError(recorder, http.StatusBadRequest, "invalid_request_error", "messages is required", "")

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var payload map[string]map[string]any
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&payload))

	errorPayload := payload["error"]
	require.Equal(t, "messages is required", errorPayload["message"])
	require.Equal(t, "invalid_request_error", errorPayload["type"])
	require.Contains(t, errorPayload, "param")
	require.Nil(t, errorPayload["param"])
	require.Contains(t, errorPayload, "code")
	require.Nil(t, errorPayload["code"])
}
