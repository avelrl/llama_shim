package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func ParseUpstreamResponse(raw []byte) (Response, error) {
	var payload struct {
		ID                   string            `json:"id"`
		Object               string            `json:"object"`
		CreatedAt            int64             `json:"created_at"`
		Status               string            `json:"status"`
		CompletedAt          *int64            `json:"completed_at"`
		Error                json.RawMessage   `json:"error"`
		IncompleteDetails    json.RawMessage   `json:"incomplete_details"`
		Instructions         json.RawMessage   `json:"instructions"`
		MaxOutputTokens      json.RawMessage   `json:"max_output_tokens"`
		MaxToolCalls         json.RawMessage   `json:"max_tool_calls"`
		Model                string            `json:"model"`
		ParallelToolCalls    json.RawMessage   `json:"parallel_tool_calls"`
		PreviousResponseID   string            `json:"previous_response_id"`
		Prompt               json.RawMessage   `json:"prompt"`
		PromptCacheKey       json.RawMessage   `json:"prompt_cache_key"`
		PromptCacheRetention json.RawMessage   `json:"prompt_cache_retention"`
		Reasoning            json.RawMessage   `json:"reasoning"`
		SafetyIdentifier     json.RawMessage   `json:"safety_identifier"`
		ServiceTier          json.RawMessage   `json:"service_tier"`
		Background           *bool             `json:"background"`
		Store                *bool             `json:"store"`
		Temperature          json.RawMessage   `json:"temperature"`
		Conversation         json.RawMessage   `json:"conversation"`
		Text                 json.RawMessage   `json:"text"`
		ToolChoice           json.RawMessage   `json:"tool_choice"`
		Tools                json.RawMessage   `json:"tools"`
		TopLogprobs          json.RawMessage   `json:"top_logprobs"`
		TopP                 json.RawMessage   `json:"top_p"`
		Truncation           json.RawMessage   `json:"truncation"`
		Usage                json.RawMessage   `json:"usage"`
		User                 json.RawMessage   `json:"user"`
		Metadata             json.RawMessage   `json:"metadata"`
		OutputText           string            `json:"output_text"`
		Output               []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Response{}, fmt.Errorf("decode upstream response: %w", err)
	}
	if payload.ID == "" {
		return Response{}, fmt.Errorf("upstream response id is empty")
	}

	response := Response{
		ID:                   payload.ID,
		Object:               payload.Object,
		CreatedAt:            payload.CreatedAt,
		Status:               strings.TrimSpace(payload.Status),
		CompletedAt:          payload.CompletedAt,
		Error:                payload.Error,
		IncompleteDetails:    payload.IncompleteDetails,
		Instructions:         payload.Instructions,
		MaxOutputTokens:      payload.MaxOutputTokens,
		MaxToolCalls:         payload.MaxToolCalls,
		Model:                payload.Model,
		ParallelToolCalls:    payload.ParallelToolCalls,
		PreviousResponseID:   payload.PreviousResponseID,
		Prompt:               payload.Prompt,
		PromptCacheKey:       payload.PromptCacheKey,
		PromptCacheRetention: payload.PromptCacheRetention,
		Reasoning:            payload.Reasoning,
		SafetyIdentifier:     payload.SafetyIdentifier,
		ServiceTier:          payload.ServiceTier,
		Background:           payload.Background,
		Store:                payload.Store,
		Temperature:          payload.Temperature,
		Text:                 payload.Text,
		ToolChoice:           payload.ToolChoice,
		Tools:                payload.Tools,
		TopLogprobs:          payload.TopLogprobs,
		TopP:                 payload.TopP,
		Truncation:           payload.Truncation,
		Usage:                payload.Usage,
		User:                 payload.User,
		OutputText:           payload.OutputText,
		Metadata:             map[string]string{},
	}
	if response.Object == "" {
		response.Object = "response"
	}
	if response.Status == "" && payload.CompletedAt != nil {
		response.Status = "completed"
	}
	if len(bytes.TrimSpace(response.Text)) == 0 || bytes.Equal(bytes.TrimSpace(response.Text), []byte("null")) {
		response.Text = mustMarshalDefaultResponseTextConfig()
	}
	if metadata, err := NormalizeResponseMetadata(payload.Metadata); err == nil {
		response.Metadata = metadata
	}

	response.Conversation = extractConversationReference(payload.Conversation)

	var outputTextBuilder strings.Builder
	for _, rawItem := range payload.Output {
		item, err := NewItem(rawItem)
		if err != nil {
			continue
		}
		response.Output = append(response.Output, item)
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, part := range item.Content {
			if part.Text == "" {
				continue
			}
			outputTextBuilder.WriteString(part.Text)
		}
	}

	if response.OutputText == "" {
		response.OutputText = outputTextBuilder.String()
	}
	if response.OutputText != "" && len(response.Output) == 0 {
		response.Output = []MessageItem{NewOutputTextMessage(response.OutputText)}
	}
	if response.Status == "" && (response.OutputText != "" || len(response.Output) > 0) {
		response.Status = "completed"
	}
	if !strings.EqualFold(response.Status, "completed") {
		response.CompletedAt = nil
	}

	return response, nil
}

func extractConversationReference(raw json.RawMessage) *ConversationReference {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}

	var conversationID string
	if err := json.Unmarshal(raw, &conversationID); err == nil {
		return NewConversationReference(conversationID)
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		return NewConversationReference(payload.ID)
	}

	return nil
}
