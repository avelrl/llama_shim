package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestNormalizeConversationMetadataAllowsEmpty(t *testing.T) {
	metadata, err := domain.NormalizeConversationMetadata(nil)
	require.NoError(t, err)
	require.Empty(t, metadata)
}

func TestNormalizeConversationMetadataRejectsTooManyEntries(t *testing.T) {
	payload := make(map[string]string, 17)
	for i := 0; i < 17; i++ {
		payload[strings.Repeat("k", 1)+string(rune('a'+i))] = "v"
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	metadata, err := domain.NormalizeConversationMetadata(raw)
	require.Nil(t, metadata)
	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "metadata", validationErr.Param)
}

func TestNormalizeConversationMetadataRejectsOverlongKey(t *testing.T) {
	raw := json.RawMessage(`{"` + strings.Repeat("k", 65) + `":"value"}`)

	metadata, err := domain.NormalizeConversationMetadata(raw)
	require.Nil(t, metadata)
	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "metadata", validationErr.Param)
}

func TestNormalizeConversationMetadataRejectsOverlongValue(t *testing.T) {
	raw := json.RawMessage(`{"topic":"` + strings.Repeat("v", 513) + `"}`)

	metadata, err := domain.NormalizeConversationMetadata(raw)
	require.Nil(t, metadata)
	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "metadata", validationErr.Param)
}
