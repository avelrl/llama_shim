package testutil

import (
	"context"
	"sync"

	"llama_shim/internal/websearch"
)

type FakeWebSearchProvider struct {
	ReadyErr error

	SearchResponses map[string]websearch.SearchResponse
	Pages           map[string]websearch.Page

	SearchFunc   func(context.Context, websearch.SearchRequest) (websearch.SearchResponse, error)
	OpenPageFunc func(context.Context, string) (websearch.Page, error)

	mu            sync.Mutex
	SearchCalls   []websearch.SearchRequest
	OpenPageCalls []string
}

func (p *FakeWebSearchProvider) CheckReady(context.Context) error {
	return p.ReadyErr
}

func (p *FakeWebSearchProvider) Search(ctx context.Context, request websearch.SearchRequest) (websearch.SearchResponse, error) {
	p.mu.Lock()
	p.SearchCalls = append(p.SearchCalls, request)
	p.mu.Unlock()

	if p.SearchFunc != nil {
		return p.SearchFunc(ctx, request)
	}
	if p.SearchResponses != nil {
		if response, ok := p.SearchResponses[request.Query]; ok {
			return response, nil
		}
	}
	return websearch.SearchResponse{}, nil
}

func (p *FakeWebSearchProvider) OpenPage(ctx context.Context, rawURL string) (websearch.Page, error) {
	p.mu.Lock()
	p.OpenPageCalls = append(p.OpenPageCalls, rawURL)
	p.mu.Unlock()

	if p.OpenPageFunc != nil {
		return p.OpenPageFunc(ctx, rawURL)
	}
	if p.Pages != nil {
		if page, ok := p.Pages[rawURL]; ok {
			return page, nil
		}
	}
	return websearch.Page{}, nil
}
