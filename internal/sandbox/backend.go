package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

type ToolExecutionError struct {
	Err error
}

func (e *ToolExecutionError) Error() string {
	if e == nil || e.Err == nil {
		return "sandbox tool execution failed"
	}
	return e.Err.Error()
}

func (e *ToolExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsToolExecutionError(err error) bool {
	var toolErr *ToolExecutionError
	return errors.As(err, &toolErr)
}

type Backend interface {
	Kind() string
	CreateSession(ctx context.Context, req CreateSessionRequest) error
	UploadFile(ctx context.Context, sessionID string, file SessionFile) error
	DeleteFile(ctx context.Context, sessionID string, name string) error
	ListFiles(ctx context.Context, sessionID string) ([]SessionFile, error)
	ExecutePython(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
	DestroySession(ctx context.Context, sessionID string) error
}

type CreateSessionRequest struct {
	SessionID   string
	MemoryLimit string
}

type SessionFile struct {
	Name    string
	Content []byte
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

func (b UnsafeHostBackend) CreateSession(_ context.Context, req CreateSessionRequest) error {
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("create unsafe_host session: session id is required")
	}
	return os.MkdirAll(b.sessionDir(req.SessionID), 0o755)
}

func (b UnsafeHostBackend) UploadFile(_ context.Context, sessionID string, file SessionFile) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	name, err := validateSessionFile(file)
	if err != nil {
		return fmt.Errorf("upload unsafe_host session file: %w", err)
	}

	sessionDir := b.sessionDir(sessionID)
	if _, err := os.Stat(sessionDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("stat unsafe_host session dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, name), file.Content, 0o644); err != nil {
		return fmt.Errorf("write unsafe_host session file: %w", err)
	}
	return nil
}

func (b UnsafeHostBackend) ListFiles(_ context.Context, sessionID string) ([]SessionFile, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrSessionNotFound
	}

	sessionDir := b.sessionDir(sessionID)
	if _, err := os.Stat(sessionDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("stat unsafe_host session dir: %w", err)
	}
	files, err := listSessionFilesFromDir(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("list unsafe_host session files: %w", err)
	}
	return files, nil
}

func (b UnsafeHostBackend) DeleteFile(_ context.Context, sessionID string, name string) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	sanitizedName, err := validateSessionFile(SessionFile{Name: name})
	if err != nil {
		return fmt.Errorf("delete unsafe_host session file: %w", err)
	}
	path := filepath.Join(b.sessionDir(sessionID), sanitizedName)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("delete unsafe_host session file: %w", err)
	}
	return nil
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
	program, err := buildPythonProgram(req.Code)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("build sandbox program: %w", err)
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
	if _, err := io.WriteString(stdin, program); err != nil {
		_ = stdin.Close()
		return ExecuteResult{}, fmt.Errorf("write python program: %w", err)
	}
	_ = stdin.Close()

	if err := cmd.Wait(); err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return ExecuteResult{Logs: logs.String()}, fmt.Errorf("sandbox execution timed out: %w", execCtx.Err())
		}
		return ExecuteResult{Logs: logs.String()}, &ToolExecutionError{Err: fmt.Errorf("execute python: %w", err)}
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

func (b DockerBackend) CreateSession(ctx context.Context, req CreateSessionRequest) error {
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("create docker session: session id is required")
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
		return err
	}
	if exists {
		if running {
			return nil
		}
		return b.startContainer(execCtx, dockerBinary, containerName)
	}

	createCmd := exec.CommandContext(execCtx, dockerBinary, b.buildDockerCreateArgs(containerName, req.MemoryLimit)...)
	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create docker sandbox session: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return b.startContainer(execCtx, dockerBinary, containerName)
}

func (b DockerBackend) UploadFile(ctx context.Context, sessionID string, file SessionFile) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	name, err := validateSessionFile(file)
	if err != nil {
		return fmt.Errorf("upload docker sandbox file: %w", err)
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
	if !exists {
		return ErrSessionNotFound
	}
	if !running {
		if err := b.startContainer(execCtx, dockerBinary, containerName); err != nil {
			return err
		}
	}

	tmpFile, err := os.CreateTemp("", "llama-shim-sandbox-upload-*")
	if err != nil {
		return fmt.Errorf("create temp sandbox file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmpFile.Write(file.Content); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp sandbox file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp sandbox file: %w", err)
	}

	target := containerName + ":/workspace/" + name
	cmd := exec.CommandContext(execCtx, dockerBinary, "cp", tmpName, target)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload docker sandbox file: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (b DockerBackend) ListFiles(ctx context.Context, sessionID string) ([]SessionFile, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrSessionNotFound
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
		return nil, err
	}
	if !exists {
		return nil, ErrSessionNotFound
	}
	if !running {
		if err := b.startContainer(execCtx, dockerBinary, containerName); err != nil {
			return nil, err
		}
	}

	tmpDir, err := os.MkdirTemp("", "llama-shim-sandbox-list-*")
	if err != nil {
		return nil, fmt.Errorf("create temp sandbox dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cmd := exec.CommandContext(execCtx, dockerBinary, "cp", containerName+":/workspace/.", tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("list docker sandbox files: %w: %s", err, strings.TrimSpace(string(output)))
	}

	files, err := listSessionFilesFromDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("list docker sandbox files: %w", err)
	}
	return files, nil
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
	program, err := buildPythonProgram(req.Code)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("build docker sandbox program: %w", err)
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
	if _, err := io.WriteString(stdin, program); err != nil {
		_ = stdin.Close()
		return ExecuteResult{}, fmt.Errorf("write sandbox program: %w", err)
	}
	_ = stdin.Close()

	if err := cmd.Wait(); err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return ExecuteResult{Logs: logs.String()}, fmt.Errorf("docker sandbox execution timed out: %w", execCtx.Err())
		}
		return ExecuteResult{Logs: logs.String()}, &ToolExecutionError{Err: fmt.Errorf("docker sandbox failed: %w", err)}
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

func (b DockerBackend) DeleteFile(ctx context.Context, sessionID string, name string) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	sanitizedName, err := validateSessionFile(SessionFile{Name: name})
	if err != nil {
		return fmt.Errorf("delete docker sandbox file: %w", err)
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
	if !exists {
		return ErrSessionNotFound
	}
	if !running {
		if err := b.startContainer(execCtx, dockerBinary, containerName); err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(execCtx, dockerBinary, "exec", "--workdir", "/workspace", containerName, "rm", "-f", "--", sanitizedName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete docker sandbox file: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (b DockerBackend) buildDockerRunArgs() []string {
	args := b.buildDockerCreateArgs("", "")
	return append([]string{"run", "--rm", "--interactive", "--pull=never"}, args[1:]...)
}

func (b DockerBackend) buildDockerCreateArgs(containerName string, memoryLimitOverride string) []string {
	image := strings.TrimSpace(b.Image)
	if image == "" {
		image = "python:3.12-slim"
	}
	memoryLimit := strings.TrimSpace(memoryLimitOverride)
	if memoryLimit == "" {
		memoryLimit = strings.TrimSpace(b.MemoryLimit)
	}
	if memoryLimit == "" {
		memoryLimit = "1g"
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

func buildPythonProgram(code string) (string, error) {
	quotedCode, err := json.Marshal(code)
	if err != nil {
		return "", fmt.Errorf("marshal python code: %w", err)
	}

	return strings.Join([]string{
		"_shim_user_code = " + string(quotedCode),
		`_shim_globals = {"__name__": "__main__"}`,
		`exec(compile(_shim_user_code, "<shim-local-code-interpreter>", "exec"), _shim_globals, _shim_globals)`,
		"",
	}, "\n"), nil
}

func validateSessionFile(file SessionFile) (string, error) {
	name := strings.TrimSpace(file.Name)
	switch {
	case name == "":
		return "", fmt.Errorf("file name is required")
	case name == "." || name == "..":
		return "", fmt.Errorf("file name must be a workspace-relative filename")
	case strings.ContainsAny(name, `/\`):
		return "", fmt.Errorf("file name must not contain path separators")
	default:
		return name, nil
	}
}

func listSessionFilesFromDir(root string) ([]SessionFile, error) {
	files := make([]SessionFile, 0)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, SessionFile{
			Name:    filepath.ToSlash(rel),
			Content: content,
		})
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})
	return files, nil
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
