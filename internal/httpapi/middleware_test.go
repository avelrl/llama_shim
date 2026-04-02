package httpapi_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
		httpapi.RequestLogMiddleware(logger),
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
	require.Contains(t, output, `"request_body":"{\"hello\":\"world\"}"`)
	require.Contains(t, output, `"response_body":"{\"echo\":{\"hello\":\"world\"}}"`)
}
