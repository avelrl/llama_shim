package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestNewResponseNormalizesAssistantOutput(t *testing.T) {
	response := domain.NewResponse("resp_1", "test-model", "OK", "resp_prev", "conv_1", 1)
	require.Equal(t, "resp_1", response.ID)
	require.Equal(t, "response", response.Object)
	require.Equal(t, "test-model", response.Model)
	require.Equal(t, "resp_prev", response.PreviousResponseID)
	require.NotNil(t, response.Conversation)
	require.Equal(t, "conv_1", response.Conversation.ID)
	require.Equal(t, "OK", response.OutputText)
	require.Len(t, response.Output, 1)
	require.Equal(t, "assistant", response.Output[0].Role)
	require.Equal(t, []domain.TextPart{{Type: "output_text", Text: "OK"}}, response.Output[0].Content)
}

func TestNewItemRejectsNonObjectPayload(t *testing.T) {
	_, err := domain.NewItem([]byte(`"not an object"`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode item payload")
}

func TestNewItemRejectsNullPayload(t *testing.T) {
	_, err := domain.NewItem([]byte(`null`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "item payload must be a JSON object")
}

func TestParseUpstreamResponseRejectsMalformedOutputItem(t *testing.T) {
	_, err := domain.ParseUpstreamResponse([]byte(`{
		"id":"resp_bad",
		"object":"response",
		"created_at":1712059200,
		"model":"test-model",
		"output":["not an object"]
	}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode upstream response output[0]")
}

func TestParseUpstreamResponseRejectsNullOutputItem(t *testing.T) {
	_, err := domain.ParseUpstreamResponse([]byte(`{
		"id":"resp_bad",
		"object":"response",
		"created_at":1712059200,
		"model":"test-model",
		"output":[null]
	}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode upstream response output[0]")
}
