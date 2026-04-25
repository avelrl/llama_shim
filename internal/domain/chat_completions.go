package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ChatCompletionOrderAsc  = "asc"
	ChatCompletionOrderDesc = "desc"
)

type StoredChatCompletion struct {
	ID           string
	Model        string
	Metadata     map[string]string
	RequestJSON  string
	ResponseJSON string
	CreatedAt    int64
}

type ListStoredChatCompletionsQuery struct {
	Model    string
	Metadata map[string]string
	After    string
	Limit    int
	Order    string
}

type StoredChatCompletionPage struct {
	Completions []StoredChatCompletion
	HasMore     bool
}

type ListStoredChatCompletionMessagesQuery struct {
	After string
	Limit int
	Order string
}

type StoredChatCompletionMessage struct {
	Sequence    int
	ID          string
	MessageJSON string
}

type StoredChatCompletionMessagePage struct {
	Messages []StoredChatCompletionMessage
	HasMore  bool
}

func StoredChatCompletionMessagesFromRequestJSON(completionID, requestJSON string) ([]StoredChatCompletionMessage, error) {
	var request struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		return nil, fmt.Errorf("decode stored chat completion request: %w", err)
	}

	messages := make([]StoredChatCompletionMessage, 0, len(request.Messages))
	for i, rawMessage := range request.Messages {
		normalized, err := normalizeStoredChatCompletionMessage(completionID, i, rawMessage)
		if err != nil {
			return nil, err
		}
		messages = append(messages, normalized)
	}
	return messages, nil
}

func StoredChatCompletionMessagePayloads(messages []StoredChatCompletionMessage) ([]map[string]any, error) {
	payloads := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		var payload map[string]any
		if err := json.Unmarshal([]byte(message.MessageJSON), &payload); err != nil {
			return nil, fmt.Errorf("decode stored chat completion message: %w", err)
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

func normalizeStoredChatCompletionMessage(completionID string, sequence int, rawMessage json.RawMessage) (StoredChatCompletionMessage, error) {
	var message map[string]any
	if err := json.Unmarshal(rawMessage, &message); err != nil {
		return StoredChatCompletionMessage{}, fmt.Errorf("decode stored chat completion message: %w", err)
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

	id := strings.TrimSpace(stringValue(message["id"]))
	if id == "" {
		id = fmt.Sprintf("%s-%d", strings.TrimSpace(completionID), sequence)
	}
	message["id"] = id

	raw, err := json.Marshal(message)
	if err != nil {
		return StoredChatCompletionMessage{}, fmt.Errorf("encode stored chat completion message: %w", err)
	}
	compacted, err := CompactJSON(raw)
	if err != nil {
		return StoredChatCompletionMessage{}, fmt.Errorf("compact stored chat completion message: %w", err)
	}
	return StoredChatCompletionMessage{
		Sequence:    sequence,
		ID:          id,
		MessageJSON: compacted,
	}, nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
