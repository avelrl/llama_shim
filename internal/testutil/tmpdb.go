package testutil

import (
	"path/filepath"
	"testing"
)

func TempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "shim.db")
}
