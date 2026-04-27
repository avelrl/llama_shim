package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage"
)

const upstreamStoredChatCompletionsPageLimit = 100

type storedChatCompletionsMergedPage struct {
	Data    []json.RawMessage
	HasMore bool
}

type storedChatCompletionListEntry struct {
	ID      string
	Created int64
	Raw     json.RawMessage
}

type storedChatCompletionForwardResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type storedChatCompletionSourcePage struct {
	Entries []storedChatCompletionListEntry
	HasMore bool
}

type storedChatCompletionSource struct {
	fetch   func(context.Context, string) (storedChatCompletionSourcePage, *storedChatCompletionForwardResponse, bool, error)
	after   string
	entries []storedChatCompletionListEntry
	next    int
	hasMore bool
	loaded  bool
	done    bool
}

func (h *proxyHandler) buildMergedStoredChatCompletionsPage(ctx context.Context, incoming *http.Request, query domain.ListStoredChatCompletionsQuery) (storedChatCompletionsMergedPage, *storedChatCompletionForwardResponse, error) {
	local := newStoredChatCompletionSource(func(fetchCtx context.Context, after string) (storedChatCompletionSourcePage, *storedChatCompletionForwardResponse, bool, error) {
		page, err := h.store.ListChatCompletions(fetchCtx, domain.ListStoredChatCompletionsQuery{
			Model:    query.Model,
			Metadata: query.Metadata,
			After:    after,
			Limit:    storedChatCompletionsSourcePageLimit(query.Limit + 1),
			Order:    query.Order,
		})
		if err != nil {
			return storedChatCompletionSourcePage{}, nil, false, err
		}
		entries := make([]storedChatCompletionListEntry, 0, len(page.Completions))
		for _, completion := range page.Completions {
			entries = append(entries, storedChatCompletionListEntry{
				ID:      strings.TrimSpace(completion.ID),
				Created: completion.CreatedAt,
				Raw:     append(json.RawMessage(nil), []byte(completion.ResponseJSON)...),
			})
		}
		return storedChatCompletionSourcePage{
			Entries: entries,
			HasMore: page.HasMore,
		}, nil, false, nil
	})

	var upstream *storedChatCompletionSource
	if h.client != nil {
		upstream = newStoredChatCompletionSource(func(fetchCtx context.Context, after string) (storedChatCompletionSourcePage, *storedChatCompletionForwardResponse, bool, error) {
			page, statusCode, headers, body, unsupported, err := h.fetchUpstreamStoredChatCompletionsPage(fetchCtx, incoming, query, after, storedChatCompletionsSourcePageLimit(query.Limit+1))
			if err != nil {
				return storedChatCompletionSourcePage{}, nil, false, err
			}
			if unsupported {
				return storedChatCompletionSourcePage{}, nil, true, nil
			}
			if statusCode != 0 {
				return storedChatCompletionSourcePage{}, &storedChatCompletionForwardResponse{
					StatusCode: statusCode,
					Headers:    headers,
					Body:       body,
				}, false, nil
			}
			entries := make([]storedChatCompletionListEntry, 0, len(page.Data))
			for _, raw := range page.Data {
				entry, err := decodeStoredChatCompletionListEntry(raw)
				if err != nil {
					return storedChatCompletionSourcePage{}, nil, false, err
				}
				entries = append(entries, entry)
			}
			return storedChatCompletionSourcePage{
				Entries: entries,
				HasMore: page.HasMore,
			}, nil, false, nil
		})
	}

	after := strings.TrimSpace(query.After)
	seenAfter := after == ""
	data := make([]json.RawMessage, 0, storedChatCompletionsSourcePageLimit(query.Limit))
	for len(data) < query.Limit+1 {
		entry, forward, err := nextMergedStoredChatCompletion(ctx, local, upstream, query.Order)
		if err != nil {
			return storedChatCompletionsMergedPage{}, nil, err
		}
		if forward != nil {
			return storedChatCompletionsMergedPage{}, forward, nil
		}
		if entry == nil {
			break
		}
		if !seenAfter {
			if entry.ID == after {
				seenAfter = true
			}
			continue
		}
		data = append(data, entry.Raw)
	}
	if !seenAfter {
		return storedChatCompletionsMergedPage{}, nil, storage.ErrNotFound
	}
	hasMore := len(data) > query.Limit
	if hasMore {
		data = data[:query.Limit]
	}
	return storedChatCompletionsMergedPage{
		Data:    data,
		HasMore: hasMore,
	}, nil, nil
}

func newStoredChatCompletionSource(fetch func(context.Context, string) (storedChatCompletionSourcePage, *storedChatCompletionForwardResponse, bool, error)) *storedChatCompletionSource {
	if fetch == nil {
		return nil
	}
	return &storedChatCompletionSource{fetch: fetch}
}

func (s *storedChatCompletionSource) peek(ctx context.Context) (*storedChatCompletionListEntry, *storedChatCompletionForwardResponse, error) {
	if s == nil {
		return nil, nil, nil
	}
	for {
		if s.done {
			return nil, nil, nil
		}
		if s.next < len(s.entries) {
			return &s.entries[s.next], nil, nil
		}
		if s.loaded && !s.hasMore {
			s.done = true
			return nil, nil, nil
		}

		page, forward, unsupported, err := s.fetch(ctx, s.after)
		if err != nil {
			return nil, nil, err
		}
		if forward != nil {
			return nil, forward, nil
		}
		if unsupported {
			s.done = true
			return nil, nil, nil
		}

		s.loaded = true
		s.entries = page.Entries
		s.next = 0
		s.hasMore = page.HasMore
		if len(page.Entries) == 0 {
			s.done = true
			return nil, nil, nil
		}

		nextAfter := strings.TrimSpace(page.Entries[len(page.Entries)-1].ID)
		if nextAfter == "" {
			s.done = true
			return nil, nil, nil
		}
		if page.HasMore && nextAfter == s.after {
			s.done = true
			return nil, nil, nil
		}
		s.after = nextAfter
	}
}

func (s *storedChatCompletionSource) advance() {
	if s == nil || s.done {
		return
	}
	if s.next < len(s.entries) {
		s.next++
	}
}

func nextMergedStoredChatCompletion(ctx context.Context, local, upstream *storedChatCompletionSource, order string) (*storedChatCompletionListEntry, *storedChatCompletionForwardResponse, error) {
	localEntry, forward, err := local.peek(ctx)
	if err != nil || forward != nil {
		return nil, forward, err
	}
	upstreamEntry, forward, err := upstream.peek(ctx)
	if err != nil || forward != nil {
		return nil, forward, err
	}

	switch {
	case localEntry == nil && upstreamEntry == nil:
		return nil, nil, nil
	case localEntry == nil:
		chosen := *upstreamEntry
		upstream.advance()
		return &chosen, nil, nil
	case upstreamEntry == nil:
		chosen := *localEntry
		local.advance()
		return &chosen, nil, nil
	case localEntry.ID == upstreamEntry.ID:
		chosen := *localEntry
		local.advance()
		upstream.advance()
		return &chosen, nil, nil
	case compareStoredChatCompletionListEntries(order, *localEntry, *upstreamEntry) <= 0:
		chosen := *localEntry
		local.advance()
		return &chosen, nil, nil
	default:
		chosen := *upstreamEntry
		upstream.advance()
		return &chosen, nil, nil
	}
}

func compareStoredChatCompletionListEntries(order string, left, right storedChatCompletionListEntry) int {
	switch order {
	case domain.ChatCompletionOrderDesc:
		if left.Created != right.Created {
			if left.Created > right.Created {
				return -1
			}
			return 1
		}
		return strings.Compare(right.ID, left.ID)
	default:
		if left.Created != right.Created {
			if left.Created < right.Created {
				return -1
			}
			return 1
		}
		return strings.Compare(left.ID, right.ID)
	}
}

func (h *proxyHandler) fetchUpstreamStoredChatCompletionsPage(ctx context.Context, incoming *http.Request, query domain.ListStoredChatCompletionsQuery, after string, limit int) (chatCompletionsListResponse, int, http.Header, []byte, bool, error) {
	values := buildUpstreamStoredChatCompletionsQuery(query, after, limit)
	request := incoming.Clone(ctx)
	request.Method = http.MethodGet
	request.Body = nil
	request.ContentLength = 0
	request.GetBody = nil
	request.URL = cloneURL(incoming.URL)
	request.URL.RawQuery = values.Encode()

	response, err := h.client.Proxy(ctx, request)
	if err != nil {
		return chatCompletionsListResponse{}, 0, nil, nil, false, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return chatCompletionsListResponse{}, 0, nil, nil, false, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if isStoredChatCompletionListUnsupported(response.StatusCode) {
			return chatCompletionsListResponse{}, 0, nil, nil, true, nil
		}
		return chatCompletionsListResponse{}, response.StatusCode, response.Header.Clone(), body, false, nil
	}

	var page chatCompletionsListResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return chatCompletionsListResponse{}, 0, nil, nil, false, err
	}
	return page, 0, nil, nil, false, nil
}

func buildUpstreamStoredChatCompletionsQuery(query domain.ListStoredChatCompletionsQuery, after string, limit int) url.Values {
	values := url.Values{}
	if model := strings.TrimSpace(query.Model); model != "" {
		values.Set("model", model)
	}
	for key, value := range query.Metadata {
		values.Set("metadata["+key+"]", value)
	}
	values.Set("limit", strconv.Itoa(storedChatCompletionsSourcePageLimit(limit)))
	values.Set("order", query.Order)
	if after = strings.TrimSpace(after); after != "" {
		values.Set("after", after)
	}
	return values
}

func storedChatCompletionsSourcePageLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > upstreamStoredChatCompletionsPageLimit {
		return upstreamStoredChatCompletionsPageLimit
	}
	return limit
}

func decodeStoredChatCompletionListEntry(raw []byte) (storedChatCompletionListEntry, error) {
	var payload struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return storedChatCompletionListEntry{}, err
	}
	return storedChatCompletionListEntry{
		ID:      strings.TrimSpace(payload.ID),
		Created: payload.Created,
		Raw:     append(json.RawMessage(nil), raw...),
	}, nil
}

func isStoredChatCompletionListUnsupported(statusCode int) bool {
	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

func cloneURL(incoming *url.URL) *url.URL {
	if incoming == nil {
		return &url.URL{}
	}
	cloned := *incoming
	return &cloned
}

func (h *proxyHandler) bestEffortForwardStoredChatCompletion(ctx context.Context, incoming *http.Request, body []byte) {
	if h.client == nil {
		return
	}

	request := incoming.Clone(ctx)
	request.URL = cloneURL(incoming.URL)
	if body == nil {
		request.Body = nil
		request.ContentLength = 0
		request.GetBody = nil
	} else {
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	response, err := h.client.Proxy(ctx, request)
	if err != nil {
		h.logger.WarnContext(ctx, "best effort upstream stored chat completion sync failed",
			"request_id", RequestIDFromContext(ctx),
			"err", err,
		)
		return
	}
	defer response.Body.Close()

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return
	}
	if isStoredChatCompletionListUnsupported(response.StatusCode) {
		return
	}

	bodySnippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	h.logger.WarnContext(ctx, "best effort upstream stored chat completion sync returned non-success",
		"request_id", RequestIDFromContext(ctx),
		"status_code", response.StatusCode,
		"body", string(bytes.TrimSpace(bodySnippet)),
	)
}
