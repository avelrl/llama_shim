package httpapi_test

import (
	"context"
	"io"
	"log/slog"
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
	require.Equal(t, "input", payload.Param)

	status, payload = httpapi.MapError(context.Background(), logger, service.ErrUpstreamTimeout)
	require.Equal(t, 504, status)
	require.Equal(t, "upstream_timeout_error", payload.Type)

	status, payload = httpapi.MapError(context.Background(), logger, &llama.UpstreamError{
		StatusCode: 400,
		Message:    `{"error":"bad request"}`,
	})
	require.Equal(t, 502, status)
	require.Equal(t, "upstream_error", payload.Type)
}
