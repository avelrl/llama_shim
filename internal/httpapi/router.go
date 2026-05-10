package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"llama_shim/internal/config"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/service"
	"llama_shim/internal/storage"
	"llama_shim/internal/upstreamcompat"
	"llama_shim/internal/websearch"
)

type RouterDeps struct {
	Logger                                   *slog.Logger
	LlamaClient                              *llama.Client
	LlamaProviders                           []config.LlamaProvider
	LlamaReadinessBearerToken                string
	ResponseService                          *service.ResponseService
	ConversationService                      *service.ConversationService
	Auth                                     StaticBearerAuthConfig
	RateLimit                                RateLimitConfig
	MetricsConfig                            MetricsConfig
	DebugTrace                               DebugTraceConfig
	UI                                       UIConfig
	Metrics                                  *Metrics
	ServiceLimits                            ServiceLimits
	StorageBackend                           string
	ChatCompletionsStoreWhenOmitted          bool
	ChatCompletionsUpstreamCompatibility     []upstreamcompat.ChatCompletionRule
	ResponsesMode                            string
	ResponsesUpstreamTransport               string
	ResponsesWebSocketEnabled                bool
	ResponsesCustomToolsMode                 string
	ResponsesConstrainedDecodingBackend      string
	ResponsesUpstreamToolCompatibility       []UpstreamToolCompatibilityRule
	ResponsesCodexEnableCompatibility        bool
	ResponsesCodexUpstreamInputCompatibility []CodexUpstreamInputCompatibilityRule
	ResponsesCodexModelMetadata              []CodexModelMetadata
	ResponsesCompactionBackend               string
	ResponsesCompactionModel                 string
	ResponsesCompactionRetainedItems         int
	ResponsesCompactionMaxInputRunes         int
	ResponsesMemoryBackend                   string
	ResponsesMemoryInject                    bool
	ResponsesMemoryMaxNotes                  int
	ResponsesMemoryMaxNoteBytes              int64
	ResponsesMemoryMaxContextBytes           int64
	ResponsesMemoryMetadataNamespace         string
	ResponsesWebSearchBackend                string
	ResponsesImageGenerationBackend          string
	WebSearchProvider                        websearch.Provider
	ImageGenerationProvider                  imagegen.Provider
	LocalComputer                            LocalComputerRuntimeConfig
	LocalCodeInterpreter                     LocalCodeInterpreterRuntimeConfig
	RetrievalIndexBackend                    string
	RetrievalEmbedderBackend                 string
	RetrievalEmbedder                        retrieval.Embedder
	Store                                    storage.Store
}

const readyzUpstreamTimeout = 2 * time.Second
const readyzProviderTimeout = 5 * time.Second
const modelsUpstreamTimeout = 5 * time.Second

func NewRouter(deps RouterDeps) http.Handler {
	authConfig, err := normalizeStaticBearerAuthConfig(deps.Auth)
	if err != nil {
		panic(err)
	}
	rateLimitConfig, err := normalizeRateLimitConfig(deps.RateLimit)
	if err != nil {
		panic(err)
	}
	metricsConfig := normalizeMetricsConfig(deps.MetricsConfig)
	debugTraceConfig := normalizeDebugTraceConfig(deps.DebugTrace)
	deps.DebugTrace = debugTraceConfig
	uiConfig := normalizeUIConfig(deps.UI)
	deps.UI = uiConfig
	debugTraceStore := newDebugTraceStoreForConfig(debugTraceConfig)
	serviceLimits := normalizeServiceLimits(deps.ServiceLimits)
	retrievalGate := newConcurrencyGate("retrieval_search", serviceLimits.RetrievalMaxConcurrentSearches, deps.Metrics)
	codeInterpreterGate := newConcurrencyGate("local_code_interpreter", serviceLimits.CodeInterpreterMaxConcurrentRuns, deps.Metrics)
	upstreamProviderResolver := newUpstreamProviderResolver(deps.LlamaProviders)

	proxyHandler := newProxyHandler(deps.Logger, deps.LlamaClient, deps.Store, serviceLimits, deps.ChatCompletionsStoreWhenOmitted, deps.ChatCompletionsUpstreamCompatibility, upstreamProviderResolver)
	responseHandler := newResponseHandler(
		deps.Logger,
		deps.ResponseService,
		proxyHandler,
		deps.ResponsesMode,
		deps.ResponsesUpstreamTransport,
		deps.ResponsesCustomToolsMode,
		deps.ResponsesConstrainedDecodingBackend,
		deps.ResponsesUpstreamToolCompatibility,
		deps.ResponsesCodexEnableCompatibility,
		deps.ResponsesCodexUpstreamInputCompatibility,
		deps.WebSearchProvider,
		deps.ImageGenerationProvider,
		deps.LocalComputer,
		deps.LocalCodeInterpreter,
		deps.Store,
		deps.Store,
		deps.Metrics,
		serviceLimits,
		retrievalGate,
		codeInterpreterGate,
	)
	conversationHandler := newConversationHandler(deps.Logger, deps.ConversationService)
	retrievalHandler := newRetrievalHandler(deps.Logger, deps.Store, deps.Metrics, serviceLimits, retrievalGate)
	containerHandler := newContainerHandler(deps.Logger, deps.LocalCodeInterpreter, deps.Store, deps.Store, serviceLimits)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		if deps.Store == nil {
			observeReadinessProbeOutcome(deps.Metrics, "readyz", "storage", "unready")
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "storage backend is not ready", "")
			return
		}
		probeStart := time.Now()
		if err := deps.Store.PingContext(r.Context()); err != nil {
			observeReadinessProbe(deps.Metrics, "readyz", "storage", probeStart, err)
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "storage backend is not ready", "")
			return
		}
		observeReadinessProbe(deps.Metrics, "readyz", "storage", probeStart, nil)
		if deps.LlamaClient == nil {
			observeReadinessProbeOutcome(deps.Metrics, "readyz", "llama", "unready")
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "llama backend is not ready", "")
			return
		}
		upstreamTimeout := readyzUpstreamTimeout
		if upstreamProviderResolver.Enabled() {
			upstreamTimeout = readyzProviderTimeout
		}
		upstreamCtx, cancel := context.WithTimeout(r.Context(), upstreamTimeout)
		probeStart = time.Now()
		var err error
		if upstreamProviderResolver.Enabled() {
			err = upstreamProviderResolver.CheckReady(upstreamCtx, deps.LlamaClient)
		} else {
			err = deps.LlamaClient.CheckReadyWithBearerToken(upstreamCtx, deps.LlamaReadinessBearerToken)
		}
		if err != nil {
			cancel()
			observeReadinessProbe(deps.Metrics, "readyz", "llama", probeStart, err)
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "llama backend is not ready", "")
			return
		}
		cancel()
		observeReadinessProbe(deps.Metrics, "readyz", "llama", probeStart, nil)
		if deps.RetrievalIndexBackend == retrieval.IndexBackendSQLiteVec || deps.RetrievalIndexBackend == retrieval.IndexBackendPGVector {
			checker, ok := deps.RetrievalEmbedder.(retrieval.ReadyChecker)
			if ok {
				retrievalCtx, cancel := context.WithTimeout(r.Context(), readyzUpstreamTimeout)
				probeStart = time.Now()
				if err := checker.CheckReady(retrievalCtx); err != nil {
					cancel()
					observeReadinessProbe(deps.Metrics, "readyz", "retrieval_embedder", probeStart, err)
					WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "retrieval embedder is not ready", "")
					return
				}
				cancel()
				observeReadinessProbe(deps.Metrics, "readyz", "retrieval_embedder", probeStart, nil)
			}
		}
		checker, ok := deps.WebSearchProvider.(websearch.ReadyChecker)
		if ok {
			webSearchCtx, cancel := context.WithTimeout(r.Context(), readyzUpstreamTimeout)
			probeStart = time.Now()
			if err := checker.CheckReady(webSearchCtx); err != nil {
				cancel()
				observeReadinessProbe(deps.Metrics, "readyz", "web_search_backend", probeStart, err)
				WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "web search backend is not ready", "")
				return
			}
			cancel()
			observeReadinessProbe(deps.Metrics, "readyz", "web_search_backend", probeStart, nil)
		}
		if deps.ImageGenerationProvider != nil {
			imageGenerationCtx, cancel := context.WithTimeout(r.Context(), readyzUpstreamTimeout)
			probeStart = time.Now()
			if err := deps.ImageGenerationProvider.CheckReady(imageGenerationCtx); err != nil {
				cancel()
				observeReadinessProbe(deps.Metrics, "readyz", "image_generation_backend", probeStart, err)
				WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "image generation backend is not ready", "")
				return
			}
			cancel()
			observeReadinessProbe(deps.Metrics, "readyz", "image_generation_backend", probeStart, nil)
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/debug/capabilities", capabilityHandler(deps))
	mux.HandleFunc("/debug/traces", debugTraceListHandler(debugTraceStore))
	mux.HandleFunc("/debug/traces/", debugTraceGetHandler(debugTraceStore))
	mountOperatorUI(mux, uiConfig)
	if metricsConfig.Enabled && deps.Metrics != nil {
		mux.Handle(metricsConfig.Path, deps.Metrics.Handler())
	}
	mux.HandleFunc("/api/codex/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		if writeCodexModels(w, deps.ResponsesCodexModelMetadata) {
			return
		}
		proxyHandler.forward(w, r)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("client_version") != "" {
			if writeCodexModels(w, deps.ResponsesCodexModelMetadata) {
				return
			}
		}
		if r.Method == http.MethodGet && upstreamProviderResolver.Enabled() {
			modelsCtx, cancel := context.WithTimeout(r.Context(), modelsUpstreamTimeout)
			defer cancel()
			upstreamProviderResolver.WriteModels(modelsCtx, w, deps.LlamaClient)
			return
		}
		proxyHandler.forward(w, r)
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && isWebSocketUpgrade(r) {
			if !deps.ResponsesWebSocketEnabled {
				WriteError(w, http.StatusNotFound, "not_found_error", "Responses WebSocket transport is disabled", "")
				return
			}
			responseHandler.websocket(w, r)
			return
		}
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.create(w, r)
	})
	mux.HandleFunc("/v1/responses/input_tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.inputTokens(w, r)
	})
	mux.HandleFunc("/v1/responses/compact", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.compact(w, r)
	})
	mux.HandleFunc("/v1/responses/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			responseHandler.delete(w, r)
		case http.MethodGet:
			responseHandler.get(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/responses/{id}/input_items", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.getInputItems(w, r)
	})
	mux.HandleFunc("/v1/responses/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.cancel(w, r)
	})
	mux.HandleFunc("/v1/conversations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		conversationHandler.create(w, r)
	})
	mux.HandleFunc("/v1/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		conversationHandler.get(w, r)
	})
	mux.HandleFunc("/v1/conversations/{id}/items", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			conversationHandler.listItems(w, r)
		case http.MethodPost:
			conversationHandler.appendItem(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/conversations/{id}/items/{item_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			conversationHandler.deleteItem(w, r)
		case http.MethodGet:
			conversationHandler.getItem(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			proxyHandler.listStoredChatCompletions(w, r)
		case http.MethodPost:
			proxyHandler.forwardChatCompletions(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/chat/completions/{completion_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			proxyHandler.getStoredChatCompletion(w, r)
		case http.MethodPost:
			proxyHandler.updateStoredChatCompletion(w, r)
		case http.MethodDelete:
			proxyHandler.deleteStoredChatCompletion(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/chat/completions/{completion_id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		proxyHandler.listStoredChatCompletionMessages(w, r)
	})
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			retrievalHandler.listFiles(w, r)
		case http.MethodPost:
			retrievalHandler.createFile(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/files/{file_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			retrievalHandler.getFile(w, r)
		case http.MethodDelete:
			retrievalHandler.deleteFile(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/files/{file_id}/content", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		retrievalHandler.getFileContent(w, r)
	})
	mux.HandleFunc("/v1/vector_stores", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			retrievalHandler.listVectorStores(w, r)
		case http.MethodPost:
			retrievalHandler.createVectorStore(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/vector_stores/{vector_store_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			retrievalHandler.getVectorStore(w, r)
		case http.MethodDelete:
			retrievalHandler.deleteVectorStore(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/vector_stores/{vector_store_id}/files", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			retrievalHandler.listVectorStoreFiles(w, r)
		case http.MethodPost:
			retrievalHandler.createVectorStoreFile(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/vector_stores/{vector_store_id}/files/{file_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			retrievalHandler.getVectorStoreFile(w, r)
		case http.MethodDelete:
			retrievalHandler.deleteVectorStoreFile(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/vector_stores/{vector_store_id}/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		retrievalHandler.searchVectorStore(w, r)
	})
	mux.HandleFunc("/v1/containers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			containerHandler.listContainers(w, r)
		case http.MethodPost:
			containerHandler.createContainer(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/containers/{container_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			containerHandler.getContainer(w, r)
		case http.MethodDelete:
			containerHandler.deleteContainer(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/containers/{container_id}/files", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			containerHandler.listContainerFiles(w, r)
		case http.MethodPost:
			containerHandler.createContainerFile(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/containers/{container_id}/files/{file_id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			containerHandler.getContainerFile(w, r)
		case http.MethodDelete:
			containerHandler.deleteContainerFile(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
		}
	})
	mux.HandleFunc("/v1/containers/{container_id}/files/{file_id}/content", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		containerHandler.getContainerFileContent(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxyHandler.forward(w, r)
	})

	return Chain(
		mux,
		RequestIDMiddleware,
		RequestLogMiddleware(deps.Logger, deps.Metrics, debugTraceStore),
		RecoverMiddleware(deps.Logger),
		JSONBodyLimitMiddleware(serviceLimits.JSONBodyBytes),
		StaticBearerAuthMiddleware(authConfig, deps.Metrics, uiConfig.publicStaticPath),
		RateLimitMiddleware(rateLimitConfig, deps.Metrics, metricsConfig.Path),
		ForwardHeadersMiddleware,
	)
}

func observeReadinessProbe(metrics *Metrics, source string, component string, start time.Time, err error) {
	outcome := "ready"
	if err != nil {
		outcome = "unready"
	}
	metrics.ObserveReadinessProbe(source, component, outcome, time.Since(start))
}

func observeReadinessProbeOutcome(metrics *Metrics, source string, component string, outcome string) {
	metrics.ObserveReadinessProbe(source, component, outcome, 0)
}

func RequestContextWithID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}
