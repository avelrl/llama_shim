package testutil

import (
	"context"
	"fmt"
	"strings"

	"llama_shim/internal/sandbox"
)

type FakeSandboxBackend struct {
	KindValue   string
	ExecuteFunc func(context.Context, sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
}

func (b FakeSandboxBackend) Kind() string {
	if strings.TrimSpace(b.KindValue) != "" {
		return b.KindValue
	}
	return "fake"
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
