CREATE TABLE IF NOT EXISTS chat_completions (
    id TEXT PRIMARY KEY,
    model TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    request_json TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_completions_model_created_at ON chat_completions(model, created_at);
CREATE INDEX IF NOT EXISTS idx_chat_completions_created_at ON chat_completions(created_at);
