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
	fields := parseResponseRequestFields(requestJSON)

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
