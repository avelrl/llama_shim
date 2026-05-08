package service

import (
	"context"
	"fmt"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/memory"
)

const (
	memoryMetadataSessionID = "session_id"
	memoryMetadataRemember  = "remember"
	memoryMetadataNote      = "note"
	memoryMetadataScope     = "scope"
	memoryMetadataInject    = "inject"
)

func (s *ResponseService) memoryContextItems(ctx context.Context, input CreateResponseInput) ([]domain.Item, error) {
	if !s.memoryRequestEnabled(input) {
		return nil, nil
	}
	metadata, err := domain.NormalizeResponseMetadata(input.Metadata)
	if err != nil {
		return nil, err
	}
	if inject, ok := memoryBoolMetadata(metadata[s.memoryMetadataKey(memoryMetadataInject)]); ok && !inject {
		return nil, nil
	} else if !ok && !s.memoryConfig.Inject {
		return nil, nil
	}

	sessionID := s.memorySessionID(metadata, input)
	notes, err := s.memoryStore.ListMemoryNotes(ctx, domain.ListMemoryNotesQuery{
		SessionID:     sessionID,
		IncludeGlobal: true,
		Limit:         s.memoryConfig.MaxNotes,
	})
	if err != nil {
		return nil, fmt.Errorf("list response memory notes: %w", err)
	}
	if len(notes) == 0 {
		return nil, nil
	}

	text := renderMemoryContext(notes, s.memoryConfig.MaxContextBytes)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	ids := make([]string, 0, len(notes))
	for _, note := range notes {
		if id := strings.TrimSpace(note.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		if err := s.memoryStore.TouchMemoryNotes(ctx, ids, domain.FormatTime(domain.NowUTC())); err != nil {
			return nil, fmt.Errorf("touch response memory notes: %w", err)
		}
	}

	return []domain.Item{domain.NewInputTextMessage("developer", text)}, nil
}

func (s *ResponseService) captureMemoryNote(ctx context.Context, input CreateResponseInput, responseID string) error {
	if !s.memoryRequestEnabled(input) {
		return nil
	}
	metadata, err := domain.NormalizeResponseMetadata(input.Metadata)
	if err != nil {
		return err
	}

	text := strings.TrimSpace(metadata[s.memoryMetadataKey(memoryMetadataRemember)])
	if text == "" {
		text = strings.TrimSpace(metadata[s.memoryMetadataKey(memoryMetadataNote)])
	}
	if text == "" {
		return nil
	}
	text = trimStringBytes(text, s.memoryConfig.MaxNoteBytes)
	if strings.TrimSpace(text) == "" {
		return nil
	}

	sessionID := s.memorySessionID(metadata, input)
	scope := strings.ToLower(strings.TrimSpace(metadata[s.memoryMetadataKey(memoryMetadataScope)]))
	switch scope {
	case "":
		if sessionID == "" {
			return nil
		}
		scope = "session"
	case "session":
		if sessionID == "" {
			return nil
		}
	case "global":
		sessionID = ""
	default:
		return nil
	}

	id, err := domain.NewPrefixedID("mem")
	if err != nil {
		return fmt.Errorf("generate memory note id: %w", err)
	}
	now := domain.FormatTime(domain.NowUTC())
	note := domain.MemoryNote{
		ID:               id,
		Scope:            scope,
		SessionID:        sessionID,
		Text:             text,
		Source:           "responses.metadata",
		SourceResponseID: strings.TrimSpace(responseID),
		Metadata: map[string]string{
			"metadata_namespace": s.memoryConfig.MetadataNamespace,
		},
		CreatedAt:  now,
		UpdatedAt:  now,
		LastUsedAt: now,
	}
	if err := s.memoryStore.SaveMemoryNote(ctx, note); err != nil {
		return fmt.Errorf("save response memory note: %w", err)
	}
	return nil
}

func (s *ResponseService) memoryRequestEnabled(input CreateResponseInput) bool {
	return !input.ForceShadowStore && s.memoryConfig.Enabled() && s.memoryStore != nil
}

func (s *ResponseService) memorySessionID(metadata map[string]string, input CreateResponseInput) string {
	if value := strings.TrimSpace(metadata[s.memoryMetadataKey(memoryMetadataSessionID)]); value != "" {
		return value
	}
	if value := strings.TrimSpace(input.ConversationID); value != "" {
		return "conversation:" + value
	}
	return ""
}

func (s *ResponseService) memoryMetadataKey(suffix string) string {
	namespace := strings.Trim(strings.TrimSpace(s.memoryConfig.MetadataNamespace), ".")
	if namespace == "" {
		namespace = memory.DefaultMetadataNamespace
	}
	return namespace + "." + suffix
}

func memoryBoolMetadata(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func renderMemoryContext(notes []domain.MemoryNote, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var builder strings.Builder
	if !appendBoundedString(&builder, "Shim memory for this request. Treat these as application-provided durable context. Use only when relevant; do not expose this block unless asked about available context.\n\n", maxBytes) {
		return builder.String()
	}
	for _, note := range notes {
		text := memoryLineText(note.Text)
		if text == "" {
			continue
		}
		label := "global"
		if strings.EqualFold(strings.TrimSpace(note.Scope), "session") {
			label = "session"
			if sessionID := strings.TrimSpace(note.SessionID); sessionID != "" {
				label += ":" + sessionID
			}
		}
		if !appendBoundedString(&builder, fmt.Sprintf("- [%s] %s\n", label, text), maxBytes) {
			break
		}
	}
	return builder.String()
}

func memoryLineText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	return text
}

func appendBoundedString(builder *strings.Builder, text string, maxBytes int) bool {
	remaining := maxBytes - builder.Len()
	if remaining <= 0 {
		return false
	}
	if len(text) <= remaining {
		builder.WriteString(text)
		return true
	}
	builder.WriteString(trimStringBytes(text, remaining))
	return false
}

func trimStringBytes(text string, maxBytes int) string {
	if maxBytes <= 0 || text == "" {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	var builder strings.Builder
	builder.Grow(maxBytes)
	for _, r := range text {
		next := string(r)
		if builder.Len()+len(next) > maxBytes {
			break
		}
		builder.WriteString(next)
	}
	return builder.String()
}
