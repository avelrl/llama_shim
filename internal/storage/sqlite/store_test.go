package sqlite_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/sqlite"
	"llama_shim/internal/testutil"
)

func TestStoreSaveResponseRoundTripAndLineage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	first := domain.StoredResponse{
		ID:                   "resp_first",
		Model:                "test-model",
		RequestJSON:          `{"input":"first"}`,
		ResponseJSON:         `{"id":"resp_first","object":"response","created_at":1712059200,"status":"completed","completed_at":1712059200,"error":null,"incomplete_details":null,"model":"test-model","output":[{"id":"msg_first","type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}],"store":true,"background":false,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":"one"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "first")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "first")},
		Output:               []domain.Item{domain.NewOutputTextMessage("one")},
		OutputText:           "one",
		Store:                true,
		CreatedAt:            "2026-04-02T12:00:00Z",
		CompletedAt:          "2026-04-02T12:00:00Z",
	}
	second := domain.StoredResponse{
		ID:                   "resp_second",
		Model:                "test-model",
		RequestJSON:          `{"input":"second"}`,
		ResponseJSON:         `{"id":"resp_second","object":"response","created_at":1712059260,"status":"completed","completed_at":1712059260,"error":null,"incomplete_details":null,"model":"test-model","output":[{"id":"msg_second","type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}],"previous_response_id":"resp_first","store":true,"background":false,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":"two"}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "second")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "first"), domain.NewOutputTextMessage("one"), domain.NewInputTextMessage("user", "second")},
		Output:               []domain.Item{domain.NewOutputTextMessage("two")},
		OutputText:           "two",
		PreviousResponseID:   first.ID,
		Store:                true,
		CreatedAt:            "2026-04-02T12:01:00Z",
		CompletedAt:          "2026-04-02T12:01:00Z",
	}

	require.NoError(t, store.SaveResponse(ctx, first))
	require.NoError(t, store.SaveResponse(ctx, second))

	got, err := store.GetResponse(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, got.ID)
	require.Equal(t, second.Model, got.Model)
	require.Equal(t, second.RequestJSON, got.RequestJSON)
	require.Equal(t, second.ResponseJSON, got.ResponseJSON)
	require.Equal(t, second.PreviousResponseID, got.PreviousResponseID)
	require.Equal(t, second.OutputText, got.OutputText)
	require.True(t, got.Store)
	require.Len(t, got.NormalizedInputItems, 1)
	require.Equal(t, "second", domain.MessageText(got.NormalizedInputItems[0]))
	require.Len(t, got.EffectiveInputItems, 3)
	require.Equal(t, "first", domain.MessageText(got.EffectiveInputItems[0]))
	require.Equal(t, "one", domain.MessageText(got.EffectiveInputItems[1]))
	require.Equal(t, "second", domain.MessageText(got.EffectiveInputItems[2]))
	require.Len(t, got.Output, 1)
	require.Equal(t, "two", domain.MessageText(got.Output[0]))

	lineage, err := store.GetResponseLineage(ctx, second.ID)
	require.NoError(t, err)
	require.Len(t, lineage, 2)
	require.Equal(t, []string{first.ID, second.ID}, []string{lineage[0].ID, lineage[1].ID})

	_, err = store.GetResponse(ctx, "resp_missing")
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestStoreSaveChatCompletionRoundTripAndList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	first := domain.StoredChatCompletion{
		ID:           "chatcmpl_first",
		Model:        "gpt-5.4",
		Metadata:     map[string]string{"topic": "alpha"},
		RequestJSON:  `{"model":"gpt-5.4","store":true,"metadata":{"topic":"alpha"},"messages":[{"role":"user","content":"first"}]}`,
		ResponseJSON: `{"id":"chatcmpl_first","object":"chat.completion","created":1712059200,"model":"gpt-5.4","metadata":{"topic":"alpha"},"choices":[{"index":0,"message":{"role":"assistant","content":"one"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059200,
	}
	second := domain.StoredChatCompletion{
		ID:           "chatcmpl_second",
		Model:        "gpt-5.4",
		Metadata:     map[string]string{"topic": "beta"},
		RequestJSON:  `{"model":"gpt-5.4","store":true,"metadata":{"topic":"beta"},"messages":[{"role":"user","content":"second"}]}`,
		ResponseJSON: `{"id":"chatcmpl_second","object":"chat.completion","created":1712059260,"model":"gpt-5.4","metadata":{"topic":"beta"},"choices":[{"index":0,"message":{"role":"assistant","content":"two"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059260,
	}
	third := domain.StoredChatCompletion{
		ID:           "chatcmpl_third",
		Model:        "gpt-4o-mini",
		Metadata:     map[string]string{"topic": "alpha"},
		RequestJSON:  `{"model":"gpt-4o-mini","store":true,"metadata":{"topic":"alpha"},"messages":[{"role":"user","content":"third"}]}`,
		ResponseJSON: `{"id":"chatcmpl_third","object":"chat.completion","created":1712059320,"model":"gpt-4o-mini","metadata":{"topic":"alpha"},"choices":[{"index":0,"message":{"role":"assistant","content":"three"},"finish_reason":"stop","logprobs":null}]}`,
		CreatedAt:    1712059320,
	}

	require.NoError(t, store.SaveChatCompletion(ctx, first))
	require.NoError(t, store.SaveChatCompletion(ctx, second))
	require.NoError(t, store.SaveChatCompletion(ctx, third))

	got, err := store.GetChatCompletion(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, got.ID)
	require.Equal(t, second.Model, got.Model)
	require.Equal(t, second.Metadata, got.Metadata)
	require.Equal(t, second.RequestJSON, got.RequestJSON)
	require.Equal(t, second.ResponseJSON, got.ResponseJSON)
	require.Equal(t, second.CreatedAt, got.CreatedAt)

	page, err := store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		Model:    "gpt-5.4",
		Metadata: map[string]string{"topic": "alpha"},
		Limit:    10,
		Order:    domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Completions, 1)
	require.Equal(t, first.ID, page.Completions[0].ID)

	page, err = store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		Limit: 1,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Len(t, page.Completions, 1)
	require.Equal(t, first.ID, page.Completions[0].ID)

	page, err = store.ListChatCompletions(ctx, domain.ListStoredChatCompletionsQuery{
		After: page.Completions[0].ID,
		Limit: 2,
		Order: domain.ChatCompletionOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Completions, 2)
	require.Equal(t, []string{second.ID, third.ID}, []string{page.Completions[0].ID, page.Completions[1].ID})

	_, err = store.GetChatCompletion(ctx, "chatcmpl_missing")
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestStoreSaveResponseUpsertsLifecyclePayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	initial := domain.StoredResponse{
		ID:                   "resp_upsert",
		Model:                "test-model",
		RequestJSON:          `{"model":"test-model","store":true,"background":true,"input":"ping"}`,
		ResponseJSON:         `{"id":"resp_upsert","object":"response","created_at":1712059200,"status":"in_progress","completed_at":null,"error":null,"incomplete_details":null,"model":"test-model","output":[],"store":true,"background":true,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":""}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "ping")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "ping")},
		Output:               nil,
		OutputText:           "",
		Store:                true,
		CreatedAt:            "2026-04-02T12:00:00Z",
		CompletedAt:          "",
	}
	updated := initial
	updated.ResponseJSON = `{"id":"resp_upsert","object":"response","created_at":1712059200,"status":"cancelled","completed_at":null,"error":null,"incomplete_details":null,"model":"test-model","output":[],"store":true,"background":true,"text":{"format":{"type":"text"}},"usage":null,"metadata":{},"output_text":""}`
	updated.CompletedAt = ""

	require.NoError(t, store.SaveResponse(ctx, initial))
	require.NoError(t, store.SaveResponse(ctx, updated))

	got, err := store.GetResponse(ctx, initial.ID)
	require.NoError(t, err)
	require.Equal(t, updated.ResponseJSON, got.ResponseJSON)
	require.Equal(t, updated.CompletedAt, got.CompletedAt)
}

func TestStoreCreateConversationAppendAndPaginateItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	createdAt := "2026-04-02T12:10:00Z"
	completedAt := "2026-04-02T12:11:00Z"
	conversation := domain.Conversation{
		ID:        "conv_test",
		Object:    "conversation",
		Metadata:  map[string]string{"topic": "demo"},
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Items: []domain.Item{
			domain.NewInputTextMessage("system", "You are a test assistant."),
			domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		},
	}

	require.NoError(t, store.CreateConversation(ctx, conversation))

	input := []domain.Item{domain.NewInputTextMessage("user", "What is the code? Reply with just the number.")}
	output := []domain.Item{domain.NewOutputTextMessage("777")}
	response := domain.StoredResponse{
		ID:                   "resp_conv_followup",
		Model:                "test-model",
		RequestJSON:          `{"conversation":"conv_test"}`,
		NormalizedInputItems: input,
		EffectiveInputItems:  append(append([]domain.Item{}, conversation.Items...), input...),
		Output:               output,
		OutputText:           "777",
		ConversationID:       conversation.ID,
		Store:                true,
		CreatedAt:            completedAt,
		CompletedAt:          completedAt,
	}

	require.NoError(t, store.SaveResponseAndAppendConversation(ctx, conversation, response, input, output))

	gotConversation, gotItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, conversation.ID, gotConversation.ID)
	require.Equal(t, map[string]string{"topic": "demo"}, gotConversation.Metadata)
	require.Equal(t, 2, gotConversation.Version)
	require.Len(t, gotItems, 4)
	require.Equal(t, []string{"seed", "seed", "response_input", "response_output"}, []string{
		gotItems[0].Source,
		gotItems[1].Source,
		gotItems[2].Source,
		gotItems[3].Source,
	})
	require.Equal(t, []int{0, 1, 2, 3}, []int{
		gotItems[0].Seq,
		gotItems[1].Seq,
		gotItems[2].Seq,
		gotItems[3].Seq,
	})
	require.Equal(t, "You are a test assistant.", domain.MessageText(gotItems[0].Item))
	require.Equal(t, "Remember: code=777. Reply OK.", domain.MessageText(gotItems[1].Item))
	require.Equal(t, "What is the code? Reply with just the number.", domain.MessageText(gotItems[2].Item))
	require.Equal(t, "777", domain.MessageText(gotItems[3].Item))
	require.NotEmpty(t, gotItems[2].ID)
	require.NotEmpty(t, gotItems[3].ID)

	page, err := store.ListConversationItems(ctx, domain.ListConversationItemsQuery{
		ConversationID: conversation.ID,
		After:          gotItems[1].ID,
		Limit:          2,
		Order:          domain.ConversationItemOrderAsc,
	})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Items, 2)
	require.Equal(t, []int{2, 3}, []int{page.Items[0].Seq, page.Items[1].Seq})
	require.Equal(t, "response_input", page.Items[0].Source)
	require.Equal(t, "response_output", page.Items[1].Source)

	appended, err := store.AppendConversationItems(ctx, gotConversation, []domain.Item{
		domain.NewInputTextMessage("user", "Another turn"),
		domain.NewInputTextMessage("user", "And one more"),
	}, completedAt)
	require.NoError(t, err)
	require.Len(t, appended, 2)
	require.Equal(t, "append", appended[0].Source)
	require.Equal(t, 4, appended[0].Seq)
	require.Equal(t, 5, appended[1].Seq)

	gotItem, err := store.GetConversationItem(ctx, conversation.ID, appended[1].ID)
	require.NoError(t, err)
	require.Equal(t, appended[1].ID, gotItem.ID)
	require.Equal(t, "And one more", domain.MessageText(gotItem.Item))
}

func TestStoreDeleteConversationItemAllowsAppendAfterMidSequenceGap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	createdAt := "2026-04-02T12:10:00Z"
	completedAt := "2026-04-02T12:11:00Z"
	deletedAt := "2026-04-02T12:12:00Z"
	appendedAt := "2026-04-02T12:13:00Z"
	conversation := domain.Conversation{
		ID:        "conv_delete_test",
		Object:    "conversation",
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Items: []domain.Item{
			domain.NewInputTextMessage("system", "You are a test assistant."),
			domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		},
	}

	require.NoError(t, store.CreateConversation(ctx, conversation))

	input := []domain.Item{domain.NewInputTextMessage("user", "What is the code? Reply with just the number.")}
	output := []domain.Item{domain.NewOutputTextMessage("777")}
	response := domain.StoredResponse{
		ID:                   "resp_delete_followup",
		Model:                "test-model",
		RequestJSON:          `{"conversation":"conv_delete_test"}`,
		NormalizedInputItems: input,
		EffectiveInputItems:  append(append([]domain.Item{}, conversation.Items...), input...),
		Output:               output,
		OutputText:           "777",
		ConversationID:       conversation.ID,
		Store:                true,
		CreatedAt:            completedAt,
		CompletedAt:          completedAt,
	}

	require.NoError(t, store.SaveResponseAndAppendConversation(ctx, conversation, response, input, output))

	gotConversation, gotItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Len(t, gotItems, 4)

	require.NoError(t, store.DeleteConversationItem(ctx, gotConversation, gotItems[0].ID, deletedAt))

	_, err = store.GetConversationItem(ctx, conversation.ID, gotItems[0].ID)
	require.ErrorIs(t, err, sqlite.ErrNotFound)

	updatedConversation, updatedItems, err := store.GetConversation(ctx, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, 3, updatedConversation.Version)
	require.Len(t, updatedItems, 3)
	require.Equal(t, []int{1, 2, 3}, []int{
		updatedItems[0].Seq,
		updatedItems[1].Seq,
		updatedItems[2].Seq,
	})

	appended, err := store.AppendConversationItems(ctx, updatedConversation, []domain.Item{
		domain.NewInputTextMessage("user", "After delete"),
	}, appendedAt)
	require.NoError(t, err)
	require.Len(t, appended, 1)
	require.Equal(t, 4, appended[0].Seq)
}

func openTestStore(t *testing.T, ctx context.Context) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(ctx, testutil.TempDBPath(t))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}
