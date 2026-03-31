package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"llama_shim/internal/httpapi"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
)

type TestApp struct {
	Server      *httptest.Server
	Store       *sqlite.Store
	LlamaServer *httptest.Server
}

func NewTestApp(t *testing.T) *TestApp {
	t.Helper()

	llamaServer := NewFakeLlamaServer(t)
	store, err := sqlite.Open(context.Background(), TempDBPath(t))
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	llamaClient := llama.NewClient(llamaServer.URL, 200*time.Millisecond)
	responseService := service.NewResponseService(store, store, llamaClient)
	conversationService := service.NewConversationService(store)

	server := httptest.NewServer(httpapi.NewRouter(httpapi.RouterDeps{
		Logger:              logger,
		LlamaClient:         llamaClient,
		ResponseService:     responseService,
		ConversationService: conversationService,
		Store:               store,
	}))

	t.Cleanup(func() {
		server.Close()
		_ = store.Close()
		llamaServer.Close()
	})

	return &TestApp{
		Server:      server,
		Store:       store,
		LlamaServer: llamaServer,
	}
}

func (a *TestApp) Client() *http.Client {
	return a.Server.Client()
}
