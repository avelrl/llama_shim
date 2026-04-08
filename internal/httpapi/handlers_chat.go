package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"llama_shim/internal/domain"
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
