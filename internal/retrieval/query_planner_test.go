package retrieval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewriteSearchQueries(t *testing.T) {
	require.Equal(t, []string{
		"height main office building",
		"file complaint service issue",
	}, RewriteSearchQueries([]string{
		"I'd like to know the height of the main office building.",
		"How do I file a complaint about a service issue?",
	}))
}

func TestPlanFileSearchQueriesSplitsComplexPrompt(t *testing.T) {
	require.Equal(t, []string{
		"banana nutrition apple storage",
		"banana nutrition",
		"apple storage",
	}, PlanFileSearchQueries("Compare banana nutrition and apple storage."))
}

func TestSearchQueryPayloadLikePreservesArrayShape(t *testing.T) {
	require.Equal(t, []string{"banana nutrition"}, SearchQueryPayloadLike([]string{"banana nutrition"}, []string{"banana nutrition"}))
	require.Equal(t, "banana nutrition", SearchQueryPayloadLike("banana nutrition", []string{"banana nutrition"}))
}
