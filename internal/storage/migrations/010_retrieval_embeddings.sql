CREATE TABLE IF NOT EXISTS vector_store_chunk_embeddings (
    chunk_id INTEGER PRIMARY KEY REFERENCES vector_store_chunks(id) ON DELETE CASCADE,
    embedding BLOB NOT NULL,
    embedding_model TEXT NOT NULL,
    embedding_dimensions INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_vector_store_chunk_embeddings_model
    ON vector_store_chunk_embeddings(embedding_model);
