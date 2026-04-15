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

func TestCreateResponseAutomaticCompactionCompactsPriorHistoryBeforeGeneration(t *testing.T) {
	t.Parallel()

	responseStore := &recordingResponseStore{
		lineages: map[string][]domain.StoredResponse{
			"resp_prev": {
				{
					ID:                   "resp_prev",
					Model:                "test-model",
					RequestJSON:          `{"model":"test-model","input":"Remember launch code 1234"}`,
					NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "Remember launch code 1234.")},
					EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "Remember launch code 1234.")},
					Output:               []domain.Item{domain.NewOutputTextMessage("Stored.")},
					OutputText:           "Stored.",
					Store:                true,
				},
			},
		},
	}
	generator := &recordingGenerator{}
	svc := service.NewResponseService(responseStore, noopConversationStore{}, generator)

	response, err := svc.Create(context.Background(), service.CreateResponseInput{
		Model:              "test-model",
		Input:              json.RawMessage(`"What is the launch code?"`),
		PreviousResponseID: "resp_prev",
		ContextManagement:  json.RawMessage(`[{"type":"compaction","compact_threshold":1}]`),
		RequestJSON:        `{"model":"test-model","previous_response_id":"resp_prev","input":"What is the launch code?","context_management":[{"type":"compaction","compact_threshold":1}]}`,
	})
	require.NoError(t, err)

	require.Len(t, generator.contexts, 1)
	require.Len(t, generator.contexts[0], 2)
	require.Equal(t, "system", generator.contexts[0][0].Role)
	require.Contains(t, domain.MessageText(generator.contexts[0][0]), "Compacted prior context summary")
	require.Contains(t, domain.MessageText(generator.contexts[0][0]), "launch code 1234")
	require.Equal(t, "user", generator.contexts[0][1].Role)
	require.Equal(t, "What is the launch code?", domain.MessageText(generator.contexts[0][1]))

	require.Len(t, response.Output, 2)
	require.Equal(t, "compaction", response.Output[0].Type)
	require.Equal(t, "message", response.Output[1].Type)

	require.Len(t, responseStore.saved, 1)
	saved := responseStore.saved[0]
	require.Len(t, saved.NormalizedInputItems, 1)
	require.Equal(t, "What is the launch code?", domain.MessageText(saved.NormalizedInputItems[0]))
	require.Len(t, saved.EffectiveInputItems, 2)
	require.Equal(t, "compaction", saved.EffectiveInputItems[0].Type)
	require.Equal(t, "What is the launch code?", domain.MessageText(saved.EffectiveInputItems[1]))
}

func TestPrepareCreateContextTrimsHistoryBeforeLatestCompaction(t *testing.T) {
	t.Parallel()

	compactionItem, err := domain.NewSyntheticCompactionItem("Prior state retained.", 2)
	require.NoError(t, err)

	responseStore := &recordingResponseStore{
		lineages: map[string][]domain.StoredResponse{
			"resp_compacted": {
				{
					ID:                   "resp_old",
					Model:                "test-model",
					RequestJSON:          `{"model":"test-model","input":"very old"}`,
					NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "Very old detail.")},
					EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "Very old detail.")},
					Output:               []domain.Item{domain.NewOutputTextMessage("Very old answer.")},
					OutputText:           "Very old answer.",
					Store:                true,
				},
				{
					ID:                   "resp_compacted",
					Model:                "test-model",
					RequestJSON:          `{"model":"test-model","input":"recent"}`,
					NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "Recent question.")},
					EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "Very old detail."), domain.NewOutputTextMessage("Very old answer."), domain.NewInputTextMessage("user", "Recent question.")},
					Output:               []domain.Item{compactionItem, domain.NewOutputTextMessage("Recent answer.")},
					OutputText:           "Recent answer.",
					PreviousResponseID:   "resp_old",
					Store:                true,
				},
			},
		},
	}
	svc := service.NewResponseService(responseStore, noopConversationStore{}, noopGenerator{})

	prepared, err := svc.PrepareCreateContext(context.Background(), service.CreateResponseInput{
		Model:              "test-model",
		Input:              json.RawMessage(`"Newest question?"`),
		PreviousResponseID: "resp_compacted",
		RequestJSON:        `{"model":"test-model","previous_response_id":"resp_compacted","input":"Newest question?"}`,
	})
	require.NoError(t, err)

	require.Len(t, prepared.ContextItems, 3)
	require.Equal(t, "system", prepared.ContextItems[0].Role)
	require.Contains(t, domain.MessageText(prepared.ContextItems[0]), "Prior state retained.")
	require.Equal(t, "assistant", prepared.ContextItems[1].Role)
	require.Equal(t, "Recent answer.", domain.MessageText(prepared.ContextItems[1]))
	require.Equal(t, "user", prepared.ContextItems[2].Role)
	require.Equal(t, "Newest question?", domain.MessageText(prepared.ContextItems[2]))
	require.NotContains(t, domain.MessageText(prepared.ContextItems[0]), "Very old detail.")
}

func TestPrepareCreateContextTrimsConversationHistoryBeforeLatestCompaction(t *testing.T) {
	t.Parallel()

	compactionItem, err := domain.NewSyntheticCompactionItem("Conversation state retained.", 3)
	require.NoError(t, err)

	conversationStore := &recordingConversationStore{
		conversation: domain.Conversation{ID: "conv_1", Object: "conversation"},
		items: []domain.ConversationItem{
			{
				ID:   "conv_item_old_user",
				Seq:  1,
				Item: domain.NewInputTextMessage("user", "Very old conversation detail."),
			},
			{
				ID:   "conv_item_old_assistant",
				Seq:  2,
				Item: domain.NewOutputTextMessage("Very old conversation answer."),
			},
			{
				ID:   "conv_item_compaction",
				Seq:  3,
				Item: compactionItem,
			},
			{
				ID:   "conv_item_recent_assistant",
				Seq:  4,
				Item: domain.NewOutputTextMessage("Recent conversation answer."),
			},
		},
	}
	svc := service.NewResponseService(&recordingResponseStore{}, conversationStore, noopGenerator{})

	prepared, err := svc.PrepareCreateContext(context.Background(), service.CreateResponseInput{
		Model:          "test-model",
		Input:          json.RawMessage(`"Newest conversation question?"`),
		ConversationID: "conv_1",
		RequestJSON:    `{"model":"test-model","conversation":"conv_1","input":"Newest conversation question?"}`,
	})
	require.NoError(t, err)

	require.Len(t, prepared.ContextItems, 3)
	require.Equal(t, "system", prepared.ContextItems[0].Role)
	require.Contains(t, domain.MessageText(prepared.ContextItems[0]), "Conversation state retained.")
	require.Equal(t, "assistant", prepared.ContextItems[1].Role)
	require.Equal(t, "Recent conversation answer.", domain.MessageText(prepared.ContextItems[1]))
	require.Equal(t, "user", prepared.ContextItems[2].Role)
	require.Equal(t, "Newest conversation question?", domain.MessageText(prepared.ContextItems[2]))
	require.NotContains(t, domain.MessageText(prepared.ContextItems[0]), "Very old conversation detail.")
}

type noopGenerator struct{}

func (noopGenerator) Generate(context.Context, string, []domain.Item, map[string]json.RawMessage) (string, error) {
	return "OK", nil
}

func (noopGenerator) GenerateStream(context.Context, string, []domain.Item, map[string]json.RawMessage, func(string) error) error {
	return nil
}

type recordingGenerator struct {
	contexts [][]domain.Item
}

func (g *recordingGenerator) Generate(_ context.Context, _ string, items []domain.Item, _ map[string]json.RawMessage) (string, error) {
	copied := append([]domain.Item(nil), items...)
	g.contexts = append(g.contexts, copied)
	return "OK", nil
}

func (g *recordingGenerator) GenerateStream(context.Context, string, []domain.Item, map[string]json.RawMessage, func(string) error) error {
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

func (noopResponseStore) SaveResponseReplayArtifacts(context.Context, string, []domain.ResponseReplayArtifact) error {
	return nil
}

func (noopResponseStore) GetResponseReplayArtifacts(context.Context, string) ([]domain.ResponseReplayArtifact, error) {
	return nil, nil
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

type recordingConversationStore struct {
	conversation domain.Conversation
	items        []domain.ConversationItem
}

func (s *recordingConversationStore) GetConversation(context.Context, string) (domain.Conversation, []domain.ConversationItem, error) {
	return s.conversation, append([]domain.ConversationItem(nil), s.items...), nil
}

func (s *recordingConversationStore) SaveResponseAndAppendConversation(context.Context, domain.Conversation, domain.StoredResponse, []domain.Item, []domain.Item) error {
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
	saved     []domain.StoredResponse
	lineages  map[string][]domain.StoredResponse
	responses map[string]domain.StoredResponse
}

func (s *recordingResponseStore) GetResponse(_ context.Context, id string) (domain.StoredResponse, error) {
	if s.responses != nil {
		if response, ok := s.responses[id]; ok {
			return response, nil
		}
	}
	return domain.StoredResponse{}, nil
}

func (s *recordingResponseStore) GetResponseLineage(_ context.Context, id string) ([]domain.StoredResponse, error) {
	if s.lineages != nil {
		if lineage, ok := s.lineages[id]; ok {
			return append([]domain.StoredResponse(nil), lineage...), nil
		}
	}
	return nil, nil
}

func (s *recordingResponseStore) SaveResponse(_ context.Context, response domain.StoredResponse) error {
	s.saved = append(s.saved, response)
	if s.responses == nil {
		s.responses = make(map[string]domain.StoredResponse)
	}
	s.responses[response.ID] = response
	return nil
}

func (s *recordingResponseStore) SaveResponseReplayArtifacts(context.Context, string, []domain.ResponseReplayArtifact) error {
	return nil
}

func (s *recordingResponseStore) GetResponseReplayArtifacts(context.Context, string) ([]domain.ResponseReplayArtifact, error) {
	return nil, nil
}

func (s *recordingResponseStore) DeleteResponse(context.Context, string) error {
	return nil
}
