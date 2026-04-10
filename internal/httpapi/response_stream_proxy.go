package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

func (h *responseHandler) proxyCreateStream(w http.ResponseWriter, r *http.Request, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage, streamOptions responseStreamOptions) {
	upstreamBody, plan, err := remapCustomToolsPayload(rawFields, h.customToolsMode, h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	logCustomToolTransport(r.Context(), h.logger, rawFields, upstreamBody, plan, h.codexCompatibilityEnabled)

	resp, err := h.proxyResponseRequest(r, upstreamBody)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	retryWithBridge, err := shouldRetryCustomToolsWithBridgeResponse(resp, plan)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithBridge {
		upstreamBody, plan, err = h.buildBridgedProxyResponsesBody(r.Context(), rawFields)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		_ = resp.Body.Close()
		resp, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}
	}

	retryWithStringifiedInput, err := shouldRetryResponsesInputAsStringResponse(resp, upstreamBody)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithStringifiedInput {
		upstreamBody, err = h.buildStringifiedResponsesBody(r.Context(), upstreamBody)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		_ = resp.Body.Close()
		resp, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}
	}
	retryWithBridge, err = shouldRetryCustomToolsWithBridgeResponse(resp, plan)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithBridge {
		upstreamBody, plan, err = h.buildBridgedCurrentResponsesBody(r.Context(), upstreamBody)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		_ = resp.Body.Close()
		resp, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}
	}

	retryWithAuto, err := shouldRetryToolChoiceWithAutoResponse(resp, plan)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}

	prepared, input, ok := prepareShadowStore(r.Context(), h.service.PrepareCreateContext, request, requestJSON)
	if retryWithAuto {
		rawResponse, err := h.retryResponseWithAuto(r.Context(), upstreamBody, plan)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		responseBody, response, err := finalizeUpstreamResponse(rawResponse, plan, true)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		if ok {
			if _, err := h.service.SaveExternalResponse(r.Context(), prepared, input, response); err != nil {
				h.logger.ErrorContext(r.Context(), "shadow store failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
		}
		if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, responseBody, plan, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
			h.logger.WarnContext(r.Context(), "stream proxy failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
		}
		return
	}

	var onCompleted func([]byte) error
	if ok {
		onCompleted = func(rawResponse []byte) error {
			response, err := domain.ParseUpstreamResponse(rawResponse)
			if err != nil {
				return err
			}
			response = annotateResponseCustomToolMetadata(response, plan)
			if response.OutputText == "" && len(response.Output) == 0 {
				return nil
			}
			_, err = h.service.SaveExternalResponse(r.Context(), prepared, input, response)
			return err
		}
	}

	if err := proxyResponsesStream(r.Context(), h.logger, w, resp, plan, onCompleted); err != nil && !shouldIgnoreStreamProxyError(err) {
		h.logger.WarnContext(r.Context(), "stream proxy failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
	}
}

func (h *responseHandler) createStreamViaUpstream(w http.ResponseWriter, r *http.Request, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage, streamOptions responseStreamOptions) {
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

	prepared, err := h.service.PrepareCreateContext(r.Context(), input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	upstreamBody, plan, err := buildUpstreamResponsesBody(rawFields, prepared.ContextItems, prepared.NormalizedInput, prepared.ToolCallRefs, h.customToolsMode, h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	logCustomToolTransport(r.Context(), h.logger, rawFields, upstreamBody, plan, h.codexCompatibilityEnabled)

	resp, err := h.proxyResponseRequest(r, upstreamBody)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	retryWithBridge, err := shouldRetryCustomToolsWithBridgeResponse(resp, plan)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithBridge {
		upstreamBody, plan, err = h.buildBridgedUpstreamResponsesBody(r.Context(), rawFields, prepared.ContextItems, prepared.NormalizedInput, prepared.ToolCallRefs)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		_ = resp.Body.Close()
		resp, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}
	}

	retryWithStringifiedInput, err := shouldRetryResponsesInputAsStringResponse(resp, upstreamBody)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	retryWithDirectProxy, err := shouldRetryLocalStateWithDirectProxyResponse(resp, request)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithDirectProxy {
		if h.logger != nil {
			h.logger.InfoContext(r.Context(), "retrying previous_response_id stream with upstream-managed state after local replay validation failure",
				"request_id", RequestIDFromContext(r.Context()),
			)
		}
		_ = resp.Body.Close()
		resp = nil
		h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
		return
	}
	if retryWithStringifiedInput {
		upstreamBody, err = h.buildStringifiedResponsesBody(r.Context(), upstreamBody)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		_ = resp.Body.Close()
		resp, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}
	}
	retryWithBridge, err = shouldRetryCustomToolsWithBridgeResponse(resp, plan)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithBridge {
		upstreamBody, plan, err = h.buildBridgedCurrentResponsesBody(r.Context(), upstreamBody)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		_ = resp.Body.Close()
		resp, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}
	}

	retryWithAuto, err := shouldRetryToolChoiceWithAutoResponse(resp, plan)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if retryWithAuto {
		rawResponse, err := h.retryResponseWithAuto(r.Context(), upstreamBody, plan)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		responseBody, response, err := finalizeUpstreamResponse(rawResponse, plan, true)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		if _, err := h.service.SaveExternalResponse(r.Context(), prepared, input, response); err != nil {
			h.logger.ErrorContext(r.Context(), "upstream local-state stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
		}
		if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, responseBody, plan, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
			h.logger.WarnContext(r.Context(), "upstream local-state stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
		}
		return
	}

	err = proxyResponsesStream(r.Context(), h.logger, w, resp, plan, func(rawResponse []byte) error {
		response, err := domain.ParseUpstreamResponse(rawResponse)
		if err != nil {
			return err
		}
		response = annotateResponseCustomToolMetadata(response, plan)
		if response.OutputText == "" && len(response.Output) == 0 {
			return nil
		}
		_, err = h.service.SaveExternalResponse(r.Context(), prepared, input, response)
		return err
	})
	if err != nil && !shouldIgnoreStreamProxyError(err) {
		h.logger.WarnContext(r.Context(), "upstream local-state stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
	}
}

func (h *responseHandler) proxyResponseRequest(r *http.Request, body []byte) (*http.Response, error) {
	cloned := r.Clone(r.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	if cloned.Header.Get("X-Request-Id") == "" {
		cloned.Header.Set("X-Request-Id", RequestIDFromContext(cloned.Context()))
	}
	return h.proxy.client.Proxy(cloned.Context(), cloned)
}

func proxyResponsesStream(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, resp *http.Response, plan customToolTransportPlan, onCompleted func([]byte) error) error {
	isSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	if logger != nil && logger.Enabled(ctx, slog.LevelDebug) {
		logger.DebugContext(ctx, "responses stream opened",
			"request_id", RequestIDFromContext(ctx),
			"status", resp.StatusCode,
			"content_type", resp.Header.Get("Content-Type"),
			"is_sse", isSSE,
		)
	}

	if !isSSE || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		originalBody := body
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if canonical, ok, err := canonicalizeAPIErrorBody(resp.StatusCode, body); err == nil && ok {
				body = canonical
			}
		}
		copyResponseHeaders(w.Header(), resp.Header)
		if !bytes.Equal(body, originalBody) {
			w.Header().Del("Content-Length")
		}
		w.WriteHeader(resp.StatusCode)
		_, err = w.Write(body)
		return err
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	disableWriteDeadline(w)
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	flusher.Flush()

	parser := newResponseStreamEventProxy(ctx, logger, plan, onCompleted)
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if flushErr := parser.WriteLine(w, line); flushErr != nil {
				return flushErr
			}
			flusher.Flush()
		}
		if err != nil {
			if err == io.EOF {
				return parser.Flush(w)
			}
			return err
		}
	}
}

func writeCompletedResponseAsSSE(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, rawResponse []byte, plan customToolTransportPlan, includeObfuscation bool) error {
	response, err := domain.ParseUpstreamResponse(rawResponse)
	if err != nil {
		return err
	}
	response = annotateResponseCustomToolMetadata(response, plan)
	response, err = normalizeResponseForStreaming(response, nil)
	if err != nil {
		return err
	}

	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
	headers.Set("X-Accel-Buffering", "no")
	disableWriteDeadline(w)
	w.WriteHeader(http.StatusOK)

	emitter, err := newResponseStreamEmitter(w, false)
	if err != nil {
		return err
	}
	events := buildResponseReplayEvents(response, includeObfuscation)
	for index, event := range events {
		event.payload["sequence_number"] = index + 1
		if err := emitter.write(event.eventType, event.payload); err != nil {
			return err
		}
	}
	if err := emitter.done(); err != nil {
		return err
	}
	if logger != nil && logger.Enabled(ctx, slog.LevelDebug) && len(events) > 0 {
		logger.DebugContext(ctx, "responses stream summary",
			"request_id", RequestIDFromContext(ctx),
			"event_count", len(events),
			"last_event_type", events[len(events)-1].eventType,
			"response_id", response.ID,
			"output_text_preview", truncateForLog(response.OutputText, 240),
		)
	}
	return nil
}

func shouldIgnoreStreamProxyError(err error) bool {
	return err != nil && errors.Is(err, context.Canceled)
}

type responseStreamEventProxy struct {
	ctx               context.Context
	logger            *slog.Logger
	plan              customToolTransportPlan
	onCompleted       func([]byte) error
	eventType         string
	dataLines         []string
	customItemByID    map[string]customToolDescriptor
	addedItemIDs      map[string]struct{}
	doneItemIDs       map[string]struct{}
	toolDoneItemIDs   map[string]struct{}
	sawCreated        bool
	sawItemAdded      bool
	sawOutputTextDone bool
	sawCompleted      bool
	summaryLogged     bool
	eventCount        int
	lastEventType     string
	errorType         string
	errorMessage      string
	responseID        string
	itemID            string
	model             string
	outputText        strings.Builder
}

func newResponseStreamEventProxy(ctx context.Context, logger *slog.Logger, plan customToolTransportPlan, onCompleted func([]byte) error) *responseStreamEventProxy {
	return &responseStreamEventProxy{
		ctx:             ctx,
		logger:          logger,
		plan:            plan,
		onCompleted:     onCompleted,
		customItemByID:  make(map[string]customToolDescriptor),
		addedItemIDs:    make(map[string]struct{}),
		doneItemIDs:     make(map[string]struct{}),
		toolDoneItemIDs: make(map[string]struct{}),
	}
}

func (p *responseStreamEventProxy) WriteLine(w io.Writer, line string) error {
	if line == "\n" || line == "\r\n" {
		return p.flushEvent(w)
	}
	switch {
	case strings.HasPrefix(line, "event:"):
		p.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
	case strings.HasPrefix(line, "data:"):
		p.dataLines = append(p.dataLines, strings.TrimRight(strings.TrimPrefix(line, "data:"), "\r\n"))
	default:
		_, err := io.WriteString(w, line)
		return err
	}
	return nil
}

func (p *responseStreamEventProxy) Flush(w io.Writer) error {
	if err := p.flushEvent(w); err != nil {
		return err
	}
	if err := p.emitSyntheticCompletionIfNeeded(w); err != nil {
		return err
	}
	p.logStreamSummary()
	return nil
}

func (p *responseStreamEventProxy) flushEvent(w io.Writer) error {
	if p.eventType == "" && len(p.dataLines) == 0 {
		return nil
	}
	defer func() {
		p.eventType = ""
		p.dataLines = nil
	}()

	payload := strings.Join(p.dataLines, "\n")
	if payload == "" {
		_, err := io.WriteString(w, "\n")
		return err
	}
	if strings.TrimSpace(payload) == "[DONE]" {
		if err := p.emitSyntheticCompletionIfNeeded(w); err != nil {
			return err
		}
		_, err := io.WriteString(w, "data: [DONE]\n\n")
		return err
	}

	outEvent := p.eventType
	outPayload := payload

	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err == nil {
		remappedEvent, remappedPayload := p.remapEvent(outEvent, data)
		outEvent = remappedEvent
		beforeEvents, remappedEvent, remappedPayload := p.normalizeTextStreamEvent(outEvent, remappedPayload)
		outEvent = remappedEvent
		toolEvents, remappedEvent, remappedPayload := p.normalizeCompletedToolCallEvent(outEvent, remappedPayload)
		outEvent = remappedEvent
		for _, event := range beforeEvents {
			if err := p.writeEvent(w, event.eventType, event.payload); err != nil {
				return err
			}
		}
		for _, event := range toolEvents {
			if err := p.writeEvent(w, event.eventType, event.payload); err != nil {
				return err
			}
		}
		p.noteEvent(outEvent, remappedPayload)
		p.logEventSummary(outEvent, remappedPayload)
		body, err := json.Marshal(remappedPayload)
		if err != nil {
			return err
		}
		outPayload = string(body)
		if p.onCompleted != nil && outEvent == "response.completed" {
			if responsePayload, ok := remappedPayload["response"]; ok {
				body, err := json.Marshal(responsePayload)
				if err != nil {
					return err
				}
				if err := p.onCompleted(body); err != nil {
					return err
				}
			}
		}
	}

	return p.writeRawEvent(w, outEvent, outPayload)
}

func (p *responseStreamEventProxy) remapEvent(eventType string, payload map[string]any) (string, map[string]any) {
	if eventType == "" {
		eventType = strings.TrimSpace(asString(payload["type"]))
	}

	if p.plan.BridgeActive() {
		switch eventType {
		case "response.output_item.added", "response.output_item.done":
			if item, ok := payload["item"].(map[string]any); ok {
				if rewritten, descriptor, changed := remapStreamOutputItem(item, p.plan.Bridge); changed {
					payload["item"] = rewritten
					if itemID := strings.TrimSpace(asString(rewritten["id"])); itemID != "" {
						p.customItemByID[itemID] = descriptor
					}
				}
			}
		case "response.function_call_arguments.delta":
			itemID := strings.TrimSpace(asString(payload["item_id"]))
			if _, ok := p.customItemByID[itemID]; ok {
				eventType = "response.custom_tool_call_input.delta"
				payload["type"] = eventType
			}
		case "response.function_call_arguments.done":
			changed := false
			if item, ok := payload["item"].(map[string]any); ok {
				if rewritten, descriptor, didChange := remapStreamOutputItem(item, p.plan.Bridge); didChange {
					payload["item"] = rewritten
					if itemID := strings.TrimSpace(asString(rewritten["id"])); itemID != "" {
						p.customItemByID[itemID] = descriptor
					}
					changed = true
				}
			}
			if !changed {
				if _, ok := p.customItemByID[strings.TrimSpace(asString(payload["item_id"]))]; ok {
					eventType = "response.custom_tool_call_input.done"
					payload["type"] = eventType
					if arguments, ok := payload["arguments"]; ok {
						payload["input"] = extractCustomToolInput(arguments)
						delete(payload, "arguments")
					}
				}
			} else {
				eventType = "response.custom_tool_call_input.done"
				payload["type"] = eventType
				if arguments, ok := payload["arguments"]; ok {
					payload["input"] = extractCustomToolInput(arguments)
					delete(payload, "arguments")
				}
			}
		case "response.completed":
			if responsePayload, ok := payload["response"].(map[string]any); ok {
				if output, ok := responsePayload["output"].([]any); ok {
					if recovered, changed := recoverPlaceholderCustomToolCalls(output, strings.TrimSpace(asString(responsePayload["id"])), p.plan.Bridge); changed {
						output = recovered
					}
					for index, entry := range output {
						item, ok := entry.(map[string]any)
						if !ok {
							continue
						}
						if rewritten, _, changed := remapStreamOutputItem(item, p.plan.Bridge); changed {
							output[index] = rewritten
						}
					}
					responsePayload["output"] = output
					payload["response"] = responsePayload
				}
			}
		}
	}

	p.observeTextStreamEvent(eventType, payload)
	return eventType, payload
}

type pendingSSEEvent struct {
	eventType string
	payload   map[string]any
}

func (p *responseStreamEventProxy) normalizeTextStreamEvent(eventType string, payload map[string]any) ([]pendingSSEEvent, string, map[string]any) {
	if eventType == "" {
		return nil, eventType, payload
	}
	if eventType == "response.created" {
		p.sawCreated = true
		return nil, eventType, payload
	}
	if eventType == "response.output_item.added" {
		p.sawItemAdded = true
		if item, ok := payload["item"].(map[string]any); ok {
			p.itemID = fallbackString(strings.TrimSpace(asString(item["id"])), p.itemID)
		}
		return nil, eventType, payload
	}
	if eventType == "response.completed" {
		p.sawCompleted = true
		if responsePayload, ok := payload["response"].(map[string]any); ok {
			p.captureResponseEnvelope(responsePayload)
		}
	}

	isTextEvent := eventType == "response.output_text.delta" || eventType == "response.output_text.done"
	isSyntheticCompletionEvent := eventType == "response.completed" && p.outputText.Len() > 0
	if !isTextEvent && !isSyntheticCompletionEvent {
		return nil, eventType, payload
	}

	p.ensureSyntheticIDs(payload)

	var before []pendingSSEEvent
	if !p.sawCreated {
		before = append(before, pendingSSEEvent{
			eventType: "response.created",
			payload: map[string]any{
				"type":     "response.created",
				"response": p.syntheticResponseEnvelope(false),
			},
		})
		p.sawCreated = true
	}
	if !p.sawItemAdded {
		before = append(before, pendingSSEEvent{
			eventType: "response.output_item.added",
			payload: map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item":         p.syntheticOutputItem(""),
			},
		})
		p.sawItemAdded = true
	}
	if eventType == "response.completed" && p.outputText.Len() > 0 {
		if !p.sawOutputTextDone {
			before = append(before, pendingSSEEvent{
				eventType: "response.output_text.done",
				payload: map[string]any{
					"type":          "response.output_text.done",
					"response_id":   p.responseID,
					"item_id":       p.itemID,
					"output_index":  0,
					"content_index": 0,
					"text":          p.outputText.String(),
				},
			})
			p.sawOutputTextDone = true
		}
		if _, seen := p.doneItemIDs[p.itemID]; !seen {
			before = append(before, pendingSSEEvent{
				eventType: "response.output_item.done",
				payload: map[string]any{
					"type":         "response.output_item.done",
					"output_index": 0,
					"item":         p.syntheticOutputItem(p.outputText.String()),
				},
			})
			p.doneItemIDs[p.itemID] = struct{}{}
		}
	}

	switch eventType {
	case "response.output_text.delta":
		payload["response_id"] = p.responseID
		payload["item_id"] = p.itemID
	case "response.output_text.done":
		payload["item_id"] = p.itemID
		if strings.TrimSpace(asString(payload["text"])) == "" {
			payload["text"] = p.outputText.String()
		}
	case "response.completed":
		payload["response"] = p.syntheticResponseEnvelope(true)
	}
	payload["type"] = eventType
	return before, eventType, payload
}

func (p *responseStreamEventProxy) normalizeCompletedToolCallEvent(eventType string, payload map[string]any) ([]pendingSSEEvent, string, map[string]any) {
	if eventType != "response.completed" {
		return nil, eventType, payload
	}

	responsePayload, ok := payload["response"].(map[string]any)
	if !ok {
		return nil, eventType, payload
	}
	output, ok := responsePayload["output"].([]any)
	if !ok || len(output) == 0 {
		return nil, eventType, payload
	}

	p.ensureSyntheticIDs(map[string]any{
		"response_id": responsePayload["id"],
		"model":       responsePayload["model"],
	})

	// Some backends jump straight to a completed response without emitting the
	// per-item add/delta/done events. Synthesize that sequence here so clients
	// receive a stable Responses-style stream.
	before := make([]pendingSSEEvent, 0, len(output)*4+1)
	if !p.sawCreated {
		before = append(before, pendingSSEEvent{
			eventType: "response.created",
			payload: map[string]any{
				"type": "response.created",
				"response": map[string]any{
					"id":          responsePayload["id"],
					"object":      responsePayload["object"],
					"model":       responsePayload["model"],
					"output_text": "",
					"output":      nil,
				},
			},
		})
		p.sawCreated = true
	}

	for outputIndex, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		itemType := strings.TrimSpace(asString(item["type"]))
		switch itemType {
		case "function_call", "custom_tool_call":
		default:
			continue
		}

		itemID := ensureCompletedToolItemID(item, strings.TrimSpace(asString(responsePayload["id"])), outputIndex)
		if itemID == "" {
			continue
		}

		if _, seen := p.addedItemIDs[itemID]; !seen {
			before = append(before, pendingSSEEvent{
				eventType: "response.output_item.added",
				payload: map[string]any{
					"type":         "response.output_item.added",
					"output_index": outputIndex,
					"item":         inProgressToolStreamItem(item),
				},
			})
			p.addedItemIDs[itemID] = struct{}{}
			p.sawItemAdded = true
		}

		if _, seen := p.toolDoneItemIDs[itemID]; !seen {
			deltaEvent, doneEvent, valueKey := toolStreamEventShape(itemType)
			value := strings.TrimSpace(asString(item[valueKey]))
			if value != "" {
				before = append(before, pendingSSEEvent{
					eventType: deltaEvent,
					payload: map[string]any{
						"type":         deltaEvent,
						"response_id":  responsePayload["id"],
						"item_id":      itemID,
						"output_index": outputIndex,
						"delta":        value,
					},
				})
			}

			donePayload := map[string]any{
				"type":         doneEvent,
				"response_id":  responsePayload["id"],
				"item_id":      itemID,
				"output_index": outputIndex,
				"item":         item,
			}
			if value != "" {
				donePayload[valueKey] = value
			}
			before = append(before, pendingSSEEvent{
				eventType: doneEvent,
				payload:   donePayload,
			})
			p.toolDoneItemIDs[itemID] = struct{}{}
		}

		if _, seen := p.doneItemIDs[itemID]; !seen {
			before = append(before, pendingSSEEvent{
				eventType: "response.output_item.done",
				payload: map[string]any{
					"type":         "response.output_item.done",
					"output_index": outputIndex,
					"item":         item,
				},
			})
			p.doneItemIDs[itemID] = struct{}{}
		}
	}

	return before, eventType, payload
}

func (p *responseStreamEventProxy) emitSyntheticCompletionIfNeeded(w io.Writer) error {
	if p.sawCompleted || p.outputText.Len() == 0 {
		return nil
	}
	p.ensureSyntheticIDs(nil)
	if !p.sawCreated {
		if err := p.writeEvent(w, "response.created", map[string]any{
			"type":     "response.created",
			"response": p.syntheticResponseEnvelope(false),
		}); err != nil {
			return err
		}
		p.sawCreated = true
	}
	if !p.sawItemAdded {
		if err := p.writeEvent(w, "response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": 0,
			"item":         p.syntheticOutputItem(""),
		}); err != nil {
			return err
		}
		p.sawItemAdded = true
	}
	if err := p.writeEvent(w, "response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"response_id":   p.responseID,
		"item_id":       p.itemID,
		"output_index":  0,
		"content_index": 0,
		"text":          p.outputText.String(),
	}); err != nil {
		return err
	}
	if err := p.writeEvent(w, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item":         p.syntheticOutputItem(p.outputText.String()),
	}); err != nil {
		return err
	}
	response := p.syntheticResponseEnvelope(true)
	if err := p.writeEvent(w, "response.completed", map[string]any{
		"type":     "response.completed",
		"response": response,
	}); err != nil {
		return err
	}
	p.sawCompleted = true
	if p.onCompleted != nil {
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if err := p.onCompleted(body); err != nil {
			return err
		}
	}
	return nil
}

func (p *responseStreamEventProxy) observeTextStreamEvent(eventType string, payload map[string]any) {
	switch eventType {
	case "response.created":
		if responsePayload, ok := payload["response"].(map[string]any); ok {
			p.captureResponseEnvelope(responsePayload)
		}
	case "response.output_item.added", "response.output_item.done":
		if item, ok := payload["item"].(map[string]any); ok {
			itemID := strings.TrimSpace(asString(item["id"]))
			p.itemID = fallbackString(itemID, p.itemID)
			if itemID != "" {
				if eventType == "response.output_item.added" {
					p.addedItemIDs[itemID] = struct{}{}
				} else {
					p.doneItemIDs[itemID] = struct{}{}
				}
			}
		}
	case "response.output_text.delta":
		p.ensureSyntheticIDs(payload)
		p.outputText.WriteString(asString(payload["delta"]))
	case "response.output_text.done":
		p.ensureSyntheticIDs(payload)
		p.sawOutputTextDone = true
		if p.outputText.Len() == 0 {
			p.outputText.WriteString(asString(payload["text"]))
		}
	case "response.completed":
		if responsePayload, ok := payload["response"].(map[string]any); ok {
			p.captureResponseEnvelope(responsePayload)
			if p.outputText.Len() == 0 {
				p.outputText.WriteString(strings.TrimSpace(asString(responsePayload["output_text"])))
			}
		}
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		itemID := strings.TrimSpace(asString(payload["item_id"]))
		if itemID == "" {
			if item, ok := payload["item"].(map[string]any); ok {
				itemID = strings.TrimSpace(asString(item["id"]))
			}
		}
		if itemID != "" {
			p.toolDoneItemIDs[itemID] = struct{}{}
		}
	}
}

func (p *responseStreamEventProxy) captureResponseEnvelope(responsePayload map[string]any) {
	p.responseID = fallbackString(strings.TrimSpace(asString(responsePayload["id"])), p.responseID)
	p.model = fallbackString(strings.TrimSpace(asString(responsePayload["model"])), p.model)
	if output, ok := responsePayload["output"].([]any); ok {
		for _, entry := range output {
			item, ok := entry.(map[string]any)
			if !ok || strings.TrimSpace(asString(item["type"])) != "message" {
				continue
			}
			itemID := strings.TrimSpace(asString(item["id"]))
			if p.sawItemAdded || p.sawOutputTextDone || p.outputText.Len() > 0 {
				p.itemID = fallbackString(p.itemID, itemID)
			} else {
				p.itemID = fallbackString(itemID, p.itemID)
			}
			content, ok := item["content"].([]any)
			if !ok {
				continue
			}
			for _, rawPart := range content {
				part, ok := rawPart.(map[string]any)
				if !ok {
					continue
				}
				if strings.TrimSpace(asString(part["type"])) != "output_text" {
					continue
				}
				text := asString(part["text"])
				if text != "" && p.outputText.Len() == 0 {
					p.outputText.WriteString(text)
				}
			}
		}
	}
}

func (p *responseStreamEventProxy) ensureSyntheticIDs(payload map[string]any) {
	if payload != nil {
		p.model = fallbackString(strings.TrimSpace(asString(payload["model"])), p.model)
		p.responseID = fallbackString(strings.TrimSpace(asString(payload["response_id"])), p.responseID)
		itemID := strings.TrimSpace(asString(payload["item_id"]))
		if p.responseID == "" && looksLikeResponseID(itemID) {
			p.responseID = itemID
		} else if p.itemID == "" && itemID != "" {
			p.itemID = itemID
		}
	}
	if p.responseID == "" {
		p.responseID = "resp_stream_proxy"
	}
	if p.itemID == "" || p.itemID == p.responseID {
		suffix := strings.TrimPrefix(p.responseID, "resp_")
		if suffix == "" || suffix == p.responseID {
			suffix = "stream_proxy"
		}
		p.itemID = "msg_" + suffix
	}
}

func (p *responseStreamEventProxy) syntheticResponseEnvelope(final bool) map[string]any {
	response := map[string]any{
		"id":          p.responseID,
		"object":      "response",
		"model":       p.model,
		"output_text": "",
		"output":      nil,
	}
	if !final {
		return response
	}
	text := p.outputText.String()
	response["output_text"] = text
	response["output"] = []map[string]any{p.syntheticOutputItem(text)}
	return response
}

func (p *responseStreamEventProxy) syntheticOutputItem(text string) map[string]any {
	return map[string]any{
		"id":   p.itemID,
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{
				"type": "output_text",
				"text": text,
			},
		},
	}
}

func (p *responseStreamEventProxy) writeEvent(w io.Writer, eventType string, payload map[string]any) error {
	p.noteEvent(eventType, payload)
	p.logEventSummary(eventType, payload)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.writeRawEvent(w, eventType, string(body))
}

func (p *responseStreamEventProxy) noteEvent(eventType string, payload map[string]any) {
	if strings.TrimSpace(eventType) == "" {
		return
	}

	p.eventCount++
	p.lastEventType = eventType

	if errPayload, ok := payload["error"].(map[string]any); ok {
		p.errorType = fallbackString(strings.TrimSpace(asString(errPayload["type"])), p.errorType)
		p.errorMessage = fallbackString(strings.TrimSpace(asString(errPayload["message"])), p.errorMessage)
	}
}

func (p *responseStreamEventProxy) logEventSummary(eventType string, payload map[string]any) {
	if p.logger == nil || payload == nil {
		return
	}

	switch eventType {
	case "response.created",
		"response.output_item.added",
		"response.output_text.done",
		"response.output_item.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.custom_tool_call_input.delta",
		"response.custom_tool_call_input.done",
		"response.completed",
		"response.failed",
		"error":
	default:
		return
	}

	attrs := []any{
		"request_id", RequestIDFromContext(p.ctx),
		"event_type", eventType,
	}

	if outputIndex, ok := intAttr(payload["output_index"]); ok {
		attrs = append(attrs, "output_index", outputIndex)
	}

	switch eventType {
	case "response.created":
		if responsePayload, ok := payload["response"].(map[string]any); ok {
			attrs = append(attrs, summarizeResponseEnvelopeForLog(responsePayload)...)
		}
	case "response.output_text.done":
		attrs = append(attrs,
			"item_id", strings.TrimSpace(asString(payload["item_id"])),
			"text_len", len(asString(payload["text"])),
			"text_preview", truncateForLog(asString(payload["text"]), 256),
		)
	case "response.output_item.added", "response.output_item.done":
		if item, ok := payload["item"].(map[string]any); ok {
			attrs = append(attrs, summarizeOutputItemForLog(item)...)
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		attrs = append(attrs,
			"item_id", strings.TrimSpace(asString(payload["item_id"])),
			"delta_len", len(asString(payload["delta"])),
		)
	case "response.function_call_arguments.done":
		attrs = append(attrs,
			"item_id", strings.TrimSpace(asString(payload["item_id"])),
			"arguments_len", len(asString(payload["arguments"])),
			"arguments_preview", truncateForLog(asString(payload["arguments"]), 256),
		)
		if item, ok := payload["item"].(map[string]any); ok {
			attrs = append(attrs, summarizeOutputItemForLog(item)...)
		}
	case "response.custom_tool_call_input.done":
		attrs = append(attrs,
			"item_id", strings.TrimSpace(asString(payload["item_id"])),
			"input_len", len(asString(payload["input"])),
			"input_preview", truncateForLog(asString(payload["input"]), 256),
		)
		if item, ok := payload["item"].(map[string]any); ok {
			attrs = append(attrs, summarizeOutputItemForLog(item)...)
		}
	case "response.completed", "response.failed":
		if responsePayload, ok := payload["response"].(map[string]any); ok {
			attrs = append(attrs, summarizeResponseEnvelopeForLog(responsePayload)...)
		}
		if errPayload, ok := payload["error"].(map[string]any); ok {
			attrs = append(attrs,
				"error_type", strings.TrimSpace(asString(errPayload["type"])),
				"error_message", truncateForLog(asString(errPayload["message"]), 256),
			)
		}
	case "error":
		if errPayload, ok := payload["error"].(map[string]any); ok {
			attrs = append(attrs,
				"error_type", strings.TrimSpace(asString(errPayload["type"])),
				"error_message", truncateForLog(asString(errPayload["message"]), 256),
			)
		}
	}

	p.logger.DebugContext(p.ctx, "responses stream event", attrs...)
}

func (p *responseStreamEventProxy) logStreamSummary() {
	if p.summaryLogged || p.logger == nil {
		return
	}
	p.summaryLogged = true

	attrs := []any{
		"request_id", RequestIDFromContext(p.ctx),
		"response_id", p.responseID,
		"model", p.model,
		"event_count", p.eventCount,
		"last_event_type", p.lastEventType,
		"saw_created", p.sawCreated,
		"saw_item_added", p.sawItemAdded,
		"saw_output_text_done", p.sawOutputTextDone,
		"saw_completed", p.sawCompleted,
		"output_text_len", p.outputText.Len(),
	}
	if preview := strings.TrimSpace(p.outputText.String()); preview != "" {
		attrs = append(attrs, "output_text_preview", truncateForLog(preview, 256))
	}
	if p.errorType != "" {
		attrs = append(attrs, "error_type", p.errorType)
	}
	if p.errorMessage != "" {
		attrs = append(attrs, "error_message", truncateForLog(p.errorMessage, 256))
	}

	p.logger.DebugContext(p.ctx, "responses stream summary", attrs...)
}

func looksLikeSSEPayload(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return true
	}

	trimmed := bytes.TrimSpace(body)
	return bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:"))
}

func bodyPreviewForLog(body []byte, limit int) string {
	if limit <= 0 {
		return formatBodyForLog(nil, len(body) > 0)
	}

	truncated := len(body) > limit
	if truncated {
		body = body[:limit]
	}
	return formatBodyForLog(body, truncated)
}

func summarizeOutputItemForLog(item map[string]any) []any {
	if item == nil {
		return nil
	}

	attrs := []any{
		"item_id", strings.TrimSpace(asString(item["id"])),
		"item_type", strings.TrimSpace(asString(item["type"])),
	}

	if status := strings.TrimSpace(asString(item["status"])); status != "" {
		attrs = append(attrs, "item_status", status)
	}
	if callID := strings.TrimSpace(asString(item["call_id"])); callID != "" {
		attrs = append(attrs, "call_id", callID)
	}
	if name := strings.TrimSpace(asString(item["name"])); name != "" {
		attrs = append(attrs, "tool_name", name)
	}
	if namespace := strings.TrimSpace(asString(item["namespace"])); namespace != "" {
		attrs = append(attrs, "tool_namespace", namespace)
	}

	return attrs
}

func summarizeResponseEnvelopeForLog(responsePayload map[string]any) []any {
	if responsePayload == nil {
		return nil
	}

	outputCount := 0
	toolCount := 0
	messageCount := 0
	if output, ok := responsePayload["output"].([]any); ok {
		outputCount = len(output)
		for _, rawItem := range output {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			switch strings.TrimSpace(asString(item["type"])) {
			case "function_call", "custom_tool_call":
				toolCount++
			case "message":
				messageCount++
			}
		}
	}

	attrs := []any{
		"response_id", strings.TrimSpace(asString(responsePayload["id"])),
		"model", strings.TrimSpace(asString(responsePayload["model"])),
		"output_count", outputCount,
		"tool_item_count", toolCount,
		"message_item_count", messageCount,
		"output_text_len", len(asString(responsePayload["output_text"])),
	}

	return attrs
}

func intAttr(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

func (p *responseStreamEventProxy) writeRawEvent(w io.Writer, eventType, body string) error {
	if eventType != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", eventType); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	return nil
}

func looksLikeResponseID(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "resp_")
}

func remapStreamOutputItem(item map[string]any, bridge customToolBridge) (map[string]any, customToolDescriptor, bool) {
	rewritten, changed := remapFunctionCallItemToCustom(item, bridge)
	if !changed {
		return nil, customToolDescriptor{}, false
	}
	descriptor, _ := bridge.ByCanonicalIdentity(strings.TrimSpace(asString(rewritten["name"])), strings.TrimSpace(asString(rewritten["namespace"])))
	return rewritten, descriptor, true
}

func inProgressToolStreamItem(item map[string]any) map[string]any {
	cloned := cloneAnyMap(item)
	switch strings.TrimSpace(asString(cloned["type"])) {
	case "function_call":
		cloned["arguments"] = ""
	case "custom_tool_call":
		cloned["input"] = ""
	}
	cloned["status"] = "in_progress"
	return cloned
}

func toolStreamEventShape(itemType string) (deltaEvent string, doneEvent string, valueKey string) {
	switch strings.TrimSpace(itemType) {
	case "custom_tool_call":
		return "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", "input"
	default:
		return "response.function_call_arguments.delta", "response.function_call_arguments.done", "arguments"
	}
}

func ensureCompletedToolItemID(item map[string]any, responseID string, outputIndex int) string {
	if item == nil {
		return ""
	}
	if itemID := strings.TrimSpace(asString(item["id"])); itemID != "" {
		return itemID
	}
	if callID := strings.TrimSpace(asString(item["call_id"])); callID != "" {
		item["id"] = callID
		return callID
	}
	suffix := strings.TrimPrefix(strings.TrimSpace(responseID), "resp_")
	if suffix == "" {
		suffix = "stream_proxy"
	}
	itemID := fmt.Sprintf("item_%s_%d", suffix, outputIndex)
	item["id"] = itemID
	return itemID
}

func cloneAnyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
