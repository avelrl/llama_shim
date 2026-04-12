package domain

import "encoding/json"

type Response struct {
	ID                   string                 `json:"id"`
	Object               string                 `json:"object"`
	CreatedAt            int64                  `json:"created_at"`
	Status               string                 `json:"status"`
	CompletedAt          *int64                 `json:"completed_at"`
	Error                json.RawMessage        `json:"error"`
	IncompleteDetails    json.RawMessage        `json:"incomplete_details"`
	Instructions         json.RawMessage        `json:"instructions"`
	MaxOutputTokens      json.RawMessage        `json:"max_output_tokens"`
	MaxToolCalls         json.RawMessage        `json:"max_tool_calls"`
	Model                string                 `json:"model"`
	Output               []Item                 `json:"output"`
	ParallelToolCalls    json.RawMessage        `json:"parallel_tool_calls"`
	PreviousResponseID   string                 `json:"previous_response_id,omitempty"`
	Prompt               json.RawMessage        `json:"prompt"`
	PromptCacheKey       json.RawMessage        `json:"prompt_cache_key"`
	PromptCacheRetention json.RawMessage        `json:"prompt_cache_retention"`
	Reasoning            json.RawMessage        `json:"reasoning"`
	SafetyIdentifier     json.RawMessage        `json:"safety_identifier"`
	ServiceTier          json.RawMessage        `json:"service_tier"`
	Conversation         *ConversationReference `json:"conversation,omitempty"`
	Background           *bool                  `json:"background"`
	Store                *bool                  `json:"store"`
	Temperature          json.RawMessage        `json:"temperature"`
	Text                 json.RawMessage        `json:"text"`
	ToolChoice           json.RawMessage        `json:"tool_choice"`
	Tools                json.RawMessage        `json:"tools"`
	TopLogprobs          json.RawMessage        `json:"top_logprobs"`
	TopP                 json.RawMessage        `json:"top_p"`
	Truncation           json.RawMessage        `json:"truncation"`
	Usage                json.RawMessage        `json:"usage"`
	User                 json.RawMessage        `json:"user"`
	Metadata             map[string]string      `json:"metadata"`
	OutputText           string                 `json:"output_text"`
}

type ConversationReference struct {
	ID string `json:"id"`
}

type StoredResponse struct {
	ID                   string
	Model                string
	RequestJSON          string
	ResponseJSON         string
	NormalizedInputItems []Item
	EffectiveInputItems  []Item
	Output               []Item
	OutputText           string
	PreviousResponseID   string
	ConversationID       string
	Store                bool
	CreatedAt            string
	CompletedAt          string
}

type CodeInterpreterSession struct {
	ID           string
	Backend      string
	CreatedAt    string
	LastActiveAt string
}

type ResponseDeletion struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

type ResponseInputTokens struct {
	Object      string `json:"object"`
	InputTokens int    `json:"input_tokens"`
}

type ResponseCompaction struct {
	ID        string          `json:"id"`
	Object    string          `json:"object"`
	CreatedAt int64           `json:"created_at"`
	Output    []Item          `json:"output"`
	Usage     json.RawMessage `json:"usage"`
}

type Conversation struct {
	ID        string
	Object    string
	Metadata  map[string]string
	Items     []Item
	Version   int
	CreatedAt string
	UpdatedAt string
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
