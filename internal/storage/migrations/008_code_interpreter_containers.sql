ALTER TABLE code_interpreter_sessions
    ADD COLUMN status TEXT NOT NULL DEFAULT 'running';

ALTER TABLE code_interpreter_sessions
    ADD COLUMN name TEXT NOT NULL DEFAULT '';

ALTER TABLE code_interpreter_sessions
    ADD COLUMN memory_limit TEXT NOT NULL DEFAULT '1g';

ALTER TABLE code_interpreter_sessions
    ADD COLUMN expires_after_minutes INTEGER NOT NULL DEFAULT 20;

CREATE TABLE IF NOT EXISTS code_interpreter_container_files (
    id TEXT PRIMARY KEY,
    container_id TEXT NOT NULL,
    backing_file_id TEXT NOT NULL,
    path TEXT NOT NULL,
    source TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY(container_id) REFERENCES code_interpreter_sessions(id) ON DELETE CASCADE,
    FOREIGN KEY(backing_file_id) REFERENCES files(id) ON DELETE CASCADE,
    UNIQUE(container_id, path)
);

CREATE INDEX IF NOT EXISTS idx_code_interpreter_container_files_container_created
    ON code_interpreter_container_files(container_id, created_at, id);
