package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"llama_shim/internal/llama"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
)

type RouterDeps struct {
	Logger                                *slog.Logger
	LlamaClient                           *llama.Client
	ResponseService                       *service.ResponseService
	ConversationService                   *service.ConversationService
	ResponsesCustomToolsMode              string
	ResponsesCodexEnableCompatibility     bool
	ResponsesCodexForceToolChoiceRequired bool
	Store                                 *sqlite.Store
}

func NewRouter(deps RouterDeps) http.Handler {
	proxyHandler := newProxyHandler(deps.Logger, deps.LlamaClient)
	responseHandler := newResponseHandler(
		deps.Logger,
		deps.ResponseService,
		proxyHandler,
		deps.ResponsesCustomToolsMode,
		deps.ResponsesCodexEnableCompatibility,
		deps.ResponsesCodexForceToolChoiceRequired,
	)
	conversationHandler := newConversationHandler(deps.Logger, deps.ConversationService)

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
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.create(w, r)
	})
	mux.HandleFunc("/v1/responses/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.get(w, r)
	})
	mux.HandleFunc("/v1/responses/{id}/input_items", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		responseHandler.getInputItems(w, r)
	})
	mux.HandleFunc("/v1/conversations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		conversationHandler.create(w, r)
	})
	mux.HandleFunc("/v1/conversations/{id}/items", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		conversationHandler.listItems(w, r)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		proxyHandler.forwardChatCompletions(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxyHandler.forward(w, r)
	})

	return Chain(
		mux,
		RequestIDMiddleware,
		ForwardHeadersMiddleware,
		RecoverMiddleware(deps.Logger),
		RequestLogMiddleware(deps.Logger),
	)
}

func RequestContextWithID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}
