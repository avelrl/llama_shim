package httpapi

import (
	"encoding/json"
)

type CreateResponseRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Text               json.RawMessage `json:"text,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	ContextManagement  json.RawMessage `json:"context_management,omitempty"`
	Store              *bool           `json:"store,omitempty"`
	Stream             *bool           `json:"stream,omitempty"`
	StreamOptions      json.RawMessage `json:"stream_options,omitempty"`
	Background         *bool           `json:"background,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Conversation       string          `json:"conversation,omitempty"`
	Instructions       string          `json:"instructions,omitempty"`
}

type CreateConversationRequest struct {
	Items    []json.RawMessage `json:"items"`
	Metadata json.RawMessage   `json:"metadata,omitempty"`
}

type listConversationItemsResponse struct {
	Object  string           `json:"object"`
	Data    []map[string]any `json:"data"`
	FirstID *string          `json:"first_id"`
	LastID  *string          `json:"last_id"`
	HasMore bool             `json:"has_more"`
}

type conversationResource struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Metadata  map[string]string `json:"metadata"`
}
