package storage

import (
	"fmt"
	"strings"
)

func NormalizeBackend(backend string) (string, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		return BackendSQLite, nil
	}
	switch backend {
	case BackendSQLite:
		return backend, nil
	default:
		return "", fmt.Errorf("unsupported storage backend %q", backend)
	}
}
