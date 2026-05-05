package retrieval

import (
	"context"
	"strings"

	"llama_shim/internal/domain"
)

type IndexCapabilities struct {
	Backend        string
	SemanticSearch bool
	HybridSearch   bool
	LocalRerank    bool
	LazyRepair     bool
}

type IndexFileParams struct {
	VectorStoreID    string
	FileID           string
	CreatedAt        int64
	ReplacedChunkIDs []int64
}

type DeleteFileParams struct {
	VectorStoreID   string
	FileID          string
	CreatedAt       int64
	RemovedChunkIDs []int64
}

type Index[Mutation any, Corpus any] interface {
	Name() string
	Capabilities() IndexCapabilities
	IndexVectorStoreFile(ctx context.Context, mutation Mutation, params IndexFileParams) error
	DeleteVectorStoreFile(ctx context.Context, mutation Mutation, params DeleteFileParams) error
	RefreshVectorStore(ctx context.Context, mutation Mutation, vectorStoreID string, createdAt int64) error
	DeleteVectorStore(ctx context.Context, mutation Mutation, vectorStoreID string) error
	SearchVectorStore(ctx context.Context, corpus Corpus, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error)
}

func IndexCapabilitiesForConfig(cfg Config, embedderConfigured bool) IndexCapabilities {
	backend := strings.ToLower(strings.TrimSpace(cfg.IndexBackend))
	if backend == "" {
		backend = IndexBackendLexical
	}
	semantic := (backend == IndexBackendSQLiteVec || backend == IndexBackendPGVector) && embedderConfigured
	localRerank := backend == IndexBackendSQLiteVec && embedderConfigured
	lazyRepair := backend == IndexBackendSQLiteVec && embedderConfigured
	return IndexCapabilities{
		Backend:        backend,
		SemanticSearch: semantic,
		HybridSearch:   semantic,
		LocalRerank:    localRerank,
		LazyRepair:     lazyRepair,
	}
}
