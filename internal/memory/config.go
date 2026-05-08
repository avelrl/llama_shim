package memory

import (
	"fmt"
	"strings"
)

const (
	BackendDisabled = "disabled"
	BackendLocal    = "local"

	DefaultMetadataNamespace = "llama_shim.memory"
)

type Config struct {
	Backend           string
	Inject            bool
	MaxNotes          int
	MaxNoteBytes      int
	MaxContextBytes   int
	MetadataNamespace string
}

const (
	defaultMaxNotes        = 8
	defaultMaxNoteBytes    = 2048
	defaultMaxContextBytes = 8192
)

func NormalizeConfig(cfg Config) (Config, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = BackendDisabled
	}
	switch backend {
	case BackendDisabled, BackendLocal:
	default:
		return Config{}, fmt.Errorf("unsupported memory backend %q", cfg.Backend)
	}
	cfg.Backend = backend

	if cfg.MaxNotes <= 0 {
		cfg.MaxNotes = defaultMaxNotes
	}
	if cfg.MaxNoteBytes <= 0 {
		cfg.MaxNoteBytes = defaultMaxNoteBytes
	}
	if cfg.MaxContextBytes <= 0 {
		cfg.MaxContextBytes = defaultMaxContextBytes
	}
	namespace := strings.Trim(strings.TrimSpace(cfg.MetadataNamespace), ".")
	if namespace == "" {
		namespace = DefaultMetadataNamespace
	}
	cfg.MetadataNamespace = namespace
	if cfg.Backend == BackendDisabled {
		cfg.Inject = false
	}
	return cfg, nil
}

func (c Config) Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.Backend), BackendLocal)
}
