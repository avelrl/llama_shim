package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxMetadataEntries = 16
	maxMetadataKeyLen  = 64
	maxMetadataValLen  = 512
)

func NormalizeConversationMetadata(raw json.RawMessage) (map[string]string, error) {
	return normalizeMetadata(raw, "metadata")
}

func NormalizeResponseMetadata(raw json.RawMessage) (map[string]string, error) {
	return normalizeMetadata(raw, "metadata")
}

func InferResponseMetadataFromRequestJSON(requestJSON string) map[string]string {
	trimmed := strings.TrimSpace(requestJSON)
	if trimmed == "" {
		return map[string]string{}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return map[string]string{}
	}

	metadata, err := NormalizeResponseMetadata(payload["metadata"])
	if err != nil {
		return map[string]string{}
	}
	return metadata
}

func normalizeMetadata(raw json.RawMessage, param string) (map[string]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]string{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, NewValidationError(param, "metadata must be an object with string values")
	}
	if len(payload) > maxMetadataEntries {
		return nil, NewValidationError(param, "metadata may include at most 16 entries")
	}

	metadata := make(map[string]string, len(payload))
	for key, value := range payload {
		if utf8.RuneCountInString(key) > maxMetadataKeyLen {
			return nil, NewValidationError(param, "metadata keys must be at most 64 characters")
		}
		text, ok := value.(string)
		if !ok {
			return nil, NewValidationError(param, "metadata must be an object with string values")
		}
		if utf8.RuneCountInString(text) > maxMetadataValLen {
			return nil, NewValidationError(param, "metadata values must be at most 512 characters")
		}
		metadata[key] = text
	}
	return metadata, nil
}

func MarshalConversationMetadata(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal conversation metadata: %w", err)
	}
	return string(raw), nil
}

func UnmarshalConversationMetadata(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]string{}, nil
	}

	var metadata map[string]string
	if err := json.Unmarshal([]byte(trimmed), &metadata); err != nil {
		return nil, fmt.Errorf("decode conversation metadata: %w", err)
	}
	if metadata == nil {
		return map[string]string{}, nil
	}
	return metadata, nil
}
