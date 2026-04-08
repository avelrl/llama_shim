package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
