package retrieval

import (
	"fmt"
	"strings"
)

const (
	IndexBackendLexical   = "lexical"
	IndexBackendSQLiteVec = "sqlite_vec"

	EmbedderBackendDisabled         = "disabled"
	EmbedderBackendOpenAICompatible = "openai_compatible"
	EmbedderBackendEmbedAnything    = "embedanything"
)

type EmbedderConfig struct {
	Backend string
	BaseURL string
	Model   string
}

type Config struct {
	IndexBackend string
	Embedder     EmbedderConfig
}

func NormalizeConfig(cfg Config) (Config, error) {
	cfg.IndexBackend = strings.ToLower(strings.TrimSpace(cfg.IndexBackend))
	if cfg.IndexBackend == "" {
		cfg.IndexBackend = IndexBackendLexical
	}
	switch cfg.IndexBackend {
	case IndexBackendLexical, IndexBackendSQLiteVec:
	default:
		return Config{}, fmt.Errorf("unsupported retrieval index backend %q", cfg.IndexBackend)
	}

	cfg.Embedder.Backend = strings.ToLower(strings.TrimSpace(cfg.Embedder.Backend))
	if cfg.Embedder.Backend == "" {
		cfg.Embedder.Backend = EmbedderBackendDisabled
	}
	switch cfg.Embedder.Backend {
	case EmbedderBackendDisabled, EmbedderBackendOpenAICompatible, EmbedderBackendEmbedAnything:
	default:
		return Config{}, fmt.Errorf("unsupported retrieval embedder backend %q", cfg.Embedder.Backend)
	}

	cfg.Embedder.BaseURL = strings.TrimSpace(cfg.Embedder.BaseURL)
	cfg.Embedder.Model = strings.TrimSpace(cfg.Embedder.Model)
	return cfg, nil
}
