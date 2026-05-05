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

func TestNormalizeBackendAcceptsPostgres(t *testing.T) {
	backend, err := storage.NormalizeBackend(" POSTGRES ")
	require.NoError(t, err)
	require.Equal(t, storage.BackendPostgres, backend)
}

func TestNormalizeBackendRejectsUnsupportedBackend(t *testing.T) {
	_, err := storage.NormalizeBackend("mysql")
	require.ErrorContains(t, err, `unsupported storage backend "mysql"`)

	_, err = storage.NormalizeBackend(" PostgreSQL ")
	require.ErrorContains(t, err, `unsupported storage backend "postgresql"`)
}
