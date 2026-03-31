package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestNormalizeInputString(t *testing.T) {
	items, err := domain.NormalizeInput(json.RawMessage(`"hello"`))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "message", items[0].Type)
	require.Equal(t, "user", items[0].Role)
	require.Equal(t, []domain.TextPart{{Type: "input_text", Text: "hello"}}, items[0].Content)
}

func TestNormalizeInputArray(t *testing.T) {
	items, err := domain.NormalizeInput(json.RawMessage(`[
		{"type":"message","role":"system","content":"You are helpful."},
		{"role":"user","content":[{"type":"text","text":"hello"}]}
	]`))
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "system", items[0].Role)
	require.Equal(t, "You are helpful.", domain.MessageText(items[0]))
	require.Equal(t, "user", items[1].Role)
	require.Equal(t, "hello", domain.MessageText(items[1]))
}

func TestNormalizeConversationItemsRejectsEmpty(t *testing.T) {
	items, err := domain.NormalizeConversationItems(nil)
	require.Nil(t, items)
	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "items", validationErr.Param)
}
