package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"llama_shim/internal/config"
	"llama_shim/internal/httpapi"
	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
)

type TestApp struct {
	Server      *httptest.Server
	Store       *sqlite.Store
	LlamaServer *httptest.Server
}

func NewTestApp(t *testing.T) *TestApp {
	return NewTestAppWithOptions(t, TestAppOptions{})
}

func NewTestAppWithCustomToolsMode(t *testing.T, customToolsMode string) *TestApp {
	return NewTestAppWithOptions(t, TestAppOptions{CustomToolsMode: customToolsMode})
}

func NewTestAppWithResponsesMode(t *testing.T, responsesMode string) *TestApp {
	return NewTestAppWithOptions(t, TestAppOptions{ResponsesMode: responsesMode})
}

func NewTestAppWithCodexSettings(t *testing.T, customToolsMode string, codexCompatibilityEnabled bool, forceToolChoiceRequired bool) *TestApp {
	return NewTestAppWithOptions(t, TestAppOptions{
		CustomToolsMode:           customToolsMode,
		CodexCompatibilityEnabled: codexCompatibilityEnabled,
		ForceToolChoiceRequired:   forceToolChoiceRequired,
	})
}

func NewTestAppWithResponsesAndCodexSettings(t *testing.T, responsesMode string, customToolsMode string, codexCompatibilityEnabled bool, forceToolChoiceRequired bool) *TestApp {
	return NewTestAppWithOptions(t, TestAppOptions{
		ResponsesMode:             responsesMode,
		CustomToolsMode:           customToolsMode,
		CodexCompatibilityEnabled: codexCompatibilityEnabled,
		ForceToolChoiceRequired:   forceToolChoiceRequired,
	})
}

type TestAppOptions struct {
	ResponsesMode                         string
	CustomToolsMode                       string
	CodexCompatibilityEnabled             bool
	ForceToolChoiceRequired               bool
	LlamaBaseURL                          string
	RetrievalConfig                       retrieval.Config
	RetrievalEmbedder                     retrieval.Embedder
	CodeInterpreterBackend                sandbox.Backend
	CodeInterpreterInputFileURLPolicy     string
	CodeInterpreterInputFileURLAllowHosts []string
	CodeInterpreterCleanupInterval        time.Duration
}

func NewTestAppWithOptions(t *testing.T, options TestAppOptions) *TestApp {
	t.Helper()

	var llamaServer *httptest.Server
	llamaBaseURL := options.LlamaBaseURL
	if llamaBaseURL == "" {
		llamaServer = NewFakeLlamaServer(t)
		llamaBaseURL = llamaServer.URL
	}
	store, err := sqlite.OpenWithOptions(context.Background(), TempDBPath(t), sqlite.OpenOptions{
		Retrieval: options.RetrievalConfig,
		Embedder:  options.RetrievalEmbedder,
	})
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	llamaClient := llama.NewClient(llamaBaseURL, 200*time.Millisecond)
	responseService := service.NewResponseService(store, store, llamaClient)
	conversationService := service.NewConversationService(store)
	testCtx, cancel := context.WithCancel(context.Background())

	responsesMode := options.ResponsesMode
	if responsesMode == "" {
		responsesMode = config.ResponsesModePreferLocal
	}

	localCodeInterpreter := httpapi.LocalCodeInterpreterRuntimeConfig{
		Backend:                options.CodeInterpreterBackend,
		InputFileURLPolicy:     options.CodeInterpreterInputFileURLPolicy,
		InputFileURLAllowHosts: append([]string(nil), options.CodeInterpreterInputFileURLAllowHosts...),
	}
	httpapi.StartLocalCodeInterpreterCleanupLoop(testCtx, logger, localCodeInterpreter, store, store, options.CodeInterpreterCleanupInterval)

	server := httptest.NewServer(httpapi.NewRouter(httpapi.RouterDeps{
		Logger:                                logger,
		LlamaClient:                           llamaClient,
		ResponseService:                       responseService,
		ConversationService:                   conversationService,
		ResponsesMode:                         responsesMode,
		ResponsesCustomToolsMode:              options.CustomToolsMode,
		ResponsesCodexEnableCompatibility:     options.CodexCompatibilityEnabled,
		ResponsesCodexForceToolChoiceRequired: options.ForceToolChoiceRequired,
		LocalCodeInterpreter:                  localCodeInterpreter,
		RetrievalIndexBackend:                 options.RetrievalConfig.IndexBackend,
		RetrievalEmbedder:                     options.RetrievalEmbedder,
		Store:                                 store,
	}))

	t.Cleanup(func() {
		cancel()
		server.Close()
		_ = store.Close()
		if llamaServer != nil {
			llamaServer.Close()
		}
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
