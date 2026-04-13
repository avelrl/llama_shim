package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const (
	ListOrderAsc  = "asc"
	ListOrderDesc = "desc"

	DefaultChunkSizeTokens = 800
	DefaultChunkOverlap    = 400
)

type StoredFile struct {
	ID            string
	Filename      string
	Purpose       string
	Bytes         int64
	CreatedAt     int64
	ExpiresAt     *int64
	Status        string
	StatusDetails *string
	Content       []byte
}

type ListFilesQuery struct {
	Purpose string
	After   string
	Limit   int
	Order   string
}

type StoredFilePage struct {
	Files   []StoredFile
	HasMore bool
}

type VectorStoreFileCounts struct {
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
	Cancelled  int `json:"cancelled"`
	Total      int `json:"total"`
}

type VectorStoreExpirationPolicy struct {
	Anchor string `json:"anchor"`
	Days   int    `json:"days"`
}

type StoredVectorStore struct {
	ID           string
	Name         string
	Metadata     map[string]string
	CreatedAt    int64
	LastActiveAt int64
	Status       string
	UsageBytes   int64
	FileCounts   VectorStoreFileCounts
	ExpiresAfter *VectorStoreExpirationPolicy
	ExpiresAt    *int64
}

type ListVectorStoresQuery struct {
	After  string
	Before string
	Limit  int
	Order  string
}

type StoredVectorStorePage struct {
	VectorStores []StoredVectorStore
	HasMore      bool
}

type StaticChunkingStrategy struct {
	MaxChunkSizeTokens int `json:"max_chunk_size_tokens"`
	ChunkOverlapTokens int `json:"chunk_overlap_tokens"`
}

type FileChunkingStrategy struct {
	Type   string                  `json:"type"`
	Static *StaticChunkingStrategy `json:"static,omitempty"`
}

type VectorStoreFileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type StoredVectorStoreFile struct {
	ID               string
	CreatedAt        int64
	VectorStoreID    string
	Status           string
	UsageBytes       int64
	LastError        *VectorStoreFileError
	Attributes       map[string]any
	ChunkingStrategy FileChunkingStrategy
}

type ListVectorStoreFilesQuery struct {
	VectorStoreID string
	After         string
	Before        string
	Filter        string
	Limit         int
	Order         string
}

type StoredVectorStoreFilePage struct {
	Files   []StoredVectorStoreFile
	HasMore bool
}

type VectorStoreSearchFilter struct {
	Type    string
	Key     string
	Value   any
	Filters []VectorStoreSearchFilter
}

type VectorStoreSearchQuery struct {
	VectorStoreID  string
	Queries        []string
	Filters        *VectorStoreSearchFilter
	MaxNumResults  int
	ScoreThreshold *float64
	HybridSearch   *VectorStoreHybridSearchOptions
	RawSearchQuery any
}

type VectorStoreHybridSearchOptions struct {
	EmbeddingWeight float64
	TextWeight      float64
}

type VectorStoreSearchResultContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type VectorStoreSearchResult struct {
	FileID     string                           `json:"file_id"`
	Filename   string                           `json:"filename"`
	Score      float64                          `json:"score"`
	Attributes map[string]any                   `json:"attributes,omitempty"`
	Content    []VectorStoreSearchResultContent `json:"content"`
}

type VectorStoreSearchPage struct {
	SearchQuery any                       `json:"search_query"`
	Results     []VectorStoreSearchResult `json:"data"`
	HasMore     bool                      `json:"has_more"`
	NextPage    *string                   `json:"next_page"`
}

func DefaultFileChunkingStrategy() FileChunkingStrategy {
	return FileChunkingStrategy{
		Type: "static",
		Static: &StaticChunkingStrategy{
			MaxChunkSizeTokens: DefaultChunkSizeTokens,
			ChunkOverlapTokens: DefaultChunkOverlap,
		},
	}
}

func NormalizeRetrievalAttributes(raw json.RawMessage, param string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]any{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, NewValidationError(param, param+" must be an object")
	}
	if len(payload) > 16 {
		return nil, NewValidationError(param, param+" may include at most 16 entries")
	}

	out := make(map[string]any, len(payload))
	for key, value := range payload {
		if len(key) > 64 {
			return nil, NewValidationError(param, param+" keys must be at most 64 characters")
		}
		switch typed := value.(type) {
		case string:
			if len(typed) > 512 {
				return nil, NewValidationError(param, param+" string values must be at most 512 characters")
			}
			out[key] = typed
		case bool:
			out[key] = typed
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) {
				return nil, NewValidationError(param, param+" number values must be finite")
			}
			out[key] = typed
		default:
			return nil, NewValidationError(param, param+" values must be strings, numbers, or booleans")
		}
	}
	return out, nil
}

func NormalizeFileChunkingStrategy(raw json.RawMessage, param string) (FileChunkingStrategy, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return DefaultFileChunkingStrategy(), nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return FileChunkingStrategy{}, NewValidationError(param, param+" must be an object")
	}

	rawType, ok := payload["type"]
	if !ok {
		return FileChunkingStrategy{}, NewValidationError(param+".type", param+".type is required")
	}
	var strategyType string
	if err := json.Unmarshal(rawType, &strategyType); err != nil || strings.TrimSpace(strategyType) == "" {
		return FileChunkingStrategy{}, NewValidationError(param+".type", param+".type must be a string")
	}

	switch strategyType {
	case "auto":
		return DefaultFileChunkingStrategy(), nil
	case "static":
		rawStatic, ok := payload["static"]
		if !ok {
			return FileChunkingStrategy{}, NewValidationError(param+".static", param+".static is required when type=static")
		}
		var static StaticChunkingStrategy
		if err := json.Unmarshal(rawStatic, &static); err != nil {
			return FileChunkingStrategy{}, NewValidationError(param+".static", param+".static must be an object")
		}
		if static.MaxChunkSizeTokens < 100 || static.MaxChunkSizeTokens > 4096 {
			return FileChunkingStrategy{}, NewValidationError(param+".static.max_chunk_size_tokens", "chunking_strategy.static.max_chunk_size_tokens must be between 100 and 4096")
		}
		if static.ChunkOverlapTokens < 0 {
			return FileChunkingStrategy{}, NewValidationError(param+".static.chunk_overlap_tokens", "chunking_strategy.static.chunk_overlap_tokens must be non-negative")
		}
		if static.ChunkOverlapTokens > static.MaxChunkSizeTokens/2 {
			return FileChunkingStrategy{}, NewValidationError(param+".static.chunk_overlap_tokens", "chunking_strategy.static.chunk_overlap_tokens must not exceed half of max_chunk_size_tokens")
		}
		return FileChunkingStrategy{Type: "static", Static: &static}, nil
	default:
		return FileChunkingStrategy{}, NewValidationError(param+".type", "unsupported chunking_strategy.type")
	}
}

func NormalizeVectorStoreSearchFilter(raw json.RawMessage, param string) (*VectorStoreSearchFilter, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, NewValidationError(param, param+" must be an object")
	}

	var filterType string
	if err := json.Unmarshal(payload["type"], &filterType); err != nil || strings.TrimSpace(filterType) == "" {
		return nil, NewValidationError(param+".type", param+".type must be a string")
	}

	switch filterType {
	case "and", "or":
		rawFilters, ok := payload["filters"]
		if !ok {
			return nil, NewValidationError(param+".filters", param+".filters is required")
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(rawFilters, &entries); err != nil || len(entries) == 0 {
			return nil, NewValidationError(param+".filters", param+".filters must be a non-empty array")
		}
		out := make([]VectorStoreSearchFilter, 0, len(entries))
		for i, entry := range entries {
			parsed, err := NormalizeVectorStoreSearchFilter(entry, fmt.Sprintf("%s.filters[%d]", param, i))
			if err != nil {
				return nil, err
			}
			if parsed != nil {
				out = append(out, *parsed)
			}
		}
		if len(out) == 0 {
			return nil, NewValidationError(param+".filters", param+".filters must be a non-empty array")
		}
		return &VectorStoreSearchFilter{Type: filterType, Filters: out}, nil
	case "eq", "ne", "gt", "gte", "lt", "lte", "in", "nin":
		var key string
		if err := json.Unmarshal(payload["key"], &key); err != nil || strings.TrimSpace(key) == "" {
			return nil, NewValidationError(param+".key", param+".key must be a string")
		}
		rawValue, ok := payload["value"]
		if !ok {
			return nil, NewValidationError(param+".value", param+".value is required")
		}
		value, err := normalizeVectorStoreSearchValue(rawValue, filterType, param+".value")
		if err != nil {
			return nil, err
		}
		return &VectorStoreSearchFilter{Type: filterType, Key: key, Value: value}, nil
	default:
		return nil, NewValidationError(param+".type", "unsupported filters.type")
	}
}

func normalizeVectorStoreSearchValue(raw json.RawMessage, filterType string, param string) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, NewValidationError(param, param+" is required")
	}

	if filterType == "in" || filterType == "nin" {
		var values []any
		if err := json.Unmarshal(trimmed, &values); err != nil || len(values) == 0 {
			return nil, NewValidationError(param, param+" must be a non-empty array")
		}
		out := make([]any, 0, len(values))
		for _, value := range values {
			normalized, ok := normalizeComparableAttributeValue(value)
			if !ok {
				return nil, NewValidationError(param, param+" values must be strings, numbers, or booleans")
			}
			out = append(out, normalized)
		}
		return out, nil
	}

	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, NewValidationError(param, param+" must be a string, number, or boolean")
	}
	normalized, ok := normalizeComparableAttributeValue(value)
	if !ok {
		return nil, NewValidationError(param, param+" must be a string, number, or boolean")
	}
	return normalized, nil
}

func MatchVectorStoreSearchFilter(attributes map[string]any, filter *VectorStoreSearchFilter) bool {
	if filter == nil {
		return true
	}

	switch filter.Type {
	case "and":
		for i := range filter.Filters {
			if !MatchVectorStoreSearchFilter(attributes, &filter.Filters[i]) {
				return false
			}
		}
		return true
	case "or":
		for i := range filter.Filters {
			if MatchVectorStoreSearchFilter(attributes, &filter.Filters[i]) {
				return true
			}
		}
		return false
	default:
		actual, ok := attributes[filter.Key]
		if !ok {
			return false
		}
		return matchComparableValue(actual, filter.Type, filter.Value)
	}
}

func normalizeComparableAttributeValue(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return typed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false
		}
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return nil, false
	}
}

func matchComparableValue(actual any, filterType string, expected any) bool {
	actualValue, ok := normalizeComparableAttributeValue(actual)
	if !ok {
		return false
	}

	switch filterType {
	case "eq":
		return comparableValuesEqual(actualValue, expected)
	case "ne":
		return !comparableValuesEqual(actualValue, expected)
	case "gt", "gte", "lt", "lte":
		actualNumber, actualOK := actualValue.(float64)
		expectedNumber, expectedOK := expected.(float64)
		if !actualOK || !expectedOK {
			return false
		}
		switch filterType {
		case "gt":
			return actualNumber > expectedNumber
		case "gte":
			return actualNumber >= expectedNumber
		case "lt":
			return actualNumber < expectedNumber
		default:
			return actualNumber <= expectedNumber
		}
	case "in", "nin":
		values, ok := expected.([]any)
		if !ok {
			return false
		}
		matched := false
		for _, candidate := range values {
			if comparableValuesEqual(actualValue, candidate) {
				matched = true
				break
			}
		}
		if filterType == "in" {
			return matched
		}
		return !matched
	default:
		return false
	}
}

func comparableValuesEqual(left, right any) bool {
	leftValue, leftOK := normalizeComparableAttributeValue(left)
	rightValue, rightOK := normalizeComparableAttributeValue(right)
	if !leftOK || !rightOK {
		return false
	}
	switch leftTyped := leftValue.(type) {
	case string:
		rightTyped, ok := rightValue.(string)
		return ok && leftTyped == rightTyped
	case bool:
		rightTyped, ok := rightValue.(bool)
		return ok && leftTyped == rightTyped
	case float64:
		rightTyped, ok := rightValue.(float64)
		return ok && leftTyped == rightTyped
	default:
		return false
	}
}
