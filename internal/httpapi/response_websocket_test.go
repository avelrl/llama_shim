package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildWebSocketStreamCreateBodyForcesStream(t *testing.T) {
	body, err := buildWebSocketStreamCreateBody([]byte(`{"model":"test-model","store":false,"input":"hello","stream":false}`))
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "test-model", payload["model"])
	require.Equal(t, false, payload["store"])
	require.Equal(t, "hello", payload["input"])
	require.Equal(t, true, payload["stream"])
}

func TestWebSocketResponseStreamWriterBoundsNonStreamBody(t *testing.T) {
	writer := newWebSocketResponseStreamWriter(context.Background(), nil, nil, 4, nil)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusBadGateway)

	n, err := writer.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)

	body, overflow := writer.Body()
	require.True(t, overflow)
	require.Equal(t, "abcde", string(body))
}
