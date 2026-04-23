package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestStructuredSyntheticCompactionExpandsStateAndRetainedItems(t *testing.T) {
	t.Parallel()

	retained := domain.NewInputTextMessage("user", "Recent question?")
	item, err := domain.NewSyntheticCompactionItemWithOptions("Short summary.", 3, domain.SyntheticCompactionOptions{
		Mode: "model_assisted_text",
		State: domain.SyntheticCompactionState{
			Summary:         "Structured summary.",
			KeyFacts:        []string{"Fact A"},
			Constraints:     []string{"Constraint B"},
			OpenLoops:       []string{"Loop C"},
			RecentToolState: []string{"Tool D"},
		},
		RetainedItems: []domain.Item{retained},
	})
	require.NoError(t, err)

	expanded, err := domain.ExpandSyntheticCompactionItems([]domain.Item{item})
	require.NoError(t, err)
	require.Len(t, expanded, 2)
	require.Equal(t, "system", expanded[0].Role)
	require.Contains(t, domain.MessageText(expanded[0]), "Structured summary.")
	require.Contains(t, domain.MessageText(expanded[0]), "Fact A")
	require.Contains(t, domain.MessageText(expanded[0]), "Constraint B")
	require.Contains(t, domain.MessageText(expanded[0]), "Loop C")
	require.Contains(t, domain.MessageText(expanded[0]), "Tool D")
	require.Equal(t, "Recent question?", domain.MessageText(expanded[1]))
}

func TestLegacySyntheticCompactionStillExpandsSummary(t *testing.T) {
	t.Parallel()

	item, err := domain.NewSyntheticCompactionItem("Prior state retained.", 2)
	require.NoError(t, err)

	expanded, err := domain.ExpandSyntheticCompactionItems([]domain.Item{item})
	require.NoError(t, err)
	require.Len(t, expanded, 1)
	require.Contains(t, domain.MessageText(expanded[0]), "Compacted prior context summary")
	require.Contains(t, domain.MessageText(expanded[0]), "Prior state retained.")
}
