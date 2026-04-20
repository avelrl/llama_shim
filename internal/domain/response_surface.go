package domain

import (
	"bytes"
	"encoding/json"
	"strings"
)

var (
	rawJSONNull             = json.RawMessage("null")
	rawJSONTrue             = json.RawMessage("true")
	rawJSONAuto             = json.RawMessage(`"auto"`)
	rawJSONDisabled         = json.RawMessage(`"disabled"`)
	rawJSONDefaultReasoning = json.RawMessage(`{"effort":null,"summary":null}`)
	rawJSONEmptyArray       = json.RawMessage("[]")
	rawJSONOnePointZero     = json.RawMessage("1.0")
)

func HydrateResponseRequestSurface(response Response, requestJSON string) Response {
	fields := parseResponseRequestFields(SanitizeResponseRequestSurfaceJSON(requestJSON))

	if response.PreviousResponseID == "" {
		response.PreviousResponseID = requestPreviousResponseID(fields)
	}
	if response.Conversation == nil {
		response.Conversation = requestConversationReference(fields)
	}
	response.Instructions = coalesceRawMessage(response.Instructions, fields["instructions"], rawJSONNull)
	response.MaxOutputTokens = coalesceRawMessage(response.MaxOutputTokens, fields["max_output_tokens"], rawJSONNull)
	response.MaxToolCalls = coalesceRawMessage(response.MaxToolCalls, fields["max_tool_calls"], rawJSONNull)
	response.ParallelToolCalls = coalesceRawMessage(response.ParallelToolCalls, fields["parallel_tool_calls"], rawJSONTrue)
	response.Prompt = coalesceRawMessage(response.Prompt, fields["prompt"], rawJSONNull)
	response.PromptCacheKey = coalesceRawMessage(response.PromptCacheKey, fields["prompt_cache_key"], rawJSONNull)
	response.PromptCacheRetention = coalesceRawMessage(response.PromptCacheRetention, fields["prompt_cache_retention"], rawJSONNull)
	response.Reasoning = coalesceRawMessage(response.Reasoning, normalizeReasoningRaw(fields["reasoning"]), rawJSONDefaultReasoning)
	response.SafetyIdentifier = coalesceRawMessage(response.SafetyIdentifier, fields["safety_identifier"], rawJSONNull)
	response.ServiceTier = coalesceRawMessage(response.ServiceTier, fields["service_tier"], rawJSONNull)
	response.Temperature = coalesceRawMessage(response.Temperature, fields["temperature"], rawJSONOnePointZero)
	response.ToolChoice = coalesceRawMessage(response.ToolChoice, fields["tool_choice"], rawJSONAuto)
	response.Tools = coalesceRawMessage(response.Tools, fields["tools"], rawJSONEmptyArray)
	response.TopLogprobs = coalesceRawMessage(response.TopLogprobs, fields["top_logprobs"], rawJSONNull)
	response.TopP = coalesceRawMessage(response.TopP, fields["top_p"], rawJSONOnePointZero)
	response.Truncation = coalesceRawMessage(response.Truncation, fields["truncation"], rawJSONDisabled)
	response.User = coalesceRawMessage(response.User, fields["user"], rawJSONNull)

	return response
}

func HydrateResponseContinuationJSON(responseJSON []byte, requestJSON string) ([]byte, error) {
	fields := parseResponseRequestFields(SanitizeResponseRequestSurfaceJSON(requestJSON))
	if len(fields) == 0 {
		return append([]byte(nil), responseJSON...), nil
	}

	previousResponseID := requestPreviousResponseID(fields)
	conversation := requestConversationReference(fields)
	if previousResponseID == "" && conversation == nil {
		return append([]byte(nil), responseJSON...), nil
	}

	var responseFields map[string]json.RawMessage
	if err := json.Unmarshal(responseJSON, &responseFields); err != nil {
		return nil, err
	}

	changed := false
	if previousResponseID != "" && rawStringFieldUnset(responseFields["previous_response_id"]) {
		rawPreviousResponseID, err := json.Marshal(previousResponseID)
		if err != nil {
			return nil, err
		}
		responseFields["previous_response_id"] = rawPreviousResponseID
		changed = true
	}
	if conversation != nil && rawConversationFieldUnset(responseFields["conversation"]) {
		rawConversation, err := json.Marshal(conversation)
		if err != nil {
			return nil, err
		}
		responseFields["conversation"] = rawConversation
		changed = true
	}
	if !changed {
		return append([]byte(nil), responseJSON...), nil
	}
	return json.Marshal(responseFields)
}

func SanitizeResponseRequestSurfaceJSON(requestJSON string) string {
	trimmed := strings.TrimSpace(requestJSON)
	if trimmed == "" {
		return ""
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return requestJSON
	}

	if rawTools, ok := fields["tools"]; ok {
		fields["tools"] = sanitizeResponseRequestToolsRaw(rawTools)
	}

	sanitized, err := json.Marshal(fields)
	if err != nil {
		return requestJSON
	}
	return string(sanitized)
}

func parseResponseRequestFields(requestJSON string) map[string]json.RawMessage {
	trimmed := strings.TrimSpace(requestJSON)
	if trimmed == "" {
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil
	}
	return fields
}

func sanitizeResponseRequestToolsRaw(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return cloneRawMessage(raw)
	}

	var tools []map[string]any
	if err := json.Unmarshal(trimmed, &tools); err != nil {
		return cloneRawMessage(raw)
	}

	changed := false
	for _, tool := range tools {
		if strings.TrimSpace(asString(tool["type"])) != "mcp" {
			continue
		}
		if _, ok := tool["authorization"]; ok {
			delete(tool, "authorization")
			changed = true
		}
		if _, ok := tool["headers"]; ok {
			delete(tool, "headers")
			changed = true
		}
		if _, ok := tool["server_url"]; ok {
			delete(tool, "server_url")
			changed = true
		}
	}
	if !changed {
		return cloneRawMessage(raw)
	}

	sanitized, err := json.Marshal(tools)
	if err != nil {
		return cloneRawMessage(raw)
	}
	return sanitized
}

func requestPreviousResponseID(fields map[string]json.RawMessage) string {
	if len(fields) == 0 {
		return ""
	}

	var previousResponseID string
	if err := json.Unmarshal(fields["previous_response_id"], &previousResponseID); err != nil {
		return ""
	}
	return strings.TrimSpace(previousResponseID)
}

func requestConversationReference(fields map[string]json.RawMessage) *ConversationReference {
	if len(fields) == 0 {
		return nil
	}
	return extractConversationReference(fields["conversation"])
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), trimmed...)
}

func rawMessageMissing(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0
}

func rawStringFieldUnset(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return true
	}

	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return false
	}
	return strings.TrimSpace(value) == ""
}

func rawConversationFieldUnset(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	return extractConversationReference(trimmed) == nil
}

func coalesceRawMessage(primary, secondary, fallback json.RawMessage) json.RawMessage {
	switch {
	case !rawMessageMissing(primary):
		return cloneRawMessage(primary)
	case !rawMessageMissing(secondary):
		return cloneRawMessage(secondary)
	default:
		return cloneRawMessage(fallback)
	}
}

func normalizeReasoningRaw(raw json.RawMessage) json.RawMessage {
	if rawMessageMissing(raw) {
		return nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return cloneRawMessage(raw)
	}
	if len(payload) == 0 {
		return cloneRawMessage(rawJSONDefaultReasoning)
	}
	if _, ok := payload["effort"]; !ok {
		payload["effort"] = rawJSONNull
	}
	if _, ok := payload["summary"]; !ok {
		payload["summary"] = rawJSONNull
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		return cloneRawMessage(raw)
	}
	return normalized
}
