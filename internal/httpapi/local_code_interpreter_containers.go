package httpapi

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/storage/sqlite"
)

const (
	defaultLocalCodeInterpreterContainerMemoryLimit  = "1g"
	defaultLocalCodeInterpreterContainerExpiryMins   = 20
	maxLocalCodeInterpreterContainerFilesListLimit   = 100
	defaultLocalCodeInterpreterContainerFilesLimit   = 20
	maxLocalCodeInterpreterContainersListLimit       = 100
	defaultLocalCodeInterpreterContainersListLimit   = 20
	localCodeInterpreterGeneratedArtifactsPurpose    = "assistants_output"
	localCodeInterpreterUploadedContainerFilePurpose = "user_data"
)

var localCodeInterpreterAllowedMemoryLimits = map[string]struct{}{
	"1g":  {},
	"4g":  {},
	"16g": {},
	"64g": {},
}

type localCodeInterpreterContainerManager struct {
	runtime  LocalCodeInterpreterRuntimeConfig
	files    LocalCodeInterpreterFileStore
	sessions LocalCodeInterpreterSessionStore
}

func newLocalCodeInterpreterContainerManager(runtime LocalCodeInterpreterRuntimeConfig, files LocalCodeInterpreterFileStore, sessions LocalCodeInterpreterSessionStore) localCodeInterpreterContainerManager {
	return localCodeInterpreterContainerManager{
		runtime:  runtime,
		files:    files,
		sessions: sessions,
	}
}

func (m localCodeInterpreterContainerManager) enabled() bool {
	return m.runtime.Enabled() && m.files != nil && m.sessions != nil
}

func (m localCodeInterpreterContainerManager) createContainer(ctx context.Context, owner string, name string, memoryLimit string, expiresAfterMinutes int) (domain.CodeInterpreterSession, error) {
	if !m.enabled() {
		return domain.CodeInterpreterSession{}, localCodeInterpreterDisabledError()
	}
	if strings.TrimSpace(name) == "" {
		return domain.CodeInterpreterSession{}, domain.NewValidationError("name", "name is required")
	}
	normalizedMemoryLimit, err := normalizeLocalCodeInterpreterMemoryLimit(memoryLimit)
	if err != nil {
		return domain.CodeInterpreterSession{}, domain.NewValidationError("memory_limit", err.Error())
	}
	if expiresAfterMinutes <= 0 {
		expiresAfterMinutes = defaultLocalCodeInterpreterContainerExpiryMins
	}

	sessionID, err := domain.NewPrefixedID("cntr")
	if err != nil {
		return domain.CodeInterpreterSession{}, fmt.Errorf("generate container id: %w", err)
	}
	now := domain.NowUTC()
	if err := m.runtime.Backend.CreateSession(ctx, sandbox.CreateSessionRequest{
		SessionID:   sessionID,
		MemoryLimit: normalizedMemoryLimit,
	}); err != nil {
		if errors.Is(err, sandbox.ErrDisabled) {
			return domain.CodeInterpreterSession{}, localCodeInterpreterDisabledError()
		}
		return domain.CodeInterpreterSession{}, fmt.Errorf("create local code interpreter container: %w", err)
	}

	session := domain.CodeInterpreterSession{
		ID:                  sessionID,
		Owner:               strings.TrimSpace(owner),
		Backend:             m.runtime.Backend.Kind(),
		Status:              "running",
		Name:                strings.TrimSpace(name),
		MemoryLimit:         normalizedMemoryLimit,
		ExpiresAfterMinutes: expiresAfterMinutes,
		CreatedAt:           domain.FormatTime(now),
		LastActiveAt:        domain.FormatTime(now),
	}
	if err := m.sessions.SaveCodeInterpreterSession(ctx, session); err != nil {
		_ = m.runtime.Backend.DestroySession(ctx, sessionID)
		return domain.CodeInterpreterSession{}, err
	}
	return session, nil
}

func (m localCodeInterpreterContainerManager) listContainers(ctx context.Context, query domain.ListCodeInterpreterSessionsQuery, owner string) (domain.CodeInterpreterSessionPage, error) {
	page, err := m.sessions.ListCodeInterpreterSessions(ctx, query)
	if err != nil {
		return domain.CodeInterpreterSessionPage{}, err
	}
	owner = strings.TrimSpace(owner)
	if owner != "" {
		filtered := make([]domain.CodeInterpreterSession, 0, len(page.Sessions))
		for _, session := range page.Sessions {
			if session.Owner == owner {
				filtered = append(filtered, session)
			}
		}
		page.Sessions = filtered
		page.HasMore = false
	}
	for i := range page.Sessions {
		session, expireErr := m.expireIfNeeded(ctx, page.Sessions[i])
		if expireErr != nil {
			return domain.CodeInterpreterSessionPage{}, expireErr
		}
		page.Sessions[i] = session
	}
	return page, nil
}

func (m localCodeInterpreterContainerManager) getContainer(ctx context.Context, id string, touch bool, owner string) (domain.CodeInterpreterSession, error) {
	session, err := m.sessions.GetCodeInterpreterSession(ctx, id)
	if err != nil {
		return domain.CodeInterpreterSession{}, err
	}
	owner = strings.TrimSpace(owner)
	if owner != "" && session.Owner != owner {
		return domain.CodeInterpreterSession{}, sqlite.ErrNotFound
	}
	session, err = m.expireIfNeeded(ctx, session)
	if err != nil {
		return domain.CodeInterpreterSession{}, err
	}
	if touch && session.Status == "running" {
		now := domain.FormatTime(domain.NowUTC())
		if err := m.sessions.TouchCodeInterpreterSession(ctx, session.ID, now); err != nil {
			return domain.CodeInterpreterSession{}, err
		}
		session.LastActiveAt = now
	}
	return session, nil
}

func (m localCodeInterpreterContainerManager) ensureContainerSession(ctx context.Context, id string, owner string) (domain.CodeInterpreterSession, error) {
	session, err := m.getContainer(ctx, id, false, owner)
	if err != nil {
		return domain.CodeInterpreterSession{}, err
	}
	if session.Status != "running" {
		return domain.CodeInterpreterSession{}, domain.NewValidationError("tools", "code_interpreter container "+`"`+id+`"`+" has expired")
	}
	if err := m.runtime.Backend.CreateSession(ctx, sandbox.CreateSessionRequest{
		SessionID:   session.ID,
		MemoryLimit: session.MemoryLimit,
	}); err != nil {
		if errors.Is(err, sandbox.ErrDisabled) {
			return domain.CodeInterpreterSession{}, localCodeInterpreterDisabledError()
		}
		return domain.CodeInterpreterSession{}, fmt.Errorf("ensure local code interpreter container: %w", err)
	}
	if err := m.restoreContainerFiles(ctx, session.ID); err != nil {
		return domain.CodeInterpreterSession{}, err
	}
	return session, nil
}

func (m localCodeInterpreterContainerManager) deleteContainer(ctx context.Context, id string, owner string) error {
	session, err := m.getContainer(ctx, id, false, owner)
	if err != nil {
		return err
	}
	files, err := m.listAllContainerFiles(ctx, id)
	if err != nil {
		return err
	}
	if session.Backend == m.runtime.Backend.Kind() && m.runtime.Backend != nil {
		if err := m.runtime.Backend.DestroySession(ctx, id); err != nil && !errors.Is(err, sandbox.ErrSessionNotFound) && !errors.Is(err, sandbox.ErrDisabled) {
			return err
		}
	}
	if err := m.sessions.DeleteCodeInterpreterSession(ctx, id); err != nil {
		return err
	}
	return m.cleanupOwnedBackingFiles(ctx, files)
}

func (m localCodeInterpreterContainerManager) restoreContainerFiles(ctx context.Context, containerID string) error {
	files, err := m.listAllContainerFiles(ctx, containerID)
	if err != nil {
		return err
	}
	for _, file := range files {
		backingFile, err := m.files.GetFile(ctx, file.BackingFileID)
		if err != nil {
			if errors.Is(err, sqlite.ErrNotFound) {
				continue
			}
			return err
		}
		if err := m.runtime.Backend.UploadFile(ctx, containerID, sandbox.SessionFile{
			Name:    path.Base(file.Path),
			Content: backingFile.Content,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m localCodeInterpreterContainerManager) stageInputFiles(ctx context.Context, containerID string, inputFiles []localCodeInterpreterInputFile) ([]localCodeInterpreterInputFile, error) {
	if len(inputFiles) == 0 {
		return nil, nil
	}
	prepared := make([]localCodeInterpreterInputFile, 0, len(inputFiles))
	for _, inputFile := range inputFiles {
		preparedFile, err := m.persistInputFile(ctx, inputFile)
		if err != nil {
			return nil, err
		}
		if _, err := m.addStoredFileToContainer(ctx, containerID, domain.StoredFile{
			ID:       preparedFile.FileID,
			Filename: preparedFile.Filename,
			Bytes:    int64(len(preparedFile.Content)),
			Content:  preparedFile.Content,
		}, preparedFile.WorkspaceName, "user", preparedFile.DeleteBackingFile); err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedFile)
	}
	return prepared, nil
}

func (m localCodeInterpreterContainerManager) addStoredFileToContainer(ctx context.Context, containerID string, storedFile domain.StoredFile, workspaceName string, source string, deleteBackingFile bool) (domain.CodeInterpreterContainerFile, error) {
	if _, err := m.ensureContainerSession(ctx, containerID, ""); err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	containerFile, err := m.saveContainerFileMetadata(ctx, containerID, storedFile.ID, workspaceName, source, storedFile.Bytes, deleteBackingFile)
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	if err := m.runtime.Backend.UploadFile(ctx, containerID, sandbox.SessionFile{
		Name:    path.Base(containerFile.Path),
		Content: storedFile.Content,
	}); err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	return containerFile, nil
}

func (m localCodeInterpreterContainerManager) createUploadedContainerFile(ctx context.Context, containerID string, filename string, content []byte) (domain.CodeInterpreterContainerFile, error) {
	fileID, err := domain.NewPrefixedID("file")
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	storedFile := domain.StoredFile{
		ID:        fileID,
		Filename:  filename,
		Purpose:   localCodeInterpreterUploadedContainerFilePurpose,
		Bytes:     int64(len(content)),
		CreatedAt: domain.NowUTC().Unix(),
		Status:    "processed",
		Content:   append([]byte(nil), content...),
	}
	if err := m.files.SaveFile(ctx, storedFile); err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	return m.addStoredFileToContainer(ctx, containerID, storedFile, sanitizeLocalCodeInterpreterWorkspaceName(filename, "uploaded_file"), "user", true)
}

func (m localCodeInterpreterContainerManager) createStoredContainerFile(ctx context.Context, containerID string, fileID string) (domain.CodeInterpreterContainerFile, error) {
	if strings.TrimSpace(fileID) == "" {
		return domain.CodeInterpreterContainerFile{}, domain.NewValidationError("file_id", "file_id is required")
	}
	storedFile, err := m.files.GetFile(ctx, fileID)
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	return m.addStoredFileToContainer(ctx, containerID, storedFile, sanitizeLocalCodeInterpreterWorkspaceName(storedFile.Filename, storedFile.ID), "user", false)
}

func (m localCodeInterpreterContainerManager) persistInputFile(ctx context.Context, inputFile localCodeInterpreterInputFile) (localCodeInterpreterInputFile, error) {
	if strings.TrimSpace(inputFile.FileID) != "" {
		return inputFile, nil
	}
	fileID, err := domain.NewPrefixedID("file")
	if err != nil {
		return localCodeInterpreterInputFile{}, err
	}
	now := domain.NowUTC().Unix()
	storedFile := domain.StoredFile{
		ID:        fileID,
		Filename:  inputFile.Filename,
		Purpose:   localCodeInterpreterUploadedContainerFilePurpose,
		Bytes:     int64(len(inputFile.Content)),
		CreatedAt: now,
		Status:    "processed",
		Content:   append([]byte(nil), inputFile.Content...),
	}
	if err := m.files.SaveFile(ctx, storedFile); err != nil {
		return localCodeInterpreterInputFile{}, err
	}
	inputFile.FileID = storedFile.ID
	inputFile.DeleteBackingFile = true
	return inputFile, nil
}

func (m localCodeInterpreterContainerManager) saveContainerFileMetadata(ctx context.Context, containerID string, backingFileID string, workspaceName string, source string, bytes int64, deleteBackingFile bool) (domain.CodeInterpreterContainerFile, error) {
	if strings.TrimSpace(backingFileID) == "" {
		return domain.CodeInterpreterContainerFile{}, fmt.Errorf("container file backing id is required")
	}
	containerPath := localCodeInterpreterContainerPath(workspaceName)
	replacedFile, err := m.sessions.GetCodeInterpreterContainerFileByPath(ctx, containerID, containerPath)
	if err != nil && !errors.Is(err, sqlite.ErrNotFound) {
		return domain.CodeInterpreterContainerFile{}, err
	}
	containerFileID, err := domain.NewPrefixedID("cfile")
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	saved, err := m.sessions.SaveCodeInterpreterContainerFile(ctx, domain.CodeInterpreterContainerFile{
		ID:                containerFileID,
		ContainerID:       containerID,
		BackingFileID:     backingFileID,
		DeleteBackingFile: deleteBackingFile,
		Path:              containerPath,
		Source:            source,
		Bytes:             bytes,
		CreatedAt:         domain.NowUTC().Unix(),
	})
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	if replacedFile.ID != "" && (replacedFile.ID != saved.ID || replacedFile.BackingFileID != saved.BackingFileID) {
		if err := m.cleanupOwnedBackingFile(ctx, replacedFile); err != nil {
			return domain.CodeInterpreterContainerFile{}, err
		}
	}
	if err := m.touchContainer(ctx, containerID); err != nil {
		return domain.CodeInterpreterContainerFile{}, err
	}
	return saved, nil
}

func (m localCodeInterpreterContainerManager) persistGeneratedFiles(ctx context.Context, containerID string, generated []sandbox.SessionFile) ([]localCodeInterpreterGeneratedFile, error) {
	if len(generated) == 0 {
		return nil, nil
	}
	limits := m.runtime.normalizedLimits()
	saved := make([]localCodeInterpreterGeneratedFile, 0, min(len(generated), limits.GeneratedFiles))
	totalBytes := 0
	now := domain.NowUTC().Unix()
	for _, file := range generated {
		if len(saved) >= limits.GeneratedFiles {
			break
		}
		if len(file.Content) > limits.GeneratedFileBytes {
			continue
		}
		if totalBytes+len(file.Content) > limits.GeneratedTotalBytes {
			continue
		}

		fileID, err := domain.NewPrefixedID("file")
		if err != nil {
			return nil, err
		}
		storedFile := domain.StoredFile{
			ID:        fileID,
			Filename:  path.Base(file.Name),
			Purpose:   localCodeInterpreterGeneratedArtifactsPurpose,
			Bytes:     int64(len(file.Content)),
			CreatedAt: now,
			Status:    "processed",
			Content:   append([]byte(nil), file.Content...),
		}
		if err := m.files.SaveFile(ctx, storedFile); err != nil {
			return nil, err
		}
		containerFile, err := m.saveContainerFileMetadata(ctx, containerID, storedFile.ID, path.Base(file.Name), "assistant", storedFile.Bytes, true)
		if err != nil {
			return nil, err
		}

		totalBytes += len(file.Content)
		saved = append(saved, localCodeInterpreterGeneratedFile{
			Bytes:           storedFile.Bytes,
			FileID:          containerFile.ID,
			BackingFileID:   storedFile.ID,
			Filename:        storedFile.Filename,
			ContainerPath:   containerFile.Path,
			ContainerID:     containerID,
			ContainerSource: containerFile.Source,
		})
	}
	return saved, nil
}

func (m localCodeInterpreterContainerManager) listContainerFiles(ctx context.Context, containerID string, query domain.ListCodeInterpreterContainerFilesQuery, owner string) (domain.CodeInterpreterContainerFilePage, error) {
	session, err := m.getContainer(ctx, containerID, false, owner)
	if err != nil {
		return domain.CodeInterpreterContainerFilePage{}, err
	}
	if session.Status != "running" {
		return domain.CodeInterpreterContainerFilePage{}, domain.NewValidationError("container_id", "container has expired and its files are no longer available")
	}
	if err := m.touchContainer(ctx, containerID); err != nil {
		return domain.CodeInterpreterContainerFilePage{}, err
	}
	return m.sessions.ListCodeInterpreterContainerFiles(ctx, query)
}

func (m localCodeInterpreterContainerManager) getContainerFile(ctx context.Context, containerID string, fileID string, owner string) (domain.CodeInterpreterContainerFile, domain.StoredFile, error) {
	session, err := m.getContainer(ctx, containerID, false, owner)
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, domain.StoredFile{}, err
	}
	if session.Status != "running" {
		return domain.CodeInterpreterContainerFile{}, domain.StoredFile{}, domain.NewValidationError("container_id", "container has expired and its files are no longer available")
	}
	containerFile, err := m.sessions.GetCodeInterpreterContainerFile(ctx, containerID, fileID)
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, domain.StoredFile{}, err
	}
	backingFile, err := m.files.GetFile(ctx, containerFile.BackingFileID)
	if err != nil {
		return domain.CodeInterpreterContainerFile{}, domain.StoredFile{}, err
	}
	if err := m.touchContainer(ctx, containerID); err != nil {
		return domain.CodeInterpreterContainerFile{}, domain.StoredFile{}, err
	}
	return containerFile, backingFile, nil
}

func (m localCodeInterpreterContainerManager) deleteContainerFile(ctx context.Context, containerID string, fileID string, owner string) error {
	containerFile, _, err := m.getContainerFile(ctx, containerID, fileID, owner)
	if err != nil {
		return err
	}
	if err := m.runtime.Backend.DeleteFile(ctx, containerID, path.Base(containerFile.Path)); err != nil && !errors.Is(err, sandbox.ErrSessionNotFound) {
		return err
	}
	if err := m.sessions.DeleteCodeInterpreterContainerFile(ctx, containerID, fileID); err != nil {
		return err
	}
	if err := m.cleanupOwnedBackingFile(ctx, containerFile); err != nil {
		return err
	}
	return m.touchContainer(ctx, containerID)
}

func (m localCodeInterpreterContainerManager) touchContainer(ctx context.Context, containerID string) error {
	now := domain.FormatTime(domain.NowUTC())
	return m.sessions.TouchCodeInterpreterSession(ctx, containerID, now)
}

func (m localCodeInterpreterContainerManager) expireIfNeeded(ctx context.Context, session domain.CodeInterpreterSession) (domain.CodeInterpreterSession, error) {
	if session.Status != "running" || !localCodeInterpreterContainerExpired(session) {
		return session, nil
	}
	if m.runtime.Backend != nil && session.Backend == m.runtime.Backend.Kind() {
		if err := m.runtime.Backend.DestroySession(ctx, session.ID); err != nil && !errors.Is(err, sandbox.ErrSessionNotFound) && !errors.Is(err, sandbox.ErrDisabled) {
			return domain.CodeInterpreterSession{}, err
		}
	}
	files, err := m.listAllContainerFiles(ctx, session.ID)
	if err != nil {
		return domain.CodeInterpreterSession{}, err
	}
	for _, file := range files {
		if err := m.sessions.DeleteCodeInterpreterContainerFile(ctx, session.ID, file.ID); err != nil {
			return domain.CodeInterpreterSession{}, err
		}
		if err := m.cleanupOwnedBackingFile(ctx, file); err != nil {
			return domain.CodeInterpreterSession{}, err
		}
	}
	session.Status = "expired"
	if err := m.sessions.SaveCodeInterpreterSession(ctx, session); err != nil {
		return domain.CodeInterpreterSession{}, err
	}
	return session, nil
}

func (m localCodeInterpreterContainerManager) listAllContainerFiles(ctx context.Context, containerID string) ([]domain.CodeInterpreterContainerFile, error) {
	after := ""
	files := make([]domain.CodeInterpreterContainerFile, 0, defaultLocalCodeInterpreterContainerFilesLimit)
	for {
		page, err := m.sessions.ListCodeInterpreterContainerFiles(ctx, domain.ListCodeInterpreterContainerFilesQuery{
			ContainerID: containerID,
			After:       after,
			Limit:       maxLocalCodeInterpreterContainerFilesListLimit,
			Order:       domain.ListOrderAsc,
		})
		if err != nil {
			return nil, err
		}
		files = append(files, page.Files...)
		if !page.HasMore || len(page.Files) == 0 {
			return files, nil
		}
		after = page.Files[len(page.Files)-1].ID
	}
}

func (m localCodeInterpreterContainerManager) cleanupOwnedBackingFiles(ctx context.Context, files []domain.CodeInterpreterContainerFile) error {
	for _, file := range files {
		if err := m.cleanupOwnedBackingFile(ctx, file); err != nil {
			return err
		}
	}
	return nil
}

func (m localCodeInterpreterContainerManager) cleanupOwnedBackingFile(ctx context.Context, file domain.CodeInterpreterContainerFile) error {
	if !file.DeleteBackingFile || strings.TrimSpace(file.BackingFileID) == "" {
		return nil
	}
	refCount, err := m.sessions.CountCodeInterpreterContainerFileBackingReferences(ctx, file.BackingFileID)
	if err != nil {
		return err
	}
	if refCount > 0 {
		return nil
	}
	if err := m.files.DeleteFile(ctx, file.BackingFileID); err != nil && !errors.Is(err, sqlite.ErrNotFound) {
		return err
	}
	return nil
}

func normalizeLocalCodeInterpreterMemoryLimit(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = defaultLocalCodeInterpreterContainerMemoryLimit
	}
	if _, ok := localCodeInterpreterAllowedMemoryLimits[normalized]; !ok {
		return "", fmt.Errorf("memory_limit must be one of 1g, 4g, 16g, or 64g")
	}
	return normalized, nil
}

func localCodeInterpreterContainerPath(workspaceName string) string {
	return "/mnt/data/" + path.Base(workspaceName)
}

func localCodeInterpreterSessionExpirationPolicy(session domain.CodeInterpreterSession) *domain.CodeInterpreterContainerExpirationPolicy {
	minutes := session.ExpiresAfterMinutes
	if minutes <= 0 {
		minutes = defaultLocalCodeInterpreterContainerExpiryMins
	}
	return &domain.CodeInterpreterContainerExpirationPolicy{
		Anchor:  "last_active_at",
		Minutes: minutes,
	}
}

func localCodeInterpreterContainerExpiresAt(session domain.CodeInterpreterSession) *int64 {
	lastActiveAt, err := domain.ParseTime(session.LastActiveAt)
	if err != nil {
		return nil
	}
	minutes := session.ExpiresAfterMinutes
	if minutes <= 0 {
		minutes = defaultLocalCodeInterpreterContainerExpiryMins
	}
	expiresAt := lastActiveAt.Add(time.Duration(minutes) * time.Minute).Unix()
	return &expiresAt
}

func localCodeInterpreterContainerExpired(session domain.CodeInterpreterSession) bool {
	expiresAt := localCodeInterpreterContainerExpiresAt(session)
	return expiresAt != nil && domain.NowUTC().Unix() >= *expiresAt
}
