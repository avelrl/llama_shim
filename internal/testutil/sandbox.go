package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/sandbox"
)

type FakeSandboxBackend struct {
	CreateSessionFunc  func(context.Context, sandbox.CreateSessionRequest) error
	DestroySessionFunc func(context.Context, string) error
	DeleteFileFunc     func(context.Context, string, string) error
	KindValue          string
	ExecuteFunc        func(context.Context, sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
	ListFilesFunc      func(context.Context, string) ([]sandbox.SessionFile, error)
	ListFileInfosFunc  func(context.Context, string, int, int64) ([]sandbox.SessionFileInfo, error)
	ReadFileFunc       func(context.Context, string, string, int64) (sandbox.SessionFile, error)
	UploadFileFunc     func(context.Context, string, sandbox.SessionFile) error
}

func (b FakeSandboxBackend) Kind() string {
	if strings.TrimSpace(b.KindValue) != "" {
		return b.KindValue
	}
	return "fake"
}

func (b FakeSandboxBackend) CreateSession(ctx context.Context, req sandbox.CreateSessionRequest) error {
	if b.CreateSessionFunc != nil {
		return b.CreateSessionFunc(ctx, req)
	}
	return nil
}

func (b FakeSandboxBackend) UploadFile(ctx context.Context, sessionID string, file sandbox.SessionFile) error {
	if b.UploadFileFunc != nil {
		return b.UploadFileFunc(ctx, sessionID, file)
	}
	return nil
}

func (b FakeSandboxBackend) ListFiles(ctx context.Context, sessionID string) ([]sandbox.SessionFile, error) {
	if b.ListFilesFunc != nil {
		return b.ListFilesFunc(ctx, sessionID)
	}
	return nil, nil
}

func (b FakeSandboxBackend) ListFileInfos(ctx context.Context, sessionID string, maxEntries int, maxHashBytes int64) ([]sandbox.SessionFileInfo, error) {
	if b.ListFileInfosFunc != nil {
		return b.ListFileInfosFunc(ctx, sessionID, maxEntries, maxHashBytes)
	}

	files, err := b.ListFiles(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if maxEntries > 0 && len(files) > maxEntries {
		return nil, sandbox.ErrSessionSnapshotTooLarge
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})
	infos := make([]sandbox.SessionFileInfo, 0, len(files))
	for _, file := range files {
		info := sandbox.SessionFileInfo{
			Name: file.Name,
			Size: int64(len(file.Content)),
		}
		if maxHashBytes <= 0 || int64(len(file.Content)) <= maxHashBytes {
			sum := sha256.Sum256(file.Content)
			info.SHA256 = hex.EncodeToString(sum[:])
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (b FakeSandboxBackend) ReadFile(ctx context.Context, sessionID string, name string, maxBytes int64) (sandbox.SessionFile, error) {
	if b.ReadFileFunc != nil {
		return b.ReadFileFunc(ctx, sessionID, name, maxBytes)
	}

	files, err := b.ListFiles(ctx, sessionID)
	if err != nil {
		return sandbox.SessionFile{}, err
	}
	for _, file := range files {
		if file.Name != name {
			continue
		}
		if maxBytes > 0 && int64(len(file.Content)) > maxBytes {
			return sandbox.SessionFile{}, sandbox.ErrSessionFileTooLarge
		}
		return sandbox.SessionFile{
			Name:    file.Name,
			Content: append([]byte(nil), file.Content...),
		}, nil
	}
	return sandbox.SessionFile{}, sandbox.ErrSessionFileNotFound
}

func (b FakeSandboxBackend) DeleteFile(ctx context.Context, sessionID string, name string) error {
	if b.DeleteFileFunc != nil {
		return b.DeleteFileFunc(ctx, sessionID, name)
	}
	return nil
}

func (b FakeSandboxBackend) ExecutePython(ctx context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	if b.ExecuteFunc != nil {
		return b.ExecuteFunc(ctx, req)
	}

	switch strings.TrimSpace(req.Code) {
	case "print(2+2)":
		return sandbox.ExecuteResult{Logs: "4\n"}, nil
	case `print("result=2.0")`:
		return sandbox.ExecuteResult{Logs: "result=2.0\n"}, nil
	default:
		return sandbox.ExecuteResult{}, fmt.Errorf("unexpected fake sandbox code: %s", req.Code)
	}
}

func (b FakeSandboxBackend) DestroySession(ctx context.Context, sessionID string) error {
	if b.DestroySessionFunc != nil {
		return b.DestroySessionFunc(ctx, sessionID)
	}
	return nil
}
