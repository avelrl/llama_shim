CREATE TABLE IF NOT EXISTS vector_store_semantic_index_meta (
    vector_store_id TEXT PRIMARY KEY REFERENCES vector_stores(id) ON DELETE CASCADE,
    embedding_model TEXT NOT NULL,
    embedding_dimensions INTEGER NOT NULL,
    chunk_count INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
