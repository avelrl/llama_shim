package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
var ErrSessionNotFound = errors.New("sandbox session not found")

type Backend interface {
	Kind() string
	CreateSession(ctx context.Context, sessionID string) error
	ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
	DestroySession(ctx context.Context, sessionID string) error
}

type ExecuteRequest struct {
	SessionID string
	Code      string
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

func (b UnsafeHostBackend) CreateSession(_ context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("create unsafe_host session: session id is required")
	}
	return os.MkdirAll(b.sessionDir(sessionID), 0o755)
}

func (b UnsafeHostBackend) ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return ExecuteResult{}, fmt.Errorf("execute python: session id is required")
	}
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

	sessionDir := b.sessionDir(req.SessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return ExecuteResult{}, fmt.Errorf("create code interpreter session dir: %w", err)
	}

	cmd := exec.CommandContext(execCtx, pythonBinary, "-I", "-S", "-B", "-")
	cmd.Dir = sessionDir
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

func (b UnsafeHostBackend) DestroySession(_ context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	if err := os.RemoveAll(b.sessionDir(sessionID)); err != nil {
		return fmt.Errorf("destroy unsafe_host session: %w", err)
	}
	return nil
}

func (b UnsafeHostBackend) sessionDir(sessionID string) string {
	return filepath.Join(os.TempDir(), "llama-shim-code-interpreter-sessions", sanitizeSessionID(sessionID))
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

func (b DockerBackend) CreateSession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("create docker session: session id is required")
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultExecutionTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dockerBinary := b.dockerBinary()
	containerName := b.containerName(sessionID)
	exists, running, err := b.inspectContainer(execCtx, dockerBinary, containerName)
	if err != nil {
		return err
	}
	if exists {
		if running {
			return nil
		}
		return b.startContainer(execCtx, dockerBinary, containerName)
	}

	createCmd := exec.CommandContext(execCtx, dockerBinary, b.buildDockerCreateArgs(containerName)...)
	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create docker sandbox session: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return b.startContainer(execCtx, dockerBinary, containerName)
}

func (b DockerBackend) ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return ExecuteResult{}, fmt.Errorf("execute docker sandbox: session id is required")
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultExecutionTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dockerBinary := b.dockerBinary()
	containerName := b.containerName(req.SessionID)
	exists, running, err := b.inspectContainer(execCtx, dockerBinary, containerName)
	if err != nil {
		return ExecuteResult{}, err
	}
	if !exists {
		return ExecuteResult{}, ErrSessionNotFound
	}
	if !running {
		if err := b.startContainer(execCtx, dockerBinary, containerName); err != nil {
			return ExecuteResult{}, err
		}
	}

	args := []string{
		"exec",
		"--interactive",
		"--workdir", "/workspace",
		containerName,
		"python3",
		"-I",
		"-S",
		"-B",
		"-",
	}
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

func (b DockerBackend) DestroySession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultExecutionTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dockerBinary := b.dockerBinary()
	containerName := b.containerName(sessionID)
	exists, _, err := b.inspectContainer(execCtx, dockerBinary, containerName)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSessionNotFound
	}
	cmd := exec.CommandContext(execCtx, dockerBinary, "rm", "-f", containerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("destroy docker sandbox session: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (b DockerBackend) buildDockerRunArgs() []string {
	args := b.buildDockerCreateArgs("")
	return append([]string{"run", "--rm", "--interactive", "--pull=never"}, args[1:]...)
}

func (b DockerBackend) buildDockerCreateArgs(containerName string) []string {
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

	args := []string{
		"create",
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
	}
	if strings.TrimSpace(containerName) != "" {
		args = append(args, "--name", containerName)
	}
	args = append(args,
		image,
		"sh",
		"-lc",
		"trap 'exit 0' TERM INT; while :; do sleep 3600; done",
	)
	return args
}

func (b DockerBackend) inspectContainer(ctx context.Context, dockerBinary string, containerName string) (exists bool, running bool, err error) {
	cmd := exec.CommandContext(ctx, dockerBinary, "inspect", "--format", "{{.State.Running}}", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		lowered := strings.ToLower(string(output))
		if strings.Contains(lowered, "no such object") || strings.Contains(lowered, "no such container") {
			return false, false, nil
		}
		return false, false, fmt.Errorf("inspect docker sandbox session: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return true, strings.EqualFold(strings.TrimSpace(string(output)), "true"), nil
}

func (b DockerBackend) startContainer(ctx context.Context, dockerBinary string, containerName string) error {
	cmd := exec.CommandContext(ctx, dockerBinary, "start", containerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start docker sandbox session: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (b DockerBackend) dockerBinary() string {
	dockerBinary := strings.TrimSpace(b.DockerBinary)
	if dockerBinary == "" {
		return "docker"
	}
	return dockerBinary
}

func (b DockerBackend) containerName(sessionID string) string {
	return "llama-shim-ci-" + sanitizeSessionID(sessionID)
}

func sanitizeSessionID(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "unknown"
	}
	if len(out) > 96 {
		return out[:96]
	}
	return out
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
	_, _ = b.builder.Write(bytes.Clone(p))
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
