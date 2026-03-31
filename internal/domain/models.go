package domain

type TextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type MessageItem struct {
	Type    string     `json:"type"`
	Role    string     `json:"role"`
	Content []TextPart `json:"content"`
}

type Response struct {
	ID                 string        `json:"id"`
	Object             string        `json:"object"`
	Model              string        `json:"model"`
	PreviousResponseID string        `json:"previous_response_id,omitempty"`
	Conversation       string        `json:"conversation,omitempty"`
	OutputText         string        `json:"output_text"`
	Output             []MessageItem `json:"output"`
}

type StoredResponse struct {
	ID                   string
	Model                string
	RequestJSON          string
	NormalizedInputItems []MessageItem
	Output               []MessageItem
	OutputText           string
	PreviousResponseID   string
	ConversationID       string
	Store                bool
	CreatedAt            string
	CompletedAt          string
}

type Conversation struct {
	ID        string        `json:"id"`
	Object    string        `json:"object"`
	Items     []MessageItem `json:"items"`
	Version   int           `json:"-"`
	CreatedAt string        `json:"-"`
	UpdatedAt string        `json:"-"`
}

type ConversationItem struct {
	ID             string
	ConversationID string
	Seq            int
	Source         string
	Role           string
	ItemType       string
	Item           MessageItem
	CreatedAt      string
}
