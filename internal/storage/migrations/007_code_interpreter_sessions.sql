CREATE TABLE IF NOT EXISTS code_interpreter_sessions (
    id TEXT PRIMARY KEY,
    backend TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_active_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_code_interpreter_sessions_last_active_at
    ON code_interpreter_sessions(last_active_at);
