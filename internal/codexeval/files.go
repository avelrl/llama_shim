package codexeval

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func copyDir(src, dst string) error {
	return copyDirFiltered(src, dst, nil)
}

func copyWorkspaceSnapshot(src, dst string) error {
	return copyDirFiltered(src, dst, skipWorkspaceSnapshotArtifact)
}

func pruneWorkspaceArtifacts(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || !isWorkspaceGeneratedArtifactPath(rel) {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

func copyDirFiltered(src, dst string, skip func(rel string, entry fs.DirEntry) bool) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if entry.IsDir() {
			return os.MkdirAll(target, mode.Perm())
		}
		if mode.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		return copyFile(path, target, mode.Perm())
	})
}

func skipWorkspaceSnapshotArtifact(rel string, _ fs.DirEntry) bool {
	return isWorkspaceGeneratedArtifactPath(rel)
}

func isWorkspaceGeneratedArtifactPath(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch part {
		case ".cache", ".gocache", ".git", ".pytest_cache", "__pycache__", "node_modules":
			return true
		}
	}
	return false
}

func copyFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyDirIfExists(src, dst string) error {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dst, 0o755)
	}
	return copyDir(src, dst)
}

func writeGitDiff(before, after, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "diff", "--no-index", "--", before, after)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			raw = append(raw, []byte(fmt.Sprintf("\nfailed to produce git diff: %v\n", err))...)
		}
	}
	return os.WriteFile(out, raw, 0o644)
}
