package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

const (
	defaultConversationItemsLimit = 20
	maxConversationItemsLimit     = 100
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

func (h *conversationHandler) listItems(w http.ResponseWriter, r *http.Request) {
	query, err := parseListConversationItemsQuery(r)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	query.ConversationID = r.PathValue("id")

	page, err := h.service.ListItems(r.Context(), query)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	response := listConversationItemsResponse{
		Object:  "list",
		Data:    make([]map[string]any, 0, len(page.Items)),
		HasMore: page.HasMore,
	}
	for _, item := range page.Items {
		response.Data = append(response.Data, conversationListPayload(item))
	}
	if len(page.Items) > 0 {
		firstID := payloadID(response.Data[0])
		lastID := payloadID(response.Data[len(response.Data)-1])
		response.FirstID = &firstID
		response.LastID = &lastID
	}

	WriteJSON(w, http.StatusOK, response)
}

func parseListConversationItemsQuery(r *http.Request) (domain.ListConversationItemsQuery, error) {
	values := r.URL.Query()
	if len(values["include"]) > 0 {
		return domain.ListConversationItemsQuery{}, domain.NewValidationError("include", "include is not supported yet")
	}

	query := domain.ListConversationItemsQuery{
		After: values.Get("after"),
		Limit: defaultConversationItemsLimit,
		Order: domain.ConversationItemOrderDesc,
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxConversationItemsLimit {
			return domain.ListConversationItemsQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
		}
		query.Limit = limit
	}

	if rawOrder := values.Get("order"); rawOrder != "" {
		switch rawOrder {
		case domain.ConversationItemOrderAsc, domain.ConversationItemOrderDesc:
			query.Order = rawOrder
		default:
			return domain.ListConversationItemsQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
	}

	return query, nil
}

func conversationListPayload(item domain.ConversationItem) map[string]any {
	payload := item.Item.Map()
	if strings.TrimSpace(asString(payload["id"])) == "" {
		payload["id"] = item.ID
	}
	return payload
}

func payloadID(payload map[string]any) string {
	if id := strings.TrimSpace(asString(payload["id"])); id != "" {
		return id
	}
	return ""
}
