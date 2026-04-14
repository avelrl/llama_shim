package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/sqlite"
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

func (h *proxyHandler) listUpstreamStoredChatCompletions(ctx context.Context, incoming *http.Request, query domain.ListStoredChatCompletionsQuery) ([]json.RawMessage, int, http.Header, []byte, error) {
	if h.client == nil {
		return nil, 0, nil, nil, nil
	}

	all := make([]json.RawMessage, 0, upstreamStoredChatCompletionsPageLimit)
	after := ""
	for {
		page, statusCode, headers, body, unsupported, err := h.fetchUpstreamStoredChatCompletionsPage(ctx, incoming, query, after)
		if err != nil {
			return nil, 0, nil, nil, err
		}
		if unsupported {
			return nil, 0, nil, nil, nil
		}
		if statusCode != 0 {
			return nil, statusCode, headers, body, nil
		}
		all = append(all, page.Data...)
		if !page.HasMore || page.LastID == nil || strings.TrimSpace(*page.LastID) == "" {
			break
		}
		after = *page.LastID
	}

	return all, 0, nil, nil, nil
}

func (h *proxyHandler) fetchUpstreamStoredChatCompletionsPage(ctx context.Context, incoming *http.Request, query domain.ListStoredChatCompletionsQuery, after string) (chatCompletionsListResponse, int, http.Header, []byte, bool, error) {
	values := buildUpstreamStoredChatCompletionsQuery(query, after)
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

func buildUpstreamStoredChatCompletionsQuery(query domain.ListStoredChatCompletionsQuery, after string) url.Values {
	values := url.Values{}
	if model := strings.TrimSpace(query.Model); model != "" {
		values.Set("model", model)
	}
	for key, value := range query.Metadata {
		values.Set("metadata["+key+"]", value)
	}
	values.Set("limit", "100")
	values.Set("order", query.Order)
	if after = strings.TrimSpace(after); after != "" {
		values.Set("after", after)
	}
	return values
}

func buildMergedStoredChatCompletionsPage(local []domain.StoredChatCompletion, upstream []json.RawMessage, query domain.ListStoredChatCompletionsQuery) (storedChatCompletionsMergedPage, error) {
	merged := make(map[string]storedChatCompletionListEntry, len(local)+len(upstream))
	for _, raw := range upstream {
		entry, err := decodeStoredChatCompletionListEntry(raw)
		if err != nil {
			return storedChatCompletionsMergedPage{}, err
		}
		merged[entry.ID] = entry
	}
	for _, completion := range local {
		entry, err := decodeStoredChatCompletionListEntry([]byte(completion.ResponseJSON))
		if err != nil {
			return storedChatCompletionsMergedPage{}, err
		}
		merged[entry.ID] = entry
	}

	entries := make([]storedChatCompletionListEntry, 0, len(merged))
	for _, entry := range merged {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left, right storedChatCompletionListEntry) int {
		switch query.Order {
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
	})

	start := 0
	if after := strings.TrimSpace(query.After); after != "" {
		start = -1
		for i, entry := range entries {
			if entry.ID == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return storedChatCompletionsMergedPage{}, sqlite.ErrNotFound
		}
	}
	if start > len(entries) {
		start = len(entries)
	}
	end := start + query.Limit
	hasMore := end < len(entries)
	if end > len(entries) {
		end = len(entries)
	}

	page := make([]json.RawMessage, 0, end-start)
	for _, entry := range entries[start:end] {
		page = append(page, entry.Raw)
	}
	return storedChatCompletionsMergedPage{
		Data:    page,
		HasMore: hasMore,
	}, nil
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
