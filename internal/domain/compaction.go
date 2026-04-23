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
	Version       int                       `json:"version"`
	Summary       string                    `json:"summary"`
	ItemCount     int                       `json:"item_count"`
	Mode          string                    `json:"mode,omitempty"`
	State         *SyntheticCompactionState `json:"state,omitempty"`
	RetainedItems []json.RawMessage         `json:"retained_items,omitempty"`
}

type SyntheticCompactionState struct {
	Summary         string   `json:"summary,omitempty"`
	KeyFacts        []string `json:"key_facts,omitempty"`
	Constraints     []string `json:"constraints,omitempty"`
	OpenLoops       []string `json:"open_loops,omitempty"`
	RecentToolState []string `json:"recent_tool_state,omitempty"`
}

type SyntheticCompactionOptions struct {
	Mode          string
	State         SyntheticCompactionState
	RetainedItems []Item
}

func NewSyntheticCompactionItem(summary string, itemCount int) (Item, error) {
	return NewSyntheticCompactionItemWithOptions(summary, itemCount, SyntheticCompactionOptions{})
}

func NewSyntheticCompactionItemWithOptions(summary string, itemCount int, options SyntheticCompactionOptions) (Item, error) {
	id, err := NewPrefixedID("cmp")
	if err != nil {
		return Item{}, fmt.Errorf("generate compaction id: %w", err)
	}

	encryptedContent, err := EncodeSyntheticCompactionPayloadWithOptions(summary, itemCount, options)
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
	return EncodeSyntheticCompactionPayloadWithOptions(summary, itemCount, SyntheticCompactionOptions{})
}

func EncodeSyntheticCompactionPayloadWithOptions(summary string, itemCount int, options SyntheticCompactionOptions) (string, error) {
	retainedItems, err := marshalSyntheticCompactionRetainedItems(options.RetainedItems)
	if err != nil {
		return "", err
	}
	state := normalizeSyntheticCompactionState(options.State)
	payload := syntheticCompactionPayload{
		Version:       1,
		Summary:       strings.TrimSpace(summary),
		ItemCount:     itemCount,
		Mode:          strings.TrimSpace(options.Mode),
		RetainedItems: retainedItems,
	}
	if !isEmptySyntheticCompactionState(state) {
		payload.State = &state
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal compaction payload: %w", err)
	}
	return syntheticCompactionPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeSyntheticCompactionPayload(encryptedContent string) (string, bool) {
	payload, ok := decodeSyntheticCompactionPayload(encryptedContent)
	if !ok {
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

		payload, ok := decodeSyntheticCompactionPayload(item.StringField("encrypted_content"))
		if !ok {
			return nil, ErrUnsupportedShape
		}
		expanded, err := expandSyntheticCompactionPayload(payload)
		if err != nil {
			return nil, err
		}
		if id := strings.TrimSpace(item.ID()); id != "" {
			withID, err := expanded[0].WithID(id)
			if err != nil {
				return nil, err
			}
			expanded[0] = withID
		}
		out = append(out, expanded...)
	}
	return out, nil
}

func TrimItemsBeforeLatestCompaction(items []Item) []Item {
	if len(items) == 0 {
		return nil
	}

	lastCompaction := -1
	for idx, item := range items {
		if item.Type == "compaction" {
			lastCompaction = idx
		}
	}
	if lastCompaction <= 0 {
		return append([]Item(nil), items...)
	}
	return append([]Item(nil), items[lastCompaction:]...)
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

func BuildSyntheticCompactionTranscript(items []Item, maxItems int, maxRunes int) string {
	if maxItems <= 0 {
		maxItems = len(items)
	}
	if maxRunes <= 0 {
		return ""
	}

	lines := make([]string, 0, min(len(items), maxItems))
	var runes int
	for idx, item := range items {
		if idx >= maxItems {
			break
		}
		line := strings.TrimSpace(syntheticCompactionLine(item))
		if line == "" {
			continue
		}
		prefix := fmt.Sprintf("%03d %s", idx+1, line)
		lineRunes := utf8.RuneCountInString(prefix)
		if runes+lineRunes+1 > maxRunes {
			remaining := maxRunes - runes - 1
			if remaining <= 0 {
				break
			}
			prefix = truncateRunes(prefix, remaining)
			lines = append(lines, prefix)
			break
		}
		lines = append(lines, prefix)
		runes += lineRunes + 1
	}
	return strings.Join(lines, "\n")
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

func decodeSyntheticCompactionPayload(encryptedContent string) (syntheticCompactionPayload, bool) {
	trimmed := strings.TrimSpace(encryptedContent)
	if !strings.HasPrefix(trimmed, syntheticCompactionPrefix) {
		return syntheticCompactionPayload{}, false
	}
	encoded := strings.TrimPrefix(trimmed, syntheticCompactionPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return syntheticCompactionPayload{}, false
	}
	var payload syntheticCompactionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return syntheticCompactionPayload{}, false
	}
	if payload.Version != 1 {
		return syntheticCompactionPayload{}, false
	}
	payload.Summary = strings.TrimSpace(payload.Summary)
	if payload.State != nil {
		state := normalizeSyntheticCompactionState(*payload.State)
		payload.State = &state
	}
	return payload, true
}

func expandSyntheticCompactionPayload(payload syntheticCompactionPayload) ([]Item, error) {
	items := []Item{NewInputTextMessage("system", renderSyntheticCompactionPayload(payload))}
	for _, raw := range payload.RetainedItems {
		item, err := NewItem(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func renderSyntheticCompactionPayload(payload syntheticCompactionPayload) string {
	if payload.State == nil || isEmptySyntheticCompactionState(*payload.State) {
		return syntheticCompactionSummaryMessage(payload.Summary)
	}

	state := normalizeSyntheticCompactionState(*payload.State)
	var builder strings.Builder
	builder.WriteString("Compacted prior context summary:\n")
	summary := strings.TrimSpace(state.Summary)
	if summary == "" {
		summary = strings.TrimSpace(payload.Summary)
	}
	if summary == "" {
		summary = "No prior context was provided."
	}
	builder.WriteString(summary)
	writeSyntheticCompactionList(&builder, "Key facts", state.KeyFacts)
	writeSyntheticCompactionList(&builder, "Constraints", state.Constraints)
	writeSyntheticCompactionList(&builder, "Open loops", state.OpenLoops)
	writeSyntheticCompactionList(&builder, "Recent tool state", state.RecentToolState)
	return builder.String()
}

func writeSyntheticCompactionList(builder *strings.Builder, title string, values []string) {
	if len(values) == 0 {
		return
	}
	builder.WriteString("\n\n")
	builder.WriteString(title)
	builder.WriteString(":\n")
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		builder.WriteString("- ")
		builder.WriteString(trimmed)
		builder.WriteString("\n")
	}
}

func marshalSyntheticCompactionRetainedItems(items []Item) ([]json.RawMessage, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("marshal retained compaction item: %w", err)
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, nil
}

func normalizeSyntheticCompactionState(state SyntheticCompactionState) SyntheticCompactionState {
	return SyntheticCompactionState{
		Summary:         truncateRunes(strings.TrimSpace(state.Summary), 4096),
		KeyFacts:        normalizeSyntheticCompactionList(state.KeyFacts, 16, 512),
		Constraints:     normalizeSyntheticCompactionList(state.Constraints, 16, 512),
		OpenLoops:       normalizeSyntheticCompactionList(state.OpenLoops, 16, 512),
		RecentToolState: normalizeSyntheticCompactionList(state.RecentToolState, 16, 512),
	}
}

func normalizeSyntheticCompactionList(values []string, maxItems int, maxRunes int) []string {
	out := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		trimmed := truncateRunes(strings.TrimSpace(value), maxRunes)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func isEmptySyntheticCompactionState(state SyntheticCompactionState) bool {
	return strings.TrimSpace(state.Summary) == "" &&
		len(state.KeyFacts) == 0 &&
		len(state.Constraints) == 0 &&
		len(state.OpenLoops) == 0 &&
		len(state.RecentToolState) == 0
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
