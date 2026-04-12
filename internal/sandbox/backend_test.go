package sandbox

import (
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
	require.Contains(t, args, "256m")
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
