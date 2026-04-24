package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"llama_shim/internal/config"
	"llama_shim/internal/httpapi"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
	"llama_shim/internal/websearch"
)

type TestApp struct {
	Server      *httptest.Server
	Store       *sqlite.Store
	LlamaServer *httptest.Server
	LlamaClient *llama.Client
	close       func()
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
	ChatCompletionsStoreWhenOmitted       *bool
	AuthMode                              string
	BearerTokens                          []string
	RateLimitEnabled                      bool
	RateLimitRequestsPerMinute            int
	RateLimitBurst                        int
	MetricsEnabled                        *bool
	MetricsPath                           string
	JSONBodyLimitBytes                    int64
	RetrievalFileUploadMaxBytes           int64
	ChatCompletionsShadowStoreMaxBytes    int64
	RetrievalMaxConcurrentSearches        int
	RetrievalMaxSearchQueries             int
	RetrievalMaxGroundingChunks           int
	CodeInterpreterMaxConcurrentRuns      int
	DBPath                                string
	LlamaBaseURL                          string
	LlamaStartupCalibrationBearerToken    string
	LlamaMaxConcurrentRequests            int
	LlamaMaxQueueWait                     time.Duration
	RetrievalConfig                       retrieval.Config
	RetrievalEmbedder                     retrieval.Embedder
	WebSearchProvider                     websearch.Provider
	ImageGenerationProvider               imagegen.Provider
	ComputerBackend                       string
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
	dbPath := options.DBPath
	if dbPath == "" {
		dbPath = TempDBPath(t)
	}
	store, err := sqlite.OpenWithOptions(context.Background(), dbPath, sqlite.OpenOptions{
		Retrieval: options.RetrievalConfig,
		Embedder:  options.RetrievalEmbedder,
	})
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	metrics := httpapi.NewMetrics()
	llamaMaxConcurrentRequests := options.LlamaMaxConcurrentRequests
	if llamaMaxConcurrentRequests <= 0 {
		llamaMaxConcurrentRequests = 4
	}
	llamaClient := llama.NewClientWithOptions(llamaBaseURL, 200*time.Millisecond, llama.ClientOptions{
		MaxConcurrentRequests:         llamaMaxConcurrentRequests,
		MaxQueueWait:                  options.LlamaMaxQueueWait,
		StartupCalibrationBearerToken: options.LlamaStartupCalibrationBearerToken,
		Observer:                      metrics,
	})
	responseService := service.NewResponseService(store, store, llamaClient)
	conversationService := service.NewConversationService(store)
	testCtx, cancel := context.WithCancel(context.Background())

	responsesMode := options.ResponsesMode
	if responsesMode == "" {
		responsesMode = config.ResponsesModePreferLocal
	}
	chatCompletionsStoreWhenOmitted := true
	if options.ChatCompletionsStoreWhenOmitted != nil {
		chatCompletionsStoreWhenOmitted = *options.ChatCompletionsStoreWhenOmitted
	}

	localCodeInterpreter := httpapi.LocalCodeInterpreterRuntimeConfig{
		Backend:                options.CodeInterpreterBackend,
		InputFileURLPolicy:     options.CodeInterpreterInputFileURLPolicy,
		InputFileURLAllowHosts: append([]string(nil), options.CodeInterpreterInputFileURLAllowHosts...),
	}
	localComputer := httpapi.LocalComputerRuntimeConfig{
		Backend: options.ComputerBackend,
	}
	metricsEnabled := true
	if options.MetricsEnabled != nil {
		metricsEnabled = *options.MetricsEnabled
	}
	httpapi.StartLocalCodeInterpreterCleanupLoop(testCtx, logger, localCodeInterpreter, store, store, options.CodeInterpreterCleanupInterval)

	server := httptest.NewServer(httpapi.NewRouter(httpapi.RouterDeps{
		Logger:              logger,
		LlamaClient:         llamaClient,
		ResponseService:     responseService,
		ConversationService: conversationService,
		Auth:                httpapi.StaticBearerAuthConfig{Mode: options.AuthMode, BearerTokens: append([]string(nil), options.BearerTokens...)},
		RateLimit:           httpapi.RateLimitConfig{Enabled: options.RateLimitEnabled, RequestsPerMinute: options.RateLimitRequestsPerMinute, Burst: options.RateLimitBurst},
		MetricsConfig:       httpapi.MetricsConfig{Enabled: metricsEnabled, Path: options.MetricsPath},
		Metrics:             metrics,
		ServiceLimits: httpapi.ServiceLimits{
			JSONBodyBytes:                    options.JSONBodyLimitBytes,
			RetrievalFileUploadBytes:         options.RetrievalFileUploadMaxBytes,
			ChatCompletionsShadowStoreBytes:  options.ChatCompletionsShadowStoreMaxBytes,
			RetrievalMaxConcurrentSearches:   options.RetrievalMaxConcurrentSearches,
			RetrievalMaxSearchQueries:        options.RetrievalMaxSearchQueries,
			RetrievalMaxGroundingChunks:      options.RetrievalMaxGroundingChunks,
			CodeInterpreterMaxConcurrentRuns: options.CodeInterpreterMaxConcurrentRuns,
		},
		ChatCompletionsStoreWhenOmitted:       chatCompletionsStoreWhenOmitted,
		ResponsesMode:                         responsesMode,
		ResponsesWebSocketEnabled:             true,
		ResponsesCustomToolsMode:              options.CustomToolsMode,
		ResponsesCodexEnableCompatibility:     options.CodexCompatibilityEnabled,
		ResponsesCodexForceToolChoiceRequired: options.ForceToolChoiceRequired,
		ResponsesWebSearchBackend:             capabilityWebSearchBackend(options),
		ResponsesImageGenerationBackend:       capabilityImageGenerationBackend(options),
		WebSearchProvider:                     options.WebSearchProvider,
		ImageGenerationProvider:               options.ImageGenerationProvider,
		LocalComputer:                         localComputer,
		LocalCodeInterpreter:                  localCodeInterpreter,
		RetrievalIndexBackend:                 options.RetrievalConfig.IndexBackend,
		RetrievalEmbedder:                     options.RetrievalEmbedder,
		Store:                                 store,
	}))

	var closeOnce sync.Once
	closeFn := func() {
		closeOnce.Do(func() {
			cancel()
			server.Close()
			_ = store.Close()
			if llamaServer != nil {
				llamaServer.Close()
			}
		})
	}
	t.Cleanup(closeFn)

	return &TestApp{
		Server:      server,
		Store:       store,
		LlamaServer: llamaServer,
		LlamaClient: llamaClient,
		close:       closeFn,
	}
}

func capabilityWebSearchBackend(options TestAppOptions) string {
	if options.WebSearchProvider != nil {
		return "searxng"
	}
	return "disabled"
}

func capabilityImageGenerationBackend(options TestAppOptions) string {
	if options.ImageGenerationProvider != nil {
		return "responses"
	}
	return "disabled"
}

func (a *TestApp) Client() *http.Client {
	return a.Server.Client()
}

func (a *TestApp) Close() {
	if a != nil && a.close != nil {
		a.close()
	}
}
