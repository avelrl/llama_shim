package domain

import "encoding/json"

func (response Response) MarshalJSON() ([]byte, error) {
	type responseJSON struct {
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
		PreviousResponseID   any                    `json:"previous_response_id"`
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
		FrequencyPenalty     json.RawMessage        `json:"frequency_penalty"`
		PresencePenalty      json.RawMessage        `json:"presence_penalty"`
		OutputText           string                 `json:"output_text"`
	}

	var previousResponseID any
	if response.PreviousResponseID != "" {
		previousResponseID = response.PreviousResponseID
	}

	return json.Marshal(responseJSON{
		ID:                   response.ID,
		Object:               response.Object,
		CreatedAt:            response.CreatedAt,
		Status:               response.Status,
		CompletedAt:          response.CompletedAt,
		Error:                response.Error,
		IncompleteDetails:    response.IncompleteDetails,
		Instructions:         response.Instructions,
		MaxOutputTokens:      response.MaxOutputTokens,
		MaxToolCalls:         response.MaxToolCalls,
		Model:                response.Model,
		Output:               response.Output,
		ParallelToolCalls:    response.ParallelToolCalls,
		PreviousResponseID:   previousResponseID,
		Prompt:               response.Prompt,
		PromptCacheKey:       response.PromptCacheKey,
		PromptCacheRetention: response.PromptCacheRetention,
		Reasoning:            response.Reasoning,
		SafetyIdentifier:     response.SafetyIdentifier,
		ServiceTier:          coalesceRawMessage(response.ServiceTier, nil, rawJSONDefault),
		Conversation:         response.Conversation,
		Background:           response.Background,
		Store:                response.Store,
		Temperature:          response.Temperature,
		Text:                 response.Text,
		ToolChoice:           response.ToolChoice,
		Tools:                response.Tools,
		TopLogprobs:          coalesceRawMessage(response.TopLogprobs, nil, rawJSONZero),
		TopP:                 response.TopP,
		Truncation:           response.Truncation,
		Usage:                response.Usage,
		User:                 response.User,
		Metadata:             response.Metadata,
		FrequencyPenalty:     coalesceRawMessage(response.FrequencyPenalty, nil, rawJSONZeroPointZero),
		PresencePenalty:      coalesceRawMessage(response.PresencePenalty, nil, rawJSONZeroPointZero),
		OutputText:           response.OutputText,
	})
}
