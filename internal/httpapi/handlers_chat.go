package httpapi

import (
	"bytes"
	"encoding/json"
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

	h.forwardWithBody(w, r, rawBody)
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
