package llama

import "encoding/json"

type ChatCompletionRequest struct {
	Model    string           `json:"model"`
	Messages []ChatMessageDTO `json:"messages"`
	Stream   bool             `json:"stream,omitempty"`
}

type ChatMessageDTO struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []chatCompletionChoice `json:"choices"`
}

type chatCompletionChoice struct {
	Delta        chatCompletionMessage `json:"delta"`
	Message      chatCompletionMessage `json:"message"`
	Text         json.RawMessage       `json:"text"`
	FinishReason *string               `json:"finish_reason"`
}

type chatCompletionMessage struct {
	Content json.RawMessage `json:"content"`
}
