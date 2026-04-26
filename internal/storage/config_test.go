package storage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/storage"
)

func TestNormalizeBackendDefaultsToSQLite(t *testing.T) {
	backend, err := storage.NormalizeBackend("")
	require.NoError(t, err)
	require.Equal(t, storage.BackendSQLite, backend)
}

func TestNormalizeBackendTrimsAndLowercases(t *testing.T) {
	backend, err := storage.NormalizeBackend(" SQLite ")
	require.NoError(t, err)
	require.Equal(t, storage.BackendSQLite, backend)
}

func TestNormalizeBackendRejectsUnsupportedBackend(t *testing.T) {
	_, err := storage.NormalizeBackend("postgres")
	require.ErrorContains(t, err, `unsupported storage backend "postgres"`)
}
