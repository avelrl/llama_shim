package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"llama_shim/internal/domain"
	"llama_shim/internal/retrieval"
)

type OpenOptions struct {
	Retrieval retrieval.Config
}

type indexVectorStoreFileParams struct {
	VectorStoreID string
	FileID        string
}

type retrievalBackend interface {
	Name() string
	IndexVectorStoreFile(ctx context.Context, tx *sql.Tx, params indexVectorStoreFileParams) error
	SearchVectorStore(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error)
}

type lexicalRetrievalBackend struct{}

func (lexicalRetrievalBackend) Name() string {
	return retrieval.IndexBackendLexical
}

func (lexicalRetrievalBackend) IndexVectorStoreFile(context.Context, *sql.Tx, indexVectorStoreFileParams) error {
	return nil
}

func (lexicalRetrievalBackend) SearchVectorStore(ctx context.Context, store *Store, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error) {
	return store.searchVectorStoreLexical(ctx, query)
}

func normalizeOpenOptions(options OpenOptions) (OpenOptions, error) {
	cfg, err := retrieval.NormalizeConfig(options.Retrieval)
	if err != nil {
		return OpenOptions{}, err
	}
	options.Retrieval = cfg
	return options, nil
}

func newRetrievalBackend(cfg retrieval.Config) (retrievalBackend, error) {
	switch cfg.IndexBackend {
	case retrieval.IndexBackendLexical:
		return lexicalRetrievalBackend{}, nil
	case retrieval.IndexBackendSQLiteVec:
		return nil, fmt.Errorf("retrieval index backend %q is not implemented yet", cfg.IndexBackend)
	default:
		return nil, fmt.Errorf("unsupported retrieval index backend %q", cfg.IndexBackend)
	}
}
