ALTER TABLE code_interpreter_sessions
    ADD COLUMN owner TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_code_interpreter_sessions_owner_created
    ON code_interpreter_sessions(owner, created_at, id);
