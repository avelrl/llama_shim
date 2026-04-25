package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"

	"github.com/coder/websocket"
)

const responsesWebSocketMaxLifetime = 60 * time.Minute

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func (h *responseHandler) websocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		if h.logger != nil {
			h.logger.WarnContext(r.Context(), "responses websocket accept failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
		}
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.SetReadLimit(h.serviceLimits.JSONBodyBytes)
	ctx, cancel := context.WithTimeout(r.Context(), responsesWebSocketMaxLifetime)
	defer cancel()

	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			if shouldIgnoreWebSocketError(err) {
				return
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				_ = writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "Responses websocket connection limit reached (60 minutes). Create a new websocket connection to continue.", "", "websocket_connection_limit_reached"))
				_ = conn.Close(websocket.StatusPolicyViolation, "connection limit reached")
				return
			}
			if h.logger != nil {
				h.logger.WarnContext(ctx, "responses websocket read failed", "request_id", RequestIDFromContext(ctx), "err", err)
			}
			return
		}
		if messageType != websocket.MessageText {
			if err := writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "Responses websocket messages must be JSON text frames", "", "")); err != nil {
				return
			}
			continue
		}
		if err := h.handleWebSocketMessage(ctx, conn, r, payload); err != nil {
			if shouldIgnoreWebSocketError(err) {
				return
			}
			if h.logger != nil {
				h.logger.WarnContext(ctx, "responses websocket message failed", "request_id", RequestIDFromContext(ctx), "err", err)
			}
			return
		}
	}
}

func shouldIgnoreWebSocketError(err error) bool {
	if err == nil {
		return false
	}
	status := websocket.CloseStatus(err)
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		status == websocket.StatusNormalClosure ||
		status == websocket.StatusGoingAway ||
		status == websocket.StatusNoStatusRcvd
}

func (h *responseHandler) handleWebSocketMessage(ctx context.Context, conn *websocket.Conn, original *http.Request, raw []byte) error {
	fields, err := decodeRawFields(raw)
	if err != nil {
		return writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "malformed JSON message", "", ""))
	}
	eventType := strings.TrimSpace(rawJSONString(fields["type"]))
	if eventType == "" {
		return writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "websocket message type is required", "type", ""))
	}
	if eventType != "response.create" {
		return writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "unsupported websocket message type: "+eventType, "type", ""))
	}

	body, generate, err := buildWebSocketCreateBody(fields)
	if err != nil {
		return writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", err.Error(), "", ""))
	}
	request, rawFields, requestJSON, err := decodeCreateResponseRequestBody(body, false)
	if err != nil {
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			status, payload := MapError(ctx, h.logger, err)
			return writeWebSocketError(ctx, conn, status, payload)
		}
		return writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "malformed JSON message", "", ""))
	}
	if err := h.validateWebSocketPreviousResponse(ctx, request); err != nil {
		status, payload := MapError(ctx, h.logger, err)
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) && validationErr.Param == "previous_response_id" {
			payload = newAPIError("invalid_request_error", validationErr.Message, validationErr.Param, "previous_response_not_found")
		}
		return writeWebSocketError(ctx, conn, status, payload)
	}

	if !generate {
		response, err := h.service.CreateWarmup(ctx, createResponseInputFromRequest(request, requestJSON, rawFields, true))
		if err != nil {
			status, payload := MapError(ctx, h.logger, err)
			return writeWebSocketError(ctx, conn, status, payload)
		}
		rawResponse, err := json.Marshal(response)
		if err != nil {
			return err
		}
		return writeCompletedResponseAsWebSocket(ctx, h.logger, conn, rawResponse)
	}

	return h.createResponseViaWebSocketStream(ctx, conn, original, body)
}

func buildWebSocketCreateBody(fields map[string]json.RawMessage) ([]byte, bool, error) {
	generate := true
	if rawGenerate, ok := fields["generate"]; ok {
		parsed, err := rawJSONBool(rawGenerate)
		if err != nil {
			return nil, false, domain.NewValidationError("generate", "generate must be a boolean")
		}
		generate = parsed
	}

	body := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		switch key {
		case "type", "generate", "stream", "stream_options", "background":
			continue
		default:
			body[key] = value
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}
	return raw, generate, nil
}

func rawJSONString(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func rawJSONBool(raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, err
	}
	return value, nil
}

func (h *responseHandler) validateWebSocketPreviousResponse(ctx context.Context, request CreateResponseRequest) error {
	previousResponseID := strings.TrimSpace(request.PreviousResponseID)
	if previousResponseID == "" {
		return nil
	}
	ok, err := h.service.HasPreviousResponse(ctx, previousResponseID)
	if err == nil && ok {
		return nil
	}
	mapped := service.MapStorageError(err)
	if errors.Is(mapped, service.ErrNotFound) || err == nil {
		return domain.NewValidationError("previous_response_id", "Previous response with id '"+previousResponseID+"' not found.")
	}
	return err
}

func (h *responseHandler) createResponseViaWebSocketStream(ctx context.Context, conn *websocket.Conn, original *http.Request, body []byte) error {
	streamBody, err := buildWebSocketStreamCreateBody(body)
	if err != nil {
		return writeWebSocketError(ctx, conn, http.StatusBadRequest, newAPIError("invalid_request_error", "malformed JSON message", "", ""))
	}

	writer := newWebSocketResponseStreamWriter(ctx, h.logger, conn, h.serviceLimits.JSONBodyBytes, func(rawResponse []byte) error {
		return h.cacheWebSocketResponse(ctx, body, rawResponse)
	})
	req := original.Clone(ctx)
	req.Method = http.MethodPost
	req.URL = &url.URL{Path: "/v1/responses"}
	req.RequestURI = "/v1/responses"
	req.Header = make(http.Header)
	req.Body = io.NopCloser(bytes.NewReader(streamBody))
	req.ContentLength = int64(len(streamBody))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(streamBody)), nil
	}
	req.Header.Set("Content-Type", "application/json")
	if requestID := RequestIDFromContext(original.Context()); requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}

	h.create(writer, req)
	if err := writer.Close(); err != nil {
		return err
	}
	if err := writer.Err(); err != nil {
		return err
	}
	if writer.Streamed() {
		return nil
	}

	status := writer.Status()
	responseBody, overflow := writer.Body()
	if overflow {
		return writeWebSocketError(ctx, conn, http.StatusBadGateway, newAPIError("upstream_error", "response body exceeded websocket bridge buffer", "", ""))
	}
	if status < 200 || status >= 300 {
		return writeWebSocketHTTPError(ctx, conn, status, responseBody)
	}
	if err := h.cacheWebSocketResponse(ctx, body, responseBody); err != nil && h.logger != nil {
		h.logger.WarnContext(ctx, "responses websocket shadow cache failed", "request_id", RequestIDFromContext(ctx), "err", err)
	}
	return writeCompletedResponseAsWebSocket(ctx, h.logger, conn, responseBody)
}

func buildWebSocketStreamCreateBody(body []byte) ([]byte, error) {
	fields, err := decodeRawFields(body)
	if err != nil {
		return nil, err
	}
	fields["stream"] = json.RawMessage("true")
	return json.Marshal(fields)
}

func (h *responseHandler) cacheWebSocketResponse(ctx context.Context, requestBody []byte, responseBody []byte) error {
	request, rawFields, requestJSON, err := decodeCreateResponseRequestBody(requestBody, false)
	if err != nil {
		return err
	}
	publicStore := request.Store == nil || *request.Store
	if publicStore || strings.TrimSpace(request.PreviousResponseID) != "" || strings.TrimSpace(request.Conversation) != "" {
		return nil
	}
	response, err := domain.ParseUpstreamResponse(responseBody)
	if err != nil {
		return err
	}
	prepared, err := h.service.PrepareCreateContext(ctx, createResponseInputFromRequest(request, requestJSON, rawFields, true))
	if err != nil {
		return err
	}
	_, err = h.service.SaveExternalResponse(ctx, prepared, createResponseInputFromRequest(request, requestJSON, rawFields, true), response)
	return err
}

func createResponseInputFromRequest(request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage, forceShadowStore bool) service.CreateResponseInput {
	return service.CreateResponseInput{
		Model:              request.Model,
		Input:              request.Input,
		TextConfig:         request.Text,
		Metadata:           request.Metadata,
		ContextManagement:  request.ContextManagement,
		Store:              request.Store,
		Background:         request.Background,
		ForceShadowStore:   forceShadowStore,
		PreviousResponseID: request.PreviousResponseID,
		ConversationID:     request.Conversation,
		Instructions:       request.Instructions,
		RequestJSON:        requestJSON,
		GenerationOptions:  buildGenerationOptions(rawFields),
	}
}

func writeCompletedResponseAsWebSocket(ctx context.Context, logger *slog.Logger, conn *websocket.Conn, rawResponse []byte) error {
	response, eventCount, lastEventType, err := forEachCompletedResponseReplayEvent(rawResponse, customToolTransportPlan{}, true, func(event responseReplayEvent) error {
		return writeWebSocketJSON(ctx, conn, event.payload)
	})
	if err != nil {
		status, payload := MapError(ctx, logger, err)
		if writeErr := writeWebSocketError(ctx, conn, status, payload); writeErr != nil {
			return writeErr
		}
		return nil
	}
	if logger != nil && logger.Enabled(ctx, slog.LevelDebug) && eventCount > 0 {
		logger.DebugContext(ctx, "responses websocket stream summary",
			"request_id", RequestIDFromContext(ctx),
			"event_count", eventCount,
			"last_event_type", lastEventType,
			"response_id", response.ID,
			"output_text_preview", truncateForLog(response.OutputText, 240),
		)
	}
	return nil
}

func writeWebSocketHTTPError(ctx context.Context, conn *websocket.Conn, status int, body []byte) error {
	var payload apiErrorPayload
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error.Message) != "" {
		return writeWebSocketError(ctx, conn, status, payload.Error)
	}
	return writeWebSocketError(ctx, conn, status, newAPIError("server_error", strings.TrimSpace(string(body)), "", ""))
}

func writeWebSocketError(ctx context.Context, conn *websocket.Conn, status int, apiErr apiError) error {
	return writeWebSocketJSON(ctx, conn, map[string]any{
		"type":   "error",
		"status": status,
		"error":  websocketErrorObject(apiErr),
	})
}

func websocketErrorObject(apiErr apiError) map[string]any {
	out := map[string]any{
		"message": apiErr.Message,
	}
	if strings.TrimSpace(apiErr.Type) != "" {
		out["type"] = apiErr.Type
	}
	if apiErr.Param != nil {
		out["param"] = *apiErr.Param
	}
	if apiErr.Code != nil {
		out["code"] = *apiErr.Code
	}
	return out
}

func writeWebSocketJSON(ctx context.Context, conn *websocket.Conn, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

func mustMarshalWebSocketHTTPError(apiErr apiError) []byte {
	raw, err := json.Marshal(apiErrorPayload{Error: apiErr})
	if err != nil {
		return []byte(`{"error":{"message":"internal server error","type":"internal_error"}}`)
	}
	return raw
}

type webSocketResponseStreamWriter struct {
	ctx         context.Context
	logger      *slog.Logger
	conn        *websocket.Conn
	header      http.Header
	bodyLimit   int64
	onCompleted func([]byte) error

	status       int
	wroteHeader  bool
	streaming    bool
	body         bytes.Buffer
	bodyOverflow bool
	err          error

	lineBuffer string
	eventType  string
	dataLines  []string
}

func newWebSocketResponseStreamWriter(ctx context.Context, logger *slog.Logger, conn *websocket.Conn, bodyLimit int64, onCompleted func([]byte) error) *webSocketResponseStreamWriter {
	if bodyLimit <= 0 {
		bodyLimit = 1 << 20
	}
	return &webSocketResponseStreamWriter{
		ctx:         ctx,
		logger:      logger,
		conn:        conn,
		header:      make(http.Header),
		bodyLimit:   bodyLimit,
		onCompleted: onCompleted,
	}
}

func (w *webSocketResponseStreamWriter) Header() http.Header {
	return w.header
}

func (w *webSocketResponseStreamWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.streaming = status >= 200 && status < 300 && strings.Contains(strings.ToLower(w.header.Get("Content-Type")), "text/event-stream")
}

func (w *webSocketResponseStreamWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	if !w.streaming {
		w.writeBufferedBody(payload)
		return len(payload), nil
	}
	if err := w.writeSSEChunk(payload); err != nil {
		w.err = err
		return 0, err
	}
	return len(payload), nil
}

func (w *webSocketResponseStreamWriter) Flush() {}

func (w *webSocketResponseStreamWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *webSocketResponseStreamWriter) Streamed() bool {
	return w.streaming
}

func (w *webSocketResponseStreamWriter) Body() ([]byte, bool) {
	return append([]byte(nil), w.body.Bytes()...), w.bodyOverflow
}

func (w *webSocketResponseStreamWriter) Err() error {
	return w.err
}

func (w *webSocketResponseStreamWriter) Close() error {
	if w.err != nil {
		return w.err
	}
	if !w.streaming {
		return nil
	}
	if strings.TrimSpace(w.lineBuffer) != "" {
		if err := w.writeSSELine(w.lineBuffer); err != nil {
			w.err = err
			return err
		}
		w.lineBuffer = ""
	}
	if w.eventType != "" || len(w.dataLines) > 0 {
		if err := w.flushSSEEvent(); err != nil {
			w.err = err
			return err
		}
	}
	return nil
}

func (w *webSocketResponseStreamWriter) writeBufferedBody(payload []byte) {
	if len(payload) == 0 || w.bodyOverflow {
		return
	}
	remaining := int(w.bodyLimit) + 1 - w.body.Len()
	if remaining <= 0 {
		w.bodyOverflow = true
		return
	}
	if len(payload) > remaining {
		w.body.Write(payload[:remaining])
		w.bodyOverflow = true
		return
	}
	w.body.Write(payload)
}

func (w *webSocketResponseStreamWriter) writeSSEChunk(chunk []byte) error {
	w.lineBuffer += string(chunk)
	for {
		idx := strings.IndexByte(w.lineBuffer, '\n')
		if idx < 0 {
			return nil
		}
		line := w.lineBuffer[:idx+1]
		w.lineBuffer = w.lineBuffer[idx+1:]
		if err := w.writeSSELine(line); err != nil {
			return err
		}
	}
}

func (w *webSocketResponseStreamWriter) writeSSELine(line string) error {
	trimmed := strings.TrimRight(line, "\r\n")
	if trimmed == "" {
		return w.flushSSEEvent()
	}
	switch {
	case strings.HasPrefix(trimmed, "event:"):
		w.eventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
	case strings.HasPrefix(trimmed, "data:"):
		data := strings.TrimPrefix(trimmed, "data:")
		if strings.HasPrefix(data, " ") {
			data = strings.TrimPrefix(data, " ")
		}
		w.dataLines = append(w.dataLines, data)
	}
	return nil
}

func (w *webSocketResponseStreamWriter) flushSSEEvent() error {
	if w.eventType == "" && len(w.dataLines) == 0 {
		return nil
	}
	eventType := w.eventType
	payload := strings.Join(w.dataLines, "\n")
	w.eventType = ""
	w.dataLines = nil

	if payload == "" || strings.TrimSpace(payload) == "[DONE]" {
		return nil
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return writeWebSocketError(w.ctx, w.conn, http.StatusBadGateway, newAPIError("upstream_error", "malformed SSE event from responses stream", "", ""))
	}
	if eventType == "" {
		eventType = strings.TrimSpace(asString(event["type"]))
	}
	if strings.TrimSpace(asString(event["type"])) == "" && eventType != "" {
		event["type"] = eventType
	}
	if eventType == "response.completed" && w.onCompleted != nil {
		if responsePayload, ok := event["response"]; ok {
			rawResponse, err := json.Marshal(responsePayload)
			if err != nil {
				return err
			}
			if err := w.onCompleted(rawResponse); err != nil && w.logger != nil {
				w.logger.WarnContext(w.ctx, "responses websocket shadow cache failed", "request_id", RequestIDFromContext(w.ctx), "err", err)
			}
		}
	}
	return writeWebSocketJSON(w.ctx, w.conn, event)
}
