package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Embedder interface {
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
}

type openAICompatibleEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewEmbedder(cfg EmbedderConfig) (Embedder, error) {
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	switch cfg.Backend {
	case "", EmbedderBackendDisabled:
		return nil, nil
	case EmbedderBackendOpenAICompatible, EmbedderBackendEmbedAnything:
		baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		if baseURL == "" {
			return nil, fmt.Errorf("embedder base_url is required when backend=%q", cfg.Backend)
		}
		model := strings.TrimSpace(cfg.Model)
		if model == "" {
			return nil, fmt.Errorf("embedder model is required when backend=%q", cfg.Backend)
		}
		return &openAICompatibleEmbedder{
			baseURL: normalizeEmbeddingsBaseURL(baseURL),
			model:   model,
			client: &http.Client{
				Timeout: 60 * time.Second,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported retrieval embedder backend %q", cfg.Backend)
	}
}

func normalizeEmbeddingsBaseURL(baseURL string) string {
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/embeddings"
	}
	return baseURL + "/v1/embeddings"
}

func (e *openAICompatibleEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	inputs := make([]string, 0, len(texts))
	for _, text := range texts {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil, fmt.Errorf("embedder input text must not be empty")
		}
		inputs = append(inputs, trimmed)
	}
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}

	body, err := json.Marshal(map[string]any{
		"model":           e.model,
		"input":           inputs,
		"encoding_format": "float",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embeddings request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request embeddings: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(payload.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings response returned %d vectors for %d inputs", len(payload.Data), len(inputs))
	}

	out := make([][]float32, len(inputs))
	for _, row := range payload.Data {
		if row.Index < 0 || row.Index >= len(inputs) {
			return nil, fmt.Errorf("embeddings response index %d out of range", row.Index)
		}
		if len(row.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings response index %d returned empty vector", row.Index)
		}
		out[row.Index] = append([]float32(nil), row.Embedding...)
	}
	for i, embedding := range out {
		if len(embedding) == 0 {
			return nil, fmt.Errorf("embeddings response missing vector for input %d", i)
		}
	}
	return out, nil
}
