package domain

type Response struct {
	ID                 string `json:"id"`
	Object             string `json:"object"`
	Model              string `json:"model"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`
	Conversation       string `json:"conversation,omitempty"`
	OutputText         string `json:"output_text"`
	Output             []Item `json:"output"`
}

type StoredResponse struct {
	ID                   string
	Model                string
	RequestJSON          string
	NormalizedInputItems []Item
	Output               []Item
	OutputText           string
	PreviousResponseID   string
	ConversationID       string
	Store                bool
	CreatedAt            string
	CompletedAt          string
}

type Conversation struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Items     []Item `json:"items"`
	Version   int    `json:"-"`
	CreatedAt string `json:"-"`
	UpdatedAt string `json:"-"`
}

type ConversationItem struct {
	ID             string
	ConversationID string
	Seq            int
	Source         string
	Role           string
	ItemType       string
	Item           Item
	CreatedAt      string
}
