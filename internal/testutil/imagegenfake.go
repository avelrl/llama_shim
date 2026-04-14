package testutil

import (
	"bytes"
	"context"
	"io"
	"sync"

	"llama_shim/internal/imagegen"
)

type FakeImageGenerationProvider struct {
	ReadyErr error

	CreateFunc       func(context.Context, []byte) ([]byte, error)
	CreateStreamFunc func(context.Context, []byte) (imagegen.StreamResponse, error)

	mu                 sync.Mutex
	CreateBodies       [][]byte
	CreateStreamBodies [][]byte
}

func (p *FakeImageGenerationProvider) CheckReady(context.Context) error {
	return p.ReadyErr
}

func (p *FakeImageGenerationProvider) Create(ctx context.Context, requestBody []byte) ([]byte, error) {
	p.mu.Lock()
	p.CreateBodies = append(p.CreateBodies, bytes.Clone(requestBody))
	p.mu.Unlock()
	if p.CreateFunc != nil {
		return p.CreateFunc(ctx, requestBody)
	}
	return nil, nil
}

func (p *FakeImageGenerationProvider) CreateStream(ctx context.Context, requestBody []byte) (imagegen.StreamResponse, error) {
	p.mu.Lock()
	p.CreateStreamBodies = append(p.CreateStreamBodies, bytes.Clone(requestBody))
	p.mu.Unlock()
	if p.CreateStreamFunc != nil {
		return p.CreateStreamFunc(ctx, requestBody)
	}
	return imagegen.StreamResponse{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}, nil
}
