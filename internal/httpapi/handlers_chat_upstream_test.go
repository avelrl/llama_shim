package httpapi

import (
	"context"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
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

func TestStoredChatUpstreamPageReadDetectsOverflow(t *testing.T) {
	t.Parallel()

	body, overflowed, err := readResponsePrefix(strings.NewReader(strings.Repeat("x", 17)), 16)
	if err != nil {
		t.Fatalf("read response prefix: %v", err)
	}
	if !overflowed {
		t.Fatal("expected overflow")
	}
	if len(body) != 17 {
		t.Fatalf("expected cap plus sentinel byte, got %d", len(body))
	}

	classificationErr := &upstreamResponseBodyTooLargeError{
		Surface:  "stored chat completions list",
		MaxBytes: 16,
	}
	decision, ok := classifyBackendFailure(classificationErr)
	if !ok {
		t.Fatal("expected backend failure classification")
	}
	if decision.Class != backendFailureMalformedBackendResponse {
		t.Fatalf("expected malformed backend response, got %s", decision.Class)
	}
	var typed *upstreamResponseBodyTooLargeError
	if !errors.As(classificationErr, &typed) {
		t.Fatal("expected typed oversized body error")
	}
}

func TestStoredChatUpstreamPageReadWithinLimit(t *testing.T) {
	t.Parallel()

	body, overflowed, err := readResponsePrefix(io.LimitReader(strings.NewReader(`{"data":[]}`), 32), 32)
	if err != nil {
		t.Fatalf("read response prefix: %v", err)
	}
	if overflowed {
		t.Fatal("unexpected overflow")
	}
	if string(body) != `{"data":[]}` {
		t.Fatalf("unexpected body %q", string(body))
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
