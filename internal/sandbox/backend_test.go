package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDockerBackendBuildDockerRunArgsAppliesHardeningDefaults(t *testing.T) {
	t.Parallel()

	args := DockerBackend{}.buildDockerRunArgs()

	require.Contains(t, args, "--rm")
	require.Contains(t, args, "--interactive")
	require.Contains(t, args, "--pull=never")
	require.Contains(t, args, "--network=none")
	require.Contains(t, args, "--read-only")
	require.Contains(t, args, "--cap-drop=ALL")
	require.Contains(t, args, "no-new-privileges")
	require.Contains(t, args, "python:3.12-slim")
	require.Contains(t, args, "sh")
	require.Contains(t, args, "trap 'exit 0' TERM INT; while :; do sleep 3600; done")
	require.Contains(t, args, "--memory")
	require.Contains(t, args, "1g")
	require.Contains(t, args, "--cpus")
	require.Contains(t, args, "0.5")
	require.Contains(t, args, "--pids-limit")
	require.Contains(t, args, "64")
	require.Contains(t, args, "--tmpfs")
	require.Contains(t, args, "/workspace:rw,noexec,nosuid,nodev,size=64m")
	require.Contains(t, args, "/tmp:rw,noexec,nosuid,nodev,size=64m")
}

func TestDockerBackendBuildDockerRunArgsUsesConfiguredValues(t *testing.T) {
	t.Parallel()

	args := DockerBackend{
		Image:       "ghcr.io/acme/code-interpreter:latest",
		MemoryLimit: "768m",
		CPULimit:    "2",
		PidsLimit:   128,
	}.buildDockerRunArgs()

	require.Contains(t, args, "ghcr.io/acme/code-interpreter:latest")
	require.Contains(t, args, "768m")
	require.Contains(t, args, "2")
	require.Contains(t, args, "128")
}

func TestBuildPythonProgramWrapsUserCodeWithoutMonkeypatchingBuiltins(t *testing.T) {
	t.Parallel()

	program, err := buildPythonProgram(`print(open("codes.txt", encoding="utf-8").read())`)
	require.NoError(t, err)
	require.Contains(t, program, "_shim_user_code = ")
	require.Contains(t, program, `_shim_globals = {"__name__": "__main__"}`)
	require.Contains(t, program, `exec(compile(_shim_user_code, "<shim-local-code-interpreter>", "exec"), _shim_globals, _shim_globals)`)
	require.NotContains(t, program, "_shim_safe_open")
	require.NotContains(t, program, "_shim_builtins.open")
	require.NotContains(t, program, "_shim_builtins.__import__")
	require.Contains(t, program, `print(open(\"codes.txt\", encoding=\"utf-8\").read())`)
}

func TestValidateSessionFileRejectsTraversal(t *testing.T) {
	t.Parallel()

	_, err := validateSessionFile(SessionFile{Name: "../secret.txt"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "path separators")
}

func TestListSessionFilesFromDirReturnsSortedRelativeFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("B"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "a.txt"), []byte("A"), 0o644))

	files, err := listSessionFilesFromDir(root)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "b.txt", files[0].Name)
	require.Equal(t, "B", string(files[0].Content))
	require.Equal(t, "nested/a.txt", files[1].Name)
	require.Equal(t, "A", string(files[1].Content))
}

func TestListSessionFilesFromDirSkipsOversizedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "small.txt"), []byte("small"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.bin"), make([]byte, maxListFileBytes+1), 0o644))

	files, err := listSessionFilesFromDir(root)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "small.txt", files[0].Name)
	require.Equal(t, "small", string(files[0].Content))
}

func TestListSessionFileInfosFromDirHashesSmallFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "a.txt"), []byte("A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("B"), 0o644))

	files, err := listSessionFileInfosFromDir(root, 10, 16)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "b.txt", files[0].Name)
	require.EqualValues(t, 1, files[0].Size)
	require.NotZero(t, files[0].ModTimeUnixNano)
	sumB := sha256.Sum256([]byte("B"))
	require.Equal(t, hex.EncodeToString(sumB[:]), files[0].SHA256)
	require.Equal(t, "nested/a.txt", files[1].Name)
	sumA := sha256.Sum256([]byte("A"))
	require.Equal(t, hex.EncodeToString(sumA[:]), files[1].SHA256)
}

func TestListSessionFileInfosFromDirRejectsTooManyFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("B"), 0o644))

	_, err := listSessionFileInfosFromDir(root, 1, 16)
	require.ErrorIs(t, err, ErrSessionSnapshotTooLarge)
}

func TestReadSessionFileFromDirRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "report.txt"), []byte("artifact-body"), 0o644))

	_, err := readSessionFileFromDir(root, "report.txt", 4)
	require.ErrorIs(t, err, ErrSessionFileTooLarge)
}

func TestIsToolExecutionError(t *testing.T) {
	t.Parallel()

	err := &ToolExecutionError{Err: errors.New("exit status 1")}
	require.True(t, IsToolExecutionError(err))
	require.False(t, IsToolExecutionError(errors.New("plain error")))
}
