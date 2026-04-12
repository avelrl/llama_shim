package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultExecutionTimeout = 20 * time.Second
	defaultOutputLimit      = 64 << 10
)

var ErrDisabled = errors.New("sandbox backend is disabled")

type Backend interface {
	Kind() string
	ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
}

type ExecuteRequest struct {
	Code string
}

type ExecuteResult struct {
	Logs string
}

type UnsafeHostBackend struct {
	PythonBinary string
	Timeout      time.Duration
}

func (b UnsafeHostBackend) Kind() string {
	return "unsafe_host"
}

func (b UnsafeHostBackend) ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	pythonBinary := strings.TrimSpace(b.PythonBinary)
	if pythonBinary == "" {
		pythonBinary = "python3"
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultExecutionTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempDir, err := os.MkdirTemp("", "llama-shim-code-interpreter-*")
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("create code interpreter temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	cmd := exec.CommandContext(execCtx, pythonBinary, "-I", "-S", "-B", "-")
	cmd.Dir = tempDir
	cmd.Env = []string{
		"LC_ALL=C.UTF-8",
		"LANG=C.UTF-8",
		"HOME=/tmp",
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONHASHSEED=0",
		"PYTHONNOUSERSITE=1",
	}

	var logs limitedOutputBuffer
	logs.limit = defaultOutputLimit
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("open python stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return ExecuteResult{}, fmt.Errorf("start python: %w", err)
	}
	if _, err := io.WriteString(stdin, req.Code); err != nil {
		_ = stdin.Close()
		return ExecuteResult{}, fmt.Errorf("write python program: %w", err)
	}
	_ = stdin.Close()

	if err := cmd.Wait(); err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return ExecuteResult{Logs: logs.String()}, fmt.Errorf("sandbox execution timed out: %w", execCtx.Err())
		}
		return ExecuteResult{Logs: logs.String()}, fmt.Errorf("execute python: %w", err)
	}

	return ExecuteResult{Logs: logs.String()}, nil
}

type DockerBackend struct {
	DockerBinary string
	Image        string
	Timeout      time.Duration
	MemoryLimit  string
	CPULimit     string
	PidsLimit    int
}

func (b DockerBackend) Kind() string {
	return "docker"
}

func (b DockerBackend) ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultExecutionTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dockerBinary := strings.TrimSpace(b.DockerBinary)
	if dockerBinary == "" {
		dockerBinary = "docker"
	}
	args := b.buildDockerRunArgs()
	cmd := exec.CommandContext(execCtx, dockerBinary, args...)

	var logs limitedOutputBuffer
	logs.limit = defaultOutputLimit
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("open docker stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return ExecuteResult{}, fmt.Errorf("start docker sandbox: %w", err)
	}
	if _, err := io.WriteString(stdin, req.Code); err != nil {
		_ = stdin.Close()
		return ExecuteResult{}, fmt.Errorf("write sandbox program: %w", err)
	}
	_ = stdin.Close()

	if err := cmd.Wait(); err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return ExecuteResult{Logs: logs.String()}, fmt.Errorf("docker sandbox execution timed out: %w", execCtx.Err())
		}
		return ExecuteResult{Logs: logs.String()}, fmt.Errorf("docker sandbox failed: %w", err)
	}

	return ExecuteResult{Logs: logs.String()}, nil
}

func (b DockerBackend) buildDockerRunArgs() []string {
	image := strings.TrimSpace(b.Image)
	if image == "" {
		image = "python:3.12-slim"
	}
	memoryLimit := strings.TrimSpace(b.MemoryLimit)
	if memoryLimit == "" {
		memoryLimit = "256m"
	}
	cpuLimit := strings.TrimSpace(b.CPULimit)
	if cpuLimit == "" {
		cpuLimit = "0.5"
	}
	pidsLimit := b.PidsLimit
	if pidsLimit <= 0 {
		pidsLimit = 64
	}

	return []string{
		"run",
		"--rm",
		"--interactive",
		"--pull=never",
		"--network=none",
		"--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m",
		"--tmpfs", "/workspace:rw,noexec,nosuid,nodev,size=64m",
		"--workdir", "/workspace",
		"--user", "65532:65532",
		"--env", "HOME=/tmp",
		"--env", "PYTHONDONTWRITEBYTECODE=1",
		"--env", "PYTHONHASHSEED=0",
		"--env", "PYTHONNOUSERSITE=1",
		"--cap-drop=ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(pidsLimit),
		"--memory", memoryLimit,
		"--cpus", cpuLimit,
		image,
		"python3",
		"-I",
		"-S",
		"-B",
		"-",
	}
}

type limitedOutputBuffer struct {
	mu        sync.Mutex
	builder   strings.Builder
	limit     int
	truncated bool
}

func (b *limitedOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(p)
	if b.limit <= 0 {
		return written, nil
	}
	remaining := b.limit - b.builder.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = io.WriteString(&b.builder, string(p))
	return written, nil
}

func (b *limitedOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.truncated {
		return b.builder.String()
	}
	return b.builder.String() + "\n...[truncated]\n"
}
