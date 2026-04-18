package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

func TestBuildUpstreamStoredChatCompletionsQuery_UsesRequestedLimitWithinPageLimit(t *testing.T) {
	t.Parallel()

	values := buildUpstreamStoredChatCompletionsQuery(domain.ListStoredChatCompletionsQuery{
		Limit: 7,
		Order: domain.ChatCompletionOrderDesc,
	}, "cursor_1")

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
	}, "")

	if got := values.Get("limit"); got != strconv.Itoa(upstreamStoredChatCompletionsPageLimit) {
		t.Fatalf("expected limit=%d, got %q", upstreamStoredChatCompletionsPageLimit, got)
	}
}

func TestListUpstreamStoredChatCompletions_StopsAfterMaxPages(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		q := r.URL.Query()
		after := q.Get("after")
		if q.Get("limit") != "1" {
			t.Fatalf("expected forwarded limit=1, got %q", q.Get("limit"))
		}
		next := "1"
		if after != "" {
			current, err := strconv.Atoi(after)
			if err != nil {
				t.Fatalf("unexpected after value %q: %v", after, err)
			}
			next = strconv.Itoa(current + 1)
		}

		_ = json.NewEncoder(w).Encode(chatCompletionsListResponse{
			Object:  "list",
			Data:    []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"id":"chatcmpl_%s","created":%s}`, next, next))},
			HasMore: true,
			LastID:  ptrString(next),
		})
	}))
	defer upstream.Close()

	h := &proxyHandler{
		logger: slog.Default(),
		client: llama.NewClient(upstream.URL, 2*time.Second),
	}

	incoming := httptest.NewRequest(http.MethodGet, "http://shim.local/v1/chat/completions", nil)
	incoming.URL = &url.URL{Path: "/v1/chat/completions"}

	results, statusCode, headers, body, err := h.listUpstreamStoredChatCompletions(context.Background(), incoming, domain.ListStoredChatCompletionsQuery{
		Limit: 1,
		Order: domain.ChatCompletionOrderAsc,
	})
	if err != nil {
		t.Fatalf("list upstream: %v", err)
	}
	if statusCode != 0 || headers != nil || body != nil {
		t.Fatalf("expected successful internal result, got status=%d headers=%v body=%q", statusCode, headers, string(body))
	}

	if got := int(requestCount.Load()); got != upstreamStoredChatCompletionsMaxPages {
		t.Fatalf("expected exactly %d upstream pages, got %d", upstreamStoredChatCompletionsMaxPages, got)
	}
	if got := len(results); got != upstreamStoredChatCompletionsMaxPages {
		t.Fatalf("expected %d merged items, got %d", upstreamStoredChatCompletionsMaxPages, got)
	}
}

func ptrString(s string) *string {
	return &s
}
