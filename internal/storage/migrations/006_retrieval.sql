CREATE TABLE IF NOT EXISTS files (
    id TEXT PRIMARY KEY,
    purpose TEXT NOT NULL,
    filename TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    status TEXT,
    status_details TEXT,
    content BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_files_created_at ON files(created_at);
CREATE INDEX IF NOT EXISTS idx_files_purpose_created_at ON files(purpose, created_at);

CREATE TABLE IF NOT EXISTS vector_stores (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_active_at INTEGER NOT NULL,
    expires_after_anchor TEXT,
    expires_after_days INTEGER,
    expires_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_vector_stores_created_at ON vector_stores(created_at);

CREATE TABLE IF NOT EXISTS vector_store_files (
    vector_store_id TEXT NOT NULL REFERENCES vector_stores(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    status TEXT NOT NULL,
    usage_bytes INTEGER NOT NULL,
    last_error_json TEXT,
    attributes_json TEXT NOT NULL,
    chunking_strategy_json TEXT NOT NULL,
    PRIMARY KEY (vector_store_id, file_id)
);

CREATE INDEX IF NOT EXISTS idx_vector_store_files_store_created_at
    ON vector_store_files(vector_store_id, created_at);
CREATE INDEX IF NOT EXISTS idx_vector_store_files_store_status_created_at
    ON vector_store_files(vector_store_id, status, created_at);

CREATE TABLE IF NOT EXISTS vector_store_chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vector_store_id TEXT NOT NULL REFERENCES vector_stores(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    token_count INTEGER NOT NULL,
    UNIQUE(vector_store_id, file_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_vector_store_chunks_store_file
    ON vector_store_chunks(vector_store_id, file_id);
