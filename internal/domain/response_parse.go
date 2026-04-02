package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseUpstreamResponse(raw []byte) (Response, error) {
	var payload struct {
		ID                 string            `json:"id"`
		Object             string            `json:"object"`
		Model              string            `json:"model"`
		PreviousResponseID string            `json:"previous_response_id"`
		Conversation       json.RawMessage   `json:"conversation"`
		OutputText         string            `json:"output_text"`
		Output             []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Response{}, fmt.Errorf("decode upstream response: %w", err)
	}
	if payload.ID == "" {
		return Response{}, fmt.Errorf("upstream response id is empty")
	}

	response := Response{
		ID:                 payload.ID,
		Object:             payload.Object,
		Model:              payload.Model,
		PreviousResponseID: payload.PreviousResponseID,
		OutputText:         payload.OutputText,
	}
	if response.Object == "" {
		response.Object = "response"
	}

	if conversationID := extractConversationID(payload.Conversation); conversationID != "" {
		response.Conversation = conversationID
	}

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

	return response, nil
}

func extractConversationID(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	var conversationID string
	if err := json.Unmarshal(raw, &conversationID); err == nil {
		return conversationID
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		return payload.ID
	}

	return ""
}
