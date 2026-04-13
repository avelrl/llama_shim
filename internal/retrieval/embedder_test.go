package retrieval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEmbedderOpenAICompatible(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"index": 0, "embedding": [1, 0]},
				{"index": 1, "embedding": [0, 1]}
			]
		}`))
	}))
	t.Cleanup(server.Close)

	embedder, err := NewEmbedder(EmbedderConfig{
		Backend: EmbedderBackendOpenAICompatible,
		BaseURL: server.URL,
		Model:   "text-embedding-3-small",
	})
	require.NoError(t, err)

	vectors, err := embedder.EmbedTexts(context.Background(), []string{"alpha", "beta"})
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 0}, {0, 1}}, vectors)
}

func TestNormalizeEmbeddingsBaseURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "http://127.0.0.1:8082/v1/embeddings", normalizeEmbeddingsBaseURL("http://127.0.0.1:8082"))
	require.Equal(t, "http://127.0.0.1:8082/v1/embeddings", normalizeEmbeddingsBaseURL("http://127.0.0.1:8082/v1"))
}

func TestEmbedAnythingHealthCheckURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "http://127.0.0.1:8082/health_check", healthCheckURL("http://127.0.0.1:8082/v1/embeddings"))
}
