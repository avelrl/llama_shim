package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

const (
	defaultConversationItemsLimit = 20
	maxConversationItemsLimit     = 100
)

var supportedConversationItemIncludes = map[string]struct{}{
	"computer_call_output.output.image_url": {},
	"code_interpreter_call.outputs":         {},
	"file_search_call.results":              {},
	"web_search_call.action.sources":        {},
}

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
	if trimmed := bytes.TrimSpace(rawBody); len(trimmed) > 0 {
		if err := json.Unmarshal(rawBody, &request); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
			return
		}
	}

	conversation, err := h.service.Create(r.Context(), service.CreateConversationInput{
		Items:    request.Items,
		Metadata: request.Metadata,
	})
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	payload, err := conversationPayload(conversation)
	if err != nil {
		status, mapped := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: mapped})
		return
	}

	WriteJSON(w, http.StatusOK, payload)
}

func (h *conversationHandler) get(w http.ResponseWriter, r *http.Request) {
	conversation, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	payload, err := conversationPayload(conversation)
	if err != nil {
		status, mapped := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: mapped})
		return
	}

	WriteJSON(w, http.StatusOK, payload)
}

func (h *conversationHandler) appendItem(w http.ResponseWriter, r *http.Request) {
	if _, err := parseConversationItemIncludes(r.URL.Query()); err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	rawItems, err := parseAppendConversationItemsBody(rawBody)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	items, err := h.service.AppendItems(r.Context(), r.PathValue("id"), rawItems)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	WriteJSON(w, http.StatusOK, conversationItemsPayload(items))
}

func (h *conversationHandler) getItem(w http.ResponseWriter, r *http.Request) {
	if _, err := parseConversationItemIncludes(r.URL.Query()); err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	item, err := h.service.GetItem(r.Context(), r.PathValue("id"), r.PathValue("item_id"))
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	WriteJSON(w, http.StatusOK, conversationItemPayload(item))
}

func (h *conversationHandler) deleteItem(w http.ResponseWriter, r *http.Request) {
	conversation, err := h.service.DeleteItem(r.Context(), r.PathValue("id"), r.PathValue("item_id"))
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	payload, err := conversationPayload(conversation)
	if err != nil {
		status, mapped := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: mapped})
		return
	}

	WriteJSON(w, http.StatusOK, payload)
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

	WriteJSON(w, http.StatusOK, conversationItemsPayload(page.Items, withHasMore(page.HasMore)))
}

func parseListConversationItemsQuery(r *http.Request) (domain.ListConversationItemsQuery, error) {
	values := r.URL.Query()
	includes, err := parseConversationItemIncludes(values)
	if err != nil {
		return domain.ListConversationItemsQuery{}, err
	}

	query := domain.ListConversationItemsQuery{
		After:   values.Get("after"),
		Include: includes,
		Limit:   defaultConversationItemsLimit,
		Order:   domain.ConversationItemOrderDesc,
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

func parseConversationItemIncludes(values url.Values) ([]string, error) {
	rawIncludes := values["include"]
	if len(rawIncludes) == 0 {
		return nil, nil
	}

	includes := make([]string, 0, len(rawIncludes))
	for _, rawInclude := range rawIncludes {
		for _, part := range strings.Split(rawInclude, ",") {
			include := strings.TrimSpace(part)
			if include == "" {
				continue
			}
			if _, ok := supportedConversationItemIncludes[include]; !ok {
				return nil, domain.NewValidationError("include", "unsupported include value")
			}
			includes = append(includes, include)
		}
	}

	if len(includes) == 0 {
		return nil, nil
	}
	return includes, nil
}

func parseAppendConversationItemsBody(rawBody []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, domain.NewValidationError("items", "items is required")
	}

	switch trimmed[0] {
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, domain.NewValidationError("items", "malformed JSON body")
		}
		if len(items) == 0 {
			return nil, domain.NewValidationError("items", "items must not be empty")
		}
		return items, nil
	case '{':
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &payload); err != nil {
			return nil, domain.NewValidationError("items", "malformed JSON body")
		}
		if rawItems, ok := payload["items"]; ok {
			var items []json.RawMessage
			if err := json.Unmarshal(rawItems, &items); err != nil {
				return nil, domain.NewValidationError("items", "items must be an array")
			}
			if len(items) == 0 {
				return nil, domain.NewValidationError("items", "items must not be empty")
			}
			return items, nil
		}
		if rawItem, ok := payload["item"]; ok {
			return []json.RawMessage{rawItem}, nil
		}
		return []json.RawMessage{trimmed}, nil
	default:
		return nil, domain.NewValidationError("items", "items must be JSON objects")
	}
}

type conversationItemsResponseOption func(*listConversationItemsResponse)

func withHasMore(hasMore bool) conversationItemsResponseOption {
	return func(response *listConversationItemsResponse) {
		response.HasMore = hasMore
	}
}

func conversationItemsPayload(items []domain.ConversationItem, opts ...conversationItemsResponseOption) listConversationItemsResponse {
	response := listConversationItemsResponse{
		Object: "list",
		Data:   make([]map[string]any, 0, len(items)),
	}
	for _, item := range items {
		response.Data = append(response.Data, conversationItemPayload(item))
	}
	if len(response.Data) > 0 {
		firstID := payloadID(response.Data[0])
		lastID := payloadID(response.Data[len(response.Data)-1])
		response.FirstID = &firstID
		response.LastID = &lastID
	}
	for _, opt := range opts {
		opt(&response)
	}
	return response
}

func conversationItemPayload(item domain.ConversationItem) map[string]any {
	payload := item.Item.Map()
	if strings.TrimSpace(asString(payload["id"])) == "" {
		payload["id"] = item.ID
	}
	return payload
}

func conversationPayload(conversation domain.Conversation) (conversationResource, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, conversation.CreatedAt)
	if err != nil {
		return conversationResource{}, fmt.Errorf("parse conversation created_at: %w", err)
	}

	metadata := make(map[string]string, len(conversation.Metadata))
	for key, value := range conversation.Metadata {
		metadata[key] = value
	}

	return conversationResource{
		ID:        conversation.ID,
		Object:    conversation.Object,
		CreatedAt: createdAt.Unix(),
		Metadata:  metadata,
	}, nil
}

func payloadID(payload map[string]any) string {
	if id := strings.TrimSpace(asString(payload["id"])); id != "" {
		return id
	}
	return ""
}
