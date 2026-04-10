package domain

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const syntheticCompactionPrefix = "llama_shim.compaction.v1:"

type syntheticCompactionPayload struct {
	Version   int    `json:"version"`
	Summary   string `json:"summary"`
	ItemCount int    `json:"item_count"`
}

func NewSyntheticCompactionItem(summary string, itemCount int) (Item, error) {
	id, err := NewPrefixedID("cmp")
	if err != nil {
		return Item{}, fmt.Errorf("generate compaction id: %w", err)
	}

	encryptedContent, err := EncodeSyntheticCompactionPayload(summary, itemCount)
	if err != nil {
		return Item{}, err
	}

	raw, err := json.Marshal(map[string]any{
		"id":                id,
		"type":              "compaction",
		"encrypted_content": encryptedContent,
	})
	if err != nil {
		return Item{}, fmt.Errorf("marshal compaction item: %w", err)
	}
	return NewItem(raw)
}

func EncodeSyntheticCompactionPayload(summary string, itemCount int) (string, error) {
	payload := syntheticCompactionPayload{
		Version:   1,
		Summary:   strings.TrimSpace(summary),
		ItemCount: itemCount,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal compaction payload: %w", err)
	}
	return syntheticCompactionPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeSyntheticCompactionPayload(encryptedContent string) (string, bool) {
	trimmed := strings.TrimSpace(encryptedContent)
	if !strings.HasPrefix(trimmed, syntheticCompactionPrefix) {
		return "", false
	}
	encoded := strings.TrimPrefix(trimmed, syntheticCompactionPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	var payload syntheticCompactionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false
	}
	if payload.Version != 1 {
		return "", false
	}
	return strings.TrimSpace(payload.Summary), true
}

func ExpandSyntheticCompactionItems(items []Item) ([]Item, error) {
	if len(items) == 0 {
		return items, nil
	}

	out := make([]Item, 0, len(items))
	for _, item := range items {
		if item.Type != "compaction" {
			out = append(out, item)
			continue
		}

		summary, ok := DecodeSyntheticCompactionPayload(item.StringField("encrypted_content"))
		if !ok {
			return nil, ErrUnsupportedShape
		}
		summaryMessage := NewInputTextMessage("system", syntheticCompactionSummaryMessage(summary))
		if id := strings.TrimSpace(item.ID()); id != "" {
			withID, err := summaryMessage.WithID(id)
			if err != nil {
				return nil, err
			}
			summaryMessage = withID
		}
		out = append(out, summaryMessage)
	}
	return out, nil
}

func BuildSyntheticCompactionSummary(items []Item) string {
	const (
		maxLines       = 16
		maxLineRunes   = 240
		maxSummaryRune = 4096
	)

	lines := make([]string, 0, min(len(items), maxLines))
	for _, item := range items {
		line := truncateRunes(strings.TrimSpace(syntheticCompactionLine(item)), maxLineRunes)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= maxLines {
			break
		}
	}
	if len(lines) == 0 {
		return "No prior context was provided."
	}
	return truncateRunes(strings.Join(lines, "\n"), maxSummaryRune)
}

func BuildSyntheticUsage(inputTokens, outputTokens int) json.RawMessage {
	raw, err := json.Marshal(map[string]any{
		"input_tokens": inputTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
		"output_tokens": outputTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": 0,
		},
		"total_tokens": inputTokens + outputTokens,
	})
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

func EstimateSyntheticTokenCount(items []Item) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return 0, err
	}
	return EstimateSyntheticTokenCountJSON(raw), nil
}

func EstimateSyntheticTokenCountJSON(raw []byte) int {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0
	}
	runes := utf8.RuneCount(trimmed)
	if runes == 0 {
		return 0
	}
	return max(1, (runes+3)/4)
}

func syntheticCompactionSummaryMessage(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		trimmed = "No prior context was provided."
	}
	return "Compacted prior context summary:\n" + trimmed
}

func syntheticCompactionLine(item Item) string {
	switch item.Type {
	case "message":
		text := strings.TrimSpace(MessageText(item))
		if text == "" {
			return strings.TrimSpace(item.Role) + ": [empty message]"
		}
		return strings.TrimSpace(item.Role) + ": " + text
	case "function_call":
		return strings.TrimSpace(item.Name()) + " call: " + compactRawText(item.RawField("arguments"))
	case "custom_tool_call":
		return strings.TrimSpace(item.Name()) + " custom call: " + compactRawText(item.RawField("input"))
	case "function_call_output", "custom_tool_call_output":
		return item.Type + " " + strings.TrimSpace(item.CallID()) + ": " + compactRawText(item.OutputRaw())
	case "reasoning":
		text := strings.TrimSpace(strings.Join(reasoningItemTexts(item), "\n"))
		if text == "" {
			text = compactRawText(item.Raw)
		}
		return "reasoning: " + text
	case "compaction":
		if summary, ok := DecodeSyntheticCompactionPayload(item.StringField("encrypted_content")); ok {
			return "prior compaction: " + summary
		}
		return "opaque compaction item"
	default:
		return item.Type + ": " + compactRawText(item.Raw)
	}
}

func compactRawText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if stringValue := compactJSONString(trimmed); stringValue != "" {
		return stringValue
	}
	if compact, err := CompactJSON(trimmed); err == nil {
		return compact
	}
	return strings.TrimSpace(string(trimmed))
}

func compactJSONString(raw []byte) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func reasoningItemTexts(item Item) []string {
	payload := item.Map()
	parts := make([]string, 0, 4)
	for _, field := range []string{"summary", "content"} {
		entries, ok := payload[field].([]any)
		if !ok {
			continue
		}
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(asString(entry["text"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	if text := strings.TrimSpace(asString(payload["encrypted_content"])); text != "" {
		parts = append(parts, text)
	}
	return parts
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	if limit == 1 {
		return string(runes[:1])
	}
	return string(runes[:limit-1]) + "…"
}
