package codexeval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyWorkspaceSnapshotSkipsGeneratedArtifacts(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "snapshot")
	mustWriteNested(t, filepath.Join(src, "app.txt"), "keep\n")
	mustWriteNested(t, filepath.Join(src, ".gocache", "cache.bin"), "drop\n")
	mustWriteNested(t, filepath.Join(src, "node_modules", "pkg", "index.js"), "drop\n")
	mustWriteNested(t, filepath.Join(src, "pkg", "__pycache__", "mod.pyc"), "drop\n")

	if err := copyWorkspaceSnapshot(src, dst); err != nil {
		t.Fatalf("copyWorkspaceSnapshot failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "app.txt")); err != nil {
		t.Fatalf("expected kept file: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dst, ".gocache"),
		filepath.Join(dst, "node_modules"),
		filepath.Join(dst, "pkg", "__pycache__"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be skipped, err=%v", path, err)
		}
	}
}

func TestPruneAttemptArtifactsRemovesPluginCatalog(t *testing.T) {
	attemptDir := t.TempDir()
	mustWriteNested(t, filepath.Join(attemptDir, "codex-home", "config.toml"), "model = test\n")
	mustWriteNested(t, filepath.Join(attemptDir, "codex-home", ".tmp", "plugins", "catalog.txt"), "drop\n")
	mustWriteNested(t, filepath.Join(attemptDir, "codex-home", ".tmp", "plugins-clone-abc123", ".git", "objects", "pack", "pack.bin"), "drop\n")

	if err := pruneAttemptArtifacts(attemptDir); err != nil {
		t.Fatalf("pruneAttemptArtifacts failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "codex-home", "config.toml")); err != nil {
		t.Fatalf("expected config to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "codex-home", ".tmp")); !os.IsNotExist(err) {
		t.Fatalf("expected codex-home .tmp to be pruned, err=%v", err)
	}
}

func TestPruneWorkspaceArtifactsRemovesGeneratedCaches(t *testing.T) {
	workspace := t.TempDir()
	mustWriteNested(t, filepath.Join(workspace, "mathutil.go"), "package codexsmoke\n")
	mustWriteNested(t, filepath.Join(workspace, ".cache", "go-build", "cache.bin"), "drop\n")
	mustWriteNested(t, filepath.Join(workspace, ".gocache", "cache.bin"), "drop\n")

	if err := pruneWorkspaceArtifacts(workspace); err != nil {
		t.Fatalf("pruneWorkspaceArtifacts failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "mathutil.go")); err != nil {
		t.Fatalf("expected source file to remain: %v", err)
	}
	for _, path := range []string{
		filepath.Join(workspace, ".cache"),
		filepath.Join(workspace, ".gocache"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be pruned, err=%v", path, err)
		}
	}
}

func mustWriteNested(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, content)
}
