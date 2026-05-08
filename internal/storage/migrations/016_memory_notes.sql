CREATE TABLE IF NOT EXISTS memory_notes (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    source_response_id TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memory_notes_scope_session_updated
    ON memory_notes(scope, session_id, updated_at, id);

CREATE INDEX IF NOT EXISTS idx_memory_notes_source_response
    ON memory_notes(source_response_id);
