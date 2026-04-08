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
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "first")},
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
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "second")},
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
	require.Equal(t, second.PreviousResponseID, got.PreviousResponseID)
	require.Equal(t, second.OutputText, got.OutputText)
	require.True(t, got.Store)
	require.Len(t, got.NormalizedInputItems, 1)
	require.Equal(t, "second", domain.MessageText(got.NormalizedInputItems[0]))
	require.Len(t, got.Output, 1)
	require.Equal(t, "two", domain.MessageText(got.Output[0]))

	lineage, err := store.GetResponseLineage(ctx, second.ID)
	require.NoError(t, err)
	require.Len(t, lineage, 2)
	require.Equal(t, []string{first.ID, second.ID}, []string{lineage[0].ID, lineage[1].ID})

	_, err = store.GetResponse(ctx, "resp_missing")
	require.ErrorIs(t, err, sqlite.ErrNotFound)
}

func TestStoreCreateConversationAppendAndPaginateItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	createdAt := "2026-04-02T12:10:00Z"
	completedAt := "2026-04-02T12:11:00Z"
	conversation := domain.Conversation{
		ID:        "conv_test",
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
