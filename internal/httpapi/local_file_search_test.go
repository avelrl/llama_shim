package httpapi

import "testing"

func TestLocalFileSearchFilenameMentionIndex_UnicodeFoldExpansion(t *testing.T) {
	t.Parallel()

	text := "Answer cites Ⱥ for context."
	filename := "Ⱥ"

	index, ok := localFileSearchFilenameMentionIndex(text, filename, nil)
	if !ok {
		t.Fatalf("expected filename mention to be found")
	}

	want := 13
	if index != want {
		t.Fatalf("expected index %d, got %d", want, index)
	}
}

func TestLocalFileSearchFilenameMentionIndex_SkipsOverlappingRanges(t *testing.T) {
	t.Parallel()

	text := "alpha.txt and alpha.txt"
	filename := "alpha.txt"
	used := []localFileSearchAnnotationRange{{Start: 0, End: 9}}

	index, ok := localFileSearchFilenameMentionIndex(text, filename, used)
	if !ok {
		t.Fatalf("expected second filename mention to be found")
	}

	want := 14
	if index != want {
		t.Fatalf("expected index %d, got %d", want, index)
	}
}
