package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage/sqlite"
)

const (
	defaultFilesLimit            = 10000
	maxFilesLimit                = 10000
	defaultVectorStoresLimit     = 20
	maxVectorStoresLimit         = 100
	defaultVectorStoreFilesLimit = 20
	maxVectorStoreFilesLimit     = 100
	defaultSearchResultsLimit    = 10
	maxSearchResultsLimit        = 50
)

type retrievalHandler struct {
	logger        *slog.Logger
	store         *sqlite.Store
	metrics       *Metrics
	serviceLimits ServiceLimits
	searchGate    *concurrencyGate
}

type fileObjectResponse struct {
	ID            string  `json:"id"`
	Object        string  `json:"object"`
	Bytes         int64   `json:"bytes"`
	CreatedAt     int64   `json:"created_at"`
	ExpiresAt     *int64  `json:"expires_at"`
	Filename      string  `json:"filename"`
	Purpose       string  `json:"purpose"`
	Status        *string `json:"status,omitempty"`
	StatusDetails *string `json:"status_details"`
}

type fileDeletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

type filesListResponse struct {
	Object  string               `json:"object"`
	Data    []fileObjectResponse `json:"data"`
	FirstID *string              `json:"first_id"`
	LastID  *string              `json:"last_id"`
	HasMore bool                 `json:"has_more"`
}

type vectorStoreObjectResponse struct {
	ID           string                              `json:"id"`
	Object       string                              `json:"object"`
	CreatedAt    int64                               `json:"created_at"`
	Name         string                              `json:"name"`
	Metadata     map[string]string                   `json:"metadata"`
	Status       string                              `json:"status"`
	UsageBytes   int64                               `json:"usage_bytes"`
	FileCounts   domain.VectorStoreFileCounts        `json:"file_counts"`
	LastActiveAt int64                               `json:"last_active_at"`
	ExpiresAfter *domain.VectorStoreExpirationPolicy `json:"expires_after"`
	ExpiresAt    *int64                              `json:"expires_at"`
}

type vectorStoresListResponse struct {
	Object  string                      `json:"object"`
	Data    []vectorStoreObjectResponse `json:"data"`
	FirstID *string                     `json:"first_id"`
	LastID  *string                     `json:"last_id"`
	HasMore bool                        `json:"has_more"`
}

type vectorStoreDeletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

type vectorStoreFileObjectResponse struct {
	ID               string                       `json:"id"`
	Object           string                       `json:"object"`
	CreatedAt        int64                        `json:"created_at"`
	UsageBytes       int64                        `json:"usage_bytes"`
	VectorStoreID    string                       `json:"vector_store_id"`
	Status           string                       `json:"status"`
	LastError        *domain.VectorStoreFileError `json:"last_error"`
	Attributes       map[string]any               `json:"attributes,omitempty"`
	ChunkingStrategy domain.FileChunkingStrategy  `json:"chunking_strategy,omitempty"`
}

type vectorStoreFilesListResponse struct {
	Object  string                          `json:"object"`
	Data    []vectorStoreFileObjectResponse `json:"data"`
	FirstID *string                         `json:"first_id"`
	LastID  *string                         `json:"last_id"`
	HasMore bool                            `json:"has_more"`
}

type vectorStoreSearchResponse struct {
	Object      string                           `json:"object"`
	SearchQuery any                              `json:"search_query"`
	Data        []domain.VectorStoreSearchResult `json:"data"`
	HasMore     bool                             `json:"has_more"`
	NextPage    *string                          `json:"next_page"`
}

func newRetrievalHandler(logger *slog.Logger, store *sqlite.Store, metrics *Metrics, serviceLimits ServiceLimits, searchGate *concurrencyGate) *retrievalHandler {
	return &retrievalHandler{
		logger:        logger,
		store:         store,
		metrics:       metrics,
		serviceLimits: normalizeServiceLimits(serviceLimits),
		searchGate:    searchGate,
	}
}

func (h *retrievalHandler) createFile(w http.ResponseWriter, r *http.Request) {
	maxUploadBytes := h.serviceLimits.RetrievalFileUploadBytes
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed multipart form body", "")
		return
	}

	purpose := strings.TrimSpace(r.FormValue("purpose"))
	if purpose == "" {
		h.writeError(w, r, domain.NewValidationError("purpose", "purpose is required"))
		return
	}

	expiresAt, err := parseFileExpiresAt(r.MultipartForm)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	uploaded, header, err := r.FormFile("file")
	if err != nil {
		h.writeError(w, r, domain.NewValidationError("file", "file is required"))
		return
	}
	defer uploaded.Close()

	content, err := io.ReadAll(io.LimitReader(uploaded, maxUploadBytes+1))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "failed to read uploaded file", "")
		return
	}
	if int64(len(content)) > maxUploadBytes {
		h.writeError(w, r, domain.NewValidationError("file", "file exceeds the configured shim-local upload limit"))
		return
	}

	fileID, err := domain.NewPrefixedID("file")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := "processed"
	file := domain.StoredFile{
		ID:        fileID,
		Filename:  header.Filename,
		Purpose:   purpose,
		Bytes:     int64(len(content)),
		CreatedAt: domain.NowUTC().Unix(),
		ExpiresAt: expiresAt,
		Status:    status,
		Content:   content,
	}
	if err := h.store.SaveFile(r.Context(), file); err != nil {
		h.writeError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, fileObjectPayload(file))
}

func (h *retrievalHandler) listFiles(w http.ResponseWriter, r *http.Request) {
	query, err := parseListFilesQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	page, err := h.store.ListFiles(r.Context(), query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	data := make([]fileObjectResponse, 0, len(page.Files))
	for _, file := range page.Files {
		data = append(data, fileObjectPayload(file))
	}
	WriteJSON(w, http.StatusOK, filesListResponse{
		Object:  "list",
		Data:    data,
		FirstID: firstFileID(data),
		LastID:  lastFileID(data),
		HasMore: page.HasMore,
	})
}

func (h *retrievalHandler) getFile(w http.ResponseWriter, r *http.Request) {
	file, err := h.store.GetFile(r.Context(), r.PathValue("file_id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, fileObjectPayload(file))
}

func (h *retrievalHandler) deleteFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	if err := h.store.DeleteFile(r.Context(), fileID); err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, fileDeletionResponse{
		ID:      fileID,
		Object:  "file",
		Deleted: true,
	})
}

func (h *retrievalHandler) getFileContent(w http.ResponseWriter, r *http.Request) {
	file, err := h.store.GetFile(r.Context(), r.PathValue("file_id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeContentDispositionFilename(file.Filename)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
}

func (h *retrievalHandler) createVectorStore(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	var request struct {
		Name         string          `json:"name"`
		FileIDs      []string        `json:"file_ids,omitempty"`
		Metadata     json.RawMessage `json:"metadata,omitempty"`
		ExpiresAfter json.RawMessage `json:"expires_after,omitempty"`
	}
	if trimmed := bytes.TrimSpace(rawBody); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(rawBody, &request); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
			return
		}
	}

	metadata, err := domain.NormalizeResponseMetadata(request.Metadata)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	expiresAfter, expiresAt, err := parseVectorStoreExpiresAfter(request.ExpiresAfter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	vectorStoreID, err := domain.NewPrefixedID("vs")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	now := domain.NowUTC().Unix()
	store := domain.StoredVectorStore{
		ID:           vectorStoreID,
		Name:         request.Name,
		Metadata:     metadata,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAfter: expiresAfter,
		ExpiresAt:    expiresAt,
	}
	if err := h.store.SaveVectorStore(r.Context(), store); err != nil {
		h.writeError(w, r, err)
		return
	}

	for _, fileID := range request.FileIDs {
		if strings.TrimSpace(fileID) == "" {
			h.writeError(w, r, domain.NewValidationError("file_ids", "file_ids must not contain empty values"))
			return
		}
		if _, err := h.store.AttachFileToVectorStore(r.Context(), vectorStoreID, strings.TrimSpace(fileID), map[string]any{}, domain.DefaultFileChunkingStrategy(), now); err != nil {
			h.writeError(w, r, err)
			return
		}
	}

	stored, err := h.store.GetVectorStore(r.Context(), vectorStoreID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, vectorStorePayload(stored))
}

func (h *retrievalHandler) listVectorStores(w http.ResponseWriter, r *http.Request) {
	query, err := parseListVectorStoresQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	page, err := h.store.ListVectorStores(r.Context(), query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	data := make([]vectorStoreObjectResponse, 0, len(page.VectorStores))
	for _, store := range page.VectorStores {
		data = append(data, vectorStorePayload(store))
	}
	WriteJSON(w, http.StatusOK, vectorStoresListResponse{
		Object:  "list",
		Data:    data,
		FirstID: firstVectorStoreID(data),
		LastID:  lastVectorStoreID(data),
		HasMore: page.HasMore,
	})
}

func (h *retrievalHandler) getVectorStore(w http.ResponseWriter, r *http.Request) {
	store, err := h.store.GetVectorStore(r.Context(), r.PathValue("vector_store_id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, vectorStorePayload(store))
}

func (h *retrievalHandler) deleteVectorStore(w http.ResponseWriter, r *http.Request) {
	vectorStoreID := r.PathValue("vector_store_id")
	if err := h.store.DeleteVectorStore(r.Context(), vectorStoreID); err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, vectorStoreDeletionResponse{
		ID:      vectorStoreID,
		Object:  "vector_store.deleted",
		Deleted: true,
	})
}

func (h *retrievalHandler) createVectorStoreFile(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	var request struct {
		FileID           string          `json:"file_id"`
		Attributes       json.RawMessage `json:"attributes,omitempty"`
		ChunkingStrategy json.RawMessage `json:"chunking_strategy,omitempty"`
	}
	if err := json.Unmarshal(rawBody, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}
	if strings.TrimSpace(request.FileID) == "" {
		h.writeError(w, r, domain.NewValidationError("file_id", "file_id is required"))
		return
	}

	attributes, err := domain.NormalizeRetrievalAttributes(request.Attributes, "attributes")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	chunkingStrategy, err := domain.NormalizeFileChunkingStrategy(request.ChunkingStrategy, "chunking_strategy")
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	file, err := h.store.AttachFileToVectorStore(
		r.Context(),
		r.PathValue("vector_store_id"),
		strings.TrimSpace(request.FileID),
		attributes,
		chunkingStrategy,
		domain.NowUTC().Unix(),
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, vectorStoreFilePayload(file))
}

func (h *retrievalHandler) listVectorStoreFiles(w http.ResponseWriter, r *http.Request) {
	query, err := parseListVectorStoreFilesQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	query.VectorStoreID = r.PathValue("vector_store_id")

	page, err := h.store.ListVectorStoreFiles(r.Context(), query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	data := make([]vectorStoreFileObjectResponse, 0, len(page.Files))
	for _, file := range page.Files {
		data = append(data, vectorStoreFilePayload(file))
	}
	WriteJSON(w, http.StatusOK, vectorStoreFilesListResponse{
		Object:  "list",
		Data:    data,
		FirstID: firstVectorStoreFileID(data),
		LastID:  lastVectorStoreFileID(data),
		HasMore: page.HasMore,
	})
}

func (h *retrievalHandler) getVectorStoreFile(w http.ResponseWriter, r *http.Request) {
	file, err := h.store.GetVectorStoreFile(r.Context(), r.PathValue("vector_store_id"), r.PathValue("file_id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, vectorStoreFilePayload(file))
}

func (h *retrievalHandler) deleteVectorStoreFile(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteVectorStoreFile(r.Context(), r.PathValue("vector_store_id"), r.PathValue("file_id")); err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, vectorStoreDeletionResponse{
		ID:      r.PathValue("file_id"),
		Object:  "vector_store.file.deleted",
		Deleted: true,
	})
}

func (h *retrievalHandler) searchVectorStore(w http.ResponseWriter, r *http.Request) {
	release, err := h.searchGate.tryAcquire()
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer release()

	start := time.Now()
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}

	var request struct {
		Query          json.RawMessage `json:"query"`
		Filters        json.RawMessage `json:"filters,omitempty"`
		MaxNumResults  *int            `json:"max_num_results,omitempty"`
		RewriteQuery   *bool           `json:"rewrite_query,omitempty"`
		RankingOptions struct {
			Ranker         string          `json:"ranker,omitempty"`
			ScoreThreshold *float64        `json:"score_threshold,omitempty"`
			HybridSearch   json.RawMessage `json:"hybrid_search,omitempty"`
		} `json:"ranking_options,omitempty"`
	}
	if err := json.Unmarshal(rawBody, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
		return
	}

	queries, rawSearchQuery, err := parseVectorStoreSearchQuery(request.Query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(queries) > h.serviceLimits.RetrievalMaxSearchQueries {
		h.writeError(w, r, domain.NewValidationError("query", fmt.Sprintf("query must contain at most %d search strings in shim-local retrieval", h.serviceLimits.RetrievalMaxSearchQueries)))
		return
	}
	if request.RewriteQuery != nil && *request.RewriteQuery {
		queries = retrieval.RewriteSearchQueries(queries)
		rawSearchQuery = retrieval.SearchQueryPayloadLike(rawSearchQuery, queries)
		if len(queries) > h.serviceLimits.RetrievalMaxSearchQueries {
			queries = queries[:h.serviceLimits.RetrievalMaxSearchQueries]
			rawSearchQuery = retrieval.SearchQueryPayloadLike(rawSearchQuery, queries)
		}
	}
	filters, err := domain.NormalizeVectorStoreSearchFilter(request.Filters, "filters")
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	maxNumResults := defaultSearchResultsLimit
	if request.MaxNumResults != nil {
		if *request.MaxNumResults < 1 || *request.MaxNumResults > maxSearchResultsLimit {
			h.writeError(w, r, domain.NewValidationError("max_num_results", "max_num_results must be between 1 and 50"))
			return
		}
		maxNumResults = *request.MaxNumResults
	}
	if ranker := strings.TrimSpace(request.RankingOptions.Ranker); ranker != "" {
		switch ranker {
		case "auto", "none", "default_2024_08_21", "default-2024-08-21":
		default:
			h.writeError(w, r, domain.NewValidationError("ranking_options.ranker", "unsupported ranking_options.ranker"))
			return
		}
	}
	if request.RankingOptions.ScoreThreshold != nil {
		if *request.RankingOptions.ScoreThreshold < 0 || *request.RankingOptions.ScoreThreshold > 1 {
			h.writeError(w, r, domain.NewValidationError("ranking_options.score_threshold", "ranking_options.score_threshold must be between 0 and 1"))
			return
		}
	}
	hybridSearch, err := parseHybridSearchOptions(request.RankingOptions.HybridSearch, "ranking_options.hybrid_search")
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	page, err := h.store.SearchVectorStore(r.Context(), domain.VectorStoreSearchQuery{
		VectorStoreID:  r.PathValue("vector_store_id"),
		Queries:        queries,
		Filters:        filters,
		MaxNumResults:  maxNumResults,
		Ranker:         strings.TrimSpace(request.RankingOptions.Ranker),
		ScoreThreshold: request.RankingOptions.ScoreThreshold,
		HybridSearch:   hybridSearch,
		RawSearchQuery: rawSearchQuery,
	})
	if err != nil {
		if h.metrics != nil {
			h.metrics.IncRetrievalSearch("vector_store_search", "error")
		}
		h.writeError(w, r, err)
		return
	}
	if h.metrics != nil {
		h.metrics.IncRetrievalSearch("vector_store_search", "ok")
	}
	h.logger.InfoContext(r.Context(), "retrieval search",
		"request_id", RequestIDFromContext(r.Context()),
		"surface", "vector_store_search",
		"vector_store_id", r.PathValue("vector_store_id"),
		"queries", queries,
		"result_count", len(page.Results),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	WriteJSON(w, http.StatusOK, vectorStoreSearchResponse{
		Object:      "vector_store.search_results.page",
		SearchQuery: page.SearchQuery,
		Data:        page.Results,
		HasMore:     page.HasMore,
		NextPage:    page.NextPage,
	})
}

func parseHybridSearchOptions(raw json.RawMessage, param string) (*domain.VectorStoreHybridSearchOptions, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, domain.NewValidationError(param, param+" must be an object")
	}
	for key := range payload {
		switch key {
		case "embedding_weight", "text_weight":
		default:
			return nil, domain.NewValidationError(param, "unsupported "+param+" field "+`"`+key+`"`)
		}
	}

	options := &domain.VectorStoreHybridSearchOptions{
		EmbeddingWeight: 1,
		TextWeight:      1,
	}
	if rawWeight, ok := payload["embedding_weight"]; ok && rawWeight != nil {
		weight, ok := rawWeight.(float64)
		if !ok {
			return nil, domain.NewValidationError(param+".embedding_weight", param+".embedding_weight must be a number")
		}
		if weight < 0 {
			return nil, domain.NewValidationError(param+".embedding_weight", param+".embedding_weight must be non-negative")
		}
		options.EmbeddingWeight = weight
	}
	if rawWeight, ok := payload["text_weight"]; ok && rawWeight != nil {
		weight, ok := rawWeight.(float64)
		if !ok {
			return nil, domain.NewValidationError(param+".text_weight", param+".text_weight must be a number")
		}
		if weight < 0 {
			return nil, domain.NewValidationError(param+".text_weight", param+".text_weight must be non-negative")
		}
		options.TextWeight = weight
	}
	if options.EmbeddingWeight <= 0 && options.TextWeight <= 0 {
		return nil, domain.NewValidationError(param, param+".embedding_weight or "+param+".text_weight must be greater than zero")
	}
	return options, nil
}

func (h *retrievalHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, payload := MapError(r.Context(), h.logger, err)
	WriteJSON(w, status, apiErrorPayload{Error: payload})
}

func parseListFilesQuery(r *http.Request) (domain.ListFilesQuery, error) {
	values := r.URL.Query()
	query := domain.ListFilesQuery{
		Purpose: values.Get("purpose"),
		After:   values.Get("after"),
		Limit:   defaultFilesLimit,
		Order:   domain.ListOrderDesc,
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxFilesLimit {
			return domain.ListFilesQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 10000")
		}
		query.Limit = limit
	}
	if rawOrder := values.Get("order"); rawOrder != "" {
		if rawOrder != domain.ListOrderAsc && rawOrder != domain.ListOrderDesc {
			return domain.ListFilesQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
		query.Order = rawOrder
	}
	return query, nil
}

func parseListVectorStoresQuery(r *http.Request) (domain.ListVectorStoresQuery, error) {
	values := r.URL.Query()
	query := domain.ListVectorStoresQuery{
		After:  values.Get("after"),
		Before: values.Get("before"),
		Limit:  defaultVectorStoresLimit,
		Order:  domain.ListOrderDesc,
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxVectorStoresLimit {
			return domain.ListVectorStoresQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	if rawOrder := values.Get("order"); rawOrder != "" {
		if rawOrder != domain.ListOrderAsc && rawOrder != domain.ListOrderDesc {
			return domain.ListVectorStoresQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
		query.Order = rawOrder
	}
	return query, nil
}

func parseListVectorStoreFilesQuery(r *http.Request) (domain.ListVectorStoreFilesQuery, error) {
	values := r.URL.Query()
	query := domain.ListVectorStoreFilesQuery{
		After:  values.Get("after"),
		Before: values.Get("before"),
		Filter: values.Get("filter"),
		Limit:  defaultVectorStoreFilesLimit,
		Order:  domain.ListOrderDesc,
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxVectorStoreFilesLimit {
			return domain.ListVectorStoreFilesQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	if rawOrder := values.Get("order"); rawOrder != "" {
		if rawOrder != domain.ListOrderAsc && rawOrder != domain.ListOrderDesc {
			return domain.ListVectorStoreFilesQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
		query.Order = rawOrder
	}
	if filter := strings.TrimSpace(query.Filter); filter != "" {
		switch filter {
		case "in_progress", "completed", "failed", "cancelled":
		default:
			return domain.ListVectorStoreFilesQuery{}, domain.NewValidationError("filter", "filter must be one of in_progress, completed, failed, or cancelled")
		}
	}
	return query, nil
}

func parseVectorStoreSearchQuery(raw json.RawMessage) ([]string, any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, domain.NewValidationError("query", "query is required")
	}

	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil || strings.TrimSpace(value) == "" {
			return nil, nil, domain.NewValidationError("query", "query must be a non-empty string")
		}
		return []string{value}, value, nil
	}

	var values []string
	if err := json.Unmarshal(trimmed, &values); err != nil || len(values) == 0 {
		return nil, nil, domain.NewValidationError("query", "query must be a string or non-empty array of strings")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, nil, domain.NewValidationError("query", "query array must not contain empty strings")
		}
		out = append(out, value)
	}
	return out, out, nil
}

func parseVectorStoreExpiresAfter(raw json.RawMessage) (*domain.VectorStoreExpirationPolicy, *int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, nil
	}

	var payload struct {
		Anchor string `json:"anchor"`
		Days   int    `json:"days"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, nil, domain.NewValidationError("expires_after", "expires_after must be an object")
	}
	if payload.Anchor != "last_active_at" {
		return nil, nil, domain.NewValidationError("expires_after.anchor", "expires_after.anchor must be last_active_at")
	}
	if payload.Days < 1 {
		return nil, nil, domain.NewValidationError("expires_after.days", "expires_after.days must be positive")
	}

	expiresAt := domain.NowUTC().Unix() + int64(payload.Days*86400)
	return &domain.VectorStoreExpirationPolicy{
		Anchor: payload.Anchor,
		Days:   payload.Days,
	}, &expiresAt, nil
}

func parseFileExpiresAt(form *multipart.Form) (*int64, error) {
	if form == nil {
		return nil, nil
	}
	anchors := form.Value["expires_after[anchor]"]
	secondsValues := form.Value["expires_after[seconds]"]
	if len(anchors) == 0 && len(secondsValues) == 0 {
		return nil, nil
	}
	if len(anchors) == 0 || len(secondsValues) == 0 {
		return nil, domain.NewValidationError("expires_after", "expires_after requires both anchor and seconds")
	}
	if anchors[0] != "created_at" {
		return nil, domain.NewValidationError("expires_after.anchor", "expires_after.anchor must be created_at")
	}
	seconds, err := strconv.Atoi(secondsValues[0])
	if err != nil || seconds < 1 {
		return nil, domain.NewValidationError("expires_after.seconds", "expires_after.seconds must be a positive integer")
	}
	expiresAt := domain.NowUTC().Unix() + int64(seconds)
	return &expiresAt, nil
}

func fileObjectPayload(file domain.StoredFile) fileObjectResponse {
	var status *string
	if strings.TrimSpace(file.Status) != "" {
		status = &file.Status
	}
	return fileObjectResponse{
		ID:            file.ID,
		Object:        "file",
		Bytes:         file.Bytes,
		CreatedAt:     file.CreatedAt,
		ExpiresAt:     file.ExpiresAt,
		Filename:      file.Filename,
		Purpose:       file.Purpose,
		Status:        status,
		StatusDetails: file.StatusDetails,
	}
}

func vectorStorePayload(store domain.StoredVectorStore) vectorStoreObjectResponse {
	return vectorStoreObjectResponse{
		ID:           store.ID,
		Object:       "vector_store",
		CreatedAt:    store.CreatedAt,
		Name:         store.Name,
		Metadata:     store.Metadata,
		Status:       store.Status,
		UsageBytes:   store.UsageBytes,
		FileCounts:   store.FileCounts,
		LastActiveAt: store.LastActiveAt,
		ExpiresAfter: store.ExpiresAfter,
		ExpiresAt:    store.ExpiresAt,
	}
}

func vectorStoreFilePayload(file domain.StoredVectorStoreFile) vectorStoreFileObjectResponse {
	return vectorStoreFileObjectResponse{
		ID:               file.ID,
		Object:           "vector_store.file",
		CreatedAt:        file.CreatedAt,
		UsageBytes:       file.UsageBytes,
		VectorStoreID:    file.VectorStoreID,
		Status:           file.Status,
		LastError:        file.LastError,
		Attributes:       file.Attributes,
		ChunkingStrategy: file.ChunkingStrategy,
	}
}

func firstFileID(files []fileObjectResponse) *string {
	if len(files) == 0 {
		return nil
	}
	return &files[0].ID
}

func lastFileID(files []fileObjectResponse) *string {
	if len(files) == 0 {
		return nil
	}
	return &files[len(files)-1].ID
}

func firstVectorStoreID(stores []vectorStoreObjectResponse) *string {
	if len(stores) == 0 {
		return nil
	}
	return &stores[0].ID
}

func lastVectorStoreID(stores []vectorStoreObjectResponse) *string {
	if len(stores) == 0 {
		return nil
	}
	return &stores[len(stores)-1].ID
}

func firstVectorStoreFileID(files []vectorStoreFileObjectResponse) *string {
	if len(files) == 0 {
		return nil
	}
	return &files[0].ID
}

func lastVectorStoreFileID(files []vectorStoreFileObjectResponse) *string {
	if len(files) == 0 {
		return nil
	}
	return &files[len(files)-1].ID
}

func sanitizeContentDispositionFilename(name string) string {
	replacer := strings.NewReplacer("\\", "_", "\"", "_", "\n", "_", "\r", "_")
	return replacer.Replace(name)
}
