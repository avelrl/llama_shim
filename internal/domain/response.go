package domain

import (
	"bytes"
	"strings"
)

func NewResponse(id, model, outputText, previousResponseID, conversationID string, createdAt int64) Response {
	assistantItem := NewOutputTextMessage(outputText)
	completedAt := createdAt
	background := false
	store := true
	response := Response{
		ID:          id,
		Object:      "response",
		CreatedAt:   createdAt,
		Status:      "completed",
		CompletedAt: &completedAt,
		Model:       model,
		Output:      []MessageItem{assistantItem},
		Background:  &background,
		Store:       &store,
		Text:        mustMarshalDefaultResponseTextConfig(),
		Metadata:    map[string]string{},
		OutputText:  outputText,
	}
	if previousResponseID != "" {
		response.PreviousResponseID = previousResponseID
	}
	response.Conversation = NewConversationReference(conversationID)
	return response
}

func ResponseFromStored(stored StoredResponse) Response {
	if strings.TrimSpace(stored.ResponseJSON) != "" {
		if response, err := ParseUpstreamResponse([]byte(stored.ResponseJSON)); err == nil {
			return hydrateStoredResponse(response, stored)
		}
	}

	response := Response{
		ID:                 stored.ID,
		Object:             "response",
		Model:              stored.Model,
		PreviousResponseID: stored.PreviousResponseID,
		Conversation:       NewConversationReference(stored.ConversationID),
		Text:               InferResponseTextConfigFromRequestJSON(stored.RequestJSON),
		OutputText:         stored.OutputText,
		Output:             stored.Output,
	}
	return hydrateStoredResponse(response, stored)
}

func hydrateStoredResponse(response Response, stored StoredResponse) Response {
	if response.ID == "" {
		response.ID = stored.ID
	}
	if response.Object == "" {
		response.Object = "response"
	}
	if response.Model == "" {
		response.Model = stored.Model
	}
	if response.PreviousResponseID == "" {
		response.PreviousResponseID = stored.PreviousResponseID
	}
	if response.Conversation == nil {
		response.Conversation = NewConversationReference(stored.ConversationID)
	}
	if len(bytes.TrimSpace(response.Text)) == 0 || bytes.Equal(bytes.TrimSpace(response.Text), []byte("null")) {
		response.Text = InferResponseTextConfigFromRequestJSON(stored.RequestJSON)
	}
	if response.Metadata == nil {
		response.Metadata = InferResponseMetadataFromRequestJSON(stored.RequestJSON)
	}
	if response.Metadata == nil {
		response.Metadata = map[string]string{}
	}
	if response.Store == nil {
		response.Store = BoolPtr(stored.Store)
	}
	if response.Background == nil {
		response.Background = BoolPtr(false)
	}
	if response.CreatedAt == 0 {
		if createdAt, ok := ParseTimeUnix(stored.CreatedAt); ok {
			response.CreatedAt = createdAt
		}
	}
	if response.Status == "" {
		switch {
		case response.CompletedAt != nil, len(response.Output) > 0, strings.TrimSpace(response.OutputText) != "":
			response.Status = "completed"
		default:
			response.Status = "in_progress"
		}
	}
	if strings.EqualFold(response.Status, "completed") {
		if response.CompletedAt == nil {
			if completedAt, ok := ParseTimeUnix(stored.CompletedAt); ok {
				response.CompletedAt = Int64Ptr(completedAt)
			}
		}
	} else {
		response.CompletedAt = nil
	}
	if response.OutputText == "" {
		response.OutputText = stored.OutputText
	}
	if len(response.Output) == 0 && len(stored.Output) > 0 {
		response.Output = stored.Output
	}
	if response.OutputText != "" && len(response.Output) == 0 && response.Status == "completed" {
		response.Output = []MessageItem{NewOutputTextMessage(response.OutputText)}
	}
	return HydrateResponseRequestSurface(response, stored.RequestJSON)
}

func BoolPtr(value bool) *bool {
	return &value
}

func Int64Ptr(value int64) *int64 {
	return &value
}

func NewConversationReference(id string) *ConversationReference {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return &ConversationReference{ID: strings.TrimSpace(id)}
}

func ConversationReferenceID(reference *ConversationReference) string {
	if reference == nil {
		return ""
	}
	return strings.TrimSpace(reference.ID)
}
