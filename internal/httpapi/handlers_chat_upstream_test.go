package httpapi

import (
	"context"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"llama_shim/internal/domain"
)

func TestBuildUpstreamStoredChatCompletionsQuery_UsesRequestedLimitWithinPageLimit(t *testing.T) {
	t.Parallel()

	values := buildUpstreamStoredChatCompletionsQuery(domain.ListStoredChatCompletionsQuery{
		Limit: 7,
		Order: domain.ChatCompletionOrderDesc,
	}, "cursor_1", 7)

	if got := values.Get("limit"); got != "7" {
		t.Fatalf("expected limit=7, got %q", got)
	}
	if got := values.Get("order"); got != domain.ChatCompletionOrderDesc {
		t.Fatalf("expected order=%q, got %q", domain.ChatCompletionOrderDesc, got)
	}
	if got := values.Get("after"); got != "cursor_1" {
		t.Fatalf("expected after=cursor_1, got %q", got)
	}
}

func TestBuildUpstreamStoredChatCompletionsQuery_CapsLimitAtPageLimit(t *testing.T) {
	t.Parallel()

	values := buildUpstreamStoredChatCompletionsQuery(domain.ListStoredChatCompletionsQuery{
		Limit: upstreamStoredChatCompletionsPageLimit + 50,
		Order: domain.ChatCompletionOrderAsc,
	}, "", upstreamStoredChatCompletionsPageLimit+50)

	if got := values.Get("limit"); got != strconv.Itoa(upstreamStoredChatCompletionsPageLimit) {
		t.Fatalf("expected limit=%d, got %q", upstreamStoredChatCompletionsPageLimit, got)
	}
}

func TestNextMergedStoredChatCompletionPrefersLocalDuplicateAndPreservesOrder(t *testing.T) {
	t.Parallel()

	local := newStaticStoredChatCompletionSource([]storedChatCompletionListEntry{
		{ID: "chatcmpl_1", Created: 10},
		{ID: "chatcmpl_3", Created: 30, Raw: []byte(`{"id":"chatcmpl_3","created":30,"source":"local"}`)},
	})
	upstream := newStaticStoredChatCompletionSource([]storedChatCompletionListEntry{
		{ID: "chatcmpl_2", Created: 20},
		{ID: "chatcmpl_3", Created: 30, Raw: []byte(`{"id":"chatcmpl_3","created":30,"source":"upstream"}`)},
	})

	var (
		ids []string
		raw []string
	)
	for {
		entry, forward, err := nextMergedStoredChatCompletion(context.Background(), local, upstream, domain.ChatCompletionOrderAsc)
		if err != nil {
			t.Fatalf("merge next: %v", err)
		}
		if forward != nil {
			t.Fatalf("unexpected forward response: %+v", forward)
		}
		if entry == nil {
			break
		}
		ids = append(ids, entry.ID)
		raw = append(raw, string(entry.Raw))
	}

	if !reflect.DeepEqual(ids, []string{"chatcmpl_1", "chatcmpl_2", "chatcmpl_3"}) {
		t.Fatalf("unexpected merge order: %#v", ids)
	}
	if raw[2] != `{"id":"chatcmpl_3","created":30,"source":"local"}` {
		t.Fatalf("expected local duplicate to win, got %q", raw[2])
	}
}

func newStaticStoredChatCompletionSource(entries []storedChatCompletionListEntry) *storedChatCompletionSource {
	copied := make([]storedChatCompletionListEntry, 0, len(entries))
	for _, entry := range entries {
		cloned := entry
		if len(cloned.Raw) == 0 {
			cloned.Raw = []byte(`{"id":"` + cloned.ID + `","created":` + strconv.FormatInt(cloned.Created, 10) + `}`)
		}
		copied = append(copied, cloned)
	}

	return newStoredChatCompletionSource(func(context.Context, string) (storedChatCompletionSourcePage, *storedChatCompletionForwardResponse, bool, error) {
		page := storedChatCompletionSourcePage{
			Entries: copied,
			HasMore: false,
		}
		copied = nil
		return page, nil, false, nil
	})
}

func TestCloneURLClonesInput(t *testing.T) {
	t.Parallel()

	original := &url.URL{Path: "/v1/chat/completions", RawQuery: "limit=1"}
	cloned := cloneURL(original)
	if cloned == original {
		t.Fatal("expected cloneURL to return a distinct pointer")
	}
	if cloned.Path != original.Path || cloned.RawQuery != original.RawQuery {
		t.Fatalf("unexpected cloned URL: %#v", cloned)
	}
}
