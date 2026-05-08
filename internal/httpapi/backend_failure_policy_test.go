package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"llama_shim/internal/llama"
	"llama_shim/internal/service"

	"github.com/stretchr/testify/require"
)

func TestClassifyBackendFailureFromHTTPStatus(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		class backendFailureClass
	}{
		{
			name:  "auth",
			err:   &llama.UpstreamError{StatusCode: http.StatusUnauthorized, Message: `{"error":{"message":"Incorrect API key provided"}}`},
			class: backendFailureAuthFailure,
		},
		{
			name:  "quota",
			err:   &llama.UpstreamError{StatusCode: http.StatusTooManyRequests, Message: `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`},
			class: backendFailureQuotaExhausted,
		},
		{
			name:  "rate limit",
			err:   &llama.UpstreamError{StatusCode: http.StatusTooManyRequests, Message: `{"error":{"message":"Rate limit reached for requests"}}`},
			class: backendFailureRateLimitRetryable,
		},
		{
			name:  "overloaded",
			err:   &llama.UpstreamError{StatusCode: http.StatusServiceUnavailable, Message: "The engine is currently overloaded, please try again later"},
			class: backendFailureModelUnavailable,
		},
		{
			name:  "capability mismatch",
			err:   &llama.UpstreamError{StatusCode: http.StatusBadRequest, Message: "unsupported response format for this backend"},
			class: backendFailureBackendCapabilityMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision, ok := classifyBackendFailure(tc.err)
			require.True(t, ok)
			require.Equal(t, tc.class, decision.Class)
		})
	}
}

func TestClassifyBackendFailurePolicyDecisions(t *testing.T) {
	decision, ok := classifyBackendFailure(&llama.TimeoutError{Message: "request timed out"})
	require.True(t, ok)
	require.Equal(t, backendFailureTransportTimeout, decision.Class)
	require.True(t, decision.Retryable)
	require.True(t, decision.Cooldown)
	require.True(t, decision.FallbackAllowed)
	require.Equal(t, http.StatusGatewayTimeout, decision.ClientStatus)

	decision, ok = classifyBackendFailure(service.ErrUpstreamFailure)
	require.True(t, ok)
	require.Equal(t, backendFailureUpstreamServerError, decision.Class)
	require.True(t, decision.Retryable)
	require.True(t, decision.Cooldown)
	require.True(t, decision.FallbackAllowed)

	decision, ok = classifyBackendFailure(context.DeadlineExceeded)
	require.True(t, ok)
	require.Equal(t, backendFailureTransportTimeout, decision.Class)

	_, ok = classifyBackendFailure(errors.New("ordinary validation failure"))
	require.False(t, ok)
}

func TestCapabilityBackendFailurePolicyContainsOperatorDecisions(t *testing.T) {
	policy := capabilityBackendFailurePolicy()
	require.NotEmpty(t, policy)

	byClass := make(map[string]capabilityBackendFailureRule, len(policy))
	for _, rule := range policy {
		byClass[rule.Class] = rule
	}
	require.Equal(t, http.StatusUnauthorized, byClass[string(backendFailureAuthFailure)].ClientStatus)
	require.Equal(t, "authentication_error", byClass[string(backendFailureAuthFailure)].ClientType)
	require.Equal(t, true, byClass[string(backendFailureRateLimitRetryable)].Retryable)
	require.Equal(t, true, byClass[string(backendFailureRateLimitRetryable)].Cooldown)
	require.Equal(t, true, byClass[string(backendFailureTransportTimeout)].FallbackAllowed)
	require.Equal(t, "backend_capability_mismatch", byClass[string(backendFailureBackendCapabilityMismatch)].ClientCode)
}
