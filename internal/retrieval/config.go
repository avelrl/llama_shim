package retrieval

import (
	"fmt"
	"strings"
)

const (
	IndexBackendLexical    = "lexical"
	IndexBackendSQLiteFTS5 = "sqlite_fts5"
	IndexBackendSQLiteVec  = "sqlite_vec"
	IndexBackendPGVector   = "pgvector"

	EmbedderBackendDisabled         = "disabled"
	EmbedderBackendOpenAICompatible = "openai_compatible"
	EmbedderBackendEmbedAnything    = "embedanything"

	PGVectorANNMethodHNSW    = "hnsw"
	PGVectorANNMethodIVFFlat = "ivfflat"
	PGVectorANNMetricCosine  = "cosine"
)

type EmbedderConfig struct {
	Backend string
	BaseURL string
	Model   string
}

type PGVectorConfig struct {
	ANN PGVectorANNConfig
}

type PGVectorANNConfig struct {
	Enabled            bool
	Method             string
	Metric             string
	Dimensions         int
	HNSWM              int
	HNSWEFConstruction int
	IVFFlatLists       int
}

type Config struct {
	IndexBackend string
	Embedder     EmbedderConfig
	PGVector     PGVectorConfig
}

func NormalizeConfig(cfg Config) (Config, error) {
	cfg.IndexBackend = strings.ToLower(strings.TrimSpace(cfg.IndexBackend))
	if cfg.IndexBackend == "" {
		cfg.IndexBackend = IndexBackendLexical
	}
	switch cfg.IndexBackend {
	case IndexBackendLexical, IndexBackendSQLiteFTS5, IndexBackendSQLiteVec, IndexBackendPGVector:
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
	cfg.PGVector.ANN.Method = strings.ToLower(strings.TrimSpace(cfg.PGVector.ANN.Method))
	if cfg.PGVector.ANN.Method == "" {
		cfg.PGVector.ANN.Method = PGVectorANNMethodHNSW
	}
	switch cfg.PGVector.ANN.Method {
	case PGVectorANNMethodHNSW, PGVectorANNMethodIVFFlat:
	default:
		return Config{}, fmt.Errorf("unsupported pgvector ANN method %q", cfg.PGVector.ANN.Method)
	}
	cfg.PGVector.ANN.Metric = strings.ToLower(strings.TrimSpace(cfg.PGVector.ANN.Metric))
	if cfg.PGVector.ANN.Metric == "" {
		cfg.PGVector.ANN.Metric = PGVectorANNMetricCosine
	}
	switch cfg.PGVector.ANN.Metric {
	case PGVectorANNMetricCosine:
	default:
		return Config{}, fmt.Errorf("unsupported pgvector ANN metric %q", cfg.PGVector.ANN.Metric)
	}
	if cfg.PGVector.ANN.HNSWM == 0 {
		cfg.PGVector.ANN.HNSWM = 16
	}
	if cfg.PGVector.ANN.HNSWEFConstruction == 0 {
		cfg.PGVector.ANN.HNSWEFConstruction = 64
	}
	if cfg.PGVector.ANN.IVFFlatLists == 0 {
		cfg.PGVector.ANN.IVFFlatLists = 100
	}
	if cfg.PGVector.ANN.HNSWM < 0 || cfg.PGVector.ANN.HNSWEFConstruction < 0 || cfg.PGVector.ANN.IVFFlatLists < 0 {
		return Config{}, fmt.Errorf("pgvector ANN index parameters must be non-negative")
	}
	if cfg.PGVector.ANN.Enabled {
		if cfg.IndexBackend != IndexBackendPGVector {
			return Config{}, fmt.Errorf("pgvector ANN requires retrieval index backend %q", IndexBackendPGVector)
		}
		if cfg.PGVector.ANN.Dimensions <= 0 {
			return Config{}, fmt.Errorf("pgvector ANN requires retrieval.index.pgvector.ann.dimensions > 0")
		}
	}
	return cfg, nil
}
