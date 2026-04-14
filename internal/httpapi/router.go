package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
)

type RouterDeps struct {
	Logger                                *slog.Logger
	LlamaClient                           *llama.Client
	ResponseService                       *service.ResponseService
	ConversationService                   *service.ConversationService
	Auth                                  StaticBearerAuthConfig
	RateLimit                             RateLimitConfig
	MetricsConfig                         MetricsConfig
	Metrics                               *Metrics
	ServiceLimits                         ServiceLimits
	ChatCompletionsStoreWhenOmitted       bool
	ResponsesMode                         string
	ResponsesCustomToolsMode              string
	ResponsesCodexEnableCompatibility     bool
	ResponsesCodexForceToolChoiceRequired bool
	LocalCodeInterpreter                  LocalCodeInterpreterRuntimeConfig
	RetrievalIndexBackend                 string
	RetrievalEmbedder                     retrieval.Embedder
	Store                                 *sqlite.Store
}

const readyzUpstreamTimeout = 2 * time.Second

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
	serviceLimits := normalizeServiceLimits(deps.ServiceLimits)
	retrievalGate := newConcurrencyGate("retrieval_search", serviceLimits.RetrievalMaxConcurrentSearches, deps.Metrics)
	codeInterpreterGate := newConcurrencyGate("local_code_interpreter", serviceLimits.CodeInterpreterMaxConcurrentRuns, deps.Metrics)

	proxyHandler := newProxyHandler(deps.Logger, deps.LlamaClient, deps.Store, deps.ChatCompletionsStoreWhenOmitted)
	responseHandler := newResponseHandler(
		deps.Logger,
		deps.ResponseService,
		proxyHandler,
		deps.ResponsesMode,
		deps.ResponsesCustomToolsMode,
		deps.ResponsesCodexEnableCompatibility,
		deps.ResponsesCodexForceToolChoiceRequired,
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
		if err := deps.Store.PingContext(r.Context()); err != nil {
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "sqlite is not ready", "")
			return
		}
		if deps.LlamaClient == nil {
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "llama backend is not ready", "")
			return
		}
		upstreamCtx, cancel := context.WithTimeout(r.Context(), readyzUpstreamTimeout)
		defer cancel()
		if err := deps.LlamaClient.CheckReady(upstreamCtx); err != nil {
			WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "llama backend is not ready", "")
			return
		}
		if deps.RetrievalIndexBackend == retrieval.IndexBackendSQLiteVec {
			checker, ok := deps.RetrievalEmbedder.(retrieval.ReadyChecker)
			if ok {
				retrievalCtx, cancel := context.WithTimeout(r.Context(), readyzUpstreamTimeout)
				defer cancel()
				if err := checker.CheckReady(retrievalCtx); err != nil {
					WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "retrieval embedder is not ready", "")
					return
				}
			}
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	if metricsConfig.Enabled && deps.Metrics != nil {
		mux.Handle(metricsConfig.Path, deps.Metrics.Handler())
	}
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
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
		RequestLogMiddleware(deps.Logger, deps.Metrics),
		RecoverMiddleware(deps.Logger),
		JSONBodyLimitMiddleware(serviceLimits.JSONBodyBytes),
		StaticBearerAuthMiddleware(authConfig, deps.Metrics),
		RateLimitMiddleware(rateLimitConfig, deps.Metrics, metricsConfig.Path),
		ForwardHeadersMiddleware,
	)
}

func RequestContextWithID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}
