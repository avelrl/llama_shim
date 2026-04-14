package imagegen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llama_shim/internal/llama"
)

const (
	BackendDisabled  = "disabled"
	BackendResponses = "responses"

	defaultTimeout = 60 * time.Second
)

type Config struct {
	Backend string
	BaseURL string
	Timeout time.Duration
}

type StreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type Provider interface {
	CheckReady(ctx context.Context) error
	Create(ctx context.Context, requestBody []byte) ([]byte, error)
	CreateStream(ctx context.Context, requestBody []byte) (StreamResponse, error)
}

func NormalizeConfig(cfg Config) (Config, error) {
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = BackendDisabled
	}
	switch cfg.Backend {
	case BackendDisabled:
		cfg.BaseURL = ""
		cfg.Timeout = 0
		return cfg, nil
	case BackendResponses:
	default:
		return Config{}, fmt.Errorf("unsupported image_generation backend %q", cfg.Backend)
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return Config{}, errors.New("responses.image_generation.base_url must not be empty when responses.image_generation.backend is enabled")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return cfg, nil
}

func NewProvider(cfg Config) (Provider, error) {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	switch normalized.Backend {
	case BackendDisabled:
		return nil, nil
	case BackendResponses:
		return &responsesProvider{
			client: llama.NewClient(normalized.BaseURL, normalized.Timeout),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported image_generation backend %q", normalized.Backend)
	}
}

type responsesProvider struct {
	client *llama.Client
}

func (p *responsesProvider) CheckReady(ctx context.Context) error {
	if p == nil || p.client == nil {
		return errors.New("image_generation provider is nil")
	}
	return p.client.CheckReady(ctx)
}

func (p *responsesProvider) Create(ctx context.Context, requestBody []byte) ([]byte, error) {
	if p == nil || p.client == nil {
		return nil, errors.New("image_generation provider is nil")
	}
	return p.client.CreateResponse(ctx, requestBody)
}

func (p *responsesProvider) CreateStream(ctx context.Context, requestBody []byte) (StreamResponse, error) {
	if p == nil || p.client == nil {
		return StreamResponse{}, errors.New("image_generation provider is nil")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://shim.local/v1/responses", bytes.NewReader(requestBody))
	if err != nil {
		return StreamResponse{}, fmt.Errorf("create image_generation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Proxy(ctx, req)
	if err != nil {
		return StreamResponse{}, err
	}

	return StreamResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       resp.Body,
	}, nil
}
