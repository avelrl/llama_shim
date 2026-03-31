package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"llama_shim/internal/service"
)

type conversationHandler struct {
	logger  *slog.Logger
	service *service.ConversationService
}

func newConversationHandler(logger *slog.Logger, service *service.ConversationService) *conversationHandler {
	return &conversationHandler{
		logger:  logger,
		service: service,
	}
}

func (h *conversationHandler) create(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	var request CreateConversationRequest
	if err := json.Unmarshal(rawBody, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}

	conversation, err := h.service.Create(r.Context(), service.CreateConversationInput{
		Items: request.Items,
	})
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	WriteJSON(w, http.StatusOK, conversation)
}
