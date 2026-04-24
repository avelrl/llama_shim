package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

	status, responseBody := h.createResponseViaWebSocketHTTP(ctx, original, body)
	if status < 200 || status >= 300 {
		return writeWebSocketHTTPError(ctx, conn, status, responseBody)
	}
	if err := h.cacheWebSocketResponse(ctx, body, responseBody); err != nil && h.logger != nil {
		h.logger.WarnContext(ctx, "responses websocket shadow cache failed", "request_id", RequestIDFromContext(ctx), "err", err)
	}
	return writeCompletedResponseAsWebSocket(ctx, h.logger, conn, responseBody)
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

func (h *responseHandler) createResponseViaWebSocketHTTP(ctx context.Context, original *http.Request, body []byte) (int, []byte) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if requestID := RequestIDFromContext(original.Context()); requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	h.create(recorder, req)

	result := recorder.Result()
	defer result.Body.Close()
	responseBody, err := io.ReadAll(result.Body)
	if err != nil {
		return http.StatusBadGateway, mustMarshalWebSocketHTTPError(newAPIError("upstream_error", "failed to read response body", "", ""))
	}
	return result.StatusCode, responseBody
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
