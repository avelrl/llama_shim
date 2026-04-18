package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/sqlite"
)

const maxChatCompletionCanonicalizedErrorBytes int64 = 1 << 20

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

	shouldStore := false
	if shadowStore, err := h.shouldShadowStoreChatCompletion(rawBody); err != nil {
		h.logger.WarnContext(r.Context(), "chat completion shadow store eligibility check failed",
			"request_id", RequestIDFromContext(r.Context()),
			"err", err,
		)
	} else {
		shouldStore = shadowStore
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

	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	isSSE := strings.Contains(contentType, "text/event-stream")
	if !isSSE {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if shouldSanitizeChatCompletionJSON(contentType) {
				if err := h.writeSanitizedChatCompletionResponse(w, r, response, rawBody, shouldStore); err != nil {
					WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
				}
				return
			}
			if shouldStore {
				h.logger.WarnContext(r.Context(), "chat completion shadow store skipped",
					"request_id", RequestIDFromContext(r.Context()),
					"reason", "non_json_content_type",
					"content_type", response.Header.Get("Content-Type"),
				)
			}
			copyResponseHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		if err := writeBestEffortCanonicalizedChatCompletionError(w, response); err != nil {
			h.logger.WarnContext(r.Context(), "chat completion upstream error proxy failed",
				"request_id", RequestIDFromContext(r.Context()),
				"err", err,
			)
		}
		return
	}

	copyResponseHeaders(w.Header(), response.Header)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	disableWriteDeadline(w)
	w.WriteHeader(response.StatusCode)

	var streamStoreCapture *chatCompletionStreamStoreCapture
	if response.StatusCode >= 200 && response.StatusCode < 300 && shouldStore {
		streamStoreCapture = newChatCompletionStreamStoreCapture(response.Header.Get("X-Request-Id"))
	}

	if err := proxyChatCompletionStream(w, response.Body, streamStoreCapture); err != nil && !shouldIgnoreStreamProxyError(err) {
		h.logger.WarnContext(r.Context(), "chat completion stream proxy failed",
			"request_id", RequestIDFromContext(r.Context()),
			"err", err,
		)
	} else if err == nil && streamStoreCapture != nil {
		reconstructedBody, reconstructErr := streamStoreCapture.ReconstructedResponse(rawBody)
		if reconstructErr != nil {
			h.logger.WarnContext(r.Context(), "chat completion streamed shadow store reconstruction failed",
				"request_id", RequestIDFromContext(r.Context()),
				"err", reconstructErr,
			)
		} else if err := h.shadowStoreChatCompletion(r.Context(), rawBody, reconstructedBody); err != nil {
			h.logger.ErrorContext(r.Context(), "chat completion streamed shadow store failed",
				"request_id", RequestIDFromContext(r.Context()),
				"err", err,
			)
		}
	}
}

func shouldSanitizeChatCompletionJSON(contentType string) bool {
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	return contentType == "" || strings.Contains(contentType, "json")
}

func (h *proxyHandler) writeSanitizedChatCompletionResponse(w http.ResponseWriter, r *http.Request, response *http.Response, rawBody []byte, shouldStore bool) error {
	headers := response.Header.Clone()
	headers.Del("Content-Length")
	writer := &delayedProxyBodyWriter{
		w:      w,
		header: headers,
		status: response.StatusCode,
	}

	var capture *limitedBodyCaptureBuffer
	target := io.Writer(writer)
	if shouldStore {
		capture = newLimitedBodyCaptureBuffer(h.serviceLimits.ChatCompletionsShadowStoreBytes)
		target = io.MultiWriter(writer, capture)
	}

	if err := sanitizeChatCompletionJSONToWriter(target, response.Body); err != nil {
		if !writer.started {
			return err
		}
		h.logger.WarnContext(r.Context(), "chat completion response sanitize failed after partial write",
			"request_id", RequestIDFromContext(r.Context()),
			"err", err,
		)
		return nil
	}
	writer.start()

	if capture == nil {
		return nil
	}
	if capture.overflowed {
		h.logger.WarnContext(r.Context(), "chat completion shadow store skipped",
			"request_id", RequestIDFromContext(r.Context()),
			"reason", "response_too_large",
			"limit_bytes", h.serviceLimits.ChatCompletionsShadowStoreBytes,
		)
		return nil
	}
	if err := h.shadowStoreChatCompletion(r.Context(), rawBody, capture.Bytes()); err != nil {
		h.logger.ErrorContext(r.Context(), "chat completion shadow store failed",
			"request_id", RequestIDFromContext(r.Context()),
			"err", err,
		)
	}
	return nil
}

func writeBestEffortCanonicalizedChatCompletionError(w http.ResponseWriter, response *http.Response) error {
	reader := bufio.NewReader(response.Body)
	body, overflowed, err := readResponsePrefix(reader, maxChatCompletionCanonicalizedErrorBytes)
	if err != nil {
		return err
	}
	if overflowed {
		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		if _, err := w.Write(body); err != nil {
			return err
		}
		_, err = io.Copy(w, reader)
		return err
	}

	originalBody := body
	if canonical, ok, err := canonicalizeAPIErrorBody(response.StatusCode, body); err == nil && ok {
		body = canonical
	}

	copyResponseHeaders(w.Header(), response.Header)
	if !bytes.Equal(body, originalBody) {
		w.Header().Del("Content-Length")
	}
	w.WriteHeader(response.StatusCode)
	_, err = w.Write(body)
	return err
}

func readResponsePrefix(reader io.Reader, maxBytes int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	return body, int64(len(body)) > maxBytes, nil
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

type chatCompletionDeletedResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
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

	page, upstreamForward, err := h.buildMergedStoredChatCompletionsPage(r.Context(), r, query)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	if upstreamForward != nil {
		copyResponseHeaders(w.Header(), upstreamForward.Headers)
		w.WriteHeader(upstreamForward.StatusCode)
		_, _ = w.Write(upstreamForward.Body)
		return
	}

	WriteJSON(w, http.StatusOK, chatCompletionsListResponse{
		Object:  "list",
		Data:    page.Data,
		FirstID: firstRawID(page.Data),
		LastID:  lastRawID(page.Data),
		HasMore: page.HasMore,
	})
}

func (h *proxyHandler) getStoredChatCompletion(w http.ResponseWriter, r *http.Request) {
	completion, err := h.store.GetChatCompletion(r.Context(), r.PathValue("completion_id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrNotFound) {
			h.forwardRequest(w, r)
			return
		}
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, completion.ResponseJSON)
}

func (h *proxyHandler) updateStoredChatCompletion(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	completionID := r.PathValue("completion_id")
	if _, err := h.store.GetChatCompletion(r.Context(), completionID); err != nil {
		if errors.Is(err, sqlite.ErrNotFound) {
			h.forwardWithBody(w, r, rawBody)
			return
		}
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	metadata, err := parseUpdateStoredChatCompletionRequestRaw(rawBody)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}

	completion, err := h.store.UpdateChatCompletionMetadata(r.Context(), completionID, metadata)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	h.bestEffortForwardStoredChatCompletion(r.Context(), r, rawBody)

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, completion.ResponseJSON)
}

func (h *proxyHandler) deleteStoredChatCompletion(w http.ResponseWriter, r *http.Request) {
	completionID := r.PathValue("completion_id")
	if err := h.store.DeleteChatCompletion(r.Context(), completionID); err != nil {
		if errors.Is(err, sqlite.ErrNotFound) {
			h.forwardRequest(w, r)
			return
		}
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	h.bestEffortForwardStoredChatCompletion(r.Context(), r, nil)

	WriteJSON(w, http.StatusOK, chatCompletionDeletedResponse{
		ID:      completionID,
		Object:  "chat.completion.deleted",
		Deleted: true,
	})
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
		if errors.Is(err, sqlite.ErrNotFound) {
			h.forwardRequest(w, r)
			return
		}
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

func (h *proxyHandler) shouldShadowStoreChatCompletion(rawBody []byte) (bool, error) {
	var request struct {
		Store *bool `json:"store,omitempty"`
	}
	if err := json.Unmarshal(rawBody, &request); err != nil {
		return false, err
	}
	if request.Store != nil {
		return *request.Store, nil
	}
	return h.chatCompletionsStoreWhenOmitted, nil
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

func parseUpdateStoredChatCompletionRequest(w http.ResponseWriter, r *http.Request) (map[string]string, error) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return nil, err
	}
	return parseUpdateStoredChatCompletionRequestRaw(rawBody)
}

func parseUpdateStoredChatCompletionRequestRaw(rawBody []byte) (map[string]string, error) {
	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) == 0 {
		return nil, domain.NewValidationError("metadata", "metadata is required")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, domain.NewValidationError("", "malformed JSON body")
	}

	for key := range payload {
		if key != "metadata" {
			return nil, domain.NewValidationError("body", "unsupported chat completion update field "+`"`+key+`"`)
		}
	}

	rawMetadata, ok := payload["metadata"]
	if !ok {
		return nil, domain.NewValidationError("metadata", "metadata is required")
	}
	if bytes.Equal(bytes.TrimSpace(rawMetadata), []byte("null")) {
		return nil, domain.NewValidationError("metadata", "metadata must be an object with string values")
	}

	return domain.NormalizeResponseMetadata(rawMetadata)
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

	var out bytes.Buffer
	if err := sanitizeChatCompletionJSONToWriter(&out, bytes.NewReader(body)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func sanitizeChatCompletionJSONToWriter(dst io.Writer, src io.Reader) error {
	decoder := json.NewDecoder(src)
	decoder.UseNumber()
	if err := writeSanitizedChatCompletionJSONValue(dst, decoder); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if _, err := decoder.Token(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return fmt.Errorf("unexpected trailing JSON content")
}

func writeSanitizedChatCompletionJSONValue(dst io.Writer, decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			return writeSanitizedChatCompletionJSONObject(dst, decoder)
		case '[':
			return writeSanitizedChatCompletionJSONArray(dst, decoder)
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
	}
	return writeJSONToken(dst, token)
}

func writeSanitizedChatCompletionJSONObject(dst io.Writer, decoder *json.Decoder) error {
	if _, err := io.WriteString(dst, "{"); err != nil {
		return err
	}
	first := true
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("unexpected JSON object key token %T", keyToken)
		}
		if _, drop := disallowedChatCompletionFields[key]; drop {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
			continue
		}
		if !first {
			if _, err := io.WriteString(dst, ","); err != nil {
				return err
			}
		}
		first = false
		if err := writeJSONToken(dst, key); err != nil {
			return err
		}
		if _, err := io.WriteString(dst, ":"); err != nil {
			return err
		}
		if err := writeSanitizedChatCompletionJSONValue(dst, decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := end.(json.Delim)
	if !ok || delim != '}' {
		return fmt.Errorf("unexpected JSON object terminator %v", end)
	}
	_, err = io.WriteString(dst, "}")
	return err
}

func writeSanitizedChatCompletionJSONArray(dst io.Writer, decoder *json.Decoder) error {
	if _, err := io.WriteString(dst, "["); err != nil {
		return err
	}
	first := true
	for decoder.More() {
		if !first {
			if _, err := io.WriteString(dst, ","); err != nil {
				return err
			}
		}
		first = false
		if err := writeSanitizedChatCompletionJSONValue(dst, decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := end.(json.Delim)
	if !ok || delim != ']' {
		return fmt.Errorf("unexpected JSON array terminator %v", end)
	}
	_, err = io.WriteString(dst, "]")
	return err
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func writeJSONToken(dst io.Writer, token any) error {
	encoded, err := json.Marshal(token)
	if err != nil {
		return err
	}
	_, err = dst.Write(encoded)
	return err
}

type delayedProxyBodyWriter struct {
	w       http.ResponseWriter
	header  http.Header
	status  int
	started bool
}

func (w *delayedProxyBodyWriter) start() {
	if w.started {
		return
	}
	copyResponseHeaders(w.w.Header(), w.header)
	w.w.WriteHeader(w.status)
	w.started = true
}

func (w *delayedProxyBodyWriter) Write(p []byte) (int, error) {
	w.start()
	return w.w.Write(p)
}

type limitedBodyCaptureBuffer struct {
	maxBytes   int64
	buf        bytes.Buffer
	overflowed bool
}

func newLimitedBodyCaptureBuffer(maxBytes int64) *limitedBodyCaptureBuffer {
	return &limitedBodyCaptureBuffer{maxBytes: maxBytes}
}

func (b *limitedBodyCaptureBuffer) Write(p []byte) (int, error) {
	if b == nil || b.maxBytes <= 0 || len(p) == 0 {
		return len(p), nil
	}
	if b.overflowed {
		return len(p), nil
	}
	remaining := b.maxBytes - int64(b.buf.Len())
	if remaining <= 0 {
		b.overflowed = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.overflowed = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBodyCaptureBuffer) Bytes() []byte {
	if b == nil {
		return nil
	}
	return b.buf.Bytes()
}

func proxyChatCompletionStream(w http.ResponseWriter, body io.Reader, capture *chatCompletionStreamStoreCapture) error {
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
			if capture != nil {
				capture.CaptureLine(sanitized)
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
