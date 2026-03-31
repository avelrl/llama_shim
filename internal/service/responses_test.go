package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

func TestCreateResponseRejectsMutuallyExclusiveStateFields(t *testing.T) {
	svc := service.NewResponseService(noopResponseStore{}, noopConversationStore{}, noopGenerator{})

	_, err := svc.Create(context.Background(), service.CreateResponseInput{
		Model:              "test-model",
		Input:              json.RawMessage(`"hello"`),
		PreviousResponseID: "resp_1",
		ConversationID:     "conv_1",
		RequestJSON:        `{"model":"test-model","input":"hello"}`,
	})
	require.Error(t, err)
	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "previous_response_id", validationErr.Param)
}

type noopGenerator struct{}

func (noopGenerator) Generate(context.Context, string, []domain.MessageItem, map[string]json.RawMessage) (string, error) {
	return "OK", nil
}

func (noopGenerator) GenerateStream(context.Context, string, []domain.MessageItem, map[string]json.RawMessage, func(string) error) error {
	return nil
}

type noopResponseStore struct{}

func (noopResponseStore) GetResponse(context.Context, string) (domain.StoredResponse, error) {
	return domain.StoredResponse{}, nil
}

func (noopResponseStore) GetResponseLineage(context.Context, string) ([]domain.StoredResponse, error) {
	return nil, nil
}

func (noopResponseStore) SaveResponse(context.Context, domain.StoredResponse) error {
	return nil
}

type noopConversationStore struct{}

func (noopConversationStore) GetConversation(context.Context, string) (domain.Conversation, []domain.ConversationItem, error) {
	return domain.Conversation{}, nil, nil
}

func (noopConversationStore) SaveResponseAndAppendConversation(context.Context, domain.Conversation, domain.StoredResponse, []domain.MessageItem, domain.MessageItem) error {
	return nil
}
