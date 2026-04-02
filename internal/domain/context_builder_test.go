package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestBuildLineageContext(t *testing.T) {
	lineage := []domain.StoredResponse{
		{
			ID:                   "resp_1",
			NormalizedInputItems: []domain.MessageItem{domain.NewInputTextMessage("user", "Remember: my code = 123. Reply OK")},
			Output:               []domain.MessageItem{domain.NewOutputTextMessage("OK")},
		},
		{
			ID:                   "resp_2",
			NormalizedInputItems: []domain.MessageItem{domain.NewInputTextMessage("user", "another turn")},
			Output:               []domain.MessageItem{domain.NewOutputTextMessage("done")},
		},
	}

	contextItems, err := domain.BuildLineageContext(lineage, "follow these instructions", []domain.MessageItem{
		domain.NewInputTextMessage("user", "What was my code?"),
	})
	require.NoError(t, err)
	require.Len(t, contextItems, 6)
	require.Equal(t, "Remember: my code = 123. Reply OK", domain.MessageText(contextItems[0]))
	require.Equal(t, "OK", domain.MessageText(contextItems[1]))
	require.Equal(t, "another turn", domain.MessageText(contextItems[2]))
	require.Equal(t, "done", domain.MessageText(contextItems[3]))
	require.Equal(t, "follow these instructions", domain.MessageText(contextItems[4]))
	require.Equal(t, "What was my code?", domain.MessageText(contextItems[5]))
}

func TestBuildConversationAppendItems(t *testing.T) {
	items := domain.BuildConversationAppendItems(2, []domain.MessageItem{
		domain.NewInputTextMessage("user", "next question"),
	}, []domain.Item{domain.NewOutputTextMessage("answer")})

	require.Len(t, items, 2)
	require.Equal(t, 2, items[0].Seq)
	require.Equal(t, "response_input", items[0].Source)
	require.Equal(t, "next question", domain.MessageText(items[0].Item))
	require.Equal(t, 3, items[1].Seq)
	require.Equal(t, "response_output", items[1].Source)
	require.Equal(t, "answer", domain.MessageText(items[1].Item))
}
