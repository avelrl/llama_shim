package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/sqlite"
)

func (h *proxyHandler) forwardChatCompletions(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	if err := validateChatCompletionsRequest(rawBody); err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	cloned := r.Clone(r.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(rawBody))
	cloned.ContentLength = int64(len(rawBody))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rawBody)), nil
	}
	if cloned.Header.Get("X-Request-Id") == "" {
		cloned.Header.Set("X-Request-Id", RequestIDFromContext(cloned.Context()))
	}

	response, err := h.client.Proxy(cloned.Context(), cloned)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer response.Body.Close()

	isSSE := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
	if !isSSE {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
			return
		}
		originalBody := body

		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if sanitized, err := sanitizeChatCompletionJSONBody(body); err != nil {
				h.logger.WarnContext(r.Context(), "chat completion response sanitize failed",
					"request_id", RequestIDFromContext(r.Context()),
					"err", err,
				)
			} else {
				body = sanitized
			}
			if shouldStore, err := shouldShadowStoreChatCompletion(rawBody); err != nil {
				h.logger.WarnContext(r.Context(), "chat completion shadow store eligibility check failed",
					"request_id", RequestIDFromContext(r.Context()),
					"err", err,
				)
			} else if shouldStore {
				if err := h.shadowStoreChatCompletion(r.Context(), rawBody, body); err != nil {
					h.logger.ErrorContext(r.Context(), "chat completion shadow store failed",
						"request_id", RequestIDFromContext(r.Context()),
						"err", err,
					)
				}
			}
		} else if canonical, ok, err := canonicalizeAPIErrorBody(response.StatusCode, body); err == nil && ok {
			body = canonical
		}

		copyResponseHeaders(w.Header(), response.Header)
		if !bytes.Equal(body, originalBody) {
			w.Header().Del("Content-Length")
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return
	}

	copyResponseHeaders(w.Header(), response.Header)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	disableWriteDeadline(w)
	w.WriteHeader(response.StatusCode)

	if err := proxyChatCompletionStream(w, response.Body); err != nil && !shouldIgnoreStreamProxyError(err) {
		h.logger.WarnContext(r.Context(), "chat completion stream proxy failed",
			"request_id", RequestIDFromContext(r.Context()),
			"err", err,
		)
	}
}

type chatCompletionsListResponse struct {
	Object  string            `json:"object"`
	Data    []json.RawMessage `json:"data"`
	FirstID *string           `json:"first_id"`
	LastID  *string           `json:"last_id"`
	HasMore bool              `json:"has_more"`
}

type chatCompletionMessagesListResponse struct {
	Object  string           `json:"object"`
	Data    []map[string]any `json:"data"`
	FirstID *string          `json:"first_id"`
	LastID  *string          `json:"last_id"`
	HasMore bool             `json:"has_more"`
}

type listStoredChatCompletionMessagesQuery struct {
	After string
	Limit int
	Order string
}

func (h *proxyHandler) listStoredChatCompletions(w http.ResponseWriter, r *http.Request) {
	query, err := parseListStoredChatCompletionsQuery(r)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	page, err := h.store.ListChatCompletions(r.Context(), query)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	data := make([]json.RawMessage, 0, len(page.Completions))
	for _, completion := range page.Completions {
		data = append(data, json.RawMessage(completion.ResponseJSON))
	}

	WriteJSON(w, http.StatusOK, chatCompletionsListResponse{
		Object:  "list",
		Data:    data,
		FirstID: firstRawID(data),
		LastID:  lastRawID(data),
		HasMore: page.HasMore,
	})
}

func (h *proxyHandler) getStoredChatCompletion(w http.ResponseWriter, r *http.Request) {
	completion, err := h.store.GetChatCompletion(r.Context(), r.PathValue("completion_id"))
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, completion.ResponseJSON)
}

func (h *proxyHandler) listStoredChatCompletionMessages(w http.ResponseWriter, r *http.Request) {
	query, err := parseListStoredChatCompletionMessagesQuery(r)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	completion, err := h.store.GetChatCompletion(r.Context(), r.PathValue("completion_id"))
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	messages, err := storedChatCompletionMessagesPage(completion, query)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	WriteJSON(w, http.StatusOK, chatCompletionMessagesListResponse{
		Object:  "list",
		Data:    messages.Data,
		FirstID: firstMapID(messages.Data),
		LastID:  lastMapID(messages.Data),
		HasMore: messages.HasMore,
	})
}

func shouldShadowStoreChatCompletion(rawBody []byte) (bool, error) {
	var request struct {
		Store  *bool `json:"store,omitempty"`
		Stream *bool `json:"stream,omitempty"`
	}
	if err := json.Unmarshal(rawBody, &request); err != nil {
		return false, err
	}
	if request.Stream != nil && *request.Stream {
		return false, nil
	}
	return request.Store != nil && *request.Store, nil
}

func (h *proxyHandler) shadowStoreChatCompletion(ctx context.Context, requestBody []byte, responseBody []byte) error {
	if h.store == nil {
		return nil
	}

	var request struct {
		Model    string          `json:"model"`
		Metadata json.RawMessage `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(requestBody, &request); err != nil {
		return fmt.Errorf("decode chat completion request: %w", err)
	}
	metadata, err := domain.NormalizeResponseMetadata(request.Metadata)
	if err != nil {
		return fmt.Errorf("normalize chat completion metadata: %w", err)
	}

	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return fmt.Errorf("decode chat completion response: %w", err)
	}
	if strings.TrimSpace(response.ID) == "" {
		return fmt.Errorf("chat completion response id missing")
	}
	if response.Created == 0 {
		return fmt.Errorf("chat completion response created missing")
	}

	requestJSON, err := domain.CompactJSON(requestBody)
	if err != nil {
		return fmt.Errorf("compact chat completion request: %w", err)
	}
	responseJSON, err := domain.CompactJSON(responseBody)
	if err != nil {
		return fmt.Errorf("compact chat completion response: %w", err)
	}

	model := strings.TrimSpace(response.Model)
	if model == "" {
		model = strings.TrimSpace(request.Model)
	}

	return h.store.SaveChatCompletion(ctx, domain.StoredChatCompletion{
		ID:           response.ID,
		Model:        model,
		Metadata:     metadata,
		RequestJSON:  requestJSON,
		ResponseJSON: responseJSON,
		CreatedAt:    response.Created,
	})
}

func parseListStoredChatCompletionsQuery(r *http.Request) (domain.ListStoredChatCompletionsQuery, error) {
	values := r.URL.Query()
	metadata, err := parseChatCompletionMetadataFilter(values)
	if err != nil {
		return domain.ListStoredChatCompletionsQuery{}, err
	}

	query := domain.ListStoredChatCompletionsQuery{
		Model:    strings.TrimSpace(values.Get("model")),
		Metadata: metadata,
		After:    strings.TrimSpace(values.Get("after")),
		Limit:    20,
		Order:    domain.ChatCompletionOrderAsc,
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 {
			return domain.ListStoredChatCompletionsQuery{}, domain.NewValidationError("limit", "limit must be a positive integer")
		}
		query.Limit = limit
	}
	if rawOrder := strings.TrimSpace(values.Get("order")); rawOrder != "" {
		switch rawOrder {
		case domain.ChatCompletionOrderAsc, domain.ChatCompletionOrderDesc:
			query.Order = rawOrder
		default:
			return domain.ListStoredChatCompletionsQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
	}

	return query, nil
}

func parseListStoredChatCompletionMessagesQuery(r *http.Request) (listStoredChatCompletionMessagesQuery, error) {
	values := r.URL.Query()
	query := listStoredChatCompletionMessagesQuery{
		After: strings.TrimSpace(values.Get("after")),
		Limit: 20,
		Order: domain.ChatCompletionOrderAsc,
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 {
			return listStoredChatCompletionMessagesQuery{}, domain.NewValidationError("limit", "limit must be a positive integer")
		}
		query.Limit = limit
	}
	if rawOrder := strings.TrimSpace(values.Get("order")); rawOrder != "" {
		switch rawOrder {
		case domain.ChatCompletionOrderAsc, domain.ChatCompletionOrderDesc:
			query.Order = rawOrder
		default:
			return listStoredChatCompletionMessagesQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
	}
	return query, nil
}

func parseChatCompletionMetadataFilter(values url.Values) (map[string]string, error) {
	metadata := map[string]string{}
	for key, rawValues := range values {
		if !strings.HasPrefix(key, "metadata[") || !strings.HasSuffix(key, "]") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(key, "metadata["), "]"))
		if name == "" {
			return nil, domain.NewValidationError("metadata", "metadata keys must not be empty")
		}
		if len(rawValues) == 0 {
			metadata[name] = ""
			continue
		}
		metadata[name] = rawValues[len(rawValues)-1]
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return domain.NormalizeResponseMetadata(raw)
}

type storedChatCompletionMessagesResult struct {
	Data    []map[string]any
	HasMore bool
}

func storedChatCompletionMessagesPage(stored domain.StoredChatCompletion, query listStoredChatCompletionMessagesQuery) (storedChatCompletionMessagesResult, error) {
	messages, err := storedChatCompletionMessages(stored)
	if err != nil {
		return storedChatCompletionMessagesResult{}, err
	}
	if query.Order == domain.ChatCompletionOrderDesc {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}

	start := 0
	if query.After != "" {
		start = -1
		for i, message := range messages {
			if mapStringValue(message["id"]) == query.After {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return storedChatCompletionMessagesResult{}, sqlite.ErrNotFound
		}
	}

	if start > len(messages) {
		start = len(messages)
	}
	end := start + query.Limit
	hasMore := end < len(messages)
	if end > len(messages) {
		end = len(messages)
	}
	return storedChatCompletionMessagesResult{
		Data:    messages[start:end],
		HasMore: hasMore,
	}, nil
}

func storedChatCompletionMessages(stored domain.StoredChatCompletion) ([]map[string]any, error) {
	var request struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(stored.RequestJSON), &request); err != nil {
		return nil, fmt.Errorf("decode stored chat completion request: %w", err)
	}

	messages := make([]map[string]any, 0, len(request.Messages))
	for i, rawMessage := range request.Messages {
		var message map[string]any
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, fmt.Errorf("decode stored chat completion message: %w", err)
		}
		switch content := message["content"].(type) {
		case []any:
			message["content_parts"] = content
			message["content"] = nil
		default:
			if _, ok := message["content_parts"]; !ok {
				message["content_parts"] = nil
			}
		}
		if _, ok := message["name"]; !ok {
			message["name"] = nil
		}
		if _, ok := message["id"]; !ok {
			message["id"] = fmt.Sprintf("%s-%d", stored.ID, i)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func firstRawID(data []json.RawMessage) *string {
	if len(data) == 0 {
		return nil
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data[0], &payload); err != nil || strings.TrimSpace(payload.ID) == "" {
		return nil
	}
	return stringPtr(payload.ID)
}

func lastRawID(data []json.RawMessage) *string {
	if len(data) == 0 {
		return nil
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data[len(data)-1], &payload); err != nil || strings.TrimSpace(payload.ID) == "" {
		return nil
	}
	return stringPtr(payload.ID)
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	out := value
	return &out
}

func firstMapID(data []map[string]any) *string {
	if len(data) == 0 {
		return nil
	}
	return stringPtr(mapStringValue(data[0]["id"]))
}

func lastMapID(data []map[string]any) *string {
	if len(data) == 0 {
		return nil
	}
	return stringPtr(mapStringValue(data[len(data)-1]["id"]))
}

func mapStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func validateChatCompletionsRequest(raw []byte) error {
	var request struct {
		Model    string          `json:"model"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return domain.NewValidationError("", "malformed JSON body")
	}

	if strings.TrimSpace(request.Model) == "" {
		return domain.NewValidationError("model", "model is required")
	}

	trimmedMessages := bytes.TrimSpace(request.Messages)
	if len(trimmedMessages) == 0 || bytes.Equal(trimmedMessages, []byte("null")) {
		return domain.NewValidationError("messages", "messages is required")
	}
	if trimmedMessages[0] != '[' {
		return domain.NewValidationError("messages", "messages must be an array")
	}

	var rawMessages []json.RawMessage
	if err := json.Unmarshal(trimmedMessages, &rawMessages); err != nil {
		return domain.NewValidationError("messages", "messages must be an array")
	}
	if len(rawMessages) == 0 {
		return domain.NewValidationError("messages", "messages must not be empty")
	}

	return nil
}

var disallowedChatCompletionFields = map[string]struct{}{
	"provider_specific_fields": {},
	"reasoning_content":        {},
}

func sanitizeChatCompletionJSONBody(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	sanitizeChatCompletionValue(payload)
	return json.Marshal(payload)
}

func sanitizeChatCompletionValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for field := range disallowedChatCompletionFields {
			delete(typed, field)
		}
		for _, nested := range typed {
			sanitizeChatCompletionValue(nested)
		}
	case []any:
		for _, nested := range typed {
			sanitizeChatCompletionValue(nested)
		}
	}
}

func proxyChatCompletionStream(w http.ResponseWriter, body io.Reader) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	flusher.Flush()

	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			sanitized, sanitizeErr := sanitizeChatCompletionSSELine(line)
			if sanitizeErr != nil {
				return sanitizeErr
			}
			if _, writeErr := io.WriteString(w, sanitized); writeErr != nil {
				return writeErr
			}
			flusher.Flush()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func sanitizeChatCompletionSSELine(line string) (string, error) {
	if !strings.HasPrefix(line, "data:") {
		return line, nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return line, nil
	}

	body, err := sanitizeChatCompletionJSONBody([]byte(payload))
	if err != nil {
		return line, nil
	}

	newline := "\n"
	if strings.HasSuffix(line, "\r\n") {
		newline = "\r\n"
	}
	return "data: " + string(body) + newline, nil
}
