package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
	"llama_shim/internal/service"
)

func TestBuildUpstreamInputItemsCollapsesAdjacentRoles(t *testing.T) {
	items := []domain.MessageItem{
		domain.NewInputTextMessage("system", "You are a test assistant."),
		domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		domain.NewInputTextMessage("user", "What is the code? Reply with just the number."),
	}

	got := buildUpstreamInputItems(items)

	require.Len(t, got, 2)
	require.Equal(t, "system", got[0]["role"])
	require.Equal(t, "You are a test assistant.", got[0]["content"])
	require.NotContains(t, got[0], "type")
	require.Equal(t, "user", got[1]["role"])
	require.Equal(t, "Remember: code=777. Reply OK.\n\nWhat is the code? Reply with just the number.", got[1]["content"])
}

func TestPrepareShadowStoreSkipsUnsupportedInputItems(t *testing.T) {
	request := CreateResponseRequest{
		Model: "test-model",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_1","name":"add","arguments":"{\"a\":1,\"b\":2}"},
			{"type":"function_call_output","call_id":"call_1","output":"{\"result\":3}"}
		]`),
	}

	prepared, input, ok := prepareShadowStore(request, `{"model":"test-model"}`)

	require.False(t, ok)
	require.Equal(t, "test-model", input.Model)
	require.Equal(t, service.PreparedResponseContext{}, prepared)
}

func TestShouldFallbackLocalState(t *testing.T) {
	require.True(t, shouldFallbackLocalState(&llama.UpstreamError{
		StatusCode: 500,
		Message:    "backend failed",
	}))
	require.False(t, shouldFallbackLocalState(domain.NewValidationError("input", "input is required")))
}
