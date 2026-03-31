package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestNewResponseNormalizesAssistantOutput(t *testing.T) {
	response := domain.NewResponse("resp_1", "test-model", "OK", "resp_prev", "conv_1")
	require.Equal(t, "resp_1", response.ID)
	require.Equal(t, "response", response.Object)
	require.Equal(t, "test-model", response.Model)
	require.Equal(t, "resp_prev", response.PreviousResponseID)
	require.Equal(t, "conv_1", response.Conversation)
	require.Equal(t, "OK", response.OutputText)
	require.Len(t, response.Output, 1)
	require.Equal(t, "assistant", response.Output[0].Role)
	require.Equal(t, []domain.TextPart{{Type: "output_text", Text: "OK"}}, response.Output[0].Content)
}
