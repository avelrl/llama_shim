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

func (noopGenerator) Generate(context.Context, string, []domain.Item, map[string]json.RawMessage) (string, error) {
	return "OK", nil
}

func (noopGenerator) GenerateStream(context.Context, string, []domain.Item, map[string]json.RawMessage, func(string) error) error {
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

func (noopResponseStore) DeleteResponse(context.Context, string) error {
	return nil
}

type noopConversationStore struct{}

func (noopConversationStore) GetConversation(context.Context, string) (domain.Conversation, []domain.ConversationItem, error) {
	return domain.Conversation{}, nil, nil
}

func (noopConversationStore) SaveResponseAndAppendConversation(context.Context, domain.Conversation, domain.StoredResponse, []domain.Item, []domain.Item) error {
	return nil
}

func TestSaveExternalResponseSkipsStatelessPersistenceWhenStoreFalse(t *testing.T) {
	t.Parallel()

	responseStore := &recordingResponseStore{}
	svc := service.NewResponseService(responseStore, noopConversationStore{}, noopGenerator{})
	store := false

	response, err := svc.SaveExternalResponse(
		context.Background(),
		service.PreparedResponseContext{
			NormalizedInput: []domain.Item{domain.NewInputTextMessage("user", "ping")},
		},
		service.CreateResponseInput{
			Model:       "test-model",
			Input:       json.RawMessage(`"ping"`),
			Store:       &store,
			RequestJSON: `{"model":"test-model","input":"ping","store":false}`,
		},
		domain.Response{
			ID:         "resp_external_stateless",
			OutputText: "OK",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "test-model", response.Model)
	require.Equal(t, "OK", response.OutputText)
	require.Len(t, response.Output, 1)
	require.Equal(t, "OK", domain.MessageText(response.Output[0]))
	require.Empty(t, responseStore.saved)
}

func TestSaveExternalResponsePersistsHiddenFollowUpWhenStoreFalse(t *testing.T) {
	t.Parallel()

	responseStore := &recordingResponseStore{}
	svc := service.NewResponseService(responseStore, noopConversationStore{}, noopGenerator{})
	store := false

	response, err := svc.SaveExternalResponse(
		context.Background(),
		service.PreparedResponseContext{
			NormalizedInput: []domain.Item{domain.NewInputTextMessage("user", "What is the result?")},
		},
		service.CreateResponseInput{
			Model:              "test-model",
			Input:              json.RawMessage(`"What is the result?"`),
			Store:              &store,
			PreviousResponseID: "resp_prev",
			RequestJSON:        `{"model":"test-model","previous_response_id":"resp_prev","store":false}`,
		},
		domain.Response{
			ID:         "resp_external_followup",
			OutputText: "42",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "resp_prev", response.PreviousResponseID)
	require.Len(t, responseStore.saved, 1)

	saved := responseStore.saved[0]
	require.Equal(t, "resp_external_followup", saved.ID)
	require.Equal(t, "test-model", saved.Model)
	require.Equal(t, "resp_prev", saved.PreviousResponseID)
	require.False(t, saved.Store)
	require.Len(t, saved.NormalizedInputItems, 1)
	require.NotEmpty(t, saved.NormalizedInputItems[0].ID())
	require.Len(t, saved.Output, 1)
	require.NotEmpty(t, saved.Output[0].ID())
	require.Equal(t, "42", saved.OutputText)
}

type recordingResponseStore struct {
	saved []domain.StoredResponse
}

func (s *recordingResponseStore) GetResponse(context.Context, string) (domain.StoredResponse, error) {
	return domain.StoredResponse{}, nil
}

func (s *recordingResponseStore) GetResponseLineage(context.Context, string) ([]domain.StoredResponse, error) {
	return nil, nil
}

func (s *recordingResponseStore) SaveResponse(_ context.Context, response domain.StoredResponse) error {
	s.saved = append(s.saved, response)
	return nil
}

func (s *recordingResponseStore) DeleteResponse(context.Context, string) error {
	return nil
}
