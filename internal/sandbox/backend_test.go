package sandbox

import (
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

func TestBuildPythonProgramGuardsOpenToWorkspace(t *testing.T) {
	t.Parallel()

	program, err := buildPythonProgram(`print(open("codes.txt", encoding="utf-8").read())`)
	require.NoError(t, err)
	require.Contains(t, program, "_shim_safe_open")
	require.Contains(t, program, "_shim_safe_import")
	require.Contains(t, program, "_shim_builtins.open = _shim_safe_open")
	require.Contains(t, program, "_shim_builtins.__import__ = _shim_safe_import")
	require.Contains(t, program, "_shim_io.open = _shim_safe_open")
	require.Contains(t, program, "file access outside workspace is not allowed")
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
