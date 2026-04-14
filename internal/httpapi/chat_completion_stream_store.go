package httpapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/domain"
)

type chatCompletionStreamStoreCapture struct {
	err               error
	done              bool
	id                string
	model             string
	created           int64
	requestID         string
	systemFingerprint string
	serviceTier       any
	usage             any
	choices           map[int]*chatCompletionStreamChoice
}

type chatCompletionStreamChoice struct {
	index        int
	role         string
	content      strings.Builder
	refusal      strings.Builder
	finishReason any
	logprobs     any
	functionCall *chatCompletionStreamFunctionCall
	toolCalls    map[int]*chatCompletionStreamToolCall
}

type chatCompletionStreamFunctionCall struct {
	name      strings.Builder
	arguments strings.Builder
}

type chatCompletionStreamToolCall struct {
	index    int
	id       string
	kind     string
	function *chatCompletionStreamFunctionCall
}

func newChatCompletionStreamStoreCapture(requestID string) *chatCompletionStreamStoreCapture {
	return &chatCompletionStreamStoreCapture{
		requestID: strings.TrimSpace(requestID),
		choices:   map[int]*chatCompletionStreamChoice{},
	}
}

func (c *chatCompletionStreamStoreCapture) CaptureLine(line string) {
	if c == nil || c.err != nil {
		return
	}
	if !strings.HasPrefix(line, "data:") {
		return
	}

	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	switch payload {
	case "", "[DONE]":
		if payload == "[DONE]" {
			c.done = true
		}
		return
	}

	var chunk map[string]any
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		c.err = fmt.Errorf("decode chat completion chunk: %w", err)
		return
	}
	c.captureChunk(chunk)
}

func (c *chatCompletionStreamStoreCapture) ReconstructedResponse(requestBody []byte) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("stream capture missing")
	}
	if c.err != nil {
		return nil, c.err
	}
	if !c.done {
		return nil, fmt.Errorf("stream capture incomplete")
	}
	if strings.TrimSpace(c.id) == "" {
		return nil, fmt.Errorf("chat completion stream id missing")
	}
	if c.created == 0 {
		return nil, fmt.Errorf("chat completion stream created missing")
	}

	var request map[string]any
	if err := json.Unmarshal(requestBody, &request); err != nil {
		return nil, fmt.Errorf("decode chat completion request: %w", err)
	}

	var rawMetadata json.RawMessage
	if metadata, ok := request["metadata"]; ok {
		raw, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("encode chat completion metadata: %w", err)
		}
		rawMetadata = raw
	}
	metadata, err := domain.NormalizeResponseMetadata(rawMetadata)
	if err != nil {
		return nil, fmt.Errorf("normalize chat completion metadata: %w", err)
	}

	model := strings.TrimSpace(c.model)
	if model == "" {
		model = strings.TrimSpace(anyString(request["model"]))
	}
	if model == "" {
		return nil, fmt.Errorf("chat completion stream model missing")
	}

	response := map[string]any{
		"id":                 c.id,
		"object":             "chat.completion",
		"created":            c.created,
		"model":              model,
		"choices":            c.reconstructedChoices(),
		"usage":              c.usage,
		"metadata":           metadata,
		"tool_choice":        request["tool_choice"],
		"temperature":        numberOrDefault(request["temperature"], 1.0),
		"top_p":              numberOrDefault(request["top_p"], 1.0),
		"presence_penalty":   numberOrDefault(request["presence_penalty"], 0.0),
		"frequency_penalty":  numberOrDefault(request["frequency_penalty"], 0.0),
		"tools":              request["tools"],
		"response_format":    request["response_format"],
		"input_user":         request["user"],
		"system_fingerprint": nil,
	}
	if strings.TrimSpace(c.systemFingerprint) != "" {
		response["system_fingerprint"] = c.systemFingerprint
	}
	if strings.TrimSpace(c.requestID) != "" {
		response["request_id"] = c.requestID
	}
	if c.serviceTier != nil {
		response["service_tier"] = c.serviceTier
	}
	if seed, ok := request["seed"]; ok {
		response["seed"] = seed
	}

	body, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode reconstructed chat completion: %w", err)
	}
	compacted, err := domain.CompactJSON(body)
	if err != nil {
		return nil, fmt.Errorf("compact reconstructed chat completion: %w", err)
	}
	return []byte(compacted), nil
}

func (c *chatCompletionStreamStoreCapture) captureChunk(chunk map[string]any) {
	if id := strings.TrimSpace(anyString(chunk["id"])); id != "" {
		c.id = id
	}
	if model := strings.TrimSpace(anyString(chunk["model"])); model != "" {
		c.model = model
	}
	if created, ok := anyInt64(chunk["created"]); ok {
		c.created = created
	}
	if fingerprint := strings.TrimSpace(anyString(chunk["system_fingerprint"])); fingerprint != "" {
		c.systemFingerprint = fingerprint
	}
	if serviceTier, ok := chunk["service_tier"]; ok {
		c.serviceTier = serviceTier
	}
	if usage, ok := chunk["usage"]; ok && usage != nil {
		c.usage = usage
	}

	rawChoices, ok := chunk["choices"].([]any)
	if !ok {
		return
	}
	for _, rawChoice := range rawChoices {
		choicePayload, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		index, ok := anyInt(choicePayload["index"])
		if !ok {
			index = 0
		}
		choice := c.choice(index)
		if finishReason, ok := choicePayload["finish_reason"]; ok && finishReason != nil {
			choice.finishReason = finishReason
		}
		if logprobs, ok := choicePayload["logprobs"]; ok {
			choice.logprobs = logprobs
		}

		delta, ok := choicePayload["delta"].(map[string]any)
		if !ok {
			continue
		}
		if role := strings.TrimSpace(anyString(delta["role"])); role != "" {
			choice.role = role
		}
		if content := anyString(delta["content"]); content != "" {
			choice.content.WriteString(content)
		}
		if refusal := anyString(delta["refusal"]); refusal != "" {
			choice.refusal.WriteString(refusal)
		}
		if functionCall, ok := delta["function_call"].(map[string]any); ok {
			mergeFunctionCall(choice.functionCallState(), functionCall)
		}
		if rawToolCalls, ok := delta["tool_calls"].([]any); ok {
			for _, rawToolCall := range rawToolCalls {
				toolCallPayload, ok := rawToolCall.(map[string]any)
				if !ok {
					continue
				}
				toolCallIndex, ok := anyInt(toolCallPayload["index"])
				if !ok {
					toolCallIndex = len(choice.toolCalls)
				}
				toolCall := choice.toolCall(toolCallIndex)
				if id := strings.TrimSpace(anyString(toolCallPayload["id"])); id != "" {
					toolCall.id = id
				}
				if kind := strings.TrimSpace(anyString(toolCallPayload["type"])); kind != "" {
					toolCall.kind = kind
				}
				if functionPayload, ok := toolCallPayload["function"].(map[string]any); ok {
					mergeFunctionCall(toolCall.functionState(), functionPayload)
				}
			}
		}
	}
}

func (c *chatCompletionStreamStoreCapture) choice(index int) *chatCompletionStreamChoice {
	choice, ok := c.choices[index]
	if ok {
		return choice
	}
	choice = &chatCompletionStreamChoice{
		index:     index,
		toolCalls: map[int]*chatCompletionStreamToolCall{},
	}
	c.choices[index] = choice
	return choice
}

func (c *chatCompletionStreamStoreCapture) reconstructedChoices() []map[string]any {
	indexes := make([]int, 0, len(c.choices))
	for index := range c.choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	choices := make([]map[string]any, 0, len(indexes))
	for _, index := range indexes {
		choice := c.choices[index]
		message := map[string]any{
			"role":          choice.roleOrDefault(),
			"content":       choice.contentOrNil(),
			"tool_calls":    choice.reconstructedToolCalls(),
			"function_call": choice.reconstructedFunctionCall(),
		}
		if refusal := strings.TrimSpace(choice.refusal.String()); refusal != "" {
			message["refusal"] = refusal
		}
		choices = append(choices, map[string]any{
			"index":         choice.index,
			"message":       message,
			"finish_reason": choice.finishReason,
			"logprobs":      choice.logprobs,
		})
	}
	return choices
}

func (c *chatCompletionStreamChoice) roleOrDefault() string {
	if strings.TrimSpace(c.role) == "" {
		return "assistant"
	}
	return c.role
}

func (c *chatCompletionStreamChoice) contentOrNil() any {
	if text := c.content.String(); text != "" {
		return text
	}
	if len(c.toolCalls) > 0 || c.functionCall != nil {
		return nil
	}
	return ""
}

func (c *chatCompletionStreamChoice) functionCallState() *chatCompletionStreamFunctionCall {
	if c.functionCall == nil {
		c.functionCall = &chatCompletionStreamFunctionCall{}
	}
	return c.functionCall
}

func (c *chatCompletionStreamChoice) toolCall(index int) *chatCompletionStreamToolCall {
	toolCall, ok := c.toolCalls[index]
	if ok {
		return toolCall
	}
	toolCall = &chatCompletionStreamToolCall{
		index: index,
	}
	c.toolCalls[index] = toolCall
	return toolCall
}

func (c *chatCompletionStreamChoice) reconstructedFunctionCall() any {
	if c.functionCall == nil {
		return nil
	}
	name := c.functionCall.name.String()
	arguments := c.functionCall.arguments.String()
	if strings.TrimSpace(name) == "" && strings.TrimSpace(arguments) == "" {
		return nil
	}
	return map[string]any{
		"name":      name,
		"arguments": arguments,
	}
}

func (c *chatCompletionStreamChoice) reconstructedToolCalls() any {
	if len(c.toolCalls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(c.toolCalls))
	for index := range c.toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	toolCalls := make([]map[string]any, 0, len(indexes))
	for _, index := range indexes {
		toolCall := c.toolCalls[index]
		payload := map[string]any{
			"id":   toolCall.id,
			"type": firstNonEmpty(toolCall.kind, "function"),
		}
		if function := toolCall.reconstructedFunction(); function != nil {
			payload["function"] = function
		}
		toolCalls = append(toolCalls, payload)
	}
	return toolCalls
}

func (c *chatCompletionStreamToolCall) functionState() *chatCompletionStreamFunctionCall {
	if c.function == nil {
		c.function = &chatCompletionStreamFunctionCall{}
	}
	return c.function
}

func (c *chatCompletionStreamToolCall) reconstructedFunction() any {
	if c.function == nil {
		return nil
	}
	name := c.function.name.String()
	arguments := c.function.arguments.String()
	if strings.TrimSpace(name) == "" && strings.TrimSpace(arguments) == "" {
		return nil
	}
	return map[string]any{
		"name":      name,
		"arguments": arguments,
	}
}

func mergeFunctionCall(target *chatCompletionStreamFunctionCall, payload map[string]any) {
	if name := anyString(payload["name"]); name != "" {
		target.name.WriteString(name)
	}
	if arguments := anyString(payload["arguments"]); arguments != "" {
		target.arguments.WriteString(arguments)
	}
}

func anyString(value any) string {
	text, _ := value.(string)
	return text
}

func anyInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		raw, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(raw), true
	default:
		return 0, false
	}
}

func anyInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case json.Number:
		raw, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return raw, true
	default:
		return 0, false
	}
}

func numberOrDefault(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		raw, err := typed.Float64()
		if err == nil {
			return raw
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
