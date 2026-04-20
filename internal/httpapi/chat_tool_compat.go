package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"llama_shim/internal/llama"
)

const minChatToolCallRetryTokens int64 = 256

type chatToolCompatRequest struct {
	Stream   bool
	Contract toolChoiceContract
}

func parseChatToolCompatRequest(rawBody []byte) (chatToolCompatRequest, error) {
	fields, err := decodeRawFields(rawBody)
	if err != nil {
		return chatToolCompatRequest{}, err
	}

	var stream bool
	if rawStream, ok := fields["stream"]; ok && len(bytes.TrimSpace(rawStream)) > 0 && !bytes.Equal(bytes.TrimSpace(rawStream), []byte("null")) {
		if err := json.Unmarshal(rawStream, &stream); err != nil {
			return chatToolCompatRequest{}, err
		}
	}

	profile := chatToolCompatRequest{Stream: stream}
	if rawChoice, ok := fields["tool_choice"]; ok {
		profile.Contract = deriveToolChoiceContract(rawChoice, nil)
	}
	return profile, nil
}

func shouldApplyChatToolCompat(profile chatToolCompatRequest) bool {
	return !profile.Stream && profile.Contract.Active()
}

func (h *proxyHandler) createChatCompletionWithToolCompat(ctx context.Context, rawBody []byte, contract toolChoiceContract) ([]byte, error) {
	rawResponse, err := h.client.CreateChatCompletion(ctx, rawBody)
	if err == nil && validateChatToolCallContract(rawResponse, contract) == nil {
		return rawResponse, nil
	}
	if err != nil && !shouldRetryChatToolCallWithCompatError(err, contract) {
		return nil, err
	}

	retryBody, rewriteErr := rewriteChatToolCallRetryBody(rawBody, contract)
	if rewriteErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, rewriteErr
	}
	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying chat completion request for tool-calling compatibility",
			"request_id", RequestIDFromContext(ctx),
			"contract_mode", contract.Mode,
			"contract_name", contract.Name,
			"contract_namespace", contract.Namespace,
		)
	}

	rawResponse, err = h.client.CreateChatCompletion(ctx, retryBody)
	if err != nil {
		return nil, err
	}
	if err := validateChatToolCallContract(rawResponse, contract); err != nil {
		return nil, err
	}
	return rawResponse, nil
}

func shouldRetryChatToolCallWithCompatError(err error, contract toolChoiceContract) bool {
	if !contract.Active() {
		return false
	}
	var upstreamErr *llama.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(upstreamErr.Message))
	return strings.Contains(message, "tool_choice") && strings.Contains(message, "invalid")
}

func rewriteChatToolCallRetryBody(rawBody []byte, contract toolChoiceContract) ([]byte, error) {
	fields, err := decodeRawFields(rawBody)
	if err != nil {
		return nil, err
	}
	if contract.Mode == toolChoiceContractRequiredNamedFunction {
		fields["tool_choice"] = json.RawMessage(`"required"`)
	}
	ensureMinimumJSONIntegerField(fields, "max_completion_tokens", minChatToolCallRetryTokens)
	ensureMinimumJSONIntegerField(fields, "max_tokens", minChatToolCallRetryTokens)
	return json.Marshal(fields)
}

func ensureMinimumJSONIntegerField(fields map[string]json.RawMessage, key string, minimum int64) {
	raw, ok := fields[key]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if key == "max_tokens" {
			fields[key] = json.RawMessage(`256`)
		}
		return
	}
	value, ok := parseJSONInteger(raw)
	if !ok || value >= minimum {
		return
	}
	fields[key] = json.RawMessage(`256`)
}

func parseJSONInteger(raw json.RawMessage) (int64, bool) {
	var integer int64
	if err := json.Unmarshal(raw, &integer); err == nil {
		return integer, true
	}
	var floatValue float64
	if err := json.Unmarshal(raw, &floatValue); err == nil {
		return int64(floatValue), true
	}
	return 0, false
}

func validateChatToolCallContract(rawResponse []byte, contract toolChoiceContract) error {
	if !contract.Active() {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(rawResponse, &payload); err != nil {
		return &toolChoiceIncompatibleBackendError{Message: "backend returned malformed chat completion JSON"}
	}

	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion did not include choices for required tool call"}
	}
	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion choice had unexpected shape"}
	}
	message, ok := firstChoice["message"].(map[string]any)
	if !ok {
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion did not include an assistant message for required tool call"}
	}

	toolCalls := chatToolCallsFromMessage(message)
	if len(toolCalls) == 0 {
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion did not produce a required tool call"}
	}

	switch contract.Mode {
	case toolChoiceContractRequiredAny:
		for _, toolCall := range toolCalls {
			if validateChatToolCallArguments(toolCall) == nil {
				return nil
			}
		}
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion returned truncated tool call arguments"}
	case toolChoiceContractRequiredNamedFunction:
		for _, toolCall := range toolCalls {
			if chatToolCallFunctionName(toolCall) != contract.Name {
				continue
			}
			if err := validateChatToolCallArguments(toolCall); err != nil {
				return err
			}
			return nil
		}
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion did not call the requested named tool"}
	default:
		return nil
	}
}

func chatToolCallsFromMessage(message map[string]any) []map[string]any {
	if rawToolCalls, ok := message["tool_calls"].([]any); ok {
		toolCalls := make([]map[string]any, 0, len(rawToolCalls))
		for _, rawToolCall := range rawToolCalls {
			toolCall, ok := rawToolCall.(map[string]any)
			if !ok {
				continue
			}
			toolCalls = append(toolCalls, toolCall)
		}
		return toolCalls
	}
	if functionCall, ok := message["function_call"].(map[string]any); ok {
		return []map[string]any{{"type": "function", "function": functionCall}}
	}
	return nil
}

func chatToolCallFunctionName(toolCall map[string]any) string {
	if function, ok := toolCall["function"].(map[string]any); ok {
		return strings.TrimSpace(asString(function["name"]))
	}
	return strings.TrimSpace(asString(toolCall["name"]))
}

func validateChatToolCallArguments(toolCall map[string]any) error {
	arguments := chatToolCallArguments(toolCall)
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	if !json.Valid([]byte(arguments)) {
		return &toolChoiceIncompatibleBackendError{Message: "backend chat completion returned truncated tool call arguments"}
	}
	return nil
}

func chatToolCallArguments(toolCall map[string]any) string {
	if function, ok := toolCall["function"].(map[string]any); ok {
		switch typed := function["arguments"].(type) {
		case string:
			return typed
		default:
			body, _ := json.Marshal(typed)
			return string(body)
		}
	}
	switch typed := toolCall["arguments"].(type) {
	case string:
		return typed
	default:
		body, _ := json.Marshal(typed)
		return string(body)
	}
}
