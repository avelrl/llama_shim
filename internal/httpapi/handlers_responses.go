package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/service"
	"llama_shim/internal/websearch"
)

type ResponseService interface {
	Create(ctx context.Context, input service.CreateResponseInput) (domain.Response, error)
	CreateStream(ctx context.Context, input service.CreateResponseInput, hooks service.StreamHooks) (domain.Response, error)
	CountInputTokens(ctx context.Context, input service.CreateResponseInput) (domain.ResponseInputTokens, error)
	Compact(ctx context.Context, input service.CreateResponseInput) (domain.ResponseCompaction, error)
	Get(ctx context.Context, id string) (domain.Response, error)
	HasPreviousResponse(ctx context.Context, id string) (bool, error)
	GetInputItems(ctx context.Context, id string) ([]domain.Item, error)
	Delete(ctx context.Context, id string) (domain.ResponseDeletion, error)
	Refresh(ctx context.Context, response domain.Response) (domain.Response, error)
	PrepareCreateContext(ctx context.Context, input service.CreateResponseInput) (service.PreparedResponseContext, error)
	SaveExternalResponse(ctx context.Context, prepared service.PreparedResponseContext, input service.CreateResponseInput, response domain.Response) (domain.Response, error)
	SaveReplayArtifacts(ctx context.Context, responseID string, artifacts []domain.ResponseReplayArtifact) error
	GetReplayArtifacts(ctx context.Context, responseID string) ([]domain.ResponseReplayArtifact, error)
}

type responseHandler struct {
	logger                       *slog.Logger
	service                      *service.ResponseService
	proxy                        *proxyHandler
	metrics                      *Metrics
	serviceLimits                ServiceLimits
	retrievalGate                *concurrencyGate
	codeInterpreterGate          *concurrencyGate
	responsesMode                string
	customToolsMode              string
	codexCompatibilityEnabled    bool
	forceCodexToolChoiceRequired bool
	webSearchProvider            websearch.Provider
	imageGenerationProvider      imagegen.Provider
	localComputer                LocalComputerRuntimeConfig
	localCodeInterpreter         LocalCodeInterpreterRuntimeConfig
	localCodeInterpreterFiles    LocalCodeInterpreterFileStore
	localCodeInterpreterSessions LocalCodeInterpreterSessionStore
}

func newResponseHandler(logger *slog.Logger, service *service.ResponseService, proxy *proxyHandler, responsesMode string, customToolsMode string, codexCompatibilityEnabled bool, forceCodexToolChoiceRequired bool, webSearchProvider websearch.Provider, imageGenerationProvider imagegen.Provider, localComputer LocalComputerRuntimeConfig, localCodeInterpreter LocalCodeInterpreterRuntimeConfig, localCodeInterpreterFiles LocalCodeInterpreterFileStore, localCodeInterpreterSessions LocalCodeInterpreterSessionStore, metrics *Metrics, serviceLimits ServiceLimits, retrievalGate *concurrencyGate, codeInterpreterGate *concurrencyGate) *responseHandler {
	return &responseHandler{
		logger:                       logger,
		service:                      service,
		proxy:                        proxy,
		metrics:                      metrics,
		serviceLimits:                normalizeServiceLimits(serviceLimits),
		retrievalGate:                retrievalGate,
		codeInterpreterGate:          codeInterpreterGate,
		responsesMode:                normalizeResponsesMode(responsesMode),
		customToolsMode:              customToolsMode,
		codexCompatibilityEnabled:    codexCompatibilityEnabled,
		forceCodexToolChoiceRequired: forceCodexToolChoiceRequired,
		webSearchProvider:            webSearchProvider,
		imageGenerationProvider:      imageGenerationProvider,
		localComputer:                localComputer,
		localCodeInterpreter:         localCodeInterpreter,
		localCodeInterpreterFiles:    localCodeInterpreterFiles,
		localCodeInterpreterSessions: localCodeInterpreterSessions,
	}
}

func (h *responseHandler) create(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	request, rawFields, requestJSON, err := decodeCreateResponseRequestBody(rawBody, false)
	if err != nil {
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			h.writeError(w, r, err)
			return
		}
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}
	streamOptions, err := parseCreateResponseStreamOptions(request.Stream, request.StreamOptions)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	hasLocalState, err := h.hasLocalCreateState(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if request.PreviousResponseID != "" && !hasLocalState {
		if request.Stream != nil && *request.Stream {
			h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
			return
		}
		h.proxyCreateWithShadowStore(w, r, request, rawBody, requestJSON, rawFields)
		return
	}

	localMCPState := h.hasLocalMCPState(r.Context(), request)
	createRoute := selectResponsesCreateRoute(h.responsesMode, buildResponsesCreateRouteInputs(
		hasLocalState,
		rawFields,
		h.webSearchProvider,
		h.imageGenerationProvider,
		h.localComputer,
		h.localCodeInterpreter,
		localMCPState,
	))
	generationOptions := buildGenerationOptions(rawFields)
	if request.Stream != nil && *request.Stream {
		switch createRoute {
		case responsesCreateRouteProxy:
			h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
		case responsesCreateRouteLocalWebSearch:
			response, err := h.createLocalWebSearchResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				if shouldFallbackLocalState(h.responsesMode, err) {
					if hasLocalState {
						h.createStreamViaUpstream(w, r, request, requestJSON, rawFields, streamOptions)
						return
					}
					h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
					return
				}
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			rawResponse, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				h.writeError(w, r, marshalErr)
				return
			}
			if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local web search stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalWebSearchDisabled:
			h.writeError(w, r, localWebSearchDisabledError())
			return
		case responsesCreateRouteLocalImageGeneration:
			response, artifacts, err := h.createLocalImageGenerationResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				if shouldFallbackLocalState(h.responsesMode, err) {
					if hasLocalState {
						h.createStreamViaUpstream(w, r, request, requestJSON, rawFields, streamOptions)
						return
					}
					h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
					return
				}
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			if err := writeResponseReplayAsSSE(w, response, artifacts, 0, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local image generation stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalComputer:
			response, err := h.createLocalComputerResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			rawResponse, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				h.writeError(w, r, marshalErr)
				return
			}
			if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local computer stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalFileSearch:
			response, err := h.createLocalFileSearchResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			rawResponse, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				h.writeError(w, r, marshalErr)
				return
			}
			if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local file search stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalMCP:
			response, err := h.createLocalMCPResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				if shouldFallbackLocalState(h.responsesMode, err) {
					if hasLocalState {
						h.createStreamViaUpstream(w, r, request, requestJSON, rawFields, streamOptions)
						return
					}
					h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
					return
				}
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			rawResponse, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				h.writeError(w, r, marshalErr)
				return
			}
			if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local MCP stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalToolSearch:
			response, err := h.createLocalToolSearchResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			rawResponse, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				h.writeError(w, r, marshalErr)
				return
			}
			if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local tool search stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalCodeInterpreter:
			response, err := h.createLocalCodeInterpreterResponse(r.Context(), request, requestJSON, rawFields)
			if err != nil {
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
				return
			}
			rawResponse, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				h.writeError(w, r, marshalErr)
				return
			}
			if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
				h.logger.WarnContext(r.Context(), "local code interpreter stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
			return
		case responsesCreateRouteLocalToolLoop:
			response, err := h.createLocalToolLoopResponse(r.Context(), request, requestJSON, rawFields)
			if err == nil {
				rawResponse, marshalErr := json.Marshal(response)
				if marshalErr != nil {
					h.writeError(w, r, marshalErr)
					return
				}
				if err := writeCompletedResponseAsSSE(r.Context(), h.logger, w, rawResponse, customToolTransportPlan{}, streamOptions.IncludeObfuscation); err != nil && !shouldIgnoreStreamProxyError(err) {
					h.logger.WarnContext(r.Context(), "local tool loop stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
				}
				return
			}
			if shouldFallbackLocalState(h.responsesMode, err) {
				if hasLocalState {
					h.createStreamViaUpstream(w, r, request, requestJSON, rawFields, streamOptions)
					return
				}
				h.proxyCreateStream(w, r, request, requestJSON, rawFields, streamOptions)
				return
			}
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
		case responsesCreateRouteLocalComputerDisabled:
			h.writeError(w, r, localComputerDisabledError())
			return
		case responsesCreateRouteLocalImageGenerationDisabled:
			h.writeError(w, r, localImageGenerationDisabledError())
			return
		case responsesCreateRouteLocalCodeInterpreterDisabled:
			h.writeError(w, r, localCodeInterpreterDisabledError())
			return
		case responsesCreateRouteLocalState:
			if err := h.createStream(w, r, request, requestJSON, generationOptions, streamOptions); err != nil {
				if shouldFallbackLocalState(h.responsesMode, err) {
					h.createStreamViaUpstream(w, r, request, requestJSON, rawFields, streamOptions)
					return
				}
				h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			}
		case responsesCreateRouteLocalStateViaUpstream:
			h.createStreamViaUpstream(w, r, request, requestJSON, rawFields, streamOptions)
		case responsesCreateRouteLocalOnlyUnsupported:
			h.writeError(w, r, newLocalOnlyUnsupportedFieldsError(rawFields))
		}
		return
	}

	switch createRoute {
	case responsesCreateRouteProxy:
		h.proxyCreateWithShadowStore(w, r, request, rawBody, requestJSON, rawFields)
		return
	case responsesCreateRouteLocalWebSearch:
		response, err := h.createLocalWebSearchResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			if shouldFallbackLocalState(h.responsesMode, err) {
				var response domain.Response
				var fallbackErr error
				if hasLocalState {
					response, fallbackErr = h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
				} else {
					response, fallbackErr = h.createProxyResponseViaUpstream(r.Context(), request, requestJSON, rawFields)
				}
				if fallbackErr == nil {
					WriteJSON(w, http.StatusOK, response)
					return
				}
				err = fallbackErr
			}
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalImageGeneration:
		response, _, err := h.createLocalImageGenerationResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			if shouldFallbackLocalState(h.responsesMode, err) {
				var response domain.Response
				var fallbackErr error
				if hasLocalState {
					response, fallbackErr = h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
				} else {
					response, fallbackErr = h.createProxyResponseViaUpstream(r.Context(), request, requestJSON, rawFields)
				}
				if fallbackErr == nil {
					WriteJSON(w, http.StatusOK, response)
					return
				}
				err = fallbackErr
			}
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalFileSearch:
		response, err := h.createLocalFileSearchResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalWebSearchDisabled:
		h.writeError(w, r, localWebSearchDisabledError())
		return
	case responsesCreateRouteLocalComputer:
		response, err := h.createLocalComputerResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalComputerDisabled:
		h.writeError(w, r, localComputerDisabledError())
		return
	case responsesCreateRouteLocalMCP:
		response, err := h.createLocalMCPResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			if shouldFallbackLocalState(h.responsesMode, err) {
				var response domain.Response
				var fallbackErr error
				if hasLocalState {
					response, fallbackErr = h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
				} else {
					response, fallbackErr = h.createProxyResponseViaUpstream(r.Context(), request, requestJSON, rawFields)
				}
				if fallbackErr == nil {
					WriteJSON(w, http.StatusOK, response)
					return
				}
				err = fallbackErr
			}
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalToolSearch:
		response, err := h.createLocalToolSearchResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalCodeInterpreter:
		response, err := h.createLocalCodeInterpreterResponse(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalToolLoop:
		response, err := h.createLocalToolLoopResponse(r.Context(), request, requestJSON, rawFields)
		if err == nil {
			WriteJSON(w, http.StatusOK, response)
			return
		}
		if shouldFallbackLocalState(h.responsesMode, err) {
			var response domain.Response
			var fallbackErr error
			if hasLocalState {
				response, fallbackErr = h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
			} else {
				response, fallbackErr = h.createProxyResponseViaUpstream(r.Context(), request, requestJSON, rawFields)
			}
			if fallbackErr == nil {
				WriteJSON(w, http.StatusOK, response)
				return
			}
			err = fallbackErr
		}
		h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
		return
	case responsesCreateRouteLocalState:
		response, err := h.service.Create(r.Context(), service.CreateResponseInput{
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
			GenerationOptions:  generationOptions,
		})
		if err == nil {
			WriteJSON(w, http.StatusOK, response)
			return
		}
		if shouldFallbackLocalState(h.responsesMode, err) {
			response, fallbackErr := h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
			if fallbackErr == nil {
				WriteJSON(w, http.StatusOK, response)
				return
			}
			err = fallbackErr
		}
		h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
		return
	case responsesCreateRouteLocalImageGenerationDisabled:
		h.writeError(w, r, localImageGenerationDisabledError())
		return
	case responsesCreateRouteLocalCodeInterpreterDisabled:
		h.writeError(w, r, localCodeInterpreterDisabledError())
		return
	case responsesCreateRouteLocalStateViaUpstream:
		response, err := h.createLocalStateViaUpstream(r.Context(), request, requestJSON, rawFields)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, response)
		return
	case responsesCreateRouteLocalOnlyUnsupported:
		h.writeError(w, r, newLocalOnlyUnsupportedFieldsError(rawFields))
		return
	}

	h.proxyCreateWithShadowStore(w, r, request, rawBody, requestJSON, rawFields)
}

func (h *responseHandler) inputTokens(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	request, rawFields, requestJSON, err := decodeCreateResponseRequestBody(rawBody, true)
	if err != nil {
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			h.writeError(w, r, err)
			return
		}
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}

	hasLocalState, err := h.hasLocalCreateState(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	localSupported := supportsLocalDerivedResponsesState(rawFields)

	switch {
	case h.responsesMode == config.ResponsesModePreferUpstream && !hasLocalState:
		h.proxyBufferedJSONRequest(w, r, rawBody)
		return
	case localSupported:
		response, err := h.service.CountInputTokens(r.Context(), service.CreateResponseInput{
			Model:              request.Model,
			Input:              request.Input,
			TextConfig:         request.Text,
			Metadata:           request.Metadata,
			PreviousResponseID: request.PreviousResponseID,
			ConversationID:     request.Conversation,
			Instructions:       request.Instructions,
			RequestJSON:        requestJSON,
		})
		if err == nil {
			WriteJSON(w, http.StatusOK, response)
			return
		}
		if !hasLocalState && shouldFallbackLocalState(h.responsesMode, err) {
			h.proxyBufferedJSONRequest(w, r, rawBody)
			return
		}
		h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
		return
	case hasLocalState:
		h.writeError(w, r, newLocalStateUnsupportedDerivedFieldsError("/v1/responses/input_tokens", rawFields))
		return
	case h.responsesMode == config.ResponsesModeLocalOnly:
		h.writeError(w, r, newLocalOnlyUnsupportedDerivedFieldsError("/v1/responses/input_tokens", rawFields))
		return
	default:
		h.proxyBufferedJSONRequest(w, r, rawBody)
	}
}

func (h *responseHandler) compact(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	request, rawFields, requestJSON, err := decodeCreateResponseRequestBody(rawBody, false)
	if err != nil {
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			h.writeError(w, r, err)
			return
		}
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}

	hasLocalState, err := h.hasLocalCreateState(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	localSupported := supportsLocalDerivedResponsesState(rawFields)

	switch {
	case h.responsesMode == config.ResponsesModePreferUpstream && !hasLocalState:
		h.proxyBufferedJSONRequest(w, r, rawBody)
		return
	case localSupported:
		response, err := h.service.Compact(r.Context(), service.CreateResponseInput{
			Model:              request.Model,
			Input:              request.Input,
			TextConfig:         request.Text,
			Metadata:           request.Metadata,
			PreviousResponseID: request.PreviousResponseID,
			ConversationID:     request.Conversation,
			Instructions:       request.Instructions,
			RequestJSON:        requestJSON,
		})
		if err == nil {
			WriteJSON(w, http.StatusOK, response)
			return
		}
		if !hasLocalState && shouldFallbackLocalState(h.responsesMode, err) {
			h.proxyBufferedJSONRequest(w, r, rawBody)
			return
		}
		h.writeError(w, r, normalizeLocalOnlyCreateError(h.responsesMode, err))
		return
	case hasLocalState:
		h.writeError(w, r, newLocalStateUnsupportedDerivedFieldsError("/v1/responses/compact", rawFields))
		return
	case h.responsesMode == config.ResponsesModeLocalOnly:
		h.writeError(w, r, newLocalOnlyUnsupportedDerivedFieldsError("/v1/responses/compact", rawFields))
		return
	default:
		h.proxyBufferedJSONRequest(w, r, rawBody)
	}
}

func (h *responseHandler) createLocalStateViaUpstream(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
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
		return domain.Response{}, err
	}

	upstreamBody, plan, err := buildUpstreamResponsesBody(rawFields, prepared.ContextItems, prepared.NormalizedInput, prepared.ToolCallRefs, h.customToolsMode, h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		return domain.Response{}, err
	}
	logCustomToolTransport(ctx, h.logger, rawFields, upstreamBody, plan, h.codexCompatibilityEnabled)

	rawResponse, usedFallback, err := h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	if err != nil && shouldRetryCustomToolsWithBridgeError(err, plan) {
		upstreamBody, plan, err = h.buildBridgedUpstreamResponsesBody(ctx, rawFields, prepared.ContextItems, prepared.NormalizedInput, prepared.ToolCallRefs)
		if err != nil {
			return domain.Response{}, err
		}
		rawResponse, usedFallback, err = h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	}
	if err != nil && shouldRetryLocalStateWithDirectProxyError(err, request) {
		response, proxyErr := h.createProxyResponseViaUpstream(ctx, request, requestJSON, rawFields)
		if proxyErr == nil {
			return response, nil
		}
		if h.logger != nil {
			h.logger.WarnContext(ctx, "previous_response_id direct-proxy fallback failed after local replay validation error",
				"request_id", RequestIDFromContext(ctx),
				"err", proxyErr,
			)
		}
	}
	if err != nil && shouldRetryResponsesInputAsStringError(err, upstreamBody) {
		upstreamBody, err = h.buildStringifiedResponsesBody(ctx, upstreamBody)
		if err != nil {
			return domain.Response{}, err
		}
		rawResponse, usedFallback, err = h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	}
	if err != nil && shouldRetryCustomToolsWithBridgeError(err, plan) {
		upstreamBody, plan, err = h.buildBridgedCurrentResponsesBody(ctx, upstreamBody)
		if err != nil {
			return domain.Response{}, err
		}
		rawResponse, usedFallback, err = h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	}
	if err != nil {
		return domain.Response{}, err
	}

	_, response, err := finalizeUpstreamResponse(rawResponse, plan, usedFallback)
	if err != nil {
		return domain.Response{}, err
	}

	return h.service.SaveExternalResponse(ctx, prepared, input, response)
}

func (h *responseHandler) createProxyResponseViaUpstream(ctx context.Context, request CreateResponseRequest, requestJSON string, rawFields map[string]json.RawMessage) (domain.Response, error) {
	upstreamBody, plan, err := remapCustomToolsPayload(rawFields, h.customToolsMode, h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		return domain.Response{}, err
	}
	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying previous_response_id request with upstream-managed state after local replay validation failure",
			"request_id", RequestIDFromContext(ctx),
		)
	}
	logCustomToolTransport(ctx, h.logger, rawFields, upstreamBody, plan, h.codexCompatibilityEnabled)

	rawResponse, usedFallback, err := h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	if err != nil && shouldRetryCustomToolsWithBridgeError(err, plan) {
		upstreamBody, plan, err = h.buildBridgedProxyResponsesBody(ctx, rawFields)
		if err != nil {
			return domain.Response{}, err
		}
		rawResponse, usedFallback, err = h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	}
	if err != nil && shouldRetryResponsesInputAsStringError(err, upstreamBody) {
		upstreamBody, err = h.buildStringifiedResponsesBody(ctx, upstreamBody)
		if err != nil {
			return domain.Response{}, err
		}
		rawResponse, usedFallback, err = h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	}
	if err != nil && shouldRetryCustomToolsWithBridgeError(err, plan) {
		upstreamBody, plan, err = h.buildBridgedCurrentResponsesBody(ctx, upstreamBody)
		if err != nil {
			return domain.Response{}, err
		}
		rawResponse, usedFallback, err = h.createResponseWithToolChoiceFallback(ctx, upstreamBody, plan)
	}
	if err != nil {
		return domain.Response{}, err
	}

	_, response, err := finalizeUpstreamResponse(rawResponse, plan, usedFallback)
	if err != nil {
		return domain.Response{}, err
	}

	prepared, input, ok := prepareShadowStore(ctx, h.service.PrepareCreateContext, request, requestJSON)
	if !ok {
		if response.PreviousResponseID == "" && request.PreviousResponseID != "" {
			response.PreviousResponseID = request.PreviousResponseID
		}
		if response.Conversation == nil && request.Conversation != "" {
			response.Conversation = domain.NewConversationReference(request.Conversation)
		}
		return response, nil
	}

	stored, err := h.service.SaveExternalResponse(ctx, prepared, input, response)
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "shadow store failed", "request_id", RequestIDFromContext(ctx), "err", err)
		}
		if response.PreviousResponseID == "" && request.PreviousResponseID != "" {
			response.PreviousResponseID = request.PreviousResponseID
		}
		if response.Conversation == nil && request.Conversation != "" {
			response.Conversation = domain.NewConversationReference(request.Conversation)
		}
		return response, nil
	}
	return stored, nil
}

func (h *responseHandler) createResponseWithToolChoiceFallback(ctx context.Context, upstreamBody []byte, plan customToolTransportPlan) ([]byte, bool, error) {
	rawResponse, err := h.proxy.client.CreateResponse(ctx, upstreamBody)
	if err == nil {
		return rawResponse, false, nil
	}
	if !shouldRetryToolChoiceWithAutoError(err, plan) {
		return nil, false, err
	}

	// Some upstreams only accept tool_choice=auto even when the caller asked for
	// required semantics. Retry with auto, then validate the final output against
	// the original contract before storing or returning it.
	rawResponse, err = h.retryResponseWithAuto(ctx, upstreamBody, plan)
	if err != nil {
		return nil, true, err
	}
	return rawResponse, true, nil
}

func (h *responseHandler) retryResponseWithAuto(ctx context.Context, upstreamBody []byte, plan customToolTransportPlan) ([]byte, error) {
	retryBody, err := rewriteToolChoiceRetryBody(upstreamBody)
	if err != nil {
		return nil, err
	}

	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying responses request with tool_choice=auto after unsupported tool_choice",
			"request_id", RequestIDFromContext(ctx),
			"contract_mode", plan.ToolChoiceContract.Mode,
			"contract_name", plan.ToolChoiceContract.Name,
			"contract_namespace", plan.ToolChoiceContract.Namespace,
		)
	}

	return h.proxy.client.CreateResponse(ctx, retryBody)
}

func (h *responseHandler) buildBridgedProxyResponsesBody(ctx context.Context, rawFields map[string]json.RawMessage) ([]byte, customToolTransportPlan, error) {
	body, plan, err := remapCustomToolsPayload(rawFields, string(customToolsModeBridge), h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying responses request with bridged custom tools after native custom-tool rejection",
			"request_id", RequestIDFromContext(ctx),
		)
	}
	logCustomToolTransport(ctx, h.logger, rawFields, body, plan, h.codexCompatibilityEnabled)
	return body, plan, nil
}

func (h *responseHandler) buildBridgedCurrentResponsesBody(ctx context.Context, upstreamBody []byte) ([]byte, customToolTransportPlan, error) {
	rawFields, err := decodeRawFields(upstreamBody)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	body, plan, err := remapCustomToolsPayload(rawFields, string(customToolsModeBridge), h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying responses request with bridged custom tools after native custom-tool rejection",
			"request_id", RequestIDFromContext(ctx),
		)
	}
	logCustomToolTransport(ctx, h.logger, rawFields, body, plan, h.codexCompatibilityEnabled)
	return body, plan, nil
}

func (h *responseHandler) buildStringifiedResponsesBody(ctx context.Context, upstreamBody []byte) ([]byte, error) {
	body, err := rewriteResponsesInputAsStringBody(upstreamBody)
	if err != nil {
		return nil, err
	}
	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying responses request with stringified input after structured-input validation failure",
			"request_id", RequestIDFromContext(ctx),
		)
	}
	return body, nil
}

func (h *responseHandler) buildBridgedUpstreamResponsesBody(ctx context.Context, rawFields map[string]json.RawMessage, contextItems []domain.Item, currentInput []domain.Item, refs map[string]domain.ToolCallReference) ([]byte, customToolTransportPlan, error) {
	body, plan, err := buildUpstreamResponsesBody(rawFields, contextItems, currentInput, refs, string(customToolsModeBridge), h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	if h.logger != nil {
		h.logger.InfoContext(ctx, "retrying responses request with bridged custom tools after native custom-tool rejection",
			"request_id", RequestIDFromContext(ctx),
		)
	}
	logCustomToolTransport(ctx, h.logger, rawFields, body, plan, h.codexCompatibilityEnabled)
	return body, plan, nil
}

func finalizeUpstreamResponse(rawResponse []byte, plan customToolTransportPlan, enforceContract bool) ([]byte, domain.Response, error) {
	responseBody, err := normalizeUpstreamResponseBody(rawResponse, plan)
	if err != nil {
		return nil, domain.Response{}, err
	}

	response, err := domain.ParseUpstreamResponse(responseBody)
	if err != nil {
		return nil, domain.Response{}, err
	}
	response = annotateResponseCustomToolMetadata(response, plan)
	if response.OutputText == "" && len(response.Output) == 0 {
		return nil, domain.Response{}, &domain.ValidationError{
			Param:   "output",
			Message: "upstream response did not include output items",
		}
	}
	if enforceContract {
		if err := enforceToolChoiceContract(response, plan.ToolChoiceContract); err != nil {
			return nil, domain.Response{}, err
		}
	}

	return responseBody, response, nil
}

func (h *responseHandler) createStream(w http.ResponseWriter, r *http.Request, request CreateResponseRequest, requestJSON string, generationOptions map[string]json.RawMessage, streamOptions responseStreamOptions) error {
	var (
		emitter    *responseStreamEmitter
		responseID string
		itemID     string
	)

	response, err := h.service.CreateStream(r.Context(), service.CreateResponseInput{
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
		GenerationOptions:  generationOptions,
	}, service.StreamHooks{
		OnCreated: func(response domain.Response) error {
			var err error
			emitter, err = newResponseStreamEmitter(w, streamOptions.IncludeObfuscation)
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
			if err := emitter.responseInProgress(response); err != nil {
				return err
			}
			if err := emitter.outputItemAdded(itemID); err != nil {
				return err
			}
			return emitter.contentPartAdded(itemID)
		},
		OnDelta: func(delta string) error {
			return emitter.outputTextDelta(responseID, itemID, delta)
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		if emitter == nil {
			return err
		}
		_, payload := MapError(r.Context(), h.logger, err)
		_ = emitter.error(payload)
		return nil
	}

	response, err = normalizeResponseForStreaming(response, map[int]string{0: itemID})
	if err != nil {
		return err
	}
	if err := emitter.outputTextDone(responseID, itemID, response.OutputText); err != nil {
		return nil
	}
	if err := emitter.contentPartDone(itemID, response.OutputText); err != nil {
		return nil
	}
	if err := emitter.outputItemDone(itemID, response.Output[0]); err != nil {
		return nil
	}
	if err := emitter.responseCompleted(response); err != nil {
		return nil
	}
	return emitter.done()
}

func (h *responseHandler) get(w http.ResponseWriter, r *http.Request) {
	query, err := parseResponseRetrieveQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

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
	if query.Stream {
		artifacts, err := h.service.GetReplayArtifacts(r.Context(), id)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		if err := writeResponseReplayAsSSE(w, response, artifacts, query.StartingAfter, query.IncludeObfuscation); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if h.logger != nil {
				h.logger.WarnContext(r.Context(), "response replay stream failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
			}
		}
		return
	}
	WriteJSON(w, http.StatusOK, response)
}

func (h *responseHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	response, err := h.service.Delete(r.Context(), id)
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

func (h *responseHandler) cancel(w http.ResponseWriter, r *http.Request) {
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
	if response.Background == nil || !*response.Background {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "only background responses can be cancelled", "background")
		return
	}

	cloned := r.Clone(r.Context())
	if cloned.Header.Get("X-Request-Id") == "" {
		cloned.Header.Set("X-Request-Id", RequestIDFromContext(cloned.Context()))
	}

	upstreamResp, err := h.proxy.client.Proxy(cloned.Context(), cloned)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer upstreamResp.Body.Close()

	body, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if canonical, ok, err := canonicalizeAPIErrorBody(upstreamResp.StatusCode, body); err == nil && ok {
		body = canonical
	}
	if upstreamResp.StatusCode >= 200 && upstreamResp.StatusCode < 300 {
		parsed, err := domain.ParseUpstreamResponse(body)
		if err == nil {
			refreshed, refreshErr := h.service.Refresh(r.Context(), parsed)
			if refreshErr == nil {
				encoded, marshalErr := json.Marshal(refreshed)
				if marshalErr == nil {
					body = encoded
				}
			} else if h.logger != nil {
				h.logger.ErrorContext(r.Context(), "response refresh failed after upstream cancel", "request_id", RequestIDFromContext(r.Context()), "err", refreshErr)
			}
		}
	}

	copyResponseHeaders(w.Header(), upstreamResp.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(upstreamResp.StatusCode)
	_, _ = w.Write(body)
}

func (h *responseHandler) getInputItems(w http.ResponseWriter, r *http.Request) {
	query, err := parseResponseInputItemsQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	id := r.PathValue("id")
	items, err := h.service.GetInputItems(r.Context(), id)
	if err != nil {
		mapped := service.MapStorageError(err)
		if errors.Is(mapped, service.ErrNotFound) {
			h.proxy.forward(w, r)
			return
		}
		h.writeError(w, r, err)
		return
	}

	response, err := paginateResponseInputItems(items, query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, response)
}

func (h *responseHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, payload := MapError(r.Context(), h.logger, err)
	WriteJSON(w, status, apiErrorPayload{Error: payload})
}

func (h *responseHandler) proxyBufferedJSONRequest(w http.ResponseWriter, r *http.Request, body []byte) {
	resp, err := h.proxyResponseRequest(r, body)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if canonical, ok, err := canonicalizeAPIErrorBody(resp.StatusCode, responseBody); err == nil && ok {
		responseBody = canonical
	}

	copyResponseHeaders(w.Header(), resp.Header)
	if len(responseBody) > 0 {
		w.Header().Del("Content-Length")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(responseBody)
}

func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
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

func decodeCreateResponseRequestBody(rawBody []byte, allowEmpty bool) (CreateResponseRequest, map[string]json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) == 0 {
		if !allowEmpty {
			return CreateResponseRequest{}, nil, "", io.EOF
		}
		return CreateResponseRequest{}, map[string]json.RawMessage{}, `{}`, nil
	}

	var payload struct {
		Model              string          `json:"model"`
		Input              json.RawMessage `json:"input"`
		Text               json.RawMessage `json:"text,omitempty"`
		Metadata           json.RawMessage `json:"metadata,omitempty"`
		ContextManagement  json.RawMessage `json:"context_management,omitempty"`
		Store              *bool           `json:"store,omitempty"`
		Stream             *bool           `json:"stream,omitempty"`
		StreamOptions      json.RawMessage `json:"stream_options,omitempty"`
		Background         *bool           `json:"background,omitempty"`
		PreviousResponseID string          `json:"previous_response_id,omitempty"`
		Conversation       json.RawMessage `json:"conversation,omitempty"`
		Instructions       string          `json:"instructions,omitempty"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return CreateResponseRequest{}, nil, "", err
	}

	rawFields, err := decodeRawFields(trimmed)
	if err != nil {
		return CreateResponseRequest{}, nil, "", err
	}
	if err := validateMCPToolDefinitions(rawFields); err != nil {
		return CreateResponseRequest{}, nil, "", err
	}
	if err := validateCreateResponseContextManagement(payload.ContextManagement); err != nil {
		return CreateResponseRequest{}, nil, "", err
	}
	requestJSON, err := compactBody(trimmed)
	if err != nil {
		return CreateResponseRequest{}, nil, "", err
	}
	requestJSON = domain.SanitizeResponseRequestSurfaceJSON(requestJSON)
	conversationID, err := decodeCreateResponseConversationID(payload.Conversation)
	if err != nil {
		return CreateResponseRequest{}, nil, "", err
	}

	return CreateResponseRequest{
		Model:              payload.Model,
		Input:              payload.Input,
		Text:               payload.Text,
		Metadata:           payload.Metadata,
		ContextManagement:  payload.ContextManagement,
		Store:              payload.Store,
		Stream:             payload.Stream,
		StreamOptions:      payload.StreamOptions,
		Background:         payload.Background,
		PreviousResponseID: payload.PreviousResponseID,
		Conversation:       conversationID,
		Instructions:       payload.Instructions,
	}, rawFields, requestJSON, nil
}

func decodeCreateResponseConversationID(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}

	var conversationID string
	if err := json.Unmarshal(trimmed, &conversationID); err == nil {
		return strings.TrimSpace(conversationID), nil
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(trimmed, &payload); err == nil {
		if strings.TrimSpace(payload.ID) == "" {
			return "", domain.NewValidationError("conversation", "conversation.id is required")
		}
		return strings.TrimSpace(payload.ID), nil
	}

	return "", domain.NewValidationError("conversation", "conversation must be a string or object with id")
}

func validateCreateResponseContextManagement(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return domain.NewValidationError("context_management", "context_management must be an array")
	}
	for _, entry := range entries {
		if err := validateCreateResponseContextManagementEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

func validateCreateResponseContextManagementEntry(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return domain.NewValidationError("context_management", "context_management entries must be objects")
	}

	typeRaw, ok := fields["type"]
	if !ok {
		return domain.NewValidationError("context_management", "context_management entries require type")
	}

	var policyType string
	if err := json.Unmarshal(typeRaw, &policyType); err != nil || strings.TrimSpace(policyType) == "" {
		return domain.NewValidationError("context_management", "context_management entries require type")
	}

	switch policyType {
	case "compaction":
		for key := range fields {
			if key == "type" || key == "compact_threshold" {
				continue
			}
			return domain.NewValidationError("context_management", "unsupported context_management field "+`"`+key+`"`+" in compaction policy")
		}

		var payload struct {
			CompactThreshold *int64 `json:"compact_threshold"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return domain.NewValidationError("context_management", "context_management entries must be objects")
		}
		if payload.CompactThreshold == nil || *payload.CompactThreshold <= 0 {
			return domain.NewValidationError("context_management", "context_management compaction policy requires compact_threshold > 0")
		}
		return nil
	default:
		return domain.NewValidationError("context_management", "unsupported context_management type "+`"`+policyType+`"`)
	}
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
	"text":                 {},
	"metadata":             {},
	"store":                {},
	"stream":               {},
	"stream_options":       {},
	"previous_response_id": {},
	"conversation":         {},
	"instructions":         {},
}

var shimLocalDerivedResponsesBaseFields = map[string]struct{}{
	"model":                {},
	"input":                {},
	"text":                 {},
	"metadata":             {},
	"previous_response_id": {},
	"conversation":         {},
	"instructions":         {},
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

func supportsLocalDerivedResponsesState(rawFields map[string]json.RawMessage) bool {
	for key := range rawFields {
		if _, ok := shimLocalDerivedResponsesBaseFields[key]; ok {
			continue
		}
		return false
	}
	return true
}

func unsupportedLocalShimFields(rawFields map[string]json.RawMessage) []string {
	unsupported := make([]string, 0)
	for key := range rawFields {
		if _, ok := shimLocalStateBaseFields[key]; ok {
			continue
		}
		if _, ok := shimLocalGenerationFields[key]; ok {
			continue
		}
		unsupported = append(unsupported, key)
	}
	sort.Strings(unsupported)
	return unsupported
}

func unsupportedLocalDerivedFields(rawFields map[string]json.RawMessage) []string {
	unsupported := make([]string, 0)
	for key := range rawFields {
		if _, ok := shimLocalDerivedResponsesBaseFields[key]; ok {
			continue
		}
		unsupported = append(unsupported, key)
	}
	sort.Strings(unsupported)
	return unsupported
}

func shouldFallbackLocalState(responsesMode string, err error) bool {
	mapped := service.MapGeneratorError(err)
	if errors.Is(err, domain.ErrUnsupportedShape) {
		return responsesMode != config.ResponsesModeLocalOnly
	}
	if responsesMode != config.ResponsesModePreferUpstream {
		return false
	}
	return errors.Is(mapped, service.ErrUpstreamFailure) || errors.Is(mapped, service.ErrUpstreamTimeout)
}

func (h *responseHandler) hasLocalCreateState(ctx context.Context, request CreateResponseRequest) (bool, error) {
	if request.Conversation != "" {
		return true, nil
	}

	if request.PreviousResponseID == "" {
		return false, nil
	}

	ok, err := h.service.HasPreviousResponse(ctx, request.PreviousResponseID)
	if err == nil && ok {
		return true, nil
	}
	mapped := service.MapStorageError(err)
	if errors.Is(mapped, service.ErrNotFound) {
		if h.responsesMode == config.ResponsesModeLocalOnly {
			return false, mapped
		}
		return false, nil
	}
	return false, err
}

func normalizeResponsesMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case config.ResponsesModePreferUpstream, config.ResponsesModeLocalOnly:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return config.ResponsesModePreferLocal
	}
}

func newLocalOnlyUnsupportedFieldsError(rawFields map[string]json.RawMessage) error {
	fields := unsupportedLocalShimFields(rawFields)
	if len(fields) == 0 {
		return domain.NewValidationError("responses.mode", "request is not supported when responses.mode=local_only")
	}
	return domain.NewValidationError("responses.mode", "request uses fields that require upstream /v1/responses and are not supported when responses.mode=local_only: "+joinCSV(fields))
}

func newLocalOnlyUnsupportedDerivedFieldsError(endpoint string, rawFields map[string]json.RawMessage) error {
	fields := unsupportedLocalDerivedFields(rawFields)
	if len(fields) == 0 {
		return domain.NewValidationError("responses.mode", "request is not supported when responses.mode=local_only")
	}
	return domain.NewValidationError("responses.mode", "request uses fields that require upstream "+endpoint+" and are not supported when responses.mode=local_only: "+joinCSV(fields))
}

func newLocalStateUnsupportedDerivedFieldsError(endpoint string, rawFields map[string]json.RawMessage) error {
	fields := unsupportedLocalDerivedFields(rawFields)
	if len(fields) == 0 {
		return domain.NewValidationError("input", "request depends on shim-local state and is not supported by "+endpoint)
	}
	return domain.NewValidationError("input", "request depends on shim-local state but uses fields that require upstream "+endpoint+": "+joinCSV(fields))
}

func normalizeLocalOnlyCreateError(responsesMode string, err error) error {
	if responsesMode == config.ResponsesModeLocalOnly && errors.Is(err, domain.ErrUnsupportedShape) {
		return domain.NewValidationError("input", "input shape is not supported when responses.mode=local_only")
	}
	return err
}

func joinCSV(values []string) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values, ", ")
}

func (h *responseHandler) proxyCreateWithShadowStore(w http.ResponseWriter, r *http.Request, request CreateResponseRequest, rawBody []byte, requestJSON string, rawFields map[string]json.RawMessage) {
	upstreamBody, plan, err := remapCustomToolsPayload(rawFields, h.customToolsMode, h.codexCompatibilityEnabled, h.forceCodexToolChoiceRequired)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	logCustomToolTransport(r.Context(), h.logger, rawFields, upstreamBody, plan, h.codexCompatibilityEnabled)

	cloned := r.Clone(r.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(upstreamBody))
	cloned.ContentLength = int64(len(upstreamBody))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(upstreamBody)), nil
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
	defer func() {
		if response != nil {
			_ = response.Body.Close()
		}
	}()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
		return
	}
	if shouldRetryCustomToolsWithBridgeBody(response.StatusCode, body, plan) {
		upstreamBody, plan, err = h.buildBridgedProxyResponsesBody(r.Context(), rawFields)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		_ = response.Body.Close()
		response, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}

		body, err = io.ReadAll(response.Body)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
			return
		}
	}
	if shouldRetryResponsesInputAsStringBody(response.StatusCode, body, upstreamBody) {
		upstreamBody, err = h.buildStringifiedResponsesBody(r.Context(), upstreamBody)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		_ = response.Body.Close()
		response, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}

		body, err = io.ReadAll(response.Body)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
			return
		}
	}
	if shouldRetryCustomToolsWithBridgeBody(response.StatusCode, body, plan) {
		upstreamBody, plan, err = h.buildBridgedCurrentResponsesBody(r.Context(), upstreamBody)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		_ = response.Body.Close()
		response, err = h.proxyResponseRequest(r, upstreamBody)
		if err != nil {
			status, payload := MapError(r.Context(), h.logger, err)
			WriteJSON(w, status, apiErrorPayload{Error: payload})
			return
		}

		body, err = io.ReadAll(response.Body)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response", "")
			return
		}
	}
	if looksLikeSSEPayload(response.Header.Get("Content-Type"), body) {
		h.logger.WarnContext(r.Context(), "unexpected SSE payload on non-stream responses request",
			"request_id", RequestIDFromContext(r.Context()),
			"content_type", response.Header.Get("Content-Type"),
			"body_preview", bodyPreviewForLog(body, 512),
		)
	}

	responseBody := body
	var parsed domain.Response
	parsedOK := false
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		remappedBody, err := normalizeUpstreamResponseBody(body, plan)
		if err != nil {
			h.logger.WarnContext(r.Context(), "custom tool response remap failed",
				"request_id", RequestIDFromContext(r.Context()),
				"err", err,
			)
		} else {
			responseBody = remappedBody
		}
		parsed, err = domain.ParseUpstreamResponse(responseBody)
		if err == nil && (parsed.OutputText != "" || len(parsed.Output) > 0) {
			parsed = annotateResponseCustomToolMetadata(parsed, plan)
			parsedOK = true
			if hasMCPToolDefinitions(rawFields) {
				hydrated := domain.HydrateResponseRequestSurface(parsed, requestJSON)
				if hydratedBody, marshalErr := json.Marshal(hydrated); marshalErr == nil {
					responseBody = hydratedBody
				} else {
					h.logger.WarnContext(r.Context(), "mcp request surface hydration failed",
						"request_id", RequestIDFromContext(r.Context()),
						"err", marshalErr,
					)
				}
			}
		}
	} else if shouldRetryToolChoiceWithAutoBody(response.StatusCode, body, plan) {
		rawResponse, err := h.retryResponseWithAuto(r.Context(), upstreamBody, plan)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		finalBody, parsed, err := finalizeUpstreamResponse(rawResponse, plan, true)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(finalBody)

		prepared, input, ok := prepareShadowStore(r.Context(), h.service.PrepareCreateContext, request, requestJSON)
		if !ok {
			return
		}
		if _, err := h.service.SaveExternalResponse(r.Context(), prepared, input, parsed); err != nil {
			h.logger.ErrorContext(r.Context(), "shadow store failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
		}
		return
	} else if canonical, ok, err := canonicalizeAPIErrorBody(response.StatusCode, body); err == nil && ok {
		responseBody = canonical
	}

	copyResponseHeaders(w.Header(), response.Header)
	if !bytes.Equal(responseBody, body) {
		w.Header().Del("Content-Length")
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}

	prepared, input, ok := prepareShadowStore(r.Context(), h.service.PrepareCreateContext, request, requestJSON)
	if !ok {
		return
	}

	if !parsedOK {
		return
	}

	_, err = h.service.SaveExternalResponse(r.Context(), prepared, input, parsed)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "shadow store failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
	}
}

func prepareShadowStore(ctx context.Context, prepare func(context.Context, service.CreateResponseInput) (service.PreparedResponseContext, error), request CreateResponseRequest, requestJSON string) (service.PreparedResponseContext, service.CreateResponseInput, bool) {
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
	}
	if input.Model == "" {
		return service.PreparedResponseContext{}, input, false
	}

	if prepare != nil {
		prepared, err := prepare(ctx, input)
		if err != nil {
			return service.PreparedResponseContext{}, input, false
		}
		return prepared, input, true
	}

	normalizedInput, err := domain.NormalizeInput(input.Input)
	if err != nil {
		return service.PreparedResponseContext{}, input, false
	}
	return service.PreparedResponseContext{
		NormalizedInput: normalizedInput,
		EffectiveInput:  normalizedInput,
		ContextItems:    normalizedInput,
	}, input, true
}

func buildUpstreamResponsesBody(rawFields map[string]json.RawMessage, contextItems []domain.Item, currentInput []domain.Item, refs map[string]domain.ToolCallReference, customToolsMode string, codexCompatibilityEnabled bool, forceCodexToolChoiceRequired bool) ([]byte, customToolTransportPlan, error) {
	effectiveMode := customToolsMode
	if parseCustomToolsMode(customToolsMode) == customToolsModeAuto && contextHasPassthroughCustomItems(contextItems) {
		effectiveMode = string(customToolsModePassthrough)
	}

	body, plan, err := remapCustomToolsPayload(rawFields, effectiveMode, codexCompatibilityEnabled, forceCodexToolChoiceRequired)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	if plan.Mode == customToolsModeBridge && !plan.Bridge.Active() {
		bridge, err := bridgeFromToolCallRefs(refs)
		if err != nil {
			return nil, customToolTransportPlan{}, err
		}
		if bridge.Active() {
			plan.Bridge = bridge
		}
	}

	payload, err := decodeRawFields(body)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}

	out := make(map[string]any, len(payload)+1)
	for key, raw := range payload {
		switch key {
		case "input", "previous_response_id", "conversation", "instructions":
			continue
		case "store":
			out[key] = false
		default:
			out[key] = json.RawMessage(raw)
		}
	}
	if _, ok := out["store"]; !ok {
		out["store"] = false
	}

	itemsForUpstream := contextItems
	if shouldApplyCodexCompatibility(rawFields, decodeToolList(payload), codexCompatibilityEnabled) {
		itemsForUpstream = injectCodexCompatibilityContext(itemsForUpstream, len(currentInput))
	}

	items, err := remapItemsForUpstream(itemsForUpstream, plan, refs)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	out["input"] = items
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, customToolTransportPlan{}, err
	}
	return encoded, plan, nil
}

func buildUpstreamInputItems(items []domain.Item) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		raw, err := item.MarshalJSON()
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}
