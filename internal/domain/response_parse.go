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
		var item struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" || len(item.Content) == 0 {
			continue
		}

		parts := make([]TextPart, 0, len(item.Content))
		for _, part := range item.Content {
			if part.Text == "" {
				continue
			}
			parts = append(parts, TextPart{
				Type: "output_text",
				Text: part.Text,
			})
			outputTextBuilder.WriteString(part.Text)
		}
		if len(parts) == 0 {
			continue
		}
		response.Output = append(response.Output, MessageItem{
			Type:    "message",
			Role:    "assistant",
			Content: parts,
		})
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
