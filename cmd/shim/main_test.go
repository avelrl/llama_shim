package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
	"llama_shim/internal/sandbox"
)

func TestBuildLogWriterWithoutFilePathUsesStdout(t *testing.T) {
	t.Parallel()

	writer, file, err := buildLogWriter("")
	require.NoError(t, err)
	require.Same(t, os.Stdout, writer)
	require.Nil(t, file)
}

func TestBuildLogWriterCreatesLogFile(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "logs", "shim.log")

	writer, file, err := buildLogWriter(logPath)
	require.NoError(t, err)
	require.NotNil(t, file)
	t.Cleanup(func() {
		require.NoError(t, file.Close())
	})

	_, err = io.WriteString(writer, "shim-test-line\n")
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "shim-test-line")
}

func TestBuildLocalCodeInterpreterRuntimeConfigDocker(t *testing.T) {
	t.Parallel()

	runtime, err := buildLocalCodeInterpreterRuntimeConfig(config.Config{
		ResponsesCodeInterpreterBackend:      config.ResponsesCodeInterpreterBackendDocker,
		ResponsesCodeInterpreterDockerBinary: "/usr/local/bin/docker",
		ResponsesCodeInterpreterDockerImage:  "python:3.12-slim",
		ResponsesCodeInterpreterDockerMemory: "512m",
		ResponsesCodeInterpreterDockerCPU:    "1",
		ResponsesCodeInterpreterDockerPids:   96,
		ResponsesCodeInterpreterTimeout:      30 * time.Second,
	})
	require.NoError(t, err)

	backend, ok := runtime.Backend.(sandbox.DockerBackend)
	require.True(t, ok)
	require.Equal(t, "/usr/local/bin/docker", backend.DockerBinary)
	require.Equal(t, "python:3.12-slim", backend.Image)
	require.Equal(t, "512m", backend.MemoryLimit)
	require.Equal(t, "1", backend.CPULimit)
	require.Equal(t, 96, backend.PidsLimit)
	require.Equal(t, 30*time.Second, backend.Timeout)
}

func TestBuildLocalCodeInterpreterRuntimeConfigUnsafeHost(t *testing.T) {
	t.Parallel()

	runtime, err := buildLocalCodeInterpreterRuntimeConfig(config.Config{
		ResponsesCodeInterpreterBackend:      config.ResponsesCodeInterpreterBackendUnsafeHost,
		ResponsesCodeInterpreterPythonBinary: "/opt/homebrew/bin/python3",
		ResponsesCodeInterpreterTimeout:      12 * time.Second,
	})
	require.NoError(t, err)

	backend, ok := runtime.Backend.(sandbox.UnsafeHostBackend)
	require.True(t, ok)
	require.Equal(t, "/opt/homebrew/bin/python3", backend.PythonBinary)
	require.Equal(t, 12*time.Second, backend.Timeout)
}
