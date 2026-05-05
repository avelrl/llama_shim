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
	BackendComfyUI   = "comfyui"
	BackendFixture   = "fixture"
	BackendResponses = "responses"

	defaultTimeout              = 60 * time.Second
	defaultComfyUIPoll          = 500 * time.Millisecond
	defaultComfyUIMaxWait       = 120 * time.Second
	defaultComfyUIMaxImageBytes = 16 << 20
)

type Config struct {
	Backend string
	BaseURL string
	Timeout time.Duration
	ComfyUI ComfyUIConfig
}

type ComfyUIConfig struct {
	Workflow      map[string]any
	WorkflowPath  string
	OutputNodeID  string
	PollInterval  time.Duration
	MaxWait       time.Duration
	MaxImageBytes int64
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
		cfg.ComfyUI = ComfyUIConfig{}
		return cfg, nil
	case BackendFixture:
		cfg.BaseURL = ""
		cfg.Timeout = 0
		cfg.ComfyUI = ComfyUIConfig{}
		return cfg, nil
	case BackendComfyUI:
	case BackendResponses:
		cfg.ComfyUI = ComfyUIConfig{}
	default:
		return Config{}, fmt.Errorf("unsupported image_generation backend %q", cfg.Backend)
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return Config{}, errors.New("responses.image_generation.base_url must not be empty when responses.image_generation.backend requires an external service")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Backend == BackendComfyUI {
		cfg.ComfyUI.WorkflowPath = strings.TrimSpace(cfg.ComfyUI.WorkflowPath)
		cfg.ComfyUI.OutputNodeID = strings.TrimSpace(cfg.ComfyUI.OutputNodeID)
		if cfg.ComfyUI.WorkflowPath == "" && len(cfg.ComfyUI.Workflow) == 0 {
			return Config{}, errors.New("responses.image_generation.comfyui.workflow or workflow_path must be configured when responses.image_generation.backend=comfyui")
		}
		if cfg.ComfyUI.PollInterval <= 0 {
			cfg.ComfyUI.PollInterval = defaultComfyUIPoll
		}
		if cfg.ComfyUI.MaxWait <= 0 {
			cfg.ComfyUI.MaxWait = defaultComfyUIMaxWait
		}
		if cfg.ComfyUI.MaxImageBytes <= 0 {
			cfg.ComfyUI.MaxImageBytes = defaultComfyUIMaxImageBytes
		}
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
	case BackendComfyUI:
		return newComfyUIProvider(normalized)
	case BackendFixture:
		return &fixtureProvider{}, nil
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
