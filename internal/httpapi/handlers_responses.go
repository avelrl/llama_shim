package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

type ResponseService interface {
	Create(ctx context.Context, input service.CreateResponseInput) (domain.Response, error)
	CreateStream(ctx context.Context, input service.CreateResponseInput, hooks service.StreamHooks) (domain.Response, error)
	Get(ctx context.Context, id string) (domain.Response, error)
	PrepareCreateContext(ctx context.Context, input service.CreateResponseInput) (service.PreparedResponseContext, error)
	SaveExternalResponse(ctx context.Context, prepared service.PreparedResponseContext, input service.CreateResponseInput, response domain.Response) (domain.Response, error)
}

type responseHandler struct {
	logger  *slog.Logger
	service *service.ResponseService
	proxy   *proxyHandler
}

func newResponseHandler(logger *slog.Logger, service *service.ResponseService, proxy *proxyHandler) *responseHandler {
	return &responseHandler{
		logger:  logger,
		service: service,
		proxy:   proxy,
	}
}

func (h *responseHandler) create(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	var request CreateResponseRequest
	if err := json.Unmarshal(rawBody, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}
	rawFields, err := decodeRawFields(rawBody)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}

	requestJSON, err := compactBody(rawBody)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}

	shouldProxy, err := h.shouldProxyCreate(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if shouldProxy {
		if request.Stream != nil && *request.Stream {
			h.proxy.forwardWithBody(w, r, rawBody)
			return
		}
		h.proxyCreateWithShadowStore(w, r, request, rawBody, requestJSON)
		return
	}

	generationOptions := buildGenerationOptions(rawFields)
	if request.Stream != nil && *request.Stream {
		h.createStream(w, r, request, requestJSON, generationOptions)
		return
	}

	if supportsLocalShimState(rawFields) {
		response, err := h.service.Create(r.Context(), service.CreateResponseInput{
			Model:              request.Model,
			Input:              request.Input,
			Store:              request.Store,
			Stream:             request.Stream,
			PreviousResponseID: request.PreviousResponseID,
			ConversationID:     request.Conversation,
			Instructions:       request.Instructions,
			RequestJSON:        requestJSON,
			GenerationOptions:  generationOptions,
		})
		if err == nil {
			WriteJSON(w, http.StatusOK, response)
			return
		}
		if shouldFallbackLocalState(err) {
			response, fallbackErr := h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
			if fallbackErr == nil {
				WriteJSON(w, http.StatusOK, response)
				return
			}
			err = fallbackErr
		}
		h.writeError(w, r, err)
		return
	}

	response, err := h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, response)
}

func (h *responseHandler) createLocalStateViaUpstream(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	input := service.CreateResponseInput{
		Model:              request.Model,
		Input:              request.Input,
		Store:              request.Store,
		Stream:             request.Stream,
		PreviousResponseID: request.PreviousResponseID,
		ConversationID:     request.Conversation,
		Instructions:       request.Instructions,
		RequestJSON:        requestJSON,
		GenerationOptions:  buildGenerationOptions(rawFields),
	}

	prepared, err := h.service.PrepareCreateContext(ctx, input)
	if err != nil {
		return domain.Response{}, err
	}

	upstreamBody, err := buildUpstreamResponsesBody(rawFields, prepared.ContextItems)
	if err != nil {
		return domain.Response{}, err
	}

	rawResponse, err := h.proxy.client.CreateResponse(ctx, upstreamBody)
	if err != nil {
		return domain.Response{}, err
	}

	response, err := domain.ParseUpstreamResponse(rawResponse)
	if err != nil {
		return domain.Response{}, err
	}
	if response.OutputText == "" || len(response.Output) == 0 {
		return domain.Response{}, &domain.ValidationError{
			Param:   "output",
			Message: "upstream response did not include assistant text output",
		}
	}

	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func (h *responseHandler) createStream(w http.ResponseWriter, r *http.Request, request CreateResponseRequest, requestJSON string, generationOptions map[string]json.RawMessage) {
	var (
		emitter    *responseStreamEmitter
		responseID string
		itemID     string
	)

	response, err := h.service.CreateStream(r.Context(), service.CreateResponseInput{
		Model:              request.Model,
		Input:              request.Input,
		Store:              request.Store,
		Stream:             request.Stream,
		PreviousResponseID: request.PreviousResponseID,
		ConversationID:     request.Conversation,
		Instructions:       request.Instructions,
		RequestJSON:        requestJSON,
		GenerationOptions:  generationOptions,
	}, service.StreamHooks{
		OnCreated: func(response domain.Response) error {
			var err error
			emitter, err = newResponseStreamEmitter(w)
			if err != nil {
				return err
			}
			responseID = response.ID
			itemID, err = domain.NewPrefixedID("msg")
			if err != nil {
				return err
			}
			if err := emitter.responseCreated(response); err != nil {
				return err
			}
			return emitter.outputItemAdded(itemID)
		},
		OnDelta: func(delta string) error {
			return emitter.outputTextDelta(responseID, itemID, delta)
		},
	})
	if err != nil {
		if emitter == nil {
			h.writeError(w, r, err)
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}

		_, payload := MapError(r.Context(), h.logger, err)
		_ = emitter.error(payload)
		return
	}

	if err := emitter.outputTextDone(itemID, response.OutputText); err != nil {
		return
	}
	if err := emitter.outputItemDone(itemID, response.OutputText); err != nil {
		return
	}
	if err := emitter.responseCompleted(response); err != nil {
		return
	}
}

func (h *responseHandler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	response, err := h.service.Get(r.Context(), id)
	if err != nil {
		mapped := service.MapStorageError(err)
		if errors.Is(mapped, service.ErrNotFound) {
			h.proxy.forward(w, r)
			return
		}
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, response)
}

func (h *responseHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, payload := MapError(r.Context(), h.logger, err)
	WriteJSON(w, status, apiErrorPayload{Error: payload})
}

func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	reader := http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(reader)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "request body is too large", "")
			return nil, err
		}
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body", "")
		return nil, err
	}
	return body, nil
}

func compactBody(raw []byte) (string, error) {
	return domain.CompactJSON(raw)
}

var shimLocalGenerationFields = map[string]struct{}{
	"temperature":       {},
	"top_p":             {},
	"frequency_penalty": {},
	"presence_penalty":  {},
	"stop":              {},
	"reasoning":         {},
	"max_output_tokens": {},
}

var shimLocalStateBaseFields = map[string]struct{}{
	"model":                {},
	"input":                {},
	"store":                {},
	"stream":               {},
	"previous_response_id": {},
	"conversation":         {},
	"instructions":         {},
}

func (h *responseHandler) shouldProxyCreate(ctx context.Context, request CreateResponseRequest) (bool, error) {
	if request.Conversation != "" {
		return false, nil
	}

	if request.PreviousResponseID != "" {
		_, err := h.service.Get(ctx, request.PreviousResponseID)
		if err == nil {
			return false, nil
		}
		mapped := service.MapStorageError(err)
		if errors.Is(mapped, service.ErrNotFound) {
			return true, nil
		}
		return false, err
	}

	return true, nil
}

func decodeRawFields(raw []byte) (map[string]json.RawMessage, error) {
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func buildGenerationOptions(rawFields map[string]json.RawMessage) map[string]json.RawMessage {
	options := make(map[string]json.RawMessage)
	for key, value := range rawFields {
		if _, ok := shimLocalGenerationFields[key]; ok {
			options[key] = value
		}
	}
	return options
}

func supportsLocalShimState(rawFields map[string]json.RawMessage) bool {
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		return false
	}
	return true
}

func shouldFallbackLocalState(err error) bool {
	mapped := service.MapGeneratorError(err)
	return errors.Is(mapped, service.ErrUpstreamFailure) || errors.Is(mapped, service.ErrUpstreamTimeout)
}

func (h *responseHandler) proxyCreateWithShadowStore(w http.ResponseWriter, r *http.Request, request CreateResponseRequest, rawBody []byte, requestJSON string) {
	cloned := r.Clone(r.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(rawBody))
	cloned.ContentLength = int64(len(rawBody))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rawBody)), nil
	}
	if cloned.Header.Get("X-Request-Id") == "" {
		cloned.Header.Set("X-Request-Id", RequestIDFromContext(cloned.Context()))
	}

	response, err := h.proxy.client.Proxy(cloned.Context(), cloned)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}

	copyResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}

	prepared, input, ok := prepareShadowStore(request, requestJSON)
	if !ok {
		return
	}

	parsed, err := domain.ParseUpstreamResponse(body)
	if err != nil || parsed.OutputText == "" || len(parsed.Output) == 0 {
		return
	}

	_, err = h.service.SaveExternalResponse(r.Context(), prepared, input, parsed)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "shadow store failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
	}
}

func prepareShadowStore(request CreateResponseRequest, requestJSON string) (service.PreparedResponseContext, service.CreateResponseInput, bool) {
	input := service.CreateResponseInput{
		Model:        request.Model,
		Input:        request.Input,
		Store:        request.Store,
		Stream:       request.Stream,
		Instructions: request.Instructions,
		RequestJSON:  requestJSON,
	}
	if input.Model == "" {
		return service.PreparedResponseContext{}, input, false
	}

	normalizedInput, err := domain.NormalizeInput(input.Input)
	if err != nil {
		return service.PreparedResponseContext{}, input, false
	}

	return service.PreparedResponseContext{
		NormalizedInput: normalizedInput,
	}, input, true
}

func buildUpstreamResponsesBody(rawFields map[string]json.RawMessage, contextItems []domain.MessageItem) ([]byte, error) {
	payload := make(map[string]any, len(rawFields)+1)
	for key, raw := range rawFields {
		switch key {
		case "input", "previous_response_id", "conversation", "instructions":
			continue
		case "store":
			payload[key] = false
		default:
			payload[key] = json.RawMessage(raw)
		}
	}
	if _, ok := payload["store"]; !ok {
		payload["store"] = false
	}
	payload["input"] = buildUpstreamInputItems(contextItems)
	return json.Marshal(payload)
}

func buildUpstreamInputItems(items []domain.MessageItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		content := domain.MessageText(item)
		if len(out) == 0 || out[len(out)-1]["role"] != item.Role {
			out = append(out, map[string]any{
				"role":    item.Role,
				"content": content,
			})
			continue
		}

		if content == "" {
			continue
		}
		if previous, ok := out[len(out)-1]["content"].(string); ok && previous != "" {
			out[len(out)-1]["content"] = previous + "\n\n" + content
			continue
		}
		out[len(out)-1]["content"] = content
	}
	return out
}
