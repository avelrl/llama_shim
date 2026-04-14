package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage/sqlite"
)

type containerHandler struct {
	logger        *slog.Logger
	manager       localCodeInterpreterContainerManager
	fileStore     LocalCodeInterpreterFileStore
	serviceLimits ServiceLimits
}

type containerObjectResponse struct {
	ID           string                                           `json:"id"`
	Object       string                                           `json:"object"`
	CreatedAt    int64                                            `json:"created_at"`
	Status       string                                           `json:"status"`
	ExpiresAfter *domain.CodeInterpreterContainerExpirationPolicy `json:"expires_after,omitempty"`
	LastActiveAt int64                                            `json:"last_active_at"`
	MemoryLimit  string                                           `json:"memory_limit"`
	Name         string                                           `json:"name"`
}

type containerListResponse struct {
	Object  string                    `json:"object"`
	Data    []containerObjectResponse `json:"data"`
	FirstID *string                   `json:"first_id"`
	LastID  *string                   `json:"last_id"`
	HasMore bool                      `json:"has_more"`
}

type containerDeletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

type containerFileObjectResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	CreatedAt   int64  `json:"created_at"`
	Bytes       int64  `json:"bytes"`
	ContainerID string `json:"container_id"`
	Path        string `json:"path"`
	Source      string `json:"source"`
}

type containerFileListResponse struct {
	Object  string                        `json:"object"`
	Data    []containerFileObjectResponse `json:"data"`
	FirstID *string                       `json:"first_id"`
	LastID  *string                       `json:"last_id"`
	HasMore bool                          `json:"has_more"`
}

type containerFileDeletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

func newContainerHandler(logger *slog.Logger, runtime LocalCodeInterpreterRuntimeConfig, files LocalCodeInterpreterFileStore, sessions LocalCodeInterpreterSessionStore, serviceLimits ServiceLimits) *containerHandler {
	return &containerHandler{
		logger:        logger,
		manager:       newLocalCodeInterpreterContainerManager(runtime, files, sessions),
		fileStore:     files,
		serviceLimits: normalizeServiceLimits(serviceLimits),
	}
}

func (h *containerHandler) createContainer(w http.ResponseWriter, r *http.Request) {
	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}
	var rawFields map[string]json.RawMessage
	if trimmed := bytes.TrimSpace(rawBody); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(rawBody, &rawFields); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
			return
		}
	} else {
		rawFields = map[string]json.RawMessage{}
	}
	for key := range rawFields {
		switch key {
		case "name", "memory_limit", "expires_after", "file_ids":
		default:
			h.writeError(w, r, domain.NewValidationError("body", "unsupported container field "+`"`+key+`"`+" in shim-local mode"))
			return
		}
	}

	var request struct {
		Name         string          `json:"name"`
		MemoryLimit  string          `json:"memory_limit,omitempty"`
		ExpiresAfter json.RawMessage `json:"expires_after,omitempty"`
		FileIDs      []string        `json:"file_ids,omitempty"`
	}
	if len(rawFields) > 0 {
		if err := json.Unmarshal(rawBody, &request); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
			return
		}
	}
	expiresAfterMinutes, err := parseContainerExpiresAfter(request.ExpiresAfter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	session, err := h.manager.createContainer(r.Context(), strings.TrimSpace(request.Name), request.MemoryLimit, expiresAfterMinutes)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	for _, fileID := range request.FileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			h.writeError(w, r, domain.NewValidationError("file_ids", "file_ids must not contain empty values"))
			return
		}
		if _, err := h.manager.createStoredContainerFile(r.Context(), session.ID, fileID); err != nil {
			_ = h.manager.deleteContainer(r.Context(), session.ID)
			h.writeError(w, r, err)
			return
		}
	}
	WriteJSON(w, http.StatusOK, containerPayload(session))
}

func (h *containerHandler) listContainers(w http.ResponseWriter, r *http.Request) {
	query, err := parseListContainersQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	page, err := h.manager.listContainers(r.Context(), query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	data := make([]containerObjectResponse, 0, len(page.Sessions))
	for _, session := range page.Sessions {
		data = append(data, containerPayload(session))
	}
	WriteJSON(w, http.StatusOK, containerListResponse{
		Object:  "list",
		Data:    data,
		FirstID: firstContainerID(data),
		LastID:  lastContainerID(data),
		HasMore: page.HasMore,
	})
}

func (h *containerHandler) getContainer(w http.ResponseWriter, r *http.Request) {
	session, err := h.manager.getContainer(r.Context(), r.PathValue("container_id"), true)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, containerPayload(session))
}

func (h *containerHandler) deleteContainer(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	if err := h.manager.deleteContainer(r.Context(), containerID); err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, containerDeletionResponse{
		ID:      containerID,
		Object:  "container.deleted",
		Deleted: true,
	})
}

func (h *containerHandler) createContainerFile(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	maxUploadBytes := h.serviceLimits.RetrievalFileUploadBytes
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20)
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed multipart form body", "")
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
		containerFile, err := h.manager.createUploadedContainerFile(r.Context(), containerID, header.Filename, content)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, containerFilePayload(containerFile))
		return
	}

	rawBody, err := readJSONBody(w, r)
	if err != nil {
		return
	}
	var rawFields map[string]json.RawMessage
	if trimmed := bytes.TrimSpace(rawBody); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(rawBody, &rawFields); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
			return
		}
	} else {
		rawFields = map[string]json.RawMessage{}
	}
	for key := range rawFields {
		if key != "file_id" {
			h.writeError(w, r, domain.NewValidationError("body", "unsupported container file field "+`"`+key+`"`+" in shim-local mode"))
			return
		}
	}
	var request struct {
		FileID string `json:"file_id"`
	}
	if len(rawFields) > 0 {
		if err := json.Unmarshal(rawBody, &request); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "malformed JSON body", "")
			return
		}
	}
	containerFile, err := h.manager.createStoredContainerFile(r.Context(), containerID, strings.TrimSpace(request.FileID))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, containerFilePayload(containerFile))
}

func (h *containerHandler) listContainerFiles(w http.ResponseWriter, r *http.Request) {
	query, err := parseListContainerFilesQuery(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	query.ContainerID = r.PathValue("container_id")
	page, err := h.manager.listContainerFiles(r.Context(), query.ContainerID, query)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	data := make([]containerFileObjectResponse, 0, len(page.Files))
	for _, file := range page.Files {
		data = append(data, containerFilePayload(file))
	}
	WriteJSON(w, http.StatusOK, containerFileListResponse{
		Object:  "list",
		Data:    data,
		FirstID: firstContainerFileID(data),
		LastID:  lastContainerFileID(data),
		HasMore: page.HasMore,
	})
}

func (h *containerHandler) getContainerFile(w http.ResponseWriter, r *http.Request) {
	containerFile, _, err := h.manager.getContainerFile(r.Context(), r.PathValue("container_id"), r.PathValue("file_id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, containerFilePayload(containerFile))
}

func (h *containerHandler) getContainerFileContent(w http.ResponseWriter, r *http.Request) {
	containerFile, backingFile, err := h.manager.getContainerFile(r.Context(), r.PathValue("container_id"), r.PathValue("file_id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeContentDispositionFilename(pathBaseFilename(containerFile.Path, backingFile.Filename))+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(backingFile.Content)
}

func (h *containerHandler) deleteContainerFile(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	fileID := r.PathValue("file_id")
	if err := h.manager.deleteContainerFile(r.Context(), containerID, fileID); err != nil {
		h.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, containerFileDeletionResponse{
		ID:      fileID,
		Object:  "container.file.deleted",
		Deleted: true,
	})
}

func (h *containerHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *domain.ValidationError
	switch {
	case errors.Is(err, sqlite.ErrNotFound):
		WriteError(w, http.StatusNotFound, "invalid_request_error", "resource not found", "")
	case errors.As(err, &validationErr):
		WriteError(w, http.StatusBadRequest, "invalid_request_error", validationErr.Message, validationErr.Param)
	default:
		h.logger.ErrorContext(r.Context(), "container request failed", "request_id", RequestIDFromContext(r.Context()), "err", err)
		WriteError(w, http.StatusInternalServerError, "server_error", "internal server error", "")
	}
}

func containerPayload(session domain.CodeInterpreterSession) containerObjectResponse {
	createdAt, _ := domain.ParseTimeUnix(session.CreatedAt)
	lastActiveAt, _ := domain.ParseTimeUnix(session.LastActiveAt)
	return containerObjectResponse{
		ID:           session.ID,
		Object:       "container",
		CreatedAt:    createdAt,
		Status:       session.Status,
		ExpiresAfter: localCodeInterpreterSessionExpirationPolicy(session),
		LastActiveAt: lastActiveAt,
		MemoryLimit:  session.MemoryLimit,
		Name:         session.Name,
	}
}

func containerFilePayload(file domain.CodeInterpreterContainerFile) containerFileObjectResponse {
	return containerFileObjectResponse{
		ID:          file.ID,
		Object:      "container.file",
		CreatedAt:   file.CreatedAt,
		Bytes:       file.Bytes,
		ContainerID: file.ContainerID,
		Path:        file.Path,
		Source:      file.Source,
	}
}

func parseContainerExpiresAfter(raw json.RawMessage) (int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return defaultLocalCodeInterpreterContainerExpiryMins, nil
	}
	var payload struct {
		Anchor  string `json:"anchor"`
		Minutes int    `json:"minutes"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return 0, domain.NewValidationError("expires_after", "expires_after must be an object")
	}
	if strings.TrimSpace(payload.Anchor) != "last_active_at" {
		return 0, domain.NewValidationError("expires_after.anchor", "expires_after.anchor must be last_active_at")
	}
	if payload.Minutes <= 0 {
		return 0, domain.NewValidationError("expires_after.minutes", "expires_after.minutes must be a positive integer")
	}
	return payload.Minutes, nil
}

func parseListContainersQuery(r *http.Request) (domain.ListCodeInterpreterSessionsQuery, error) {
	values := r.URL.Query()
	query := domain.ListCodeInterpreterSessionsQuery{
		After: strings.TrimSpace(values.Get("after")),
		Limit: defaultLocalCodeInterpreterContainersListLimit,
		Order: domain.ListOrderDesc,
		Name:  strings.TrimSpace(values.Get("name")),
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxLocalCodeInterpreterContainersListLimit {
			return domain.ListCodeInterpreterSessionsQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	if rawOrder := strings.TrimSpace(values.Get("order")); rawOrder != "" {
		switch rawOrder {
		case domain.ListOrderAsc, domain.ListOrderDesc:
			query.Order = rawOrder
		default:
			return domain.ListCodeInterpreterSessionsQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
	}
	return query, nil
}

func parseListContainerFilesQuery(r *http.Request) (domain.ListCodeInterpreterContainerFilesQuery, error) {
	values := r.URL.Query()
	query := domain.ListCodeInterpreterContainerFilesQuery{
		After: strings.TrimSpace(values.Get("after")),
		Limit: defaultLocalCodeInterpreterContainerFilesLimit,
		Order: domain.ListOrderDesc,
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxLocalCodeInterpreterContainerFilesListLimit {
			return domain.ListCodeInterpreterContainerFilesQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	if rawOrder := strings.TrimSpace(values.Get("order")); rawOrder != "" {
		switch rawOrder {
		case domain.ListOrderAsc, domain.ListOrderDesc:
			query.Order = rawOrder
		default:
			return domain.ListCodeInterpreterContainerFilesQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
	}
	return query, nil
}

func firstContainerID(data []containerObjectResponse) *string {
	if len(data) == 0 {
		return nil
	}
	value := data[0].ID
	return &value
}

func lastContainerID(data []containerObjectResponse) *string {
	if len(data) == 0 {
		return nil
	}
	value := data[len(data)-1].ID
	return &value
}

func firstContainerFileID(data []containerFileObjectResponse) *string {
	if len(data) == 0 {
		return nil
	}
	value := data[0].ID
	return &value
}

func lastContainerFileID(data []containerFileObjectResponse) *string {
	if len(data) == 0 {
		return nil
	}
	value := data[len(data)-1].ID
	return &value
}

func pathBaseFilename(containerPath string, fallback string) string {
	if base := path.Base(strings.TrimSpace(containerPath)); base != "" && base != "." && base != "/" {
		return base
	}
	return fallback
}
