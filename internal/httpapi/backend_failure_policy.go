package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"llama_shim/internal/llama"
	"llama_shim/internal/service"
)

type backendFailureClass string

const (
	backendFailureAuthFailure               backendFailureClass = "auth_failure"
	backendFailurePermissionFailure         backendFailureClass = "permission_failure"
	backendFailureQuotaExhausted            backendFailureClass = "quota_exhausted"
	backendFailureRateLimitRetryable        backendFailureClass = "rate_limit_retryable"
	backendFailureModelUnavailable          backendFailureClass = "model_unavailable"
	backendFailureUnsupportedToolOrParam    backendFailureClass = "unsupported_tool_or_param"
	backendFailureTransportTimeout          backendFailureClass = "transport_timeout"
	backendFailureStreamIdleTimeout         backendFailureClass = "stream_idle_timeout"
	backendFailureMalformedBackendResponse  backendFailureClass = "malformed_backend_response"
	backendFailureBackendCapabilityMismatch backendFailureClass = "backend_capability_mismatch"
	backendFailureLocalRuntimeUnavailable   backendFailureClass = "local_runtime_unavailable"
	backendFailureUpstreamServerError       backendFailureClass = "upstream_server_error"
	backendFailureTransportError            backendFailureClass = "transport_error"
	backendFailureUnknown                   backendFailureClass = "unknown"
)

type backendFailureDecision struct {
	Class               backendFailureClass
	Retryable           bool
	Cooldown            bool
	CooldownHintSeconds int
	FallbackAllowed     bool
	ClientStatus        int
	ClientType          string
	ClientCode          string
	ClientMessage       string
}

func classifyBackendFailure(err error) (backendFailureDecision, bool) {
	if err == nil {
		return backendFailureDecision{}, false
	}

	var timeoutErr *llama.TimeoutError
	var upstreamErr *llama.UpstreamError
	var invalidResponseErr *llama.InvalidResponseError
	var netErr net.Error
	switch {
	case errors.As(err, &timeoutErr), errors.Is(err, service.ErrUpstreamTimeout), errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return backendFailureDecisionFor(backendFailureTransportTimeout), true
	case errors.As(err, &upstreamErr):
		return backendFailureDecisionFor(classifyBackendHTTPFailure(upstreamErr.StatusCode, upstreamErr.Message)), true
	case errors.As(err, &invalidResponseErr):
		return backendFailureDecisionFor(classifyInvalidBackendResponse(invalidResponseErr.Message)), true
	case errors.Is(err, service.ErrUpstreamFailure):
		return backendFailureDecisionFor(backendFailureUpstreamServerError), true
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "stream idle timeout"):
		return backendFailureDecisionFor(backendFailureStreamIdleTimeout), true
	case strings.Contains(message, "local runtime") && strings.Contains(message, "not ready"):
		return backendFailureDecisionFor(backendFailureLocalRuntimeUnavailable), true
	case strings.Contains(message, "unsupported") && (strings.Contains(message, "tool") || strings.Contains(message, "parameter") || strings.Contains(message, "param")):
		return backendFailureDecisionFor(backendFailureUnsupportedToolOrParam), true
	case strings.Contains(message, "call llama:"):
		return backendFailureDecisionFor(backendFailureTransportError), true
	default:
		return backendFailureDecision{}, false
	}
}

func classifyBackendHTTPFailure(status int, message string) backendFailureClass {
	normalized := strings.ToLower(message)
	switch status {
	case http.StatusUnauthorized:
		return backendFailureAuthFailure
	case http.StatusForbidden:
		return backendFailurePermissionFailure
	case http.StatusTooManyRequests:
		if strings.Contains(normalized, "quota") ||
			strings.Contains(normalized, "billing") ||
			strings.Contains(normalized, "insufficient_quota") ||
			strings.Contains(normalized, "credits") {
			return backendFailureQuotaExhausted
		}
		return backendFailureRateLimitRetryable
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return backendFailureTransportTimeout
	case http.StatusNotFound:
		if strings.Contains(normalized, "model") {
			return backendFailureModelUnavailable
		}
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotImplemented:
		if looksLikeCapabilityMismatch(normalized) {
			return backendFailureBackendCapabilityMismatch
		}
		if looksLikeUnsupportedToolOrParam(normalized) {
			return backendFailureUnsupportedToolOrParam
		}
	case http.StatusServiceUnavailable:
		if strings.Contains(normalized, "overload") ||
			strings.Contains(normalized, "unavailable") ||
			strings.Contains(normalized, "try again") ||
			strings.Contains(normalized, "slow down") {
			return backendFailureModelUnavailable
		}
	}
	if status >= 500 {
		return backendFailureUpstreamServerError
	}
	return backendFailureUnknown
}

func classifyInvalidBackendResponse(message string) backendFailureClass {
	normalized := strings.ToLower(message)
	if looksLikeCapabilityMismatch(normalized) {
		return backendFailureBackendCapabilityMismatch
	}
	if looksLikeUnsupportedToolOrParam(normalized) {
		return backendFailureUnsupportedToolOrParam
	}
	return backendFailureMalformedBackendResponse
}

func looksLikeCapabilityMismatch(message string) bool {
	return strings.Contains(message, "capability") ||
		strings.Contains(message, "not supported by backend") ||
		strings.Contains(message, "incompatible backend") ||
		strings.Contains(message, "unsupported response format") ||
		strings.Contains(message, "unsupported tool_choice")
}

func looksLikeUnsupportedToolOrParam(message string) bool {
	return strings.Contains(message, "unsupported") ||
		strings.Contains(message, "unknown parameter") ||
		strings.Contains(message, "unknown field") ||
		strings.Contains(message, "invalid tool") ||
		strings.Contains(message, "tool_choice is invalid") ||
		strings.Contains(message, "invalid_request_error")
}

func backendFailureDecisionFor(class backendFailureClass) backendFailureDecision {
	switch class {
	case backendFailureAuthFailure:
		return backendFailureDecision{Class: class, ClientStatus: http.StatusUnauthorized, ClientType: "authentication_error", ClientCode: "upstream_auth_failure", ClientMessage: "upstream authentication failed"}
	case backendFailurePermissionFailure:
		return backendFailureDecision{Class: class, ClientStatus: http.StatusForbidden, ClientType: "permission_error", ClientCode: "upstream_permission_failure", ClientMessage: "upstream permission denied"}
	case backendFailureQuotaExhausted:
		return backendFailureDecision{Class: class, Cooldown: true, CooldownHintSeconds: 900, ClientStatus: http.StatusTooManyRequests, ClientType: "rate_limit_error", ClientCode: "insufficient_quota", ClientMessage: "upstream quota exhausted"}
	case backendFailureRateLimitRetryable:
		return backendFailureDecision{Class: class, Retryable: true, Cooldown: true, CooldownHintSeconds: 60, FallbackAllowed: true, ClientStatus: http.StatusTooManyRequests, ClientType: "rate_limit_error", ClientCode: "upstream_rate_limit", ClientMessage: "upstream rate limit exceeded"}
	case backendFailureModelUnavailable:
		return backendFailureDecision{Class: class, Retryable: true, Cooldown: true, CooldownHintSeconds: 30, FallbackAllowed: true, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "model_unavailable", ClientMessage: "upstream model is unavailable"}
	case backendFailureUnsupportedToolOrParam:
		return backendFailureDecision{Class: class, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "unsupported_tool_or_param", ClientMessage: "upstream rejected a tool or parameter"}
	case backendFailureTransportTimeout:
		return backendFailureDecision{Class: class, Retryable: true, Cooldown: true, CooldownHintSeconds: 15, FallbackAllowed: true, ClientStatus: http.StatusGatewayTimeout, ClientType: "upstream_timeout_error", ClientCode: "upstream_timeout", ClientMessage: "upstream request timed out"}
	case backendFailureStreamIdleTimeout:
		return backendFailureDecision{Class: class, Retryable: true, Cooldown: true, CooldownHintSeconds: 15, FallbackAllowed: true, ClientStatus: http.StatusGatewayTimeout, ClientType: "upstream_timeout_error", ClientCode: "upstream_stream_idle_timeout", ClientMessage: "upstream stream timed out"}
	case backendFailureMalformedBackendResponse:
		return backendFailureDecision{Class: class, Retryable: true, FallbackAllowed: true, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "malformed_backend_response", ClientMessage: "upstream returned a malformed response"}
	case backendFailureBackendCapabilityMismatch:
		return backendFailureDecision{Class: class, FallbackAllowed: true, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "backend_capability_mismatch", ClientMessage: "upstream backend does not support the requested capability"}
	case backendFailureLocalRuntimeUnavailable:
		return backendFailureDecision{Class: class, Cooldown: true, CooldownHintSeconds: 30, ClientStatus: http.StatusServiceUnavailable, ClientType: "service_unavailable", ClientCode: "local_runtime_unavailable", ClientMessage: "local runtime is unavailable"}
	case backendFailureTransportError:
		return backendFailureDecision{Class: class, Retryable: true, Cooldown: true, CooldownHintSeconds: 15, FallbackAllowed: true, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "transport_error", ClientMessage: "upstream transport failed"}
	case backendFailureUnknown:
		return backendFailureDecision{Class: class, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "unknown_backend_failure", ClientMessage: "upstream request failed"}
	default:
		return backendFailureDecision{Class: backendFailureUpstreamServerError, Retryable: true, Cooldown: true, CooldownHintSeconds: 30, FallbackAllowed: true, ClientStatus: http.StatusBadGateway, ClientType: "upstream_error", ClientCode: "upstream_server_error", ClientMessage: "upstream request failed"}
	}
}

func backendFailurePolicyLogAttrs(decision backendFailureDecision) []any {
	return []any{
		"failure_class", string(decision.Class),
		"retryable", decision.Retryable,
		"cooldown", decision.Cooldown,
		"cooldown_hint_seconds", decision.CooldownHintSeconds,
		"fallback_allowed", decision.FallbackAllowed,
	}
}
