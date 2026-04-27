package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
)

var shimLocalImageGenerationFields = map[string]struct{}{
	"tools":               {},
	"tool_choice":         {},
	"parallel_tool_calls": {},
}

type localImageGenerationConfig struct {
	Tool map[string]any
}

func isLocalImageGenerationToolRequest(rawFields map[string]json.RawMessage) bool {
	tools := decodeToolList(rawFields)
	return len(tools) == 1 && strings.EqualFold(strings.TrimSpace(asString(tools[0]["type"])), "image_generation")
}

func supportsLocalImageGeneration(rawFields map[string]json.RawMessage, provider imagegen.Provider) bool {
	if provider == nil {
		return false
	}
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		if _, ok := shimLocalImageGenerationFields[key]; ok {
			continue
		}
		return false
	}

	_, err := parseLocalImageGenerationConfig(rawFields)
	return err == nil
}

func parseLocalImageGenerationConfig(rawFields map[string]json.RawMessage) (localImageGenerationConfig, error) {
	tools := decodeToolList(rawFields)
	if len(tools) != 1 {
		return localImageGenerationConfig{}, domain.NewValidationError("tools", "shim-local image_generation requires exactly one image_generation tool")
	}
	tool := tools[0]
	toolType := strings.ToLower(strings.TrimSpace(asString(tool["type"])))
	if toolType != "image_generation" {
		return localImageGenerationConfig{}, domain.NewValidationError("tools", "shim-local image_generation requires tools[0].type=image_generation")
	}
	for key := range tool {
		switch key {
		case "type", "action", "background", "input_fidelity", "model", "moderation", "n", "output_compression", "output_format", "partial_images", "quality", "size":
		default:
			return localImageGenerationConfig{}, domain.NewValidationError("tools", "unsupported image_generation tool field "+`"`+key+`"`+" in shim-local mode")
		}
	}
	if err := validateLocalImageGenerationToolChoice(rawFields["tool_choice"]); err != nil {
		return localImageGenerationConfig{}, err
	}
	if err := validateLocalImageGenerationParallelToolCalls(rawFields["parallel_tool_calls"]); err != nil {
		return localImageGenerationConfig{}, err
	}
	return localImageGenerationConfig{Tool: tool}, nil
}

func validateLocalImageGenerationToolChoice(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var simple string
	if err := json.Unmarshal(trimmed, &simple); err == nil {
		switch strings.ToLower(strings.TrimSpace(simple)) {
		case "auto", "required":
			return nil
		default:
			return domain.NewValidationError("tool_choice", "shim-local image_generation supports tool_choice auto or required")
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return domain.NewValidationError("tool_choice", "tool_choice must be a string or object")
	}
	if strings.ToLower(strings.TrimSpace(asString(payload["type"]))) != "image_generation" {
		return domain.NewValidationError("tool_choice", "shim-local image_generation supports only tool_choice.type=image_generation")
	}
	return nil
}

func validateLocalImageGenerationParallelToolCalls(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var value bool
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return domain.NewValidationError("parallel_tool_calls", "parallel_tool_calls must be a boolean")
	}
	return nil
}

func localImageGenerationDisabledError() error {
	return domain.NewValidationError("tools", "shim-local image_generation runtime is disabled; set responses.image_generation.backend to responses or use responses.mode=prefer_upstream")
}

func (h *responseHandler) createLocalImageGenerationResponse(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, []domain.ResponseReplayArtifact, error) {
	_, err := parseLocalImageGenerationConfig(rawFields)
	if err != nil {
		return domain.Response{}, nil, err
	}

	input := service.CreateResponseInput{
		Model:              request.Model,
		Input:              request.Input,
		TextConfig:         request.Text,
		Metadata:           request.Metadata,
		Store:              request.Store,
		Stream:             request.Stream,
		Background:         request.Background,
		PreviousResponseID: request.PreviousResponseID,
		ConversationID:     request.Conversation,
		Instructions:       request.Instructions,
		RequestJSON:        requestJSON,
		GenerationOptions:  buildGenerationOptions(rawFields),
	}

	prepared, err := h.service.PrepareCreateContext(ctx, input)
	if err != nil {
		return domain.Response{}, nil, err
	}

	upstreamBody, _, err := buildUpstreamResponsesBody(rawFields, prepared.ContextItems, prepared.NormalizedInput, prepared.ToolCallRefs, h.customToolsMode, h.codexCompatibilityEnabled, h.effectiveForceCodexToolChoiceRequired(rawFields))
	if err != nil {
		return domain.Response{}, nil, err
	}

	var (
		rawResponse []byte
		artifacts   []domain.ResponseReplayArtifact
	)
	if request.Stream != nil && *request.Stream {
		streamResponse, err := h.imageGenerationProvider.CreateStream(ctx, upstreamBody)
		if err != nil {
			return domain.Response{}, nil, err
		}
		rawResponse, artifacts, err = captureLocalImageGenerationStream(ctx, h.logger, streamResponse)
		if err != nil {
			return domain.Response{}, nil, err
		}
	} else {
		rawResponse, err = h.imageGenerationProvider.Create(ctx, upstreamBody)
		if err != nil {
			return domain.Response{}, nil, err
		}
	}

	response, err := domain.ParseUpstreamResponse(rawResponse)
	if err != nil {
		return domain.Response{}, nil, err
	}
	response.Store = nil

	response, err = h.service.SaveExternalResponse(ctx, prepared, input, response)
	if err != nil {
		return domain.Response{}, nil, err
	}
	if shouldPersistLocalImageGenerationArtifacts(input) {
		if err := h.service.SaveReplayArtifacts(ctx, response.ID, artifacts); err != nil {
			return domain.Response{}, nil, err
		}
	}
	return response, artifacts, nil
}

func shouldPersistLocalImageGenerationArtifacts(input service.CreateResponseInput) bool {
	return input.PreviousResponseID != "" || input.ConversationID != "" || input.Store == nil || *input.Store
}

func captureLocalImageGenerationStream(ctx context.Context, logger *slog.Logger, stream imagegen.StreamResponse) ([]byte, []domain.ResponseReplayArtifact, error) {
	defer func() {
		if stream.Body != nil {
			_ = stream.Body.Close()
		}
	}()

	if stream.StatusCode < 200 || stream.StatusCode >= 300 {
		body, err := io.ReadAll(io.LimitReader(stream.Body, 1<<20))
		if err != nil {
			return nil, nil, fmt.Errorf("read image_generation error response: %w", err)
		}
		return nil, nil, &llama.UpstreamError{
			StatusCode: stream.StatusCode,
			Message:    string(bytes.TrimSpace(body)),
		}
	}

	if !strings.Contains(strings.ToLower(stream.Header.Get("Content-Type")), "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(stream.Body, 8<<20))
		if err != nil {
			return nil, nil, fmt.Errorf("read image_generation response: %w", err)
		}
		return body, nil, nil
	}

	var (
		completedRaw []byte
		artifacts    []domain.ResponseReplayArtifact
	)
	proxy := newResponseStreamEventProxy(ctx, logger, customToolTransportPlan{}, "", func(rawResponse []byte, replayArtifacts []domain.ResponseReplayArtifact) error {
		completedRaw = append([]byte(nil), rawResponse...)
		artifacts = append([]domain.ResponseReplayArtifact(nil), replayArtifacts...)
		return nil
	})

	reader := bufio.NewReader(stream.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if writeErr := proxy.WriteLine(io.Discard, line); writeErr != nil {
				return nil, nil, writeErr
			}
		}
		if err != nil {
			if err == io.EOF {
				if flushErr := proxy.Flush(io.Discard); flushErr != nil {
					return nil, nil, flushErr
				}
				break
			}
			return nil, nil, err
		}
	}

	if len(completedRaw) == 0 {
		return nil, nil, &llama.InvalidResponseError{Message: "image_generation backend stream completed without a final response"}
	}
	return completedRaw, artifacts, nil
}
