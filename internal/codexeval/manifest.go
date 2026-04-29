package codexeval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var taskIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func LoadTasks(tasksDir, suite string) ([]Task, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}
	tasks := make([]Task, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskDir := filepath.Join(tasksDir, entry.Name())
		manifestPath := filepath.Join(taskDir, "task.yaml")
		raw, err := os.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", manifestPath, err)
		}
		var manifest Manifest
		if err := yaml.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
		}
		if !manifest.InSuite(suite) {
			continue
		}
		if err := manifest.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", manifestPath, err)
		}
		tasks = append(tasks, Task{Manifest: manifest, Dir: taskDir})
	}
	sort.Slice(tasks, func(i, j int) bool {
		if taskSortRank(tasks[i].Manifest.ID) != taskSortRank(tasks[j].Manifest.ID) {
			return taskSortRank(tasks[i].Manifest.ID) < taskSortRank(tasks[j].Manifest.ID)
		}
		return tasks[i].Manifest.ID < tasks[j].Manifest.ID
	})
	return tasks, nil
}

func taskSortRank(id string) int {
	if id == "boot" {
		return 0
	}
	return 1
}

func (m Manifest) InSuite(suite string) bool {
	if suite == "" {
		return true
	}
	for _, candidate := range m.Suites {
		if candidate == suite {
			return true
		}
	}
	return false
}

func (m *Manifest) Validate() error {
	if !taskIDPattern.MatchString(m.ID) {
		return fmt.Errorf("invalid task id %q", m.ID)
	}
	if strings.TrimSpace(m.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(m.Suites) == 0 {
		return fmt.Errorf("at least one suite is required")
	}
	if m.Attempts < 0 {
		return fmt.Errorf("attempts must be >= 0")
	}
	if _, err := m.TimeoutDuration(); err != nil {
		return err
	}
	if err := m.Expected.Validate(); err != nil {
		return err
	}
	for key, value := range m.Env {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("env key is empty")
		}
		if filepath.IsAbs(value) {
			return fmt.Errorf("env %s contains an absolute path; use ${workspace}", key)
		}
	}
	return nil
}

func (m *Manifest) TimeoutDuration() (time.Duration, error) {
	if m.timeoutOnce > 0 {
		return m.timeoutOnce, nil
	}
	raw := strings.TrimSpace(m.Timeout)
	if raw == "" {
		m.timeoutOnce = 180 * time.Second
		return m.timeoutOnce, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", raw, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("timeout must be positive")
	}
	m.timeoutOnce = parsed
	return parsed, nil
}

func (expected Expected) Validate() error {
	hasChecker := expected.FinalTextEquals != "" ||
		len(expected.FinalTextContains) > 0 ||
		len(expected.Files) > 0 ||
		len(expected.Commands) > 0 ||
		len(expected.CodexEvents) > 0 ||
		len(expected.ForbiddenOutput) > 0
	if !hasChecker {
		return fmt.Errorf("at least one deterministic checker is required")
	}
	for _, file := range expected.Files {
		if err := validateRelativePath(file.Path); err != nil {
			return fmt.Errorf("file expectation %q: %w", file.Path, err)
		}
		if file.Absent && file.Exists != nil && *file.Exists {
			return fmt.Errorf("file expectation %q cannot set absent and exists=true", file.Path)
		}
		if file.Matches != "" {
			if _, err := regexp.Compile(file.Matches); err != nil {
				return fmt.Errorf("file expectation %q invalid regex: %w", file.Path, err)
			}
		}
	}
	for _, command := range expected.Commands {
		if strings.TrimSpace(command.Command) == "" {
			return fmt.Errorf("command checker is empty")
		}
		if command.Timeout != "" {
			parsed, err := time.ParseDuration(command.Timeout)
			if err != nil {
				return fmt.Errorf("command checker timeout %q: %w", command.Timeout, err)
			}
			if parsed <= 0 {
				return fmt.Errorf("command checker timeout must be positive")
			}
		}
	}
	return nil
}

func validateRelativePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes workspace")
	}
	return nil
}

func attemptsFor(manifest Manifest, override int) int {
	if override > 0 {
		return override
	}
	if manifest.Attempts > 0 {
		return manifest.Attempts
	}
	return 1
}
