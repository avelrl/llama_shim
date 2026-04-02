package httpapi

import (
	"encoding/json"
)

type CreateResponseRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Store              *bool           `json:"store,omitempty"`
	Stream             *bool           `json:"stream,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Conversation       string          `json:"conversation,omitempty"`
	Instructions       string          `json:"instructions,omitempty"`
}

type CreateConversationRequest struct {
	Items []json.RawMessage `json:"items"`
}

type listConversationItemsResponse struct {
	Object  string           `json:"object"`
	Data    []map[string]any `json:"data"`
	FirstID *string          `json:"first_id"`
	LastID  *string          `json:"last_id"`
	HasMore bool             `json:"has_more"`
}
