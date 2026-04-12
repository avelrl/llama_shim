package testutil

import (
	"context"
	"fmt"
	"strings"

	"llama_shim/internal/sandbox"
)

type FakeSandboxBackend struct {
	CreateSessionFunc  func(context.Context, string) error
	DestroySessionFunc func(context.Context, string) error
	KindValue          string
	ExecuteFunc        func(context.Context, sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
	UploadFileFunc     func(context.Context, string, sandbox.SessionFile) error
}

func (b FakeSandboxBackend) Kind() string {
	if strings.TrimSpace(b.KindValue) != "" {
		return b.KindValue
	}
	return "fake"
}

func (b FakeSandboxBackend) CreateSession(ctx context.Context, sessionID string) error {
	if b.CreateSessionFunc != nil {
		return b.CreateSessionFunc(ctx, sessionID)
	}
	return nil
}

func (b FakeSandboxBackend) UploadFile(ctx context.Context, sessionID string, file sandbox.SessionFile) error {
	if b.UploadFileFunc != nil {
		return b.UploadFileFunc(ctx, sessionID, file)
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
